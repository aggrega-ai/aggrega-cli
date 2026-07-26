// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPhaseB0_NilRecipe pins phaseB0's early-return when state.Recipe
// is nil — exercises the entry guard arm.
func TestPhaseB0_NilRecipe(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{}
	state := &PhaseBState{}
	if err := phaseB0(tmplCtx, state, types.OrderedMap[string]{}); err != nil {
		t.Errorf("nil recipe: %v", err)
	}
	if state.PhaseReached != 0 {
		t.Errorf("PhaseReached = %d; want 0", state.PhaseReached)
	}
}

// TestPhaseB1_NilRecipe pins phaseB1's early-return.
func TestPhaseB1_NilRecipe(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{}
	state := &PhaseBState{}
	if err := phaseB1(tmplCtx, state); err != nil {
		t.Errorf("nil recipe: %v", err)
	}
	if state.PhaseReached != 1 {
		t.Errorf("PhaseReached = %d; want 1", state.PhaseReached)
	}
}

// TestPhaseB1_NoValuesMapping pins phaseB1's no-VM short-circuit:
// state.ResolvedValues is set from ctx.Values flattening.
func TestPhaseB1_NoValuesMapping(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{}
	tmplCtx.Values.Set("k", "v")
	state := &PhaseBState{
		Recipe: &types.Recipe{},
	}
	if err := phaseB1(tmplCtx, state); err != nil {
		t.Errorf("no VM: %v", err)
	}
	if state.PhaseReached != 1 {
		t.Errorf("PhaseReached = %d; want 1", state.PhaseReached)
	}
}

// TestPhaseB3_EmptyArtifacts pins phaseB3's no-artifacts short-circuit.
func TestPhaseB3_EmptyArtifacts(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{}
	state := &PhaseBState{Recipe: &types.Recipe{}}
	if err := phaseB3(context.Background(), tmplCtx, state, nil, NodeRenderContext{}); err != nil {
		t.Errorf("empty artifacts: %v", err)
	}
	if state.PhaseReached != 3 {
		t.Errorf("PhaseReached = %d; want 3", state.PhaseReached)
	}
}

// TestPhaseB3_NilRecipe pins phaseB3's early-return.
func TestPhaseB3_NilRecipe(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{}
	if err := phaseB3(context.Background(), &types.TemplateContext{}, state, nil, NodeRenderContext{}); err != nil {
		t.Errorf("nil recipe: %v", err)
	}
}

// TestPhaseB4_NilWorkload pins phaseB4's early-return when no Workload
// is parsed.
func TestPhaseB4_NilWorkload(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{}
	if err := phaseB4(context.Background(), &types.TemplateContext{}, state, nil, NodeRenderContext{}); err != nil {
		t.Errorf("nil workload: %v", err)
	}
	if state.PhaseReached != 4 {
		t.Errorf("PhaseReached = %d; want 4", state.PhaseReached)
	}
}

// TestWalkForTemplate_NilAndScalar pins the zero-input + nil-pointer
// + scalar branches of the reflective walker.
func TestWalkForTemplate_NilAndScalar(t *testing.T) {
	t.Parallel()
	// Invalid reflect.Value.
	if got := walkForTemplate(reflect.Value{}); got != nil {
		t.Errorf("invalid → nil; got %v", got)
	}
	// Nil pointer.
	var nilPtr *int
	if got := walkForTemplate(reflect.ValueOf(nilPtr)); got != nil {
		t.Errorf("nil pointer → nil; got %v", got)
	}
	// Scalar pass-through.
	if got := walkForTemplate(reflect.ValueOf(42)); got != 42 {
		t.Errorf("int → 42; got %v", got)
	}
	if got := walkForTemplate(reflect.ValueOf("hello")); got != "hello" {
		t.Errorf("string → hello; got %v", got)
	}
}

// TestWalkForTemplate_OrderedMapDetection pins the OrderedMap marker-
// interface dispatch via direct invocation with both value and
// pointer-held OrderedMap[any].
func TestWalkForTemplate_OrderedMapDetection(t *testing.T) {
	t.Parallel()
	om := types.NewOrderedMap[any]()
	om.Set("a", "1")
	om.Set("b", 2)

	// *OrderedMap[any] via interface detection.
	got := walkForTemplate(reflect.ValueOf(om))
	m, ok := got.(map[string]any)
	if !ok || m["a"] != "1" || m["b"] != 2 {
		t.Errorf("*OrderedMap → map; got %v", got)
	}

	// OrderedMap[any] value (CanAddr-based detection).
	var omValue types.OrderedMap[any]
	omValue.Set("k", "v")
	// Pass as a value through walkStruct; the marker interface is
	// detected via CanAddr branch when wrapping in a struct.
	type holder struct {
		Inner types.OrderedMap[any]
	}
	h := holder{Inner: omValue}
	got = walkForTemplate(reflect.ValueOf(h))
	hm, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("struct holder → map; got %v", got)
	}
	innerMap, ok := hm["inner"].(map[string]any)
	if !ok || innerMap["k"] != "v" {
		t.Errorf("inner OrderedMap value → map; got %v", hm["inner"])
	}
}

// TestWalkForTemplate_StructAndMapAndSlice pins the three reflection
// kinds.
func TestWalkForTemplate_StructAndMapAndSlice(t *testing.T) {
	t.Parallel()
	// Struct.
	type s struct {
		Name  string `json:"name"`
		Empty string `json:"empty,omitempty"`
		Skip  string `json:"-"`
	}
	got := walkForTemplate(reflect.ValueOf(s{Name: "x"}))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("struct → map; got %v", got)
	}
	if m["name"] != "x" {
		t.Errorf("name = %v; want x", m["name"])
	}
	if _, has := m["empty"]; has {
		t.Errorf("omitempty empty field should be dropped")
	}
	if _, has := m["-"]; has {
		t.Errorf(`json:"-" should be dropped`)
	}

	// Map (with string-convertible keys).
	got = walkForTemplate(reflect.ValueOf(map[string]int{"a": 1, "b": 2}))
	mm, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("map → map; got %v", got)
	}
	if mm["a"] != 1 {
		t.Errorf("map[a] = %v; want 1", mm["a"])
	}

	// Slice.
	got = walkForTemplate(reflect.ValueOf([]int{1, 2, 3}))
	sl, ok := got.([]any)
	if !ok || len(sl) != 3 {
		t.Errorf("slice → []any of len 3; got %v", got)
	}
}

// TestFieldJSONName_AllParseArms pins the JSON-tag parser: name from
// tag, name as field name when no tag, dash-skip, omitempty, omitzero.
func TestFieldJSONName_AllParseArms(t *testing.T) {
	t.Parallel()
	type s struct {
		Tagged   string `json:"tagged"`
		Omit     string `json:"omit,omitempty"`
		OmitZero string `json:"omitz,omitzero"`
		Skip     string `json:"-"`
		NoTag    string
		Other    string `json:"-,"`
	}
	t1 := reflect.TypeOf(s{})

	cases := []struct {
		field    string
		wantName string
		wantOmit bool
		wantSkip bool
	}{
		{"Tagged", "tagged", false, false},
		{"Omit", "omit", true, false},
		{"OmitZero", "omitz", true, false},
		{"Skip", "", false, true},
		{"NoTag", "notag", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			f, _ := t1.FieldByName(tc.field)
			name, omit, skip := fieldJSONName(f)
			if name != tc.wantName || omit != tc.wantOmit || skip != tc.wantSkip {
				t.Errorf("got (%q, %v, %v); want (%q, %v, %v)",
					name, omit, skip, tc.wantName, tc.wantOmit, tc.wantSkip)
			}
		})
	}
	// The `json:"-,"` form is NOT a skip — it's a literal field name "-".
	f, _ := t1.FieldByName("Other")
	name, _, skip := fieldJSONName(f)
	if skip {
		t.Errorf(`json:"-,"  should NOT skip`)
	}
	_ = name
}

// TestTmplCtxValues_Populated pins the success path of the values
// projector by constructing an IRGraph with a valid IRPayload.
func TestTmplCtxValues_Populated(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	// ValuesMapping uses json.RawMessage payloads.
	graph.Spec.ValuesMapping.Set("node-x", []byte(`{"k":"v"}`))
	got := tmplCtxValues(graph, "node-x")
	if got.Len() == 0 {
		t.Errorf("populated values should not be empty; got %v", got)
	}
}

// TestTmplCtxInfra_Populated pins the success path of the infra
// projector.
func TestTmplCtxInfra_Populated(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	graph.Spec.InfraMapping.Set("node-x", []byte(`{"size":"small"}`))
	got := tmplCtxInfra(graph, "node-x")
	if got.Len() == 0 {
		t.Errorf("populated infra should not be empty; got %v", got)
	}
}

// TestEvaluateBoundaryFRTemplates_LocalsAndProvides pins the success
// path: locals + provides both evaluate; state.Provides is populated
// at the key.
func TestEvaluateBoundaryFRTemplates_LocalsAndProvides(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "test-fr"
	fr.Spec.Locals.Set("L1", "literal-value")
	fr.Spec.Provides.Set("P1", "{{ .values.something }}")
	frCtx := &types.TemplateContext{}
	frCtx.Values.Set("something", "computed")
	state := &BoundaryFRState{
		Provides: map[boundaryFRKey]map[string]any{},
	}
	key := boundaryFRKey{BoundaryPath: "infra", FRInstance: "fr-i"}
	if err := evaluateBoundaryFRTemplates(fr, "fr-i", frCtx, state, key); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if frCtx.Locals.Len() != 1 {
		t.Errorf("locals not populated; got %v", frCtx.Locals)
	}
	if state.Provides[key]["P1"] != "computed" {
		t.Errorf("provides not populated; got %v", state.Provides[key])
	}
}

// TestEvaluateBoundaryFRTemplates_LocalsError pins the error branch
// when a local template fails to evaluate.
func TestEvaluateBoundaryFRTemplates_LocalsError(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "test-fr"
	fr.Spec.Locals.Set("bad", "{{ .missing | required \"forced fail\" }}")
	frCtx := &types.TemplateContext{}
	state := &BoundaryFRState{Provides: map[boundaryFRKey]map[string]any{}}
	key := boundaryFRKey{BoundaryPath: "p", FRInstance: "i"}
	err := evaluateBoundaryFRTemplates(fr, "i", frCtx, state, key)
	if err == nil {
		t.Errorf("expected error from required-fail template")
	}
}

// TestValidateBoundaryScopeFRTemplate_AllBranches pins nil, parse-fail,
// allowed-field, rejected-field paths.
func TestValidateBoundaryScopeFRTemplate_AllBranches(t *testing.T) {
	t.Parallel()
	if err := validateBoundaryScopeFRTemplate(nil); err != nil {
		t.Errorf("nil fr: %v", err)
	}
	// FR with allowed fields.
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "fr"
	fr.Spec.ValuesMapping = types.ValuesMapping{Inline: "{{ .boundary.name }}"}
	if err := validateBoundaryScopeFRTemplate(fr); err != nil {
		t.Errorf("allowed fields should pass: %v", err)
	}
	// FR with rejected fields (any per-component pillar reference).
	fr2 := &types.FeatureRecipe{}
	fr2.Metadata.Name = "fr2"
	fr2.Spec.InjectMapping = "{{ .context.inject.env.K }}"
	err := validateBoundaryScopeFRTemplate(fr2)
	if err == nil {
		t.Errorf("rejected field should fail")
	}
	// Parse failure must propagate, not skip. injectMapping is parsed only
	// here for a boundary-scope FR (the dispatch never evaluates it), so a
	// swallowed parse error would vanish silently.
	fr3 := &types.FeatureRecipe{}
	fr3.Metadata.Name = "fr3"
	fr3.Spec.InjectMapping = "{{ .boundary.name " // unterminated action
	if err := validateBoundaryScopeFRTemplate(fr3); err == nil {
		t.Errorf("a malformed inline template must propagate a parse error, not be skipped")
	}
	// workload: and artifacts: inline templates are subject to the same
	// boundary-scope allowlist. A workload referencing only .boundary.* passes.
	frWL := &types.FeatureRecipe{}
	frWL.Metadata.Name = "frWL"
	frWL.Spec.Workload = types.WorkloadTemplate{Inline: "image: app:{{ .boundary.name }}"}
	if err := validateBoundaryScopeFRTemplate(frWL); err != nil {
		t.Errorf("boundary-scope workload with .boundary.* should pass: %v", err)
	}
	// A workload referencing a per-component-only field is rejected.
	frWLBad := &types.FeatureRecipe{}
	frWLBad.Metadata.Name = "frWLBad"
	frWLBad.Spec.Workload = types.WorkloadTemplate{Inline: "image: app:{{ .context.instance_id }}"}
	if err := validateBoundaryScopeFRTemplate(frWLBad); !errors.Is(err, ErrFRTemplateInvalidField) {
		t.Errorf("boundary-scope workload referencing .context.* must be rejected, got: %v", err)
	}
	// An inline artifact referencing a per-component-only field is rejected.
	frArtBad := &types.FeatureRecipe{}
	frArtBad.Metadata.Name = "frArtBad"
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("cm.yaml", types.ArtifactEntry{Inline: "data: {{ .attachment_name }}"})
	frArtBad.Spec.Artifacts = art
	if err := validateBoundaryScopeFRTemplate(frArtBad); !errors.Is(err, ErrFRTemplateInvalidField) {
		t.Errorf("boundary-scope artifact referencing .attachment_name must be rejected, got: %v", err)
	}
}
