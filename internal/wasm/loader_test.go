// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	extism "github.com/extism/go-sdk"
)

func TestTranslateRejectsAllowedHosts(t *testing.T) {
	spec := PluginSpec{
		Name:         "test-plugin",
		Path:         "fixture.wasm",
		AllowedHosts: []string{"example.com"},
	}
	_, err := translateToManifest(spec)
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("expected ErrWasmManifestRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "AllowedHosts") {
		t.Errorf("error should name AllowedHosts capability; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "test-plugin") {
		t.Errorf("error should name the plugin identifier; got: %s", err.Error())
	}
}

func TestTranslateRejectsAllowedPaths(t *testing.T) {
	// AllowedPaths is set only via direct manifest construction; the
	// public PluginSpec API does not expose a way to set it (by design;
	// AOT-purity discipline). This test exercises the firewall against
	// a manifest constructed directly with AllowedPaths non-empty.
	manifest := extism.Manifest{
		AllowedPaths: map[string]string{"/etc": "/etc"},
	}
	err := enforceAOTPurityFirewall(manifest, "test-plugin")
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("expected ErrWasmManifestRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "AllowedPaths") {
		t.Errorf("error should name AllowedPaths capability; got: %s", err.Error())
	}
}

func TestTranslateRejectsDeclaredHostFunctionOutsideAllowlist(t *testing.T) {
	spec := PluginSpec{
		Name:                  "test-plugin",
		Path:                  "fixture.wasm",
		DeclaredHostFunctions: []string{"aggrega_log", "rogue_function"},
	}
	_, err := translateToManifest(spec)
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("expected ErrWasmManifestRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "rogue_function") {
		t.Errorf("error should name the violating host function; got: %s", err.Error())
	}
}

func TestTranslateAcceptsAllowlistedHostFunctions(t *testing.T) {
	spec := PluginSpec{
		Name:                  "test-plugin",
		Path:                  "fixture.wasm",
		DeclaredHostFunctions: []string{"aggrega_log", "aggrega_emit_warning"},
	}
	_, err := translateToManifest(spec)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
}

func TestTranslateMinimalSpecPasses(t *testing.T) {
	spec := PluginSpec{
		Name: "test-plugin",
		Path: "fixture.wasm",
	}
	manifest, err := translateToManifest(spec)
	if err != nil {
		t.Fatalf("expected success for minimal spec, got error: %v", err)
	}
	if len(manifest.Wasm) != 1 {
		t.Errorf("expected exactly one wasm source, got %d", len(manifest.Wasm))
	}
	if len(manifest.AllowedHosts) != 0 {
		t.Errorf("expected empty AllowedHosts, got %v", manifest.AllowedHosts)
	}
	if len(manifest.AllowedPaths) != 0 {
		t.Errorf("expected empty AllowedPaths, got %v", manifest.AllowedPaths)
	}
}

func TestTranslateAppliesMemoryAndTimeout(t *testing.T) {
	spec := PluginSpec{
		Name:           "test-plugin",
		Path:           "fixture.wasm",
		MaxMemoryPages: 32,
		TimeoutMs:      1500,
	}
	manifest, err := translateToManifest(spec)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if manifest.Memory == nil {
		t.Fatalf("expected Memory to be set when MaxMemoryPages != 0")
	}
	if manifest.Memory.MaxPages != 32 {
		t.Errorf("expected MaxPages=32, got %d", manifest.Memory.MaxPages)
	}
	if manifest.Timeout != 1500 {
		t.Errorf("expected Timeout=1500, got %d", manifest.Timeout)
	}
}

func TestAllowlistContainsBaselineHostFunctions(t *testing.T) {
	for _, name := range []string{"aggrega_log", "aggrega_emit_warning"} {
		if _, ok := hostFunctionAllowlist[name]; !ok {
			t.Errorf("baseline host function %q missing from allowlist", name)
		}
	}
}

// TestCallWithPanicRecoveryWrapsPanic exercises the panic-recovery wrap
// without needing a real Wasm-side panic source. The wrap converts a
// Go-side panic into an ErrWasmGuestPanic-wrapped error; this test
// passes a function that panics directly and asserts the wrap shape.
//
// Coverage gap: this test verifies the wrap shape only. It does NOT
// prove that an actual Wasm trap (for example the unreachable
// instruction in guest code, divide-by-zero, out-of-bounds memory
// access) propagates through Extism's call boundary as a panic that
// reaches this wrap. Extism translates most Wasm traps into Go errors
// rather than Go panics; the wrap is the safety net behind that error
// path. End-to-end coverage of a real Wasm-side panic source requires
// a hand-authored panic.wasm fixture — see testdata/README.md for the
// authoring path when a Wasm toolchain is available.
func TestCallWithPanicRecoveryWrapsPanic(t *testing.T) {
	output, err := callWithPanicRecovery("test-plugin", "test_function", func() ([]byte, error) {
		panic("synthetic test panic")
	})
	if err == nil {
		t.Fatalf("expected error from panicking function, got nil with output %q", output)
	}
	if !errors.Is(err, ErrWasmGuestPanic) {
		t.Errorf("expected error to wrap ErrWasmGuestPanic; got: %v", err)
	}
	if !strings.Contains(err.Error(), "test-plugin") {
		t.Errorf("error should name the plugin identifier; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "test_function") {
		t.Errorf("error should name the called function; got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "synthetic test panic") {
		t.Errorf("error should include the panic value; got: %s", err.Error())
	}
}

func TestCallWithPanicRecoveryPassesNormalReturns(t *testing.T) {
	output, err := callWithPanicRecovery("test-plugin", "test_function", func() ([]byte, error) {
		return []byte("expected output"), nil
	})
	if err != nil {
		t.Fatalf("expected nil error from non-panicking function, got %v", err)
	}
	if string(output) != "expected output" {
		t.Errorf("expected output to pass through; got %q", string(output))
	}
}

func TestCallWithPanicRecoveryPassesNormalErrors(t *testing.T) {
	sentinel := fmt.Errorf("regular error path")
	_, err := callWithPanicRecovery("test-plugin", "test_function", func() ([]byte, error) {
		return nil, sentinel
	})
	if err != sentinel {
		t.Errorf("expected sentinel error to pass through unwrapped; got %v", err)
	}
	if errors.Is(err, ErrWasmGuestPanic) {
		t.Error("non-panic error path should not wrap ErrWasmGuestPanic")
	}
}

