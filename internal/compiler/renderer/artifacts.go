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

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ExternalArtifactsHandler is the marker interface implemented by Engines
// whose provider model handles artifacts outside Phase B's standard
// emission path. Today: the helm-chart engine, whose `artifactsFrom:`
// files are merged into the chart's `templates/` directory pre-Helm-
// rendering rather than emitted as standalone files. Engines satisfying
// this marker reject inline `artifacts:` at Phase B.3 (the chart
// structure cannot accept inline declarations) and treat
// `artifactsFrom:` as a no-op at Phase B.3 (the pre-template injection
// runs outside Phase B).
//
// Dispatch parity. Phase B.3 dispatches by marker assertion in the same
// shape as the ReleaseComposer dispatch. Engines that opt into the
// behaviour declare it with a no-op method; the artifacts evaluator does
// not branch on provider-name strings.
type ExternalArtifactsHandler interface {
	HandlesArtifactsExternally()
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
			return nil, fmt.Errorf(
				"CATALOG_ERROR: recipe %q (%q): provider %q does not support inline artifacts:; declare artifactsFrom: instead so the files can be injected into the chart's templates/ directory",
				state.RecipeName, state.RecipePath, state.Recipe.Spec.Provider,
			)
		}
		// External-handler engines (today: helm-chart) consume
		// artifactsFrom: outside Phase B.3 — they merge files into the
		// chart structure before Helm rendering rather than emitting
		// standalone manifests. B.3 is a no-op for this branch; the
		// engine's pre-template injection path handles the actual merge.
		return nil, nil
	}

	// Intermediate-tagged artifacts are consumed by the orchestrator's
	// per-boundary grouping in RenderAll — the `if f.Intermediate { ... }`
	// branch routes solely on the flag. The grouping is Origin-agnostic:
	// every Intermediate=true EmittedFile flows to ComposeRelease via
	// ReleaseInfo regardless of EmitOrigin. End-to-end exercise:
	// TestPhaseB3_ArtifactsIntermediate_RoutesViaReleaseComposer in
	// renderer_integration_test.go is the live anchor — a regression that
	// breaks the routing changes the test outcome.
	_, isReleaseComposer := engine.(ReleaseComposer)

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
			Intermediate: isReleaseComposer,
		})
	}

	for _, path := range state.Recipe.Spec.ArtifactsFrom {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("CATALOG_ERROR: recipe %q artifactsFrom %q: %w", state.RecipeName, path, err)
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
			Intermediate: isReleaseComposer,
		})
	}

	return out, nil
}

// evaluateOneArtifact handles the common per-entry pipeline: nil-pillar
// pre-check, Go-template evaluation, YAML parse, provenance-label merge,
// YAML re-marshal. Returns the marshalled bytes ready to attach to an
// EmittedFile. tmplName is the template-engine identifier surfaced in
// parse / execute error messages; filename is the user-facing artifact name
// surfaced in CATALOG_ERROR.
func evaluateOneArtifact(
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	nodeCtx NodeRenderContext,
	filename string,
	tmplText string,
	tmplName string,
) ([]byte, error) {
	if err := preCheckArtifactPillars(state, filename, tmplText); err != nil {
		return nil, err
	}

	rendered, err := EvalTemplate(tmplName, tmplText, tmplCtx)
	if err != nil {
		return nil, fmt.Errorf("CATALOG_ERROR: artifact %q template eval: %w", filename, err)
	}

	if strings.TrimSpace(rendered) == "" {
		return []byte(rendered), nil
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(rendered), &node); err != nil {
		return nil, fmt.Errorf("CATALOG_ERROR: artifact %q template output is not valid YAML: %w", filename, err)
	}
	if node.Kind == 0 {
		// Empty document (e.g., `---` only) — emit the rendered bytes as-is.
		return []byte(rendered), nil
	}

	labels := buildProvenanceLabels(state, nodeCtx)
	if err := injectProvenanceNode(filename, &node, labels); err != nil {
		return nil, err
	}

	body, err := yaml.Marshal(&node)
	if err != nil {
		return nil, fmt.Errorf("CATALOG_ERROR: artifact %q yaml marshal: %w", filename, err)
	}
	return body, nil
}

// preCheckArtifactPillars walks the template's parsed field-access set
// (via CollectTemplateFieldAccesses) and short-circuits with a CATALOG_ERROR
// when the template references a pillar slot that the current Recipe has
// not declared:
//   - any `.workload.*` reference when state.ParsedWorkload == nil.
//   - any `.source.*` reference when state.Recipe.Spec.Source == nil.
//
// The pre-check names the failing expression authoritatively (the literal
// dot-path captured by the parser) and avoids depending on text/template's
// runtime nil-pointer error formatting. CollectTemplateFieldAccesses is the
// in-house helper already used by orchestrator's unused-key warning path.
func preCheckArtifactPillars(state *PhaseBState, filename, tmplText string) error {
	refs, err := CollectTemplateFieldAccesses(tmplText)
	if err != nil {
		return fmt.Errorf("CATALOG_ERROR: artifact %q template parse: %w", filename, err)
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
			return fmt.Errorf(
				"CATALOG_ERROR: artifact %q template references %q but Recipe declares no workload:/workloadFrom:",
				filename, ref,
			)
		}
		if !sourceDeclared && (ref == ".source" || strings.HasPrefix(ref, ".source.")) {
			return fmt.Errorf(
				"CATALOG_ERROR: artifact %q template references %q but Recipe declares no source:",
				filename, ref,
			)
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
func injectProvenanceNode(filename string, root *yaml.Node, labels map[string]string) error {
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

	labelsKey, labelsNode := findKeyValueNode(metadata, "labels")
	if labelsKey != nil && labelsNode != nil {
		labelsNode = unwrapAlias(labelsNode)
		if labelsNode.Kind != yaml.MappingNode {
			return fmt.Errorf(
				"CATALOG_ERROR: artifact %q metadata.labels has unexpected shape (expected mapping, got %s); cannot merge provenance labels",
				filename, nodeKindName(labelsNode.Kind),
			)
		}
		mergeProvenanceIntoLabels(labelsNode, labels)
		return nil
	}

	// No labels: key under metadata; append a new mapping node carrying the
	// provenance set.
	newLabels := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mergeProvenanceIntoLabels(newLabels, labels)
	metadata.Content = append(metadata.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "labels"},
		newLabels,
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
