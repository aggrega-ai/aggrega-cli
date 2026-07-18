// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"sync/atomic"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// markerCodec is a codec that records each Segment/Reassemble call so a test can
// prove the executor routed a scope's carving through a provider-supplied codec
// rather than the stock default. It delegates the actual work to the stock YAML
// codec so the pipeline behaves normally.
type markerCodec struct {
	inner    frp.Codec
	segments *int32
}

func (m markerCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	atomic.AddInt32(m.segments, 1)
	return m.inner.Segment(name, body)
}

func (m markerCodec) Reassemble(name string, body []byte, segs []frp.Unit, patched []frp.PatchedUnit) ([]byte, []string, error) {
	return m.inner.Reassemble(name, body, segs, patched)
}

// codecProviderEngine is a minimal Engine that also implements CodecProvider,
// returning a markerCodec — a local stand-in for any provider engine that
// overrides the stock codec.
type codecProviderEngine struct {
	segments *int32
}

func (e codecProviderEngine) Render(context.Context, map[string]any, NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}
func (e codecProviderEngine) Contract() ProviderContract { return ProviderContract{} }

// PipelineCodec satisfies CodecProvider: it reports a marker codec, standing in
// for any provider whose guest advertises the carve/reassemble exports.
func (e codecProviderEngine) PipelineCodec(yamlIndent int) (frp.Codec, bool) {
	return markerCodec{inner: newStockYAMLCodec(yamlIndent), segments: e.segments}, true
}

// plainEngine is an Engine that does NOT implement CodecProvider — it must fall
// back to the stock codec.
type plainEngine struct{}

func (plainEngine) Render(context.Context, map[string]any, NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}
func (plainEngine) Contract() ProviderContract { return ProviderContract{} }

// TestSelectCodec_PicksProviderCodec asserts selectCodec returns a provider's
// own codec when its engine implements CodecProvider, and the stock codec
// otherwise (an empty provider name, a nil factory, or a plain engine).
func TestSelectCodec_PicksProviderCodec(t *testing.T) {
	t.Parallel()
	var segs int32
	factory := func(name string) (Engine, error) {
		switch name {
		case "marker":
			return codecProviderEngine{segments: &segs}, nil
		case "plain":
			return plainEngine{}, nil
		default:
			return nil, ErrUnknownProvider
		}
	}

	// A provider whose engine implements CodecProvider → marker codec.
	mc := selectCodec(factory, "marker", 2)
	if _, ok := mc.(markerCodec); !ok {
		t.Errorf("expected markerCodec for provider %q, got %T", "marker", mc)
	}
	// A plain engine → stock codec.
	if _, ok := selectCodec(factory, "plain", 2).(stockYAMLCodec); !ok {
		t.Errorf("expected stockYAMLCodec for a plain engine")
	}
	// Empty provider name → stock codec.
	if _, ok := selectCodec(factory, "", 2).(stockYAMLCodec); !ok {
		t.Errorf("expected stockYAMLCodec for an empty provider name")
	}
	// Nil factory → stock codec.
	if _, ok := selectCodec(nil, "marker", 2).(stockYAMLCodec); !ok {
		t.Errorf("expected stockYAMLCodec for a nil factory")
	}
	// Unknown provider → stock codec.
	if _, ok := selectCodec(factory, "nope", 2).(stockYAMLCodec); !ok {
		t.Errorf("expected stockYAMLCodec for an unknown provider")
	}
}

// TestRunNodePipeline_RoutesThroughProviderCodec proves the executor selects the
// own provider's codec by provider name through the engine factory: a step over
// a node whose own provider supplies a markerCodec routes its carve through that
// codec, observed by the marker's call counter.
func TestRunNodePipeline_RoutesThroughProviderCodec(t *testing.T) {
	t.Parallel()
	var segs int32
	factory := func(name string) (Engine, error) {
		if name == "marker" {
			return codecProviderEngine{segments: &segs}, nil
		}
		return plainEngine{}, nil
	}

	own := []EmittedFile{{Name: "deployment.yaml", Origin: EmitOriginWorkload, Body: []byte("kind: Deployment\nmetadata:\n  name: web\n")}}
	// A no-op step that produces no patches; we only assert the codec was used to
	// build the keyed set.
	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		own, nil, false,
		"marker" /* ownProvider */, "", /* no host */
		renderctx.RenderContext{}, &types.TemplateContext{},
		selectCodec(factory, "marker", 2), selectCodec(factory, "", 2),
		cannedInvoker(t, frp.FRPStepOutput{}), describerFor("own", []string{"marker"}), "node")
	if err != nil {
		t.Fatalf("runNodePipelineWithInvoker: %v", err)
	}
	if atomic.LoadInt32(&segs) == 0 {
		t.Errorf("provider codec was never used to segment the own scope")
	}
}
