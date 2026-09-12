// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// The guest codec carved and reassembled on a background context, and the memoized
// describe that drives every crossing minted one too. Both run a guest, and both
// sit under a render that holds the build's context.
//
// # Why the context is carried on the CODEC rather than on its methods
//
// The carve-and-reassemble contract is the guest export contract unchanged, and
// one implementation of it compiles INTO a guest, where a host context does not
// exist. So the contract cannot take one. The capability query that produces the
// codec takes it instead, and the codec it returns carries it for the run that
// asked — built inside the pipeline run that holds the context, used for that
// run's crossings, discarded after.
//
// # What these proofs cover, and what they do not
//
// They establish that THE RENDER'S CONTEXT IS THE ONE THE GUEST MACHINERY
// RECEIVES: a withdrawn render supplies no codec and performs no crossing, while
// a live control supplies one and carves.
//
// That an ENDED CONTEXT UNWINDS AN ALREADY-RUNNING GUEST is a separate fact,
// proved elsewhere in this tree against a real module whose exported function
// never returns, and cited rather than re-asserted here.

// TestAWithdrawnRenderGetsNoGuestCodec is the load-bearing proof for the
// capability query.
//
// The control is what discriminates: a live render must be handed a codec, and a
// withdrawn one must not. A run where neither got one would be satisfied by a
// guest that advertises no codec at all, which is why the live arm is asserted
// first and fatally.
//
// A planted defect makes this fail: describe on a background context instead of
// the render's, and the withdrawn render is handed a codec exactly as the control
// is.
func TestAWithdrawnRenderGetsNoGuestCodec(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	uri := embeddedGuestURI(t, "helm-chart")

	live := wasmengine.New(embeddedGuestLookup, uri, loader)
	if codec, ok := live.PipelineCodec(context.Background(), 2); !ok || codec == nil {
		t.Fatalf("the live control was handed no codec (ok=%v), so the withdrawn arm below would "+
			"pass for the wrong reason: this guest must advertise the carve and reassemble "+
			"exports for the comparison to mean anything", ok)
	}

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	// A FRESH engine, because the descriptor is memoized per engine: reusing the
	// live one would answer from the cache and measure nothing.
	stopped := wasmengine.New(embeddedGuestLookup, uri, loader)
	if codec, ok := stopped.PipelineCodec(withdrawn, 2); ok || codec != nil {
		t.Errorf("a render whose context had already ended was handed a codec (ok=%v); the "+
			"withdrawal never reached the guest load the capability query drives", ok)
	}
}

// TestACarveUnderAWithdrawnRenderNeverCrosses is the other half: a codec obtained
// while the render was live must stop carving once the render ends.
//
// This is what the value-carried context buys. The codec's own contract takes
// none, so without it a withdrawal reaching the render would leave every
// subsequent crossing running on a context nobody could end.
//
// A planted defect makes this fail: have the crossings use a background context
// instead of the one the codec carries, and the carve after the withdrawal
// succeeds exactly as the one before it did.
func TestACarveUnderAWithdrawnRenderNeverCrosses(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	uri := embeddedGuestURI(t, "helm-chart")
	engine := wasmengine.New(embeddedGuestLookup, uri, loader)

	ctx, stop := context.WithCancel(context.Background())
	codec, ok := engine.PipelineCodec(ctx, 2)
	if !ok || codec == nil {
		t.Fatalf("no codec while the render was live (ok=%v); there is nothing to withdraw", ok)
	}

	// The live control: a carve while the render is still running must cross.
	const body = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: probe\n"
	if _, err := codec.Segment("probe.yaml", []byte(body)); err != nil {
		t.Fatalf("the live control's carve failed (%v), so the withdrawn arm below would pass "+
			"for the wrong reason", err)
	}

	stop()

	_, err := codec.Segment("probe.yaml", []byte(body))
	if err == nil {
		t.Fatal("a carve issued after the render was withdrawn crossed into the guest anyway; " +
			"the codec is running on a context the render cannot end")
	}
	if !mentionsRenderWithdrawal(err) {
		t.Errorf("the withdrawn carve failed with %v; it was supposed to fail ON the withdrawal, "+
			"and failing for some other reason means this proof is not measuring cancellation", err)
	}
}

// mentionsRenderWithdrawal matches a caller's withdrawal through the standard
// sentinels, with a text fallback for the guest runtime, which reports a closed
// module in its own words rather than wrapping the context error.
func mentionsRenderWithdrawal(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "module closed")
}
