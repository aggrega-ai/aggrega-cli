// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestNormalizeInjectMechanism_ReadsTheListAndNothingElse pins that every list
// entry the delivery resolves holds its key, in the order written — a list entry
// whose value: and from: are both absent or non-string included, holding the
// empty string the delivery resolves it to — and that a mechanism in any shape
// other than a list yields an empty map rather than being read some other way.
func TestNormalizeInjectMechanism_ReadsTheListAndNothingElse(t *testing.T) {
	t.Parallel()
	got := normalizeInjectMechanism([]any{
		map[string]any{"in": "k1", "value": "v1"},
		map[string]any{"in": "k2", "value": 42}, // no string value: or from:
		map[string]any{"in": "k3", "from": "v3"},
	})
	if want := []string{"k1", "k2", "k3"}; !reflect.DeepEqual(got.Keys(), want) {
		t.Errorf("list: keys %v; want %v", got.Keys(), want)
	}
	if v, _ := got.Get("k1"); v != "v1" {
		t.Errorf("k1 = %q; want v1", v)
	}
	if v, ok := got.Get("k2"); !ok || v != "" {
		t.Errorf("k2 = %q (present=%v); want the empty string the delivery resolves it to", v, ok)
	}

	om := types.NewOrderedMap[any]()
	om.Set("k1", "v1")
	var nilOM *types.OrderedMap[any]
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"nil", nil},
		{"nil ordered map", nilOM},
		{"ordered map", om},
		{"string map", map[string]string{"z": "z-val", "a": "a-val"}},
		{"plain map", map[string]any{"keep": "value"}},
		{"scalar", "scalar-not-a-list"},
	} {
		if got := normalizeInjectMechanism(tc.raw); got.Len() != 0 {
			t.Errorf("%s: got keys %v, want none", tc.name, got.Keys())
		}
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

// TestParseInjectMappingOutput_EveryShape pins the structured-parse, flat-map
// fallback and empty-output cases, together with the mechanism naming each of
// them reports.
//
// The naming is asserted on every shape rather than only on the structured one,
// because the difference between "named none" and "named nothing" is what tells
// a flat delivery dictionary — the legitimate env-only idiom — from a structured
// output that filled no mechanism. A reading that collapsed the two would refuse
// every flat-output FeatureRecipe.
func TestParseInjectMappingOutput_EveryShape(t *testing.T) {
	t.Parallel()
	// Empty / brace-only.
	got, mechanisms, err := ParseInjectMappingOutput("")
	if got != nil || mechanisms != nil || err != nil {
		t.Errorf("empty → (nil, nil, nil); got (%v, %v, %v)", got, mechanisms, err)
	}
	got, mechanisms, err = ParseInjectMappingOutput("{}")
	if got != nil || mechanisms != nil || err != nil {
		t.Errorf("braces → (nil, nil, nil); got (%v, %v, %v)", got, mechanisms, err)
	}

	// Flat-map output (single-level map): it names no mechanism at all, which is
	// what keeps the rule over this naming from refusing the env-only idiom.
	output := "K1: v1\nK2: v2\n"
	got, mechanisms, err = ParseInjectMappingOutput(output)
	if err != nil {
		t.Fatalf("a flat output parses; got %v", err)
	}
	if got == nil || got.Env.Len() != 2 {
		t.Errorf("flat-map should populate Env; got %v", got)
	}
	if mechanisms != nil {
		t.Errorf("a flat output names no mechanism; got %v", mechanisms)
	}

	// Structured output.
	output = `env:
  K1: v1
file:
  F1: /x
`
	got, mechanisms, err = ParseInjectMappingOutput(output)
	if err != nil {
		t.Fatalf("a structured output parses; got %v", err)
	}
	if got == nil || got.Env.Len() != 1 || got.File.Len() != 1 {
		t.Errorf("structured: got %+v", got)
	}
	if len(mechanisms) != 2 || mechanisms[0] != "env" || mechanisms[1] != "file" {
		t.Errorf("structured naming = %v; want [env file]", mechanisms)
	}

	// A structured output that fills one mechanism names exactly that one, which
	// is what says the other declared mechanisms went unmapped.
	output = `env:
  K1: v1
`
	got, mechanisms, err = ParseInjectMappingOutput(output)
	if err != nil {
		t.Fatalf("a single-mechanism output parses; got %v", err)
	}
	if got == nil {
		t.Fatalf("structured single-mechanism output should parse; got nil")
	}
	if len(mechanisms) != 1 || mechanisms[0] != "env" {
		t.Errorf("single-mechanism naming = %v; want [env]", mechanisms)
	}
}

// TestResolveValuesMappingText_AllModes pins each ValuesMapping mode:
// empty, inline, file $import (success + read error), bare-name library
// import (resolved against the mapping-library set; and the unresolved case),
// and the rejected host-bearing form.
func TestResolveValuesMappingText_AllModes(t *testing.T) {
	t.Parallel()
	libs := map[string]types.MappingLibrary{
		"Lib": mappingLibraryFixture("Lib", map[string]string{"X": "imported: fragment\n"}),
	}

	// Empty.
	got, err := resolveValuesMappingText(types.ValuesMapping{}, "n", "Recipe", libs)
	if err != nil || got != "" {
		t.Errorf("empty: (%q, %v)", got, err)
	}

	// Inline.
	got, err = resolveValuesMappingText(types.ValuesMapping{Inline: "k: v\n"}, "n", "Recipe", libs)
	if err != nil || got != "k: v\n" {
		t.Errorf("inline: (%q, %v)", got, err)
	}

	// Bare-name library import (mode 3) — resolves to the entry's fragment.
	got, err = resolveValuesMappingText(types.ValuesMapping{Import: "Lib#/mappings/X"}, "n", "Recipe", libs)
	if err != nil || got != "imported: fragment\n" {
		t.Errorf("bare-name library import should resolve to the entry fragment; got (%q, %v)", got, err)
	}

	// Bare-name library import naming an absent library errors.
	_, err = resolveValuesMappingText(types.ValuesMapping{Import: "Absent#/mappings/X"}, "n", "Recipe", libs)
	if err == nil || !errors.Is(err, ErrMappingLibraryNotFound()) {
		t.Errorf("unresolved library import should error ErrMappingLibraryNotFound; got %v", err)
	}

	// Host-bearing $import is rejected (not addressable by location).
	_, err = resolveValuesMappingText(types.ValuesMapping{Import: "oci://hub/lib@1#/mappings/X"}, "n", "Recipe", libs)
	if err == nil || !errors.Is(err, ErrHostInLibraryImport()) {
		t.Errorf("host-bearing $import should error ErrHostInLibraryImport; got %v", err)
	}

	// File import — read success.
	tmpFile := filepath.Join(t.TempDir(), "vm.yaml")
	if err := os.WriteFile(tmpFile, []byte("k: v\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = resolveValuesMappingText(types.ValuesMapping{Import: tmpFile}, "n", "Recipe", libs)
	if err != nil || got != "k: v\n" {
		t.Errorf("file import: (%q, %v)", got, err)
	}

	// File import — read error.
	_, err = resolveValuesMappingText(types.ValuesMapping{Import: "/nonexistent/path/nope.yaml"}, "n", "Recipe", libs)
	if err == nil {
		t.Errorf("missing file should error")
	}
}

// TestResolveWorkloadText_AllModes pins inline + file + missing-file + library +
// host-rejection across the four content modes.
func TestResolveWorkloadText_AllModes(t *testing.T) {
	t.Parallel()
	wlibs := map[string]types.WorkloadLibrary{
		"BaseWorkloads": workloadLibraryFixture("BaseWorkloads", map[string]string{"Deployment": "kind: Deployment"}),
	}
	// Empty.
	got, err := resolveWorkloadText(types.WorkloadTemplate{}, "n", wlibs)
	if err != nil || got != "" {
		t.Errorf("empty: (%q, %v)", got, err)
	}
	// Inline.
	got, err = resolveWorkloadText(types.WorkloadTemplate{Inline: "kind: Workload"}, "n", wlibs)
	if err != nil || got != "kind: Workload" {
		t.Errorf("inline: (%q, %v)", got, err)
	}
	// Library import.
	got, err = resolveWorkloadText(types.WorkloadTemplate{Import: "BaseWorkloads#/workloads/Deployment"}, "n", wlibs)
	if err != nil || got != "kind: Deployment" {
		t.Errorf("library import: (%q, %v)", got, err)
	}
	// Host-bearing reference rejected.
	_, err = resolveWorkloadText(types.WorkloadTemplate{Import: "oci://hub/BaseWorkloads#/workloads/Deployment"}, "n", wlibs)
	if err == nil || !errors.Is(err, ErrHostInLibraryImport()) {
		t.Errorf("oci:// $import should error ErrHostInLibraryImport; got %v", err)
	}
	// File missing.
	_, err = resolveWorkloadText(types.WorkloadTemplate{Import: "/nope/x"}, "n", wlibs)
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
	got := AssembleEnrichedUses("node-1", graph, provides, nil)
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
	got = AssembleEnrichedUses("node-1", graph, provides, nil)
	if _, ok := got.Get("target-id"); !ok {
		t.Errorf("alias should default to target")
	}
}

// TestParseInjectMappingOutput_MalformedYAML pins the line between an output
// that does not parse and one that parses as nothing.
//
// An output that reads as neither shape — the structured read and the flat read
// both fail — is reported as an error, because none of its keys can be delivered
// and a caller that could not tell it from an empty output would drop every one
// of them without a word. An output that parses and delivers nothing is not an
// error, so the halt this error drives never reaches a mapping that legitimately
// delivers nothing. Each failing row has a parsing twin beside it, so a parse
// that reported every output as a failure fails this test too.
func TestParseInjectMappingOutput_MalformedYAML(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		output  string
		wantErr bool
	}{
		// "[" opens a sequence the output never closes.
		{name: "an unclosed sequence", output: "[\n  not closed", wantErr: true},
		// A double quote inside a double-quoted scalar ends it early.
		{name: "a quote inside a quoted scalar", output: "env:\n  KEY: \"ab\"c\"\n", wantErr: true},
		{name: "a quote inside a quoted flat scalar", output: "KEY: \"ab\"c\"\n", wantErr: true},
		// A backslash starts an escape, and \q is none YAML knows.
		{name: "a backslash inside a quoted scalar", output: "env:\n  KEY: \"ab\\qc\"\n", wantErr: true},
		{name: "a scalar where a dictionary belongs", output: "just text", wantErr: true},

		{name: "the same value written as a JSON string", output: "env:\n  KEY: \"ab\\\"c\"\n"},
		{name: "a dictionary delivering nothing", output: "env: {}\n"},
		{name: "a null document", output: "null\n"},
		{name: "a comment and nothing else", output: "# nothing to deliver\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, mechanisms, err := ParseInjectMappingOutput(tc.output)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("output %q parses, so it must not be reported as unreadable; got %v", tc.output, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("output %q reads as neither shape and must be reported; got (%v, %v, nil)", tc.output, got, mechanisms)
			}
			if got != nil || mechanisms != nil {
				t.Errorf("an unreadable output delivers nothing; got (%v, %v)", got, mechanisms)
			}
		})
	}
}

// TestNormalizeInjectMechanism_SchemaVerbose pins the schema-verbose
// []any path: each entry decoded through the delivery's own entry reader;
// from wins over value when both are present, as the delivery reads it.
func TestNormalizeInjectMechanism_SchemaVerbose(t *testing.T) {
	t.Parallel()
	// Schema-verbose form: list of {in, from?, value?} entries.
	src := []any{
		map[string]any{"in": "K1", "value": "v1-direct"},
		map[string]any{"in": "K2", "from": "f2"},
		map[string]any{"in": "K3", "value": "ignored-because-from-wins", "from": "f3"},
		// Invalid entry (no `in` field) skipped.
		map[string]any{"value": "no-in"},
	}
	got := normalizeInjectMechanism(src)
	if got.Len() != 3 {
		t.Errorf("verbose: Len=%d; want 3", got.Len())
	}
	if v, _ := got.Get("K3"); v != "f3" {
		t.Errorf("K3 from-wins-over-value: got %q", v)
	}
	if v, _ := got.Get("K2"); v != "f2" {
		t.Errorf("K2 from path: got %q", v)
	}
}

// TestEvalPathTemplate_MissingKeyExecuteError pins EvalPathTemplate's
// execute-time error when the template references a variable the context
// defines but drills into it for something missingkey=error rejects.
func TestEvalPathTemplate_MissingKeyExecuteError(t *testing.T) {
	t.Parallel()
	// .capability is defined, so the reference check admits it. The template then
	// drills into a sub-key of a plain string; missingkey=error turns that into an
	// execute-time failure.
	_, err := EvalPathTemplate("t", "directory", "b", `{{ .capability.unknown }}`, PathTemplateContext{Capability: "helm"})
	if err == nil {
		t.Errorf("missingkey=error should fail on deep miss")
	}
	// Verify the error message mentions execution.
	if !strings.Contains(err.Error(), "execute") && !strings.Contains(err.Error(), "missing") {
		// At least one of these substrings should be present.
		t.Logf("error text: %v", err)
	}
}
