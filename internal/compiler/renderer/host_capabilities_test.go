// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// forwardingWrapper mimics the Wasm-provider adapter: it drives render itself
// (via the embedded stubEngine, which is not HostCapable) and forwards a
// provider's host-side capability holder through HostCapabilities. It stands in
// for a provider routed to a Wasm guest, whose host-side capabilities must
// resolve from the forwarded holder rather than from a concrete engine type.
type forwardingWrapper struct {
	stubEngine
	holder HostCapabilities
}

func (w forwardingWrapper) HostCapabilities() HostCapabilities { return w.holder }

// TestResolveHostCaps_ForwardedHolder proves ResolveHostCaps reads a HostCapable
// wrapper's forwarded holder — the seam that keeps a provider's host-side
// capabilities visible when it routes through Wasm, without the host asserting a
// concrete engine type.
func TestResolveHostCaps_ForwardedHolder(t *testing.T) {
	holder := HostCapabilities{
		Codec:                        func(int) frp.Codec { return nil },
		ProjectSubCharts:             func([]SubChartInfo) ([]SubChartProjection, error) { return nil, nil },
		BoundaryConsumerEnvOverrides: func(*types.TemplateContext) (map[string]map[string]string, error) { return nil, nil },
	}
	wrapper := forwardingWrapper{holder: holder}

	got := ResolveHostCaps(wrapper)
	if got.Codec == nil || got.ProjectSubCharts == nil || got.BoundaryConsumerEnvOverrides == nil {
		t.Error("ResolveHostCaps dropped the forwarded holder's function fields")
	}
}

// TestResolveHostCaps_NonHostCapableIsZero proves an engine that reports no
// host-side capabilities resolves to the zero holder, whose false markers and nil
// fields deliver the host's default fallbacks.
func TestResolveHostCaps_NonHostCapableIsZero(t *testing.T) {
	e := &stubEngine{}
	if _, ok := Engine(e).(HostCapable); ok {
		t.Fatal("stubEngine unexpectedly implements HostCapable; zero-holder case is not exercised")
	}
	got := ResolveHostCaps(e)
	if got.Codec != nil || got.ProjectSubCharts != nil || got.BoundaryConsumerEnvOverrides != nil {
		t.Error("ResolveHostCaps returned a non-zero holder for a non-HostCapable engine")
	}
}

// boundaryAssemblyEngine is a no-op engine reporting a fixed boundary-assembly
// mode, standing in for a provider whose guest self-reports the mode via
// describe(). It exercises the host's render classification from the reported
// value alone.
type boundaryAssemblyEngine struct {
	stubEngine
	mode string
}

func (b boundaryAssemblyEngine) BoundaryAssembly() string { return b.mode }

// TestRenderIsIntermediateFromReport pins the intermediate-marking rule to the
// engine's boundary-assembly self-report: a fragment-merging provider ("merge") is
// intermediate; a nesting provider ("nest") and a flat-output provider ("none")
// are not.
func TestRenderIsIntermediateFromReport(t *testing.T) {
	if !RenderIsIntermediate(boundaryAssemblyEngine{mode: "merge"}) {
		t.Error(`boundary_assembly "merge" should be intermediate`)
	}
	if RenderIsIntermediate(boundaryAssemblyEngine{mode: "nest"}) {
		t.Error(`boundary_assembly "nest" must not be intermediate`)
	}
	if RenderIsIntermediate(boundaryAssemblyEngine{mode: "none"}) {
		t.Error(`boundary_assembly "none" must not be intermediate`)
	}
}

// TestAssemblesBoundaryFromReport pins the assembler classification to the engine's
// boundary-assembly self-report: a merging ("merge") or nesting ("nest") provider
// assembles; a flat-output provider ("none") and an engine that reports no mode do
// not.
func TestAssemblesBoundaryFromReport(t *testing.T) {
	if !AssemblesBoundary(boundaryAssemblyEngine{mode: "merge"}) {
		t.Error(`boundary_assembly "merge" should assemble`)
	}
	if !AssemblesBoundary(boundaryAssemblyEngine{mode: "nest"}) {
		t.Error(`boundary_assembly "nest" should assemble`)
	}
	if AssemblesBoundary(boundaryAssemblyEngine{mode: "none"}) {
		t.Error(`boundary_assembly "none" must not assemble`)
	}
	if AssemblesBoundary(&stubEngine{}) {
		t.Error("an engine that reports no boundary-assembly mode must not assemble")
	}
}
