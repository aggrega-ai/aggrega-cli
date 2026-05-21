// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"fmt"

	extism "github.com/extism/go-sdk"
)

// wasiSnapshotPreview1Namespace is the Wasm import namespace WASI
// preview-1 imports declare. Plugins built for the wasm32-wasip1
// Rust target, TinyGo's -target=wasi, the Extism JS PDK (via
// extism-js), and AssemblyScript's WASI bindings all import under
// this exact namespace string.
const wasiSnapshotPreview1Namespace = "wasi_snapshot_preview1"

// wasiStubAllowlist names the WASI preview-1 imports the host
// satisfies with deterministic stubs. Plugins importing only these
// names load successfully through the AOT-purity firewall; the host
// provides no-op or fixed-constant implementations.
//
// The allowlist is intentionally bounded to runtime-init imports —
// imports a plugin's language runtime touches unconditionally during
// startup before any plugin logic runs. Imports outside this set
// (filesystem traversal, network sockets, polling) grant runtime
// capabilities and are categorically rejected by the firewall.
var wasiStubAllowlist = map[string]bool{
	"fd_write":          true,
	"fd_read":           true,
	"fd_close":          true,
	"fd_seek":           true,
	"fd_tell":           true,
	"fd_fdstat_get":     true,
	"fd_prestat_get":    true,
	"fd_prestat_dir_name": true,
	"clock_time_get":    true,
	"clock_res_get":     true,
	"random_get":        true,
	"environ_get":       true,
	"environ_sizes_get": true,
	"args_get":          true,
	"args_sizes_get":    true,
	"proc_exit":         true,
}

// wasiCapabilityDenylist names WASI preview-1 imports the firewall
// rejects with an explanatory diagnostic. The map's value is the
// human-readable capability the import would grant; the firewall
// embeds it in the rejection error so callers see exactly which
// invariant the rejection protects.
//
// Imports not on either the allowlist or the denylist default-deny;
// the firewall produces a generic "not on the stubbed-subset
// allowlist" diagnostic for those. The denylist exists to give
// frequently-encountered rejections a clear capability label.
var wasiCapabilityDenylist = map[string]string{
	"path_open":              "filesystem capability (violates No Ghost Infrastructure)",
	"path_create_directory":  "filesystem capability (violates No Ghost Infrastructure)",
	"path_filestat_get":      "filesystem capability (violates No Ghost Infrastructure)",
	"path_filestat_set_size": "filesystem capability (violates No Ghost Infrastructure)",
	"path_filestat_set_times": "filesystem capability (violates No Ghost Infrastructure)",
	"path_link":              "filesystem capability (violates No Ghost Infrastructure)",
	"path_readlink":          "filesystem capability (violates No Ghost Infrastructure)",
	"path_remove_directory":  "filesystem capability (violates No Ghost Infrastructure)",
	"path_rename":            "filesystem capability (violates No Ghost Infrastructure)",
	"path_symlink":           "filesystem capability (violates No Ghost Infrastructure)",
	"path_unlink_file":       "filesystem capability (violates No Ghost Infrastructure)",
	"fd_readdir":             "filesystem-traversal capability (violates No Ghost Infrastructure)",
	"sock_accept":            "network capability (violates AOT Purity)",
	"sock_recv":              "network capability (violates AOT Purity)",
	"sock_send":              "network capability (violates AOT Purity)",
	"sock_shutdown":          "network capability (violates AOT Purity)",
	"poll_oneoff":            "variable-time blocking (potentially violates Deterministic Output)",
}

// wasiErrnoSuccess is the WASI preview-1 errno value for a successful
// host-function call. Every stub returns this on the normal path.
const wasiErrnoSuccess uint32 = 0

// validateWasiImports walks the plugin Wasm binary's import section
// and applies the WASI per-import firewall pass. Each function import
// under wasi_snapshot_preview1 must be in wasiStubAllowlist; any
// import in wasiCapabilityDenylist is rejected with a capability-
// label diagnostic; any other wasi_snapshot_preview1 import is
// rejected as "not on the stubbed-subset allowlist".
//
// Returns nil on success; returns an error wrapping
// ErrWasmManifestRejected on the first rejection encountered. The
// function name embedded in the error message identifies the
// violating import.
//
// Imports under namespaces other than wasi_snapshot_preview1
// (extism:host/env, extism:host/user, etc.) are not the concern of
// this pass; the existing host-function allowlist in
// manifest_translate.go handles those.
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
		if wasiStubAllowlist[imp.Name] {
			continue
		}
		if capability, denied := wasiCapabilityDenylist[imp.Name]; denied {
			return fmt.Errorf("plugin %q imports wasi_snapshot_preview1.%s: %s: %w",
				pluginName, imp.Name, capability, ErrWasmManifestRejected)
		}
		return fmt.Errorf("plugin %q imports wasi_snapshot_preview1.%s: not on the stubbed-subset allowlist: %w",
			pluginName, imp.Name, ErrWasmManifestRejected)
	}
	return nil
}

// buildWasiStubHostFunctions returns the WASI stub HostFunction values
// the loader passes to extism.NewPlugin. Extism groups HostFunction
// values by Namespace before registering them on its internal Wazero
// runtime; setting Namespace = "wasi_snapshot_preview1" causes Extism
// to register the stubs as a wasi_snapshot_preview1 host module so
// plugin imports under that namespace resolve to the stubs.
//
// The PluginConfig that consumes these functions MUST set
// EnableWasi = false. EnableWasi = true would have Extism instantiate
// its bundled wasi_snapshot_preview1 module first, which would
// conflict with the stubs and grant the plugin the real WASI
// capabilities (host clock, host entropy, host filesystem) the
// firewall denies.
func buildWasiStubHostFunctions() []extism.HostFunction {
	stubs := []extism.HostFunction{
		buildWasiStubFdWrite(),
		buildWasiStubFdRead(),
		buildWasiStubFdClose(),
		buildWasiStubFdSeek(),
		buildWasiStubFdTell(),
		buildWasiStubFdFdstatGet(),
		buildWasiStubFdPrestatGet(),
		buildWasiStubFdPrestatDirName(),
		buildWasiStubClockTimeGet(),
		buildWasiStubClockResGet(),
		buildWasiStubRandomGet(),
		buildWasiStubEnvironGet(),
		buildWasiStubEnvironSizesGet(),
		buildWasiStubArgsGet(),
		buildWasiStubArgsSizesGet(),
		buildWasiStubProcExit(),
	}
	for i := range stubs {
		stubs[i].SetNamespace(wasiSnapshotPreview1Namespace)
	}
	return stubs
}

// wasiProcExitTrap is the typed value the stubbed proc_exit panics
// with. The Loader's call panic-recovery wrap detects this type and
// translates the recovered value into an error wrapping
// ErrWasmPluginExited carrying the exit code.
type wasiProcExitTrap struct {
	code uint32
}

// i32 returns the low 32 bits of a uint64 stack slot, interpreting
// the slot as a WASI i32 parameter (pointers and integers in Wasm32).
func i32(slot uint64) uint32 {
	return uint32(slot)
}

// writeU32LE writes a little-endian uint32 to plugin memory at the
// given offset. Returns false when the offset is out of range — the
// caller surfaces this to the guest as errno BADF.
func writeU32LE(plugin *extism.CurrentPlugin, offset uint32, value uint32) bool {
	return plugin.Memory().WriteUint32Le(offset, value)
}

// writeU64LE writes a little-endian uint64 to plugin memory.
func writeU64LE(plugin *extism.CurrentPlugin, offset uint32, value uint64) bool {
	return plugin.Memory().WriteUint64Le(offset, value)
}

// readU32LE reads a little-endian uint32 from plugin memory.
func readU32LE(plugin *extism.CurrentPlugin, offset uint32) (uint32, bool) {
	return plugin.Memory().ReadUint32Le(offset)
}

// zeroFillPluginMemory writes `length` zero bytes starting at the
// given plugin-memory offset. Used by stubs that conceptually fill a
// buffer (random_get) but must not leak host state.
func zeroFillPluginMemory(plugin *extism.CurrentPlugin, offset, length uint32) bool {
	zeros := make([]byte, length)
	return plugin.Memory().Write(offset, zeros)
}

// buildWasiStubFdWrite stubs WASI fd_write(fd: i32, iovs: i32,
// iovs_len: i32, written_ptr: i32) -> errno: i32. Per the stubbed-
// subset contract the bytes are discarded — the plugin sees a
// successful write whose returned byte count matches the requested
// length, but no bytes leave the plugin. The stub reads the iovec
// table from plugin memory only to compute the requested length;
// the iovec contents are never read.
func buildWasiStubFdWrite() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_write",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			iovs := i32(stack[1])
			iovsLen := i32(stack[2])
			writtenPtr := i32(stack[3])

			var totalLength uint32
			for i := uint32(0); i < iovsLen; i++ {
				// Each iovec is { buf_ptr: u32, buf_len: u32 } — 8
				// bytes per entry. Only buf_len matters; buf_ptr is
				// ignored because the bytes are discarded.
				bufLen, ok := readU32LE(plugin, iovs+i*8+4)
				if !ok {
					stack[0] = uint64(wasiErrnoBadf)
					return
				}
				totalLength += bufLen
			}

			if !writeU32LE(plugin, writtenPtr, totalLength) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{
			extism.ValueTypeI32, // fd
			extism.ValueTypeI32, // iovs
			extism.ValueTypeI32, // iovs_len
			extism.ValueTypeI32, // written_ptr
		},
		[]extism.ValueType{extism.ValueTypeI32}, // errno
	)
}

// wasiErrnoBadf is the WASI errno for "bad file descriptor"; the
// stubs return it on memory-read failures (a structural error the
// plugin should treat as transient).
const wasiErrnoBadf uint32 = 8

// buildWasiStubFdRead stubs WASI fd_read(fd, iovs, iovs_len, nread_ptr) -> errno.
// Always writes 0 bytes-read to nread_ptr (EOF semantics) and returns success.
func buildWasiStubFdRead() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_read",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			nreadPtr := i32(stack[3])
			if !writeU32LE(plugin, nreadPtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{
			extism.ValueTypeI32, extism.ValueTypeI32, extism.ValueTypeI32, extism.ValueTypeI32,
		},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubFdClose stubs fd_close(fd) -> errno. Always returns success.
func buildWasiStubFdClose() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_close",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubFdSeek stubs fd_seek(fd, offset: i64, whence, newoffset_ptr) -> errno.
// Writes 0 to newoffset_ptr; returns success.
func buildWasiStubFdSeek() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_seek",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			newoffsetPtr := i32(stack[3])
			if !writeU64LE(plugin, newoffsetPtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{
			extism.ValueTypeI32, // fd
			extism.ValueTypeI64, // offset
			extism.ValueTypeI32, // whence
			extism.ValueTypeI32, // newoffset_ptr
		},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubFdTell stubs fd_tell(fd, offset_ptr) -> errno. Writes
// 0 to offset_ptr; returns success.
func buildWasiStubFdTell() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_tell",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			offsetPtr := i32(stack[1])
			if !writeU64LE(plugin, offsetPtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubFdFdstatGet stubs fd_fdstat_get(fd, stat_ptr) -> errno.
// Writes 24 zero bytes (the WASI fdstat structure size) at stat_ptr;
// returns success.
func buildWasiStubFdFdstatGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_fdstat_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			statPtr := i32(stack[1])
			if !zeroFillPluginMemory(plugin, statPtr, 24) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// wasiErrnoBadFileDescriptor is what fd_prestat_get returns to
// signal "no more preopened directories". Plugins iterate
// fd_prestat_get from fd=3 upward until they get badf, which is the
// idiomatic way to discover preopens.
const wasiErrnoBadFileDescriptor = wasiErrnoBadf

// buildWasiStubFdPrestatGet stubs fd_prestat_get(fd, prestat_ptr) -> errno.
// Always returns badf so plugins see "no preopened directories" — the
// stubbed-subset never grants filesystem preopens.
func buildWasiStubFdPrestatGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_prestat_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			stack[0] = uint64(wasiErrnoBadFileDescriptor)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubFdPrestatDirName stubs
// fd_prestat_dir_name(fd, path_ptr, path_len) -> errno. Always returns
// badf for the same reason as fd_prestat_get.
func buildWasiStubFdPrestatDirName() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"fd_prestat_dir_name",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			stack[0] = uint64(wasiErrnoBadFileDescriptor)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubClockTimeGet stubs
// clock_time_get(clock_id, precision: i64, time_ptr) -> errno.
// Writes uint64(0) to time_ptr — a fixed constant, deterministic
// across builds. Plugins observing the timestamp see the same value
// regardless of host wall-clock state.
func buildWasiStubClockTimeGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"clock_time_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			timePtr := i32(stack[2])
			if !writeU64LE(plugin, timePtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{
			extism.ValueTypeI32, // clock_id
			extism.ValueTypeI64, // precision
			extism.ValueTypeI32, // time_ptr
		},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubClockResGet stubs clock_res_get(clock_id, res_ptr) -> errno.
// Writes uint64(1) (one-nanosecond resolution) to res_ptr.
func buildWasiStubClockResGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"clock_res_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			resPtr := i32(stack[1])
			if !writeU64LE(plugin, resPtr, 1) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubRandomGet stubs random_get(buf, buf_len) -> errno.
// Zero-fills the buffer of length buf_len at buf; returns success.
// Deterministic by design — the plugin never sees real entropy.
func buildWasiStubRandomGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"random_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			buf := i32(stack[0])
			bufLen := i32(stack[1])
			if !zeroFillPluginMemory(plugin, buf, bufLen) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubEnvironGet stubs environ_get(env_ptr, env_buf) -> errno.
// No-op — environ_sizes_get returns zero counts, so plugins never call
// environ_get expecting actual data. Still implemented to satisfy the
// import slot.
func buildWasiStubEnvironGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"environ_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubEnvironSizesGet stubs
// environ_sizes_get(count_ptr, size_ptr) -> errno. Writes 0 to both
// pointers — the plugin sees an empty environment.
func buildWasiStubEnvironSizesGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"environ_sizes_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			countPtr := i32(stack[0])
			sizePtr := i32(stack[1])
			if !writeU32LE(plugin, countPtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			if !writeU32LE(plugin, sizePtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubArgsGet stubs args_get(argv_ptr, argv_buf) -> errno.
// No-op for the same reason as environ_get.
func buildWasiStubArgsGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"args_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubArgsSizesGet stubs
// args_sizes_get(count_ptr, size_ptr) -> errno. Writes 0 to both — the
// plugin sees an empty argument list.
func buildWasiStubArgsSizesGet() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"args_sizes_get",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			countPtr := i32(stack[0])
			sizePtr := i32(stack[1])
			if !writeU32LE(plugin, countPtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			if !writeU32LE(plugin, sizePtr, 0) {
				stack[0] = uint64(wasiErrnoBadf)
				return
			}
			stack[0] = uint64(wasiErrnoSuccess)
		},
		[]extism.ValueType{extism.ValueTypeI32, extism.ValueTypeI32},
		[]extism.ValueType{extism.ValueTypeI32},
	)
}

// buildWasiStubProcExit stubs proc_exit(rval) -> ().
//
// The stub panics with a typed wasiProcExitTrap value carrying the
// guest-supplied exit code. The Loader's Plugin.Call panic-recovery
// wrap (see loader.go) detects this type and translates the
// recovered value into an error wrapping ErrWasmPluginExited. The
// host process is NOT exited — the trap stops only the guest's
// execution.
func buildWasiStubProcExit() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"proc_exit",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			panic(wasiProcExitTrap{code: i32(stack[0])})
		},
		[]extism.ValueType{extism.ValueTypeI32},
		nil, // proc_exit has no return — it diverges
	)
}
