// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// customHostWasm is a real wasm32 module built by wat2wasm from a WAT source
// that imports the curated host function aggrega_log under the Extism custom-host
// namespace (extism:host/user) and exports a trivial "noop" function. It is a
// toolchain-produced binary (not bytes assembled in this test) used to prove the
// derivation namespace contract against a real module and to prove the loader
// registers a non-empty derived host-function set end-to-end. The WAT source:
//
//	(module
//	  (import "extism:host/user" "aggrega_log" (func $log (param i64 i64)))
//	  (memory (export "memory") 1)
//	  (func (export "noop") (result i32) (i32.const 0)))
var customHostWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x0a, 0x02, 0x60, 0x02, 0x7e, 0x7e, 0x00,
	0x60, 0x00, 0x01, 0x7f, 0x02, 0x20, 0x01, 0x10, 0x65, 0x78, 0x74, 0x69, 0x73, 0x6d, 0x3a, 0x68,
	0x6f, 0x73, 0x74, 0x2f, 0x75, 0x73, 0x65, 0x72, 0x0b, 0x61, 0x67, 0x67, 0x72, 0x65, 0x67, 0x61,
	0x5f, 0x6c, 0x6f, 0x67, 0x00, 0x00, 0x03, 0x02, 0x01, 0x01, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07,
	0x11, 0x02, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x04, 0x6e, 0x6f, 0x6f, 0x70,
	0x00, 0x01, 0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
}

// wasmImport is a (module, name) function import the test module-builder
// encodes into a hand-crafted Wasm import section.
type wasmImport struct {
	module string
	name   string
}

// buildWasmWithImports hand-crafts a minimal Wasm 1.0 module: the eight-byte
// header (magic + version) followed by an import section (section ID 2) listing
// the given function imports. No toolchain is involved — the byte layout is the
// stable Wasm 1.0 binary format, so the derivation's namespace contract is
// pinned deterministically. An empty imports slice produces a header with no
// import section (the absent-import-section case).
func buildWasmWithImports(imports []wasmImport) []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // \0asm + version 1
	if len(imports) == 0 {
		return out
	}

	var body []byte
	body = append(body, encodeULEB128(uint32(len(imports)))...)
	for _, imp := range imports {
		body = append(body, encodeName(imp.module)...)
		body = append(body, encodeName(imp.name)...)
		body = append(body, 0x00) // import kind: function
		body = append(body, 0x00) // type index 0 (uleb128)
	}

	out = append(out, wasmSectionImport)                   // section ID 2
	out = append(out, encodeULEB128(uint32(len(body)))...) // section size
	out = append(out, body...)
	return out
}

// encodeName length-prefixes a UTF-8 string per the Wasm name format.
func encodeName(s string) []byte {
	out := encodeULEB128(uint32(len(s)))
	return append(out, []byte(s)...)
}

// encodeULEB128 encodes a uint32 as little-endian base-128.
func encodeULEB128(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

// TestDeriveHostFunctions_NamespaceContract pins the namespace contract:
// imports under extism:host/user ARE derived; imports under extism:host/env and
// wasi_snapshot_preview1 are EXCLUDED. The output is canonical (sorted), so the
// expected set is in sorted order regardless of import order.
func TestDeriveHostFunctions_NamespaceContract(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmWithImports([]wasmImport{
		{module: extismHostUserNamespace, name: "aggrega_log"},
		{module: extismHostEnvNamespace, name: "input_length"},
		{module: wasiSnapshotPreview1Namespace, name: "fd_write"},
		{module: extismHostUserNamespace, name: "aggrega_emit_warning"},
	})

	got, err := DeriveHostFunctions("test-plugin", wasmBytes)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	want := []string{"aggrega_emit_warning", "aggrega_log"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("derived = %v; want %v (canonical sorted, custom-host namespace only)", got, want)
	}
}

// TestDeriveHostFunctions_EmptyImportSection proves a module with no
// import section yields an empty derived set.
func TestDeriveHostFunctions_EmptyImportSection(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmWithImports(nil)
	got, err := DeriveHostFunctions("test-plugin", wasmBytes)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("derived = %v; want empty for a module with no imports", got)
	}
}

// TestDeriveHostFunctions_NoCustomImports proves a module that imports
// only runtime-ABI and WASI functions yields an empty custom-host set — the
// shape every existing toolchain-built fixture has.
func TestDeriveHostFunctions_NoCustomImports(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmWithImports([]wasmImport{
		{module: extismHostEnvNamespace, name: "input_length"},
		{module: extismHostEnvNamespace, name: "output_set"},
		{module: wasiSnapshotPreview1Namespace, name: "fd_write"},
	})
	got, err := DeriveHostFunctions("test-plugin", wasmBytes)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("derived = %v; want empty (no extism:host/user imports)", got)
	}
}

// TestDeriveHostFunctions_CanonicalSortedAndDeduped proves the output is
// canonical: imports out of alphabetical order, with a duplicate, yield a
// sorted, de-duplicated set, so a re-derive of the same .wasm is byte-identical
// and a stamp comparison is a plain equality.
func TestDeriveHostFunctions_CanonicalSortedAndDeduped(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmWithImports([]wasmImport{
		{module: extismHostUserNamespace, name: "aggrega_log"},
		{module: extismHostUserNamespace, name: "aggrega_emit_warning"},
		{module: extismHostUserNamespace, name: "aggrega_log"},
	})
	got, err := DeriveHostFunctions("test-plugin", wasmBytes)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	want := []string{"aggrega_emit_warning", "aggrega_log"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("derived = %v; want %v (canonical: sorted + de-duplicated)", got, want)
	}
}

// TestVerifyDerivedAgainstAllowlist proves the derived set is verified against
// the curated allowlist by the same subset gate the loader reuses: a derived
// custom host function outside the allowlist halts with ErrWasmManifestRejected.
func TestVerifyDerivedAgainstAllowlist(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmWithImports([]wasmImport{
		{module: extismHostUserNamespace, name: "rogue_function"},
	})
	derived, err := DeriveHostFunctions("test-plugin", wasmBytes)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	err = validateHostFunctionsSubset("test-plugin", "provider", derived)
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("expected ErrWasmManifestRejected for a non-allowlisted derived host function; got %v", err)
	}
	if !strings.Contains(err.Error(), "rogue_function") {
		t.Errorf("error should name the violating host function; got %v", err)
	}
}

// TestDeriveHostFunctions_RealToolchainModule proves the derivation
// namespace contract against a real wat2wasm-built module that imports a custom
// host function under extism:host/user.
func TestDeriveHostFunctions_RealToolchainModule(t *testing.T) {
	t.Parallel()
	got, err := DeriveHostFunctions("custom-host", customHostWasm)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	want := []string{"aggrega_log"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("derived = %v; want %v from the real toolchain-built module", got, want)
	}
}

// TestLoad_RegistersDerivedNonEmptyHostFunctionSet proves the full load path on a
// real toolchain-built module with a non-empty custom host-function import:
// derivation finds aggrega_log, the verify-subset gate passes (it is
// allowlisted), the loader registers the derived set, and the Extism plugin
// constructs successfully end-to-end.
func TestLoad_RegistersDerivedNonEmptyHostFunctionSet(t *testing.T) {
	t.Parallel()
	loader := &Loader{}
	plugin, err := loader.Load(context.Background(), PluginSpec{
		Name:  "custom-host",
		Bytes: customHostWasm,
	})
	if err != nil {
		t.Fatalf("Load with a derived non-empty allowlisted host-function set should succeed; got %v", err)
	}
	defer plugin.Close(context.Background())
}
