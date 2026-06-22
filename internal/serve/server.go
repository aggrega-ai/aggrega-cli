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
	"fmt"
	"io"
	"os"
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
// No mutex on Server's mutable state. The dispatch loop runs a reader
// goroutine that consumes framed JSON-RPC messages from stdin and
// pushes them onto a channel; the main goroutine selects on that
// channel and runs handlers synchronously. Handlers — the only writers
// of Server fields — execute in the main goroutine only. The reader
// goroutine reads from stdin and writes to channels; it never touches
// Server mutable state. The previous "single goroutine" architecture
// served the same no-mutex invariant; the reader goroutine was
// introduced so the aggrega/shutdown drain timer can start at
// notification arrival rather than after the in-flight handler
// returns.
//
// Handlers run on a worker pool whose default size is one, so they
// execute serially in arrival order and this no-mutex invariant holds
// as stated; the id-order stdout serializer is the sole owner of the
// output writer.
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

	// shuttingDown is set to true by dispatchShutdown after an
	// aggrega/shutdown notification arrives. The drain-state
	// pre-check in handle consults this flag to reject subsequent
	// requests with errShuttingDown and drop subsequent
	// notifications.
	shuttingDown bool

	// serverCtx is the session-level context every per-request context
	// derives from; serverCancel cancels it. The reader goroutine calls
	// serverCancel the moment an aggrega/shutdown notification arrives,
	// which cancels the derived context of any in-flight handler so it
	// can return within the drain budget instead of running to
	// completion. Both are initialised in dispatchLoop. serverCancel is
	// idempotent — the dispatch loop also defers it as an exit backstop.
	serverCtx    context.Context
	serverCancel context.CancelFunc

	// lazyCatalog is the per-URI Tier-1/2/3 cascade for the WHOLE catalog
	// surface — Component + Feature (on-demand cascade) plus the tolerantly-
	// enumerated Recipe / FeatureRecipe / Aggregate / Provider / SchemaLibrary /
	// MappingLibrary shell (recipe resolution, sub-aggregate loading, the closure
	// walk, and closure-scoped Pass-2). Constructed once at startup via
	// catalog.NewLazyLookup; no per-request reconstruction, no eager whole-catalog
	// load (0090 point-7). The struct carries its own session-scoped hot cache +
	// negative cache; consumers obtain resolver.CatalogLookup via AsCatalogLookup,
	// the recipe surfaces via RecipeLookupWithManifest / SubAggregateLoader /
	// MappingLibraries, and the closure walk via ClosureLookup. Reset() on
	// invalidation re-walks a changed workspace.
	lazyCatalog *catalog.LazyLookup

	// activeRelease is the workspace's session-scoped active Release,
	// resolved by the four-step disambiguation cascade at Run
	// startup. nil when the cascade exhausted into Step 4 fallback
	// (no authoring_release config, not exactly one Release file,
	// no .aggrega/active-release marker). Operations needing Release
	// context consult cascadeAttempt + cascadeDiagnosticMessage to
	// construct error-envelope text when activeRelease is nil.
	//
	// Subprocess-test coverage of the cascade-via-Run wiring is
	// deferred: no operation currently reads activeRelease and
	// surfaces it through a wire response, so an external observer
	// cannot assert "the cascade populated this field correctly".
	// The cascade's correctness is pinned by direct unit tests on
	// runManifestCascade in manifest_cascade_test.go; the Run-time
	// invocation itself is covered only by compile + vet plus the
	// implicit panic-free-startup behavior the existing subprocess
	// tests exercise. When a future operation reads activeRelease
	// and surfaces it (Tier-4 validate's execution path, or a
	// dedicated query like aggrega/activeRelease), add a subprocess
	// test that constructs a workspace matching each cascade step's
	// win condition and asserts the operation's response carries the
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
	// alongside activeRelease at Run startup; immutable for the
	// daemon's session lifetime.
	cascadeAttempt cascadeAttempt

	// lockfile is nil in this scaffold; populated per active Release
	// when the lockfile machinery ships.
	lockfile *lockfile.Lockfile

	parseCache  *cache
	diagnostics map[string][]Diagnostic

	in     *bufio.Reader
	out    io.Writer
	stderr io.Writer

	// positionEncoding selects how Position.Character offsets are
	// interpreted in JSON-RPC requests + responses. Defaults to
	// UTF-16 (LSP 3.17 protocol default). The aggrega/initialize
	// handler may overwrite this when the client negotiates a
	// different encoding via general.positionEncodings capability.
	// Subsequent LineIndex constructions in the daemon read this
	// field via BuildLineIndexWithEncoding.
	positionEncoding yamlpos.PositionEncoding

	// testInDispatchHook, when non-nil, runs at the top of handle()
	// before per-method dispatch. The production setup never sets
	// this field — it is the test seam tests use to inject
	// controllable handler latency for timing-sensitive coverage
	// (e.g., distinguishing arrival-based from observation-based
	// drain-timer semantics). A nil hook is a no-op.
	testInDispatchHook func()
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
	activeRelease, activeReleasePath, attempt := runManifestCascade(root, wsCfg, stderr)
	srv := &Server{
		workspaceRoot:     root,
		configSource:      source.String(),
		compilerVersion:   cfg.CompilerVersion,
		workspaceMarker:   marker,
		wsConfig:          wsCfg,
		lazyCatalog:       lazyCatalog,
		activeRelease:     activeRelease,
		activeReleasePath: activeReleasePath,
		cascadeAttempt:    attempt,
		parseCache:        newCache(wsCfg.ParseCache.MaxEntries, wsCfg.ParseCache.MaxBytes),
		diagnostics:       map[string][]Diagnostic{},
		in:                bufio.NewReader(cfg.Stdin),
		out:               cfg.Stdout,
		stderr:            stderr,
		positionEncoding:  yamlpos.PositionEncodingUTF16,
	}

	// At-startup lockfile load + drift check. Best-effort: silent
	// no-op when there is no active Release; stderr-log-and-skip on
	// lockfile load failure or closure-compute failure. The per-
	// handleIrGraph check is the reliable path.
	srv.loadAndCheckLockfileAtStartup()

	drainTimeout := cfg.DrainTimeout
	if drainTimeout == 0 {
		drainTimeout = defaultDrainTimeout
	}
	return srv.dispatchLoop(drainTimeout)
}
