// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "errors"

// ErrWasmManifestRejected indicates that the Extism manifest produced
// from a PluginSpec, or the plugin Wasm binary itself, declares a
// capability the AOT-purity firewall refuses. Capabilities the
// firewall rejects today:
//
//   - non-empty AllowedHosts (outbound network access)
//   - non-empty AllowedPaths (filesystem access)
//   - declared host functions outside the curated allowlist
//   - wasi_snapshot_preview1 imports outside the curated import
//     allowlist (e.g. path_open, sock_*, fd_readdir — these grant
//     filesystem or network capabilities)
//
// WASI stdio passthrough and wall-time access remain denied by
// construction: the Loader enables WASI but hands the Extism runtime a
// deterministic, capability-denied ModuleConfig (frozen observable
// wall time, real monotonic time, zero entropy, discarded stdio, no
// filesystem / args / environment). The benign runtime-init WASI
// imports the firewall allows (clocks, entropy, stdio, environment and
// argument discovery, scheduler yield, proc_exit, and fd metadata) are
// served by the Extism-bundled wazero WASI runtime under that
// ModuleConfig; see wasi_imports.go for the allowlist and loader.go
// for the deterministic configuration.
//
// Errors wrapping this sentinel name the violated capability and the
// plugin identifier so callers see exactly which surface caused the
// rejection.
var ErrWasmManifestRejected = errors.New("wasm: plugin manifest rejected by AOT-purity firewall")

// ErrWasmGuestPanic indicates that guest plugin code panicked during a
// Plugin.Call invocation. The Loader's Call method wraps the Extism
// call boundary with `defer recover()`; on panic, the recovered value
// is wrapped in an error that satisfies errors.Is against this
// sentinel and carries the plugin identifier plus the called function
// name in its message.
//
// Note: Extism translates many Wasm-side traps (for example the
// unreachable instruction, divide-by-zero, out-of-bounds memory
// access) into error return values rather than Go panics. This
// sentinel is reserved for panics that escape Extism's own error
// envelope — the safety net behind the SDK's normal error path.
var ErrWasmGuestPanic = errors.New("wasm: guest plugin panicked during call")

// ErrWasmPluginExited indicates that a guest plugin invoked WASI
// proc_exit with a non-zero code during a Plugin.Call. The Extism
// runtime surfaces the exit as a non-zero exit code on the call's
// return path; Plugin.Call converts that into an error wrapping this
// sentinel. The plugin's execution is stopped; the host process is
// unaffected. Plugin authors who reach proc_exit are doing something
// the Aggrega plugin model does not support — plugins return their
// output via the Extism call/return ABI, not via process exit.
//
// The wrapping error carries the exit code the plugin passed to
// proc_exit so callers see the failure code in the message.
var ErrWasmPluginExited = errors.New("wasm: plugin invoked proc_exit")
