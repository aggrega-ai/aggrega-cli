// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// soakCallCount is the number of step invocations the GC-soak canary
// drives through one plugin instance. It is large enough to cross many
// guest-runtime GC cycles (the guest is a std-Go WASI reactor with a
// full collector) while staying within CI time budget.
const soakCallCount = 4000

// soakForcedGCEvery forces a host-side runtime.GC() at this call
// cadence during the soak. The guest's own collector runs under
// natural allocation pressure across the 4000 calls; the host GC drives
// the netpoll path that, in the std-Go WASI runtime, routes through
// poll_oneoff with zero file descriptors. Exercising that path
// repeatedly is the regression canary for a poll_oneoff crash under the
// host's deterministic WASI configuration.
const soakForcedGCEvery = 500

// soakTimeout bounds the whole soak. A hang in the guest runtime (for
// example a frozen monotonic clock making the runtime spin, or a
// poll_oneoff that never returns) would otherwise block CI
// indefinitely; the context deadline turns a hang into a loud failure.
const soakTimeout = 90 * time.Second

// TestFRPStepGCSoakDeterministicAndNoHang is the sustained-load canary
// for the std-Go wasip1 frp-step guest through the real loader. It
// loads one plugin instance and invokes step soakCallCount times over
// the same input, forcing host GC periodically, and asserts:
//
//   - No call errors (no poll_oneoff crash, no trap).
//   - Every output is byte-identical to the first — the guest is
//     deterministic across many calls in one instance despite the guest
//     runtime's GC cycling and the host's real-monotonic clock
//     advancing between calls.
//   - The whole soak completes within soakTimeout — a hang fails loudly
//     rather than blocking CI.
//
// Runs unconditionally in CI: the std-Go toolchain is always present,
// so the fixture build does not skip for a missing toolchain (it skips
// only when the PDK source itself is absent).
func TestFRPStepGCSoakDeterministicAndNoHang(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	ctx, cancel := context.WithTimeout(context.Background(), soakTimeout)
	defer cancel()

	loader := &wasm.Loader{}
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name: "frp-step-go",
		Path: pluginPath,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe: %v", err)
	}
	defer plugin.Close(ctx)

	payload := frpStepInputPayload(t)

	first, err := wasm.InvokeExport(ctx, plugin, desc, wasm.StepExport, payload)
	if err != nil {
		t.Fatalf("InvokeExport(step) call 0: %v", err)
	}

	for i := 1; i < soakCallCount; i++ {
		if ctx.Err() != nil {
			t.Fatalf("soak exceeded %s after %d calls (possible hang): %v", soakTimeout, i, ctx.Err())
		}
		out, err := wasm.InvokeExport(ctx, plugin, desc, wasm.StepExport, payload)
		if err != nil {
			t.Fatalf("InvokeExport(step) call %d: %v", i, err)
		}
		if !bytes.Equal(first, out) {
			t.Fatalf("step output not byte-identical at call %d:\nfirst: %s\ngot:   %s", i, first, out)
		}
		if i%soakForcedGCEvery == 0 {
			runtime.GC()
		}
	}
}
