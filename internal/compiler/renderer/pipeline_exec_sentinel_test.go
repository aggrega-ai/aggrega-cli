// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// compatibleDescriber returns a stepDescriber that always reports a descriptor
// whose step export is own-scope and tested-compatible with the "helm" provider.
// The sentinel tests pass ownProvider "helm" so the pre-walk compatibility check
// proceeds, letting each test exercise its own downstream halt (absent target,
// hostless /host) rather than tripping ErrPluginProviderIncompatible first.
func compatibleDescriber() stepDescriber {
	return func(_ context.Context, _ types.PipelineStep) (wasm.PluginDescriptor, error) {
		return wasm.PluginDescriptor{
			ABIVersion:          wasm.ABIVersionV1Alpha1,
			PluginType:          wasm.PluginTypeFRPStep,
			CompatibleProviders: []string{"helm"},
			Exports:             []wasm.PluginExport{{Name: "step", Operates: "patch", Scope: wasm.ScopeOwn}},
		}, nil
	}
}

// cannedInvoker returns a StepInvoker that always replies with the marshalled
// out, regardless of the payload. It is the test seam for driving the executor
// down failure paths the reference plugin cannot produce (a PATCH to an absent
// token; a /host op from a hostless step). The executor body
// (runNodePipelineWithInvoker) is the same one the production path runs — only
// the invoker is canned — so these tests assert the real halt behaviour.
func cannedInvoker(t *testing.T, out frp.FRPStepOutput) frp.StepInvoker {
	t.Helper()
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal canned output: %v", err)
	}
	return func(_ context.Context, _ types.PipelineStep, _ []byte) ([]byte, error) {
		return raw, nil
	}
}

// oneStep is a single pipeline step; the canned invoker ignores its fields, so
// only its presence (a step to run) matters.
func oneStep() []types.PipelineStep {
	return []types.PipelineStep{{ID: "s", Plugin: "canned", Function: "step"}}
}

// ownDeployment is one structured own document so the keyed set is non-empty
// and the segmenter produces a real token the test can avoid addressing.
func ownDeployment() []EmittedFile {
	return []EmittedFile{{
		Name:   "deployment.yaml",
		Origin: EmitOriginWorkload,
		Body:   []byte("kind: Deployment\nmetadata:\n  name: web\n"),
	}}
}

// TestRunNodePipeline_TargetNotEmitted drives the executor with a canned step
// that PATCHes a token absent from the keyed set, and asserts the build halts
// with ErrPipelineTargetNotEmitted — the renderer promotion of the walk's
// ErrPipelineTargetAbsent. Also confirms the walk's typed cause remains in the
// errors.Is chain.
func TestRunNodePipeline_TargetNotEmitted(t *testing.T) {
	t.Parallel()

	out := frp.FRPStepOutput{Patches: []frp.JSONPatchOp{{
		Scope: frp.DocumentScopeOwn,
		Key:   "no-such-token.yaml", // absent from the set
		Op:    "add",
		Path:  "/metadata/annotations/x",
		Value: "y",
	}}}

	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		ownDeployment(), nil, false, "helm", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, 2, cannedInvoker(t, out), compatibleDescriber(), "node")
	if err == nil {
		t.Fatalf("expected halt for PATCH to absent token; got nil")
	}
	if !errors.Is(err, ErrPipelineTargetNotEmitted) {
		t.Errorf("expected ErrPipelineTargetNotEmitted; got %v", err)
	}
	// The walk's typed cause is preserved in the unwrap chain.
	if !errors.Is(err, frp.ErrPipelineTargetAbsent) {
		t.Errorf("expected the walk's ErrPipelineTargetAbsent to remain in the errors.Is chain; got %v", err)
	}
}

// TestRunNodePipeline_HostEmitFromHostlessHalts drives a canned step that emits
// a /host side effect from a host=nil call (a Recipe or boundary-scope step has
// no host component), and asserts ErrPipelineHostScopeUnavailable.
func TestRunNodePipeline_HostEmitFromHostlessHalts(t *testing.T) {
	t.Parallel()

	out := frp.FRPStepOutput{SideEffects: []frp.SideEffect{{
		Kind:  "emit_file",
		Scope: frp.DocumentScopeHost,
		File:  &frp.SideEffectFile{Path: "host-thing.yaml", Content: "kind: ConfigMap\n"},
	}}}

	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		ownDeployment(), nil /* hostless */, false, "helm", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, 2, cannedInvoker(t, out), compatibleDescriber(), "node")
	if err == nil {
		t.Fatalf("expected halt for /host EMIT from a hostless step; got nil")
	}
	if !errors.Is(err, ErrPipelineHostScopeUnavailable) {
		t.Errorf("expected ErrPipelineHostScopeUnavailable; got %v", err)
	}
}

// TestRunNodePipeline_HostPatchFromHostlessHalts drives a canned step that
// PATCHes a /host-scoped document from a host=nil call. The host root is empty,
// so the walk reports an absent target scoped to host; the executor promotes it
// to ErrPipelineHostScopeUnavailable rather than the generic target-not-emitted
// diagnostic, naming the precise hostless cause.
func TestRunNodePipeline_HostPatchFromHostlessHalts(t *testing.T) {
	t.Parallel()

	out := frp.FRPStepOutput{Patches: []frp.JSONPatchOp{{
		Scope: frp.DocumentScopeHost,
		Key:   "anything.yaml",
		Op:    "add",
		Path:  "/metadata/annotations/x",
		Value: "y",
	}}}

	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		ownDeployment(), nil /* hostless */, false, "helm", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, 2, cannedInvoker(t, out), compatibleDescriber(), "node")
	if err == nil {
		t.Fatalf("expected halt for /host PATCH from a hostless step; got nil")
	}
	if !errors.Is(err, ErrPipelineHostScopeUnavailable) {
		t.Errorf("expected ErrPipelineHostScopeUnavailable; got %v", err)
	}
}
