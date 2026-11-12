// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package stepwalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"slices"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/jsonpatch"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// noEval is a stub TemplateEvaluator for steps whose `with` is empty (the
// short-circuit path in evalWith never calls it). Tests that exercise a
// non-empty `with` and need the renderer's real evaluator live in the
// external test package, which may import the renderer without a cycle.
func noEval(name, text string, data any) (string, error) { return text, nil }

// rawMap builds a keyed unit root from key/JSON-literal pairs in order. Each
// value becomes a structured unit (a Tree-bearing PATCH target) tokened by
// its key — the shape the walk operates on.
func rawMap(pairs ...string) types.OrderedMap[frp.Unit] {
	var m types.OrderedMap[frp.Unit]
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], frp.Unit{Token: pairs[i], MediaType: "application/yaml", Tree: json.RawMessage(pairs[i+1])})
	}
	return m
}

// opaqueMap builds a keyed unit root whose entries are opaque (no Tree) —
// SELECT/EMIT targets that halt a PATCH. Each pair is key/media-type.
func opaqueMap(pairs ...string) types.OrderedMap[frp.Unit] {
	var m types.OrderedMap[frp.Unit]
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], frp.Unit{Token: pairs[i], MediaType: pairs[i+1], Bytes: []byte("opaque-bytes")})
	}
	return m
}

// scriptedInvoker returns canned outputs per step id, recording the
// payloads it received so a test can inspect what the walk handed the
// guest. A step id absent from outputs returns an empty output.
type scriptedInvoker struct {
	outputs map[string]frp.FRPStepOutput
	// raw scripts a step's reply as the guest's own wire bytes rather than as a Go
	// value the host encodes. It is what lets a test put a value on the wire that
	// the host's own walk would never produce — an undeclared severity, say — so
	// the decode boundary is exercised with what a real guest could send. A step
	// present in raw wins over one present in outputs.
	raw      map[string][]byte
	fail     map[string]error // step id -> invoke error
	received map[string][]byte
	// inputs records the step input the walk handed over beside the payload.
	inputs map[string]types.OrderedMap[any]
	// check is the outputs check a step's invocation returns, as an invoker that
	// read the export's declared output contract returns one.
	check map[string]OutputsCheck
}

func newScripted() *scriptedInvoker {
	return &scriptedInvoker{
		outputs:  map[string]frp.FRPStepOutput{},
		raw:      map[string][]byte{},
		fail:     map[string]error{},
		received: map[string][]byte{},
		inputs:   map[string]types.OrderedMap[any]{},
		check:    map[string]OutputsCheck{},
	}
}

func (s *scriptedInvoker) invoke(_ context.Context, step types.PipelineStep, input types.OrderedMap[any], payload []byte) ([]byte, OutputsCheck, error) {
	s.received[step.ID] = payload
	s.inputs[step.ID] = input
	if err, ok := s.fail[step.ID]; ok {
		return nil, nil, err
	}
	check := s.check[step.ID]
	if b, ok := s.raw[step.ID]; ok {
		return b, check, nil
	}
	out := s.outputs[step.ID]
	raw, err := out.EncodeCBOR()
	return raw, check, err
}

// guestReply encodes out the way a guest built with a plugin kit answers: the
// binary encoding of the step's output shape, written by the kits' own codec from
// the kits' own types rather than by the host's.
func guestReply(t *testing.T, out abi.FRPStepOutput) []byte {
	t.Helper()
	b, err := out.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the guest's reply: %v", err)
	}
	return b
}

// receivedInput decodes the payload the walk handed a step, in the encoding the
// step's guest reads.
func receivedInput(t *testing.T, payload []byte) frp.FRPStepInput {
	t.Helper()
	var in frp.FRPStepInput
	if err := in.DecodeCBORStrict(payload); err != nil {
		t.Fatalf("decode the step's payload: %v", err)
	}
	return in
}

func patch(scope string, key, op, path string, value any) frp.JSONPatchOp {
	return frp.JSONPatchOp{Scope: scope, Key: key, Op: op, Path: path, Value: value}
}

// getString resolves a JSON string at a flat top-level key of a unit's
// tree-view in the set.
func getString(t *testing.T, root types.OrderedMap[frp.Unit], docKey, field string) string {
	t.Helper()
	u, ok := root.Get(docKey)
	if !ok {
		t.Fatalf("document %q absent", docKey)
	}
	var obj map[string]any
	if err := json.Unmarshal(u.Tree, &obj); err != nil {
		t.Fatalf("unmarshal %q: %v", docKey, err)
	}
	s, _ := obj[field].(string)
	return s
}

func TestWalk_CumulativeMultiStep(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "a.yaml", "replace", "/x", "1")}}
	inv.outputs["s2"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "a.yaml", "add", "/y", "2")}}

	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "p1", Function: "f"},
		{ID: "s2", Plugin: "p2", Function: "f"},
	}
	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
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
	docs := frp.DocumentSet{
		Own:  rawMap("own.yaml", `{"o":"0"}`),
		Host: rawMap("host.yaml", `{"h":"0"}`),
	}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{
		patch(frp.DocumentScopeOwn, "own.yaml", "replace", "/o", "own-val"),
		patch(frp.DocumentScopeHost, "host.yaml", "replace", "/h", "host-val"),
	}}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
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
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	snapBytes, _ := json.Marshal(docs)

	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "missing.yaml", "add", "/k", "v")}}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}}

	_, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, frp.ErrPipelineTargetAbsent()) {
		t.Fatalf("err = %v, want frp.ErrPipelineTargetAbsent", err)
	}
	// Input docs untouched (Walk takes DocumentSet by value).
	gotBytes, _ := json.Marshal(docs)
	if string(gotBytes) != string(snapBytes) {
		t.Errorf("caller's set mutated on strict halt:\n got %s\nwant %s", gotBytes, snapBytes)
	}
}

func TestWalk_AbsentTargetOptionalWarnContinue(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "missing.yaml", "add", "/k", "v")}}
	inv.outputs["s2"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "a.yaml", "replace", "/x", "after")}}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "p", Function: "f", Optional: true},
		{ID: "s2", Plugin: "p", Function: "f"},
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
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
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0","y":"0"}`)}
	before, _ := json.Marshal(docs)

	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{
		patch(frp.DocumentScopeOwn, "a.yaml", "replace", "/x", "mutated"), // succeeds
		patch(frp.DocumentScopeOwn, "a.yaml", "replace", "/nope", "v"),    // fails: member absent
	}}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}}

	_, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err == nil {
		t.Fatal("expected a strict halt from the failing second patch")
	}
	after, _ := json.Marshal(docs)
	if string(after) != string(before) {
		t.Errorf("whole-step rollback failed:\n before %s\n  after %s", before, after)
	}
}

func TestWalk_ErrorDiagnosticHalts(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Diagnostics: []renderctx.Diagnostic{
		{Severity: abi.SeverityHalt, Code: "bad", Message: "guest refused"},
	}}

	// strict
	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, frp.ErrPipelineStepFailed()) {
		t.Fatalf("strict: err = %v, want frp.ErrPipelineStepFailed", err)
	}

	// optional
	res, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", Optional: true}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("optional: error diagnostic should warn, not halt: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("optional error diagnostic: expected a warning")
	}
}

func TestWalk_SideEffectAndPrecedingThreading(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{SideEffects: []frp.SideEffect{{Kind: "emit_file", File: &frp.SideEffectFile{Path: "f.txt", Content: "c"}}}}
	inv.outputs["s2"] = frp.FRPStepOutput{}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "plugin-one", Function: "f"},
		{ID: "s2", Plugin: "plugin-two", Function: "f"},
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(res.SideEffects) != 1 || res.SideEffects[0].Kind != "emit_file" {
		t.Errorf("side effects not accumulated: %+v", res.SideEffects)
	}
	// Step 2 must have seen step 1 in PrecedingSteps with its side effect.
	s2in := receivedInput(t, inv.received["s2"])
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
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.fail["s1"] = errors.New("guest trap")

	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, frp.ErrPipelineStepFailed()) {
		t.Fatalf("trap strict: err = %v, want frp.ErrPipelineStepFailed", err)
	}

	res, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", Optional: true}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("trap optional: should warn not halt: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("trap optional: expected warning")
	}
}

// TestWalk_ContractViolationHaltsEvenOptional proves a typed-I/O contract
// violation halts the walk even when the step is Optional, while an ordinary
// step failure on the same Optional step soft-fails with a warning. The
// contract-violation guard is specific: it matches the sentinel, not any error.
func TestWalk_ContractViolationHaltsEvenOptional(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}

	// A contract violation surfaced by the invoker halts even though the step
	// is Optional — the same way a broken predicate does.
	inv := newScripted()
	inv.fail["s1"] = fmt.Errorf("`with` input does not satisfy the declared input contract: %w", frp.ErrPipelineContractViolation())
	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", Optional: true}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Fatalf("contract violation on optional step: err = %v, want halt with frp.ErrPipelineContractViolation", err)
	}

	// A plain failure on the same Optional step soft-fails: the guard does not
	// over-reach to every error.
	inv2 := newScripted()
	inv2.fail["s1"] = errors.New("guest trap")
	res, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", Optional: true}},
		inv2.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("plain failure on optional step should warn not halt: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("plain failure on optional step: expected a warning")
	}
}

// TestWalk_PatchAgainstOpaqueUnitHalts proves a PATCH addressing an opaque
// (no-tree) unit halts with ErrPipelineTargetOpaque rather than silently
// skipping: an opaque unit is a SELECT/EMIT target only, never a PATCH target.
func TestWalk_PatchAgainstOpaqueUnitHalts(t *testing.T) {
	docs := frp.DocumentSet{Own: opaqueMap("blob.bin", "application/octet-stream")}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "blob.bin", "add", "/k", "v")}}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}}

	_, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, frp.ErrPipelineTargetOpaque()) {
		t.Fatalf("err = %v, want frp.ErrPipelineTargetOpaque", err)
	}
}

// TestWalk_AppliedOpsRecordedPerTouchedUnit proves the walk records, per
// scope and token, the ops that successfully edited a unit — the surgical
// reassembly input the caller's codec consumes. A rolled-back optional step
// contributes nothing.
func TestWalk_AppliedOpsRecordedPerTouchedUnit(t *testing.T) {
	docs := frp.DocumentSet{
		Own:  rawMap("a.yaml", `{"x":"0"}`),
		Host: rawMap("h.yaml", `{"y":"0"}`),
	}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{
		patch(frp.DocumentScopeOwn, "a.yaml", "replace", "/x", "1"),
		patch(frp.DocumentScopeHost, "h.yaml", "replace", "/y", "2"),
	}}
	// An optional step that fails must not leave its ops in the record.
	inv.outputs["s2"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "a.yaml", "replace", "/absent", "z")}}
	steps := []types.PipelineStep{
		{ID: "s1", Plugin: "p", Function: "f"},
		{ID: "s2", Plugin: "p", Function: "f", Optional: true},
	}

	res, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := len(res.AppliedOps.Own["a.yaml"]); got != 1 {
		t.Errorf("own a.yaml applied ops = %d, want 1 (the rolled-back optional step's op must be dropped)", got)
	}
	if got := len(res.AppliedOps.Host["h.yaml"]); got != 1 {
		t.Errorf("host h.yaml applied ops = %d, want 1", got)
	}
}

// TestWalk_FailedTestOpMatchesEngineSentinel confirms the %w wrap keeps the
// engine's ErrTestFailed reachable through the walk's umbrella sentinel.
func TestWalk_FailedTestOpMatchesEngineSentinel(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"actual"}`)}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Patches: []frp.JSONPatchOp{patch(frp.DocumentScopeOwn, "a.yaml", "test", "/x", "expected")}}

	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, jsonpatch.ErrTestFailed()) {
		t.Fatalf("err = %v, want it to wrap jsonpatch.ErrTestFailed", err)
	}
	if !errors.Is(err, frp.ErrPipelineStepFailed()) {
		t.Errorf("err = %v, want it to also match frp.ErrPipelineStepFailed", err)
	}
}

// TestWalk_GuestReportedErrorMatchesHostTaxonomy confirms a step whose guest
// ran to completion but reported a failure on its output envelope halts
// through the shared host taxonomy: reachable both as a guest error and as a
// step failure, naming the export the step declared and carrying the guest's
// own reason.
func TestWalk_GuestReportedErrorMatchesHostTaxonomy(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Error: "could not resolve the chart"}

	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "render_chart"}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, wasm.ErrWasmGuestError()) {
		t.Fatalf("err = %v, want it to wrap the guest-error sentinel", err)
	}
	if !errors.Is(err, frp.ErrPipelineStepFailed()) {
		t.Errorf("err = %v, want it to also match frp.ErrPipelineStepFailed", err)
	}
	if !strings.Contains(err.Error(), `"render_chart"`) {
		t.Errorf("err = %v, want the message to name the step's export", err)
	}
	if !strings.Contains(err.Error(), "could not resolve the chart") {
		t.Errorf("err = %v, want the message to carry the guest's own reason", err)
	}
}

// TestWalk_UnknownDiagnosticSeverityHalts is the arbiter for the closed severity
// set at the pipeline's guest boundary: a step whose returned diagnostic carries
// a severity the shared diagnostic contract does not declare halts the walk,
// naming the plugin, the export, and the offending value — rather than being
// skipped by every routing arm, coerced to a default, or downgraded because the
// step happened to be optional.
//
// The needle travels as the guest's wire bytes, not as a Go value the host built,
// so the halt is proved at the boundary a real guest's output crosses.
func TestWalk_UnknownDiagnosticSeverityHalts(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.raw["s1"] = guestReply(t, abi.FRPStepOutput{Diagnostics: []abi.Diagnostic{{Severity: "critical", Message: "a value no socket declares"}}})

	step := types.PipelineStep{ID: "s1", Plugin: "oci://registry.example/policy/house-rules", Function: "render_chart"}

	_, err := Walk(context.Background(), docs, []types.PipelineStep{step},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err == nil {
		t.Fatal("an undeclared severity was accepted; want a halt")
	}
	if !errors.Is(err, wasm.ErrWasmDiagnosticSeverity()) {
		t.Errorf("err = %v, want it to wrap the undeclared-severity sentinel", err)
	}
	for _, want := range []string{
		"oci://registry.example/policy/house-rules", // the plugin
		`"render_chart"`,                                     // the export
		`"critical"`,                                         // the offending value
		abi.SeverityHalt, abi.SeverityWarn, abi.SeverityInfo, // what it should have been
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt message does not name %q:\n  %v", want, err)
		}
	}

	// Optional must not swallow it: an unroutable severity is a violated output
	// contract, which halts regardless of the flag.
	optional := step
	optional.Optional = true
	if _, err := Walk(context.Background(), docs, []types.PipelineStep{optional},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval); err == nil {
		t.Error("an optional step downgraded an undeclared severity to a warning; want a halt")
	}
}

// TestWalk_DeclaredDiagnosticSeverityPasses is the other half of the needle: the
// guard admits every severity the contract declares, so closing the set costs a
// conforming guest nothing. The advisory severities surface as warnings and the
// build proceeds; the halting one halts through the diagnostic path rather than
// through the severity guard.
func TestWalk_DeclaredDiagnosticSeverityPasses(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}

	for _, sev := range []string{abi.SeverityWarn, abi.SeverityInfo} {
		inv := newScripted()
		inv.raw["s1"] = guestReply(t, abi.FRPStepOutput{Diagnostics: []abi.Diagnostic{{Severity: sev, Message: "heads up"}}})
		res, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
			inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
		if err != nil {
			t.Fatalf("severity %q: Walk returned %v; a declared severity must pass the guard", sev, err)
		}
		if len(res.Warnings) != 1 {
			t.Errorf("severity %q: surfaced %d advisories, want 1", sev, len(res.Warnings))
		}
	}

	inv := newScripted()
	inv.raw["s1"] = guestReply(t, abi.FRPStepOutput{Diagnostics: []abi.Diagnostic{{Severity: abi.SeverityHalt, Message: "guest refused"}}})
	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if err == nil {
		t.Fatal("the halting severity did not halt")
	}
	if errors.Is(err, wasm.ErrWasmDiagnosticSeverity()) {
		t.Errorf("the halting severity was refused by the severity guard rather than routed: %v", err)
	}
}

// TestWalk_RefusesARepeatedOutputKey proves a step's answer naming one output key
// twice halts at the walk's decode with the output-shape condition, naming the
// export, rather than keeping either entry. No codec can write such an answer —
// every order-keeping container holds a key once — so the bytes are written by
// hand: one map whose outputs entry is the order-keeping array form, carrying the
// key "k" twice, each value the bytes of a JSON string.
func TestWalk_RefusesARepeatedOutputKey(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	inv.raw["s1"] = []byte{
		0xa1, 0x67, 'o', 'u', 't', 'p', 'u', 't', 's',
		0x82,
		0x82, 0x61, 'k', 0x43, '"', 'a', '"',
		0x82, 0x61, 'k', 0x43, '"', 'b', '"',
	}
	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "render_chart"}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, wasm.ErrWasmOutputShape()) {
		t.Fatalf("err = %v, want the output-shape condition", err)
	}
	for _, want := range []string{`"render_chart"`, `"k"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s: %v", want, err)
		}
	}
}

// authoredOrder is a key order no sort produces, with an integer-like key in the
// middle, which a JavaScript object would move to the front.
var authoredOrder = []string{"zeta", "10", "alpha"}

// TestWalk_HandsTheInvokerTheInputThePayloadCarries proves the step input the
// walk hands its invoker beside the payload is the one the payload carries, in the
// order the `with` rendered it — so a check of the step's declared input contract
// reads the value the guest reads, without decoding the payload again.
func TestWalk_HandsTheInvokerTheInputThePayloadCarries(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	inv := newScripted()
	render := func(name, text string, data any) (string, error) { return text, nil }
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", With: "zeta: 1\n\"10\": ten\nalpha: a\n"}}

	if _, err := Walk(context.Background(), docs, steps, inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, render); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	handed := inv.inputs["s1"]
	carried := receivedInput(t, inv.received["s1"]).StepInput
	if got := handed.Keys(); !slices.Equal(got, authoredOrder) {
		t.Errorf("the input handed to the invoker is in order %v, want the rendered order %v", got, authoredOrder)
	}
	if got := carried.Keys(); !slices.Equal(got, authoredOrder) {
		t.Errorf("the input the payload carries is in order %v, want the rendered order %v", got, authoredOrder)
	}
	handedJSON, err := json.Marshal(handed)
	if err != nil {
		t.Fatalf("encode the handed input: %v", err)
	}
	carriedJSON, err := json.Marshal(carried)
	if err != nil {
		t.Fatalf("encode the carried input: %v", err)
	}
	if !bytes.Equal(handedJSON, carriedJSON) {
		t.Errorf("the input handed over is not the one the payload carries:\n handed:  %s\n carried: %s", handedJSON, carriedJSON)
	}
}

// TestWalk_AppliesTheOutputsCheckToTheDecodedOutputs proves the walk applies the
// outputs check its invoker returned to the outputs it decoded from the guest's
// answer, in the order the guest returned them, and reports a refusal as the
// invocation's — halting even an optional step on a violated contract. A check
// that passes lets the step commit its outputs to the steps channel.
func TestWalk_AppliesTheOutputsCheckToTheDecodedOutputs(t *testing.T) {
	docs := frp.DocumentSet{Own: rawMap("a.yaml", `{"x":"0"}`)}
	var outs types.OrderedMap[any]
	for _, k := range authoredOrder {
		outs.Set(k, "v-"+k)
	}

	inv := newScripted()
	inv.outputs["s1"] = frp.FRPStepOutput{Outputs: outs}
	var seen []string
	inv.check["s1"] = func(got types.OrderedMap[any]) error {
		seen = got.Keys()
		return fmt.Errorf("outputs refused: %w", frp.ErrPipelineContractViolation())
	}
	_, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", Optional: true}},
		inv.invoke, &types.TemplateContext{}, renderctx.RenderContext{}, noEval)
	if !errors.Is(err, frp.ErrPipelineContractViolation()) {
		t.Fatalf("err = %v, want the check's contract violation, halting the optional step", err)
	}
	if !strings.Contains(err.Error(), `step "s1" (index 0) invoke: outputs refused`) {
		t.Errorf("err = %v, want the check's refusal reported as the invocation's", err)
	}
	if !slices.Equal(seen, authoredOrder) {
		t.Errorf("the check read the outputs in order %v, want the order the guest returned %v", seen, authoredOrder)
	}

	passing := newScripted()
	passing.outputs["s1"] = frp.FRPStepOutput{Outputs: outs}
	passing.check["s1"] = func(types.OrderedMap[any]) error { return nil }
	withCtx := &types.TemplateContext{}
	if _, err := Walk(context.Background(), docs, []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f"}},
		passing.invoke, withCtx, renderctx.RenderContext{}, noEval); err != nil {
		t.Fatalf("a passing check halted the walk: %v", err)
	}
	so, ok := withCtx.Steps.Get("s1")
	if !ok {
		t.Fatal(".steps.s1 absent after a step whose outputs passed the check")
	}
	if got := so.Outputs.Keys(); !slices.Equal(got, authoredOrder) {
		t.Errorf(".steps.s1.outputs is in order %v, want %v", got, authoredOrder)
	}
}
