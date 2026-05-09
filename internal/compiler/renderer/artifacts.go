// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Phase B.3 artifacts evaluation per chapter 16's three-input-kinds model.
// Inline `artifacts:` (OrderedMap filename → template) and file-loaded
// `artifactsFrom:` (list of paths) both evaluate against the full
// TemplateContext including .workload and .source, get provenance labels
// merged into K8s-style metadata.labels, and emit as EmittedFile entries
// tagged with their origin so the Phase B.5 collision detector can name
// every contributor on a cross-origin clash.

package renderer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ExternalArtifactsHandler is the marker interface implemented by Engines
// whose provider model handles artifacts outside Phase B's standard
// emission path. Today: the helm-chart engine, whose `artifactsFrom:`
// files are merged into the chart's `templates/` directory rather than
// emitted as standalone files. Engines satisfying this marker reject
// inline `artifacts:` at Phase B.3 (the chart structure cannot accept
// inline declarations).
//
// Optional companion capabilities. Engines that opt into external
// dispatch tune the per-artifact placement and the template delimiter
// pair through two independent capability interfaces:
//
//   - ArtifactsPathProvider — engine declares the per-node-relative path
//     each emitted artifactsFrom file lands at (e.g., helm-chart's
//     "templates/<basename>"). Engines that don't implement this interface
//     emit at the bare basename, matching the standard non-external
//     emission shape.
//
//   - ArtifactsTemplateDelims — engine declares the Go-template delimiter
//     pair Aggrega's pre-templating uses on each artifactsFrom file
//     (e.g., helm-chart's "[[", "]]" so Helm's "{{ ... }}" survives into
//     the downstream render). Engines that don't implement this
//     interface use the default "{{", "}}" delimiters.
//
// Dispatch parity. Phase B.3 dispatches by marker assertion in the same
// shape as the BoundaryAssembler dispatch. The two companion capabilities
// follow the same pattern (independent type assertions with default
// fallbacks) used by WorkloadSynthesizer / BoundaryAssembler elsewhere
// in the package.
type ExternalArtifactsHandler interface {
	HandlesArtifactsExternally()
}

// ArtifactsPathProvider declares per-artifact path placement for engines
// satisfying ExternalArtifactsHandler. The path the method returns is
// per-node-relative; the orchestrator's per-(path, provider) nesting layers
// the dispatch path's prefix and `charts/<nodeID>/` on top for
// BoundaryAssembler engines. Engines that don't implement this interface
// fall back to the bare basename — matching the standard non-external
// emission shape — when the artifacts evaluator dispatches an artifactsFrom
// file.
type ArtifactsPathProvider interface {
	PathForArtifact(nodeCtx NodeRenderContext, basename string) string
}

// ArtifactsTemplateDelims declares the Go-template delimiter pair used
// when Aggrega's pre-templating evaluates artifactsFrom files for an
// ExternalArtifactsHandler engine. Engines whose provider model runs a
// downstream templating pass with the same default `{{ }}` delimiters
// return alternates here so Aggrega's pass leaves the downstream syntax
// untouched. helm-chart returns "[[", "]]" so Helm's `{{ ... }}` blocks
// survive into the chart's render pipeline.
//
// Engines that don't implement this interface fall back to the default
// "{{", "}}" pair (text/template default).
type ArtifactsTemplateDelims interface {
	ArtifactDelimiters() (left, right string)
}

// evaluateArtifacts walks Recipe.Spec.Artifacts (inline) followed by
// Recipe.Spec.ArtifactsFrom (file-loaded), evaluates each entry as a Go
// template against tmplCtx, parses the output as YAML, merges the canonical
// provenance label set into metadata.labels (when the document carries
// metadata), and returns the emitted-file slice for the caller to append to
// state.EmittedFiles. Each emission carries the matching EmitOrigin tag so
// Phase B.5 cross-origin collision detection can name every contributor.
//
// Per-provider dispatch:
//   - helm / terraform / cloudformation / future flat providers — emissions
//     land as standalone files under the boundary output directory
//     (Intermediate=false).
//   - compose — emissions are tagged Intermediate=true so the orchestrator
//     routes them to ComposeRelease's parseNodeFragment via ReleaseInfo,
//     mirroring how engine.Render output is routed for ReleaseComposer
//     engines today.
//   - helm-chart — inline `artifacts:` is rejected with a CATALOG_ERROR
//     because Helm owns the chart's templates/ directory and cannot accept
//     inline-only artifacts. `artifactsFrom:` is a no-op at B.3; the
//     helm-chart provider's special pre-template injection path is the
//     responsibility of a follow-up work item, not B.3.
//
// Self-containment: the chapter requires artifacts to reference only the
// Recipe's declared inputs (pillars, .workload, .source). Cross-Recipe
// resource references are not compile-time checked — runtime apply catches
// them. The constraint is documented here on the evaluator; a future
// iteration may add a static checker.
func evaluateArtifacts(
	_ context.Context,
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	engine Engine,
	nodeCtx NodeRenderContext,
) ([]EmittedFile, error) {
	if state == nil || state.Recipe == nil {
		return nil, nil
	}
	hasInline := state.Recipe.Spec.Artifacts.Len() > 0
	hasFromFile := len(state.Recipe.Spec.ArtifactsFrom) > 0
	if !hasInline && !hasFromFile {
		return nil, nil
	}

	if _, external := engine.(ExternalArtifactsHandler); external {
		if hasInline {
			return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath},
				fmt.Sprintf("recipe %q (%q): provider %q does not support inline artifacts:; declare artifactsFrom: instead so the files can be injected into the chart's templates/ directory",
					state.RecipeName, state.RecipePath, state.Recipe.Spec.Provider),
				nil, nil)
		}
		// External-handler engines: evaluate each artifactsFrom file with
		// the engine's chosen template-delimiter pair, place each emission
		// at the engine-provided path, and feed every emission into
		// state.EmittedFiles for downstream collision detection (Phase B.5)
		// and per-node grouping. The two capability interfaces
		// (ArtifactsPathProvider, ArtifactsTemplateDelims) drive per-engine
		// behaviour; defaults apply when an engine does not implement them.
		leftDelim, rightDelim := "{{", "}}"
		if d, ok := engine.(ArtifactsTemplateDelims); ok {
			leftDelim, rightDelim = d.ArtifactDelimiters()
		}
		pathFn := func(_ NodeRenderContext, basename string) string { return basename }
		if p, ok := engine.(ArtifactsPathProvider); ok {
			pathFn = p.PathForArtifact
		}
		var out []EmittedFile
		for _, path := range state.Recipe.Spec.ArtifactsFrom {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("recipe %q artifactsFrom %q", state.RecipeName, path))
			}
			basename := filepath.Base(path)
			body, err := evaluateOneArtifactWithDelims(
				tmplCtx, state, nodeCtx, basename, string(data),
				fmt.Sprintf("artifactsFrom[%s/%s]", state.RecipeName, basename),
				leftDelim, rightDelim,
			)
			if err != nil {
				return nil, err
			}
			out = append(out, EmittedFile{
				Name:         pathFn(nodeCtx, basename),
				Origin:       EmitOriginArtifactsFromFile,
				Body:         body,
				Intermediate: false,
			})
		}
		return out, nil
	}

	// Intermediate-tagged emitted files are consumed by the orchestrator's
	// per-(path, provider) grouping in RenderAll — the `if f.Intermediate`
	// branch routes solely on the flag. The grouping is Origin-agnostic:
	// every Intermediate=true EmittedFile flows to AssembleBoundary
	// regardless of EmitOrigin. End-to-end exercise:
	// TestPhaseB3_ArtifactsIntermediate_RoutesViaReleaseComposer in
	// renderer_integration_test.go is the live anchor — a regression that
	// breaks the routing changes the test outcome.
	_, isAssembler := engine.(BoundaryAssembler)

	var out []EmittedFile

	for _, filename := range state.Recipe.Spec.Artifacts.Keys() {
		tmpl, _ := state.Recipe.Spec.Artifacts.Get(filename)
		body, err := evaluateOneArtifact(tmplCtx, state, nodeCtx, filename, tmpl, fmt.Sprintf("artifacts[%s/%s]", state.RecipeName, filename))
		if err != nil {
			return nil, err
		}
		out = append(out, EmittedFile{
			Name:         filename,
			Origin:       EmitOriginArtifactsInline,
			Body:         body,
			Intermediate: isAssembler,
		})
	}

	for _, path := range state.Recipe.Spec.ArtifactsFrom {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("recipe %q artifactsFrom %q", state.RecipeName, path))
		}
		basename := filepath.Base(path)
		body, err := evaluateOneArtifact(tmplCtx, state, nodeCtx, basename, string(data), fmt.Sprintf("artifactsFrom[%s/%s]", state.RecipeName, basename))
		if err != nil {
			return nil, err
		}
		out = append(out, EmittedFile{
			Name:         basename,
			Origin:       EmitOriginArtifactsFromFile,
			Body:         body,
			Intermediate: isAssembler,
		})
	}

	return out, nil
}

// provenanceSkipWarning is the single user-facing message emitted from
// either of the two skip-paths inside evaluateOneArtifactWithDelims (the
// pre-emptive `{{` substring detection and the post-parse-failure
// fallback). One message regardless of trigger: the user-facing
// remediation is identical (`_helpers.tpl`-side provenance injection),
// so the warning text reads the same and the operator's mental model
// stays clean.
func provenanceSkipWarning(filename string) string {
	return fmt.Sprintf(
		"artifactsFrom %q: skipped provenance label injection (file contains downstream template syntax incompatible with Aggrega's pre-render YAML round-trip); use chart _helpers.tpl for provenance on this file",
		filename,
	)
}

// evaluateOneArtifact is the default-delimiters wrapper used by the
// standard (non-external-handler) emission path. Thin call into
// evaluateOneArtifactWithDelims with the literal "{{", "}}" pair — no
// empty-string sentinel logic.
func evaluateOneArtifact(
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	nodeCtx NodeRenderContext,
	filename string,
	tmplText string,
	tmplName string,
) ([]byte, error) {
	return evaluateOneArtifactWithDelims(tmplCtx, state, nodeCtx, filename, tmplText, tmplName, "{{", "}}")
}

// evaluateOneArtifactWithDelims handles the per-entry pipeline: nil-pillar
// pre-check, Go-template evaluation with the supplied delimiter pair, YAML
// parse, provenance-label merge, YAML re-marshal. Returns the marshalled
// bytes ready to attach to an EmittedFile. tmplName is the template-engine
// identifier surfaced in parse / execute error messages; filename is the
// user-facing artifact name surfaced in CATALOG_ERROR.
//
// YAML parse handling differs by call path:
//
//   - Standard (default delimiters, non-external-handler engines): a parse
//     failure is a CATALOG_ERROR — Aggrega's pre-templating output is
//     authoritative, and a non-YAML result is an authoring bug.
//   - External-handler engines using non-default delimiters (helm-chart's
//     `[[ ]]`): a parse failure is the common case for files mixing Helm
//     `{{ }}` template structure with YAML, and is NOT an error. The
//     evaluator emits the rendered bytes unchanged, skips provenance
//     injection, and appends a state.Warnings entry naming the
//     artifactsFrom file so the build surfaces the gap.
//
// The delimiter pair drives the dispatch — when leftDelim+rightDelim
// differ from the default `{{ }}`, the parse-failure path takes the
// warn-and-emit branch.
func evaluateOneArtifactWithDelims(
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	nodeCtx NodeRenderContext,
	filename string,
	tmplText string,
	tmplName string,
	leftDelim, rightDelim string,
) ([]byte, error) {
	if err := preCheckArtifactPillars(state, filename, tmplText, leftDelim, rightDelim); err != nil {
		return nil, err
	}

	rendered, err := EvalTemplateWithDelims(tmplName, tmplText, tmplCtx, leftDelim, rightDelim)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("artifact %q template eval", filename))
	}

	if strings.TrimSpace(rendered) == "" {
		return []byte(rendered), nil
	}

	usingDefaultDelims := leftDelim == "{{" && rightDelim == "}}"

	// External-handler path (non-default delimiters): the output may carry
	// downstream template syntax — `{{ ... }}` in unquoted positions —
	// that yaml.v3 would either fail to parse OR re-shape destructively
	// during the round-trip (interpreting `{{` as the start of a flow
	// mapping at scalar leaves; quoted-scalar positions like
	// `name: {{ .Values.x }}` re-emerge as `name: '{{ .Values.x }}'` with
	// shifted spacing). Two triggers route to the same outcome — skip
	// provenance injection, emit the rendered bytes unchanged, append
	// state.Warnings via the shared warning shape:
	//
	//   1. Pre-emptive: any `{{` present in the rendered output. Catches
	//      both YAML-parseable-but-round-trip-destructive and
	//      YAML-unparseable cases without first attempting a round-trip
	//      that may corrupt the file's bytes.
	//   2. Post-parse: yaml.Unmarshal fails on a non-default-delimiter
	//      file even after the `{{` substring check (e.g., structural
	//      template syntax that uses other characters). Defense-in-depth.
	//
	// Default-delimiter (non-external-handler) callers continue to treat
	// parse failure as a CATALOG_ERROR — Aggrega's pre-templating output
	// is authoritative for that path.
	if !usingDefaultDelims && strings.Contains(rendered, "{{") {
		state.Warnings = append(state.Warnings, provenanceSkipWarning(filename))
		return []byte(rendered), nil
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(rendered), &node); err != nil {
		if usingDefaultDelims {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("artifact %q template output is not valid YAML", filename))
		}
		state.Warnings = append(state.Warnings, provenanceSkipWarning(filename))
		return []byte(rendered), nil
	}
	if node.Kind == 0 {
		// Empty document (e.g., `---` only) — emit the rendered bytes as-is.
		return []byte(rendered), nil
	}

	labels := buildProvenanceLabels(state, nodeCtx)
	annotations := buildProvenanceAnnotations(state, nodeCtx)
	if err := injectProvenanceNode(filename, &node, labels, annotations); err != nil {
		return nil, err
	}

	body, err := yaml.Marshal(&node)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("artifact %q yaml marshal", filename))
	}
	return body, nil
}

// preCheckArtifactPillars walks the template's parsed field-access set
// (via CollectTemplateFieldAccessesWithDelims) and short-circuits with a
// CATALOG_ERROR when the template references a pillar slot that the
// current Recipe has not declared:
//   - any `.workload.*` reference when state.ParsedWorkload == nil.
//   - any `.source.*` reference when state.Recipe.Spec.Source == nil.
//
// The pre-check names the failing expression authoritatively (the literal
// dot-path captured by the parser) and avoids depending on text/template's
// runtime nil-pointer error formatting.
//
// leftDelim/rightDelim are the same delimiter pair the eventual
// EvalTemplateWithDelims call will use, so the parser sees Aggrega's
// templating exactly as the renderer will execute it. For external-handler
// engines using non-default delimiters (helm-chart's `[[ ]]`), Helm's
// `{{ ... }}` blocks become opaque text and don't surface as parse errors
// or as field-access entries.
func preCheckArtifactPillars(state *PhaseBState, filename, tmplText, leftDelim, rightDelim string) error {
	refs, err := CollectTemplateFieldAccessesWithDelims(tmplText, leftDelim, rightDelim)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("artifact %q template parse", filename))
	}
	workloadDeclared := state.ParsedWorkload != nil
	sourceDeclared := false
	if state.Recipe != nil {
		switch s := state.Recipe.Spec.Source.(type) {
		case nil:
			sourceDeclared = false
		case string:
			sourceDeclared = s != ""
		default:
			sourceDeclared = true
		}
	}

	for _, ref := range refs {
		if !workloadDeclared && (ref == ".workload" || strings.HasPrefix(ref, ".workload.")) {
			return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: filename},
				fmt.Sprintf("artifact %q template references %q but Recipe declares no workload:/workloadFrom:", filename, ref),
				nil, nil)
		}
		if !sourceDeclared && (ref == ".source" || strings.HasPrefix(ref, ".source.")) {
			return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: filename},
				fmt.Sprintf("artifact %q template references %q but Recipe declares no source:", filename, ref),
				nil, nil)
		}
	}
	return nil
}

// injectProvenanceNode merges the canonical provenance-label set into the
// document's metadata.labels block, mutating the supplied *yaml.Node in
// place. yaml.Node-based traversal preserves the user's authored field
// order through the round-trip — keys outside metadata stay where the
// author wrote them; keys inside metadata stay where the author wrote them;
// existing user-authored labels stay where the author wrote them.
//
// Provenance labels APPEND after any existing user-authored labels in
// alphabetical order of the provenance-label key (deterministic for
// byte-equality of output across runs of identical input). When a
// provenance label key collides with a user-authored label, the user-
// authored value is overwritten with the compiler's value at the existing
// position — provenance labels are authoritative on key collision per the
// chapter-16 contract.
//
// When the document does not carry a metadata block (compose service
// fragments, raw config-only fragments) the injector returns nil unchanged
// — silent no-op per chapter 16's "manifests without metadata skip
// silently" contract.
//
// Q1a guardrail: when metadata.labels exists but its kind is not a YAML
// mapping (scalar, sequence, alias, etc.), the function returns a
// CATALOG_ERROR rather than silently discarding the user-authored content.
// Aligns with Invariant 4 — the compiler never silently mutates user
// content into compliance.
func injectProvenanceNode(filename string, root *yaml.Node, labels, annotations map[string]string) error {
	mapping := documentMapping(root)
	if mapping == nil {
		return nil
	}
	metadata := mappingValueByKey(mapping, "metadata")
	if metadata == nil {
		return nil
	}
	metadata = unwrapAlias(metadata)
	if metadata.Kind != yaml.MappingNode {
		return nil
	}

	if err := mergeOrAppendProvenanceMapping(filename, metadata, "labels", labels); err != nil {
		return err
	}
	if err := mergeOrAppendProvenanceMapping(filename, metadata, "annotations", annotations); err != nil {
		return err
	}
	return nil
}

// mergeOrAppendProvenanceMapping merges entries into the named mapping
// child of metadata, creating the child if absent. Same Q1a guardrail as
// the original labels-only path: if the child exists but its kind is not
// a YAML mapping, return CATALOG_ERROR rather than silently discarding
// user content. No-op when entries is empty.
func mergeOrAppendProvenanceMapping(filename string, metadata *yaml.Node, key string, entries map[string]string) error {
	if len(entries) == 0 {
		return nil
	}
	keyNode, valNode := findKeyValueNode(metadata, key)
	if keyNode != nil && valNode != nil {
		valNode = unwrapAlias(valNode)
		if valNode.Kind != yaml.MappingNode {
			return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: filename},
				fmt.Sprintf("artifact %q metadata.%s has unexpected shape (expected mapping, got %s); cannot merge provenance entries",
					filename, key, nodeKindName(valNode.Kind)),
				nil, nil)
		}
		mergeProvenanceIntoLabels(valNode, entries)
		return nil
	}
	newMapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mergeProvenanceIntoLabels(newMapping, entries)
	metadata.Content = append(metadata.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		newMapping,
	)
	return nil
}

// documentMapping returns the mapping node at the document root, or nil
// when the document is empty / not a mapping. Handles both the
// DocumentNode wrapper that yaml.Unmarshal produces and the bare mapping
// case that some constructors yield.
func documentMapping(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	n = unwrapAlias(n)
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// mappingValueByKey looks up the value node for a given string key inside
// a mapping. Returns nil when the key is absent. Iterates Content in pairs
// — Content[2i] is the key, Content[2i+1] is the value.
func mappingValueByKey(mapping *yaml.Node, key string) *yaml.Node {
	_, value := findKeyValueNode(mapping, key)
	return value
}

// findKeyValueNode returns the (key, value) yaml.Node pair for a given
// string key inside a mapping, or (nil, nil) when the key is absent.
// Returning the key node lets callers distinguish "absent" from "present
// but value is nil"; today only mappingValueByKey discards the key, so
// the dual return surface stays useful for the path that needs to check
// presence before reaching for the value.
func findKeyValueNode(mapping *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return k, mapping.Content[i+1]
		}
	}
	return nil, nil
}

// mergeProvenanceIntoLabels mutates labelsNode IN PLACE: it overwrites the
// value of any pre-existing provenance keys by mutating the existing value
// node (Kind/Tag/Value/Content reset) and appends net-new provenance keys
// to labelsNode.Content in alphabetical key order. Caller-shared *yaml.Node
// references see the mutation immediately. The append order is
// deterministic so the marshalled output is byte-identical across runs of
// identical input.
func mergeProvenanceIntoLabels(labelsNode *yaml.Node, labels map[string]string) {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := labels[k]
		if _, existing := findKeyValueNode(labelsNode, k); existing != nil {
			existing.Kind = yaml.ScalarNode
			existing.Tag = "!!str"
			existing.Value = v
			existing.Content = nil
			continue
		}
		labelsNode.Content = append(labelsNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v},
		)
	}
}

// unwrapAlias resolves a yaml.AliasNode to its referent; returns the
// argument unchanged when it is not an alias. Defensive — Aggrega's
// catalog templates don't author YAML anchors today, but yaml.v3 surfaces
// alias nodes generically and the rest of the injection path expects to
// see the resolved Kind.
func unwrapAlias(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

// nodeKindName returns a human-readable name for a yaml.Node Kind, used
// in CATALOG_ERROR messages so the operator sees what shape the labels
// block actually had.
func nodeKindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return fmt.Sprintf("unknown kind %d", int(k))
	}
}
