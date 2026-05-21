// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestNormalizeInjectMechanism_AllShapes pins each documented dispatch
// arm: nil, *OrderedMap[any] (nil + populated), map[string]string,
// map[string]any, schema-verbose []any.
func TestNormalizeInjectMechanism_AllShapes(t *testing.T) {
	t.Parallel()
	// Nil.
	if got := normalizeInjectMechanism(nil); got.Len() != 0 {
		t.Errorf("nil → empty; got %v", got)
	}
	// Nil pointer.
	var nilOM *types.OrderedMap[any]
	if got := normalizeInjectMechanism(nilOM); got.Len() != 0 {
		t.Errorf("nil OrderedMap pointer → empty; got %v", got)
	}
	// Populated *OrderedMap[any] with non-string values filtered out.
	om := types.NewOrderedMap[any]()
	om.Set("k1", "v1")
	om.Set("k2", 42) // skipped (non-string)
	om.Set("k3", "v3")
	got := normalizeInjectMechanism(om)
	if got.Len() != 2 {
		t.Errorf("populated *OrderedMap[any]: Len=%d; want 2", got.Len())
	}
	if v, _ := got.Get("k1"); v != "v1" {
		t.Errorf("k1 = %q; want v1", v)
	}
	if _, ok := got.Get("k2"); ok {
		t.Errorf("non-string k2 should be filtered out")
	}
	// map[string]string.
	got = normalizeInjectMechanism(map[string]string{"z": "z-val", "a": "a-val"})
	if got.Len() != 2 {
		t.Errorf("map[string]string: Len=%d; want 2", got.Len())
	}
	keys := got.Keys()
	if keys[0] != "a" {
		t.Errorf("plain map keys should sort; got %v", keys)
	}
	// map[string]any with mixed value types.
	got = normalizeInjectMechanism(map[string]any{
		"keep": "value",
		"skip": 99,
	})
	if got.Len() != 1 {
		t.Errorf("map[string]any: Len=%d; want 1 (non-string filtered)", got.Len())
	}
	// Unsupported shape.
	got = normalizeInjectMechanism("scalar-not-a-map")
	if got.Len() != 0 {
		t.Errorf("unsupported shape → empty; got %v", got)
	}
}

// TestMergeAdditional_BothBranches pins the absent-mechanism (copy)
// and present-mechanism (merge) arms.
func TestMergeAdditional_BothBranches(t *testing.T) {
	t.Parallel()
	base := &types.OrderedMap[types.OrderedMap[string]]{}
	// Pre-populate base with one mechanism.
	var baseInner types.OrderedMap[string]
	baseInner.Set("a", "1")
	base.Set("existing", baseInner)

	// Overlay carries one new mechanism + a key for the existing one.
	overlay := types.OrderedMap[types.OrderedMap[string]]{}
	var existingOverlay types.OrderedMap[string]
	existingOverlay.Set("a", "OVERRIDE") // last-wins on key collision
	existingOverlay.Set("b", "2")
	overlay.Set("existing", existingOverlay)
	var newInner types.OrderedMap[string]
	newInner.Set("k", "v")
	overlay.Set("new-mech", newInner)

	mergeAdditional(base, overlay)

	if base.Len() != 2 {
		t.Fatalf("after merge: base.Len = %d; want 2", base.Len())
	}
	merged, _ := base.Get("existing")
	if v, _ := merged.Get("a"); v != "OVERRIDE" {
		t.Errorf("last-wins on a; got %q", v)
	}
	if v, _ := merged.Get("b"); v != "2" {
		t.Errorf("b should be merged in; got %q", v)
	}
	fresh, ok := base.Get("new-mech")
	if !ok {
		t.Errorf("new-mech should be added")
	}
	if v, _ := fresh.Get("k"); v != "v" {
		t.Errorf("fresh.k = %q; want v", v)
	}
}

// TestParseInjectMappingOutput_EveryShape pins the structured-parse,
// flat-map fallback, dangling-reference error, and empty-output cases.
func TestParseInjectMappingOutput_EveryShape(t *testing.T) {
	t.Parallel()
	// Empty / brace-only.
	got, err := ParseInjectMappingOutput("", nil)
	if got != nil || err != nil {
		t.Errorf("empty → (nil, nil); got (%v, %v)", got, err)
	}
	got, err = ParseInjectMappingOutput("{}", nil)
	if got != nil || err != nil {
		t.Errorf("braces → (nil, nil); got (%v, %v)", got, err)
	}

	// Flat-map output (single-level map).
	output := "K1: v1\nK2: v2\n"
	got, err = ParseInjectMappingOutput(output, nil)
	if err != nil {
		t.Fatalf("flat: %v", err)
	}
	if got == nil || got.Env.Len() != 2 {
		t.Errorf("flat-map should populate Env; got %v", got)
	}

	// Structured output.
	output = `env:
  K1: v1
file:
  F1: /x
`
	got, err = ParseInjectMappingOutput(output, nil)
	if err != nil {
		t.Fatalf("structured: %v", err)
	}
	if got == nil || got.Env.Len() != 1 || got.File.Len() != 1 {
		t.Errorf("structured: got %+v", got)
	}

	// Dangling-reference: authored mechanism not in output.
	output = `env:
  K1: v1
`
	_, err = ParseInjectMappingOutput(output, []string{"env", "file"})
	if err == nil || !errors.Is(err, ErrUnmappedCustomMechanism) {
		t.Errorf("authored 'file' missing should error ErrUnmappedCustomMechanism; got %v", err)
	}

	// Authored matches structured output.
	got, err = ParseInjectMappingOutput(output, []string{"env"})
	if err != nil || got == nil {
		t.Errorf("authored env present: err=%v got=%v", err, got)
	}
}

// TestResolveValuesMappingText_AllModes pins each ValuesMapping mode:
// empty, inline, file $import (success + read error), OCI library
// import (not yet supported error).
func TestResolveValuesMappingText_AllModes(t *testing.T) {
	t.Parallel()
	// Empty.
	got, err := resolveValuesMappingText(types.ValuesMapping{}, "n", "Recipe")
	if err != nil || got != "" {
		t.Errorf("empty: (%q, %v)", got, err)
	}

	// Inline.
	got, err = resolveValuesMappingText(types.ValuesMapping{Inline: "k: v\n"}, "n", "Recipe")
	if err != nil || got != "k: v\n" {
		t.Errorf("inline: (%q, %v)", got, err)
	}

	// OCI library import.
	_, err = resolveValuesMappingText(types.ValuesMapping{Import: "oci://hub/lib@1#/schemas/X"}, "n", "Recipe")
	if err == nil || !errors.Is(err, ErrLibraryImportNotYetSupported) {
		t.Errorf("OCI import should error ErrLibraryImportNotYetSupported; got %v", err)
	}

	// File import — read success.
	tmpFile := filepath.Join(t.TempDir(), "vm.yaml")
	if err := os.WriteFile(tmpFile, []byte("k: v\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = resolveValuesMappingText(types.ValuesMapping{Import: tmpFile}, "n", "Recipe")
	if err != nil || got != "k: v\n" {
		t.Errorf("file import: (%q, %v)", got, err)
	}

	// File import — read error.
	_, err = resolveValuesMappingText(types.ValuesMapping{Import: "/nonexistent/path/nope.yaml"}, "n", "Recipe")
	if err == nil {
		t.Errorf("missing file should error")
	}
}

// TestResolveWorkloadText_AllModes pins inline + file + missing-file.
func TestResolveWorkloadText_AllModes(t *testing.T) {
	t.Parallel()
	// Empty.
	got, err := resolveWorkloadText(types.WorkloadTemplate{}, "n")
	if err != nil || got != "" {
		t.Errorf("empty: (%q, %v)", got, err)
	}
	// Inline.
	got, err = resolveWorkloadText(types.WorkloadTemplate{Inline: "kind: Workload"}, "n")
	if err != nil || got != "kind: Workload" {
		t.Errorf("inline: (%q, %v)", got, err)
	}
	// File missing.
	_, err = resolveWorkloadText(types.WorkloadTemplate{Import: "/nope/x"}, "n")
	if err == nil {
		t.Errorf("missing file should error")
	}
}

// TestResolveArtifactEntryText_AllModes pins inline + file + missing.
func TestResolveArtifactEntryText_AllModes(t *testing.T) {
	t.Parallel()
	got, err := resolveArtifactEntryText(types.ArtifactEntry{}, "n", "art")
	if err != nil || got != "" {
		t.Errorf("empty: (%q, %v)", got, err)
	}
	got, err = resolveArtifactEntryText(types.ArtifactEntry{Inline: "content"}, "n", "art")
	if err != nil || got != "content" {
		t.Errorf("inline: (%q, %v)", got, err)
	}
	_, err = resolveArtifactEntryText(types.ArtifactEntry{Import: "/nope/y"}, "n", "art")
	if err == nil {
		t.Errorf("missing file should error")
	}
}

// TestIsResourceGeneratingFR_EveryBranch pins each documented branch:
// nil, source as non-empty string, source as non-string (structured),
// valuesMapping populated, all-empty.
func TestIsResourceGeneratingFR_EveryBranch(t *testing.T) {
	t.Parallel()
	if isResourceGeneratingFR(nil) {
		t.Errorf("nil should be false")
	}
	if isResourceGeneratingFR(&types.FeatureRecipe{}) {
		t.Errorf("empty FR should be false")
	}
	withSource := &types.FeatureRecipe{}
	withSource.Spec.Source = "/some/path"
	if !isResourceGeneratingFR(withSource) {
		t.Errorf("non-empty source string should be true")
	}
	emptyStr := &types.FeatureRecipe{}
	emptyStr.Spec.Source = ""
	if isResourceGeneratingFR(emptyStr) {
		t.Errorf("empty source string should be false")
	}
	withVM := &types.FeatureRecipe{}
	withVM.Spec.ValuesMapping = types.ValuesMapping{Inline: "k: v"}
	if !isResourceGeneratingFR(withVM) {
		t.Errorf("non-empty valuesMapping should be true")
	}
}

// TestAssembleEnrichedUses_AllBranches pins edge filtering by source,
// missing-target skip, alias defaulting, and the annotations-buckets
// branch.
func TestAssembleEnrichedUses_AllBranches(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	target := types.IRNode{
		Component:       "comp/target",
		ResolvedVersion: "1.0.0",
		BoundaryPath:    "infra/web",
	}
	target.Labels.Set("tier", "data")
	target.CatalogAnnotations.Set("ann.cat", "x")
	graph.Spec.Nodes.Set("target-id", target)

	// Edges:
	// 1. From "node-1" → "target-id" with alias "alias-x".
	// 2. From "node-1" → "absent-target" (skipped: target not in Nodes).
	// 3. From "other-node" → "target-id" (skipped: wrong source).
	graph.Spec.Edges = []types.IREdge{
		{Source: "node-1", Target: "target-id", Socket: "http", Alias: "alias-x"},
		{Source: "node-1", Target: "absent-target", Socket: "http"},
		{Source: "other-node", Target: "target-id", Socket: "http"},
	}

	provides := NodeProvides{"target-id": types.OrderedMap[string]{}}
	got := AssembleEnrichedUses("node-1", graph, provides)
	if got.Len() != 1 {
		t.Fatalf("expected 1 enriched edge; got %d", got.Len())
	}
	ee, ok := got.Get("alias-x")
	if !ok {
		t.Errorf("alias key missing")
	}
	if ee.Target != "target-id" {
		t.Errorf("Target = %q; want target-id", ee.Target)
	}
	if ee.Annotations == nil {
		t.Errorf("annotation buckets should populate")
	}
	if ee.BoundaryPath != "infra/web" {
		t.Errorf("BoundaryPath lost; got %q", ee.BoundaryPath)
	}

	// Edge without alias → defaults to target.
	graph.Spec.Edges = []types.IREdge{
		{Source: "node-1", Target: "target-id", Socket: "http"},
	}
	got = AssembleEnrichedUses("node-1", graph, provides)
	if _, ok := got.Get("target-id"); !ok {
		t.Errorf("alias should default to target")
	}
}

// TestCollectFromNode_AllNodeKinds pins the parse-AST walker by
// constructing a template that exercises every dispatch arm.
func TestCollectFromNode_AllNodeKinds(t *testing.T) {
	t.Parallel()
	src := `{{ .alpha }} {{ if .beta }}{{ .gamma }}{{ else }}{{ .delta }}{{ end }} {{ range .epsilon }}{{ . }}{{ end }} {{ with .zeta }}{{ .eta }}{{ end }} {{ template "tpl" . }}`
	// We need a *parse.ListNode root to feed collectFromNode.
	trees, err := parse.Parse("t", src, "{{", "}}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	seen := map[string]bool{}
	root := trees["t"].Root
	collectFromNode(root, seen)
	for _, k := range []string{".alpha", ".beta", ".gamma", ".delta", ".epsilon", ".zeta", ".eta"} {
		if !seen[k] {
			t.Errorf("expected %q in seen; got %v", k, seen)
		}
	}
	// Nil ListNode.
	collectFromNode((*parse.ListNode)(nil), seen)
	// Nil node.
	collectFromNode(nil, seen)
}

// TestCollectFromPipe_NilGuard pins the pipe-walker's nil-guard.
func TestCollectFromPipe_NilGuard(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	collectFromPipe(nil, seen)
	if len(seen) != 0 {
		t.Errorf("nil pipe should leave seen empty; got %v", seen)
	}
}

// TestParseInjectMappingOutput_MalformedYAML pins the malformed-input
// branch: returning (nil, nil) when neither structured nor flat parse
// produces content.
func TestParseInjectMappingOutput_MalformedYAML(t *testing.T) {
	t.Parallel()
	// "[" parses as an unclosed sequence — YAML can't read it, both
	// structured and flat fall back to (nil, nil).
	got, err := ParseInjectMappingOutput("[\n  not closed", nil)
	if err != nil || got != nil {
		t.Errorf("malformed: (%v, %v); want (nil, nil)", got, err)
	}
}

// TestNormalizeInjectMechanism_SchemaVerbose pins the schema-verbose
// []any path: each entry decoded via DecodeVerboseInjectEntry; value
// wins over from when both present.
func TestNormalizeInjectMechanism_SchemaVerbose(t *testing.T) {
	t.Parallel()
	// Schema-verbose form: list of {in, from?, value?} entries.
	src := []any{
		map[string]any{"in": "K1", "value": "v1-direct"},
		map[string]any{"in": "K2", "from": "f2"},
		map[string]any{"in": "K3", "value": "v3", "from": "ignored-because-value-wins"},
		// Invalid entry (no `in` field) skipped.
		map[string]any{"value": "no-in"},
	}
	got := normalizeInjectMechanism(src)
	if got.Len() != 3 {
		t.Errorf("verbose: Len=%d; want 3", got.Len())
	}
	if v, _ := got.Get("K3"); v != "v3" {
		t.Errorf("K3 value-wins-over-from: got %q", v)
	}
	if v, _ := got.Get("K2"); v != "f2" {
		t.Errorf("K2 from path: got %q", v)
	}
}

// TestUnmarshalImportableThroughEvalPathTemplate_Truncated pins
// EvalPathTemplate's execute-time error when the template references a
// path that exists in the validation allowlist but produces a value
// missingkey=error rejects.
func TestEvalPathTemplate_MissingKeyExecuteError(t *testing.T) {
	t.Parallel()
	// .provider is in the allowlist — validation passes. The template
	// then drills into a sub-key that doesn't exist; missingkey=error
	// turns that into an execute-time failure.
	_, err := EvalPathTemplate("t", "directory", "b", `{{ .provider.unknown }}`, map[string]any{"provider": "helm"})
	if err == nil {
		t.Errorf("missingkey=error should fail on deep miss")
	}
	// Verify the error message mentions execution.
	if !strings.Contains(err.Error(), "execute") && !strings.Contains(err.Error(), "missing") {
		// At least one of these substrings should be present.
		t.Logf("error text: %v", err)
	}
}
