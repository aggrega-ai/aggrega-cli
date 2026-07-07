// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// failingLookup is a plugin-path lookup that always errors. The host-capability
// path never loads the guest, so the lookup is never called; the failure documents
// that no guest resolution happens on the host-side capability path.
func failingLookup(string) (pluginpath.ResolvedPlugin, error) {
	return pluginpath.ResolvedPlugin{}, errors.New("lookup must not be called on the host-capability path")
}

// embeddedGuestURI resolves an embedded provider guest URI by basename, and
// embeddedGuestLookup resolves that URI to the guest's bytes — mirroring the embed
// tier of the production plugin-resolution cascade so the render classification
// reads a real guest's describe() self-report.
func embeddedGuestURI(t *testing.T, basename string) string {
	t.Helper()
	for _, uri := range embedded.URIs() {
		if strings.Contains(uri, basename) {
			return uri
		}
	}
	t.Fatalf("no embedded guest URI carries %q; embedded URIs: %v", basename, embedded.URIs())
	return ""
}

func embeddedGuestLookup(uri string) (pluginpath.ResolvedPlugin, error) {
	b, ok := embedded.Lookup(uri)
	if !ok {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("no embedded guest for %q", uri)
	}
	return pluginpath.ResolvedPlugin{
		Bytes:  b,
		URI:    uri,
		Tier:   pluginpath.TierEmbed,
		Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096},
	}, nil
}

// TestWasmEngineExposesCodecAndArtifactsSeams proves the wrapper implements the
// pipeline-codec seam (CodecProvider) directly — the pipeline codec is the
// guest's own carve/reassemble self-report, driven through the wrapper — and the
// render-crossing ArtifactsEmitter seam. With an unloadable guest the codec seam
// reports no codec (the pipeline
// borrows the stock YAML codec) and the artifact-delimiter accessor falls back
// to the default pair.
func TestWasmEngineExposesCodecAndArtifactsSeams(t *testing.T) {
	loader := &wasm.Loader{}
	w := wasmengine.New(failingLookup, "test://x", loader)

	var e renderer.Engine = w
	if _, ok := e.(renderer.CodecProvider); !ok {
		t.Error("wrapper must implement CodecProvider — the pipeline-codec seam")
	}
	if _, ok := e.(renderer.ArtifactsEmitter); !ok {
		t.Error("wrapper must implement ArtifactsEmitter — the render-crossing artifacts seam")
	}

	// The guest cannot be loaded (failingLookup), so the codec seam reports no
	// codec — the pipeline borrows the stock YAML codec — and the delimiter
	// accessor takes its default-pair fallback rather than the guest's pair.
	if codec, ok := w.PipelineCodec(2); ok || codec != nil {
		t.Errorf("unloadable-guest PipelineCodec = (%v, %v); want (nil, false)", codec, ok)
	}
	if l, r := w.ArtifactTemplateDelimiters(); l != "{{" || r != "}}" {
		t.Errorf("unloadable-guest delimiter fallback = %q/%q; want {{ / }}", l, r)
	}
}

// TestArtifactTemplateDelimitersFromDescribe proves the delimiter accessor reads
// the guest's live describe() self-report: helm-chart declares the "[["/"]]" pair
// so its artifact bodies leave Helm's "{{ }}" syntax untouched, while helm
// declares no alternate pair and falls back to the default.
func TestArtifactTemplateDelimitersFromDescribe(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	helmchartURI := embeddedGuestURI(t, "helmchart-provider")
	helmURI := embeddedGuestURI(t, "helm-provider")

	hc := wasmengine.New(embeddedGuestLookup, helmchartURI, loader)
	if l, r := hc.ArtifactTemplateDelimiters(); l != "[[" || r != "]]" {
		t.Errorf("helm-chart delimiters = %q/%q; want [[ / ]]", l, r)
	}
	h := wasmengine.New(embeddedGuestLookup, helmURI, loader)
	if l, r := h.ArtifactTemplateDelimiters(); l != "{{" || r != "}}" {
		t.Errorf("helm delimiters = %q/%q; want {{ / }}", l, r)
	}
}

// TestRenderIsIntermediateThroughWasmWrapper proves the classification reads the
// guest's live describe() boundary-assembly self-report: helm-chart nests per node
// and must never be classified intermediate even when Wasm-wrapped (including as an
// assembler, where a raw BoundaryAssembler assertion on the wrapper would answer
// true), while compose merges fragments and remains intermediate.
func TestRenderIsIntermediateThroughWasmWrapper(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	helmchartURI := embeddedGuestURI(t, "helmchart-provider")
	composeURI := embeddedGuestURI(t, "compose-provider")

	if got := renderer.RenderIsIntermediate(wasmengine.New(embeddedGuestLookup, helmchartURI, loader)); got {
		t.Error("RenderIsIntermediate(New helm-chart) = true, want false")
	}
	if got := renderer.RenderIsIntermediate(wasmengine.NewAssembler(embeddedGuestLookup, helmchartURI, loader)); got {
		t.Error("RenderIsIntermediate(NewAssembler helm-chart) = true, want false")
	}
	if got := renderer.RenderIsIntermediate(wasmengine.NewAssembler(embeddedGuestLookup, composeURI, loader)); !got {
		t.Error("RenderIsIntermediate(NewAssembler compose) = false, want true")
	}
}
