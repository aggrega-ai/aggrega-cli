// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package wasm is the sole construction site for Extism plugins.
// Per the project's loader-containment discipline, only this package
// imports github.com/extism/go-sdk and constructs *extism.Plugin
// values. Every consumer in the compiler — the publishing-linter, the
// future mutator / policy-validator / helm-renderer / pipeline /
// linter sockets — receives a *Plugin constructed by Loader.Load and
// invokes it through Plugin.Call.
//
// The package's AOT-purity firewall has two layers. The manifest
// firewall (see manifest_translate.go) rejects any plugin
// configuration that would let guest code reach beyond its in-memory
// inputs at run time: outbound network access via AllowedHosts,
// filesystem access via AllowedPaths, or host functions outside the
// curated allowlist (see hostfuncs.go). The static import firewall
// (see wasi_imports.go) walks the plugin's Wasm import section and
// rejects any WASI import outside a curated allowlist of benign
// runtime-init imports — filesystem traversal, network sockets, and
// directory access are all default-denied.
//
// WASI itself is enabled, but configured deterministically and
// capability-denied: the loader hands the Extism runtime a wazero
// ModuleConfig with frozen observable wall time, real monotonic time
// (the guest language runtime needs an advancing monotonic clock to
// bootstrap), a zero entropy source, discarded stdio, and no
// filesystem, argument, or environment passthrough. The guest's
// language runtime bootstraps against real WASI behaviour, yet every
// value it can observe is a deterministic function of the build input.
// This preserves Aggrega's determinism and ahead-of-time guarantees
// regardless of which plugin runs.
//
// Plugin.Call wraps the Extism call boundary with a deferred recover()
// that converts a Go-side panic into an ErrWasmGuestPanic-wrapped
// error. The wrap is a safety net behind Extism's normal error
// envelope; most Wasm-side traps surface as ordinary Go errors through
// the SDK and never trigger the panic path. A guest that invokes
// proc_exit surfaces as a non-zero exit code wrapped in
// ErrWasmPluginExited.
package wasm

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"
)

// Loader is the package's entry point for constructing a Plugin from a
// PluginSpec. A zero-value Loader is ready to use; callers SHOULD reuse
// a single Loader across the life of the compiler process so that any
// future runtime-configuration knobs the loader picks up (Wazero
// runtime caches, observability adapters, and so on) amortise across
// plugin instances.
type Loader struct {
	// Reserved for future runtime-configuration knobs the loader
	// itself owns (a shared Wazero compilation cache, for example).
	// Today the struct is empty; declared as a struct rather than a
	// free function so future knobs can land without changing every
	// call site.
}

// Plugin wraps an Extism plugin handle with Aggrega-specific lifecycle
// and call-boundary behaviour. Constructed only by Loader.Load.
//
// Plugin accumulates structured warnings emitted by the guest via the
// aggrega_emit_warning host function across the lifetime of a single
// Plugin instance. Concurrent host-function invocations on the same
// Plugin are race-free under warningsMu; the warnings slice is
// retrievable via Warnings (defensive copy, no clear) or
// DrainWarnings (returns + atomic clear). The warning channel
// surfaces guest-side diagnostics the host's build-time validator
// converts into user-visible warnings.
type Plugin struct {
	name       string
	plugin     *extism.Plugin
	warningsMu sync.Mutex
	warnings   []string
}

// Warnings returns a defensive copy of the warnings accumulated by
// guest-side aggrega_emit_warning invocations. The returned slice is
// safe to mutate; subsequent calls to Warnings or DrainWarnings
// observe internal state unaffected by mutations of a prior return.
// Callers that want to surface warnings repeatedly (without taking
// ownership) use this; callers that surface each warning once use
// DrainWarnings.
func (p *Plugin) Warnings() []string {
	p.warningsMu.Lock()
	defer p.warningsMu.Unlock()
	if len(p.warnings) == 0 {
		return nil
	}
	out := make([]string, len(p.warnings))
	copy(out, p.warnings)
	return out
}

// DrainWarnings returns the warnings accumulated since the last drain
// (or since the Plugin was constructed) and atomically clears
// internal state. The returned slice's ownership transfers to the
// caller; the Plugin sees no further trace of those warnings. Callers
// that surface each warning exactly once across a long-lived Plugin
// use DrainWarnings to avoid double-surfacing.
func (p *Plugin) DrainWarnings() []string {
	p.warningsMu.Lock()
	defer p.warningsMu.Unlock()
	if len(p.warnings) == 0 {
		return nil
	}
	out := p.warnings
	p.warnings = nil
	return out
}

// appendWarning records a formatted warning string under warningsMu.
// Called by the aggrega_emit_warning host function body; extracted as
// a method so concurrent-append tests can exercise the locked-append
// path without reaching into struct internals.
func (p *Plugin) appendWarning(formatted string) {
	p.warningsMu.Lock()
	p.warnings = append(p.warnings, formatted)
	p.warningsMu.Unlock()
}

// Load constructs a Plugin from a PluginSpec. The flow:
//
//  1. Translate the spec into an Extism manifest and run the
//     AOT-purity firewall against the manifest fields.
//  2. Read the plugin Wasm bytes from spec.Path once into memory.
//  3. Walk the bytes' import section and apply the static import
//     firewall: allowlisted WASI imports pass; capability-granting
//     names are rejected with ErrWasmManifestRejected.
//  4. Derive the host functions the guest actually imports from the
//     same byte slice, verify the derived set is a subset of the
//     curated allowlist, and — when the spec carries an authored
//     declaration — cross-check that authored declaration against the
//     derived truth, halting on disagreement.
//  5. Register the derived host functions (aggrega_log,
//     aggrega_emit_warning, and future per-socket host functions);
//     the derived set, not an authored list, is what the plugin's
//     bound surface is built from.
//  6. Construct the underlying extism.Plugin from the same in-memory
//     byte slice (via extism.WasmData), with EnableWasi: true and the
//     deterministic, capability-denied ModuleConfig from
//     deterministicModuleConfig. The plugin's WASI imports resolve to
//     the Extism-bundled wazero WASI runtime, whose observable
//     behaviour the ModuleConfig pins to deterministic values.
//
// Reading the bytes once and threading the same slice through firewall
// inspection, host-function derivation, and Extism instantiation
// eliminates the TOCTOU window between validation and execution.
// Returns an error wrapping ErrWasmManifestRejected if the firewall
// rejects the manifest or any import or a derived host function is
// outside the allowlist; ErrWasmHostFunctionsDeclarationMismatch if an
// authored declaration disagrees with the derived set; the Extism SDK's
// error otherwise.
func (l *Loader) Load(ctx context.Context, spec PluginSpec) (*Plugin, error) {
	manifest, err := translateToManifest(spec)
	if err != nil {
		return nil, err
	}

	// Bytes supplied directly (embedded / verified-cache content with no
	// stable filesystem location) win over Path; otherwise read Path once.
	// Reading once and threading the same slice through firewall inspection,
	// derivation, and Extism instantiation eliminates the TOCTOU window
	// between validation and execution.
	wasmBytes := spec.Bytes
	if wasmBytes == nil {
		var err error
		wasmBytes, err = os.ReadFile(spec.Path)
		if err != nil {
			return nil, fmt.Errorf("wasm: read plugin %q from %s: %w", spec.Name, spec.Path, err)
		}
	}

	if err := validateWasiImports(spec.Name, wasmBytes); err != nil {
		return nil, err
	}

	// Derive the host functions the guest actually imports from the bytes,
	// then verify the derived set against the curated allowlist by reusing
	// the loader's single subset gate. The derived set is authoritative —
	// the .wasm import section is ground truth for which host functions the
	// guest calls.
	declaredHostFunctions, err := deriveDeclaredHostFunctions(spec.Name, wasmBytes)
	if err != nil {
		return nil, err
	}
	if err := validateDeclaredHostFunctions(PluginSpec{
		Name:                  spec.Name,
		DeclaredHostFunctions: declaredHostFunctions,
	}); err != nil {
		return nil, err
	}

	// When the entity authored a host-function declaration, cross-check it
	// against the derived truth. The authored field is a deliberate
	// independent claim, not a mirror; a disagreement is a Loud Guardrail.
	if len(spec.DeclaredHostFunctions) > 0 {
		if err := crossCheckAuthoredHostFunctions(spec.Name, spec.DeclaredHostFunctions, declaredHostFunctions); err != nil {
			return nil, err
		}
	}

	// Replace the manifest's WasmFile entry (which would have Extism
	// re-read from disk) with WasmData carrying the already-read
	// bytes. The firewall validated these specific bytes; Extism
	// will compile and instantiate the same bytes.
	manifest.Wasm = []extism.Wasm{
		extism.WasmData{Data: wasmBytes, Name: spec.Name},
	}

	// Allocate the wrapper before constructing the Extism plugin so
	// the per-plugin host-function closures can capture *Plugin and
	// thread guest-emitted warnings into the warnings slice. The
	// Extism plugin handle is attached to p after NewPlugin returns.
	p := &Plugin{name: spec.Name}

	functions := p.hostFunctionsForDeclared(declaredHostFunctions)

	config := extism.PluginConfig{
		EnableWasi:   true,
		ModuleConfig: deterministicModuleConfig(),
	}

	extismPlugin, err := extism.NewPlugin(ctx, manifest, config, functions)
	if err != nil {
		return nil, fmt.Errorf("wasm: load plugin %q: %w", spec.Name, err)
	}

	p.plugin = extismPlugin
	return p, nil
}

// zeroEntropyReader is an io.Reader that fills every read with zero
// bytes. It backs the guest's WASI random_get so a plugin observes a
// deterministic entropy source — never real host randomness, which
// would break the Deterministic Output invariant.
type zeroEntropyReader struct{}

func (zeroEntropyReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// deterministicModuleConfig returns the wazero ModuleConfig the loader
// hands the Extism runtime when instantiating a guest. The config
// enables real WASI behaviour (the guest language runtime bootstraps
// against it) while denying every capability that would let the guest
// observe host state varying across builds:
//
//   - WithWalltime fixed at the Unix epoch (0s, 0ns): the guest's
//     observable wall clock is a build-invariant constant, so a guest
//     reading the time cannot leak real time into the output. This is
//     the Deterministic Output invariant at the clock surface.
//   - WithSysNanotime: a REAL advancing monotonic clock. A guest
//     language runtime that reads monotonic time during bootstrap
//     requires it to advance — a frozen monotonic clock makes the Go
//     runtime spin (it loops waiting for the clock to move) and a
//     zero monotonic clock trips the runtime's "nanotime returning
//     zero" guard. Monotonic time measures elapsed duration, not a
//     wall-clock instant, so a guest that does not deliberately encode
//     elapsed time into its output stays deterministic; encoding
//     elapsed monotonic time into output is an authoring-level
//     determinism violation a guest must avoid, caught by the
//     determinism guards rather than prevented by construction.
//   - WithRandSource(zeroEntropyReader): the guest's entropy source
//     yields zero bytes, never real host randomness.
//   - WithStdout/WithStderr(io.Discard): the guest's stdio is
//     discarded; nothing the guest writes reaches the host's streams.
//   - No FSConfig, no args, no environment: wazero's defaults deny the
//     filesystem and present empty args and environment, so the guest
//     reads nothing from the host process. The static import firewall
//     additionally rejects the filesystem-traversal and socket WASI
//     imports a guest would need to reach beyond this sandbox.
func deterministicModuleConfig() wazero.ModuleConfig {
	return wazero.NewModuleConfig().
		WithWalltime(func() (sec int64, nsec int32) { return 0, 0 }, sys.ClockResolution(1)).
		WithSysNanotime().
		WithRandSource(zeroEntropyReader{}).
		WithStdout(io.Discard).
		WithStderr(io.Discard)
}

// Name returns the plugin's diagnostic identifier (the Name field of
// the PluginSpec it was loaded from).
func (p *Plugin) Name() string {
	return p.name
}

// Call invokes an exported function on the plugin. Returns the output
// bytes the guest wrote to its output buffer.
//
// On a guest panic that escapes Extism's normal error envelope, the
// panic-recovery wrap converts the panic into an error wrapping
// ErrWasmGuestPanic. The error message names the plugin and the
// invoked function.
//
// On a non-zero exit code from the guest function, returns an error
// wrapping ErrWasmPluginExited and carrying the exit code. A guest
// reaches a non-zero exit code by invoking WASI proc_exit; under the
// AOT-purity discipline exit code zero is the only "ran successfully"
// signal, so any non-zero code is an error the caller surfaces.
func (p *Plugin) Call(ctx context.Context, funcName string, input []byte) ([]byte, error) {
	return callWithPanicRecovery(p.name, funcName, func() ([]byte, error) {
		exitCode, output, err := p.plugin.CallWithContext(ctx, funcName, input)
		if err != nil {
			return nil, fmt.Errorf("wasm: plugin %q call to %s: %w", p.name, funcName, err)
		}
		if exitCode != 0 {
			return nil, fmt.Errorf("wasm: plugin %q call to %s invoked proc_exit(%d): %w",
				p.name, funcName, exitCode, ErrWasmPluginExited)
		}
		return output, nil
	})
}

// callWithPanicRecovery invokes f and converts any panic into an error
// wrapping ErrWasmGuestPanic. Extism translates most Wasm-side traps
// into ordinary Go errors; this wrap is the safety net behind that
// error path, catching a Go-side panic that escapes Extism's normal
// error envelope.
//
// Extracted from Plugin.Call so the wrap shape is independently
// testable without needing a real Wasm-side panic source — tests pass
// a function that panics directly.
func callWithPanicRecovery(pluginName, funcName string, f func() ([]byte, error)) (output []byte, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		err = fmt.Errorf("wasm: guest plugin %q panicked in %s: %v: %w",
			pluginName, funcName, r, ErrWasmGuestPanic)
	}()
	return f()
}

// Close releases the resources held by the underlying Extism plugin.
// Safe to call once; subsequent calls return the SDK's error if any.
// Callers SHOULD invoke Close when the plugin is no longer needed —
// typically deferred immediately after Load.
func (p *Plugin) Close(ctx context.Context) error {
	if p == nil || p.plugin == nil {
		return nil
	}
	return p.plugin.CloseWithContext(ctx)
}
