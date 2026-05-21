// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"sort"

	extism "github.com/extism/go-sdk"
)

// hostFunctionAllowlist is the curated set of host function names the
// AOT-purity firewall permits a plugin to import. Every plugin's
// PluginSpec.DeclaredHostFunctions MUST be a subset of this set; a
// declared function outside the set triggers ErrWasmManifestRejected.
//
// The baseline set is the two diagnostic emitters every Aggrega plugin
// is expected to need: a logging surface and a structured-warning
// emitter. Per-socket extension hooks (additional host functions for
// specific plugin sockets like a mutator socket needing a context
// accessor) ship as separate registrations when those sockets land;
// the framework here is the registration plumbing only.
var hostFunctionAllowlist = map[string]extism.HostFunction{
	"aggrega_log":          buildHostFunctionAggregaLog(),
	"aggrega_emit_warning": buildHostFunctionAggregaEmitWarning(),
}

// allowlistedHostFunctionNames returns the curated names as a slice,
// useful for inclusion-checks against PluginSpec.DeclaredHostFunctions.
// The slice is sorted alphabetically so error messages embedding it
// (for example the firewall's "outside the allowlist X" diagnostic)
// produce identical text across runs of the same input — Go map
// iteration order is non-deterministic and would otherwise reorder
// the names per build.
func allowlistedHostFunctionNames() []string {
	names := make([]string, 0, len(hostFunctionAllowlist))
	for name := range hostFunctionAllowlist {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// selectAllowlistedHostFunctions returns the HostFunction values
// corresponding to the names a PluginSpec declares. Caller is
// responsible for verifying every name is in the allowlist first; this
// helper assumes that check has already passed.
func selectAllowlistedHostFunctions(declared []string) []extism.HostFunction {
	out := make([]extism.HostFunction, 0, len(declared))
	for _, name := range declared {
		if fn, ok := hostFunctionAllowlist[name]; ok {
			out = append(out, fn)
		}
	}
	return out
}

// buildHostFunctionAggregaLog returns the host-side binding for
// aggrega_log(level i64, message_ptr i64). The Wasm guest passes a
// log-level integer and a pointer to a UTF-8 string in plugin memory;
// the host reads the string from memory and forwards to the standard
// logger.
//
// Today the body is a stub. The forwarding wiring to Aggrega's logging
// surface lands when the first consuming plugin (the publishing-linter
// that is planned) integrates and the diagnostic flow needs to be
// observable end-to-end. Until then, calls are accepted but produce no
// output.
func buildHostFunctionAggregaLog() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"aggrega_log",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			// TODO: read the message from plugin memory at stack[1],
			// forward to Aggrega's standard logger keyed by stack[0]
			// (log level). Wiring lands with the first consumer.
			_ = plugin
			_ = ctx
			_ = stack
		},
		[]extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64},
		[]extism.ValueType{},
	)
}

// buildHostFunctionAggregaEmitWarning returns the host-side binding
// for aggrega_emit_warning(code_ptr i64, message_ptr i64, path_ptr i64).
// The guest emits a structured warning the host surfaces via the
// build-time validator's warning channel.
//
// Body is a stub for the same reason as aggrega_log; the forwarding
// wiring lands when a consuming plugin integrates.
func buildHostFunctionAggregaEmitWarning() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"aggrega_emit_warning",
		func(ctx context.Context, plugin *extism.CurrentPlugin, stack []uint64) {
			// TODO: read code, message, path strings from plugin
			// memory at stack[0], stack[1], stack[2]; forward to
			// Aggrega's warning emitter.
			_ = plugin
			_ = ctx
			_ = stack
		},
		[]extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64, extism.ValueTypeI64},
		[]extism.ValueType{},
	)
}
