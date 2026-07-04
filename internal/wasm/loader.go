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
// curated allowlist (see hostfuncs.go). The import-module firewall
// (see wasi_imports.go) walks the plugin's Wasm import section and
// rejects any import whose declaring module is outside a curated set of
// namespaces (the WASI preview-1 runtime, the Extism runtime ABI, and
// Aggrega's curated host functions). It does not censor individual WASI
// function names: a statically-linked language runtime imports
// filesystem and socket functions its live code paths never call, and
// naming an import is not exercising a capability.
//
// WASI itself is enabled, but configured as a capability-denied
// sandbox: the loader hands the Extism runtime a wazero ModuleConfig
// with frozen observable wall time (pinned to a build-invariant
// instant), real monotonic time (the guest language runtime needs an
// advancing monotonic clock to bootstrap), real host entropy, discarded
// stdio, no filesystem or environment passthrough, and a single
// synthetic program name as argv. The guest bootstraps against real
// WASI behaviour with zero ambient authority — no preopened filesystem,
// no sockets, an empty environment — so any I/O call fails at the call
// boundary. Wall time is frozen so a guest reading the clock cannot leak
// build time into its output; entropy and monotonic time are
// deliberately NOT pinned, so a guest that encodes nondeterminism into
// its output produces visibly differing bytes across runs rather than
// a stably-masked result. This preserves Aggrega's
// determinism and ahead-of-time guarantees regardless of which plugin
// runs.
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
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"
)

// FetchService supplies external-input bytes to guests that import the
// aggrega_fetch_external_input host function. Fetch returns the bytes
// for url, recording requiredBy (the requesting plugin's name) as
// provenance; a fetch the service refuses — a disallowed scheme, an
// unpinned URL in a frozen closure — returns an error whose message
// names the URL and the remedy.
//
// The interface is declared here, on the consuming side, so this
// package depends on no concrete acquisition implementation; the
// compiler's external-input store satisfies it.
type FetchService interface {
	Fetch(ctx context.Context, url string, requiredBy string) ([]byte, error)
}

// Loader is the package's entry point for constructing a Plugin from a
// PluginSpec. A zero-value Loader is ready to use; callers SHOULD reuse
// a single Loader across the life of the compiler process so that any
// future runtime-configuration knobs the loader picks up (Wazero
// runtime caches, observability adapters, and so on) amortise across
// plugin instances.
type Loader struct {
	// ExternalInputs, when non-nil, serves the
	// aggrega_fetch_external_input host function for every Plugin this
	// Loader constructs. Nil — the zero-value Loader — keeps every load
	// working: a guest fetch then receives an in-band error envelope
	// stating that external inputs are not available in this context.
	ExternalInputs FetchService

	// Cache, when non-nil, memoizes compiled guest machine code across
	// every Plugin this Loader constructs, so the expensive compilation of
	// a large guest amortizes over a build's many per-node loads. Nil — the
	// zero-value Loader — leaves the runtime at its default configuration,
	// compiling each load independently, exactly as before. The cache
	// memoizes only compiled code, a pure function of the guest bytes, so it
	// cannot alter output and each load still yields a fresh, isolated
	// Plugin instance. Constructed via NewCachingLoader, which pairs it with
	// a close function.
	Cache wazero.CompilationCache
}

// NewCachingLoader returns a Loader whose constructed Plugins share a single
// wazero CompilationCache, plus a close function that releases it. The shared
// cache amortizes the compilation of a large guest across a build's many
// per-node loads: the first load compiles the guest, and later loads of the
// same bytes reuse the cached machine code rather than recompiling. Each load
// still runs the full load-describe-invoke-close cycle against a fresh,
// isolated Plugin instance — the cache changes only compilation cost, never
// isolation or output, since compiled code is a pure function of the identical
// guest bytes.
//
// The cache is disk-backed under the user cache directory
// (see wasmCompileCacheDir), so compiled guest machine code survives across
// separate CLI invocations: the guest is compiled once per machine and reused
// on every later build of any project, rather than recompiled from scratch
// each run. The store holds only compiled code keyed by the guest bytes and a
// wazero version/arch marker, so it can neither alter rendered output nor cross
// the render-hermeticity firewall — it is a local compiled-artifact cache, not
// an external runtime input. When a disk-backed cache cannot be created (no
// resolvable user cache directory, or the directory cannot be provisioned), the
// loader falls back to an in-memory cache so a build never fails for want of a
// cache location.
//
// fetch serves the aggrega_fetch_external_input host function for every Plugin
// the Loader constructs (nil is safe — a guest fetch then receives the in-band
// not-available envelope). The caller MUST invoke the returned close function
// when the Loader is no longer needed, typically deferred at the construction
// site; it releases the cache's resources.
func NewCachingLoader(fetch FetchService) (*Loader, func() error) {
	var cache wazero.CompilationCache
	if dir, ok := wasmCompileCacheDir(); ok {
		if disk, err := wazero.NewCompilationCacheWithDir(dir); err == nil {
			cache = disk
		}
	}
	if cache == nil {
		cache = wazero.NewCompilationCache()
	}
	l := &Loader{ExternalInputs: fetch, Cache: cache}
	return l, func() error { return cache.Close(context.Background()) }
}

// wasmCompileCacheDir returns the directory for the persistent wazero
// compilation cache and whether one could be resolved. The provider guests are
// identical bytes across every project, so the cache is rooted at the
// user-level cache directory rather than a per-project one: the first build on
// a machine pays the guest's compilation cost, and every later build of any
// project reuses it. wazero namespaces its entries by a version+arch+os marker
// beneath this directory, so an incompatible cache is ignored, not misread. A
// false return (no resolvable user cache directory) signals the caller to fall
// back to an in-memory cache rather than fail the build.
func wasmCompileCacheDir() (string, bool) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(base, "aggrega", "wasm-compile"), true
}

// externalInputsContextKey keys the external-input service on a
// context for construction sites that receive their per-build services
// only through the context they are called with.
type externalInputsContextKey struct{}

// ContextWithExternalInputs returns a context carrying svc for
// downstream Loader construction sites. It exists for call paths — the
// render pipeline's per-step plugin dispatch — whose only shared
// plumbing between the command layer and the Loader construction site
// is the context; callers that construct a Loader directly set its
// ExternalInputs field instead.
func ContextWithExternalInputs(ctx context.Context, svc FetchService) context.Context {
	return context.WithValue(ctx, externalInputsContextKey{}, svc)
}

// ExternalInputsFromContext returns the external-input service carried
// by ctx, or nil when none is attached — the nil is safe to assign to
// Loader.ExternalInputs (guest fetches then receive the in-band
// not-available envelope).
func ExternalInputsFromContext(ctx context.Context) FetchService {
	svc, _ := ctx.Value(externalInputsContextKey{}).(FetchService)
	return svc
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
	// externalInputs serves the aggrega_fetch_external_input host
	// function; copied from the constructing Loader's ExternalInputs
	// field. Nil yields in-band not-available error envelopes.
	externalInputs FetchService
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
//  3. Walk the bytes' import section and apply the import-module
//     firewall: imports under a curated set of module namespaces pass;
//     an import under any other module is rejected with
//     ErrWasmManifestRejected.
//  4. Derive the host functions the guest actually imports from the
//     same byte slice and verify the derived set is a subset of the
//     curated allowlist. The .wasm import section is ground truth; no
//     authored list participates.
//  5. Register the derived host functions (aggrega_log,
//     aggrega_emit_warning, aggrega_fetch_external_input, and future
//     per-socket host functions); the derived set, not an authored
//     list, is what the plugin's bound surface is built from.
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
// outside the allowlist; the Extism SDK's error otherwise.
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

	if err := validateImportModules(spec.Name, wasmBytes); err != nil {
		return nil, err
	}

	// Derive the host functions the guest actually imports from the bytes,
	// then verify the derived set against the curated allowlist by reusing
	// the loader's single subset gate. The derived set is authoritative —
	// the .wasm import section is ground truth for which host functions the
	// guest calls — and is what the loader registers.
	hostFunctions, err := DeriveHostFunctions(spec.Name, wasmBytes)
	if err != nil {
		return nil, err
	}
	if err := validateHostFunctionsSubset(spec.Name, hostFunctions); err != nil {
		return nil, err
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
	// The loader's external-input service rides along so the
	// aggrega_fetch_external_input closure can serve guest fetches.
	p := &Plugin{name: spec.Name, externalInputs: l.ExternalInputs}

	functions := p.hostFunctionsForDeclared(hostFunctions)

	config := extism.PluginConfig{
		EnableWasi:   true,
		ModuleConfig: deterministicModuleConfig(spec.programName()),
	}
	// A shared compilation cache memoizes the guest's compiled machine code so
	// repeated loads of the same bytes skip recompilation. It carries no
	// runtime authority and no observable behaviour of its own — it changes
	// only compilation cost — so it leaves the deterministic ModuleConfig and
	// the AOT-purity firewall untouched.
	if l.Cache != nil {
		config.RuntimeConfig = wazero.NewRuntimeConfig().WithCompilationCache(l.Cache)
	}

	extismPlugin, err := extism.NewPlugin(ctx, manifest, config, functions)
	if err != nil {
		return nil, fmt.Errorf("wasm: load plugin %q: %w", spec.Name, err)
	}

	p.plugin = extismPlugin
	return p, nil
}

// deterministicModuleConfig returns the wazero ModuleConfig the loader
// hands the Extism runtime when instantiating a guest. The config
// enables real WASI behaviour (the guest language runtime bootstraps
// against it) while granting zero ambient authority, so any I/O call
// the guest attempts fails at the call boundary:
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
//     determinism violation that surfaces as differing output rather
//     than being prevented by construction.
//   - WithRandSource(rand.Reader): REAL host entropy. Pinning entropy
//     to a fixed source would stabilise the guest language runtime's
//     internal hash seed and map-iteration order, which would MASK
//     guest nondeterminism instead of letting it surface as differing
//     output. Real entropy paired with the frozen wall clock is the
//     correct sandbox: capability-denied yet nondeterminism-revealing.
//     A guest whose output depends on entropy is an authoring-level
//     determinism violation this configuration deliberately surfaces
//     rather than hides.
//   - WithStdout/WithStderr(io.Discard): the guest's stdio is
//     discarded; nothing the guest writes reaches the host's streams.
//   - No FSConfig, no environment: wazero's defaults deny the filesystem
//     and present an empty environment, so the guest reads nothing from
//     the host process. With no preopened directories and no sockets,
//     any filesystem or network WASI call a guest makes fails at the
//     call boundary — capability denial, not import censorship, is what
//     guarantees no I/O.
//   - WithArgs(programName): a single constant, capability-free program
//     name as the guest's argv. A transitive dependency's package init may
//     read os.Args[0] and panic on an empty argv; supplying a constant
//     avoids that panic. WASI argv is data, not a host-function import,
//     so the firewall is untouched and the value is a build-invariant
//     constant.
func deterministicModuleConfig(programName string) wazero.ModuleConfig {
	return wazero.NewModuleConfig().
		WithWalltime(func() (sec int64, nsec int32) { return 0, 0 }, sys.ClockResolution(1)).
		WithSysNanotime().
		WithRandSource(rand.Reader).
		WithStdout(io.Discard).
		WithStderr(io.Discard).
		WithArgs(programName)
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
