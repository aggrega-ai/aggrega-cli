// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "errors"

// ErrWasmManifestRejected indicates that the Extism manifest produced
// from a PluginSpec declares a capability the AOT-purity firewall
// refuses. Capabilities the firewall rejects today:
//
//   - non-empty AllowedHosts (outbound network access)
//   - non-empty AllowedPaths (filesystem access)
//   - declared host functions outside the curated allowlist
//
// Two additional capability surfaces (WASI stdio passthrough, wall-time
// access) are denied by construction: the Loader never sets
// PluginConfig.EnableWasi to true and never sets PluginConfig.ModuleConfig,
// so the Extism runtime cannot enable them. The firewall therefore has
// no manifest field to check for those two — they cannot be requested
// through the PluginSpec API.
//
// Errors wrapping this sentinel name the violated capability and the
// plugin identifier so callers see exactly which spec field caused the
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
