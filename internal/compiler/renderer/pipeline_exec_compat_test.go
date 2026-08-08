// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// describerFor returns a stepDescriber that always reports a frp_step descriptor
// whose step export declares the given scope and whose targets is the
// given set. It is the seam through which the pre-walk compatibility check reads a
// plugin's declared compatibility without a real .wasm — the executor body
// (runNodePipelineWithInvoker) is the production code path; only the descriptor is
// canned.
func describerFor(scope string, compatible []string) stepDescriber {
	return func(_ context.Context, _ types.PipelineStep) (wasm.PluginDescriptor, error) {
		return wasm.PluginDescriptor{
			AbiVersion:   wasm.ABIVersionV1Alpha1,
			PluginType:   string(wasm.PluginTypeFRPStep),
			Capabilities: compatible,
			Exports:      []wasm.PluginExport{{Name: "step", Operates: "patch", Scope: scope}},
		}, nil
	}
}

// noopInvoker is a StepInvoker that returns an empty step output: no patches, no
// side effects. A compatible run reaches the walk and folds back unchanged. The
// pre-walk compatibility halt fires before the invoker is ever called, so the
// mismatch tests never depend on the invoker (it reuses cannedInvoker from the
// sentinel test, same package).
func noopInvoker(t *testing.T) frp.StepInvoker {
	t.Helper()
	return cannedInvoker(t, frp.FRPStepOutput{})
}

// TestProviderCompat_OwnScopeMismatchHalts proves an own-scope step whose plugin
// does not list the node's own provider halts with ErrPluginProviderIncompatible
// before the walk runs any guest.
func TestProviderCompat_OwnScopeMismatchHalts(t *testing.T) {
	t.Parallel()

	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		ownDeployment(), nil, false,
		"helm" /* ownProvider */, "", /* no host */
		renderctx.RenderContext{}, &types.TemplateContext{}, newStockYAMLCodec(2), newStockYAMLCodec(2),
		noopInvoker(t), describerFor(wasm.ScopeOwn, []string{"compose"}), "node")
	if err == nil {
		t.Fatalf("expected halt for own-scope provider mismatch; got nil")
	}
	if !errors.Is(err, ErrPluginProviderIncompatible()) {
		t.Errorf("expected ErrPluginProviderIncompatible; got %v", err)
	}
	// The Loud Guardrail names the offending provider, its scope, and the
	// declared set in declared order.
	msg := err.Error()
	for _, want := range []string{`provider "helm"`, "own-scope", "compose", "canned"} {
		if !strings.Contains(msg, want) {
			t.Errorf("incompatibility message missing %q; got: %s", want, msg)
		}
	}
}

// TestProviderCompat_HostScopeMismatchHalts proves a host-scope step whose plugin
// does not list the host component's provider halts with
// ErrPluginProviderIncompatible. A host set IS present (component-scope FR), so the
// host check is live.
func TestProviderCompat_HostScopeMismatchHalts(t *testing.T) {
	t.Parallel()

	host := []EmittedFile{{Name: "host-deployment.yaml", Origin: EmitOriginWorkload, Body: []byte("kind: Deployment\nmetadata:\n  annotations: {}\n")}}

	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		ownDeployment(), host, false,
		"compose" /* ownProvider */, "helm", /* hostProvider */
		renderctx.RenderContext{}, &types.TemplateContext{}, newStockYAMLCodec(2), newStockYAMLCodec(2),
		noopInvoker(t), describerFor(wasm.ScopeHost, []string{"compose"}), "node")
	if err == nil {
		t.Fatalf("expected halt for host-scope provider mismatch; got nil")
	}
	if !errors.Is(err, ErrPluginProviderIncompatible()) {
		t.Errorf("expected ErrPluginProviderIncompatible; got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{`provider "helm"`, "host-scope", "compose"} {
		if !strings.Contains(msg, want) {
			t.Errorf("incompatibility message missing %q; got: %s", want, msg)
		}
	}
}

// TestProviderCompat_CompatibleProceeds proves a step whose plugin lists the
// node's own provider passes the pre-walk check and the pipeline runs to
// completion (no halt; the own set returns unchanged from the no-op invoker).
func TestProviderCompat_CompatibleProceeds(t *testing.T) {
	t.Parallel()

	own := ownDeployment()
	res, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		own, nil, false,
		"helm" /* ownProvider */, "",
		renderctx.RenderContext{}, &types.TemplateContext{}, newStockYAMLCodec(2), newStockYAMLCodec(2),
		noopInvoker(t), describerFor(wasm.ScopeOwn, []string{"helm", "compose"}), "node")
	if err != nil {
		t.Fatalf("compatible provider must proceed; got halt: %v", err)
	}
	if len(res.Own) != len(own) {
		t.Errorf("compatible no-op run mutated the own set: got %d files, want %d", len(res.Own), len(own))
	}
}

// TestProviderCompat_EmptyTargetsHalts proves a plugin declaring an
// empty targets set matches no provider — running it anywhere is an
// undeclared compatibility claim — so the build halts with
// ErrPluginProviderIncompatible. Locks the empty=halt decision against regression.
func TestProviderCompat_EmptyTargetsHalts(t *testing.T) {
	t.Parallel()

	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		ownDeployment(), nil, false,
		"helm" /* ownProvider */, "",
		renderctx.RenderContext{}, &types.TemplateContext{}, newStockYAMLCodec(2), newStockYAMLCodec(2),
		noopInvoker(t), describerFor(wasm.ScopeOwn, nil /* empty targets */), "node")
	if err == nil {
		t.Fatalf("expected halt for empty targets; got nil")
	}
	if !errors.Is(err, ErrPluginProviderIncompatible()) {
		t.Errorf("expected ErrPluginProviderIncompatible; got %v", err)
	}
	// The message renders an empty declared set as "(none)".
	if !strings.Contains(err.Error(), "(none)") {
		t.Errorf("empty-set message should render the declared set as (none); got: %s", err.Error())
	}
}
