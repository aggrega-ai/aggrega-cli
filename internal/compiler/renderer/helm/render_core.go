// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// ValuesValidator is a values-schema validation strategy for
// RenderLoadedChart. It receives the chart and the fully coalesced values the
// render consumes and returns an error when the values violate the chart's
// values.schema.json contract (including dependency charts' schemas).
// Validation is a gate, not a transform: a validator never alters the values,
// so the rendered bytes are identical across validation strategies — only
// acceptance can differ.
//
// Callers that must render hermetically — the provider Wasm guest, whose
// sandbox grants no ambient network — pass an explicit validator (see
// NewHermeticValuesValidator) so no remote schema $ref is fetched at render
// time.
type ValuesValidator func(ch *chart.Chart, vals chartutil.Values) error

// RenderLoadedChart renders an already-loaded Helm chart against the Phase B
// values dictionary using the Helm Go SDK's template engine, returning flat
// rendered YAML files. Each rendered template is split into its individual YAML
// documents and every document is emitted as its own file keyed by
// "<component-id>-<kind>[-<namespace>]-<name>.yaml" (kebab-normalized). Keying by
// resource identity rather than template basename keeps every rendered resource
// distinct: two templates in different subdirectories that share a basename no
// longer collapse onto one output slot. A document that carries no resource
// identity — a generateName resource, a List wrapper — is keyed instead by a
// deterministic kebab of its rendered template path, so it still emits a stable,
// distinct file. A chart's Helm-native crds/ directory is emitted alongside the
// templated resources: Helm installs those CRD files verbatim rather than through
// the template engine, so they are split, labeled, and keyed by resource identity
// exactly like templated documents, in a deterministic order. It is the
// filesystem-free render core: it receives the chart in memory and never reads
// from disk, so both the in-tree host engine (which loads the chart from a path)
// and the provider Wasm guest (which reconstructs the chart from the payload's
// source projection) share this one byte-identical-critical render path. Do not
// re-implement this logic in a caller — route through this function so the two
// render surfaces stay byte-for-byte identical.
//
// validate is the values-schema validation strategy the caller supplies; it
// gates on the coalesced values table without altering it, so rendered output
// is byte-identical for any accepted input. The provider Wasm guest passes the
// hermetic validator (see NewHermeticValuesValidator), which skips the SDK's
// live-fetching schema validation.
func RenderLoadedChart(
	_ context.Context,
	ch *chart.Chart,
	values map[string]any,
	nodeCtx rendercore.NodeRenderContext,
	validate ValuesValidator,
) (map[string][]byte, error) {
	// Prune condition:/tags:-disabled dependency subcharts before rendering,
	// matching the Helm CLI: a dependency whose condition path resolves to
	// false in the merged values is removed from the chart tree so its
	// templates never render, and dependency import-values are merged. The
	// values object drives condition evaluation and carries the Phase B
	// injected values, so an injected condition (e.g. an injected
	// `<subchart>.enabled: false`) prunes its subchart exactly as the Helm CLI
	// would — not merely the chart's own defaults. This runs before coalescing
	// and before building the render values, mirroring the CLI's render order.
	// It mutates ch; the chart is freshly loaded per render (the provider guest
	// reconstructs it per node, the in-tree host loads it per node), so the
	// mutation is not an aliasing or determinism hazard across nodes.
	if err := chartutil.ProcessDependenciesWithMerge(ch, chartutil.Values(values)); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: process dependencies", nodeCtx.NodeID))
	}

	// Coalesce the Phase B dict with the chart's default values
	cvals, err := chartutil.CoalesceValues(ch, chartutil.Values(values))
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: coalesce values", nodeCtx.NodeID))
	}

	// Build release options. We are not installing — just rendering templates.
	// Namespace comes from the `aggrega.ai/namespace` annotation when present
	// (merged from boundary + boundary_component); otherwise "default".
	ns := rendercore.NamespaceFromAnnotations(nodeCtx.Annotations)
	if ns == "" {
		ns = "default"
	}
	relOpts := chartutil.ReleaseOptions{
		Name:      sanitizeContainerName(nodeCtx.NodeID),
		Namespace: ns,
		Revision:  1,
		IsInstall: true,
	}
	caps := chartutil.DefaultCapabilities

	// Skip the SDK's live-fetching validator and gate on the supplied one
	// instead. The table handed to the validator is the same coalesced values
	// table the render consumes.
	renderVals, err := chartutil.ToRenderValuesWithSchemaValidation(ch, cvals, relOpts, caps, true)
	if err == nil {
		if vals, ok := renderVals["Values"].(chartutil.Values); ok {
			err = validate(ch, vals)
		} else {
			err = fmt.Errorf("render values carry no coalesced values table to validate")
		}
	}
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: render values", nodeCtx.NodeID))
	}

	// Render
	rendered, err := engine.Engine{}.Render(ch, renderVals)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: render", nodeCtx.NodeID))
	}

	// rendered is map[path]string. Path format: "<chart>/templates/<file>.yaml".
	// Each rendered template is split into its individual YAML documents and each
	// document is emitted as its own file, keyed by resource identity rather than
	// template basename so same-basename resources in different subdirectories
	// stay distinct. Each rendered document is post-processed to merge
	// aggrega.ai/* provenance labels into metadata.labels so sourced mode matches
	// sourceless-mode's labeling.
	aggregaLabels := buildAggregaLabels(nodeCtx)
	aggregaAnnotations := buildAggregaAnnotations(nodeCtx)
	hookWeight := helmHookWeight(nodeCtx)
	out := make(map[string][]byte, len(rendered))
	// keyOrigin maps each emitted output key back to the rendered template that
	// produced it, so a distinct-content collision can name both source templates.
	keyOrigin := make(map[string]string, len(rendered))
	// The file-key prefix uses the sanitised component id, not the raw NodeID: a
	// boundary-scope FR's synthetic NodeID carries marker syntax
	// (`<boundary-fr>:<inst>@<boundary>`) whose `<>:@` are illegal in filenames on
	// some platforms. ProvenanceComponentID is the raw NodeID for ordinary
	// components (byte-identical key) and the sanitised LabelComponentID for
	// FR-emitted nodes.
	prefix := rendercore.ProvenanceComponentID(nodeCtx)
	keys := make([]string, 0, len(rendered))
	for k := range rendered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// emitDocuments labels, annotates, keys, and stores each split document from
	// one source (a rendered template or a chart CRD file). sourcePath names the
	// origin: it seeds the deterministic fallback filename for identity-less
	// documents and names the source in a collision halt. Templates and CRD files
	// share this one emission path so both surfaces derive filenames, inject
	// provenance, and detect collisions identically.
	emitDocuments := func(docs []renderedDocument, sourcePath string) error {
		// A single source may render the same identity more than once (e.g. a
		// range emitting duplicate names). Count identities within this source so
		// those occurrences take a stable document-index tiebreaker; unique
		// identities stay tiebreaker-free.
		idCount := make(map[string]int, len(docs))
		ids := make([]string, len(docs))
		for i := range docs {
			id := deriveResourceIdentity(docs[i])
			if id == "" {
				// A rendered document without a kind or a metadata.name — a
				// generateName resource, a List wrapper — has no resource
				// identity to name a file by. Fall back to a deterministic id
				// derived from the source path so the filename stays stable
				// run-to-run; the idCount tiebreaker below disambiguates multiple
				// such documents rendered from one source.
				id = pathFallbackIdentity(sourcePath)
			}
			ids[i] = id
			idCount[id]++
		}

		for i := range docs {
			id := ids[i]
			if idCount[id] > 1 {
				id = id + "-" + strconv.Itoa(i)
			}
			labeled, err := injectAggregaLabels(docs[i].content, aggregaLabels, nodeCtx.YAMLIndent)
			if err != nil {
				return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: inject labels into %q", nodeCtx.NodeID, sourcePath))
			}
			annotated, err := injectAggregaAnnotations(labeled, aggregaAnnotations, hookWeight, nodeCtx.YAMLIndent)
			if err != nil {
				return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: inject annotations into %q", nodeCtx.NodeID, sourcePath))
			}
			outKey := prefix + "-" + id + ".yaml"
			if prior, exists := out[outKey]; exists {
				if bytes.Equal(prior, annotated) {
					// Byte-identical duplicate: keeping one loses nothing.
					continue
				}
				return cerrors.Wrap(ErrRenderedFileCollision, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: rendered resources %q and %q both derive output %q with differing content", nodeCtx.NodeID, keyOrigin[outKey], sourcePath, outKey))
			}
			out[outKey] = annotated
			keyOrigin[outKey] = sourcePath
		}
		return nil
	}

	for _, path := range keys {
		content := strings.TrimSpace(rendered[path])
		if content == "" {
			continue
		}
		base := filepath.Base(path)
		// Drop partial-template files (Helm convention: underscore prefix)
		if strings.HasPrefix(base, "_") {
			continue
		}
		// Drop non-manifest rendered output: Helm renders NOTES.txt (plain-text
		// usage notes) and may emit other non-YAML files alongside the manifests.
		// Only YAML/JSON documents are Kubernetes objects to label and emit.
		switch strings.ToLower(filepath.Ext(base)) {
		case ".yaml", ".yml", ".json":
		default:
			continue
		}

		docs, err := splitRenderedDocuments([]byte(content), nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: template %q: %s", nodeCtx.NodeID, path, err.Error()))
		}
		if err := emitDocuments(docs, path); err != nil {
			return nil, err
		}
	}

	// Emit the chart's Helm-native crds/ directory. CRDs shipped there — rather
	// than under templates/ — are not rendered through the template engine; Helm
	// installs them verbatim. They are split, labeled, and keyed by resource
	// identity exactly like templated resources. CRDs are processed in Filename
	// order so the output is deterministic run-to-run.
	crds := ch.CRDObjects()
	crdOrder := make([]int, len(crds))
	for i := range crds {
		crdOrder[i] = i
	}
	sort.Slice(crdOrder, func(a, b int) bool {
		return crds[crdOrder[a]].Filename < crds[crdOrder[b]].Filename
	})
	for _, idx := range crdOrder {
		crd := crds[idx]
		if crd.File == nil || len(crd.File.Data) == 0 {
			continue
		}
		docs, err := splitRenderedDocuments(crd.File.Data, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: crd %q: %s", nodeCtx.NodeID, crd.Filename, err.Error()))
		}
		if err := emitDocuments(docs, crd.Filename); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// renderedDocument is one YAML document split out of a rendered template: its
// re-encoded bytes plus the resource identity (kind, namespace, name) used to
// derive a stable output filename.
type renderedDocument struct {
	content   []byte
	kind      string
	namespace string
	name      string
}

// splitRenderedDocuments decodes a rendered template's YAML stream into its
// individual documents, re-encoding each at the render indent and extracting the
// resource identity fields. Empty and null documents are dropped — a whitespace-
// or comment-only body between "---" separators, and a document whose root
// deserializes to null (a bare "---" left by a false conditional, an explicit
// "null", or "~"), carry no resource, so they are skipped rather than named,
// matching the behavior of rendering the same chart with the Helm CLI. A
// mapping-root document that lacks a kind or a metadata.name (a generateName
// resource, a List wrapper) is returned with empty identity fields; the caller
// derives a deterministic path-based filename for it. A document whose root is a
// non-null, non-mapping value (a bare scalar or a sequence) is not a labelable
// resource and halts, since no resource-shaped output can be produced from it.
func splitRenderedDocuments(streamBytes []byte, indent int) ([]renderedDocument, error) {
	dec := yaml.NewDecoder(bytes.NewReader(streamBytes))
	var out []renderedDocument
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse rendered YAML: %w", err)
		}
		if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
			continue
		}
		root := doc.Content[0]
		if root == nil || root.Tag == "!!null" {
			continue
		}
		if root.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("rendered document is not a resource mapping; cannot derive an output filename")
		}
		kind := scalarChildValue(root, "kind")
		metadata := findMapChild(root, "metadata")
		name := scalarChildValue(metadata, "name")
		namespace := scalarChildValue(metadata, "namespace")

		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(rendercore.ClampYAMLIndent(indent))
		if err := enc.Encode(&doc); err != nil {
			return nil, fmt.Errorf("encode rendered YAML: %w", err)
		}
		_ = enc.Close()

		out = append(out, renderedDocument{content: buf.Bytes(), kind: kind, namespace: namespace, name: name})
	}
	return out, nil
}

// scalarChildValue returns the scalar value mapped from key in a MappingNode, or
// "" when absent or non-scalar. A nil node yields "".
func scalarChildValue(m *yaml.Node, key string) string {
	v := findMapChild(m, key)
	if v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

// deriveResourceIdentity builds the identity portion of an output filename from a
// resource's kind, namespace, and name, kebab-normalized and joined with "-".
// The namespace segment is omitted for cluster-scoped resources (empty
// namespace). A document that lacks a kind or a metadata.name has no derivable
// resource identity; deriveResourceIdentity returns "" for it and the caller
// substitutes a deterministic path-derived fallback.
func deriveResourceIdentity(d renderedDocument) string {
	if d.kind == "" || d.name == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	parts = append(parts, kebabIdentityPart(d.kind))
	if ns := kebabIdentityPart(d.namespace); ns != "" {
		parts = append(parts, ns)
	}
	parts = append(parts, kebabIdentityPart(d.name))
	return strings.Join(parts, "-")
}

// pathFallbackIdentity derives a deterministic identity part from a rendered
// template path with its file extension stripped, kebab-normalized. It names the
// output file for a document that carries no derivable resource identity, keeping
// the emitted filename stable run-to-run.
func pathFallbackIdentity(path string) string {
	return kebabIdentityPart(strings.TrimSuffix(path, filepath.Ext(path)))
}

// kebabIdentityPart lowercases s and collapses every run of characters outside
// [a-z0-9] to a single "-", trimming leading and trailing dashes. The mapping is
// deterministic so the derived filename is stable run-to-run.
func kebabIdentityPart(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		} else {
			pendingDash = true
		}
	}
	return b.String()
}
