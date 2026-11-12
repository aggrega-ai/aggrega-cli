// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// This drives the reassembly half of a codec's contract into its refusal: a
// patched unit whose token the carve of the same file never produced.
//
// The non-conforming artefact here is a CODEC rather than a Wasm guest, and that
// is a property of the condition rather than a weaker proof of it. A provider
// that advertises its own carve and reassemble exports owns both halves in its
// guest, so its own reassembly answers for a token it cannot resolve and this
// refusal is never reached. The refusal is reached where the two halves are
// SPLIT — a codec that carves for itself and delegates its structural
// reassembly to the stock YAML codec, which is an in-process Go composition and
// is the shape the shipped chart codec has. So a codec is what can commit this
// violation, and a Wasm guest structurally cannot.
//
// It is driven through the real executor and the real codec-selection seam: the
// codec arrives as an interface VALUE from an engine that reports one, which is
// the installation the code graph reports as having no callers at all. Driving it
// through that seam is what proves the seam live rather than asserted.

// tokenDivergingCodec is a deliberately non-conforming codec: it carves through
// the stock YAML codec and then renames every unit's token, while delegating
// reassembly to the stock codec unchanged. The stock codec re-derives a file's
// tokens from the file's own bytes, so the tokens it produces at reassembly are
// the real ones and the renamed tokens resolve to nothing.
//
// Everything else about it is faithful — the unit's bytes, its media type and its
// tree are the stock codec's own — so the only thing wrong is the correspondence
// between the two halves. That is what keeps the halt attributable: a codec that
// also mangled its trees could fail for either reason.
type tokenDivergingCodec struct {
	stock frp.Codec
}

// divergentTokenSuffix is appended to every carved token. It is a suffix rather
// than a replacement so the diverged token still reads as derived from the real
// one in the diagnostic, which is what a codec author debugging this would see.
const divergentTokenSuffix = "#diverged"

func (c tokenDivergingCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	units, err := c.stock.Segment(name, body)
	if err != nil {
		return nil, err
	}
	out := make([]frp.Unit, 0, len(units))
	for _, u := range units {
		u.Token += divergentTokenSuffix
		out = append(out, u)
	}
	return out, nil
}

func (c tokenDivergingCodec) Reassemble(name string, body []byte, segs []frp.Unit, patched []frp.PatchedUnit) ([]byte, []string, error) {
	return c.stock.Reassemble(name, body, segs, patched)
}

// codecOnlyEngine is a provider engine that renders nothing and exists only to
// supply a codec. The executor asks the engine rendering a scope whether it
// supplies one, so a codec reaches the pipeline only through an engine — which is
// why this stands in for one rather than the codec being passed directly.
type codecOnlyEngine struct {
	codec frp.Codec
}

func (e codecOnlyEngine) Render(context.Context, map[string]any, NodeRenderContext) ([]RenderedFile, error) {
	return nil, errors.New("codecOnlyEngine renders nothing; it exists to supply a codec")
}

func (e codecOnlyEngine) Contract() ProviderContract { return ProviderContract{} }

// PipelineCodec satisfies CodecProvider, which is the interface the executor
// checks the engine against.
func (e codecOnlyEngine) PipelineCodec(context.Context, int) (frp.Codec, bool) { return e.codec, true }

// TestNonconformingCodec_PatchedTokenNotCarved drives a patch from the real guest
// through a codec whose carve and reassembly disagree about tokens, and asserts
// the refusal names its condition and carries its code.
//
// The patch comes from the same non-conforming step guest the side-effect proofs
// use, in its conforming patch mode: the guest addresses the document by the key
// it was handed, which is the diverged token, so nothing about the guest is at
// fault here and the codec is the only thing that can be.
func TestNonconformingCodec_PatchedTokenNotCarved(t *testing.T) {
	lookup := nonconformingStepLookup(t)

	codec := tokenDivergingCodec{stock: rendercore.NewStockYAMLCodec(2)}
	factory := func(string) (Provider, error) { return codecOnlyEngine{codec: codec}, nil }

	own := []EmittedFile{{
		Name:   "workload.yaml",
		Origin: EmitOriginWorkload,
		Body:   []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n"),
	}}
	steps := []types.PipelineStep{{
		ID:       "patcher",
		Plugin:   "nonconforming-step.wasm",
		Function: "step",
		With:     "violation: patch-own\n",
	}}

	_, err := runNodePipeline(context.Background(), steps, own, nil, false, "diverging", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, redact.Keys{},
		2, lookup, factory, "codec-node")

	if err == nil {
		t.Fatal("a patched token the carve did not produce must halt; got no error")
	}
	if !errors.Is(err, rendercore.ErrPipelinePatchUnitNotCarved()) {
		t.Fatalf("expected ErrPipelinePatchUnitNotCarved in the error chain; got: %v", err)
	}
	gotShort, ok := cerrors.ShortCodeOf(err)
	if !ok {
		t.Fatalf("the halt carries no short code, so a reader has nothing to quote; got: %v", err)
	}
	if gotShort != "E0268" {
		t.Fatalf("short code = %q, want %q", gotShort, "E0268")
	}
	sentence, ok := cerrors.MessageOf(err)
	if !ok {
		t.Fatalf("the halt carries no registered sentence; got: %v", err)
	}
	emitted := cerrors.CauseChain(err)
	if !strings.Contains(emitted, sentence) {
		t.Fatalf("the emitted diagnostic does not carry the registered sentence.\nsentence: %s\nemitted: %s",
			sentence, emitted)
	}
	t.Logf("EMITTED [%s] %s", gotShort, emitted)
}

// TestNonconformingCodec_AgreeingTokensAreAccepted is the positive control for the
// test above, and it is what keeps that halt attributable to the token divergence
// rather than to the composition, the patch mode, or the codec-supplying engine.
// The same guest, the same patch and the same engine seam are used with the stock
// codec installed directly, and the pipeline completes.
func TestNonconformingCodec_AgreeingTokensAreAccepted(t *testing.T) {
	lookup := nonconformingStepLookup(t)

	factory := func(string) (Provider, error) {
		return codecOnlyEngine{codec: rendercore.NewStockYAMLCodec(2)}, nil
	}

	own := []EmittedFile{{
		Name:   "workload.yaml",
		Origin: EmitOriginWorkload,
		Body:   []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n"),
	}}
	steps := []types.PipelineStep{{
		ID:       "patcher",
		Plugin:   "nonconforming-step.wasm",
		Function: "step",
		With:     "violation: patch-own\n",
	}}

	res, err := runNodePipeline(context.Background(), steps, own, nil, false, "agreeing", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, redact.Keys{},
		2, lookup, factory, "codec-node")

	if err != nil {
		t.Fatalf("a codec whose carve and reassembly agree must be accepted; got: %v", err)
	}
	// The patch has to have landed, or the control would pass on a pipeline that
	// reassembled nothing — which is the state the divergent case must be told
	// apart from.
	var patched bool
	for _, f := range res.Own {
		if f.Name == "workload.yaml" && strings.Contains(string(f.Body), "nonconforming.step") {
			patched = true
		}
	}
	if !patched {
		t.Fatal("the control patch did not land, so the divergent case's halt is not attributable to the token divergence")
	}
}
