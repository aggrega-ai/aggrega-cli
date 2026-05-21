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
//   - wasi_snapshot_preview1 imports outside the stubbed-subset
//     allowlist (e.g. path_open, sock_*, fd_readdir, poll_oneoff —
//     these grant filesystem, network, or non-deterministic-time
//     capabilities even when WASI is not enabled at the runtime level)
//
// WASI stdio passthrough and wall-time access remain denied by
// construction: the Loader never sets PluginConfig.EnableWasi to true
// and never sets PluginConfig.ModuleConfig, so the Extism runtime
// cannot enable them. The runtime-init WASI imports the firewall
// stubs (fd_write, clock_time_get, random_get, environ_*, args_*,
// proc_exit, fd_*) are satisfied by deterministic host-side stubs;
// see wasistubs.go for the allowlist and per-stub semantics.
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

// ErrWasmPluginExited indicates that a guest plugin invoked the
// stubbed proc_exit host function during a Plugin.Call. The plugin's
// execution is stopped; the host process is unaffected. Plugin authors
// who reach proc_exit during a non-error code path are doing something
// the Aggrega plugin model does not support — plugins return their
// output via the Extism call/return ABI, not via process exit.
//
// The wrapping error carries the exit code the plugin passed to
// proc_exit so callers can distinguish "exit 0" (orderly but
// unexpected termination) from "exit N" (deliberate failure
// signalling) if useful.
var ErrWasmPluginExited = errors.New("wasm: plugin invoked proc_exit")
