// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// failingLookup is a plugin-path lookup that always errors. The host-capability
// and intermediate-classification paths never load the guest, so the lookup is
// never called; the failure documents that no guest resolution happens on these
// paths.
func failingLookup(string) (pluginpath.ResolvedPlugin, error) {
	return pluginpath.ResolvedPlugin{}, errors.New("lookup must not be called on capability/classification paths")
}

// TestWasmEngineExposesForwardedHostCapabilities proves the wrapper itself
// implements none of the host-side capability interfaces the helm-chart provider
// carries, while its forwarded host-capability holder surfaces them — the seam
// that keeps those host-side capabilities visible when a provider routes through
// Wasm.
func TestWasmEngineExposesForwardedHostCapabilities(t *testing.T) {
	loader := &wasm.Loader{}
	w := wasmengine.New(failingLookup, "test://x", helmchart.HostCaps(), loader)

	var e renderer.Engine = w
	if _, ok := e.(renderer.CodecProvider); ok {
		t.Error("wrapper unexpectedly implements CodecProvider directly")
	}
	if _, ok := e.(renderer.ExternalArtifactsHandler); ok {
		t.Error("wrapper unexpectedly implements ExternalArtifactsHandler directly")
	}
	if _, ok := e.(renderer.ArtifactsPathProvider); ok {
		t.Error("wrapper unexpectedly implements ArtifactsPathProvider directly")
	}
	if _, ok := e.(renderer.ArtifactsTemplateDelims); ok {
		t.Error("wrapper unexpectedly implements ArtifactsTemplateDelims directly")
	}

	caps := renderer.ResolveHostCaps(w)
	if !caps.ExternalArtifacts {
		t.Error("forwarded holder does not carry ExternalArtifacts")
	}
	if caps.Codec == nil {
		t.Error("forwarded holder does not carry Codec")
	}
	if caps.PathForArtifact == nil {
		t.Error("forwarded holder does not carry PathForArtifact")
	}
	if caps.ArtifactDelimiters == nil {
		t.Error("forwarded holder does not carry ArtifactDelimiters")
	}
}

// TestRenderIsIntermediateThroughWasmWrapper proves the classification reads the
// forwarded holder: helm-chart nests per node and must never be classified
// intermediate even when Wasm-wrapped (including as an assembler, where a raw
// BoundaryAssembler assertion on the wrapper would answer true), while compose
// merges fragments and remains intermediate.
func TestRenderIsIntermediateThroughWasmWrapper(t *testing.T) {
	loader := &wasm.Loader{}

	if got := renderer.RenderIsIntermediate(wasmengine.New(failingLookup, "test://x", helmchart.HostCaps(), loader)); got {
		t.Error("RenderIsIntermediate(New helm-chart) = true, want false")
	}
	if got := renderer.RenderIsIntermediate(wasmengine.NewAssembler(failingLookup, "test://x", helmchart.HostCaps(), loader)); got {
		t.Error("RenderIsIntermediate(NewAssembler helm-chart) = true, want false")
	}
	if got := renderer.RenderIsIntermediate(wasmengine.NewAssembler(failingLookup, "test://x", compose.HostCaps(), loader)); !got {
		t.Error("RenderIsIntermediate(NewAssembler compose) = false, want true")
	}
}
