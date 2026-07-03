// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// hostCapEngine is a non-wrapper Engine that also implements the host-side
// capability interfaces the helm-chart engine carries (codec, external
// artifacts, artifact path, artifact delimiters). It stands in for the wrapped
// in-tree engine an Unwrapper exposes, so a test can assert hostEngine resolves
// those capabilities through the unwrap without importing a provider package
// (which would cycle back into this package).
type hostCapEngine struct{ stubEngine }

func (hostCapEngine) Codec(int) frp.Codec                                   { return nil }
func (hostCapEngine) HandlesArtifactsExternally()                           {}
func (hostCapEngine) PathForArtifact(_ NodeRenderContext, b string) string { return b }
func (hostCapEngine) ArtifactDelimiters() (string, string)                 { return "[[", "]]" }

// unwrapStub is an Unwrapper whose Unwrap returns a hostCapEngine. It embeds
// only the bare stubEngine, so it implements none of the host-side capability
// interfaces itself — hostEngine must reach through Unwrap to find them.
type unwrapStub struct {
	stubEngine
	wrapped Engine
}

func (u unwrapStub) Unwrap() Engine { return u.wrapped }

// TestHostEngineUnwrapsToWrappedCapabilities proves the unwrap seam is
// load-bearing: the wrapper itself satisfies none of the host-side capability
// interfaces, but hostEngine reaches through Unwrap to the wrapped engine that
// does.
func TestHostEngineUnwrapsToWrappedCapabilities(t *testing.T) {
	wrapper := unwrapStub{wrapped: hostCapEngine{}}

	// The seam is only load-bearing if the wrapper genuinely lacks these.
	var w Engine = wrapper
	if _, ok := w.(CodecProvider); ok {
		t.Error("wrapper unexpectedly implements CodecProvider directly; seam not load-bearing")
	}
	if _, ok := w.(ExternalArtifactsHandler); ok {
		t.Error("wrapper unexpectedly implements ExternalArtifactsHandler directly; seam not load-bearing")
	}
	if _, ok := w.(ArtifactsPathProvider); ok {
		t.Error("wrapper unexpectedly implements ArtifactsPathProvider directly; seam not load-bearing")
	}
	if _, ok := w.(ArtifactsTemplateDelims); ok {
		t.Error("wrapper unexpectedly implements ArtifactsTemplateDelims directly; seam not load-bearing")
	}

	got := hostEngine(wrapper)
	if _, ok := got.(CodecProvider); !ok {
		t.Error("hostEngine(wrapper) does not satisfy CodecProvider")
	}
	if _, ok := got.(ExternalArtifactsHandler); !ok {
		t.Error("hostEngine(wrapper) does not satisfy ExternalArtifactsHandler")
	}
	if _, ok := got.(ArtifactsPathProvider); !ok {
		t.Error("hostEngine(wrapper) does not satisfy ArtifactsPathProvider")
	}
	if _, ok := got.(ArtifactsTemplateDelims); !ok {
		t.Error("hostEngine(wrapper) does not satisfy ArtifactsTemplateDelims")
	}
}

// TestHostEngineIdentityForNonWrapper proves hostEngine returns a non-Unwrapper
// engine unchanged.
func TestHostEngineIdentityForNonWrapper(t *testing.T) {
	e := &stubEngine{}
	if _, ok := Engine(e).(Unwrapper); ok {
		t.Fatal("stubEngine unexpectedly implements Unwrapper; identity case is not exercised")
	}
	if got := hostEngine(e); got != Engine(e) {
		t.Error("hostEngine returned a different engine for a non-wrapper input")
	}
}

// compile-time sanity: the stubs satisfy the interfaces the test asserts.
var (
	_ Engine    = hostCapEngine{}
	_ Unwrapper = unwrapStub{}
)
