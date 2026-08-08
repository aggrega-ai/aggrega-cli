// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"errors"
	"testing"
)

// fakeRawMemory is a fake pluginRawMemoryReader over a byte slice, so the
// streaming host-function body is exercised without a running guest. Read
// returns the in-bounds sub-slice, or (nil, false) for a range that overruns.
type fakeRawMemory struct {
	data []byte
}

func (f fakeRawMemory) Read(offset, byteCount uint32) ([]byte, bool) {
	end := int(offset) + int(byteCount)
	if int(offset) > len(f.data) || end > len(f.data) {
		return nil, false
	}
	return f.data[offset:end], true
}

// TestHandleCLIWrite_WritesRawRange proves the streaming body copies exactly the
// (pointer, length) range the guest names — not a length-table-derived string —
// to the injected writer.
func TestHandleCLIWrite_WritesRawRange(t *testing.T) {
	mem := fakeRawMemory{data: []byte("XXhello worldYY")}
	var buf bytes.Buffer
	p := &Plugin{name: "probe"}

	p.handleCLIWrite(mem, &buf, "aggrega_cli_write_stdout", []uint64{2, 11})

	if got := buf.String(); got != "hello world" {
		t.Fatalf("streamed bytes = %q; want %q", got, "hello world")
	}
}

// TestHandleCLIWrite_NilWriterDrops proves the defensive nil-writer guard: the
// compile-time profile injects no writer, so a body reached with a nil writer
// must drop silently rather than panic.
func TestHandleCLIWrite_NilWriterDrops(t *testing.T) {
	mem := fakeRawMemory{data: []byte("payload")}
	p := &Plugin{name: "probe"}

	// Must not panic and must not read memory it will discard.
	p.handleCLIWrite(mem, nil, "aggrega_cli_write_stdout", []uint64{0, 7})
}

// TestHandleCLIWrite_OutOfBoundsNoWrite proves an out-of-bounds range writes
// nothing (the read fails; the host logs and returns without trapping).
func TestHandleCLIWrite_OutOfBoundsNoWrite(t *testing.T) {
	mem := fakeRawMemory{data: []byte("short")}
	var buf bytes.Buffer
	p := &Plugin{name: "probe"}

	p.handleCLIWrite(mem, &buf, "aggrega_cli_write_stderr", []uint64{2, 100})

	if buf.Len() != 0 {
		t.Fatalf("wrote %q for an out-of-bounds range; want nothing", buf.String())
	}
}

// TestCLIAllowlist_DisjointFromSharedAllowlist is the single-source guard for
// the two profiles' disjointness: the streaming names live ONLY in
// cliHostFunctionAllowlist, never in the shared hostFunctionAllowlist. If a
// future edit merges them, the compile-time subset gate would begin admitting
// streaming and this guard fails.
func TestCLIAllowlist_DisjointFromSharedAllowlist(t *testing.T) {
	streaming := []string{"aggrega_cli_write_stdout", "aggrega_cli_write_stderr"}
	for _, name := range streaming {
		if hostFunctionAllowlist[name] {
			t.Errorf("%q is in the shared hostFunctionAllowlist; the compile-time profile must not admit streaming", name)
		}
		if !cliHostFunctionAllowlist[name] {
			t.Errorf("%q is missing from cliHostFunctionAllowlist", name)
		}
	}
	if len(cliHostFunctionAllowlist) != len(streaming) {
		t.Errorf("cliHostFunctionAllowlist has %d entries; want exactly the %d streaming names", len(cliHostFunctionAllowlist), len(streaming))
	}
}

// TestValidateHostFunctionsSubsetCLI_RejectsUnknown proves the CLI subset gate
// still rejects a name in neither allowlist, wrapping ErrWasmManifestRejected —
// the union grows the set by exactly the streaming names, nothing more.
func TestValidateHostFunctionsSubsetCLI_RejectsUnknown(t *testing.T) {
	// A streaming name passes.
	if err := validateHostFunctionsSubsetCLI("probe", "cli", []string{"aggrega_cli_write_stdout"}); err != nil {
		t.Fatalf("CLI gate rejected a streaming name: %v", err)
	}
	// A shared name still passes.
	if err := validateHostFunctionsSubsetCLI("probe", "cli", []string{"aggrega_log"}); err != nil {
		t.Fatalf("CLI gate rejected a shared allowlist name: %v", err)
	}
	// An unknown name is rejected.
	err := validateHostFunctionsSubsetCLI("probe", "cli", []string{"aggrega_cli_write_stdout", "aggrega_exfiltrate"})
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("CLI gate error = %v; want wrapping ErrWasmManifestRejected", err)
	}
}
