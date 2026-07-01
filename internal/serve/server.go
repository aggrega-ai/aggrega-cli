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
// spawns the daemon as a subprocess.
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

// defaultDrainTimeout bounds how long the dispatch loop waits after
// the reader goroutine observes an aggrega/shutdown notification
// before returning. The timer starts at notification ARRIVAL (in the
// reader), not at observation in the main loop — so worst-case exit
// latency is max(in_flight_handler_runtime, defaultDrainTimeout).
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

	// DrainTimeout sets the budget the dispatch loop waits after
	// observing an aggrega/shutdown notification before returning.
	// Zero value defaults to defaultDrainTimeout (2 seconds). Tests
	// pass a short value (e.g., 50ms) so shutdown behavior exercises
	// in subsecond wall time.
	DrainTimeout time.Duration

	// CompilerVersion is the binary's build identity, stamped into the IR
	// provenance the daemon produces for inspect and Tier-4 diagnostics so its
	// output names the same compiler version the CLI reports. The command layer
	// derives it (the daemon cannot, living below the main package); an empty
	// value stamps an empty compiler-version.
	CompilerVersion string
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
type Diagnostic struct {
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
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
//   - The per-file diagnostics map is guarded by diagMu. Publication
//     copies the file's slice under the lock and marshals outside it.
//   - positionEncoding is held in an atomic value: the initialize
//     handler stores it, every position-mapping site loads it.
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

	// shuttingDown is set at the dispatch loop's acceptance of an
	// aggrega/shutdown message — on the single-threaded main loop, so
	// every message accepted afterward strictly observes the flag —
	// and is idempotently re-set by dispatchShutdown on the worker.
	// The acceptance-boundary gate consults it to reject subsequent
	// requests with errShuttingDown and drop subsequent notifications
	// before they reach a worker; once a message clears that gate and
	// reaches a worker, dispatch processes it normally. Atomic because
	// the main loop and the worker store it on different goroutines
	// under the pool, while the main loop also loads it at the gate.
	shuttingDown atomic.Bool

	// serverCtx is the session-level context every per-request context
	// derives from; serverCancel cancels it. The reader goroutine calls
	// serverCancel the moment an aggrega/shutdown notification arrives,
	// which cancels the derived context of any in-flight handler so it
	// can return within the drain budget instead of running to
	// completion. Both are initialised in dispatchLoop. serverCancel is
	// idempotent — the dispatch loop also defers it as an exit backstop.
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
	// Provider / SchemaLibrary / MappingLibrary shell. Constructed once at startup
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
// one home.
func (s *Server) setDiagnostics(file string, diags []Diagnostic) {
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

// snapshotDiagnostics returns a copy of file's diagnostics slice taken
// under diagMu. The copy is what callers marshal or return on the wire,
// so the marshal runs off the lock and never races a concurrent
// writer's whole-slice reassignment. A missing entry returns nil.
func (s *Server) snapshotDiagnostics(file string) []Diagnostic {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	diags, ok := s.diagnostics[file]
	if !ok || len(diags) == 0 {
		return nil
	}
	out := make([]Diagnostic, len(diags))
	copy(out, diags)
	return out
}

// diagnosticsFiles returns the diagnostics map's keys, sorted, under
// diagMu. Used by the initialize startup-flush so the whole-map range
// does not race a concurrent writer.
func (s *Server) diagnosticsFiles() []string {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	files := make([]string, 0, len(s.diagnostics))
	for f := range s.diagnostics {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// Run anchors the workspace root, loads the merged workspace +
// global config, constructs a Server, and enters the JSON-RPC
// dispatch loop. Returns when stdin reaches EOF, the dispatch loop's
// drain timer fires after an aggrega/shutdown notification, or an
// unrecoverable I/O error occurs.
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
	srv := &Server{
		workspaceRoot:   root,
		configSource:    source.String(),
		compilerVersion: cfg.CompilerVersion,
		workspaceMarker: marker,
		wsConfig:        wsCfg,
		lazyCatalog:     lazyCatalog,
		parseCache:      newCache(wsCfg.ParseCache.MaxEntries, wsCfg.ParseCache.MaxBytes),
		diagnostics:     map[string][]Diagnostic{},
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
