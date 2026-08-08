// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Walk is the shared driver for both Recipe and FeatureRecipe pipelines: both
// carry the same []types.PipelineStep and reach the guest through the same
// Walk. The `when` gate therefore has a single implementation exercised by
// every walk-level test below; there is no per-surface gate to test
// separately. No higher-level end-to-end test is added here because the
// Recipe/FeatureRecipe entry paths require a real Wasm guest.

// realTemplateEval compiles and executes the step text as a Go template against
// the data value, so a test can gate a `when` on a field of the real
// TemplateContext the walk threads through.
func realTemplateEval(name, text string, data any) (string, error) {
	tpl, err := template.New(name).Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// errOnWhen returns an evaluator that fails whenever it is asked to render the
// given text (the step's `when` body), so a test can drive the render-error
// halt path while leaving other templates rendering verbatim.
func errOnWhen(whenText string) TemplateEvaluator {
	return func(name, text string, data any) (string, error) {
		if text == whenText {
			return "", fmt.Errorf("boom")
		}
		return text, nil
	}
}

// TestWalk_WhenFalseSkipsPassThrough proves a falsy `when` skips the step as a
// pass-through: the guest is never invoked and the next step sees the pre-skip
// tree (the skipped step contributed no patch).
func TestWalk_WhenFalseSkipsPassThrough(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	// If s1 ran it would overwrite x; it must not run.
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "SHOULD_NOT_APPEAR")}}
	inv.outputs["s2"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "final")}}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "p", Function: "f", When: "false"},
		{ID: "s2", Plugin: "p", Function: "f"},
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if _, ran := inv.received["s1"]; ran {
		t.Errorf("skipped step's guest was invoked")
	}
	// s2's input tree must show the pre-skip value (s1 contributed nothing).
	var s2in FRPStepInput
	if err := json.Unmarshal(inv.received["s2"], &s2in); err != nil {
		t.Fatalf("unmarshal s2 payload: %v", err)
	}
	if got := getString(t, s2in.Documents.Own, "a.yaml", "x"); got != "0" {
		t.Errorf("s2 saw x=%q, want 0 (skipped step must contribute no patch)", got)
	}
	if got := getString(t, res.Documents.Own, "a.yaml", "x"); got != "final" {
		t.Errorf("final x=%q, want final", got)
	}
}

// TestWalk_WhenTrueAndAbsentRun proves a truthy `when` and an absent `when`
// both run the step normally.
func TestWalk_WhenTrueAndAbsentRun(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "1")}}
	inv.outputs["s2"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "add", "/y", "2")}}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "p", Function: "f", When: "true"}, // explicit truthy
		{ID: "s2", Plugin: "p", Function: "f"},               // absent when
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := getString(t, res.Documents.Own, "a.yaml", "x"); got != "1" {
		t.Errorf("truthy-when step did not run: x=%q", got)
	}
	if got := getString(t, res.Documents.Own, "a.yaml", "y"); got != "2" {
		t.Errorf("absent-when step did not run: y=%q", got)
	}
}

// TestWalk_WhenFalseNoPrecedingEntry proves a skipped step leaves no
// PrecedingStep entry for a downstream step to observe.
func TestWalk_WhenFalseNoPrecedingEntry(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{SideEffects: []SideEffect{{Kind: "emit_file", File: &SideEffectFile{Path: "f.txt", Content: "c"}}}}
	inv.outputs["s2"] = FRPStepOutput{}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "plugin-one", Function: "f", When: "false"},
		{ID: "s2", Plugin: "plugin-two", Function: "f"},
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.SideEffects) != 0 {
		t.Errorf("skipped step contributed side effects: %+v", res.SideEffects)
	}
	var s2in FRPStepInput
	if err := json.Unmarshal(inv.received["s2"], &s2in); err != nil {
		t.Fatalf("unmarshal s2 payload: %v", err)
	}
	if len(s2in.PrecedingSteps) != 0 {
		t.Errorf("s2 saw %d preceding steps, want 0 (skipped step must not record)", len(s2in.PrecedingSteps))
	}
}

// TestWalk_WhenComposesWithOptional proves `when` and `optional` are
// orthogonal: a truthy-when optional step that fails still soft-fails to a
// warning, while a falsy-when optional step is simply skipped before execution.
func TestWalk_WhenComposesWithOptional(t *testing.T) {
	// Case A: when:"true" + optional:true + fails -> soft-fail warning.
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.fail["s1"] = errors.New("guest trap")
	res, err := Walk(context.Background(), docs,
		[]types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", When: "true", Optional: true}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("truthy-when optional failure should warn, not halt: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected a soft-fail warning for the optional step")
	}

	// Case B: when:"false" + optional:true -> skipped before execution.
	inv2 := newScripted()
	inv2.fail["s1"] = errors.New("would trap if run")
	res2, err := Walk(context.Background(), docs,
		[]types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", When: "false", Optional: true}},
		inv2.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("falsy-when optional step should skip cleanly: %v", err)
	}
	if _, ran := inv2.received["s1"]; ran {
		t.Errorf("falsy-when step was invoked; when-gate must short-circuit before execution")
	}
	if len(res2.Warnings) != 0 {
		t.Errorf("skipped step raised warnings: %+v", res2.Warnings)
	}
}

// TestWalk_WhenErrorHaltsEvenOptional proves a `when` that render-errors halts
// the walk loudly with ErrPipelineStepFailed, independent of Optional.
func TestWalk_WhenErrorHaltsEvenOptional(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	whenText := "{{ .broken"
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", When: whenText, Optional: true}}

	_, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, errOnWhen(whenText))
	if !errors.Is(err, ErrPipelineStepFailed()) {
		t.Fatalf("err = %v, want ErrPipelineStepFailed", err)
	}
	if _, ran := inv.received["s1"]; ran {
		t.Errorf("guest invoked despite a when-render error")
	}
}

// TestWalk_WhenSkipDeterministicVsAbsent proves a step skipped via a falsy
// `when` is a no-op: the result document set is byte-identical to a walk where
// the step is genuinely absent from the slice.
func TestWalk_WhenSkipDeterministicVsAbsent(t *testing.T) {
	mk := func() DocumentSet { return DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)} }
	s2 := types.PipelineStep{ID: "s2", Plugin: "p", Function: "f"}

	invA := newScripted()
	invA.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "unused")}}
	invA.outputs["s2"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "1")}}
	withSkip, err := Walk(context.Background(), mk(),
		[]types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", When: "false"}, s2},
		invA.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk with skip: %v", err)
	}

	invB := newScripted()
	invB.outputs["s2"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "1")}}
	absent, err := Walk(context.Background(), mk(),
		[]types.PipelineStep{s2},
		invB.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk without step: %v", err)
	}

	a, _ := json.Marshal(withSkip.Documents)
	b, _ := json.Marshal(absent.Documents)
	if string(a) != string(b) {
		t.Errorf("skip is not a no-op:\n skip   %s\n absent %s", a, b)
	}
}

// TestWalk_WhenReadsTemplateContext proves the `when` gate renders against the
// same TemplateContext the walk threads through, gating on one of its fields.
func TestWalk_WhenReadsTemplateContext(t *testing.T) {
	when := `{{ eq .AttachmentName "go" }}`
	mkSteps := func() []types.PipelineStep {
		return []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", When: when}}
	}
	newInv := func() *scriptedInvoker {
		inv := newScripted()
		inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "ran")}}
		return inv
	}

	// AttachmentName == "go" -> gate truthy -> step runs.
	invRun := newInv()
	resRun, err := Walk(context.Background(), DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}, mkSteps(),
		invRun.invoke, &types.TemplateContext{AttachmentName: "go"}, renderctx.RenderContext{}, realTemplateEval)
	if err != nil {
		t.Fatalf("Walk (gate truthy): %v", err)
	}
	if got := getString(t, resRun.Documents.Own, "a.yaml", "x"); got != "ran" {
		t.Errorf("gate truthy on context field: x=%q, want ran", got)
	}

	// AttachmentName != "go" -> gate falsy -> step skipped.
	invSkip := newInv()
	resSkip, err := Walk(context.Background(), DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}, mkSteps(),
		invSkip.invoke, &types.TemplateContext{AttachmentName: "stop"}, renderctx.RenderContext{}, realTemplateEval)
	if err != nil {
		t.Fatalf("Walk (gate falsy): %v", err)
	}
	if _, ran := invSkip.received["s1"]; ran {
		t.Errorf("step ran despite a falsy context-driven gate")
	}
	if got := getString(t, resSkip.Documents.Own, "a.yaml", "x"); got != "0" {
		t.Errorf("gate falsy: x=%q, want 0 (unchanged)", got)
	}
}
