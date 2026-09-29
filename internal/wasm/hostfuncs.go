// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	extism "github.com/extism/go-sdk"
)

// hostFunctionAllowlist is the curated set of host function names the
// AOT-purity load gate permits a plugin to import. The set derived from a
// plugin's .wasm imports MUST be a subset of this set; a derived function
// outside the set triggers ErrWasmManifestRejected.
//
// The map's role is closed-set membership for the allowlist check;
// it does not own HostFunction values. The bound host functions are
// built by hostFunctionsForDeclared, and each body is handed the
// calling Plugin at call time so it can accumulate guest-emitted
// warnings into that instance's Plugin.warnings.
//
// The baseline set is the two diagnostic emitters every Aggrega
// plugin is expected to need — a logging surface and a structured-
// warning emitter — plus the external-input fetch channel. The fetch
// channel is host-mediated, not guest network access: the guest names
// a URL and the host serves bytes through the content-addressed
// external-input store's pin discipline, so the AOT-purity load gate
// (no AllowedHosts, no sockets) is unchanged.
var hostFunctionAllowlist = map[string]bool{
	"aggrega_log":                  true,
	"aggrega_emit_warning":         true,
	"aggrega_fetch_external_input": true,
	"aggrega_pack_source":          true,
	"aggrega_read_source_file":     true,
	"aggrega_parse_source":         true,
}

// allowlistedHostFunctionNames returns the curated names as a slice,
// useful for inclusion-checks against the set derived from a .wasm.
// The slice is sorted alphabetically so error messages embedding it
// (for example the load gate's "outside the allowlist X" diagnostic)
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

// cliHostFunctionAllowlist is the additional set of host function names
// granted only under the runtime CLI instantiation profile (Loader.LoadCLI),
// never under the compile-time build profile (Loader.Load).
//
// These are the streaming output emitters a CLI plugin uses to write bytes to
// the terminal's stdout and stderr as it runs. They are held in a SEPARATE map
// from hostFunctionAllowlist — not merged into it — precisely so the
// compile-time subset gate (validateHostFunctionsSubset, which consults only
// hostFunctionAllowlist) rejects any build-profile guest that imports them. A
// build runs ahead of time with no terminal to stream to and must stay
// deterministic and capability-denied, so streaming is denied there by
// construction; the runtime CLI profile runs at the terminal, outside any
// build, so it may grant these.
var cliHostFunctionAllowlist = map[string]bool{
	"aggrega_cli_write_stdout": true,
	"aggrega_cli_write_stderr": true,
}

// cliAllowlistedHostFunctionNames returns the union of the shared allowlist and
// the CLI-only allowlist as a sorted slice, for the runtime CLI subset gate's
// rejection message. The union is what a CLI-profile guest is validated
// against: a CLI guest may import the shared diagnostic emitters and the
// streaming emitters, but nothing else. Sorted for identical message text
// across runs.
func cliAllowlistedHostFunctionNames() []string {
	names := make([]string, 0, len(hostFunctionAllowlist)+len(cliHostFunctionAllowlist))
	for name := range hostFunctionAllowlist {
		names = append(names, name)
	}
	for name := range cliHostFunctionAllowlist {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// hostFunctionSignature is the Wasm import signature — the param and result
// value types — a bound host function must present so a guest importing it
// instantiates. It is the single source of each host function's shape: the real
// per-function constructors and the inert describe-only stubs both read it, so
// the two builders can never drift from each other or from the shape a guest
// declares in its .wasm import section.
type hostFunctionSignature struct {
	params  []extism.ValueType
	results []extism.ValueType
}

// hostFunctionSignatures is the single per-name registry of every curated host
// function's Wasm import signature. Both the real per-function constructors and
// the describe-only inert stubs read a function's param/result shape from here,
// so a signature has exactly one home and no builder re-spells the value-type
// lists. Every name in the union allowlist (the shared host functions plus the
// two CLI streaming emitters) has an entry, so any plugin type instantiates
// under the describe-only profile.
var hostFunctionSignatures = map[string]hostFunctionSignature{
	"aggrega_log":                  {params: []extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64}},
	"aggrega_emit_warning":         {params: []extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64, extism.ValueTypeI64}},
	"aggrega_fetch_external_input": {params: []extism.ValueType{extism.ValueTypeI64}, results: []extism.ValueType{extism.ValueTypeI64}},
	"aggrega_pack_source":          {params: []extism.ValueType{extism.ValueTypeI64}, results: []extism.ValueType{extism.ValueTypeI64}},
	"aggrega_read_source_file":     {params: []extism.ValueType{extism.ValueTypeI64}, results: []extism.ValueType{extism.ValueTypeI64}},
	"aggrega_parse_source":         {params: []extism.ValueType{extism.ValueTypeI64}, results: []extism.ValueType{extism.ValueTypeI64}},
	"aggrega_cli_write_stdout":     {params: []extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64}},
	"aggrega_cli_write_stderr":     {params: []extism.ValueType{extism.ValueTypeI64, extism.ValueTypeI64}},
}

// hostFunctionSignatureFor returns the registered Wasm import signature for the
// named host function and whether one is registered. It is the single read path
// over hostFunctionSignatures; every builder consults it rather than re-spelling
// a function's param/result shape.
func hostFunctionSignatureFor(name string) (hostFunctionSignature, bool) {
	sig, ok := hostFunctionSignatures[name]
	return sig, ok
}

// newBoundHostFunction constructs a host function named name bound to body,
// reading its Wasm import signature from the single per-name signature registry
// so the bound shape matches what a guest imports without the constructor
// re-spelling the value-type lists. It panics if name has no registered
// signature — a maintenance guard catching a host function added to the
// allowlist without a matching signature entry.
//
// Every real host function is bound through here, which is what makes this the
// one place the time a body spends running host code is recorded. The per-call
// ceiling bounds guest execution, and the recorded intervals are what keep host
// work off that ceiling — see hostTimeAccount. Binding a real body
// anywhere else would leave its cost charged to whichever guest happened to
// call it.
//
// A bound host function is part of a guest's compiled form, and one compiled
// form serves every instance a process makes of that guest, so the function
// captures no instance. It reads the instance whose guest code is calling from
// the call's context, where Plugin.callRaw — the only site that runs guest code —
// places it, and hands that instance to body. Reached with no instance on the
// context it panics: that would be guest code running outside a Plugin call,
// which the Extism SDK refuses in the same way, and a host function silently
// serving the wrong instance is the failure the panic exists to rule out.
func newBoundHostFunction(name string, body boundHostBody) extism.HostFunction {
	sig, ok := hostFunctionSignatureFor(name)
	if !ok {
		panic("wasm: no registered signature for host function " + name)
	}
	accounted := func(ctx context.Context, cp *extism.CurrentPlugin, stack []uint64) {
		p := callingPluginFromContext(ctx)
		if p == nil {
			panic("wasm: host function " + name + " was called outside a plugin call")
		}
		p.hostTime.enter()
		defer p.hostTime.leave()
		body(ctx, p, cp, stack)
	}
	return extism.NewHostFunctionWithStack(name, accounted, sig.params, sig.results)
}

// boundHostBody is the body of a real host function: it is handed the calling
// instance alongside the runtime's own arguments.
type boundHostBody func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64)

// callingPluginContextKey keys, on a call's context, the Plugin whose guest code
// is running.
type callingPluginContextKey struct{}

// contextWithCallingPlugin returns a context carrying p as the instance whose
// guest code the call runs.
func contextWithCallingPlugin(ctx context.Context, p *Plugin) context.Context {
	return context.WithValue(ctx, callingPluginContextKey{}, p)
}

// callingPluginFromContext returns the instance contextWithCallingPlugin placed
// on ctx, or nil when there is none.
func callingPluginFromContext(ctx context.Context) *Plugin {
	p, _ := ctx.Value(callingPluginContextKey{}).(*Plugin)
	return p
}

// describeOnlyHostFunctions is the describe-only introspection profile's builder:
// it returns an inert stub for every name in declared, bound in place of the
// real host-function closures. Each stub presents the same Wasm import signature
// the guest imports — read from the single per-name signature registry, so it can
// never drift from the real builders — but its body performs no real work: it
// does no external fetch, reads no source, appends no warning, and writes no
// stream. Binding inert stubs lets a guest of any type instantiate purely to be
// introspected through describe() without granting it any real host capability,
// so a describe() that calls a host function reaches only a no-op.
//
// Its shape matches the real builders so loadCore dispatches to it through the
// same loadProfile seam.
func describeOnlyHostFunctions(declared []string) []extism.HostFunction {
	out := make([]extism.HostFunction, 0, len(declared))
	for _, name := range declared {
		out = append(out, inertHostFunction(name))
	}
	return out
}

// inertHostFunction constructs one inert stub for the named host function,
// reading its signature from the single registry. The body zeroes each result
// slot, so a value-returning import returns a benign zero — which the guest-side
// wrappers read as "the host returned no envelope" — and a void import's body
// does nothing. It panics if name has no registered signature — the same
// maintenance guard newBoundHostFunction applies — so a name admitted by the
// union subset gate but absent from the registry surfaces loudly rather than as
// a silently-unbound guest import.
func inertHostFunction(name string) extism.HostFunction {
	sig, ok := hostFunctionSignatureFor(name)
	if !ok {
		panic("wasm: describe-only stub requested for host function with no registered signature: " + name)
	}
	resultCount := len(sig.results)
	return extism.NewHostFunctionWithStack(
		name,
		func(ctx context.Context, cp *extism.CurrentPlugin, stack []uint64) {
			for i := 0; i < resultCount; i++ {
				stack[i] = 0
			}
		},
		sig.params,
		sig.results,
	)
}

// hostFunctionsForDeclared returns the bound HostFunction values the
// caller's derived host-function slice asks for. Each body is handed the
// calling *Plugin (see newBoundHostFunction), so it can accumulate
// warnings into that instance's Plugin.warnings or format log lines keyed
// by the plugin's identifier.
//
// The caller is responsible for validating that every name in
// `declared` is in the allowlist first; this function assumes that
// check has already passed (validateHostFunctionsSubset enforces the
// subset check before Load's flow reaches here).
func hostFunctionsForDeclared(declared []string) []extism.HostFunction {
	out := make([]extism.HostFunction, 0, len(declared))
	for _, name := range declared {
		switch name {
		case "aggrega_log":
			out = append(out, hostFunctionAggregaLog())
		case "aggrega_emit_warning":
			out = append(out, hostFunctionAggregaEmitWarning())
		case "aggrega_fetch_external_input":
			out = append(out, hostFunctionAggregaFetchExternalInput())
		case "aggrega_pack_source":
			out = append(out, hostFunctionAggregaPackSource())
		case "aggrega_read_source_file":
			out = append(out, hostFunctionAggregaReadSourceFile())
		case "aggrega_parse_source":
			out = append(out, hostFunctionAggregaParseSource())
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

// hostFunctionsForDeclaredCLI is the runtime CLI profile's builder: it returns
// the bound HostFunction values for a guest loaded through
// Loader.LoadCLI. It handles the two streaming emitters directly and delegates
// every other name to hostFunctionsForDeclared, so the shared diagnostic
// emitters are built by the single shared builder and never re-implemented
// here.
//
// This is a distinct builder entry point from the compile-time
// hostFunctionsForDeclared, which has no streaming case: a build-profile load
// can never reach a streaming closure, both because the compile-time subset
// gate rejects the streaming names before any builder runs and because the
// compile-time builder cannot construct one. The delegation's default path
// preserves the panic-on-unknown safety net: a name admitted by the CLI subset
// gate but handled by no builder surfaces loudly at the first call site rather
// than as a silently-unbound guest import.
//
// The caller (LoadCLI) is responsible for validating that every name in
// `declared` is in the union allowlist first; this function assumes that check
// has already passed.
func hostFunctionsForDeclaredCLI(declared []string) []extism.HostFunction {
	out := make([]extism.HostFunction, 0, len(declared))
	for _, name := range declared {
		switch name {
		case "aggrega_cli_write_stdout":
			out = append(out, hostFunctionCLIWriteStdout())
		case "aggrega_cli_write_stderr":
			out = append(out, hostFunctionCLIWriteStderr())
		default:
			out = append(out, hostFunctionsForDeclared([]string{name})...)
		}
	}
	return out
}

// pluginMemoryReader is the minimal interface the host function
// bodies use to read arguments from plugin memory. Both methods take
// an offset alone and derive the argument's length from the Extism
// allocation table; they differ only in the shape the argument is
// declared to carry. ReadString reads an argument declared as UTF-8
// text — a log message, a diagnostic field, a URL. ReadBytes reads an
// argument declared as an opaque byte run, which is what a host
// function whose request travels in the binary encoding needs: the
// bytes are not text and decoding them as text would corrupt them.
//
// The Extism SDK's *CurrentPlugin satisfies this interface
// structurally; tests substitute a fake reader to exercise the body
// logic without fabricating a *CurrentPlugin (the SDK exposes no
// public constructor or test double for that type).
//
// This is deliberately not pluginRawMemoryReader, whose contract
// shape differs rather than whose result type differs: that seam
// takes an explicit (pointer, length) pair because the streaming
// contract names the length itself.
type pluginMemoryReader interface {
	ReadString(offset uint64) (string, error)
	ReadBytes(offset uint64) ([]byte, error)
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
func hostFunctionAggregaLog() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_log",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaLog(cp, stack)
		},
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

// pluginRawMemoryReader is the length-explicit read seam the streaming CLI
// host-function bodies use: it reads byteCount RAW bytes from guest linear
// memory starting at offset, returning the bytes and whether the range was in
// bounds. The wazero api.Memory the Extism SDK exposes through
// CurrentPlugin.Memory satisfies this interface structurally; tests substitute
// a fake over a byte slice to exercise the body without a running guest.
//
// This is deliberately not pluginMemoryReader.ReadString: the streaming
// contract passes an explicit (pointer, length) pair, so the host copies
// exactly the bytes the guest names — including a pointer into a static region
// with no Extism length-table entry — rather than deriving the length from the
// Extism allocation table.
type pluginRawMemoryReader interface {
	Read(offset, byteCount uint32) ([]byte, bool)
}

// hostFunctionCLIWriteStdout returns the host-side closure binding
// aggrega_cli_write_stdout(ptr i64, len i64), a streaming emitter granted only
// under the runtime CLI instantiation profile. The guest passes a pointer to a
// byte range in its linear memory and the range's length; the host copies those
// bytes out and writes them, incrementally, to the stdout writer injected when
// the plugin was loaded through Loader.LoadCLI.
//
// This runs at the terminal, as the CLI plugin executes — not inside a build.
// A compile-time build load (Loader.Load) can never bind this closure: the
// streaming name is absent from the shared allowlist, so the build-profile
// subset gate rejects a guest importing it before any closure is constructed.
// Streaming to real terminal streams is therefore available in the runtime
// profile and denied in the deterministic, capability-denied compile-time
// profile.
//
// Body extracted into handleCLIWrite so the test suite can exercise the
// read-and-write path against a fake memory reader and an in-memory writer
// without fabricating an *extism.CurrentPlugin.
func hostFunctionCLIWriteStdout() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_cli_write_stdout",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleCLIWrite(cp.Memory(), p.stdout, "aggrega_cli_write_stdout", stack)
		},
	)
}

// hostFunctionCLIWriteStderr is the stderr sibling of hostFunctionCLIWriteStdout,
// binding aggrega_cli_write_stderr(ptr i64, len i64) to the stderr writer
// injected at LoadCLI. It carries the same runtime-only grant: the compile-time
// build profile never binds it.
func hostFunctionCLIWriteStderr() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_cli_write_stderr",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleCLIWrite(cp.Memory(), p.stderr, "aggrega_cli_write_stderr", stack)
		},
	)
}

// handleCLIWrite is the testable inner logic shared by the stdout and stderr
// streaming host functions. It reads stack[1] bytes from guest memory at
// stack[0] via the raw-read seam and writes them to w. A nil writer (the
// compile-time profile never injects one) drops the write silently — a
// defensive guard, since a compile-time load can never reach this body. A
// memory read outside the guest's bounds or a writer error logs a host-side
// diagnostic naming the function and returns without trapping, so a bad guest
// pointer is observable in host logs without crashing the call.
func (p *Plugin) handleCLIWrite(mem pluginRawMemoryReader, w io.Writer, funcName string, stack []uint64) {
	ptr := stack[0]
	length := stack[1]
	if w == nil {
		return
	}
	data, ok := mem.Read(uint32(ptr), uint32(length))
	if !ok {
		log.Printf("plugin %q: %s: memory read of %d bytes at offset %d out of bounds", p.name, funcName, length, ptr)
		return
	}
	if _, err := w.Write(data); err != nil {
		log.Printf("plugin %q: %s: writing %d bytes to the stream failed: %v", p.name, funcName, length, err)
	}
}

// hostFunctionAggregaEmitWarning returns the host-side closure
// binding aggrega_emit_warning(code_ptr i64, message_ptr i64,
// path_ptr i64). The guest emits a structured warning the host
// accumulates into Plugin.warnings; the caller surfaces accumulated
// warnings after Plugin.Call returns via Plugin.DrainWarnings.
//
// Body extracted into handleAggregaEmitWarning so the test suite
// can exercise the read-format-append path against a fake reader.
func hostFunctionAggregaEmitWarning() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_emit_warning",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaEmitWarning(cp, stack)
		},
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

// pluginMemoryWriter is the write-side sibling of pluginMemoryReader:
// the minimal interface a host function body uses to write a result
// payload into plugin memory and obtain the offset the guest reads it
// back from. The Extism SDK's *CurrentPlugin satisfies this interface
// structurally; tests substitute a fake writer to exercise the body
// logic without fabricating a *CurrentPlugin.
type pluginMemoryWriter interface {
	WriteBytes(b []byte) (uint64, error)
}

// hostFunctionAggregaFetchExternalInput returns the host-side closure
// binding aggrega_fetch_external_input(url_ptr i64) -> envelope_ptr
// i64. The guest passes a pointer to a UTF-8 URL string in plugin
// memory; the host resolves the URL through the plugin's external-
// input service and writes one FetchExternalInputResponse JSON document back into
// plugin memory, returning its offset.
//
// Failure is deliberately in-band — an error envelope, never a trap or
// panic: an unpinned URL in a frozen closure must surface as a
// deterministic render failure with a clear message, and the guest
// must be able to carry that message into its own diagnostics. Every
// failure envelope's message is also recorded on the plugin's warning
// channel so the host caller can surface the precise reason after the
// call returns.
//
// Body extracted into handleAggregaFetchExternalInput so the test
// suite can exercise the read-fetch-write path against fake memory
// interfaces without fabricating an *extism.CurrentPlugin.
func hostFunctionAggregaFetchExternalInput() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_fetch_external_input",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaFetchExternalInput(ctx, cp, cp, stack)
		},
	)
}

// handleAggregaFetchExternalInput is the testable inner logic of the
// aggrega_fetch_external_input host function body. Reads the URL from
// plugin memory at stack[0] via the reader interface, fetches its
// bytes through the plugin's external-input service with the plugin's
// name as the requiring-site provenance, and writes the result
// envelope into plugin memory via the writer interface, leaving the
// envelope's offset in stack[0].
//
// Every failure — a memory read failure, no external-input service
// configured for this plugin, or a fetch error — produces an error
// envelope rather than a trap, and records the same message on the
// plugin's warning channel. The no-service case covers every loader
// whose ExternalInputs field is nil (the zero-value Loader): the guest
// receives a clear "not available in this context" message instead of
// a panic.
func (p *Plugin) handleAggregaFetchExternalInput(ctx context.Context, reader pluginMemoryReader, writer pluginMemoryWriter, stack []uint64) {
	rawURL, err := reader.ReadString(stack[0])
	if err != nil {
		log.Printf("plugin %q: aggrega_fetch_external_input: memory read failed: %v", p.name, err)
		stack[0] = p.writeFetchFailure(writer,
			fmt.Sprintf("aggrega_fetch_external_input: reading the URL from plugin memory failed: %v", err), abi.CodeFetchRequestUnreadable)
		return
	}
	if p.externalInputs == nil {
		stack[0] = p.writeFetchFailure(writer,
			fmt.Sprintf("external inputs are not available in this context: cannot fetch %q", rawURL), abi.CodeFetchInputUnavailable)
		return
	}
	payload, err := p.externalInputs.Fetch(ctx, rawURL, p.name)
	if err != nil {
		stack[0] = p.writeFetchFailure(writer, err.Error(), abi.CodeFetchFailed)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	stack[0] = p.writeFetchEnvelope(writer, abi.FetchExternalInputResponse{Content: &encoded})
}

// writeFetchFailure records message on the plugin's warning channel and
// writes an error envelope carrying it, returning the envelope's
// offset. The warning mirrors the envelope so the host caller sees the
// same diagnostic the guest received.
func (p *Plugin) writeFetchFailure(writer pluginMemoryWriter, message, code string) uint64 {
	p.appendWarning(fmt.Sprintf("plugin %s: %s", p.name, message))
	return p.writeFetchEnvelope(writer, abi.FetchExternalInputResponse{Error: message, HostfuncErrorCode: code})
}

// writeFetchEnvelope marshals env and writes it into plugin memory,
// returning the offset the guest reads it from. On a marshal or memory
// write failure it logs a host-side diagnostic and returns offset 0 —
// the one case where no envelope can be delivered; the guest-side PDK
// wrapper treats a zero offset as "host returned no envelope".
func (p *Plugin) writeFetchEnvelope(writer pluginMemoryWriter, env abi.FetchExternalInputResponse) uint64 {
	b, err := env.MarshalJSON()
	if err != nil {
		log.Printf("plugin %q: aggrega_fetch_external_input: envelope marshal failed: %v", p.name, err)
		return 0
	}
	offset, err := writer.WriteBytes(b)
	if err != nil {
		log.Printf("plugin %q: aggrega_fetch_external_input: memory write failed: %v", p.name, err)
		return 0
	}
	return offset
}

// hostFunctionAggregaPackSource returns the host-side closure binding
// aggrega_pack_source(request_ptr i64) -> response_ptr i64. The guest passes a
// pointer to a SourcePackRequest document naming a selection (op and path)
// relative to the acquired source root; the host resolves the selection under
// the current node's source root, packs the selected bytes, and writes one
// SourcePackResponse document back into plugin memory, returning its offset.
//
// Both slots on this call carry canonical CBOR, so the request slot is an opaque
// byte run rather than UTF-8 text. That is a shape the guest declares, not just
// an encoding it chooses: a guest binding whose request slot is typed as text
// cannot pass these bytes intact. The host reads the request through
// pluginMemoryReader.ReadBytes and both directions go through the payload types'
// generated canonical-CBOR entry points, so neither side hand-parses bytes and
// the request is byte-identical for identical input.
//
// The source root is read from the plugin at call time (Plugin.sourceRoot), not
// captured when the closure is built, because one plugin instance may render
// many nodes with different acquired sources; the caller sets it per node before
// each invocation. Failure is in-band — an error envelope, never a trap — so a
// sandbox rejection or a read failure surfaces as a deterministic render failure
// the guest carries into its own diagnostics.
//
// Body extracted into handleAggregaPackSource so the test suite can exercise the
// read-pack-write path against fake memory interfaces without fabricating an
// *extism.CurrentPlugin.
func hostFunctionAggregaPackSource() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_pack_source",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaPackSource(cp, cp, stack)
		},
	)
}

// handleAggregaPackSource is the testable inner logic of the aggrega_pack_source
// host function body. It reads the request bytes from plugin memory at stack[0],
// resolves the selection under the plugin's source root through the sandbox, packs
// the selected bytes, and writes the SourcePackResponse envelope into plugin
// memory, leaving its offset in stack[0]. Every failure produces an error
// envelope rather than a trap.
func (p *Plugin) handleAggregaPackSource(reader pluginMemoryReader, writer pluginMemoryWriter, stack []uint64) {
	reqBytes, err := reader.ReadBytes(stack[0])
	if err != nil {
		log.Printf("plugin %q: aggrega_pack_source: memory read failed: %v", p.name, err)
		stack[0] = p.writeSourcePackFailure(writer, fmt.Sprintf("aggrega_pack_source: reading the request from plugin memory failed: %v", err), abi.CodeRequestUnreadable)
		return
	}
	var req abi.SourcePackRequest
	if err := req.DecodeCBOR(reqBytes); err != nil {
		stack[0] = p.writeSourcePackFailure(writer, fmt.Sprintf("aggrega_pack_source: decoding the request failed: %v", err), abi.CodeRequestUndecodable)
		return
	}
	if p.sourceRoot == "" {
		stack[0] = p.writeSourcePackFailure(writer, "aggrega_pack_source: no acquired source is available in this context", abi.CodeSourceRootUnavailable)
		return
	}
	proj, code, err := p.packSourceSelection(req.Op, req.Path)
	if err != nil {
		stack[0] = p.writeSourcePackFailure(writer, err.Error(), code)
		return
	}
	b, err := abi.SourcePackResponse{SourceProjection: proj}.EncodeCBOR()
	if err != nil {
		log.Printf("plugin %q: aggrega_pack_source: response encode failed: %v", p.name, err)
		stack[0] = 0
		return
	}
	stack[0] = p.writeEnvelopeBytes(writer, b)
}

// hostFunctionAggregaReadSourceFile returns the host-side closure binding
// aggrega_read_source_file(request_ptr i64) -> response_ptr i64. The guest passes
// a pointer to a SourceFileRequest document naming a single file relative to the
// acquired source root; the host resolves it under the current node's source
// root, reads the file, and writes one SourceFileResponse document back into
// plugin memory, returning its offset.
//
// Both slots on this call carry canonical CBOR, so the request slot is an opaque
// byte run rather than UTF-8 text, exactly as on aggrega_pack_source: a guest
// binding whose request slot is typed as text cannot pass these bytes intact.
// The host reads the request through pluginMemoryReader.ReadBytes and both
// directions go through the payload types' generated canonical-CBOR entry points.
//
// This is the content-based-selection primitive, designed for a guest whose
// resolution logic must inspect a file's content before choosing what to pack. No
// production guest uses it yet — the production guests resolve from the name-only
// listing alone — so it is exercised by a test-fixture guest rather than a shipped
// one. It follows aggrega_pack_source's source-root and in-band-failure discipline.
//
// Body extracted into handleAggregaReadSourceFile so the test suite can exercise
// the read-read-write path against fake memory interfaces.
func hostFunctionAggregaReadSourceFile() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_read_source_file",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaReadSourceFile(cp, cp, stack)
		},
	)
}

// handleAggregaReadSourceFile is the testable inner logic of the
// aggrega_read_source_file host function body. It reads the request bytes from
// plugin memory at stack[0], resolves the file under the plugin's source root
// through the sandbox, reads it, and writes the SourceFileResponse envelope into
// plugin memory, leaving its offset in stack[0]. Every failure produces an error
// envelope rather than a trap.
func (p *Plugin) handleAggregaReadSourceFile(reader pluginMemoryReader, writer pluginMemoryWriter, stack []uint64) {
	reqBytes, err := reader.ReadBytes(stack[0])
	if err != nil {
		log.Printf("plugin %q: aggrega_read_source_file: memory read failed: %v", p.name, err)
		stack[0] = p.writeSourceFileFailure(writer, fmt.Sprintf("aggrega_read_source_file: reading the request from plugin memory failed: %v", err), abi.CodeRequestUnreadable)
		return
	}
	var req abi.SourceFileRequest
	if err := req.DecodeCBOR(reqBytes); err != nil {
		stack[0] = p.writeSourceFileFailure(writer, fmt.Sprintf("aggrega_read_source_file: decoding the request failed: %v", err), abi.CodeRequestUndecodable)
		return
	}
	if p.sourceRoot == "" {
		stack[0] = p.writeSourceFileFailure(writer, "aggrega_read_source_file: no acquired source is available in this context", abi.CodeSourceRootUnavailable)
		return
	}
	resolved, code, err := p.resolveUnderSourceRoot(req.Path)
	if err != nil {
		stack[0] = p.writeSourceFileFailure(writer, err.Error(), code)
		return
	}
	if err := p.sourceFileReadable(resolved, req.Path); err != nil {
		stack[0] = p.writeSourceFileFailure(writer, "aggrega_read_source_file: "+err.Error(), abi.CodeSourceReadFailed)
		return
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		stack[0] = p.writeSourceFileFailure(writer, fmt.Sprintf("aggrega_read_source_file: reading %q failed: %v", req.Path, err), abi.CodeSourceReadFailed)
		return
	}
	b, err := abi.SourceFileResponse{Content: data}.EncodeCBOR()
	if err != nil {
		log.Printf("plugin %q: aggrega_read_source_file: response encode failed: %v", p.name, err)
		stack[0] = 0
		return
	}
	stack[0] = p.writeEnvelopeBytes(writer, b)
}

// hostFunctionAggregaParseSource returns the host-side closure binding
// aggrega_parse_source(request_ptr i64) -> response_ptr i64. The guest passes a
// pointer to a ParseInput document naming a syntactic format, the source bytes,
// and an optional opaque options document; the host runs the producer (a parser)
// for that format through the injected source-parser seam and writes one
// ParseSourceResponse document — the canonical tree on success, an error message
// on failure — back into plugin memory, returning its offset.
//
// Both slots on this call carry canonical CBOR, so the request slot is an opaque
// byte run rather than UTF-8 text. That is a shape the guest declares, not just
// an encoding it chooses: a guest binding whose request slot is typed as text
// cannot pass these bytes intact. The host reads the request through
// pluginMemoryReader.ReadBytes and both directions go through the payload types'
// generated canonical-CBOR entry points, so neither side hand-parses bytes and
// the request is byte-identical for identical input.
//
// It is the single shared entry every host-mediated producer-consumption call
// routes through, so the re-entrancy guard here protects any consumer/producer
// pair. The seam is read from the plugin at call time (Plugin.sourceParser), not
// captured when the closure is built, because one plugin instance may serve many
// invocations; the caller injects it through SetSourceParser before each
// invocation. Failure is in-band — an error envelope, never a trap — so a
// no-producer resolution, a producer failure, or a denied re-entrant call
// surfaces as a message the consuming guest carries into its own diagnostics.
//
// Body extracted into handleAggregaParseSource so the test suite can exercise the
// read-guard-run-write path against fake memory interfaces without fabricating an
// *extism.CurrentPlugin.
func hostFunctionAggregaParseSource() extism.HostFunction {
	return newBoundHostFunction(
		"aggrega_parse_source",
		func(ctx context.Context, p *Plugin, cp *extism.CurrentPlugin, stack []uint64) {
			p.handleAggregaParseSource(ctx, cp, cp, stack)
		},
	)
}

// handleAggregaParseSource is the testable inner logic of the aggrega_parse_source
// host function body. It reads the ParseInput request from plugin memory at
// stack[0], denies a re-entrant producer-consumption call, runs the injected
// source-parser seam under an incremented producer-consumption depth, and writes
// the ParseSourceResponse envelope into plugin memory, leaving its offset in
// stack[0]. Every failure produces an error envelope rather than a trap.
//
// The re-entrancy guard is checked before the seam-presence check so a nested
// call is denied as a re-entry regardless of whether the reached plugin carries a
// seam — the re-entry is the salient failure. The depth rides ctx: the seam runs
// the producer's export with the incremented-depth context, so a producer that
// calls back into aggrega_parse_source observes depth at the limit and is denied
// here.
func (p *Plugin) handleAggregaParseSource(ctx context.Context, reader pluginMemoryReader, writer pluginMemoryWriter, stack []uint64) {
	reqBytes, err := reader.ReadBytes(stack[0])
	if err != nil {
		log.Printf("plugin %q: aggrega_parse_source: memory read failed: %v", p.name, err)
		stack[0] = p.writeParseSourceFailure(writer, fmt.Sprintf("aggrega_parse_source: reading the request from plugin memory failed: %v", err), abi.CodeRequestUnreadable)
		return
	}
	var req abi.ParseInput
	if err := req.DecodeCBOR(reqBytes); err != nil {
		stack[0] = p.writeParseSourceFailure(writer, fmt.Sprintf("aggrega_parse_source: decoding the request failed: %v", err), abi.CodeRequestUndecodable)
		return
	}
	if producerConsumptionDepthFromContext(ctx) >= producerConsumptionDepthLimit {
		stack[0] = p.writeParseSourceFailure(writer, fmt.Sprintf("aggrega_parse_source: %v", ErrProducerConsumptionReentry()), abi.CodeProducerConsumptionReentry)
		return
	}
	if p.sourceParser == nil {
		stack[0] = p.writeParseSourceFailure(writer, "aggrega_parse_source: no source parser is available in this context", abi.CodeNoSourceParser)
		return
	}
	depth := producerConsumptionDepthFromContext(ctx)
	tree, err := p.sourceParser(contextWithProducerConsumptionDepth(ctx, depth+1), req.Format, req.Bytes, req.Options)
	if err != nil {
		// The seam adapter maps a cross-package resolution sentinel to its wire
		// code via a CodedHostError; any other seam error is a parse failure.
		code := abi.CodeParseFailed
		var coded *CodedHostError
		if errors.As(err, &coded) {
			code = coded.Code
		}
		stack[0] = p.writeParseSourceFailure(writer, err.Error(), code)
		return
	}
	b, err := abi.ParseSourceResponse{Tree: tree}.EncodeCBOR()
	if err != nil {
		log.Printf("plugin %q: aggrega_parse_source: response encode failed: %v", p.name, err)
		stack[0] = 0
		return
	}
	stack[0] = p.writeEnvelopeBytes(writer, b)
}

// writeParseSourceFailure records message on the plugin's warning channel and
// writes a ParseSourceResponse error envelope carrying it, returning the
// envelope's offset. The warning mirrors the envelope so the host caller sees the
// same diagnostic the guest received.
//
// The failure envelope is encoded exactly as the success envelope is, through the
// payload type's canonical-CBOR entry point. A guest reads one reply slot and
// cannot know before decoding which of the two it holds, so an error envelope in
// a different encoding would be undecodable by the very path that has to report
// the failure.
func (p *Plugin) writeParseSourceFailure(writer pluginMemoryWriter, message, code string) uint64 {
	p.appendWarning(fmt.Sprintf("plugin %s: %s", p.name, message))
	b, err := abi.ParseSourceResponse{Error: message, HostfuncErrorCode: code}.EncodeCBOR()
	if err != nil {
		log.Printf("plugin %q: aggrega_parse_source: error envelope encode failed: %v", p.name, err)
		return 0
	}
	return p.writeEnvelopeBytes(writer, b)
}

// packSourceSelection resolves a selection under the plugin's source root and
// packs it into a SourceProjection. The op names the selection shape:
//
//   - "file" packs the single file at the resolved path, keyed by its base name.
//   - "dir" packs the immediate files of the resolved directory, each keyed by
//     its base name, in sorted order.
//   - "tree" packs the whole subtree rooted at the resolved path, recursively, in
//     the host's deterministic walk order (filepath.WalkDir visits lexically per
//     directory), each file keyed by its slash-separated path relative to the
//     resolved root.
//
// The resolved path is sandboxed under the source root; a selection escaping
// above it is refused. The keying and walk order are fixed by the host's
// deterministic walk, so the packed bytes are byte-identical for the same input.
// packSourceSelection returns the wire error code for the failure condition it
// hits alongside the error, so the host boundary sets one code per condition
// (the sandbox escape's code is owned by resolveUnderSourceRoot). On success the
// code is the empty string.
//
// Every selection is read through the plugin's source scope — the ignore rules
// the entity's lock records — so what a guest is packed is exactly what a
// publication of the entity carries: a file or directory the rules leave out is
// not packed, a file named directly that they leave out answers as absent with
// its path named, and a symbolic link is refused rather than followed.
func (p *Plugin) packSourceSelection(op, rel string) (*abi.SourceProjection, string, error) {
	resolved, code, err := p.resolveUnderSourceRoot(rel)
	if err != nil {
		return nil, code, err
	}
	switch op {
	case "file":
		if err := p.sourceFileReadable(resolved, rel); err != nil {
			return nil, abi.CodeSourceReadFailed, fmt.Errorf("aggrega_pack_source: %v", err)
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			return nil, abi.CodeSourceReadFailed, fmt.Errorf("aggrega_pack_source: reading %q failed: %v", rel, err)
		}
		return &abi.SourceProjection{Files: []abi.SourceFile{{Path: filepath.Base(resolved), Content: data}}}, "", nil
	case "dir":
		entries, err := os.ReadDir(resolved)
		if err != nil {
			return nil, abi.CodeSourceReadFailed, fmt.Errorf("aggrega_pack_source: listing %q failed: %v", rel, err)
		}
		var files []abi.SourceFile
		for _, e := range entries {
			full := filepath.Join(resolved, e.Name())
			if e.Type()&fs.ModeSymlink != 0 {
				if err := p.sourceFileReadable(full, path.Join(rel, e.Name())); err != nil {
					return nil, abi.CodeSourceReadFailed, fmt.Errorf("aggrega_pack_source: %v", err)
				}
				continue
			}
			if e.IsDir() || p.sourceScope.Excluded(full, false) {
				continue
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return nil, abi.CodeSourceReadFailed, fmt.Errorf("aggrega_pack_source: reading %q failed: %v", e.Name(), err)
			}
			files = append(files, abi.SourceFile{Path: e.Name(), Content: data})
		}
		return &abi.SourceProjection{Files: files}, "", nil
	case "tree":
		entries, walkErr := p.sourceScope.Entries(resolved, nil)
		if walkErr != nil {
			return nil, abi.CodeSourceReadFailed, fmt.Errorf("aggrega_pack_source: walking %q failed: %v", rel, walkErr)
		}
		var files []abi.SourceFile
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			data, err := os.ReadFile(filepath.Join(resolved, filepath.FromSlash(e.Path)))
			if err != nil {
				return nil, abi.CodeSourceReadFailed, fmt.Errorf("aggrega_pack_source: walking %q failed: %v", rel, err)
			}
			files = append(files, abi.SourceFile{Path: e.Path, Content: data})
		}
		return &abi.SourceProjection{Files: files}, "", nil
	default:
		return nil, abi.CodeUnknownSelectionOp, fmt.Errorf("aggrega_pack_source: unknown selection op %q", op)
	}
}

// sourceFileReadable reports why the file at resolved — named rel by the guest —
// is not one of the source's files, or nil where it is: a symbolic link is
// refused, and a file the scope's rules leave out answers as not there, naming
// it, because a publication of the entity does not carry it and a consumer's
// render could not read it either.
func (p *Plugin) sourceFileReadable(resolved, rel string) error {
	if err := sourcewalk.RefuseLink(resolved, rel); err != nil {
		return err
	}
	if p.sourceScope.Excluded(resolved, false) {
		return fmt.Errorf("%q is not among the source's files: the ignore rules recorded for the entity leave it out, "+
			"so a publication of the entity does not carry it", rel)
	}
	return nil
}

// resolveUnderSourceRoot resolves a slash-separated, source-root-relative path
// to an absolute filesystem path under the plugin's source root, refusing any
// path that escapes above the root. The empty string and "." both name the root
// itself. A path that climbs above the root is refused even where the join
// would land back inside it, so a "../" traversal in the guest-supplied path is
// a loud refusal rather than a read outside the acquired source, and the
// refusal does not depend on what the root is named.
// resolveUnderSourceRoot owns the sandbox-escape wire code: on a refused escape
// it returns CodeSourceEscape alongside the error, so the escape condition maps
// to its code in exactly one place. On success the code is the empty string.
//
// The containment question itself is pathscope's, and the wire code stops here.
// The code belongs to a closed vocabulary a guest branches on, and most callers
// asking the same question have no guest to answer, so the shared predicate
// reports a bare refusal and this wrapper is where it acquires a code.
func (p *Plugin) resolveUnderSourceRoot(rel string) (string, string, error) {
	// The guest spells the root itself as the empty string; pathscope refuses an
	// empty relative path, so the meaning is normalised here, where it is held.
	sel := rel
	if sel == "" {
		sel = "."
	}
	joined, ok := pathscope.Resolve(p.sourceRoot, filepath.FromSlash(sel))
	if !ok {
		return "", abi.CodeSourceEscape, fmt.Errorf("selection %q escapes the source root", rel)
	}
	return joined, "", nil
}

// writeSourcePackFailure records message on the plugin's warning channel and
// writes a SourcePackResponse error envelope carrying it, returning the
// envelope's offset. The warning mirrors the envelope so the host caller sees
// the same diagnostic the guest received.
//
// The failure envelope is encoded exactly as the success envelope is, through the
// payload type's canonical-CBOR entry point. A guest reads one reply slot and
// cannot know before decoding which of the two it holds, so an error envelope in
// a different encoding would be undecodable by the very path that has to report
// the failure.
func (p *Plugin) writeSourcePackFailure(writer pluginMemoryWriter, message, code string) uint64 {
	p.appendWarning(fmt.Sprintf("plugin %s: %s", p.name, message))
	b, err := abi.SourcePackResponse{Error: message, HostfuncErrorCode: code}.EncodeCBOR()
	if err != nil {
		log.Printf("plugin %q: aggrega_pack_source: error envelope encode failed: %v", p.name, err)
		return 0
	}
	return p.writeEnvelopeBytes(writer, b)
}

// writeSourceFileFailure is the read-source-file sibling of
// writeSourcePackFailure: it records message on the plugin's warning channel and
// writes a SourceFileResponse error envelope carrying it, returning the
// envelope's offset, and it encodes that envelope in the same encoding the
// success envelope carries for the reason given there.
func (p *Plugin) writeSourceFileFailure(writer pluginMemoryWriter, message, code string) uint64 {
	p.appendWarning(fmt.Sprintf("plugin %s: %s", p.name, message))
	b, err := abi.SourceFileResponse{Error: message, HostfuncErrorCode: code}.EncodeCBOR()
	if err != nil {
		log.Printf("plugin %q: aggrega_read_source_file: error envelope encode failed: %v", p.name, err)
		return 0
	}
	return p.writeEnvelopeBytes(writer, b)
}

// writeEnvelopeBytes writes an already-marshaled envelope into plugin memory,
// returning the offset the guest reads it from. On a memory write failure it
// logs a host-side diagnostic and returns offset 0 — the guest-side PDK wrapper
// treats a zero offset as "host returned no envelope".
func (p *Plugin) writeEnvelopeBytes(writer pluginMemoryWriter, b []byte) uint64 {
	offset, err := writer.WriteBytes(b)
	if err != nil {
		log.Printf("plugin %q: source host function: memory write failed: %v", p.name, err)
		return 0
	}
	return offset
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
