// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/jsonpatch"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// rawMap builds a keyed RawMessage root from key/JSON-literal pairs in
// order.
func rawMap(pairs ...string) types.OrderedMap[json.RawMessage] {
	var m types.OrderedMap[json.RawMessage]
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], json.RawMessage(pairs[i+1]))
	}
	return m
}

// scriptedInvoker returns canned outputs per step id, recording the
// payloads it received so a test can inspect what the walk handed the
// guest. A step id absent from outputs returns an empty output.
type scriptedInvoker struct {
	outputs  map[string]FRPStepOutput
	fail     map[string]error // step id -> invoke error
	received map[string][]byte
}

func newScripted() *scriptedInvoker {
	return &scriptedInvoker{
		outputs:  map[string]FRPStepOutput{},
		fail:     map[string]error{},
		received: map[string][]byte{},
	}
}

func (s *scriptedInvoker) invoke(_ context.Context, step types.PipelineStep, payload []byte) ([]byte, error) {
	s.received[step.ID] = payload
	if err, ok := s.fail[step.ID]; ok {
		return nil, err
	}
	out := s.outputs[step.ID]
	return json.Marshal(out)
}

func patch(scope DocumentScope, key, op, path string, value any) JSONPatchOp {
	return JSONPatchOp{Scope: scope, Key: key, Op: op, Path: path, Value: value}
}

// getString resolves a JSON string at a flat top-level key of a document
// in the set.
func getString(t *testing.T, root types.OrderedMap[json.RawMessage], docKey, field string) string {
	t.Helper()
	raw, ok := root.Get(docKey)
	if !ok {
		t.Fatalf("document %q absent", docKey)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal %q: %v", docKey, err)
	}
	s, _ := obj[field].(string)
	return s
}

func TestWalk_CumulativeMultiStep(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "1")}}
	inv.outputs["s2"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "add", "/y", "2")}}

	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "p1", Function: "f"},
		{ID: "s2", Plugin: "p2", Function: "f"},
	}
	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := getString(t, res.Documents.Own, "a.yaml", "x"); got != "1" {
		t.Errorf("x = %q, want 1 (step 1 patch)", got)
	}
	if got := getString(t, res.Documents.Own, "a.yaml", "y"); got != "2" {
		t.Errorf("y = %q, want 2 (step 2 patch, cumulative)", got)
	}
}

func TestWalk_OwnAndHostInOneStep(t *testing.T) {
	docs := DocumentSet{
		Own:  rawMap("own.yaml", `{"o":"0"}`),
		Host: rawMap("host.yaml", `{"h":"0"}`),
	}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{
		patch(DocumentScopeOwn, "own.yaml", "replace", "/o", "own-val"),
		patch(DocumentScopeHost, "host.yaml", "replace", "/h", "host-val"),
	}}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := getString(t, res.Documents.Own, "own.yaml", "o"); got != "own-val" {
		t.Errorf("own patch landed wrong: o = %q", got)
	}
	if got := getString(t, res.Documents.Host, "host.yaml", "h"); got != "host-val" {
		t.Errorf("host patch landed wrong: h = %q", got)
	}
}

func TestWalk_AbsentTargetStrictHalt(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	snapBytes, _ := json.Marshal(docs)

	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "missing.yaml", "add", "/k", "v")}}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}}

	_, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if !errors.Is(err, ErrPipelineTargetAbsent) {
		t.Fatalf("err = %v, want ErrPipelineTargetAbsent", err)
	}
	// Input docs untouched (Walk takes DocumentSet by value).
	gotBytes, _ := json.Marshal(docs)
	if string(gotBytes) != string(snapBytes) {
		t.Errorf("caller's set mutated on strict halt:\n got %s\nwant %s", gotBytes, snapBytes)
	}
}

func TestWalk_AbsentTargetOptionalWarnContinue(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "missing.yaml", "add", "/k", "v")}}
	inv.outputs["s2"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "after")}}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "p", Function: "f", Optional: true},
		{ID: "s2", Plugin: "p", Function: "f"},
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if err != nil {
		t.Fatalf("Walk: optional step should not halt: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected a loud warning for the skipped optional step")
	}
	if got := getString(t, res.Documents.Own, "a.yaml", "x"); got != "after" {
		t.Errorf("following step did not apply: x = %q, want after", got)
	}
}

// TestWalk_SnapshotRestoreByteIdentical is the arbiter for the snapshot
// approach: a step whose second patch fails after its first succeeded must
// leave the set byte-identical to its pre-step state.
func TestWalk_SnapshotRestoreByteIdentical(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0","y":"0"}`)}
	before, _ := json.Marshal(docs)

	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{
		patch(DocumentScopeOwn, "a.yaml", "replace", "/x", "mutated"), // succeeds
		patch(DocumentScopeOwn, "a.yaml", "replace", "/nope", "v"),    // fails: member absent
	}}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}}

	_, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if err == nil {
		t.Fatal("expected a strict halt from the failing second patch")
	}
	after, _ := json.Marshal(docs)
	if string(after) != string(before) {
		t.Errorf("whole-step rollback failed:\n before %s\n  after %s", before, after)
	}
}

func TestWalk_ErrorDiagnosticHalts(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Diagnostics: []renderer.Diagnostic{
		{Severity: "error", Code: "bad", Message: "guest refused"},
	}}

	// strict
	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if !errors.Is(err, ErrPipelineStepFailed) {
		t.Fatalf("strict: err = %v, want ErrPipelineStepFailed", err)
	}

	// optional
	res, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", Optional: true}},
		inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if err != nil {
		t.Fatalf("optional: error diagnostic should warn, not halt: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("optional error diagnostic: expected a warning")
	}
}

func TestWalk_WithEvalIntoStepInput(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", With: "key: {{ .values.x }}"}}

	withCtx := &types.TemplateContext{}
	withCtx.Values.Set("x", "rendered-value")

	_, err := Walk(context.Background(), docs, steps, inv.invoke, withCtx, renderer.RenderContext{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	var got FRPStepInput
	if err := json.Unmarshal(inv.received["s1"], &got); err != nil {
		t.Fatalf("unmarshal received payload: %v", err)
	}
	v, ok := got.StepInput.Get("key")
	if !ok {
		t.Fatalf("step input missing rendered `with` key; payload=%s", inv.received["s1"])
	}
	if s, _ := v.(string); s != "rendered-value" {
		t.Errorf("step input key = %v, want rendered-value", v)
	}
}

func TestWalk_SideEffectAndPrecedingThreading(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{SideEffects: []SideEffect{{Kind: "emit_file", File: &SideEffectFile{Path: "f.txt", Content: "c"}}}}
	inv.outputs["s2"] = FRPStepOutput{}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "plugin-one", Function: "f"},
		{ID: "s2", Plugin: "plugin-two", Function: "f"},
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.SideEffects) != 1 || res.SideEffects[0].Kind != "emit_file" {
		t.Errorf("side effects not accumulated: %+v", res.SideEffects)
	}
	// Step 2 must have seen step 1 in PrecedingSteps with its side effect.
	var s2in FRPStepInput
	if err := json.Unmarshal(inv.received["s2"], &s2in); err != nil {
		t.Fatalf("unmarshal s2 payload: %v", err)
	}
	if len(s2in.PrecedingSteps) != 1 {
		t.Fatalf("s2 preceding steps = %d, want 1", len(s2in.PrecedingSteps))
	}
	if s2in.PrecedingSteps[0].PluginURI != "plugin-one" {
		t.Errorf("preceding plugin = %q, want plugin-one", s2in.PrecedingSteps[0].PluginURI)
	}
	if len(s2in.PrecedingSteps[0].SideEffects) != 1 {
		t.Errorf("preceding side effects not threaded: %+v", s2in.PrecedingSteps[0])
	}
}

func TestWalk_InvokeTrapStrictAndOptional(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.fail["s1"] = errors.New("guest trap")

	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if !errors.Is(err, ErrPipelineStepFailed) {
		t.Fatalf("trap strict: err = %v, want ErrPipelineStepFailed", err)
	}

	res, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", Optional: true}},
		inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if err != nil {
		t.Fatalf("trap optional: should warn not halt: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("trap optional: expected warning")
	}
}

// TestWalk_FailedTestOpMatchesEngineSentinel confirms the %w wrap keeps the
// engine's ErrTestFailed reachable through the walk's umbrella sentinel.
func TestWalk_FailedTestOpMatchesEngineSentinel(t *testing.T) {
	docs := DocumentSet{Own: rawMap("a.yaml", `{"x":"actual"}`)}
	inv := newScripted()
	inv.outputs["s1"] = FRPStepOutput{Patches: []JSONPatchOp{patch(DocumentScopeOwn, "a.yaml", "test", "/x", "expected")}}

	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		inv.invoke, &types.TemplateContext{}, renderer.RenderContext{})
	if !errors.Is(err, jsonpatch.ErrTestFailed) {
		t.Fatalf("err = %v, want it to wrap jsonpatch.ErrTestFailed", err)
	}
	if !errors.Is(err, ErrPipelineStepFailed) {
		t.Errorf("err = %v, want it to also match ErrPipelineStepFailed", err)
	}
}
