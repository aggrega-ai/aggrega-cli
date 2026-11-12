// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package stepwalk

import (
	"context"
	"encoding/json"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestWalk_StepsChannelCrossStepDataFlow proves a step's returned outputs
// reach a later step's `with` through the .steps.<id>.outputs channel: step A
// returns outputs {k: v}, step B's `with` references .steps.A.outputs.k, and
// B's rendered step input carries A's value. The eval stub resolves the
// reference by reading the live template context the walk mutates, so the test
// exercises the real per-node context pointer, not a stand-in.
func TestWalk_StepsChannelCrossStepDataFlow(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}

	var aOut types.OrderedMap[any]
	aOut.Set("k", "from-A")

	inv := newScripted()
	inv.outputs["A"] = frp.FRPStepOutput{Outputs: aOut}
	inv.outputs["B"] = frp.FRPStepOutput{}

	// The eval stub resolves B's `with` against the context the walk hands it —
	// proving A's output was recorded into .steps before B evaluated.
	eval := func(name, text string, data any) (string, error) {
		if !strings.Contains(text, ".steps.A.outputs.k") {
			return text, nil
		}
		tc := data.(*types.TemplateContext)
		so, ok := tc.Steps.Get("A")
		if !ok {
			return "", nil
		}
		v, _ := so.Outputs.Get("k")
		return `{"received":"` + v + `"}`, nil
	}

	steps := []types.PipelineStep{
		{ID: "A", Plugin: "p", Function: "f"},
		{ID: "B", Plugin: "p", Function: "f", With: "{{ .steps.A.outputs.k }}"},
	}

	withCtx := &types.TemplateContext{}
	_, err := Walk(context.Background(), docs, steps, inv.invoke, withCtx, renderctx.RenderContext{}, eval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// The channel is populated on the per-node context.
	so, ok := withCtx.Steps.Get("A")
	if !ok {
		t.Fatalf(".steps.A absent after walk")
	}
	if got, _ := so.Outputs.Get("k"); got != "from-A" {
		t.Errorf(".steps.A.outputs.k = %q, want from-A", got)
	}

	// B's rendered step input carries A's value.
	bIn := receivedInput(t, inv.received["B"])
	got, _ := bIn.StepInput.Get("received")
	if got != "from-A" {
		t.Errorf("B step input received = %v, want from-A", got)
	}
}

// TestWalk_StepsChannelSkips proves the two no-contribution cases leave the
// channel untouched: a committed step with an empty ID, and a committed step
// with no outputs. A rolled-back optional step is covered by the applied-ops
// test; here the focus is the empty-ID and no-outputs guards.
func TestWalk_StepsChannelSkips(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}

	var out types.OrderedMap[any]
	out.Set("k", "v")

	inv := newScripted()
	inv.outputs[""] = frp.FRPStepOutput{Outputs: out} // empty-ID step: no channel entry
	inv.outputs["has-id"] = frp.FRPStepOutput{}       // no outputs: no channel entry

	steps := []types.PipelineStep{
		{ID: "", Plugin: "p", Function: "f"},
		{ID: "has-id", Plugin: "p", Function: "f"},
	}

	withCtx := &types.TemplateContext{}
	_, err := Walk(context.Background(), docs, steps, inv.invoke, withCtx, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if withCtx.Steps.Len() != 0 {
		t.Errorf("Steps channel = %d entries, want 0 (empty-ID and no-outputs steps contribute nothing)", withCtx.Steps.Len())
	}
}

// TestWalk_StepsChannelRolledBackStepDropsOutputs proves a soft-failed
// optional step records no outputs — the channel reflects only committed
// steps, mirroring the applied-ops record.
func TestWalk_StepsChannelRolledBackStepDropsOutputs(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}

	var out types.OrderedMap[any]
	out.Set("k", "should-not-appear")

	inv := newScripted()
	// Optional step returns outputs but its patch targets an absent unit, so it
	// soft-fails and rolls back — its outputs must not reach the channel.
	inv.outputs["opt"] = frp.FRPStepOutput{
		Outputs: out,
		Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "missing.yaml", "add", "/k", "v")},
	}
	steps := []types.PipelineStep{{ID: "opt", Plugin: "p", Function: "f", Optional: true}}

	withCtx := &types.TemplateContext{}
	res, err := Walk(context.Background(), docs, steps, inv.invoke, withCtx, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected a soft-fail warning")
	}
	if withCtx.Steps.Len() != 0 {
		t.Errorf("Steps channel = %d entries, want 0 (a rolled-back step records no outputs)", withCtx.Steps.Len())
	}
}

// TestStringifyStepOutput pins the injection-boundary stringify: scalars land
// in their natural unquoted form, a JSON null lands empty, and a composite
// renders as canonical order-preserving JSON so identical input is
// byte-identical.
func TestStringifyStepOutput(t *testing.T) {
	// Values as they arrive from the wire: OrderedMap[any] decodes numbers as
	// json.Number, nested objects as *OrderedMap[any], arrays as []any.
	num := decodeAny(t, `8080`)
	obj := decodeAny(t, `{"b":2,"a":1}`) // author order b,a is preserved on the wire
	arr := decodeAny(t, `[1,"two",true]`)

	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string", "plain", "plain"},
		{"bool", true, "true"},
		{"number", num, "8080"},
		{"nil", nil, ""},
		{"object-order-preserved", obj, `{"b":2,"a":1}`},
		{"array", arr, `[1,"two",true]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := stringifyStepOutput(c.in)
			if err != nil {
				t.Fatalf("stringifyStepOutput(%v): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("stringifyStepOutput = %q, want %q", got, c.want)
			}
		})
	}
}

// decodeAny unmarshals a JSON literal the way the wire outputs decode — into
// the same any shapes OrderedMap[any] produces (json.Number, *OrderedMap[any],
// []any) — so the stringify test exercises the real value types.
func decodeAny(t *testing.T, lit string) any {
	t.Helper()
	var m types.OrderedMap[any]
	if err := json.Unmarshal([]byte(`{"v":`+lit+`}`), &m); err != nil {
		t.Fatalf("decode %q: %v", lit, err)
	}
	v, _ := m.Get("v")
	return v
}
