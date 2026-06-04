// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Phase B.3 artifacts evaluation per chapter 16's three-input-kinds model.
// `artifacts:` is a single OrderedMap keyed by output filename; each entry
// is either inline content (a templated string) or $import:-loaded (a map
// containing only $import: with a file path). Both forms evaluate against
// the full TemplateContext including .workload and .source, get provenance
// labels merged into K8s-style metadata.labels, and emit as EmittedFile
// entries tagged with their origin so the Phase B.5 collision detector can
// name every contributor on a cross-origin clash.

package renderer

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ExternalArtifactsHandler is the marker interface implemented by Engines
// whose provider model handles artifacts outside Phase B's standard
// emission path. Today: the helm-chart engine, whose $import:-loaded
// artifact entries are merged into the chart's `templates/` directory
// rather than emitted as standalone files. Engines satisfying this marker
// reject inline-content entries at Phase B.3 (the chart structure cannot
// accept inline-content artifact declarations).
//
// Optional companion capabilities. Engines that opt into external
// dispatch tune the per-artifact placement and the template delimiter
// pair through two independent capability interfaces:
//
//   - ArtifactsPathProvider — engine declares the per-node-relative path
//     each emitted $import:-loaded artifact lands at (e.g., helm-chart's
//     "templates/<basename>"). Engines that don't implement this interface
//     emit at the bare basename, matching the standard non-external
//     emission shape.
//
//   - ArtifactsTemplateDelims — engine declares the Go-template delimiter
//     pair Aggrega's pre-templating uses on each $import:-loaded artifact
//     file (e.g., helm-chart's "[[", "]]" so Helm's "{{ ... }}" survives
//     into the downstream render). Engines that don't implement this
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
// emission shape — when the artifacts evaluator dispatches a $import:-loaded
// artifact entry.
type ArtifactsPathProvider interface {
	PathForArtifact(nodeCtx NodeRenderContext, basename string) string
}

// ArtifactsTemplateDelims declares the Go-template delimiter pair used
// when Aggrega's pre-templating evaluates $import:-loaded artifact files
// for an ExternalArtifactsHandler engine. Engines whose provider model
// runs a downstream templating pass with the same default `{{ }}`
// delimiters return alternates here so Aggrega's pass leaves the
// downstream syntax untouched. helm-chart returns "[[", "]]" so Helm's
// `{{ ... }}` blocks survive into the chart's render pipeline.
//
// Engines that don't implement this interface fall back to the default
// "{{", "}}" pair (text/template default).
type ArtifactsTemplateDelims interface {
	ArtifactDelimiters() (left, right string)
}

// evaluateArtifacts walks Recipe.Spec.Artifacts in author order, dispatching
// per entry on content shape (inline content vs $import:-loaded), evaluates
// each entry as a Go template against tmplCtx, parses the output as YAML,
// merges the canonical provenance label set into metadata.labels (when the
// document carries metadata), and returns the emitted-file slice for the
// caller to append to state.EmittedFiles. Each emission carries the matching
// EmitOrigin tag so Phase B.5 cross-origin collision detection can name
// every contributor.
//
// Per-provider dispatch:
//   - helm / terraform / cloudformation / future flat providers — emissions
//     land as standalone files under the boundary output directory
//     (Intermediate=false).
//   - compose — emissions are tagged Intermediate=true so the orchestrator
//     routes them to ComposeRelease's parseNodeFragment via ReleaseInfo,
//     mirroring how engine.Render output is routed for ReleaseComposer
//     engines today.
//   - helm-chart — inline-content artifact entries are rejected with a
//     CATALOG_ERROR because Helm owns the chart's templates/ directory and
//     cannot accept inline-content artifact declarations. $import:-loaded
//     entries are a no-op at B.3; the helm-chart provider's special
//     pre-template injection path is the responsibility of a follow-up work
//     item, not B.3.
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
	if state.Recipe.Spec.Artifacts.Len() == 0 {
		return nil, nil
	}

	if _, external := engine.(ExternalArtifactsHandler); external {
		// External-handler engines (helm-chart and similar): each
		// artifact entry must be a file-import (mode 2). Inline
		// content is forbidden because the engine consumes pre-
		// templated files at chart-templates/ injection time; the
		// inline-template-string pattern doesn't survive the chart's
		// own template engine.
		leftDelim, rightDelim := "{{", "}}"
		if d, ok := engine.(ArtifactsTemplateDelims); ok {
			leftDelim, rightDelim = d.ArtifactDelimiters()
		}
		pathFn := func(_ NodeRenderContext, basename string) string { return basename }
		if p, ok := engine.(ArtifactsPathProvider); ok {
			pathFn = p.PathForArtifact
		}
		var out []EmittedFile
		for _, name := range state.Recipe.Spec.Artifacts.Keys() {
			entry, _ := state.Recipe.Spec.Artifacts.Get(name)
			if entry.Inline != "" {
				return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath},
					fmt.Sprintf("recipe %q (%q): provider %q does not support inline artifacts entries; use the {$import: <path>} mode so the file can be injected into the chart's templates/ directory",
						state.RecipeName, state.RecipePath, state.Recipe.Spec.Provider),
					nil, nil)
			}
			tmplText, err := resolveArtifactEntryText(entry, state.RecipeName, name)
			if err != nil {
				return nil, err
			}
			basename := filepath.Base(entry.Import)
			body, err := evaluateOneArtifactWithDelims(
				tmplCtx, state, nodeCtx, basename, tmplText,
				fmt.Sprintf("artifactsImport[%s/%s]", state.RecipeName, basename),
				leftDelim, rightDelim,
			)
			if err != nil {
				return nil, err
			}
			out = append(out, EmittedFile{
				Name:         pathFn(nodeCtx, basename),
				Origin:       EmitOriginArtifactsImport,
				Body:         body,
				Intermediate: false,
			})
		}
		return out, nil
	}

	// Intermediate-tagged emitted files are consumed by the orchestrator's
	// per-(path, provider) grouping in RenderAll — the `if f.Intermediate`
	// branch routes solely on the flag.
	_, isAssembler := engine.(BoundaryAssembler)

	var out []EmittedFile

	for _, filename := range state.Recipe.Spec.Artifacts.Keys() {
		entry, _ := state.Recipe.Spec.Artifacts.Get(filename)
		tmplText, err := resolveArtifactEntryText(entry, state.RecipeName, filename)
		if err != nil {
			return nil, err
		}
		// Mode 1 (inline) — filename is the author-declared map key.
		// Mode 2 (file import) — filename is still the map key (the
		// author chose it; basename derivation happens at the
		// external-handler path above where the chart-injection
		// convention requires basename-from-path).
		origin := EmitOriginArtifactsInline
		if entry.Import != "" {
			origin = EmitOriginArtifactsImport
		}
		body, err := evaluateOneArtifact(tmplCtx, state, nodeCtx, filename, tmplText, fmt.Sprintf("artifacts[%s/%s]", state.RecipeName, filename))
		if err != nil {
			return nil, err
		}
		out = append(out, EmittedFile{
			Name:         filename,
			Origin:       origin,
			Body:         body,
			Intermediate: isAssembler,
		})
	}

	return out, nil
}

// provenanceSkipWarning is the user-facing message emitted when a
// helm-chart `$import:`-loaded artifact does not survive Aggrega's
// pre-render YAML round-trip, so provenance labels cannot be merged
// before the file is handed to Helm. The guidance points first at
// quoting scalar Helm substitutions — a quoted `{{ }}` scalar round-
// trips, so the compiler can then inject provenance — and reserves the
// chart's `_helpers.tpl` for files using structural Helm template
// actions, which never parse as YAML pre-render.
func provenanceSkipWarning(filename string) string {
	return fmt.Sprintf(
		"artifact %q ($import-loaded): skipped provenance label injection — the file does not survive Aggrega's pre-render YAML round-trip. Quote scalar Helm substitutions (e.g. namespace: \"{{ .Release.Namespace }}\") so the compiler can inject provenance; for structural Helm actions ({{- if }}, {{- range }}, {{- include }}), add provenance via the chart's _helpers.tpl instead.",
		filename,
	)
}

// helmTemplateTokenRe matches the maximal `{{ ... }}` spans on a single
// line — the Helm references that must survive a provenance round-trip
// verbatim. Structural / multi-line actions never reach the verification;
// they fail the YAML parse first.
var helmTemplateTokenRe = regexp.MustCompile(`\{\{.*?\}\}`)

// helmTemplateTokens returns the Helm `{{ ... }}` references in s. The
// provenance round-trip is accepted only when each is preserved verbatim
// in the re-marshalled output — yaml.v3 parses an unquoted `{{ x }}` value
// as a flow mapping and re-marshals it as `{? {x: ”} : ”}`, which this
// check detects as a dropped token.
func helmTemplateTokens(s string) []string {
	return helmTemplateTokenRe.FindAllString(s, -1)
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
//     $import:-loaded artifact file so the build surfaces the gap.
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

	// External-handler path (non-default delimiters, e.g. helm-chart's
	// `[[ ]]`): the post-`[[ ]]` output may still carry Helm `{{ ... }}`
	// destined for Helm's downstream render. Provenance is merged only when
	// the YAML round-trip below preserves those Helm references intact —
	// the gate is "does the round-trip preserve every Helm token", NOT
	// merely "does it parse":
	//
	//   - Helm `{{ }}` confined to QUOTED scalar positions (or files using
	//     only `[[ ]]` context) round-trip cleanly — the reference rides
	//     inside a string scalar (the re-marshal may swap `"{{ x }}"` for
	//     `'{{ x }}'`, cosmetic and harmless). Provenance is merged.
	//   - UNQUOTED `{{ x }}` at a value position does NOT fail to parse:
	//     yaml.v3 reads it as a flow mapping and re-marshals it destructively
	//     (`{{ x }}` -> `{? {x: ''} : ''}`), corrupting the Helm template. A
	//     parse success with a mangled re-marshal is therefore treated as a
	//     skip, not an inject — caught by the token check after the marshal.
	//   - Structural Helm actions (`{{- if }}`, `{{- range }}`,
	//     `{{- include }}`) fail the parse outright.
	//
	// All non-round-trippable cases route to the same outcome: skip
	// provenance, emit the rendered bytes UNCHANGED, and warn — provenance
	// for such files belongs in the chart's `_helpers.tpl`.
	//
	// Default-delimiter (non-external-handler) callers instead treat a parse
	// failure as a CATALOG_ERROR — Aggrega's pre-templating output is
	// authoritative for that path, and its `{{ }}` were Aggrega's own
	// delimiters, already resolved, so no Helm tokens remain to preserve.

	labels := buildProvenanceLabels(state, nodeCtx)
	annotations := buildProvenanceAnnotations(state, nodeCtx)

	// Primary: byte-faithful per-document surgical splice. Every byte the splice
	// does not insert stays identical, so no Helm `{{ }}` token outside the
	// inserted label lines can be re-shaped — and every document in a multi-doc
	// stream is stamped (the single-yaml.Unmarshal path this replaces decoded only
	// the first document and silently dropped the rest). ok=false (a non-spliceable
	// metadata shape, or a post-splice anomaly) routes to the round-trip fallback.
	if out, ok := spliceProvenanceMultiDoc(rendered, labels, annotations); ok {
		return []byte(out), nil
	}

	// Fallback: multi-document-aware round-trip — verified-safe (decodes every
	// document, injects each, re-marshals all) for the documents the splice cannot
	// handle. The external-delims path gates on Helm-token preservation; a
	// default-delims parse failure is a CATALOG_ERROR; otherwise skip + warn and
	// emit the ORIGINAL rendered bytes.
	body, outcome, rtErr := injectProvenanceRoundTrip(filename, rendered, labels, annotations, usingDefaultDelims)
	switch outcome {
	case artifactInjected:
		if rtErr != nil {
			return nil, rtErr // CATALOG_ERROR from the Q1a metadata-shape guard
		}
		return body, nil
	case artifactParseFailDefault:
		return nil, cerrors.Wrap(rtErr, cerrors.CatalogError, cerrors.SourceLocation{File: filename}, fmt.Sprintf("artifact %q template output is not valid YAML", filename))
	default: // artifactSkip
		state.Warnings = append(state.Warnings, provenanceSkipWarning(filename))
		return []byte(rendered), nil
	}
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
				fmt.Sprintf("artifact %q template references %q but Recipe declares no workload:", filename, ref),
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
