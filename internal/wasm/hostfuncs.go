// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"fmt"
	"log"
	"sort"

	extism "github.com/extism/go-sdk"
)

// hostFunctionAllowlist is the curated set of host function names the
// AOT-purity firewall permits a plugin to import. The set derived from a
// plugin's .wasm imports MUST be a subset of this set; a derived function
// outside the set triggers ErrWasmManifestRejected.
//
// The map's role is closed-set membership for the allowlist check;
// it does not own HostFunction values. Per-plugin closures are built
// by the (*Plugin).hostFunctionsForDeclared method so the closure
// bodies can capture the receiver and accumulate guest-emitted
// warnings into Plugin.warnings.
//
// The baseline set is the two diagnostic emitters every Aggrega
// plugin is expected to need: a logging surface and a structured-
// warning emitter. Per-socket extension hooks for additional host
// functions land as the corresponding plugin sockets ship.
var hostFunctionAllowlist = map[string]bool{
	"aggrega_log":          true,
	"aggrega_emit_warning": true,
}

// allowlistedHostFunctionNames returns the curated names as a slice,
// useful for inclusion-checks against the set derived from a .wasm.
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

// hostFunctionsForDeclared returns the per-Plugin HostFunction
// closures the caller's derived host-function slice asks for. Each
// returned closure captures *Plugin so the host-side body can
// accumulate warnings into Plugin.warnings or format log lines
// keyed by the plugin's identifier.
//
// The caller is responsible for validating that every name in
// `declared` is in the allowlist first; this method assumes that
// check has already passed (validateHostFunctionsSubset enforces the
// subset check before Load's flow reaches here).
func (p *Plugin) hostFunctionsForDeclared(declared []string) []extism.HostFunction {
	out := make([]extism.HostFunction, 0, len(declared))
	for _, name := range declared {
		switch name {
		case "aggrega_log":
			out = append(out, p.hostFunctionAggregaLog())
		case "aggrega_emit_warning":
			out = append(out, p.hostFunctionAggregaEmitWarning())
		default:
			// Invariant violation: validateHostFunctionsSubset
			// admitted a name the builder switch doesn't handle.
			// This is a maintenance safety net for the case where a
			// future change adds a name to hostFunctionAllowlist
			// without adding the matching switch case here — the
			// guest would otherwise see a silent no-op (an import
			// the loader passed but never bound). Panic loudly so
			// the gap surfaces at the first call site rather than
			// at debug time inside a plugin invocation.
			panic("wasm: hostFunctionsForDeclared called with name not in allowlist-builder set: " + name)
		}
	}
	return out
}

// pluginMemoryReader is the minimal interface the host function
// bodies use to read string arguments from plugin memory. The Extism
// SDK's *CurrentPlugin satisfies this interface structurally; tests
// substitute a fake reader to exercise the body logic without
// fabricating a *CurrentPlugin (the SDK exposes no public
// constructor or test double for that type).
type pluginMemoryReader interface {
	ReadString(offset uint64) (string, error)
}

// hostFunctionAggregaLog returns the host-side closure binding
// aggrega_log(level i64, message_ptr i64). The guest passes a
// log-level integer and a pointer to a UTF-8 string in plugin
// memory; the host reads the string and emits one log line on the
// stdlib log destination keyed by the plugin's name and the level
// label.
//
// Body extracted into handleAggregaLog so the test suite can
// exercise the read-and-format path against a fake
// pluginMemoryReader without fabricating an *extism.CurrentPlugin.
func (p *Plugin) hostFunctionAggregaLog() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"aggrega_log",
		func(ctx context.Context, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaLog(cp, stack)
		},
		[]extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64},
		[]extism.ValueType{},
	)
}

// handleAggregaLog is the testable inner logic of the aggrega_log
// host function body. Reads message from plugin memory at stack[1]
// via the reader interface; on read failure logs a host-side
// diagnostic and returns without panicking. On success logs one
// formatted line:
//
//	plugin "<name>" [<level-label>] <message>
//
// The host-side diagnostic on read failure surfaces the plugin name
// and the underlying error so a bad guest pointer is observable in
// host logs without crashing the call.
func (p *Plugin) handleAggregaLog(reader pluginMemoryReader, stack []uint64) {
	level := stack[0]
	message, err := reader.ReadString(stack[1])
	if err != nil {
		log.Printf("plugin %q: aggrega_log: memory read failed: %v", p.name, err)
		return
	}
	log.Printf("plugin %q [%s] %s", p.name, levelLabel(level), message)
}

// hostFunctionAggregaEmitWarning returns the host-side closure
// binding aggrega_emit_warning(code_ptr i64, message_ptr i64,
// path_ptr i64). The guest emits a structured warning the host
// accumulates into Plugin.warnings; the caller surfaces accumulated
// warnings after Plugin.Call returns via Plugin.DrainWarnings.
//
// Body extracted into handleAggregaEmitWarning so the test suite
// can exercise the read-format-append path against a fake reader.
func (p *Plugin) hostFunctionAggregaEmitWarning() extism.HostFunction {
	return extism.NewHostFunctionWithStack(
		"aggrega_emit_warning",
		func(ctx context.Context, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaEmitWarning(cp, stack)
		},
		[]extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64, extism.ValueTypeI64},
		[]extism.ValueType{},
	)
}

// handleAggregaEmitWarning is the testable inner logic of the
// aggrega_emit_warning host function body. Reads code, message, and
// path strings from plugin memory at stack[0], stack[1], stack[2]
// respectively via the reader interface. On any read failure: logs a
// host-side diagnostic naming the parameter slot and returns without
// appending — the partially-read warning is discarded to avoid
// recording structured data with missing fields.
//
// On all three reads succeeding: formats the warning string and
// appends to Plugin.warnings under the warnings mutex via
// appendWarning. The format:
//
//	plugin <name>: <message> [code=<code>, path=<path>]
//
// keeps the per-warning line self-describing so the caller's
// surface (typically stderr from the build command) is human-
// readable without further structuring.
func (p *Plugin) handleAggregaEmitWarning(reader pluginMemoryReader, stack []uint64) {
	code, err := reader.ReadString(stack[0])
	if err != nil {
		log.Printf("plugin %q: aggrega_emit_warning: memory read failed at code: %v", p.name, err)
		return
	}
	message, err := reader.ReadString(stack[1])
	if err != nil {
		log.Printf("plugin %q: aggrega_emit_warning: memory read failed at message: %v", p.name, err)
		return
	}
	path, err := reader.ReadString(stack[2])
	if err != nil {
		log.Printf("plugin %q: aggrega_emit_warning: memory read failed at path: %v", p.name, err)
		return
	}
	formatted := fmt.Sprintf("plugin %s: %s [code=%s, path=%s]", p.name, message, code, path)
	p.appendWarning(formatted)
}

// levelLabel maps a wire-side log level integer to a descriptive
// label. Convention matches the syslog-style ascending-severity
// ordinal: 0=trace, 1=debug, 2=info, 3=warn, 4=error. Unknown
// numeric levels render as "level=N" so the log line still carries
// the wire-side value.
func levelLabel(level uint64) string {
	switch level {
	case 0:
		return "trace"
	case 1:
		return "debug"
	case 2:
		return "info"
	case 3:
		return "warn"
	case 4:
		return "error"
	default:
		return fmt.Sprintf("level=%d", level)
	}
}
