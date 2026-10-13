// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package wasm is the sole construction site for Extism plugins.
// Per the project's loader-containment discipline, only this package
// imports github.com/extism/go-sdk and constructs *extism.Plugin
// values. Every consumer in the compiler — the publishing-linter, the
// future mutator / policy / helm-renderer / pipeline /
// linter sockets — receives a *Plugin constructed by Loader.Load and
// invokes it through InvokeExport, which accepts only an export the
// plugin-type registry lists, or through the entry points for an export
// authored content names.
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
// The plugin's call core wraps the Extism call boundary with a deferred recover()
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/attribindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
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
	// so each load compiles the guest on a runtime of its own. The cache
	// memoizes only compiled code, a pure function of the guest bytes, so it
	// cannot alter output and each load still yields a fresh, isolated
	// Plugin instance. A non-nil cache also lets every load of a guest in the
	// process instantiate one compiled form, built on its first load, rather
	// than compiling and wiring a runtime per load. Constructed via
	// NewCachingLoader.
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
//     on-disk store at CompileCacheDir regardless of disablePersistent. Every
//     test process — including the many package binaries a single suite run
//     executes in parallel — shares that one directory, so each distinct guest
//     compiles at most once across the whole run and every later process reuses
//     the machine code instead of cold-compiling its own copy. An in-memory cache
//     is the fallback only when that directory cannot be created. This branch
//     decides only disk-vs-memory; the directory itself is resolved by cacheRoot,
//     which is what keeps it off a developer's real build cache.
//   - Otherwise selectCompilationCacheWithDir honors disablePersistent: false
//     (the default) prefers the persistent on-disk store at CompileCacheDir with
//     an in-memory fallback; true uses an in-memory-only cache. Only the first
//     caller's disablePersistent is consulted, which matches real usage: a
//     process runs one CLI command's worth of that dial, or the daemon's
//     disk-backed default.
//
// Sharing one cache changes only compilation cost, never isolation or output;
// each load still yields a fresh, isolated Plugin. The first build of a guest's
// compiled form in the process runs alone with respect to every other build that
// could read what it is publishing — see firstLoadGate, which records the
// runtime behaviour that requires it — and takes a cross-process file lock when
// the cache is a shared on-disk directory, so processes do not compile the same
// guest at once. Different guests still compile in parallel, and a load that
// reuses a built compiled form takes no lock at all. None of this changes the
// compiled code or the rendered bytes.
//
// When the resolved cache is disk-backed, the on-disk directory is recorded in
// sharedCompilationCacheDir, and every Loader NewCachingLoader constructs carries
// it, so the cross-process lock can place its per-guest lock file there; an
// in-memory cache leaves it empty and a build takes no file lock.
//
// A disk-backed cache is also held to its size cap here, once, before this
// process compiles or reads anything. That placement is what keeps the cap off
// the compile path — see reclaimCompileCacheAtStartup, which records why this is
// the safe moment to remove an entry from a directory other processes are using.
func sharedCompilationCache(disablePersistent bool) wazero.CompilationCache {
	sharedCompilationCacheOnce.Do(func() {
		if testing.Testing() {
			sharedCompilationCacheInstance, sharedCompilationCacheDir = testCompilationCache()
		} else {
			sharedCompilationCacheInstance, sharedCompilationCacheDir = selectCompilationCacheWithDir(disablePersistent)
		}
		reclaimCompileCacheAtStartup(sharedCompilationCacheDir)
	})
	return sharedCompilationCacheInstance
}

// testCompilationCache returns the shared on-disk cache test binaries use, at the
// compile-cache directory every other caller resolves (CompileCacheDir), falling
// back to an in-memory cache only when that directory cannot be created. The
// second return is the on-disk directory backing the cache, or "" when the
// fallback in-memory cache is used; callers record it so the cross-process compile
// lock can locate its per-guest lock files.
//
// It exists as its own function only because a test binary always takes the disk
// branch regardless of the disable-persistent dial (see sharedCompilationCache);
// it decides nothing about *where* the cache lives, which is cacheRoot's answer
// alone.
func testCompilationCache() (wazero.CompilationCache, string) {
	dir, ok := CompileCacheDir()
	if !ok {
		return wazero.NewCompilationCache(), ""
	}
	return compilationCacheAt(dir)
}

var (
	sharedCompilationCacheOnce     sync.Once
	sharedCompilationCacheInstance wazero.CompilationCache
	// sharedCompilationCacheDir is the on-disk directory backing the process-wide
	// shared compilation cache, or "" when that cache is in-memory. It is written
	// exactly once, inside sharedCompilationCacheOnce.Do, so every later read
	// (the compile directory each caching Loader carries) observes it through the
	// Once's happens-before. It is the single source of truth for the cache
	// directory: the same path passed to wazero.NewCompilationCacheWithDir, never
	// recomputed.
	sharedCompilationCacheDir string
)

// selectCompilationCacheWithDir is the disk-vs-memory choice behind
// sharedCompilationCache: with disablePersistent false it prefers the persistent
// on-disk store at CompileCacheDir and falls back to an in-memory cache when no
// cache directory resolves or the disk store cannot be created; with
// disablePersistent true it returns an in-memory-only cache. The second return is
// the on-disk directory backing the cache, or "" when the in-memory cache is
// used; sharedCompilationCache records it so Load's cross-process compile lock
// can locate its per-guest lock files without re-deriving the selection.
func selectCompilationCacheWithDir(disablePersistent bool) (wazero.CompilationCache, string) {
	if disablePersistent {
		return wazero.NewCompilationCache(), ""
	}
	dir, ok := CompileCacheDir()
	if !ok {
		return wazero.NewCompilationCache(), ""
	}
	return compilationCacheAt(dir)
}

// compilationCacheAt returns a disk-backed compilation cache rooted at dir,
// falling back to an in-memory cache only when that directory cannot be created.
// The second return is dir when the disk store was built and "" for the in-memory
// fallback, so a caller can tell which branch it got without inspecting the
// opaque cache value.
//
// Taking the directory as a parameter is what lets the disk branch be exercised
// against a caller-chosen directory. That matters because a test binary resolves
// every cache location to one shared root (see cacheRoot), which a test can
// neither start empty nor safely populate; a test that must observe the
// disk-vs-memory decision itself therefore passes its own throwaway directory
// here rather than reaching for a second, unguarded way to name a cache
// directory.
func compilationCacheAt(dir string) (wazero.CompilationCache, string) {
	if disk, err := wazero.NewCompilationCacheWithDir(dir); err == nil {
		return disk, dir
	}
	return wazero.NewCompilationCache(), ""
}

// Directory names beneath the cache root. Each cache names its leaf exactly
// once, so a location is always the root plus one of these rather than a second
// spelling of the whole path.
const (
	compileCacheDirName  = "wasm-compile"
	describeCacheDirName = "plugin-describe"

	// testCacheRootSuffix names the directory that holds every cache while a test
	// binary is running: the directory a normal invocation owns, with this
	// appended, beside it. It is a sibling of that directory rather than a
	// directory inside it, so the two never share a byte in either direction.
	//
	// It is a fixed name rather than a per-process one so all test processes share
	// one set of caches and a whole run compiles each guest at most once.
	testCacheRootSuffix = "-test"
)

// cacheRootAtStart is the root this process keeps its persistent caches under,
// as the environment stated it when the process started, together with what was
// missing when no root resolves. Every cache location in the process derives from
// this one capture.
//
// It is captured once rather than asked for on each call, and the reason is that
// the root is read from the environment, so the answer can change while the
// process runs. Two things depend on it not changing. The process-wide
// compiled-module cache is built once, on first use, and holds the directory it
// was built with for the whole process — so a later caller resolving a different
// directory would write to a path the built cache never opened, or to one that has
// since been removed. And the directory holds machine code that gets mapped
// executable, so where it lives should be settled before any code in the process
// has had the chance to move it, rather than being whatever the environment says
// at the moment the first guest happens to load.
//
// Where the root is on each platform is not decided here: machinedirs decides it,
// for this process's own platform and environment, and that same decision is what
// `aggrega config init` reports — so the caches and the report cannot name
// different directories.
var cacheRootAtStart, cacheRootAtStartErr = processCacheRoot(machinedirs.Host(), os.Getenv)

// processCacheRoot is the cache root a process running on p, with the
// environment lookup reads, keeps its caches under: the machine-wide root a
// normal invocation owns, or its test sibling within a test binary.
func processCacheRoot(p machinedirs.Platform, lookup func(string) string) (string, error) {
	if testing.Testing() {
		return machinedirs.SiblingCacheRoot(p, lookup, testCacheRootSuffix)
	}
	return machinedirs.CacheRoot(p, lookup)
}

// cacheRoot returns the root directory this process's persistent caches live
// under and whether one could be resolved.
//
// It is the single site every cache reads that root from. Every cache — the
// compiled-module store, the plugin-describe store, the provenance index, the
// compile-attribution index — derives its own location from this one answer, so
// the read side and the write side of any one cache cannot disagree about where
// it lives, and a cache added later inherits the decision instead of repeating it.
// The answer is also the same whenever it is asked, because it is captured at
// process start (see cacheRootAtStart).
//
// A test binary and a normal invocation differ in exactly one thing: the name of
// the directory they take beneath the per-user cache directory. Both resolve that
// same per-user parent rather than a shared system location every local account
// may write to, and that matters more here than for an ordinary cache. A
// compiled-module entry is machine code, which the runtime maps as executable
// memory and jumps into after checking a magic number, a version string and a
// checksum — all three of which anyone able to write the entry can produce. So a
// cache directory a second local account could write to would hand that account
// code execution inside this process, and placing the directory where only its
// owner can write is what removes the possibility rather than policing it.
//
// The two names are siblings, never nested, and that is what keeps a test run away
// from a developer's own bytes: those are expensive to rebuild, nothing a test
// holds can reconstruct them, and the cache-maintenance surface removes a resolved
// directory outright. Every such removal is bounded to the root the running
// process resolved, so neither side can reach the other's.
//
// The test name is fixed rather than per-process so every test process shares one
// set of caches and a whole run compiles each guest at most once, with later
// processes reusing the machine code rather than each paying its own cold compile.
//
// A false return (no resolvable per-user cache directory) signals the caller to
// fall back to a non-persistent path rather than fail.
func cacheRoot() (string, bool) {
	if cacheRootAtStartErr != nil {
		return "", false
	}
	return cacheRootAtStart, true
}

// AggregaCacheRoot returns the root directory the CLI's persistent caches live
// under and whether one could be resolved. It is the exported accessor over the
// single internal root spelling, so a sibling cache that lives alongside the
// compile cache and the plugin-describe cache — such as the plugin provenance
// index or the compile-attribution index — locates the same parent without
// re-deriving it. A false return means no cache root could be resolved.
func AggregaCacheRoot() (string, bool) {
	return cacheRoot()
}

// AggregaCacheRootError returns why no cache root resolved for this process —
// what the platform's rule found missing — or nil when one did. It is the reason
// behind a false [AggregaCacheRoot], for a caller that has to tell a reader why a
// machine-wide cache is unavailable rather than only that it is.
func AggregaCacheRootError() error {
	return cacheRootAtStartErr
}

// CompileCacheDir returns the directory of the persistent wazero compiled-module
// cache and whether one could be resolved. The provider guests are identical
// bytes across every project, so the cache is rooted at the cache root rather
// than a per-project directory: the first build on a machine pays the guest's
// compilation cost, and every later build of any project reuses it. wazero
// namespaces its entries by a version+arch+os marker beneath this directory, so
// an incompatible cache is ignored, not misread.
//
// It is the one spelling of this directory. The loader compiles into it and the
// cache-inspection and cache-maintenance commands enumerate and reclaim it, so
// those commands necessarily act on the same bytes the loader produced. A false
// return means no cache root could be resolved, so no persistent compile cache
// exists to act on and the caller falls back to an in-memory cache.
func CompileCacheDir() (string, bool) {
	root, ok := cacheRoot()
	if !ok {
		return "", false
	}
	return filepath.Join(root, compileCacheDirName), true
}

// PluginDescribeCacheDir returns the directory for the persistent
// plugin-describe cache and whether one could be resolved. A guest's describe()
// output is a static, content-addressed self-report, so the cache keys each
// descriptor on the resolved guest's content digest and lives alongside the
// compile cache under the shared cache root: a configured plugin is described
// once and re-read from cache on later runs, and a change to the resolved
// artifact yields a new digest and so a fresh entry. A false return signals the
// caller to skip the cache and describe the guest directly.
func PluginDescribeCacheDir() (string, bool) {
	root, ok := cacheRoot()
	if !ok {
		return "", false
	}
	return filepath.Join(root, describeCacheDirName), true
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
	dir, ok := CompileCacheDir()
	if !ok {
		return true
	}
	return dirHasNoEntries(dir)
}

// dirHasNoEntries reports whether dir holds no entries, treating an unreadable or
// absent directory as empty. It is the cold-guess predicate PluginCompileCacheCold
// applies to the resolved compile-cache directory, split out so the predicate can
// be exercised against a caller-chosen directory in the three states it
// distinguishes — absent, present but empty, populated.
func dirHasNoEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
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
	name   string
	plugin *extism.Plugin
	// compiled is the compiled form this instance was made from, set only when the
	// instance owns it — a load that shares nothing with any other, such as one by
	// a Loader with no cache. Close releases it with the instance. A compiled form
	// shared across the loads of a process is never set here and never closed.
	compiled   *extism.CompiledPlugin
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
	// sourceScope is what those host functions read the source's files through:
	// the ignore rules the entity's lock records, set with the root by
	// SetSource and cleared by SetSourceRoot, so a rule list never outlives the
	// node it was set for.
	sourceScope sourcewalk.Scope
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
	// callTimeout is the per-call ceiling on GUEST execution, resolved at load
	// from the spec's TimeoutMs or the platform default. The runtime is
	// configured to close a module when its call context is done, and
	// callUnderGuestTimeCap cancels that context once the guest has spent this
	// much time executing — so the two together are what turn a guest that will
	// not yield into a loud cancellation. Zero disables the ceiling; no load path
	// produces a zero, since effectiveTimeoutMs substitutes the platform default.
	callTimeout time.Duration
	// hostTime accumulates the time this instance spends inside host functions
	// during one export invocation, on the clock callUnderGuestTimeCap measures
	// the guest in, so that time stays off the guest's ceiling. It is reset per
	// invocation.
	hostTime hostTimeAccount
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
	p.sourceScope = sourcewalk.Scope{}
}

// SetSource sets the acquired source location as [Plugin.SetSourceRoot] does,
// together with the scope its files are read through: the ignore rules the
// entity's lock records, spelled against the entity's own directory. A pack or a
// read of a file the rules leave out answers as though the file were not there,
// naming it, so a guest reads exactly the files a publication of the entity
// carries; a symbolic link is refused whatever the rules say. The zero scope
// applies no rule.
func (p *Plugin) SetSource(root string, scope sourcewalk.Scope) {
	p.sourceRoot = root
	p.sourceScope = scope
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
//  6. Instantiate the underlying extism.Plugin from the guest's compiled
//     form — compiled from the same in-memory byte slice (via
//     extism.WasmData) with EnableWasi: true, or reused from an earlier
//     load of the same bytes — under the deterministic,
//     capability-denied ModuleConfig from deterministicModuleConfig. The
//     plugin's WASI imports resolve to the Extism-bundled wazero WASI
//     runtime, whose observable behaviour the ModuleConfig pins to
//     deterministic values.
//
// Reading the bytes once and threading the same slice through load-gate
// inspection, host-function derivation, and Extism instantiation
// eliminates the TOCTOU window between validation and execution.
// Returns an error wrapping ErrWasmManifestRejected if the load gate
// rejects the manifest or any import or a derived host function is
// outside the allowlist; the Extism SDK's error otherwise.
func (l *Loader) Load(ctx context.Context, spec PluginSpec) (*Plugin, error) {
	return l.loadCore(ctx, spec, buildProfile, nil, nil)
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
	return l.loadCore(ctx, spec, cliProfile, stdout, stderr)
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
// serialization stay single-sourced. Only the profile's two functions differ:
//
//   - its subset gate is the union gate (validateHostFunctionsSubsetCLI), so any
//     plugin type's imports pass and any type is describable — the build
//     profile's strict shared gate, which rejects the streaming emitters, does
//     not apply to a surface that only needs the self-report.
//   - its builder is the inert-stub builder (describeOnlyHostFunctions), so a
//     guest's describe() reaches only no-op stubs and gains no real host
//     capability during introspection.
//
// The streaming closures' writers are nil: the inert stubs stream nothing, so
// there is no terminal to write to. The deterministic, capability-denied
// ModuleConfig is unchanged.
func (l *Loader) loadForDescribe(ctx context.Context, spec PluginSpec) (*Plugin, error) {
	return l.loadCore(ctx, spec, describeProfile, nil, nil)
}

// loadProfile is one instantiation profile: the subset gate a guest's derived
// host functions are checked against, the builder that binds them, and the name
// that keeps the compiled forms built under different profiles apart. The three
// travel as one value so a compiled form can never be filed under one profile's
// name while carrying another profile's host functions.
type loadProfile struct {
	name           string
	validateSubset func(name, typeLabel string, names []string) error
	buildFunctions func(declared []string) []extism.HostFunction
}

// The three profiles Load, LoadCLI and loadForDescribe run under.
var (
	buildProfile    = loadProfile{name: "build", validateSubset: validateHostFunctionsSubset, buildFunctions: hostFunctionsForDeclared}
	cliProfile      = loadProfile{name: "cli", validateSubset: validateHostFunctionsSubsetCLI, buildFunctions: hostFunctionsForDeclaredCLI}
	describeProfile = loadProfile{name: "describe", validateSubset: validateHostFunctionsSubsetCLI, buildFunctions: describeOnlyHostFunctions}
)

// loadCore is the single shared body Load, LoadCLI and loadForDescribe run, so
// the entry points single-source the manifest gate, byte read, import-module
// allowlist, host-function derivation, compilation, instantiation and compile
// serialization rather than keeping parallel copies. The profiles differ only in
// the arguments: profile carries the subset gate the derived host-function set is
// checked against (the shared allowlist for the build profile, the union for the
// CLI and describe-only profiles) and the builder that binds the host functions;
// stdout and stderr are the streams the CLI streaming host functions write to
// (nil on the other profiles, which bind no streaming host function).
//
// The flow mirrors Load's documented sequence: translate + manifest gate, read the
// bytes once, apply the import-module allowlist, derive the host functions from
// the bytes and check the derived set against the profile's gate, then obtain
// the guest's compiled form (see compiledPlugin) and instantiate it from the
// same in-memory bytes under the deterministic, capability-denied ModuleConfig.
// Reading the bytes once and threading the same slice through load-gate
// inspection, derivation, and compilation eliminates the TOCTOU window between
// validation and execution.
//
// Every load returns a fresh instance, with its own linear memory, its own
// sandbox configuration and its own Plugin state. When the Loader has a cache,
// the compiled form the instance is made from — the compiled guest and runtime
// kernel, the WASI and host modules — is built on the first load of the guest
// under this profile and reused by every later one in the process. The compiled
// form holds no instance state: the host functions bound into it find the
// instance they serve through the call's context (see newBoundHostFunction).
func (l *Loader) loadCore(
	ctx context.Context,
	spec PluginSpec,
	profile loadProfile,
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
	if err := profile.validateSubset(spec.Name, string(spec.PluginType), hostFunctions); err != nil {
		return nil, err
	}

	// Replace the manifest's WasmFile entry (which would have Extism
	// re-read from disk) with WasmData carrying the already-read
	// bytes. The load gate validated these specific bytes; Extism
	// will compile and instantiate the same bytes.
	manifest.Wasm = []extism.Wasm{
		extism.WasmData{Data: wasmBytes, Name: spec.Name},
	}

	// The per-load state the host functions serve: the loader's external-input
	// service for guest fetches, the CLI streams for the streaming emitters, and
	// the warnings, source and parser the caller sets per invocation. A host
	// function reads it from the calling instance at call time, never from the
	// compiled form, which every instance of this guest may share.
	p := &Plugin{
		name:           spec.Name,
		externalInputs: l.ExternalInputs,
		hostFunctions:  hostFunctions,
		stdout:         stdout,
		stderr:         stderr,
		callTimeout:    time.Duration(effectiveTimeoutMs(spec)) * time.Millisecond,
	}

	// WithCloseOnContextDone is what makes a cancelled call context unwind a
	// running guest instead of being noticed only once the guest next returns.
	// It is set on EVERY load, cache or no cache: it is the runtime half of the
	// per-call ceiling (callUnderGuestTimeCap is the other), and a load that
	// omitted it would carry a ceiling that could not cancel anything.
	//
	// A shared compilation cache memoizes the guest's compiled machine code so
	// repeated loads of the same bytes skip recompilation. It carries no
	// runtime authority and no observable behaviour of its own — it changes
	// only compilation cost — so it leaves the deterministic ModuleConfig and
	// the AOT-purity load gate untouched.
	runtimeConfig := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if l.Cache != nil {
		runtimeConfig = runtimeConfig.WithCompilationCache(l.Cache)
	}
	config := extism.PluginConfig{
		EnableWasi:    true,
		ModuleConfig:  deterministicModuleConfig(spec.programName()),
		RuntimeConfig: runtimeConfig,
	}

	// The compiled form is built at most once per guest, profile and memory
	// ceiling in the process, and the locks that build takes are held only while
	// it runs — see compiledPlugin. Keying it on the guest's content digest lets
	// different guests compile in parallel. None of this changes the compiled code
	// or the rendered bytes, only when compilation happens and how often the same
	// compiled form is rebuilt.
	guestDigest := digest.ContentDigest(wasmBytes)
	compiled, owned, err := l.compiledPlugin(guestDigest, profile.name, manifest, func() (*extism.CompiledPlugin, error) {
		return extism.NewCompiledPlugin(ctx, manifest, config, profile.buildFunctions(hostFunctions))
	})
	var extismPlugin *extism.Plugin
	if err == nil {
		extismPlugin, err = compiled.Instance(ctx, extism.PluginInstanceConfig{ModuleConfig: config.ModuleConfig})
		if err != nil && owned {
			_ = compiled.Close(ctx)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("wasm: load plugin %q: %w", spec.Name, err)
	}

	p.plugin = extismPlugin
	if owned {
		p.compiled = compiled
	}
	if l.OnLoad != nil {
		l.OnLoad(spec.Name)
	}
	return p, nil
}

// compiledKey names one compiled form within one compilation cache: the guest's
// content digest plus everything the compiled form fixes for every instance made
// from it — the load profile, which decides which host functions are bound, and
// the memory ceiling, which the runtime enforces for all of its instances.
type compiledKey struct {
	cache    wazero.CompilationCache
	digest   string
	profile  string
	maxPages uint32
}

// compiledSlot holds the compiled form for one compiledKey once its first load
// has built it. The mutex makes that build happen once; the pointer lets every
// later load read it without taking any lock.
type compiledSlot struct {
	mu       sync.Mutex
	compiled atomic.Pointer[extism.CompiledPlugin]
}

// compiledPlugins maps each compiledKey to its compiledSlot for the life of the
// process. It gains an entry per distinct guest, profile and memory ceiling
// loaded, never per load or per call, and it is never pruned: the compiled code
// an entry refers to stays in the cache's engine for the life of the process
// whether or not the entry exists.
var compiledPlugins sync.Map

// compiledPlugin returns the compiled form a load of the guest identified by
// guestDigest instantiates, calling build to make it on the guest's first load
// under this profile and memory ceiling and reusing it on every later one. owned
// reports that the caller must close the form together with the one instance made
// from it; a shared form is never closed.
//
// Only that first build takes a lock — the slot's own, then the first-load gate
// and the compile locks (see firstLoadGate and compileGuest). A load that finds
// the form built takes none.
//
// Two kinds of load build a form of their own and own it. A Loader with no cache
// shares nothing, since each of its loads runs on its own runtime and engine, so
// it takes no lock either. A load whose manifest carries a plugin configuration
// has that configuration fixed into its compiled form, and keeping a form per
// distinct configuration would let the process-wide table grow with whatever a
// caller put there; it still passes the first-load gate, because it compiles
// against the shared engine.
func (l *Loader) compiledPlugin(guestDigest, profile string, manifest extism.Manifest, build func() (*extism.CompiledPlugin, error)) (*extism.CompiledPlugin, bool, error) {
	if l.Cache == nil {
		cp, err := build()
		return cp, true, err
	}
	gated := func() (*extism.CompiledPlugin, error) {
		return l.firstLoadGate(guestDigest, func() (*extism.CompiledPlugin, error) {
			return l.compileGuest(guestDigest, build)
		})
	}
	if len(manifest.Config) > 0 {
		cp, err := gated()
		return cp, true, err
	}

	key := compiledKey{cache: l.Cache, digest: guestDigest, profile: profile}
	if manifest.Memory != nil {
		key.maxPages = manifest.Memory.MaxPages
	}
	v, ok := compiledPlugins.Load(key)
	if !ok {
		v, _ = compiledPlugins.LoadOrStore(key, &compiledSlot{})
	}
	slot := v.(*compiledSlot)
	if cp := slot.compiled.Load(); cp != nil {
		return cp, false, nil
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if cp := slot.compiled.Load(); cp != nil {
		return cp, false, nil
	}
	cp, err := gated()
	if err != nil {
		return nil, false, err
	}
	slot.compiled.Store(cp)
	return cp, false, nil
}

// firstLoadGate runs build — a build of a guest's compiled form against l.Cache —
// excluding every other build that could read a module this one is still
// publishing. It is the one exclusion a cache hit needs, and it is reached only
// by the first load of a guest's compiled form in the process: a later load
// reuses the form and never reaches it.
//
// 🔴 It exists for one behaviour of the WebAssembly runtime
// (github.com/tetratelabs/wazero), and that behaviour must be re-verified at
// every upgrade of the runtime: a compiled module read from the on-disk
// compilation cache is put into the engine's shared in-memory table BEFORE its
// entry preambles are compiled. Until that compile step returns, a second compile
// of the same module finds the entry in the table and returns at once, and an
// instance made from it reads preambles that may not exist yet. A compile that
// misses the cache builds the whole entry before publishing it, so only a disk
// hit has the window — and the loader cannot see which of the two the runtime
// will do, so every build takes the gate.
//
// The gate closes the window twice over:
//
//   - per guest: builds of one guest's compiled form, under any profile or
//     memory ceiling, take turns, so each after the first finds the guest's entry
//     complete;
//   - for the runtime kernel every guest's compiled form includes: until one
//     build against the cache has completed, builds of different guests take
//     turns as well, because each of them compiles that kernel. Once one has
//     completed the kernel's entry is complete, and the engine keeps it while the
//     cache is open: the engine drops a module only once every compile of it has
//     been closed, and closing a compiled form releases none of them
//     (TestSharedEngineKeepsEveryModuleItCompiled pins that), so later builds of
//     different guests run in parallel.
func (l *Loader) firstLoadGate(guestDigest string, build func() (*extism.CompiledPlugin, error)) (*extism.CompiledPlugin, error) {
	guest := compileMutex(guestDigest)
	guest.Lock()
	defer guest.Unlock()

	if _, done := firstLoadDone.Load(l.Cache); done {
		return build()
	}
	firstLoadMu.Lock()
	if _, done := firstLoadDone.Load(l.Cache); !done {
		defer firstLoadMu.Unlock()
		cp, err := build()
		if err == nil {
			firstLoadDone.Store(l.Cache, struct{}{})
		}
		return cp, err
	}
	firstLoadMu.Unlock()
	return build()
}

var (
	// firstLoadMu makes the first build against a compilation cache run alone
	// (see firstLoadGate).
	firstLoadMu sync.Mutex

	// firstLoadDone records each compilation cache a build has completed against.
	// A cache recorded here holds a complete runtime kernel.
	firstLoadDone sync.Map
)

// compileLocks maps a guest content digest to the *sync.Mutex that makes builds
// of that guest's compiled form take turns (see firstLoadGate). Distinct guests
// get distinct mutexes, so they build in parallel.
var compileLocks sync.Map

var (
	// attribObserveMu serializes in-process cold compile observations so two
	// concurrent cold compiles in THIS process cannot interleave their
	// snapshot-before / snapshot-after windows and cross-attribute each other's
	// leaves. The locks a first build takes are always taken in one order — the
	// compiled form's slot, the per-guest turn, the first-load gate, this mutex,
	// then the cross-process file lock — so concurrent builds cannot deadlock.
	// Cross-PROCESS concurrent cold compiles are not serialized by this mutex;
	// their confounded diffs degrade to unattributed (recorded as nothing), never
	// to a wrong attribution.
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

// compileGuest runs the build of the guest's compiled form under the
// cross-process compile lock, and on a cold, not-yet-observed first compile
// additionally records which compiled-module cache leaves that compile emitted,
// so a later reference-based reclamation can attribute each leaf to the guests
// that depend on it. It runs inside firstLoadGate, whose per-guest turn puts at
// most one build of this guest here at a time.
//
// Every attribution outcome is SAFE because compiled leaves are re-derivable:
// the worst case of any misattribution is an unnecessary recompile, never a
// broken build. The attribution mutex serializes only in-process cold
// observations; cross-process cold-compile races confound the leaf diff and
// degrade to unattributed (recorded as nothing, so the leaf is retained). The
// per-process memo keeps snapshotting and the attribution lock off every build
// after a guest's first, and a guest the attribution index already records as
// present on disk is not observed at all, since a disk hit emits nothing to
// attribute; a wrong prediction leaves the compile's leaves unattributed, which
// retains them.
func (l *Loader) compileGuest(guestDigest string, compile func() (*extism.CompiledPlugin, error)) (*extism.CompiledPlugin, error) {
	// Nothing to observe without an observable compile directory, or once this
	// guest was observed in this process.
	if l.compileDir == "" {
		return lockedCompile(l, guestDigest, compile)
	}
	if _, seen := attribObserved.Load(guestDigest); seen {
		return lockedCompile(l, guestDigest, compile)
	}
	if l.predictDiskHit(guestDigest) {
		attribObserved.Store(guestDigest, struct{}{})
		return lockedCompile(l, guestDigest, compile)
	}

	attribObserveMu.Lock()
	before := listCompileLeaves(l.compileDir)
	compiled, err := lockedCompile(l, guestDigest, compile)
	after := listCompileLeaves(l.compileDir)
	// Warm forever regardless of outcome — a failed compile is not retried into
	// observation, and a successful one is observed exactly once.
	attribObserved.Store(guestDigest, struct{}{})
	atomic.AddInt64(&attribSnapshotCount, 1)
	attribObserveMu.Unlock()

	l.attributeCompileLeaves(guestDigest, before, after)
	return compiled, err
}

// lockedCompile takes the guest's cross-process compile lock, runs compile, and
// releases the lock. It is the sole path that invokes the compile so the lock
// discipline is identical on the observed and unobserved branches.
func lockedCompile(l *Loader, guestDigest string, compile func() (*extism.CompiledPlugin, error)) (*extism.CompiledPlugin, error) {
	release := l.lockGuestFile(guestDigest)
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
// Membership is isCompileCacheLeaf, the same predicate capped reclamation
// consults, so what this snapshot counts as a compiled module and what a size cap
// removes are one answer rather than two that agree today. The cache-inspection
// command applies its own small predicate on the far side of the package
// boundary; keep the two in step, since a change to which files count as compiled
// modules must land on both.
//
// A cache directory that is itself a link is walked where it leads, as
// [CollectCompileCacheLeaves] walks it, so the snapshot keys a leaf exactly as
// reclamation and the cache listing do.
func listCompileLeaves(dir string) map[string]int64 {
	out := map[string]int64{}
	_ = pathscope.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !isCompileCacheLeaf(d.Name()) {
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

// lockGuestFile takes the cross-process compile lock for the guest identified by
// key (its content digest) when this Loader compiles into an on-disk directory
// other processes may share, and returns the function that releases it. With no
// such directory it takes nothing: an in-memory cache is private to the process.
// The in-process turn a build takes is firstLoadGate's, not this lock's.
//
// The lock keeps processes sharing the directory from compiling the same guest at
// the same time, and that is an efficiency measure, not a safety one. The entry is
// safe either way, because the runtime publishes it by writing a temporary file
// in the same directory and renaming it onto the entry's name, so a reader opens
// a whole entry or finds none. For the same reason a load the attribution index
// predicts will be served from disk skips the lock (see predictDiskHit): holding
// it would only make processes queue to read one file, and a wrong prediction
// costs at most a compile another process is also running. The lock is
// best-effort: if its file cannot be opened or locked, the build proceeds
// without it.
func (l *Loader) lockGuestFile(key string) func() {
	if l.compileDir == "" || l.predictDiskHit(key) {
		return func() {}
	}
	return flockGuestCompile(l.compileDir, key)
}

// predictedHits memoizes, per compile directory and guest digest, whether this
// process predicted that guest's compiled form is on disk, so the attribution
// index is read once per guest rather than once per build.
var predictedHits sync.Map

// predictDiskHit reports whether the attribution index predicts the runtime will
// serve this guest from the on-disk cache. The loader cannot ask the runtime
// itself — the runtime decides hit or miss inside its compile and does not report
// it, and the entry's file name hashes processor features the runtime does not
// expose — so the index, which records each leaf a guest's cold compile emitted,
// is the one predictor available. A false answer is always safe: it only means
// the build is observed and takes the cross-process lock.
func (l *Loader) predictDiskHit(guestDigest string) bool {
	if l.compileDir == "" {
		return false
	}
	key := l.compileDir + "\x00" + guestDigest
	if v, ok := predictedHits.Load(key); ok {
		return v.(bool)
	}
	hit := l.guestAttributed(guestDigest)
	predictedHits.Store(key, hit)
	return hit
}

// guestAttributed reports whether the attribution index records a leaf, other
// than the shared runtime kernel, that names guestDigest as a referrer and is
// still present on disk at its recorded size.
func (l *Loader) guestAttributed(guestDigest string) bool {
	ix, err := attribindex.Load(attribindex.PathForCompileDir(l.compileDir))
	if err != nil {
		return false
	}
	want := digest.BareHex(guestDigest)
	for _, e := range ix.Entries() {
		if e.Kernel {
			continue
		}
		for _, r := range e.Referrers {
			if r != want {
				continue
			}
			info, statErr := os.Stat(filepath.Join(l.compileDir, filepath.FromSlash(e.Leaf)))
			if statErr == nil && info.Size() == e.Size {
				return true
			}
		}
	}
	return false
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
//   - No sleep, yield or stdin: wazero's defaults make a guest's sleep
//     and yield return at once and its stdin report end of input at once,
//     so a guest cannot wait anywhere but inside a host function. The
//     per-call ceiling depends on it — measured in processor time, a guest
//     able to sleep would stop spending its budget — so configuring a real
//     sleep, yield or stdin here removes the ceiling's hold on a guest that
//     will not return.
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

// call invokes an exported function on the plugin. Returns the output
// bytes the guest wrote to its output buffer.
//
// It is unexported on purpose. A caller outside this package reaches a guest
// only through [InvokeExport], which takes an export the plugin-type registry
// lists, or through one of the entry points for an export authored content
// names; an exported call taking any name would let a socket call an export no
// list holds, with a payload, and nothing would refuse it.
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
func (p *Plugin) call(ctx context.Context, funcName string, input []byte) ([]byte, error) {
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
			p.name, funcName, exitCode, ErrWasmPluginExited())
	}
	return output, nil
}

// callRaw invokes funcName on the guest and returns the raw exit code alongside
// the output, applying NO exit-code policy: a clean non-zero exit is reported
// through exitCode as a status, never folded into err. err is set only by a real
// Wasm trap (wrapped, naming the plugin and function) or a recovered guest panic
// (wrapping [ErrWasmGuestPanic]); on either, exitCode is zero and output nil.
//
// It is the shared call core behind both [Plugin.call] — which layers the
// compile-time non-zero→error policy on top — and the runtime CLI runner's
// [InvokeExportStatus] — which reads the exit code as the command's status. One
// core keeps the trap wrap and the panic-recovery net single-sourced; the two
// callers differ only in how they treat a clean non-zero exit.
func (p *Plugin) callRaw(ctx context.Context, funcName string, input []byte) (exitCode uint32, output []byte, err error) {
	// This is the one site that runs guest code, so it is where the host functions
	// bound into the compiled form learn which instance they are serving.
	ctx = contextWithCallingPlugin(ctx, p)
	err = p.callUnderGuestTimeCap(ctx, funcName, func(ctx context.Context) error {
		var callErr error
		output, callErr = callWithPanicRecovery(p.name, funcName, func() ([]byte, error) {
			code, out, e := p.plugin.CallWithContext(ctx, funcName, input)
			if e != nil {
				return nil, fmt.Errorf("wasm: plugin %q call to %s: %w", p.name, funcName, e)
			}
			exitCode = code
			return out, nil
		})
		return callErr
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
// Extracted from the call core so the wrap shape is independently
// testable without needing a real Wasm-side panic source — tests pass
// a function that panics directly.
func callWithPanicRecovery(pluginName, funcName string, f func() ([]byte, error)) (output []byte, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		err = fmt.Errorf("wasm: guest plugin %q panicked in %s: %v: %w",
			pluginName, funcName, r, ErrWasmGuestPanic())
	}()
	return f()
}

// Close releases the resources held by the underlying Extism plugin
// instance, and the compiled form it was made from when the instance owns it.
// A compiled form shared across the loads of the process outlives every
// instance made from it. Safe to call once; subsequent calls return the SDK's
// error if any. Callers SHOULD invoke Close when the plugin is no longer
// needed — typically deferred immediately after Load.
func (p *Plugin) Close(ctx context.Context) error {
	if p == nil || p.plugin == nil {
		return nil
	}
	err := p.plugin.CloseWithContext(ctx)
	if p.compiled != nil {
		if cerr := p.compiled.Close(ctx); err == nil {
			err = cerr
		}
	}
	return err
}
