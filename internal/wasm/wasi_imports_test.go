// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// stdGoWasip1Imports is the WASI preview-1 import set a standard-Go
// GOOS=wasip1 reactor emits. The firewall MUST accept every one of
// these, otherwise a std-Go guest cannot load. Verified against a
// std-Go c-shared probe's import section.
var stdGoWasip1Imports = []string{
	"args_get", "args_sizes_get",
	"clock_time_get",
	"environ_get", "environ_sizes_get",
	"fd_close", "fd_fdstat_get", "fd_fdstat_set_flags",
	"fd_prestat_dir_name", "fd_prestat_get", "fd_write",
	"poll_oneoff", "proc_exit", "random_get", "sched_yield",
}

// TestValidateImportModulesAcceptsStdGoWasip1Set asserts the firewall
// accepts the exact import set a standard-Go GOOS=wasip1 reactor
// emits — the load-bearing property that lets the Go PDK ship as a
// std-Go wasip1 plugin. Every name here declares the
// wasi_snapshot_preview1 module, which is an allowed import module.
func TestValidateImportModulesAcceptsStdGoWasip1Set(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmModuleWithImports(t, importsForNames(wasiSnapshotPreview1Namespace, stdGoWasip1Imports))
	if err := validateImportModules("stdgo-wasip1-probe", wasmBytes); err != nil {
		t.Errorf("validateImportModules rejected the std-Go GOOS=wasip1 import set: %v", err)
	}
}

// TestValidateImportModulesAcceptsAnyWasiFunctionName pins the core
// property of the capability firewall: WASI function names are no
// longer censored. A guest importing filesystem-, directory-, and
// socket-granting WASI functions under wasi_snapshot_preview1 now
// loads, because naming a WASI import is not exercising a capability —
// the loader denies the capability itself at instantiation (no
// preopened filesystem, no sockets). A statically-linked language
// runtime pulls these in transitively without ever calling them.
func TestValidateImportModulesAcceptsAnyWasiFunctionName(t *testing.T) {
	t.Parallel()
	formerlyDenied := []string{
		"path_open", "fd_readdir", "sock_accept", "sock_shutdown",
		"fd_filestat_set_size",
	}
	for _, name := range formerlyDenied {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wasmBytes := buildWasmModuleWithImports(t, importsForNames(
				wasiSnapshotPreview1Namespace, []string{name}))
			if err := validateImportModules("wasi-name-probe", wasmBytes); err != nil {
				t.Errorf("validateImportModules rejected wasi_snapshot_preview1.%s; any WASI function name under an allowed module must pass: %v", name, err)
			}
		})
	}
}

// TestValidateImportModulesAcceptsExtismNamespaces confirms imports
// under the two Extism module namespaces — extism:host/env (the
// runtime ABI) and extism:host/user (Aggrega's curated host functions)
// — load cleanly. Both are allowed import modules.
func TestValidateImportModulesAcceptsExtismNamespaces(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmModuleWithImports(t, []functionImport{
		{Module: extismHostEnvNamespace, Name: "input_length"},
		{Module: extismHostEnvNamespace, Name: "alloc"},
		{Module: extismHostUserNamespace, Name: "aggrega_log"},
		{Module: extismHostUserNamespace, Name: "aggrega_emit_warning"},
	})
	if err := validateImportModules("extism-ns-probe", wasmBytes); err != nil {
		t.Errorf("validateImportModules rejected the Extism namespaces: %v", err)
	}
}

// TestValidateImportModulesRejectsUnknownModule is the core firewall
// test: an import declaring a module namespace outside the allowed set
// is rejected with an error wrapping ErrWasmManifestRejected whose
// message names the offending module. This is what the import-module
// firewall exists to catch — a guest naming a surface the host does
// not provide.
func TestValidateImportModulesRejectsUnknownModule(t *testing.T) {
	t.Parallel()
	const badModule = "evil:host/net"
	wasmBytes := buildWasmModuleWithImports(t, []functionImport{
		{Module: badModule, Name: "connect"},
	})
	err := validateImportModules("unknown-module-probe", wasmBytes)
	if err == nil {
		t.Fatalf("expected rejection of an import under module %q; got nil", badModule)
	}
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Errorf("error does not wrap ErrWasmManifestRejected: %v", err)
	}
	if !strings.Contains(err.Error(), badModule) {
		t.Errorf("error message missing the offending module %q: %v", badModule, err)
	}
}

// TestValidateImportModulesRejectsUnknownModuleAmongAllowed confirms
// the firewall inspects every import, not just the first: a disallowed
// module mixed in with allowed ones is still caught.
func TestValidateImportModulesRejectsUnknownModuleAmongAllowed(t *testing.T) {
	t.Parallel()
	const badModule = "evil:host/fs"
	wasmBytes := buildWasmModuleWithImports(t, []functionImport{
		{Module: wasiSnapshotPreview1Namespace, Name: "fd_write"},
		{Module: extismHostUserNamespace, Name: "aggrega_log"},
		{Module: badModule, Name: "open"},
	})
	err := validateImportModules("mixed-module-probe", wasmBytes)
	if err == nil {
		t.Fatalf("expected rejection of an import under module %q; got nil", badModule)
	}
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Errorf("error does not wrap ErrWasmManifestRejected: %v", err)
	}
	if !strings.Contains(err.Error(), badModule) {
		t.Errorf("error message missing the offending module %q: %v", badModule, err)
	}
}

// TestValidateImportModulesAcceptsEmptyImportSection confirms a Wasm
// module with no import section validates cleanly. Plugins that
// import nothing are structurally valid.
func TestValidateImportModulesAcceptsEmptyImportSection(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmModuleWithImports(t, nil)
	if err := validateImportModules("empty-probe", wasmBytes); err != nil {
		t.Errorf("validateImportModules rejected an import-section-free module: %v", err)
	}
}

// TestParseFunctionImportsRejectsBadHeader exercises the parser's
// header check: short bytes, bad magic, bad version all produce a
// descriptive error rather than panic.
func TestParseFunctionImportsRejectsBadHeader(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		bytes []byte
	}{
		{"empty", nil},
		{"too short", []byte{0x00, 0x61, 0x73}},
		{"bad magic", []byte{0xff, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}},
		{"bad version", []byte{0x00, 0x61, 0x73, 0x6d, 0x02, 0x00, 0x00, 0x00}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFunctionImports(tc.bytes)
			if err == nil {
				t.Errorf("expected error for %s header; got nil", tc.name)
			}
		})
	}
}

// TestCallWithPanicRecoveryWrapsGuestPanic confirms a Go-side panic
// escaping Extism's error envelope translates to an error wrapping
// ErrWasmGuestPanic. proc_exit no longer routes through a panic; it
// surfaces as a non-zero exit code in Plugin.Call (see
// TestPluginRoundTrip_WasiProbeRust's sibling exit-code coverage).
func TestCallWithPanicRecoveryWrapsGuestPanic(t *testing.T) {
	t.Parallel()
	_, err := callWithPanicRecovery("test-plugin", "test-func", func() ([]byte, error) {
		panic("generic panic")
	})
	if err == nil {
		t.Fatal("expected error from generic panic; got nil")
	}
	if !errors.Is(err, ErrWasmGuestPanic()) {
		t.Errorf("generic panic must wrap ErrWasmGuestPanic: %v", err)
	}
	if errors.Is(err, ErrWasmPluginExited()) {
		t.Errorf("generic panic must not wrap ErrWasmPluginExited: %v", err)
	}
}

// --- helpers ---------------------------------------------------------

// importsForNames builds a functionImport slice for the given names
// under the given namespace. Convenience for the table-driven tests.
func importsForNames(namespace string, names []string) []functionImport {
	out := make([]functionImport, 0, len(names))
	for _, name := range names {
		out = append(out, functionImport{Module: namespace, Name: name})
	}
	return out
}

// buildWasmModuleWithImports synthesizes a minimal Wasm 1.0 binary
// declaring the given function imports. The module has:
//
//   - Header (8 bytes): magic + version
//   - Type section (id 1): one void type "() -> ()" if any imports
//     are declared
//   - Import section (id 2): one function import per entry in imps
//     (all share type index 0; the firewall doesn't read types)
//
// No function, code, export, or data sections — the bytes are
// import-section-validation fodder, not a runnable module.
//
// Returns the byte slice; never returns an error (the construction is
// internal and well-defined). Calls t.Fatalf if the helper itself
// fails to write into its bytes.Buffer (essentially impossible).
func buildWasmModuleWithImports(t *testing.T, imps []functionImport) []byte {
	t.Helper()
	var buf bytes.Buffer

	// Header: magic + version
	buf.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})

	if len(imps) == 0 {
		return buf.Bytes()
	}

	// Type section (id 1) with one void function type:
	//   form 0x60 (func), param count 0, result count 0
	typeSectionBody := []byte{0x01, 0x60, 0x00, 0x00}
	writeSection(&buf, 0x01, typeSectionBody)

	// Import section (id 2) with one function import per imp.
	var importBody bytes.Buffer
	writeULEB128(&importBody, uint32(len(imps)))
	for _, imp := range imps {
		writeWasmName(&importBody, imp.Module)
		writeWasmName(&importBody, imp.Name)
		importBody.WriteByte(0x00)   // kind: function
		writeULEB128(&importBody, 0) // type index 0
	}
	writeSection(&buf, 0x02, importBody.Bytes())

	return buf.Bytes()
}

// writeSection writes a length-prefixed Wasm section to w.
func writeSection(w *bytes.Buffer, id byte, body []byte) {
	w.WriteByte(id)
	writeULEB128(w, uint32(len(body)))
	w.Write(body)
}

// writeULEB128 encodes v as Little-Endian Base 128 unsigned integer.
func writeULEB128(w *bytes.Buffer, v uint32) {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
			w.WriteByte(b)
			continue
		}
		w.WriteByte(b)
		return
	}
}

// writeWasmName encodes a length-prefixed UTF-8 string.
func writeWasmName(w *bytes.Buffer, s string) {
	writeULEB128(w, uint32(len(s)))
	w.WriteString(s)
}
