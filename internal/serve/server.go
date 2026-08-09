// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package serve implements the long-running compiler-as-service daemon
// invoked by `aggrega serve`. The daemon speaks JSON-RPC 2.0 over
// stdin/stdout under the `aggrega/` method namespace, holds catalog +
// parse-cache + diagnostic state across requests, and serves an
// IDE-aligned operation set for editor integrations.
//
// The package's contract is the wire protocol, not the Go API — no
// public types outside this package consume serve.Run or the handler
// functions. The only intended consumer is the IDE extension that
// spawns the daemon as a subprocess. Because the protocol rather than
// the Go surface is what callers bind to, the sections below state it
// here rather than leaving it to be reassembled from the handlers.
//
// # The protocol is domain-specific, not the Language Server Protocol
//
// The framing is the one editors already speak — Content-Length headers
// over stdin/stdout — and a few shapes borrow LSP's vocabulary, notably
// yamlpos.Position, yamlpos.Range and the position-encoding negotiation
// on aggrega/initialize. The method set is not LSP's. Every method sits
// under the `aggrega/` namespace and operates on Aggregates, Releases,
// catalog entities and the IR graph, which LSP does not model. An editor
// integration translates between the two surfaces; pointing a stock LSP
// client at this daemon does not work.
//
// # The method and notification set
//
// Requests, each answered with a result or a JSON-RPC error envelope:
//
//	aggrega/initialize           handshake: negotiates the position
//	                             encoding and reports the resolved
//	                             workspace root, the configuration layer
//	                             that supplied it, and the marker that
//	                             anchored it
//	aggrega/validate             parses and semantically validates one
//	                             file, returning its diagnostics
//	aggrega/invalidate           drops cached state for one file or for
//	                             the whole workspace
//	aggrega/diagnostics          returns the diagnostics already cached
//	                             for a file
//	aggrega/irGraph              returns the IR graph for a Release
//	aggrega/releaseCandidates    lists the workspace's Release documents,
//	                             selected by document kind rather than by
//	                             filename
//	aggrega/resolve              position to the definition it names
//	aggrega/findReferences       position to the sites referencing it
//	aggrega/findImplementations  position to the entities implementing it
//	aggrega/hover                position to hover contents
//	aggrega/completion           position to completion items
//
// Notifications:
//
//	aggrega/shutdown             starts the drain-then-abort sequence
//	                             documented on dispatchLoop
//	aggrega/publishDiagnostics   server-push, carrying diagnostics the
//	                             daemon produced outside any request —
//	                             a lockfile drift finding, for instance
//
// # Validation seeds the per-file state every other file operation reads
//
// aggrega/validate is the only method that populates a file's parse and
// diagnostic cache entries. aggrega/diagnostics, aggrega/hover,
// aggrega/completion, aggrega/resolve, aggrega/findReferences and
// aggrega/findImplementations all read that state and refuse with the
// file-not-validated application code when it is absent, so a client
// sends validate for a file before any of them. Entries are keyed by
// file path alone: one daemon serves one workspace and one client, so
// no per-client dimension is needed.
//
// # Paths on the wire
//
// A request naming a document names it relative to the workspace root
// the handshake reported. The daemon refuses any other form rather than
// repairing it: an absolute value, or one resolving outside that root,
// draws the standard invalid-params error, whose message states the
// contract and then the specific fault. The refusal is load-bearing
// rather than defensive — filepath.Join concatenates an absolute second
// operand instead of failing, so a value quietly joined has the daemon
// read, and report diagnostics against, a path the workspace never
// held. Repairing such a value here would teach every client that the
// contract is optional, so the request is rejected and its sender
// corrects it. aggrega/irGraph is the method this governs today; see
// resolveReleaseParam.
//
// Error envelopes point back at artifacts through fields named for the
// artifact, never through one general-purpose path field. On errorData,
// File is the source document its Line and Column locate, Lockfile is a
// lockfile, and Release is a Release. A client learns what it was handed
// from the field it read, rather than having to branch on the error code
// beside it.
//
// # Where the rest of the contract lives
//
// The wire framing, the worker-pool submission loop and the two-step
// shutdown are on dispatchLoop. The application error codes and the
// condition each one reports are on their declarations in this package.
// Cache staleness is not a client responsibility: cache re-reads the
// file's modification time on every lookup and discards an entry the
// file has outgrown, so external edits — a branch switch, an editor
// writing outside the extension — heal on their own, and
// aggrega/invalidate is a latency optimisation rather than a
// correctness requirement. Diagnostic position fidelity, including the
// authoring shapes that fall back to the head of the file, is on
// runTier4Validation. Workspace anchoring is in package workspaceconfig,
// whose Marker constants are declared in the order discovery consults
// them; the choice of active Release when a workspace holds several is
// on runManifestCascade.
package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// defaultDrainTimeout is the tight backstop the SECOND aggrega/shutdown
// (the abort) arms: after the reader cancels serverCtx, this budget
// gives the dispatch loop an exit trigger once cancellation has
// propagated to in-flight handlers. The FIRST shutdown does not arm it —
// that signal drains accepted in-flight work to completion.
const defaultDrainTimeout = 2 * time.Second

// RunConfig carries the I/O streams the daemon reads from and writes
// to plus optional runtime overrides. The cobra-bound entry point
// passes cmd.InOrStdin / OutOrStdout / ErrOrStderr and the
// --workspace-root flag value; tests inject bytes.Buffer (or
// io.Pipe) values to drive the dispatch loop in-memory without
// spawning a subprocess.
type RunConfig struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// WorkspaceRoot, when non-empty, bypasses parent-walk discovery
	// and anchors the workspace at this absolute path. The cobra
	// wiring validates the path exists and is a directory before
	// passing it through; Run treats an empty string as "use
	// discovery." Empty string is treated as unset (an operator
	// running --workspace-root="" falls through to discovery as if
	// the flag were not passed).
	WorkspaceRoot string

	// DrainTimeout sets the tight backstop the second aggrega/shutdown
	// (the abort) arms after cancelling in-flight work. Zero value
	// defaults to defaultDrainTimeout (2 seconds). Tests pass a short
	// value (e.g., 50ms) so shutdown behavior exercises in subsecond
	// wall time.
	DrainTimeout time.Duration

	// CompilerVersion is the binary's build identity, stamped into the IR
	// provenance the daemon produces for inspect and Tier-4 diagnostics so its
	// output names the same compiler version the CLI reports. The command layer
	// derives it (the daemon cannot, living below the main package); an empty
	// value stamps an empty compiler-version.
	CompilerVersion string

	// BuildOnInspect, when non-nil, overrides the serve.build_on_inspect
	// config-file key for this daemon invocation: the command layer sets it
	// from the --build-on-inspect flag only when the operator passed the
	// flag, so an unset flag (nil) leaves the config-file value in force. It
	// controls whether the IR-graph inspection request runs the build's
	// analysis — true publishes the build's findings as an additional
	// diagnostic layer; false (the default) returns the graph without running
	// that stage. Nothing is written to disk under either value.
	BuildOnInspect *bool
}

// resolveBuildOnInspect applies the build-on-inspect precedence: an explicit
// flag (flag non-nil, set from --build-on-inspect) wins over the config-file
// key; absent the flag, the config-file value applies.
func resolveBuildOnInspect(flag *bool, cfgValue bool) bool {
	if flag != nil {
		return *flag
	}
	return cfgValue
}

// Diagnostic is the wire shape returned by `aggrega/validate` and
// `aggrega/diagnostics` (and pushed via aggrega/publishDiagnostics).
// Line + Column are 1-based per LSP-aligned convention; Severity is
// one of "error" / "warning" / "info".
//
// EndLine + EndColumn carry the 1-based end position of the token the
// diagnostic spans. When both are present (non-zero) the extension
// renders a range spanning the whole token; when absent the extension
// falls back to a point-style range at the start position. omitempty
// keeps the wire backward compatible — pre-end-position clients ignore
// the fields and post-end-position clients consuming an envelope
// without them fall through to point-style.
//
// Source names which of the daemon's two diagnostic layers raised the entry, so
// a client can group them instead of showing one undifferentiated list. It is
// always present: a client must never have to tell "no source" apart from "the
// other layer", which is why it is stamped at each layer's own boundary rather
// than at the construction sites.
type Diagnostic struct {
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Source    string `json:"source"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
}

// The two layer sources. DiagnosticSourceSemantic covers everything the
// resolution and analysis stages report about the authored documents;
// DiagnosticSourceBuild covers the advisories a render raises, which are the
// findings `aggrega build` prints — so the value names the command whose output
// they are rather than an internal stage.
const (
	DiagnosticSourceSemantic = "aggrega"
	DiagnosticSourceBuild    = "aggrega-build"
)

// stampSource sets src on every entry in diags and returns diags.
//
// It mutates in place, which is deliberate: a caller that both stores a slice
// and returns it on the same request stamps both with one call. The layer is
// authoritative — an entry arriving with some other source is overwritten
// rather than preserved, because a diagnostic's source is a fact about which
// layer published it and no construction site is entitled to claim otherwise.
func stampSource(diags []Diagnostic, src string) []Diagnostic {
	for i := range diags {
		diags[i].Source = src
	}
	return diags
}

// Server holds the daemon's per-session state. Constructed once in Run
// after workspace-root discovery + workspace-config load; reused for
// every JSON-RPC request the dispatch loop reads.
//
// Concurrency. The daemon dispatches handlers through a bounded worker
// pool whose size is configurable via concurrency.pool_size (default
// 4). When the pool size is greater than 1, handlers run concurrently,
// so every Server field a handler touches is synchronized to support
// that. The synchronized surfaces are:
//
//   - The parse cache and the session catalog lookup are each
//     internally synchronized by their own mutex; callers need no
//     external lock. A handler resolving a Release does NOT pin the
//     session catalog: it forks a request-scoped view (ForRequest)
//     carrying its own lockfile-pin snapshot and pin-dependent caches,
//     so one request's pins can never interleave into another's
//     multi-step resolution. The session catalog holds only the
//     pin-independent, session-stable Tier-1 indexes, snapshotted into
//     each view; it is reset only on workspace invalidation.
//
//   - The per-file diagnostics map is guarded by diagMu. Publication
//     copies the file's slice under the lock and marshals outside it.
//
//   - positionEncoding is held in an atomic value: the initialize
//     handler stores it, every position-mapping site loads it.
//
//   - shuttingDown is an atomic bool: the dispatch loop's acceptance
//     gate and the shutdown handler store it, the acceptance gate loads
//     it.
//
//   - The per-request cancel registry (cancelTracker) is internally
//     synchronized by its own mutex: each in-flight handler tracks and
//     untracks its id under the lock, so concurrent track / untrack
//     across the pool need no external lock.
//
// The active-Release fields (activeRelease, activeReleasePath,
// cascadeAttempt, lockfile) are resolved lazily by ensureCascade on the
// first request that needs Release context, guarded by cascadeOnce. The
// once's happens-before guarantee makes the single write inside the
// guarded function visible to every worker that later calls ensureCascade
// before reading, so concurrent workers under the pool observe a fully
// populated snapshot without a per-field lock. The aggrega/initialize
// handler does not need Release context and does not trigger the cascade,
// so the handshake answers before any workspace walk runs.
//
// The remaining fields (workspaceRoot, configSource, compilerVersion,
// workspaceMarker, wsConfig, lazyCatalog pointer, serverCtx/serverCancel,
// cancelTracker pointer) are written once at construction before the
// worker pool starts, then only read during handler execution, so they
// are immutable snapshots needing no lock (the registry the cancelTracker
// pointer names carries its own lock for its mutable map). The reader
// goroutine reads stdin and writes channels; it never touches Server
// mutable state. The id-order stdout serializer is the sole owner of the
// output writer, so no worker touches s.out directly.
type Server struct {
	workspaceRoot string
	configSource  string
	// compilerVersion is the binary build identity injected from the command
	// layer (RunConfig.CompilerVersion) and stamped into the IR provenance the
	// daemon produces, so inspect and Tier-4 output report the same version the
	// CLI does.
	compilerVersion string
	// workspaceMarker names the marker tier that anchored the
	// workspace root via discovery. The zero value (no marker) when
	// --workspace-root was supplied explicitly, since no marker was
	// consulted in that path. Surfaced on aggrega/initialize via its
	// String() wire token.
	workspaceMarker workspaceconfig.Marker
	wsConfig        workspaceconfig.Config
	// buildOnInspect is the resolved serve build-on-inspect dial (the
	// --build-on-inspect flag overriding the serve.build_on_inspect config
	// key). When false (the default), handleIrGraph returns the IR graph
	// without running the build's analysis; when true that stage runs, so the
	// build's findings surface as an additional diagnostic layer. Written once
	// at construction, then only read during handler execution — an immutable
	// snapshot needing no lock.
	buildOnInspect bool

	// shuttingDown is set at the dispatch loop's acceptance of the first
	// aggrega/shutdown message — on the single-threaded main loop, so
	// every message accepted afterward strictly observes the flag. The
	// acceptance-boundary gate consults it to reject subsequent requests
	// with errShuttingDown and drop subsequent notifications before they
	// reach a worker. It stays atomic because the shutdown handler stores
	// it and the gate loads it; both run on the main loop today, and the
	// atomic keeps the surface safe if a store ever moves onto a worker.
	shuttingDown atomic.Bool

	// serverCtx is the session-level context every per-request context
	// derives from; serverCancel cancels it. The reader goroutine calls
	// serverCancel on the SECOND aggrega/shutdown (the abort), which
	// cancels the derived context of any in-flight handler so it returns
	// at its next phase boundary within the drain budget. The FIRST
	// shutdown does not cancel — it drains in-flight work to completion.
	// Both are initialised in dispatchLoop. serverCancel is idempotent —
	// the dispatch loop also defers it as an exit backstop.
	serverCtx    context.Context
	serverCancel context.CancelFunc

	// cancelTracker maps an in-flight request's id to its per-request
	// cancel func, so a specific running request is addressable by id.
	// It stores the same per-request cancel handle handle already owns
	// (a second address, not a second cancel authority); track / untrack
	// run inside handle for every id-bearing request. The per-id cancel
	// pickup it exposes is built and tested but not yet routed to any
	// production caller — see cancelRegistry. Constructed lazily in
	// dispatchLoop alongside serverCtx; the in-memory test harness may
	// construct it earlier.
	cancelTracker *cancelRegistry

	// lazyCatalog is the session-scoped catalog lookup: the per-URI Tier-1/2/3
	// cascade for the WHOLE catalog surface — Component + Feature (on-demand
	// cascade) plus the tolerantly-enumerated Recipe / FeatureRecipe / Aggregate /
	// Plugin / SchemaLibrary / MappingLibrary / WorkloadLibrary / ArtifactsLibrary shell. Constructed once at startup
	// via catalog.NewLazyLookup; no per-request reconstruction, no eager
	// whole-catalog load. It holds the session-stable, pin-independent Tier-1
	// filesystem indexes; Reset() on invalidation re-walks a changed workspace.
	//
	// A Release-resolving handler does NOT read or pin this lookup directly. It
	// forks a request-scoped view via ForRequest, seeded with that request's
	// lockfile pins and snapshotting the session Tier-1 indexes, and threads the
	// view through closure compute and the render pipeline (AsCatalogLookup,
	// RecipeLookupWithManifest, SubAggregateLoader, MappingLibraries, ClosureLookup,
	// the closure-scoped extends + Pass-2). Each request therefore resolves against
	// its own immutable pin snapshot, so a concurrent request's pins never perturb
	// it — the consistency the worker pool depends on.
	lazyCatalog *catalog.LazyLookup

	// cascadeOnce guards the lazy resolution of the active-Release
	// fields below. The first request that needs Release context calls
	// ensureCascade, which runs the disambiguation cascade and the
	// at-startup lockfile + drift check exactly once; every later
	// request finds the work already done. Resolution is deferred off
	// the daemon's construction path so aggrega/initialize answers
	// before any workspace-wide filesystem walk runs.
	cascadeOnce sync.Once

	// activeRelease is the workspace's session-scoped active Release,
	// resolved by the four-step disambiguation cascade on the first
	// request that needs Release context (see ensureCascade). nil when
	// the cascade exhausted into Step 4 fallback (no authoring_release
	// config, not exactly one Release file, no .aggrega/active-release
	// marker). Operations needing Release context consult cascadeAttempt
	// + cascadeDiagnosticMessage to construct error-envelope text when
	// activeRelease is nil.
	//
	// Subprocess-test coverage of the cascade-via-daemon wiring is
	// deferred: no operation currently reads activeRelease and
	// surfaces it through a wire response, so an external observer
	// cannot assert "the cascade populated this field correctly".
	// The cascade's correctness is pinned by direct unit tests on
	// runManifestCascade in manifest_cascade_test.go; the lazy
	// invocation itself is covered by compile + vet plus the
	// implicit panic-free behavior the existing subprocess tests
	// exercise. When a future operation reads activeRelease and
	// surfaces it (Tier-4 validate's execution path, or a dedicated
	// query like aggrega/activeRelease), add a subprocess test that
	// constructs a workspace matching each cascade step's win
	// condition and asserts the operation's response carries the
	// expected Release. The pickup point is here when a consumer
	// lands.
	activeRelease *types.Release

	// activeReleasePath is the absolute filesystem path of the
	// active Release the cascade resolved. Empty when activeRelease
	// is nil. Tracked alongside the parsed Release object so
	// lockfile.PathFor and the at-startup drift check can derive
	// the conventional lockfile location from it.
	activeReleasePath string

	// cascadeAttempt records what the disambiguation cascade tried
	// and which step (if any) selected activeRelease. Populated
	// alongside activeRelease on first use by ensureCascade; immutable
	// for the rest of the daemon's session lifetime.
	cascadeAttempt cascadeAttempt

	// lockfile is nil in this scaffold; populated per active Release
	// when the lockfile machinery ships.
	lockfile *lockfile.Lockfile

	parseCache *cache

	// diagMu guards every read and write of the diagnostics map. The
	// critical section is O(1) map work plus, at publish time, a copy
	// of one file's slice header; it never spans a pipeline run or any
	// I/O. Writers always reassign a whole slice (never mutate one in
	// place), so a header copy taken under the lock is a safe snapshot
	// to marshal after releasing it.
	diagMu      sync.Mutex
	diagnostics map[string][]Diagnostic

	// renderDiags is the second diagnostic layer, guarded by the same diagMu:
	// the coded advisories the render step raises, keyed by the file they are
	// attributed to. It is kept apart from diagnostics rather than merged into it
	// because the two layers refresh on different events — the semantic layer on
	// every buffer validation, the render layer only when an inspection actually
	// renders — so one overwriting the other would make each request erase the
	// other's findings. Reads compose the two; writes only ever touch one.
	renderDiags map[string][]Diagnostic

	in     *bufio.Reader
	out    io.Writer
	stderr io.Writer

	// positionEncodingValue holds the negotiated position encoding,
	// which selects how Position.Character offsets are interpreted in
	// JSON-RPC requests + responses. Defaults to UTF-16 (LSP 3.17
	// protocol default). The aggrega/initialize handler stores a new
	// value when the client negotiates a different encoding via the
	// general.positionEncodings capability; every position-mapping
	// site loads it via the positionEncoding accessor. Held in an
	// atomic value because the initialize handler that stores it and
	// the read sites run on different worker goroutines under the pool.
	positionEncodingValue atomic.Value

	// testInDispatchHook, when non-nil, runs at the top of handle()
	// before per-method dispatch. The production setup never sets
	// this field — it is the test seam tests use to inject
	// controllable handler latency for timing-sensitive coverage
	// (e.g., distinguishing arrival-based from observation-based
	// drain-timer semantics). A nil hook is a no-op.
	testInDispatchHook func()

	// testCtxHook, when non-nil, runs inside handle() with the request's
	// per-request context and id after both are established, so a test
	// can observe the live per-request context (e.g. block on its
	// Done() to assert shutdown or per-id cancellation propagates). The
	// production setup never sets this field. A nil hook is a no-op.
	testCtxHook func(ctx context.Context, id json.RawMessage)
}

// positionEncoding returns the negotiated position encoding. It loads
// the atomic value the initialize handler set; an unset value (a Server
// constructed without setPositionEncoding) reports the UTF-16 protocol
// default rather than an empty encoding.
func (s *Server) positionEncoding() yamlpos.PositionEncoding {
	v, ok := s.positionEncodingValue.Load().(yamlpos.PositionEncoding)
	if !ok {
		return yamlpos.PositionEncodingUTF16
	}
	return v
}

// setPositionEncoding stores the negotiated position encoding. Called
// by the initialize handler when the client negotiates an encoding.
func (s *Server) setPositionEncoding(enc yamlpos.PositionEncoding) {
	s.positionEncodingValue.Store(enc)
}

// setDiagnostics records the diagnostics slice for file under diagMu.
// A nil slice is stored verbatim (it reads back as an empty result).
// This is the single write entry point for the diagnostics map; all
// handler write sites route through it so the locking discipline has
// one home — which is also why the semantic layer's source is stamped
// here rather than at each construction site.
func (s *Server) setDiagnostics(file string, diags []Diagnostic) {
	stampSource(diags, DiagnosticSourceSemantic)
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	s.diagnostics[file] = diags
}

// dropDiagnostics removes file's diagnostics entry under diagMu. No-op
// when the entry is absent.
func (s *Server) dropDiagnostics(file string) {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	delete(s.diagnostics, file)
}

// snapshotDiagnostics returns a copy of file's composed diagnostics taken under
// diagMu: the semantic layer first, then the render layer. The copy is what
// callers marshal or return on the wire, so the marshal runs off the lock and
// never races a concurrent writer's whole-slice reassignment. Composition
// happens here, at the single read entry point, so no caller can publish one
// layer and silently drop the other. Both layers empty returns nil.
func (s *Server) snapshotDiagnostics(file string) []Diagnostic {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	semantic := s.diagnostics[file]
	render := s.renderDiags[file]
	if len(semantic)+len(render) == 0 {
		return nil
	}
	out := make([]Diagnostic, 0, len(semantic)+len(render))
	out = append(out, semantic...)
	out = append(out, render...)
	return out
}

// setRenderDiagnostics replaces the whole render layer with byFile and returns
// the sorted set of files whose entry the replacement touched — the union of the
// files that had render diagnostics and the files that now do — so the caller
// republishes exactly the files whose composed state changed, clearing a stale
// advisory as reliably as it publishes a new one. Whole-layer replacement is
// what a whole-closure render calls for: a per-file merge would strand an
// advisory on a file that stopped raising one.
func (s *Server) setRenderDiagnostics(byFile map[string][]Diagnostic) []string {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	touched := map[string]bool{}
	for f := range s.renderDiags {
		touched[f] = true
	}
	next := make(map[string][]Diagnostic, len(byFile))
	for f, d := range byFile {
		if len(d) == 0 {
			continue
		}
		next[f] = stampSource(d, DiagnosticSourceBuild)
		touched[f] = true
	}
	s.renderDiags = next
	files := make([]string, 0, len(touched))
	for f := range touched {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// diagnosticsFiles returns the keys of both diagnostic layers, deduplicated and
// sorted, under diagMu. Used by the initialize startup-flush so the whole-map
// range does not race a concurrent writer. Both layers are walked so a file
// carrying only render advisories is flushed like any other.
func (s *Server) diagnosticsFiles() []string {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	seen := make(map[string]bool, len(s.diagnostics)+len(s.renderDiags))
	for f := range s.diagnostics {
		seen[f] = true
	}
	for f := range s.renderDiags {
		seen[f] = true
	}
	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// Run anchors the workspace root, loads the merged workspace +
// global config, constructs a Server, and enters the JSON-RPC
// dispatch loop. Returns when stdin reaches EOF, the drain-then-abort
// shutdown sequence completes (the first aggrega/shutdown drains
// accepted in-flight work; a second aborts it), or an unrecoverable I/O
// error occurs.
//
// Workspace-root anchoring:
//   - When cfg.WorkspaceRoot is non-empty, Run uses the explicit
//     path as the root and skips discovery. The cobra wiring is
//     expected to have validated the path; Run re-validates as
//     defense-in-depth.
//   - When cfg.WorkspaceRoot is empty, Run walks upward from the
//     daemon's cwd looking for any of the five marker types per
//     discoverWorkspaceRoot.
//
// Config loading uses the three-level precedence cascade per
// workspaceconfig.LoadWithGlobal — workspace file > global file >
// built-in defaults. The resolved Source is recorded on Server as
// configSource and surfaced in the aggrega/initialize response.
//
// On unrecoverable startup failure (root not discoverable, config
// malformed, explicit AGGREGA_CONFIG pointing at a missing file),
// Run returns an error before entering the dispatch loop. The cobra
// entry point surfaces that error to the process exit code; the IDE
// client interprets non-zero exit as a "language server failed to
// start" condition.
func Run(cfg RunConfig) error {
	if cfg.WorkspaceRoot != "" {
		st, statErr := os.Stat(cfg.WorkspaceRoot)
		if statErr != nil {
			return fmt.Errorf("explicit workspace root %s: %w", cfg.WorkspaceRoot, statErr)
		}
		if !st.IsDir() {
			return fmt.Errorf("explicit workspace root %s is not a directory", cfg.WorkspaceRoot)
		}
	}

	// Single resolution path shared with the CLI: discover the root
	// (or honor the explicit one), cascade-merge the config tree, and
	// report the marker tier for the aggrega/initialize wire token.
	resolved, err := workspaceconfig.Resolve("", cfg.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	root := resolved.Root()
	wsCfg := resolved.Config()
	source := resolved.Source()
	marker := resolved.Marker()

	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	puller := catalog.NewDefaultOCIPuller(wsCfg)
	lazyCatalog := catalog.NewLazyLookup(
		root,
		wsCfg.ArtifactCacheDir,
		wsCfg.Workspace.CatalogDirs,
		puller,
	)
	// The render pipeline's per-request Loaders draw on the process-wide shared
	// compilation cache (see wasm.NewCachingLoaderFromContext): a guest's
	// expensive compilation is paid once for the daemon's life and served warm on
	// every later request, and the single cache is safe for concurrent use across
	// the worker pool. It is a process-lifetime singleton the daemon neither
	// creates, injects, nor closes — it is reclaimed at process exit.
	srv := &Server{
		workspaceRoot:   root,
		configSource:    source.String(),
		compilerVersion: cfg.CompilerVersion,
		workspaceMarker: marker,
		wsConfig:        wsCfg,
		buildOnInspect:  resolveBuildOnInspect(cfg.BuildOnInspect, wsCfg.Serve.BuildOnInspect),
		lazyCatalog:     lazyCatalog,
		parseCache:      newCache(wsCfg.ParseCache.MaxEntries, wsCfg.ParseCache.MaxBytes),
		diagnostics:     map[string][]Diagnostic{},
		renderDiags:     map[string][]Diagnostic{},
		in:              bufio.NewReader(cfg.Stdin),
		out:             cfg.Stdout,
		stderr:          stderr,
	}
	srv.setPositionEncoding(yamlpos.PositionEncodingUTF16)

	// The active-Release cascade and the at-startup lockfile + drift
	// check are NOT run here: they walk the whole workspace tree, and
	// running them before the dispatch loop would delay the answer to
	// aggrega/initialize until the walk finished. They run lazily via
	// ensureCascade on the first request that needs Release context.
	drainTimeout := cfg.DrainTimeout
	if drainTimeout == 0 {
		drainTimeout = defaultDrainTimeout
	}
	return srv.dispatchLoop(drainTimeout)
}

// markCascadeDone marks the cascade guard as already fired without
// running the cascade, so a subsequent ensureCascade is a no-op. It is
// the seam the in-memory test harness uses: those tests build a Server
// as a struct literal (bypassing Run) and inject the active-Release
// fields directly, so the lazy cascade must not run and overwrite the
// injected state with a walk of the empty test workspace.
func (s *Server) markCascadeDone() {
	s.cascadeOnce.Do(func() {})
}

// ensureCascade resolves the active-Release fields (activeRelease,
// activeReleasePath, cascadeAttempt) and runs the at-startup lockfile +
// drift check, exactly once for the daemon's session. Every handler that
// reads one of those fields calls this first, so no handler observes a
// zero-value Release the cascade would have populated.
//
// The work is deferred here rather than run at construction because it
// walks the whole workspace tree: on a large tree the walk takes long
// enough that running it before the dispatch loop would delay the answer
// to aggrega/initialize. The initialize handler does not need Release
// context and does not call this, so the handshake stays fast regardless
// of tree size.
//
// The once's happens-before guarantee publishes the single write to the
// active-Release fields to every worker that later calls ensureCascade
// before reading, so the fields need no per-field lock under the worker
// pool.
func (s *Server) ensureCascade() {
	s.cascadeOnce.Do(func() {
		s.activeRelease, s.activeReleasePath, s.cascadeAttempt = runManifestCascade(s.workspaceRoot, s.wsConfig, s.stderr)

		// At-startup lockfile load + drift check. Best-effort: silent
		// no-op when there is no active Release; stderr-log-and-skip on
		// lockfile load failure or closure-compute failure. The per-
		// handleIrGraph check is the reliable path.
		s.loadAndCheckLockfileAtStartup()
	})
}
