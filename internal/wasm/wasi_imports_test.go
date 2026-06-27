// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"
)

// TestValidateWasiImportsAcceptsAllowlistedNames builds a Wasm
// binary fragment importing every name in wasiImportAllowlist and
// asserts the firewall accepts the module. The fragment is
// deliberately minimal — header + import section + nothing else —
// because the firewall walks only the import section.
func TestValidateWasiImportsAcceptsAllowlistedNames(t *testing.T) {
	t.Parallel()
	names := sortedKeys(wasiImportAllowlist)
	wasmBytes := buildWasmModuleWithImports(t, importsForNames(wasiSnapshotPreview1Namespace, names))
	if err := validateWasiImports("allowlist-probe", wasmBytes); err != nil {
		t.Errorf("validateWasiImports rejected an allowlist-only module: %v", err)
	}
}

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

// TestValidateWasiImportsAcceptsStdGoWasip1Set asserts the firewall
// accepts the exact import set a standard-Go GOOS=wasip1 reactor
// emits — the load-bearing property that lets the Go PDK ship as a
// std-Go wasip1 plugin. A regression that drops any of these names
// from the allowlist fails here loudly.
func TestValidateWasiImportsAcceptsStdGoWasip1Set(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmModuleWithImports(t, importsForNames(wasiSnapshotPreview1Namespace, stdGoWasip1Imports))
	if err := validateWasiImports("stdgo-wasip1-probe", wasmBytes); err != nil {
		t.Errorf("validateWasiImports rejected the std-Go GOOS=wasip1 import set: %v", err)
	}
	// Every name in the std-Go set must be on the allowlist explicitly.
	for _, name := range stdGoWasip1Imports {
		if !wasiImportAllowlist[name] {
			t.Errorf("std-Go wasip1 import %q missing from wasiImportAllowlist", name)
		}
	}
}

// TestValidateWasiImportsRejectsCapabilityGrantingNames runs a
// table-driven check over every entry in wasiCapabilityDenylist: each
// name MUST produce an ErrWasmManifestRejected error whose message
// embeds both the import name and the capability label.
func TestValidateWasiImportsRejectsCapabilityGrantingNames(t *testing.T) {
	t.Parallel()
	for name, capability := range wasiCapabilityDenylist {
		name, capability := name, capability
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wasmBytes := buildWasmModuleWithImports(t, importsForNames(
				wasiSnapshotPreview1Namespace, []string{name}))
			err := validateWasiImports("capability-probe", wasmBytes)
			if err == nil {
				t.Fatalf("expected rejection of wasi_snapshot_preview1.%s; got nil", name)
			}
			if !errors.Is(err, ErrWasmManifestRejected) {
				t.Errorf("error does not wrap ErrWasmManifestRejected: %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, name) {
				t.Errorf("error message missing import name %q: %v", name, err)
			}
			if !strings.Contains(msg, capability) {
				t.Errorf("error message missing capability label %q: %v", capability, err)
			}
		})
	}
}

// TestValidateWasiImportsRejectsFilesystemAndSocketImports pins the
// specific capability-granting imports that MUST stay rejected even
// though the allowlist expanded for the std-Go reactor set: directory
// traversal (fd_readdir), file paths (path_open), and sockets
// (sock_*). These are the capabilities the firewall exists to deny.
func TestValidateWasiImportsRejectsFilesystemAndSocketImports(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"fd_readdir", "path_open", "sock_open", "sock_connect"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wasmBytes := buildWasmModuleWithImports(t, importsForNames(
				wasiSnapshotPreview1Namespace, []string{name}))
			err := validateWasiImports("denied-probe", wasmBytes)
			if err == nil {
				t.Fatalf("expected rejection of wasi_snapshot_preview1.%s; got nil", name)
			}
			if !errors.Is(err, ErrWasmManifestRejected) {
				t.Errorf("error does not wrap ErrWasmManifestRejected: %v", err)
			}
			if wasiImportAllowlist[name] {
				t.Errorf("capability-granting import %q must not be on the allowlist", name)
			}
		})
	}
}

// TestValidateWasiImportsRejectsUnknownNames covers the default-deny
// path: an import under wasi_snapshot_preview1 that is on neither the
// allowlist nor the denylist must be rejected.
func TestValidateWasiImportsRejectsUnknownNames(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmModuleWithImports(t, importsForNames(
		wasiSnapshotPreview1Namespace, []string{"does_not_exist"}))
	err := validateWasiImports("unknown-probe", wasmBytes)
	if err == nil {
		t.Fatalf("expected rejection of unknown WASI import; got nil")
	}
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Errorf("error does not wrap ErrWasmManifestRejected: %v", err)
	}
	if !strings.Contains(err.Error(), "does_not_exist") {
		t.Errorf("error message missing the unknown import name: %v", err)
	}
}

// TestValidateWasiImportsIgnoresOtherNamespaces confirms the firewall
// pass is scoped to wasi_snapshot_preview1: imports under
// extism:host/env and extism:host/user pass through this pass without
// rejection (the host-function allowlist in manifest_translate.go
// owns those surfaces).
func TestValidateWasiImportsIgnoresOtherNamespaces(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmModuleWithImports(t, []functionImport{
		{Module: "extism:host/env", Name: "alloc"},
		{Module: "extism:host/user", Name: "aggrega_log"},
	})
	if err := validateWasiImports("other-ns-probe", wasmBytes); err != nil {
		t.Errorf("firewall must not touch non-WASI namespaces: %v", err)
	}
}

// TestValidateWasiImportsAcceptsEmptyImportSection confirms a Wasm
// module with no import section validates cleanly. Plugins that
// import nothing are structurally valid.
func TestValidateWasiImportsAcceptsEmptyImportSection(t *testing.T) {
	t.Parallel()
	wasmBytes := buildWasmModuleWithImports(t, nil)
	if err := validateWasiImports("empty-probe", wasmBytes); err != nil {
		t.Errorf("validateWasiImports rejected an import-section-free module: %v", err)
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
	if !errors.Is(err, ErrWasmGuestPanic) {
		t.Errorf("generic panic must wrap ErrWasmGuestPanic: %v", err)
	}
	if errors.Is(err, ErrWasmPluginExited) {
		t.Errorf("generic panic must not wrap ErrWasmPluginExited: %v", err)
	}
}

// TestWasiImportAllowlistAndDenylistDisjoint confirms no import name
// appears on both maps — the firewall classification would be
// ambiguous otherwise. poll_oneoff moved from the denylist to the
// allowlist; this guards against a future re-introduction collision.
func TestWasiImportAllowlistAndDenylistDisjoint(t *testing.T) {
	t.Parallel()
	for name := range wasiImportAllowlist {
		if _, denied := wasiCapabilityDenylist[name]; denied {
			t.Errorf("name %q appears on both allowlist and denylist", name)
		}
	}
}

// --- helpers ---------------------------------------------------------

// sortedKeys returns the keys of m sorted alphabetically. Used for
// deterministic test iteration over allowlist/denylist maps.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// importsForNames builds a functionImport slice for the given names
// under the given namespace. Convenience for the table-driven tests.
func importsForNames(namespace string, names []string) []functionImport {
	out := make([]functionImport, 0, len(names))
	for _, name := range names {
		out = append(out, functionImport{Module: namespace, Name: name})
	}
	return out
}

// buildWasmModuleWithImports synthesises a minimal Wasm 1.0 binary
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
