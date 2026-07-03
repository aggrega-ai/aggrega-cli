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

// failingLookup is a plugin-path lookup that always errors. The unwrap and
// intermediate-classification paths never load the guest, so the lookup is
// never called; the failure documents that no guest resolution happens on
// these paths.
func failingLookup(string) (pluginpath.ResolvedPlugin, error) {
	return pluginpath.ResolvedPlugin{}, errors.New("lookup must not be called on unwrap/classification paths")
}

// TestWasmEngineUnwrapExposesHostCapabilities proves the wrapper itself
// implements none of the host-side capabilities the wrapped helm-chart engine
// carries, while Unwrap resolves to the engine that does — the seam that keeps
// those host-side capabilities visible when a provider routes through Wasm.
func TestWasmEngineUnwrapExposesHostCapabilities(t *testing.T) {
	loader := &wasm.Loader{}
	w := wasmengine.New(failingLookup, "test://x", &helmchart.HelmEngine{}, loader)

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

	u := w.Unwrap()
	if _, ok := u.(renderer.CodecProvider); !ok {
		t.Error("unwrapped engine does not satisfy CodecProvider")
	}
	if _, ok := u.(renderer.ExternalArtifactsHandler); !ok {
		t.Error("unwrapped engine does not satisfy ExternalArtifactsHandler")
	}
	if _, ok := u.(renderer.ArtifactsPathProvider); !ok {
		t.Error("unwrapped engine does not satisfy ArtifactsPathProvider")
	}
	if _, ok := u.(renderer.ArtifactsTemplateDelims); !ok {
		t.Error("unwrapped engine does not satisfy ArtifactsTemplateDelims")
	}
}

// TestRenderIsIntermediateThroughWasmWrapper proves the classification unwraps
// to the wrapped engine: helm-chart nests per node and must never be classified
// intermediate even when Wasm-wrapped (including as an assembler, where the
// wrapper alone would answer BoundaryAssembler=true without the seam), while
// compose merges fragments and remains intermediate.
func TestRenderIsIntermediateThroughWasmWrapper(t *testing.T) {
	loader := &wasm.Loader{}
	helm := &helmchart.HelmEngine{}
	composeEng := &compose.ComposeEngine{}

	if got := renderer.RenderIsIntermediate(wasmengine.New(failingLookup, "test://x", helm, loader)); got {
		t.Error("RenderIsIntermediate(New helm-chart) = true, want false")
	}
	if got := renderer.RenderIsIntermediate(wasmengine.NewAssembler(failingLookup, "test://x", helm, loader)); got {
		t.Error("RenderIsIntermediate(NewAssembler helm-chart) = true, want false")
	}
	if got := renderer.RenderIsIntermediate(wasmengine.NewAssembler(failingLookup, "test://x", composeEng, loader)); !got {
		t.Error("RenderIsIntermediate(NewAssembler compose) = false, want true")
	}
}

// TestHelmEngineIsNotUnwrapper documents that an in-tree engine is not an
// Unwrapper, so hostEngine returns it unchanged (identity) — the non-wrapper
// branch of the seam.
func TestHelmEngineIsNotUnwrapper(t *testing.T) {
	var e renderer.Engine = &helmchart.HelmEngine{}
	if _, ok := e.(renderer.Unwrapper); ok {
		t.Error("helmchart.HelmEngine unexpectedly implements Unwrapper")
	}
}
