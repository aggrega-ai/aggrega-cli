// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package wasm is the sole construction site for Extism plugins.
// Per the project's loader-containment discipline, only this package
// imports github.com/extism/go-sdk and constructs *extism.Plugin
// values. Every consumer in the compiler — the publishing-linter, the
// future mutator / policy / helm-renderer / pipeline /
// linter sockets — receives a *Plugin constructed by Loader.Load and
// invokes it through Plugin.Call.
//
// The package's AOT-purity load gate has two layers. The manifest
// gate (see manifest_translate.go) rejects any plugin
// configuration that would let guest code reach beyond its in-memory
// inputs at run time: outbound network access via AllowedHosts,
// filesystem access via AllowedPaths, or host functions outside the
// curated allowlist (see hostfuncs.go). The import-module allowlist
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
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
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

// SourceParser is the seam a consumer socket's host runs to obtain a source's
// canonical structured tree through the host, without the consumer guest ever
// invoking a producer guest directly. Given the syntactic format, the source
// bytes, and an opaque options document, it resolves and runs the producer (the
// parser) and returns the tree, or an error the host surfaces in-band to the
// consuming guest. A nil options is passed through verbatim.
//
// The seam is host-mediated producer consumption made generic: the callback
// plumbing, injection, and the re-entrancy guard are producer- and
// consumer-agnostic, so any consumer socket reuses them with only a typed leaf.
// The parser is the sole producer leaf today. The type is declared here, on the
// consuming side, so package wasm depends on no concrete producer implementation
// (which would invert the dependency direction — a producer's orchestration
// imports this package); the consumer socket injects a closure wrapping the
// producer-resolution-and-run path. The ctx carries the producer-consumption
// depth: the host-function body increments it before invoking the seam so a
// producer that attempts a nested producer-consumption call is denied at the
// shared seam entry.
type SourceParser func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error)

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

	// compileDir, when non-empty, is the on-disk directory the shared compilation
	// cache writes compiled leaves under. It is set only when this Loader is
	// backed by a disk-shared cache; it lets a cold compile observe which leaves
	// it emits so a later reference-based reclamation can attribute them to the
	// guest that produced them. Empty — an in-memory cache or a bare Loader —
	// disables that observation entirely, keeping the load a pure compile.
	compileDir string

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
// output nor cross the render-hermeticity boundary — it is a local
// compiled-artifact cache, not an external runtime input.
//
// fetch serves the aggrega_fetch_external_input host function for every Plugin
// the Loader constructs (nil is safe — a guest fetch then receives the in-band
// not-available envelope). There is nothing to close: the shared cache is a
// process-lifetime singleton reclaimed at process exit, not owned by any
// individual caller.
func NewCachingLoader(fetch FetchService) *Loader {
	cache := sharedCompilationCache(false)
	return &Loader{ExternalInputs: fetch, Cache: cache, compileDir: sharedCompilationCacheDir}
}

// NewCachingLoaderFromContext is NewCachingLoader with the persistent-cache dial
// read from ctx (see ContextWithDisablePluginCache). The dial only affects the
// first construction in a process, where it selects the shared cache's backing
// store: a disable signal makes the process-wide cache in-memory-only, no signal
// leaves it disk-backed (the default). A process runs a single command's worth
// of that dial, so first-construction-wins matches real usage. Every later
// construction returns a Loader over that same shared cache regardless of ctx.
func NewCachingLoaderFromContext(ctx context.Context, fetch FetchService) *Loader {
	cache := sharedCompilationCache(DisablePluginCacheFromContext(ctx))
	return &Loader{ExternalInputs: fetch, Cache: cache, compileDir: sharedCompilationCacheDir}
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

// AggregaCacheRoot returns the user-level cache root the CLI's persistent caches
// live under and whether one could be resolved. It is the exported accessor over
// the single internal root spelling, so a sibling cache that lives alongside the
// compile cache and the plugin-describe cache — such as the plugin provenance
// index — locates the same parent without re-spelling the root itself. A false
// return means no user cache directory could be resolved.
func AggregaCacheRoot() (string, bool) {
	return aggregaCacheRoot()
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

// CompileCacheDir returns the directory of the persistent wazero
// compiled-module cache and whether one could be resolved. It exposes the
// internally-resolved location so cache-inspection and cache-maintenance
// commands enumerate and reclaim the same directory the loader compiles into,
// without re-deriving the path. A false return means no user cache directory
// could be resolved, so no persistent compile cache exists to act on.
func CompileCacheDir() (string, bool) {
	return wasmCompileCacheDir()
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

// producerConsumptionDepthLimit bounds host-mediated producer consumption to a
// single level. A consumer at depth 0 may drive a producer (which runs at depth
// 1 and returns to 0, so sequential consumption of many sources is fine), but a
// producer running at depth 1 that attempts a further producer-consumption call
// is denied. The bound is 1 because the mechanism has one producer leaf and no
// legitimate need to nest; a deeper chain is a cycle to halt loudly, not a
// workflow.
const producerConsumptionDepthLimit = 1

// producerConsumptionDepthContextKey keys the producer-consumption nesting depth
// on a context. The depth rides the context across the host-call boundary — the
// context an export is invoked with reaches the host-function body that runs the
// seam — so the re-entrancy guard needs no shared mutable state.
type producerConsumptionDepthContextKey struct{}

// producerConsumptionDepthFromContext reports the host-mediated
// producer-consumption nesting depth carried by ctx. The zero value — no key
// attached — is depth 0, a consumer that has not yet entered the seam.
func producerConsumptionDepthFromContext(ctx context.Context) int {
	depth, _ := ctx.Value(producerConsumptionDepthContextKey{}).(int)
	return depth
}

// contextWithProducerConsumptionDepth returns a context carrying depth for the
// downstream producer run, so a nested producer-consumption call the producer
// attempts observes the incremented depth and is denied at the shared seam
// entry. The seam-entry host body sets the incremented depth on the context it
// hands the seam, and the seam threads that context into the producer's export
// invocation.
func contextWithProducerConsumptionDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, producerConsumptionDepthContextKey{}, depth)
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
	// sourceParser is the producer-consumption seam the aggrega_parse_source host
	// function runs to obtain a source's canonical tree through the host. It is
	// injected per invocation through SetSourceParser, the same way sourceRoot is
	// injected, because one plugin instance may serve many invocations. The
	// host-function body reads it at call time; nil yields an in-band
	// not-available failure rather than a silent no-op.
	sourceParser SourceParser
	// stdout and stderr are the streams the CLI streaming host functions
	// (aggrega_cli_write_stdout / aggrega_cli_write_stderr) write guest output
	// to. They are set only on the runtime CLI profile, by Loader.LoadCLI; the
	// compile-time build profile (Loader.Load) leaves them nil and never binds
	// a streaming closure, so a build never streams. A nil writer is a safe
	// no-op the streaming body guards against.
	stdout io.Writer
	stderr io.Writer
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

// SetSourceParser injects the producer-consumption seam the aggrega_parse_source
// host function runs to obtain a source's canonical tree through the host. The
// consumer socket sets it immediately before invoking a consuming export, the
// same way SetSourceRoot injects the acquired source location, because one plugin
// instance may serve many invocations. A nil seam disables producer consumption:
// a parse-source request then receives an in-band not-available failure rather
// than reaching a producer.
func (p *Plugin) SetSourceParser(parse SourceParser) {
	p.sourceParser = parse
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
//     AOT-purity load gate against the manifest fields.
//  2. Read the plugin Wasm bytes from spec.Path once into memory.
//  3. Walk the bytes' import section and apply the import-module
//     allowlist: imports under a curated set of module namespaces pass;
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
// Reading the bytes once and threading the same slice through load-gate
// inspection, host-function derivation, and Extism instantiation
// eliminates the TOCTOU window between validation and execution.
// Returns an error wrapping ErrWasmManifestRejected if the load gate
// rejects the manifest or any import or a derived host function is
// outside the allowlist; the Extism SDK's error otherwise.
func (l *Loader) Load(ctx context.Context, spec PluginSpec) (*Plugin, error) {
	return l.loadCore(ctx, spec, validateHostFunctionsSubset, (*Plugin).hostFunctionsForDeclared, nil, nil)
}

// LoadCLI constructs a Plugin under the runtime CLI instantiation profile,
// binding the streaming host functions aggrega_cli_write_stdout and
// aggrega_cli_write_stderr to stdout and stderr. It is the terminal-facing
// sibling of Load: LoadCLI is for a CLI plugin run at the terminal, as a
// command executes — not inside a build.
//
// The two profiles are deliberately disjoint, so the streaming grant can never
// leak into a build (which must stay ahead-of-time deterministic and
// capability-denied, with no terminal to stream to):
//
//   - Separate allowlist. LoadCLI validates the guest's derived imports against
//     the UNION of the shared allowlist and the CLI-only allowlist, so a CLI
//     guest may import the streaming emitters. Load validates against the shared
//     allowlist ALONE, so a build-profile guest importing a streaming emitter is
//     rejected with an error wrapping ErrWasmManifestRejected before any host
//     function is bound.
//   - Separate builder. LoadCLI builds host-function closures through
//     hostFunctionsForDeclaredCLI, the only builder that constructs a streaming
//     closure; Load's builder has no streaming case.
//   - Separate entry point. The compiler's build pipeline calls Load /
//     LoadAndDescribe and never LoadCLI, so no build path reaches this profile.
//
// Everything else is shared with Load through loadCore, including the unchanged
// AOT-purity load gate (a CLI guest still may not declare AllowedHosts or
// AllowedPaths) and the deterministic, capability-denied ModuleConfig — WASI
// stdout/stderr stay discarded, because a CLI guest streams through the host
// functions, not through WASI stdio.
func (l *Loader) LoadCLI(ctx context.Context, spec PluginSpec, stdout, stderr io.Writer) (*Plugin, error) {
	return l.loadCore(ctx, spec, validateHostFunctionsSubsetCLI, (*Plugin).hostFunctionsForDeclaredCLI, stdout, stderr)
}

// loadForDescribe constructs a Plugin under the describe-only introspection
// profile: it binds inert host-function stubs in place of the real host
// functions and validates the guest's imports against the union allowlist, so a
// guest of any type — including a streaming CLI guest the build profile would
// reject before describe() could run — instantiates purely to be introspected.
//
// It is the third profile alongside Load (build) and LoadCLI (runtime CLI),
// sharing loadCore with them so the manifest check, byte read, import-module
// allowlist, host-function derivation, Extism instantiation, and compile
// serialization stay single-sourced. Only the two profile arguments differ:
//
//   - validateSubset is the union gate (validateHostFunctionsSubsetCLI), so any
//     plugin type's imports pass and any type is describable — the build
//     profile's strict shared gate, which rejects the streaming emitters, does
//     not apply to a surface that only needs the self-report.
//   - buildFunctions is the inert-stub builder (describeOnlyHostFunctions), so a
//     guest's describe() reaches only no-op stubs and gains no real host
//     capability during introspection.
//
// The streaming closures' writers are nil: the inert stubs stream nothing, so
// there is no terminal to write to. The deterministic, capability-denied
// ModuleConfig is unchanged.
func (l *Loader) loadForDescribe(ctx context.Context, spec PluginSpec) (*Plugin, error) {
	return l.loadCore(ctx, spec, validateHostFunctionsSubsetCLI, (*Plugin).describeOnlyHostFunctions, nil, nil)
}

// loadCore is the single shared body Load and LoadCLI both run, so the two
// entry points single-source the manifest gate, byte read, import-module allowlist,
// host-function derivation, Extism instantiation, and compile serialization
// rather than keeping parallel copies. The two profiles differ only in the
// arguments: validateSubset is the subset gate the derived host-function set is
// checked against (the shared allowlist for the build profile, the union for
// the CLI profile); buildFunctions is the builder that constructs the bound
// closures; stdout and stderr are the streams the CLI streaming closures write
// to (nil on the build profile, which binds no streaming closure).
//
// The flow mirrors Load's documented sequence: translate + manifest gate, read the
// bytes once, apply the import-module allowlist, derive the host functions from
// the bytes and check the derived set against validateSubset, then instantiate
// the Extism plugin from the same in-memory bytes under the deterministic,
// capability-denied ModuleConfig. Reading the bytes once and threading the same
// slice through load-gate inspection, derivation, and instantiation eliminates
// the TOCTOU window between validation and execution.
func (l *Loader) loadCore(
	ctx context.Context,
	spec PluginSpec,
	validateSubset func(name, typeLabel string, names []string) error,
	buildFunctions func(p *Plugin, declared []string) []extism.HostFunction,
	stdout, stderr io.Writer,
) (*Plugin, error) {
	manifest, err := translateToManifest(spec)
	if err != nil {
		return nil, err
	}

	// Bytes supplied directly (embedded / verified-cache content with no
	// stable filesystem location) win over Path; otherwise read Path once.
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
	// then verify the derived set against the profile's subset gate. The
	// derived set is authoritative — the .wasm import section is ground truth
	// for which host functions the guest calls — and is what the loader
	// registers.
	hostFunctions, err := DeriveHostFunctions(spec.Name, wasmBytes)
	if err != nil {
		return nil, err
	}
	if err := validateSubset(spec.Name, string(spec.PluginType), hostFunctions); err != nil {
		return nil, err
	}

	// Replace the manifest's WasmFile entry (which would have Extism
	// re-read from disk) with WasmData carrying the already-read
	// bytes. The load gate validated these specific bytes; Extism
	// will compile and instantiate the same bytes.
	manifest.Wasm = []extism.Wasm{
		extism.WasmData{Data: wasmBytes, Name: spec.Name},
	}

	// Allocate the wrapper before constructing the Extism plugin so
	// the per-plugin host-function closures can capture *Plugin and
	// thread guest-emitted warnings into the warnings slice. The
	// Extism plugin handle is attached to p after NewPlugin returns.
	// The loader's external-input service rides along so the
	// aggrega_fetch_external_input closure can serve guest fetches; the
	// CLI streams ride along so the streaming closures can emit to them.
	p := &Plugin{name: spec.Name, externalInputs: l.ExternalInputs, hostFunctions: hostFunctions, stdout: stdout, stderr: stderr}

	functions := buildFunctions(p, hostFunctions)

	config := extism.PluginConfig{
		EnableWasi:   true,
		ModuleConfig: deterministicModuleConfig(spec.programName()),
	}
	// A shared compilation cache memoizes the guest's compiled machine code so
	// repeated loads of the same bytes skip recompilation. It carries no
	// runtime authority and no observable behaviour of its own — it changes
	// only compilation cost — so it leaves the deterministic ModuleConfig and
	// the AOT-purity load gate untouched.
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
	guestDigest := digest.ContentDigest(wasmBytes)
	extismPlugin, err := l.compileGuest(ctx, guestDigest, func() (*extism.Plugin, error) {
		return extism.NewPlugin(ctx, manifest, config, functions)
	})
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

var (
	// attribObserveMu serializes in-process cold compile observations so two
	// concurrent cold compiles in THIS process cannot interleave their
	// snapshot-before / snapshot-after windows and cross-attribute each other's
	// leaves. It is taken OUTERMOST — strictly outside the per-guest compile lock,
	// never inside it — so the lock order (attribution ⊃ per-guest) is consistent
	// and deadlock-free. Cross-PROCESS concurrent cold compiles are not serialized
	// by this mutex; their confounded diffs degrade to unattributed (recorded as
	// nothing), never to a wrong attribution.
	attribObserveMu sync.Mutex

	// attribObserved memoizes the guest content digests already observed once in
	// this process. A digest in the memo is warm forever: the render hot path
	// (repeated warm loads) never snapshots and never takes attribObserveMu, so
	// attribution stays entirely off warm and hot loads.
	attribObserved sync.Map

	// attribSnapshotCount counts how many times the cold-observation path ran its
	// snapshot. It exists for tests to assert a warm reload does not re-observe.
	attribSnapshotCount int64
)

// compileGuest runs the guest's compile-and-instantiate under the per-guest
// compile lock, and on a cold, not-yet-observed first compile additionally
// records which compiled-module cache leaves that compile emitted, so a later
// reference-based reclamation can attribute each leaf to the guests that depend
// on it.
//
// Every attribution outcome is SAFE because compiled leaves are re-derivable:
// the worst case of any misattribution is an unnecessary recompile, never a
// broken build. The attribution mutex serializes only in-process cold
// observations; cross-process cold-compile races confound the leaf diff and
// degrade to unattributed (recorded as nothing, so the leaf is retained). The
// per-process memo keeps snapshotting and the attribution lock entirely off warm
// and hot loads — the steady state of a render is a warm reload that takes only
// the cheap per-guest lock and compiles nothing.
func (l *Loader) compileGuest(ctx context.Context, guestDigest string, compile func() (*extism.Plugin, error)) (*extism.Plugin, error) {
	_ = ctx
	// Zero-steady-state fast path: nothing to observe (no cache or no observable
	// compile dir), or this guest was already observed once in this process.
	if l.Cache == nil || l.compileDir == "" {
		return lockedCompile(l, guestDigest, compile)
	}
	if _, seen := attribObserved.Load(guestDigest); seen {
		return lockedCompile(l, guestDigest, compile)
	}

	attribObserveMu.Lock()
	before := listCompileLeaves(l.compileDir)
	plugin, err := lockedCompile(l, guestDigest, compile)
	after := listCompileLeaves(l.compileDir)
	// Warm forever regardless of outcome — a failed compile is not retried into
	// observation, and a successful one is observed exactly once.
	attribObserved.Store(guestDigest, struct{}{})
	atomic.AddInt64(&attribSnapshotCount, 1)
	attribObserveMu.Unlock()

	l.attributeCompileLeaves(guestDigest, before, after)
	return plugin, err
}

// lockedCompile takes the per-guest compile lock, runs compile, and releases the
// lock. It is the sole path that invokes the compile so the lock discipline is
// identical on the observed and unobserved branches.
func lockedCompile(l *Loader, guestDigest string, compile func() (*extism.Plugin, error)) (*extism.Plugin, error) {
	release := l.lockGuestCompile(guestDigest)
	defer release()
	return compile()
}

// attributeCompileLeaves records the leaves a cold compile emitted (the set
// present in after but not before) against guestDigest, using the single-leaf
// and two-leaf cold-compile emission shapes. It records NOTHING on any ambiguous
// diff, so a confounded concurrent cold compile leaves its leaves unattributed
// (and therefore retained) rather than misattributed. All index writes are
// best-effort — a write failure never breaks the load.
func (l *Loader) attributeCompileLeaves(guestDigest string, before, after map[string]int64) {
	var added []string
	for k := range after {
		if _, ok := before[k]; !ok {
			added = append(added, k)
		}
	}

	idxPath := attribindex.PathForCompileDir(l.compileDir)
	switch len(added) {
	case 0:
		// Warm on-disk hit: the guest's leaves were already compiled. Nothing to
		// attribute here — the guest was attributed on its own cold compile.
		return
	case 1:
		// A later distinct guest: its own single leaf (the shared kernel is
		// reused, not re-emitted). Attribute the leaf to this guest and add this
		// guest to the kernel's referrer set (a no-op until a kernel is recorded).
		leaf := added[0]
		_ = attribindex.RecordGuestLeaf(idxPath, leaf, after[leaf], guestDigest)
		_ = attribindex.AddKernelReferrer(idxPath, guestDigest)
	case 2:
		// A fresh cache's first cold compile emits two leaves: the guest plus the
		// one-time shared runtime kernel. Attribute only when a size guard proves
		// one is the tiny kernel and one is the guest, and only when no kernel is
		// recorded yet — a two-leaf diff with a kernel already known is a
		// confounded concurrent compile, not a first compile.
		if recorded, _ := attribindex.KernelRecorded(idxPath); recorded {
			return
		}
		a, b := added[0], added[1]
		sa, sb := after[a], after[b]
		small, large := a, b
		smallSize, largeSize := sa, sb
		if sb < sa {
			small, large = b, a
			smallSize, largeSize = sb, sa
		}
		// The kernel is orders of magnitude smaller than any guest. Require a wide
		// margin (>=4x) AND an absolute kernel ceiling, so an unexpected pair is
		// treated as ambiguous rather than guessed.
		if smallSize*4 <= largeSize && smallSize <= 512*1024 {
			_ = attribindex.RecordKernelLeaf(idxPath, small, smallSize, guestDigest)
			_ = attribindex.RecordGuestLeaf(idxPath, large, largeSize, guestDigest)
		}
	default:
		// More than two new leaves: a concurrent cross-process cold compile
		// confounded the diff. Record nothing; the leaves stay unattributed and so
		// are retained. Never guess.
	}
}

// listCompileLeaves lists the compiled-module cache leaves beneath dir as a map
// from leaf key (path relative to dir, forward-slashed) to on-disk byte size. It
// excludes the transient per-guest compile-lock sidecars and the half-written
// *.tmp staging files an in-flight compile leaves behind, so the snapshot diff
// captures only finished compiled modules.
//
// This exclusion mirrors the compiled-leaf membership test the cache-inspection
// command applies (cmd/aggrega isCompileLeaf); the two are kept as separate
// small predicates on either side of the package boundary. Keep them in step: a
// change to which files count as compiled leaves must land on both.
func listCompileLeaves(dir string) map[string]int64 {
	out := map[string]int64{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".compile-") && strings.HasSuffix(name, ".lock") {
			return nil
		}
		if strings.HasSuffix(name, ".tmp") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			out[filepath.ToSlash(rel)] = info.Size()
		}
		return nil
	})
	return out
}

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
//     so the load gate is untouched and the value is a build-invariant
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
	exitCode, output, err := p.callRaw(ctx, funcName, input)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		// Compile-time exit-code policy: exit code zero is the only "ran
		// successfully" signal, so any non-zero code is a failure the caller
		// surfaces. The runtime CLI runner deliberately applies the OPPOSITE
		// policy through InvokeExportStatus — a terminal command's non-zero
		// exit is an ordinary status, not an error. The two policies are a
		// deliberate near-duplicate; keep them distinct, do not unify them.
		return nil, fmt.Errorf("wasm: plugin %q call to %s invoked proc_exit(%d): %w",
			p.name, funcName, exitCode, ErrWasmPluginExited)
	}
	return output, nil
}

// callRaw invokes funcName on the guest and returns the raw exit code alongside
// the output, applying NO exit-code policy: a clean non-zero exit is reported
// through exitCode as a status, never folded into err. err is set only by a real
// Wasm trap (wrapped, naming the plugin and function) or a recovered guest panic
// (wrapping [ErrWasmGuestPanic]); on either, exitCode is zero and output nil.
//
// It is the shared call core behind both [Plugin.Call] — which layers the
// compile-time non-zero→error policy on top — and the runtime CLI runner's
// [InvokeExportStatus] — which reads the exit code as the command's status. One
// core keeps the trap wrap and the panic-recovery net single-sourced; the two
// callers differ only in how they treat a clean non-zero exit.
func (p *Plugin) callRaw(ctx context.Context, funcName string, input []byte) (exitCode uint32, output []byte, err error) {
	output, err = callWithPanicRecovery(p.name, funcName, func() ([]byte, error) {
		code, out, e := p.plugin.CallWithContext(ctx, funcName, input)
		if e != nil {
			return nil, fmt.Errorf("wasm: plugin %q call to %s: %w", p.name, funcName, e)
		}
		exitCode = code
		return out, nil
	})
	if err != nil {
		return 0, nil, err
	}
	return exitCode, output, nil
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
