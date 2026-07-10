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
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
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
	// Plugin instance. Constructed via NewCachingLoader.
	Cache wazero.CompilationCache

	// OnLoad, when non-nil, is invoked with the plugin's name each time this
	// Loader successfully constructs a Plugin. It is a diagnostic observation
	// hook only: it carries no authority over the load and cannot alter rendered
	// output — it exists to let a progress surface report plugin-compilation
	// activity as guests are constructed. Nil — the zero-value Loader — skips the
	// call.
	OnLoad func(pluginName string)

	// OnDescribe, when non-nil, is invoked with the plugin's name each time this
	// Loader successfully describes a guest through LoadAndDescribe — that is, each
	// time the guest's describe() export is called and its descriptor validated. Like
	// OnLoad it is a diagnostic observation hook only: it carries no authority and
	// cannot alter rendered output. It exists to observe how often the describe gate
	// runs — a caller that memoizes a descriptor across many per-node loads expects
	// it to fire once per provider, not once per node. Nil — the zero-value Loader —
	// skips the call.
	OnDescribe func(pluginName string)
}

// NewCachingLoader returns a fresh Loader backed by the process-wide shared
// compilation cache (see sharedCompilationCache).
//
// Every Loader in the process shares one wazero CompilationCache, so a given
// guest compiles once per process and every later load — across any build,
// render step, socket, or served request — reuses the cached machine code.
// Each load still runs the full load-describe-invoke-close cycle against a
// fresh, isolated Plugin instance: the cache changes only compilation cost,
// never isolation or output, since compiled code is a pure function of the
// identical guest bytes. The store holds only compiled code keyed by the guest
// bytes and a wazero version/arch marker, so it can neither alter rendered
// output nor cross the render-hermeticity firewall — it is a local
// compiled-artifact cache, not an external runtime input.
//
// fetch serves the aggrega_fetch_external_input host function for every Plugin
// the Loader constructs (nil is safe — a guest fetch then receives the in-band
// not-available envelope). There is nothing to close: the shared cache is a
// process-lifetime singleton reclaimed at process exit, not owned by any
// individual caller.
func NewCachingLoader(fetch FetchService) *Loader {
	return &Loader{ExternalInputs: fetch, Cache: sharedCompilationCache(false)}
}

// NewCachingLoaderFromContext is NewCachingLoader with the persistent-cache dial
// read from ctx (see ContextWithDisablePluginCache). The dial only affects the
// first construction in a process, where it selects the shared cache's backing
// store: a disable signal makes the process-wide cache in-memory-only, no signal
// leaves it disk-backed (the default). A process runs a single command's worth
// of that dial, so first-construction-wins matches real usage. Every later
// construction returns a Loader over that same shared cache regardless of ctx.
func NewCachingLoaderFromContext(ctx context.Context, fetch FetchService) *Loader {
	return &Loader{ExternalInputs: fetch, Cache: sharedCompilationCache(DisablePluginCacheFromContext(ctx))}
}

// sharedCompilationCache returns the one process-wide wazero compilation cache
// every Loader in the process shares. It is constructed once, on first use, and
// lives for the whole process; it is never closed, as the process reclaims it at
// exit.
//
// Sharing one cache across every loader — every build, render step, socket, and
// served request — means each distinct guest compiles once per process and all
// later loads reuse the machine code, and no loader ever runs against a private
// or absent cache that would recompile the same guest and contend with the rest.
//
// The selection is made once, at first construction:
//
//   - Within a test binary (testing.Testing() reports true) the cache is an
//     on-disk store rooted at a fixed directory under the system temp directory
//     (see testCompilationCacheDir), regardless of disablePersistent. Every test
//     process — including the many package binaries a single suite run executes
//     in parallel — shares that one directory, so each distinct guest compiles at
//     most once across the whole run and every later process reuses the machine
//     code instead of cold-compiling its own copy. The directory is deliberately
//     off the user cache directory, so the suite never touches a developer's real
//     build cache. An in-memory cache is the fallback only when that directory
//     cannot be created.
//   - Otherwise selectCompilationCache honors disablePersistent: false (the
//     default) prefers the persistent on-disk store under wasmCompileCacheDir
//     with an in-memory fallback; true uses an in-memory-only cache. Only the
//     first caller's disablePersistent is consulted, which matches real usage: a
//     process runs one CLI command's worth of that dial, or the daemon's
//     disk-backed default.
//
// Sharing one cache changes only compilation cost, never isolation or output;
// each load still yields a fresh, isolated Plugin. Concurrent compilation of the
// SAME guest against a shared cache can race, though — two loads that both find
// the cache cold may compile at once and one can observe a torn, zero-function
// module — so Load serializes same-guest compilation: an in-process per-guest
// lock always, plus a cross-process file lock when the cache is a shared
// on-disk directory. Different guests still compile in parallel. The
// serialization changes only when compilation happens, never the compiled code
// or the rendered bytes.
//
// When the resolved cache is disk-backed, the on-disk directory is recorded in
// sharedCompilationCacheDir so Load's cross-process lock can place its per-guest
// lock file there; an in-memory cache leaves it empty and Load takes only the
// in-process lock.
func sharedCompilationCache(disablePersistent bool) wazero.CompilationCache {
	sharedCompilationCacheOnce.Do(func() {
		if testing.Testing() {
			sharedCompilationCacheInstance, sharedCompilationCacheDir = testCompilationCache()
			return
		}
		sharedCompilationCacheInstance, sharedCompilationCacheDir = selectCompilationCacheWithDir(disablePersistent)
	})
	return sharedCompilationCacheInstance
}

// testCompilationCacheDir returns the fixed directory that test binaries share
// as their compile cache. It is a stable location under the system temp
// directory — the same path for every test process, so a whole suite run
// compiles each guest once and reuses it — and deliberately distinct from the
// user cache directory selectCompilationCache uses, so tests never touch a
// developer's real build cache.
func testCompilationCacheDir() string {
	return filepath.Join(os.TempDir(), "aggrega-wasm-compile-test")
}

// testCompilationCache returns the shared on-disk cache test binaries use,
// rooted at testCompilationCacheDir, falling back to an in-memory cache only
// when that directory cannot be created. The second return is the on-disk
// directory backing the cache, or "" when the fallback in-memory cache is used;
// callers record it so the cross-process compile lock can locate its per-guest
// lock files.
func testCompilationCache() (wazero.CompilationCache, string) {
	dir := testCompilationCacheDir()
	if disk, err := wazero.NewCompilationCacheWithDir(dir); err == nil {
		return disk, dir
	}
	return wazero.NewCompilationCache(), ""
}

var (
	sharedCompilationCacheOnce     sync.Once
	sharedCompilationCacheInstance wazero.CompilationCache
	// sharedCompilationCacheDir is the on-disk directory backing the process-wide
	// shared compilation cache, or "" when that cache is in-memory. It is written
	// exactly once, inside sharedCompilationCacheOnce.Do, so every later read
	// (Load's cross-process compile lock) observes it through the Once's
	// happens-before. It is the single source of truth for the cache directory:
	// the same path passed to wazero.NewCompilationCacheWithDir, never recomputed.
	sharedCompilationCacheDir string
)

// selectCompilationCache is the disk-vs-memory primitive behind
// sharedCompilationCache's non-test branch: with disablePersistent false it
// prefers the persistent on-disk store under wasmCompileCacheDir and falls back
// to an in-memory cache when no cache directory resolves or the disk store
// cannot be created; with disablePersistent true it returns an in-memory-only
// cache.
//
// Every shipping path reaches this primitive through sharedCompilationCache. The
// sole direct callers are the disk-branch unit tests, which must exercise the
// on-disk selection that sharedCompilationCache's test-mode gate would otherwise
// mask. That deliberate bypass is the only sanctioned way to reach this
// primitive without the gate; it must not be "simplified" to route through
// sharedCompilationCache, which would blind the disk-branch coverage.
func selectCompilationCache(disablePersistent bool) wazero.CompilationCache {
	cache, _ := selectCompilationCacheWithDir(disablePersistent)
	return cache
}

// selectCompilationCacheWithDir is selectCompilationCache's primitive: it makes
// the same disk-vs-memory choice and additionally returns the on-disk directory
// backing the cache, or "" when the in-memory fallback is used. It is the single
// site that decides the cache directory; sharedCompilationCache records the
// returned directory so Load's cross-process compile lock can locate its
// per-guest lock files without re-deriving the selection.
func selectCompilationCacheWithDir(disablePersistent bool) (wazero.CompilationCache, string) {
	if !disablePersistent {
		if dir, ok := wasmCompileCacheDir(); ok {
			if disk, err := wazero.NewCompilationCacheWithDir(dir); err == nil {
				return disk, dir
			}
		}
	}
	return wazero.NewCompilationCache(), ""
}

// aggregaCacheRoot returns the user-level cache root the CLI's persistent caches
// live under and whether one could be resolved. It is the single site that names
// the root directory, so the compile cache and the plugin-describe cache share
// one parent rather than each re-spelling it. A false return (no resolvable user
// cache directory) signals the caller to fall back to a non-persistent path
// rather than fail.
func aggregaCacheRoot() (string, bool) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(base, "aggrega"), true
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
	root, ok := aggregaCacheRoot()
	if !ok {
		return "", false
	}
	return filepath.Join(root, "wasm-compile"), true
}

// PluginDescribeCacheDir returns the directory for the persistent
// plugin-describe cache and whether one could be resolved. A guest's describe()
// output is a static, content-addressed self-report, so the cache keys each
// descriptor on the resolved guest's content digest and lives alongside the
// compile cache under the shared user-level aggrega root: a configured plugin is
// described once and re-read from cache on later runs, and a change to the
// resolved artifact yields a new digest and so a fresh entry. Under a test
// binary the directory is a fixed path under the system temp directory rather
// than the developer's real cache, so the suite never touches it. A false return
// signals the caller to skip the cache and describe the guest directly.
func PluginDescribeCacheDir() (string, bool) {
	if testing.Testing() {
		return filepath.Join(os.TempDir(), "aggrega-plugin-describe-test"), true
	}
	root, ok := aggregaCacheRoot()
	if !ok {
		return "", false
	}
	return filepath.Join(root, "plugin-describe"), true
}

// PluginCompileCacheCold reports a coarse, up-front guess of whether a build
// using ctx will compile a provider guest rather than serve it from the cache.
// It is used only to pick the progress bar's cold or warm weight profile before
// rendering starts. It is true when the persistent on-disk cache is disabled for
// this build (see ContextWithDisablePluginCache — the in-memory cache then starts
// empty), when no persistent cache directory can be resolved (an in-memory cache
// is used), or when the persistent cache directory does not yet exist or holds no
// entries.
//
// The check reads only the presence of cache entries, not which specific guests
// are cached, so a populated directory guesses warm even if it lacks the exact
// guest this build needs. That imprecision is confined to bar weighting, which
// tolerates residual error; the visible "compiling" step is not gated on this
// guess but on whether a guest is observed to actually compile during the build.
func PluginCompileCacheCold(ctx context.Context) bool {
	if DisablePluginCacheFromContext(ctx) {
		return true
	}
	dir, ok := wasmCompileCacheDir()
	if !ok {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return true
	}
	return false
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

// disablePluginCacheContextKey keys the disable-persistent-cache dial on a
// context for the Loader construction site, which receives its per-build
// choices only through the context it is called with.
type disablePluginCacheContextKey struct{}

// ContextWithDisablePluginCache returns a context that, when disable is true,
// signals downstream caching-Loader construction to skip the persistent on-disk
// compile cache and use an in-memory-only cache for this build. Passing false
// leaves the default disk-backed behaviour in place. It mirrors
// ContextWithExternalInputs for call paths — the render pipeline's per-step
// plugin dispatch — whose only shared plumbing to the Loader is the context.
func ContextWithDisablePluginCache(ctx context.Context, disable bool) context.Context {
	return context.WithValue(ctx, disablePluginCacheContextKey{}, disable)
}

// DisablePluginCacheFromContext reports whether ctx signals that the persistent
// on-disk compile cache should be skipped in favour of an in-memory-only cache.
// The zero value — no signal attached — is false, preserving the default
// disk-backed behaviour.
func DisablePluginCacheFromContext(ctx context.Context) bool {
	disable, _ := ctx.Value(disablePluginCacheContextKey{}).(bool)
	return disable
}

// verifyDeterminismContextKey keys the render-twice-and-compare dial on a context
// for the render crossing, which receives its per-build choices only through the
// context it is invoked with.
type verifyDeterminismContextKey struct{}

// ContextWithVerifyDeterminism returns a context that, when verify is true,
// signals the render crossing to render each node a second time and compare the
// emitted bytes, halting the build on any divergence. Passing false leaves the
// default single-render behaviour in place. It mirrors ContextWithDisablePluginCache
// for call paths — the render pipeline's per-node dispatch — whose only shared
// plumbing to the render crossing is the context.
func ContextWithVerifyDeterminism(ctx context.Context, verify bool) context.Context {
	return context.WithValue(ctx, verifyDeterminismContextKey{}, verify)
}

// VerifyDeterminismFromContext reports whether ctx signals that each render
// crossing should render twice and compare the emitted bytes. The zero value —
// no signal attached — is false, preserving the default single-render behaviour.
func VerifyDeterminismFromContext(ctx context.Context) bool {
	verify, _ := ctx.Value(verifyDeterminismContextKey{}).(bool)
	return verify
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
	// hostFunctions is the host-function set derived from this guest's
	// .wasm import section at load — canonical (sorted, de-duplicated) and,
	// because Load admits only allowlisted imports, wholly within the
	// allowlist. The load gate cross-checks it against the descriptor's
	// declared host_functions so describe() cannot drift from the binary.
	hostFunctions []string
	// sourceRoot is the acquired source location the pack-source and
	// read-source-file host functions resolve a guest's selection under. It
	// varies per node — a single plugin instance may render many nodes, each
	// with its own acquired source — so the caller sets it through
	// SetSourceRoot immediately before each export invocation rather than at
	// load. The host-function closures read it at call time, so a stale value
	// from a prior node never leaks into a later one.
	sourceRoot string
}

// SetSourceRoot sets the acquired source location the pack-source and
// read-source-file host functions resolve a guest's selection under, for the
// next export invocation. The caller sets it immediately before invoking a
// sourced export and re-sets it per node, because one plugin instance may
// render many nodes, each with its own acquired source. An empty root disables
// source packing: a pack or read request then receives an in-band
// not-available error rather than resolving against a stale or absent root.
func (p *Plugin) SetSourceRoot(root string) {
	p.sourceRoot = root
}

// HostFunctions returns the host functions this guest imports from its .wasm,
// as derived at load. The returned slice is a defensive copy in canonical
// order (sorted, de-duplicated); because the loader admits only allowlisted
// imports, every name is in the host-function allowlist. The load gate
// cross-checks this against the descriptor's declared host_functions.
func (p *Plugin) HostFunctions() []string {
	if len(p.hostFunctions) == 0 {
		return nil
	}
	out := make([]string, len(p.hostFunctions))
	copy(out, p.hostFunctions)
	return out
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
	p := &Plugin{name: spec.Name, externalInputs: l.ExternalInputs, hostFunctions: hostFunctions}

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

	// Serialize concurrent compilation of THIS guest. NewPlugin compiles the
	// guest on a cold cache; two concurrent cold loads of the same guest against
	// the shared cache can race and one can observe a torn, zero-function module.
	// Keying the lock on the guest's content digest lets different guests still
	// compile in parallel; only same-guest loads take turns (first compiles and
	// warms the cache, the rest hit the warm cache). This changes only when
	// compilation happens, never the compiled code or rendered bytes.
	releaseCompile := l.lockGuestCompile(digest.ContentDigest(wasmBytes))
	extismPlugin, err := extism.NewPlugin(ctx, manifest, config, functions)
	releaseCompile()
	if err != nil {
		return nil, fmt.Errorf("wasm: load plugin %q: %w", spec.Name, err)
	}

	p.plugin = extismPlugin
	if l.OnLoad != nil {
		l.OnLoad(spec.Name)
	}
	return p, nil
}

// compileLocks maps a guest content digest to the *sync.Mutex that serializes
// in-process compilation of that guest. Distinct guests get distinct mutexes, so
// they compile in parallel; concurrent loads of one guest contend on its single
// mutex.
var compileLocks sync.Map

// compileMutex returns the process-wide mutex for key, creating it on first use.
func compileMutex(key string) *sync.Mutex {
	m, _ := compileLocks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// lockGuestCompile serializes compilation of the guest identified by key (its
// content digest) and returns a release function the caller invokes once
// compilation/instantiation has returned. It always takes an in-process
// per-guest mutex; when this Loader is backed by a shared on-disk compilation
// cache it also takes an exclusive cross-process file lock over a per-guest lock
// file in that cache directory, so parallel processes sharing the directory do
// not compile the same guest into a torn cache entry at once.
//
// The two locks are taken in a fixed order — in-process mutex first, then the
// file lock — and released in reverse, so concurrent callers cannot deadlock.
// The file lock is best-effort: if the lock file cannot be opened or locked the
// in-process mutex still serializes same-guest compilation within this process.
func (l *Loader) lockGuestCompile(key string) func() {
	mu := compileMutex(key)
	mu.Lock()

	// A cross-process lock is only meaningful when this Loader shares a real
	// on-disk cache directory with other processes; an in-memory cache is
	// process-private and the in-process mutex alone suffices.
	dir := sharedCompilationCacheDir
	if l.Cache == nil || dir == "" {
		return mu.Unlock
	}

	releaseFile := flockGuestCompile(dir, key)
	return func() {
		releaseFile()
		mu.Unlock()
	}
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
