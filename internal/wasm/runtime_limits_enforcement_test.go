// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"testing"
	"time"
)

// growLoopWasm is a real wasm32 module built by wat2wasm from a WAT source that
// imports nothing (so it passes the WASI/import firewall) and exports a "grow"
// function which grows linear memory one page at a time and stores into each
// freshly-added page. Under a memory cap, memory.grow returns -1 once the cap is
// reached; the subsequent store into the (non-existent) page is out-of-bounds and
// traps. It is a toolchain-produced binary used to prove the loader's memory cap
// is actually enforced by the runtime at Call time. The WAT source:
//
//	(module
//	  (memory (export "memory") 1)
//	  (func (export "grow") (result i32)
//	    (local $base i32)
//	    (loop $l
//	      (local.set $base (i32.mul (memory.grow (i32.const 1)) (i32.const 65536)))
//	      (i32.store (local.get $base) (i32.const 1))
//	      (br $l))
//	    (i32.const 0)))
var growLoopWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f, 0x03,
	0x02, 0x01, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x11, 0x02, 0x06, 0x6d, 0x65, 0x6d, 0x6f,
	0x72, 0x79, 0x02, 0x00, 0x04, 0x67, 0x72, 0x6f, 0x77, 0x00, 0x00, 0x0a, 0x1f, 0x01, 0x1d, 0x01,
	0x01, 0x7f, 0x03, 0x40, 0x41, 0x01, 0x40, 0x00, 0x41, 0x80, 0x80, 0x04, 0x6c, 0x21, 0x00, 0x20,
	0x00, 0x41, 0x01, 0x36, 0x02, 0x00, 0x0c, 0x00, 0x0b, 0x41, 0x00, 0x0b,
}

// spinLoopWasm is a real wasm32 module built by wat2wasm from a WAT source that
// imports nothing and exports a "spin" function whose body is an unconditional
// infinite loop. It is used to prove the loader's per-call wall-time cap is
// actually enforced by the runtime: a capped Call cancels the spinning guest. The
// WAT source:
//
//	(module
//	  (memory (export "memory") 1)
//	  (func (export "spin") (result i32)
//	    (loop $l (br $l))
//	    (i32.const 0)))
var spinLoopWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f, 0x03,
	0x02, 0x01, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x11, 0x02, 0x06, 0x6d, 0x65, 0x6d, 0x6f,
	0x72, 0x79, 0x02, 0x00, 0x04, 0x73, 0x70, 0x69, 0x6e, 0x00, 0x00, 0x0a, 0x0b, 0x01, 0x09, 0x00,
	0x03, 0x40, 0x0c, 0x00, 0x0b, 0x41, 0x00, 0x0b,
}

// memoryTrapCapPages is the memory cap used by the memory-trap proof. The Extism
// runtime injects its own linear memory with a 16-page minimum, so a cap below 16
// is rejected at instantiation; 16 is the smallest cap that loads, which means
// the grow-loop guest runs and traps at Call time on its first out-of-bounds
// store rather than failing earlier at load. That makes 16 the cap that proves a
// genuine runtime memory trap on a successfully-loaded plugin.
const memoryTrapCapPages uint32 = 16

// TestLoaderEnforcesMemoryCapTrapsAtCall proves the loader's memory cap is
// enforced by the runtime: a plugin loaded with an explicit small cap that grows
// memory past the cap traps at Call time. The trap surfaces as a non-nil error
// from Plugin.Call wrapping the runtime's out-of-bounds memory access; the host
// does not silently let the guest exceed its memory ceiling.
func TestLoaderEnforcesMemoryCapTrapsAtCall(t *testing.T) {
	loader := &Loader{}
	plugin, err := loader.Load(context.Background(), PluginSpec{
		Name:           "grow-loop",
		Bytes:          growLoopWasm,
		MaxMemoryPages: memoryTrapCapPages,
	})
	if err != nil {
		t.Fatalf("Load with an explicit memory cap should succeed at the runtime's 16-page floor; got %v", err)
	}
	defer plugin.Close(context.Background())

	_, callErr := plugin.Call(context.Background(), "grow", nil)
	if callErr == nil {
		t.Fatalf("a guest growing memory past its cap must trap; Call returned nil error")
	}
}

// TestLoaderEnforcesTimeoutCancelsSpinningCall proves the loader's per-call
// ceiling still bites where it must: a plugin loaded with a small ceiling whose
// export spins forever — calling no host function, so every millisecond it burns
// is its own — is cancelled. The proof asserts the Call returns an error WITHIN a
// generous outer deadline; it does not assert exact timing, only that the
// cancellation fires before the test's own deadline. A Call that never returned
// would block the test until the outer deadline turned the hang into a loud
// failure.
//
// It is one half of a pair. This half would still pass if the ceiling charged
// every kind of time indiscriminately, and
// TestParseSourceProbe_HostTimeDoesNotSpendTheGuestCeiling is the other half,
// where a guest waiting on a slow HOST call is NOT cancelled. Neither alone pins
// the distinction the ceiling draws; together they do.
func TestLoaderEnforcesTimeoutCancelsSpinningCall(t *testing.T) {
	const callTimeoutMs = 50
	const outerDeadline = 10 * time.Second

	loader := &Loader{}
	plugin, err := loader.Load(context.Background(), PluginSpec{
		Name:      "spin-loop",
		Bytes:     spinLoopWasm,
		TimeoutMs: callTimeoutMs,
	})
	if err != nil {
		t.Fatalf("Load with an explicit timeout should succeed; got %v", err)
	}
	defer plugin.Close(context.Background())

	done := make(chan error, 1)
	go func() {
		_, callErr := plugin.Call(context.Background(), "spin", nil)
		done <- callErr
	}()

	select {
	case callErr := <-done:
		if callErr == nil {
			t.Fatalf("a spinning guest past its timeout must be cancelled; Call returned nil error")
		}
	case <-time.After(outerDeadline):
		t.Fatalf("Call did not return within %s — the per-call timeout did not cancel the spinning guest", outerDeadline)
	}
}
