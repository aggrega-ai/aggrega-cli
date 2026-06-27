// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"encoding/binary"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// TestWasiProbeDeterministicValues proves the byte-level deterministic
// contract the loader's WASI configuration promises, against the real
// wasi-probe fixture. The Extism Plugin wrapper exposes only the Extism
// kernel's memory, not a non-PDK guest's main-module memory, so the
// fixture's observation buffer cannot be read through the production
// loader path (TestPluginRoundTrip_WasiProbeRust covers load + run +
// no-trap there). This test instantiates the same fixture directly on a
// wazero runtime using the EXACT production ModuleConfig
// (deterministicModuleConfig) so the assertion is over the real
// configuration, not a re-declared copy.
//
// The fixture writes a 40-byte observation buffer and exercises three
// WASI imports against it:
//
//	bytes  0.. 8  clock_time_get result (u64 LE) — frozen wall time → 0
//	bytes  8..24  random_get buffer (16 bytes)    — zero entropy → all 0
//	bytes 24..28  fd_write written count (u32 LE) — requested 13 bytes
//	bytes 28..32  fd_write errno (u32 LE)         — success → 0
func TestWasiProbeDeterministicValues(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "wasi-probe-rust.wasm")

	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)

	mod, err := runtime.InstantiateWithConfig(ctx, wasmBytes, deterministicModuleConfig())
	if err != nil {
		t.Fatalf("instantiate wasi-probe under deterministic config: %v", err)
	}

	fn := mod.ExportedFunction("probe_wasi_stubs")
	if fn == nil {
		t.Fatal("fixture does not export probe_wasi_stubs")
	}
	res, err := fn.Call(ctx)
	if err != nil {
		t.Fatalf("probe_wasi_stubs call: %v", err)
	}
	offset := uint32(res[0])

	buf, ok := mod.Memory().Read(offset, 40)
	if !ok {
		t.Fatalf("read observation buffer at offset %d: out of bounds", offset)
	}

	// clock_time_get → frozen observable wall time (zero).
	if got := binary.LittleEndian.Uint64(buf[0:8]); got != 0 {
		t.Errorf("clock_time_get observed %d; want 0 (frozen walltime)", got)
	}

	// random_get → zero entropy: all 16 bytes zero.
	for i, b := range buf[8:24] {
		if b != 0 {
			t.Errorf("random_get byte %d = %#x; want 0 (zero entropy source)", i, b)
		}
	}

	// fd_write → discards but reports the requested byte count
	// ("probe message" is 13 bytes) with errno 0.
	if got := binary.LittleEndian.Uint32(buf[24:28]); got != 13 {
		t.Errorf("fd_write reported written count %d; want 13", got)
	}
	if got := binary.LittleEndian.Uint32(buf[28:32]); got != 0 {
		t.Errorf("fd_write errno %d; want 0 (success)", got)
	}
}
