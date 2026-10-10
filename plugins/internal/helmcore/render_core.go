// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

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
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/releaseutil"
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

// ChartRender is what one render of a chart produced: the flat files it emits,
// keyed by output filename, and the advisories the render raised about what it
// emitted, in the order the render met what each one is about. Advisories never
// change a file.
type ChartRender struct {
	Files      map[string][]byte
	Advisories []renderctx.Diagnostic
}

// RenderLoadedChart is [RenderChart] for a caller that reads the files alone: it
// returns the files that render emits and not the advisories it raised.
func RenderLoadedChart(
	ctx context.Context,
	ch *chart.Chart,
	values map[string]any,
	nodeCtx rendercore.NodeRenderContext,
	validate ValuesValidator,
) (map[string][]byte, error) {
	rendered, err := RenderChart(ctx, ch, values, nodeCtx, validate)
	if err != nil {
		return nil, err
	}
	return rendered.Files, nil
}

// RenderChart renders an already-loaded Helm chart against the Phase B values
// dictionary using the Helm Go SDK's template engine, returning flat rendered
// YAML files and the advisories the render raised. Each rendered template is
// split into its individual YAML documents and every document is emitted as its
// own file keyed by "<component-id>-<kind>[-<namespace>]-<name>.yaml"
// (kebab-normalized). Keying by resource identity rather than template basename
// keeps every rendered resource distinct: two templates in different
// subdirectories that share a basename no longer collapse onto one output slot. A
// document that carries no resource identity — a generateName resource, a List
// wrapper — is keyed instead by a deterministic kebab of its rendered template
// path, so it still emits a stable, distinct file. A chart's Helm-native crds/
// directory is emitted alongside the templated resources: Helm installs those CRD
// files verbatim rather than through the template engine, so they are split,
// labeled, and keyed by resource identity exactly like templated documents, in a
// deterministic order. It is the filesystem-free render core: it receives the
// chart in memory and never reads from disk, which is what lets the provider Wasm
// guest — which reconstructs the chart from the payload's source projection —
// drive this one byte-identical-critical render path. Do not re-implement this
// logic in a caller: route through this function so a node's rendered bytes have
// a single producer.
//
// Where the node's Release declares a namespace, every namespaced resource the
// chart renders without one is written into it before its identity is derived, as
// a Helm install with that release namespace places it — see [namespacePlacement]
// for which resources that reaches and which it leaves, and for the one advisory
// it raises.
//
// validate is the values-schema validation strategy the caller supplies; it
// gates on the coalesced values table without altering it, so rendered output
// is byte-identical for any accepted input. The provider Wasm guest passes the
// hermetic validator (see NewHermeticValuesValidator), which skips the SDK's
// live-fetching schema validation.
func RenderChart(
	_ context.Context,
	ch *chart.Chart,
	values map[string]any,
	nodeCtx rendercore.NodeRenderContext,
	validate ValuesValidator,
) (ChartRender, error) {
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
	// reconstructs it per node), so the mutation is not an aliasing or
	// determinism hazard across nodes.
	if err := chartutil.ProcessDependenciesWithMerge(ch, chartutil.Values(values)); err != nil {
		return ChartRender{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: process dependencies", nodeCtx.NodeID))
	}

	// Coalesce the Phase B dict with the chart's default values
	cvals, err := chartutil.CoalesceValues(ch, chartutil.Values(values))
	if err != nil {
		return ChartRender{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: coalesce values", nodeCtx.NodeID))
	}

	// Build release options. We are not installing — just rendering templates.
	// Namespace comes from the `aggrega.ai/namespace` annotation when present
	// (merged from boundary + boundary_component); otherwise the cluster default.
	// The fallback is resolved by EffectiveNamespace rather than written out here,
	// so the namespace this render targets and the namespace an emitted resource's
	// name is declared under cannot disagree.
	ns := EffectiveNamespace(nodeCtx)
	relOpts := chartutil.ReleaseOptions{
		Name:      releaseName(nodeCtx),
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
		return ChartRender{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: render values", nodeCtx.NodeID))
	}

	// Render
	rendered, err := engine.Engine{}.Render(ch, renderVals)
	if err != nil {
		return ChartRender{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: render", nodeCtx.NodeID))
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
	// The file-key prefix is the target-name basis, not the raw NodeID: a
	// boundary-scope FR's synthetic NodeID carries marker syntax
	// (`<boundary-fr>:<inst>@<boundary>`) whose `<>:@` are illegal in filenames on
	// some platforms, so that node carries the basis explicitly.
	// TargetNameBasis is the raw NodeID everywhere else — for an ordinary
	// component and for a per-component FR attachment alike, so both keep the key
	// they already emitted.
	//
	// It is the same value the release name above is derived from, and that
	// coupling is deliberate: the two are one name written twice — here as
	// authored, there through an injective encoding — so two nodes land on one
	// release name exactly when they land on one prefix. Deriving the release name
	// from a second value would put a lossy name beside a lossless one, and the
	// lossless one would keep two nodes' output apart while the lossy one silently
	// gave them a single in-cluster identity.
	//
	// What it is NOT is the component identity these documents are LABELLED with.
	// Both names here belong to the author's chart — the release name its
	// templates read, the prefix on the files it produced — while the label says
	// which component the resources belong to. For a Feature attachment
	// delivering a chart its author supplied the two part: the label carries the
	// attachment's own composed identity, and these two stay the host's.
	prefix := rendercore.TargetNameBasis(nodeCtx)
	keys := make([]string, 0, len(rendered))
	for k := range rendered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Every source the chart emits is split into its documents before any document
	// is emitted, because whether a custom kind is namespaced can be declared by a
	// CustomResourceDefinition anywhere in the render — in a template that sorts
	// after the resource, or in crds/, which is emitted last. A source that fails to
	// split ends the splitting there, and its failure is returned when the emission
	// below reaches it, so a render failing in two places reports the one it
	// reported before the documents were read ahead.
	type chartSource struct {
		path string
		docs []renderedDocument
		err  error
	}
	var sources []chartSource
	splitFailed := false
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
			sources = append(sources, chartSource{path: path, err: cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: template %q: %s", nodeCtx.NodeID, path, err.Error()))})
			splitFailed = true
			break
		}
		sources = append(sources, chartSource{path: path, docs: docs})
	}

	// The chart's Helm-native crds/ directory. CRDs shipped there — rather than
	// under templates/ — are not rendered through the template engine; Helm
	// installs them verbatim. They are split, labeled, and keyed by resource
	// identity exactly like templated resources, after every template. CRDs are
	// processed in Filename order so the output is deterministic run-to-run.
	if !splitFailed {
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
				sources = append(sources, chartSource{path: crd.Filename, err: cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: crd %q: %s", nodeCtx.NodeID, crd.Filename, err.Error()))})
				break
			}
			sources = append(sources, chartSource{path: crd.Filename, docs: docs})
		}
	}

	split := make([][]renderedDocument, 0, len(sources))
	for _, s := range sources {
		split = append(split, s.docs)
	}
	// The namespace placement writes is the declared one, which is also what the
	// chart rendered against as its release namespace (see EffectiveNamespace); with
	// none declared it places nothing, rather than writing the cluster default.
	placement := newNamespacePlacement(rendercore.NamespaceFromAnnotations(nodeCtx.Annotations), split, nodeCtx.YAMLIndent)

	// emitDocuments places, labels, annotates, keys, and stores each split document
	// from one source (a rendered template or a chart CRD file). sourcePath names the
	// origin: it seeds the deterministic fallback filename for identity-less
	// documents and names the source in a collision halt. Templates and CRD files
	// share this one emission path so both surfaces are placed, derive filenames,
	// inject provenance, and detect collisions identically.
	emitDocuments := func(docs []renderedDocument, sourcePath string) error {
		// Placement comes first, because the namespace a document writes is part of
		// the identity its filename is derived from: a placed resource is named
		// exactly as one whose chart writes the namespace itself.
		for i := range docs {
			if err := placement.place(&docs[i]); err != nil {
				return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: place a resource of %q in namespace %q", nodeCtx.NodeID, sourcePath, placement.namespace))
			}
		}

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
				return cerrors.Wrap(wasm.ErrPluginOutputNameCollision(), cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: rendered resources %q and %q both derive output %q with differing content: two rendered resources derive the same output filename with differing content", nodeCtx.NodeID, keyOrigin[outKey], sourcePath, outKey))
			}
			out[outKey] = annotated
			keyOrigin[outKey] = sourcePath
		}
		return nil
	}

	for _, s := range sources {
		if s.err != nil {
			return ChartRender{}, s.err
		}
		if err := emitDocuments(s.docs, s.path); err != nil {
			return ChartRender{}, err
		}
	}

	return ChartRender{Files: out, Advisories: placement.advisories}, nil
}

// renderedDocument is one YAML document split out of a rendered template: its
// re-encoded bytes plus the resource identity (kind, namespace, name) used to
// derive a stable output filename, and the parsed document those bytes encode,
// which placing the document in a namespace edits and encodes again.
type renderedDocument struct {
	content   []byte
	kind      string
	namespace string
	name      string
	doc       *yaml.Node
}

// root is the document's root mapping.
func (d renderedDocument) root() *yaml.Node {
	if d.doc == nil || len(d.doc.Content) == 0 {
		return nil
	}
	return d.doc.Content[0]
}

// splitRenderedDocuments splits a rendered template's YAML stream into its
// individual documents, re-encoding each at the render indent and extracting the
// resource identity fields. The stream is first normalized into per-document
// substrings on the "---" directives-end marker, tolerating a comment or other
// non-whitespace glued directly onto the marker with no separating whitespace
// (for example "---# a comment", which whitespace-chomping template control can
// produce): the marker splits and the glued text opens the next document. The
// split preserves document order so the emitted output is byte-identical
// run-to-run. Empty and null documents are dropped — a whitespace- or
// comment-only body between "---" separators, and a document whose root
// deserializes to null (a bare "---" left by a false conditional, an explicit
// "null", or "~"), carry no resource, so they are skipped rather than named,
// matching the behavior of rendering the same chart with the Helm CLI. A
// mapping-root document that lacks a kind or a metadata.name (a generateName
// resource, a List wrapper) is returned with empty identity fields; the caller
// derives a deterministic path-based filename for it. A document whose root is a
// non-null, non-mapping value (a bare scalar or a sequence) is not a labelable
// resource and halts, since no resource-shaped output can be produced from it.
func splitRenderedDocuments(streamBytes []byte, indent int) ([]renderedDocument, error) {
	manifests := releaseutil.SplitManifests(string(streamBytes))
	keys := make([]string, 0, len(manifests))
	for k := range manifests {
		keys = append(keys, k)
	}
	// SplitManifests returns an unordered map keyed "manifest-N"; sort by the
	// integer suffix to restore document order and keep the output byte-identical
	// run-to-run.
	sort.Sort(releaseutil.BySplitManifestsOrder(keys))

	var out []renderedDocument
	for _, k := range keys {
		dec := yaml.NewDecoder(strings.NewReader(manifests[k]))
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				continue
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

		content, err := encodeRenderedDocument(&doc, indent)
		if err != nil {
			return nil, err
		}

		out = append(out, renderedDocument{content: content, kind: kind, namespace: namespace, name: name, doc: &doc})
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
