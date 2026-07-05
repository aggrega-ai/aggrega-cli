// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"
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
		AssemblesBoundary:  true,
		ExternalArtifacts:  true,
		PathForArtifact:    func(_ NodeRenderContext, b string) string { return "templates/" + b },
		ArtifactDelimiters: func() (string, string) { return "[[", "]]" },
	}
	wrapper := forwardingWrapper{holder: holder}

	got := ResolveHostCaps(wrapper)
	if !got.AssemblesBoundary {
		t.Error("ResolveHostCaps did not surface AssemblesBoundary from the forwarded holder")
	}
	if !got.ExternalArtifacts {
		t.Error("ResolveHostCaps did not surface ExternalArtifacts from the forwarded holder")
	}
	if got.PathForArtifact == nil || got.ArtifactDelimiters == nil {
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
	if got.AssemblesBoundary || got.ExternalArtifacts || got.Codec != nil || got.ProjectSource != nil {
		t.Error("ResolveHostCaps returned a non-zero holder for a non-HostCapable engine")
	}
}

// TestRenderIsIntermediateFromHolder pins the intermediate-marking rule to the
// holder: a boundary-assembling provider that is not external is intermediate; an
// external boundary-assembler (umbrella nest-per-node) and a flat-output provider
// are not.
func TestRenderIsIntermediateFromHolder(t *testing.T) {
	if !(HostCapabilities{AssemblesBoundary: true}).RenderIsIntermediate() {
		t.Error("AssemblesBoundary && !ExternalArtifacts should be intermediate")
	}
	if (HostCapabilities{AssemblesBoundary: true, ExternalArtifacts: true}).RenderIsIntermediate() {
		t.Error("an external boundary-assembler must not be intermediate")
	}
	if (HostCapabilities{}).RenderIsIntermediate() {
		t.Error("a flat-output provider must not be intermediate")
	}
}
