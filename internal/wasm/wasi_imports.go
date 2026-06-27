// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"fmt"
)

// wasiSnapshotPreview1Namespace is the Wasm import namespace WASI
// preview-1 imports declare. Plugins built for the standard-Go
// GOOS=wasip1 target, the Rust wasm32-wasip1 target, the Extism JS PDK
// (via extism-js / QuickJS), and AssemblyScript's WASI bindings all
// import under this exact namespace string.
const wasiSnapshotPreview1Namespace = "wasi_snapshot_preview1"

// wasiImportAllowlist names the WASI preview-1 imports the firewall
// permits. A plugin importing only these names loads through the
// static import firewall; the Extism-bundled wazero WASI runtime,
// instantiated under the deterministic ModuleConfig (see loader.go),
// supplies their behaviour.
//
// The allowlist is bounded to benign runtime-init imports — the set a
// language runtime touches unconditionally during startup (clocks,
// entropy, stdio, environment and argument discovery, scheduler yield,
// and process exit) plus the file-descriptor metadata calls a guest's
// stdio layer issues. Imports outside this set (filesystem traversal,
// directory access, network sockets) grant runtime capabilities and
// are categorically rejected.
//
// The set is verified against the imports a standard-Go GOOS=wasip1
// reactor emits; it may shift slightly by Go or toolchain version, in
// which case a newly-emitted benign import is added here after review.
var wasiImportAllowlist = map[string]bool{
	"fd_write":            true,
	"fd_read":             true,
	"fd_close":            true,
	"fd_seek":             true,
	"fd_tell":             true,
	"fd_fdstat_get":       true,
	"fd_fdstat_set_flags": true,
	"fd_prestat_get":      true,
	"fd_prestat_dir_name": true,
	"clock_time_get":      true,
	"clock_res_get":       true,
	"random_get":          true,
	"environ_get":         true,
	"environ_sizes_get":   true,
	"args_get":            true,
	"args_sizes_get":      true,
	"poll_oneoff":         true,
	"sched_yield":         true,
	"proc_exit":           true,
}

// wasiCapabilityDenylist names WASI preview-1 imports the firewall
// rejects with an explanatory diagnostic. The map's value is the
// human-readable capability the import would grant; the firewall
// embeds it in the rejection error so callers see exactly which
// invariant the rejection protects.
//
// Imports not on either the allowlist or the denylist default-deny;
// the firewall produces a generic "not on the import allowlist"
// diagnostic for those. The denylist exists to give frequently-
// encountered rejections a clear capability label.
var wasiCapabilityDenylist = map[string]string{
	"path_open":               "filesystem capability (violates No Ghost Infrastructure)",
	"path_create_directory":   "filesystem capability (violates No Ghost Infrastructure)",
	"path_filestat_get":       "filesystem capability (violates No Ghost Infrastructure)",
	"path_filestat_set_size":  "filesystem capability (violates No Ghost Infrastructure)",
	"path_filestat_set_times": "filesystem capability (violates No Ghost Infrastructure)",
	"path_link":               "filesystem capability (violates No Ghost Infrastructure)",
	"path_readlink":           "filesystem capability (violates No Ghost Infrastructure)",
	"path_remove_directory":   "filesystem capability (violates No Ghost Infrastructure)",
	"path_rename":             "filesystem capability (violates No Ghost Infrastructure)",
	"path_symlink":            "filesystem capability (violates No Ghost Infrastructure)",
	"path_unlink_file":        "filesystem capability (violates No Ghost Infrastructure)",
	"fd_readdir":              "filesystem-traversal capability (violates No Ghost Infrastructure)",
	"sock_accept":             "network capability (violates AOT Purity)",
	"sock_recv":               "network capability (violates AOT Purity)",
	"sock_send":               "network capability (violates AOT Purity)",
	"sock_shutdown":           "network capability (violates AOT Purity)",
}

// validateWasiImports walks the plugin Wasm binary's import section
// and applies the static import firewall pass. Each function import
// under wasi_snapshot_preview1 must be in wasiImportAllowlist; any
// import in wasiCapabilityDenylist is rejected with a capability-
// label diagnostic; any other wasi_snapshot_preview1 import is
// rejected as "not on the import allowlist".
//
// Returns nil on success; returns an error wrapping
// ErrWasmManifestRejected on the first rejection encountered. The
// function name embedded in the error message identifies the
// violating import.
//
// Imports under namespaces other than wasi_snapshot_preview1
// (extism:host/env, extism:host/user, etc.) are not the concern of
// this pass; the host-function allowlist in manifest_translate.go
// handles those.
func validateWasiImports(pluginName string, wasmBytes []byte) error {
	imports, err := parseFunctionImports(wasmBytes)
	if err != nil {
		return fmt.Errorf("plugin %q: parsing wasm import section: %w",
			pluginName, err)
	}
	for _, imp := range imports {
		if imp.Module != wasiSnapshotPreview1Namespace {
			continue
		}
		if wasiImportAllowlist[imp.Name] {
			continue
		}
		if capability, denied := wasiCapabilityDenylist[imp.Name]; denied {
			return fmt.Errorf("plugin %q imports wasi_snapshot_preview1.%s: %s: %w",
				pluginName, imp.Name, capability, ErrWasmManifestRejected)
		}
		return fmt.Errorf("plugin %q imports wasi_snapshot_preview1.%s: not on the import allowlist: %w",
			pluginName, imp.Name, ErrWasmManifestRejected)
	}
	return nil
}

// wasmMagicBytes is the four-byte preamble every Wasm binary begins
// with ("\0asm" in ASCII; 0x00 0x61 0x73 0x6d in network order).
var wasmMagicBytes = [4]byte{0x00, 0x61, 0x73, 0x6d}

// wasmVersionV1 is the Wasm 1.0 module version. The firewall reads
// only version-1 modules; later spec revisions (e.g. WASI preview 2's
// component-model binary format) need explicit support added.
var wasmVersionV1 = [4]byte{0x01, 0x00, 0x00, 0x00}

// wasmSectionImport is the section ID for the import section in the
// Wasm 1.0 binary format.
const wasmSectionImport byte = 0x02

// wasmImportKindFunction is the import-descriptor kind tag for
// function imports.
const wasmImportKindFunction byte = 0x00

// parseFunctionImports walks the Wasm binary's import section and
// returns every function import as a (module, name) pair. Other
// import kinds (memory, table, global) are skipped — the AOT-purity
// firewall only cares about function imports because every WASI
// capability the firewall guards (filesystem, network, time, entropy)
// is exposed as a function import.
//
// Returns nil + empty slice when the binary's import section is
// absent (a plugin importing nothing is structurally valid). Returns
// an error wrapping a parse diagnostic when the bytes are not a
// well-formed Wasm 1.0 module or the import section is malformed.
//
// The parser is intentionally minimal: it walks sections by their
// length prefix, decodes only the import section's entries, and
// stops at section ID 2 since every later section is irrelevant to
// the firewall's check. Wasm 1.0's binary format is small and
// stable; the parser does not depend on Wazero or any other Wasm
// runtime so the firewall stays isolated from runtime internals.
func parseFunctionImports(wasmBytes []byte) ([]functionImport, error) {
	if len(wasmBytes) < 8 {
		return nil, fmt.Errorf("wasm binary too short (%d bytes; need at least 8 for header)", len(wasmBytes))
	}
	if [4]byte{wasmBytes[0], wasmBytes[1], wasmBytes[2], wasmBytes[3]} != wasmMagicBytes {
		return nil, fmt.Errorf("wasm binary magic mismatch (got %x; want %x)",
			wasmBytes[0:4], wasmMagicBytes)
	}
	if [4]byte{wasmBytes[4], wasmBytes[5], wasmBytes[6], wasmBytes[7]} != wasmVersionV1 {
		return nil, fmt.Errorf("wasm binary version mismatch (got %x; only 1.0 supported)",
			wasmBytes[4:8])
	}

	cursor := 8
	for cursor < len(wasmBytes) {
		sectionID := wasmBytes[cursor]
		cursor++
		sectionSize, consumed, err := readUleb128(wasmBytes[cursor:])
		if err != nil {
			return nil, fmt.Errorf("wasm section %d size: %w", sectionID, err)
		}
		cursor += consumed
		sectionEnd := cursor + int(sectionSize)
		if sectionEnd > len(wasmBytes) {
			return nil, fmt.Errorf("wasm section %d size %d overruns binary (end=%d, len=%d)",
				sectionID, sectionSize, sectionEnd, len(wasmBytes))
		}
		if sectionID == wasmSectionImport {
			return parseImportSection(wasmBytes[cursor:sectionEnd])
		}
		// The import section is section 2; skip every other section
		// without parsing. Sections that appear before the import
		// section (type, function-signature index 0 in custom or
		// rare layouts) are also skipped — we only need imports.
		cursor = sectionEnd
		if sectionID > wasmSectionImport {
			// Imports must appear at most once and never after their
			// section ID in conforming Wasm 1.0; if we've passed
			// section 2 without finding imports, the module has none.
			//
			// Edge case: a malformed module with sections in non-
			// conforming order (e.g. imports after function-signature
			// or after exports) is treated as "no imports" by this
			// parser. The firewall accepts it; Wazero / Extism reject
			// it at their own compile step a few microseconds later.
			// The double-validation is acceptable — malformed binaries
			// fail the build either way, and the parser stays simple.
			return nil, nil
		}
	}

	return nil, nil
}

// functionImport names a single function imported under (Module,
// Name). The firewall's WASI per-import check looks at functions with
// Module == wasi_snapshot_preview1.
type functionImport struct {
	Module string
	Name   string
}

// parseImportSection walks an import section's bytes and returns the
// function imports. Imports of other kinds (memory, table, global)
// are decoded enough to advance the cursor but their descriptors are
// not returned.
func parseImportSection(body []byte) ([]functionImport, error) {
	count, consumed, err := readUleb128(body)
	if err != nil {
		return nil, fmt.Errorf("import count: %w", err)
	}
	cursor := consumed

	out := make([]functionImport, 0, count)
	for i := uint32(0); i < count; i++ {
		moduleName, n, err := readWasmName(body[cursor:])
		if err != nil {
			return nil, fmt.Errorf("import %d module name: %w", i, err)
		}
		cursor += n
		fieldName, n, err := readWasmName(body[cursor:])
		if err != nil {
			return nil, fmt.Errorf("import %d field name: %w", i, err)
		}
		cursor += n

		if cursor >= len(body) {
			return nil, fmt.Errorf("import %d descriptor truncated", i)
		}
		kind := body[cursor]
		cursor++

		switch kind {
		case wasmImportKindFunction:
			// Function imports carry a uleb128 type-index. Decode and
			// discard; the firewall only needs the (module, name)
			// pair.
			_, n, err := readUleb128(body[cursor:])
			if err != nil {
				return nil, fmt.Errorf("import %d function type index: %w", i, err)
			}
			cursor += n
			out = append(out, functionImport{Module: moduleName, Name: fieldName})
		case 0x01:
			// Table import: limits = { flags: byte, min: uleb128, [max: uleb128 if flags & 1] }
			// + element-type byte (already consumed before the limits in some layouts;
			// in Wasm 1.0 the order is element-type then limits).
			if cursor >= len(body) {
				return nil, fmt.Errorf("import %d table descriptor truncated", i)
			}
			cursor++ // element-type byte
			n, err := skipLimits(body[cursor:])
			if err != nil {
				return nil, fmt.Errorf("import %d table limits: %w", i, err)
			}
			cursor += n
		case 0x02:
			// Memory import: just the limits.
			n, err := skipLimits(body[cursor:])
			if err != nil {
				return nil, fmt.Errorf("import %d memory limits: %w", i, err)
			}
			cursor += n
		case 0x03:
			// Global import: value-type byte + mutability byte.
			if cursor+2 > len(body) {
				return nil, fmt.Errorf("import %d global descriptor truncated", i)
			}
			cursor += 2
		default:
			return nil, fmt.Errorf("import %d unknown descriptor kind %d", i, kind)
		}
	}
	return out, nil
}

// readUleb128 decodes a Little-Endian Base 128 unsigned 32-bit integer
// from the start of body. Returns the decoded value, the number of
// bytes consumed, and an error if the encoding is malformed.
//
// The Wasm spec uses uleb128 for length prefixes and index references.
// This implementation handles up to 5 continuation bytes (the max for
// a 32-bit value).
func readUleb128(body []byte) (uint32, int, error) {
	var result uint32
	var shift uint
	for i := 0; i < 5; i++ {
		if i >= len(body) {
			return 0, 0, errors.New("uleb128 truncated")
		}
		b := body[i]
		result |= uint32(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, errors.New("uleb128 exceeds 32-bit range (more than 5 bytes)")
}

// readWasmName decodes a length-prefixed UTF-8 string per the Wasm
// name-section format. Returns the string, total bytes consumed
// (length-prefix + payload), and an error if the encoding is
// malformed.
func readWasmName(body []byte) (string, int, error) {
	length, consumed, err := readUleb128(body)
	if err != nil {
		return "", 0, fmt.Errorf("name length: %w", err)
	}
	if consumed+int(length) > len(body) {
		return "", 0, fmt.Errorf("name payload of %d bytes overruns available %d",
			length, len(body)-consumed)
	}
	return string(body[consumed : consumed+int(length)]), consumed + int(length), nil
}

// skipLimits advances past a Wasm limits structure (used by memory
// and table types). Returns bytes consumed.
//
// Layout: flags byte (0 = min only, 1 = min + max) + min uleb128 +
// optional max uleb128.
func skipLimits(body []byte) (int, error) {
	if len(body) < 1 {
		return 0, errors.New("limits truncated at flags byte")
	}
	flags := body[0]
	cursor := 1
	_, n, err := readUleb128(body[cursor:])
	if err != nil {
		return 0, fmt.Errorf("limits min: %w", err)
	}
	cursor += n
	if flags&0x01 != 0 {
		_, n, err := readUleb128(body[cursor:])
		if err != nil {
			return 0, fmt.Errorf("limits max: %w", err)
		}
		cursor += n
	}
	return cursor, nil
}
