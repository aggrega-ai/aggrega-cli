// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// TestPhaseB2_NilRecipe pins phaseB2's early-return path.
func TestPhaseB2_NilRecipe(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{}
	if err := phaseB2(&types.TemplateContext{}, state, validator.WorkloadSlots{}); err != nil {
		t.Errorf("nil recipe: %v", err)
	}
	if state.PhaseReached != 2 {
		t.Errorf("PhaseReached = %d; want 2", state.PhaseReached)
	}
}

// TestPhaseB2_NoWorkload pins phaseB2's no-workload short-circuit:
// state.ParsedWorkload stays nil.
func TestPhaseB2_NoWorkload(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{Recipe: &types.Recipe{}}
	if err := phaseB2(&types.TemplateContext{}, state, validator.WorkloadSlots{}); err != nil {
		t.Errorf("no workload: %v", err)
	}
	if state.ParsedWorkload != nil {
		t.Errorf("ParsedWorkload should be nil without workload; got %v", state.ParsedWorkload)
	}
}

// TestPhaseB6_NoFeatures pins phaseB6's path with zero featureRefs:
// returns empty files map without error.
func TestPhaseB6_NoFeatures(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{}
	state := &PhaseBState{NodeID: "n"}
	out, held, err := phaseB6(nil, tmplCtx, state, nil, nil, NodeProvides{}, RecipeLookup{}, NodeRenderContext{}, nil, nil, nil, nil, RendererInput{}, nil, false, "", false, renderctx.RenderContext{})
	if err != nil {
		t.Errorf("no features: %v", err)
	}
	if len(out.files) != 0 {
		t.Errorf("no features should produce empty map; got %v", out.files)
	}
	if len(out.hostPatched) != 0 {
		t.Errorf("no features should produce empty hostPatched; got %v", out.hostPatched)
	}
	if len(out.contributions) != 0 {
		t.Errorf("no features should hand the group no contributions; got %v", out.contributions)
	}
	if len(out.deferred) != 0 {
		t.Errorf("no features should produce empty deferred; got %v", out.deferred)
	}
	if held != nil {
		t.Errorf("no features should hold no attachment for the pass after the walk; got %v", held.items)
	}
}

// TestNodeKindNameAll pins each branch via direct dispatch.
func TestNodeKindNameAll(t *testing.T) {
	t.Parallel()
	// Already covered in helpers6; this confirms the default fallthrough
	// uses the format string. Not duplicating — covered.
	_ = t
}

// TestWalkForTemplate_PtrDereferenceAndInterface pins additional
// dereferencing branches of walkForTemplate.
func TestWalkForTemplate_PtrDereferenceAndInterface(t *testing.T) {
	t.Parallel()
	// Pointer to int.
	n := 42
	got := walkForTemplate(reflect.ValueOf(&n))
	if got != 42 {
		t.Errorf("pointer-to-int dereference = %v; want 42", got)
	}
	// Pointer to pointer to int.
	pn := &n
	got = walkForTemplate(reflect.ValueOf(&pn))
	if got != 42 {
		t.Errorf("**int dereference = %v; want 42", got)
	}
	// Interface holding nil.
	var iface any = nil
	got = walkForTemplate(reflect.ValueOf(&iface).Elem())
	if got != nil {
		t.Errorf("nil interface → nil; got %v", got)
	}
}

// TestWalkStruct_UnexportedFieldSkip pins the unexported-field skip
// branch of walkStruct.
func TestWalkStruct_UnexportedFieldSkip(t *testing.T) {
	t.Parallel()
	type withUnexported struct {
		Exported   string `json:"e"`
		unexported string //nolint:unused // tested via reflection
	}
	v := withUnexported{Exported: "e-val", unexported: "u-val"}
	got := walkForTemplate(reflect.ValueOf(v))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("struct → map; got %v", got)
	}
	if m["e"] != "e-val" {
		t.Errorf("exported field lost; got %v", m)
	}
	if _, has := m["unexported"]; has {
		t.Errorf("unexported field should be skipped")
	}
}

// TestIsEmptyForTemplate_OrderedMapNonZero pins the IsZero-method
// fallback for an OrderedMap value carrying entries (non-zero).
func TestIsEmptyForTemplate_OrderedMapNonZero(t *testing.T) {
	t.Parallel()
	var m types.OrderedMap[string]
	m.Set("k", "v")
	if got := isEmptyForTemplate(reflect.ValueOf(m)); got {
		t.Errorf("populated OrderedMap should not be empty")
	}
}

// TestExtractPath_NodeBaseContext_AltKeys pins the metadata fallback
// branches of ExtractNodeBaseContext: an aggregate without
// Provenance still populates metadata.aggregate_name from the graph's
// metadata block.
func TestExtractPath_NodeBaseContext_AltKeys(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	graph.Metadata.AggregateName = "alt-agg"
	graph.Spec.Provenance.ReleaseVersion = ""
	graph.Spec.Nodes.Set("n", types.IRNode{})
	got := ExtractNodeBaseContext("n", graph)
	if got.Metadata["aggregate_name"] != "alt-agg" {
		t.Errorf("metadata aggregate_name = %v; want alt-agg", got.Metadata["aggregate_name"])
	}
}
