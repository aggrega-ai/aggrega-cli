// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Phase B.3 artifacts host-side projection per chapter 16's three-input-kinds
// model. `artifacts:` is a single OrderedMap keyed by output filename; each entry
// is either inline content (a templated string) or $import:-loaded (a map
// containing only $import: with a file path). The host runs the generic,
// invariant-forced work — the $import read, the pillar pre-check, the Go-template
// evaluation against the full TemplateContext (including .workload and .source),
// and the build-provenance key computation — and projects the result to the
// provider guest, which decides format, provenance placement, output path, and
// final bytes. This file carries the host projection builder, the exported
// Kubernetes-YAML provenance placer the YAML-emitting guests call, and the shared
// provenance-splice primitives.

package renderer

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ArtifactsEmitter is the optional interface a provider engine implements to
// place a node's pre-evaluated artifact set into provider-native output. Phase
// B.3 builds an [ArtifactsProjection] — each entry already read from its source,
// evaluated as a Go template against the render context, and carrying the host-
// computed build-provenance key sets — and dispatches it here. The guest owns the
// format decision, the provenance placement, the output path, and the final
// bytes; the host runs only the generic, invariant-forced work (the $import read,
// the pillar pre-check, the template evaluation, and the provenance-key
// computation).
//
// Mirrors [WorkloadSynthesizer]: an engine that does not implement it produces a
// CATALOG_ERROR at B.3. Implemented by the Wasm provider adapter for every shipped
// provider; the delimiter accessor reports the guest's declared artifact-template
// delimiter pair so the host evaluates each entry with the pair the guest's target
// syntax needs (the default "{{"/"}}", or an alternate such as helm-chart's
// "[["/"]]").
type ArtifactsEmitter interface {
	// EmitArtifacts places the pre-evaluated artifact set into provider-native
	// output. Returns []EmittedFile (each tagged with its artifacts origin and
	// Intermediate marking by the emitter) or a render error.
	EmitArtifacts(ctx context.Context, proj ArtifactsProjection, nodeCtx NodeRenderContext) ([]EmittedFile, error)
	// ArtifactTemplateDelimiters reports the Go-template delimiter pair the host
	// evaluates each artifact entry with before projecting it — the guest's own
	// declared pair, so the guest's target syntax (e.g. Helm's "{{ ... }}") is
	// left untouched by the host's pre-templating pass.
	ArtifactTemplateDelimiters() (left, right string)
}

// buildArtifactsProjection performs the generic, invariant-forced host work for a
// node's artifacts: it walks Recipe.Spec.Artifacts in author order, resolves each
// entry's text (inline content or the $import:-loaded file the host reads per
// Invariant 2), runs the pillar pre-check, and evaluates the entry as a Go
// template against tmplCtx using the supplied delimiter pair. It attaches the
// host-computed build-provenance label and annotation key sets — the same key
// computation every emitting surface shares — to each entry. Format, provenance
// placement, path, and final bytes are the guest's concern via
// [ArtifactsEmitter.EmitArtifacts].
//
// The per-entry output name is the author-declared map key for an inline entry
// and the imported file's base name for an $import:-loaded entry; origin records
// which form the entry took so a guest that rejects one form can halt on it.
func buildArtifactsProjection(
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	nodeCtx NodeRenderContext,
	leftDelim, rightDelim string,
) (ArtifactsProjection, error) {
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))

	var proj ArtifactsProjection
	for _, key := range state.Recipe.Spec.Artifacts.Keys() {
		entry, _ := state.Recipe.Spec.Artifacts.Get(key)
		tmplText, err := resolveArtifactEntryText(entry, state.RecipeName, key)
		if err != nil {
			return ArtifactsProjection{}, err
		}
		name := key
		origin := "inline"
		if entry.Import != "" {
			name = filepath.Base(entry.Import)
			origin = "import"
		}
		if err := preCheckArtifactPillars(state, name, tmplText, leftDelim, rightDelim); err != nil {
			return ArtifactsProjection{}, err
		}
		rendered, err := EvalTemplateWithDelims(
			fmt.Sprintf("artifacts[%s/%s]", state.RecipeName, name),
			tmplText, tmplCtx, leftDelim, rightDelim,
		)
		if err != nil {
			return ArtifactsProjection{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: name}, fmt.Sprintf("artifact %q template eval", name))
		}
		proj.Entries = append(proj.Entries, ArtifactProjectionEntry{
			Name:        name,
			Origin:      origin,
			Content:     []byte(rendered),
			Labels:      labels,
			Annotations: annotations,
		})
	}
	return proj, nil
}

// SpliceArtifactProvenance stamps the supplied build-provenance label and
// annotation sets into a rendered Kubernetes-YAML artifact's metadata and returns
// the placed bytes. It is the exported placer a Kubernetes-YAML-emitting provider
// guest calls once per artifact entry: the guest supplies the entry's already-
// template-evaluated bytes and the host-computed provenance sets, and receives the
// stamped body, an optional warning to raise as a diagnostic, and a hard error.
//
// Placement path (identical for every YAML-splicing guest): a byte-faithful
// per-document surgical splice first, falling back to a multi-document round-trip
// for documents the splice cannot handle. defaultDelims selects the round-trip's
// parse-failure policy: true (the guest's target syntax was Aggrega's own default
// "{{"/"}}", already resolved) treats a parse failure as a CATALOG_ERROR; false
// (an alternate-delimiter guest whose output may still carry the target engine's
// "{{ ... }}", e.g. helm-chart) treats a non-round-trippable file as a skip —
// emitting the original bytes unchanged and returning warning naming the file, so
// provenance for such files rides in the chart's own helpers. indent is the YAML
// indentation width the round-trip re-marshal uses. Empty content is returned
// unchanged with no warning.
//
// A non-empty warning is advisory (the guest raises it as a diagnostic); a
// non-nil err is a hard render failure the guest surfaces as such.
func SpliceArtifactProvenance(name string, rendered []byte, labels, annotations map[string]string, defaultDelims bool, indent int) (body []byte, warning string, err error) {
	r := string(rendered)
	if strings.TrimSpace(r) == "" {
		return rendered, "", nil
	}

	// Primary: byte-faithful per-document surgical splice. Every byte the splice
	// does not insert stays identical, so no target-engine token outside the
	// inserted label lines can be re-shaped. ok=false routes to the round-trip.
	if out, ok := spliceProvenanceMultiDoc(r, labels, annotations); ok {
		return []byte(out), "", nil
	}

	// Fallback: multi-document-aware round-trip for the documents the splice
	// cannot handle.
	b, outcome, rtErr := injectProvenanceRoundTrip(name, r, labels, annotations, defaultDelims, indent)
	switch outcome {
	case artifactInjected:
		if rtErr != nil {
			return nil, "", rtErr // CATALOG_ERROR from the Q1a metadata-shape guard
		}
		return b, "", nil
	case artifactParseFailDefault:
		return nil, "", cerrors.Wrap(rtErr, cerrors.CatalogError, cerrors.SourceLocation{File: name}, fmt.Sprintf("artifact %q template output is not valid YAML", name))
	default: // artifactSkip
		return rendered, provenanceSkipWarning(name), nil
	}
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

// Deliberate second splicer — do NOT unify with the helm render-core label
// injector (helm.injectAggregaLabels / injectAggregaAnnotations). This surgical
// yaml.Node splice is the artifacts path: it preserves the user's authored field
// order and every byte outside the inserted label lines, which a Helm-authored
// artifact needs so its `{{ ... }}` template tokens survive. The render-core
// injector runs on a fully-remarshalled document with its own quoting and
// hook-weight handling for synthesized manifests. The two operate on genuinely
// different inputs and unifying them would change emitted bytes; they are left
// separate on purpose.
//
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
