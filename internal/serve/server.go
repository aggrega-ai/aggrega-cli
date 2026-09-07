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
// 🔴 The listing below is a READING AID and is not the enumeration to trust. The
// authoritative one is the availability table in availability.go, which the
// routing index is built from and which Methods() reports — so a method exists
// because a row there names it, and nothing here decides anything. This prose is a
// second copy of that table's method column, and it has drifted from it before:
// treat a method absent here as an omission in this comment rather than as a
// method the daemon does not carry, and read the table when the answer matters.
//
// Requests, each answered with a result or a JSON-RPC error envelope:
//
//	aggrega/initialize             handshake: negotiates the position
//	                               encoding and reports the resolved
//	                               workspace root, the configuration layer
//	                               that supplied it, and the marker that
//	                               anchored it
//	aggrega/validate               parses one file and returns its
//	                               diagnostics, adding the semantic checks
//	                               when the request names a Release to run
//	                               them against, and the publishing-conventions
//	                               findings when serve.lint_on_validate is on
//	aggrega/invalidate             drops cached state for one file or for
//	                               the whole workspace
//	aggrega/diagnostics            returns the diagnostics already cached
//	                               for a file
//	aggrega/lint                   checks catalog publishing conventions over
//	                               one entity, over every entity a Release's
//	                               closure reaches, or over every entity under
//	                               the configured catalog directories
//	aggrega/irGraph                returns the IR graph for a Release
//	aggrega/build                  builds a Release with every gate on and
//	                               writes the artifacts it rendered into the
//	                               workspace's output directory
//	aggrega/buildPreview           renders a Release the way a build does,
//	                               with every gate on, and reports the
//	                               artifacts a build would emit without
//	                               writing any of them
//	aggrega/resolveDependencies    resolves a Release's catalog and source
//	                               dependencies into the cache, writes the
//	                               per-Release lockfile, and reports what it
//	                               pinned
//	aggrega/acquireExternalInputs  pins the external inputs a Release's
//	                               render consumes into the content-addressed
//	                               store and records them in the lockfile,
//	                               reporting the pins it added
//	aggrega/catalogIndex           lists every workspace-local entity's
//	                               kind, name, description, labels and
//	                               source file
//	aggrega/attachCandidates       lists the Features whose declared
//	                               applicability a label set satisfies, for an
//	                               entity, a graph node, or a label set sent
//	                               outright
//	aggrega/connectionCandidates   lists an entity's dependency sockets and,
//	                               per socket, the components whose declared
//	                               labels satisfy what it declares
//	aggrega/releaseCandidates      lists the workspace's Release documents,
//	                               selected by document kind rather than by
//	                               filename
//	aggrega/activeRelease          reports which Release a client should
//	                               select by default, or the empty path and
//	                               the reason when the workspace offers none
//	aggrega/effectiveConfiguration reports the configuration resolved for
//	                               this session and, per setting, the layer
//	                               that determined it
//	aggrega/pluginAdd              searches the configured registry for a plugin
//	                               of the named type and adds it to the
//	                               configuration — a socket attachment, a
//	                               provider capability binding, or a
//	                               command-extension install
//	aggrega/pluginRemove           drops a configured plugin's entry from the
//	                               configuration, failing rather than reporting
//	                               success when the query matches nothing
//	aggrega/pluginSync             aligns a plugin entity's machine-owned records
//	                               — its host-function set and its command mirror
//	                               — with the module those are derived from,
//	                               leaving an already-current file untouched
//	aggrega/pluginGenCatalog       generates one Plugin entity per selected guest
//	                               from its self-report plus an authored overlay,
//	                               writing them or handing the content back
//	aggrega/resolve                position to the definition it names
//	aggrega/findReferences         position to the sites referencing it
//	aggrega/findImplementations    position to the entities implementing it
//	aggrega/hover                  position to hover contents
//	aggrega/completion             position to completion items
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
// the document's canonical path alone — see Paths on the wire below for
// which spelling that is: one daemon serves one workspace and one
// client, so no per-client dimension is needed.
//
// # Paths on the wire
//
// Two rules, and which one applies turns on what the path names rather
// than on which method carries it.
//
// A request naming a document the client already holds open — the file
// or uri parameter of aggrega/validate, aggrega/diagnostics,
// aggrega/invalidate, aggrega/hover, aggrega/completion,
// aggrega/resolve, aggrega/findReferences and
// aggrega/findImplementations — may spell it either way. Both spellings
// name one document and are answered alike, because each is resolved to
// one canonical form at the request boundary before anything reads it.
// The canonical form is the absolute path, which is forced by two
// consumers rather than preferred: the parse cache stats its own key to
// decide whether an entry has gone stale, and the working directory is
// not guaranteed to be the workspace root; and
// aggrega/publishDiagnostics carries the key as the identifier a client
// matches against its own open documents, resolving it as a filesystem
// path. See canonicalFile, which converts nothing itself — pathscope.Resolve
// and catalog.RelativeToWorkspace are the single homes for the two
// directions. A value naming nothing is keyed as it arrived, so such a
// request still draws file-not-validated naming the path it asked about.
//
// A parameter naming a document the daemon must LOCATE inside the
// workspace is the other rule: aggrega/irGraph's release names a Release
// relative to the workspace root the handshake reported, and the daemon
// refuses any other form rather than repairing it. An absolute value, or
// one resolving outside that root, draws the standard invalid-params
// error, whose message states the contract and then the specific fault.
// The refusal is load-bearing rather than defensive — filepath.Join
// concatenates an absolute second operand instead of failing, so a value
// quietly joined has the daemon read, and report diagnostics against, a
// path the workspace never held. Repairing such a value here would teach
// every client that the contract is optional, so the request is rejected
// and its sender corrects it. See resolveReleaseParam.
//
// The two differ because the faults differ. A file parameter names
// something the sender already has, so the only question is which
// document it is, and resolving is a lossless answer. A release
// parameter selects a document out of the workspace, where an absolute
// value silently joined would select outside it.
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
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
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

	// Toolchain is the versions a dependency resolution performed here records
	// into the lockfile it writes: this binary's compiler version and the version
	// of each core plugin it carries. It is supplied by the command layer for the
	// same reason CompilerVersion is — the value describes the binary, which the
	// daemon lives below and cannot read — so a lockfile written here and one
	// written by the command line carry the identical stamp rather than two
	// derivations of it. A nil stamp records none.
	Toolchain *lockfile.ToolchainStamp

	// InstallCLIPlugin installs a command-extension plugin as a top-level command,
	// supplied by the command layer for the same reason CompilerVersion is: the
	// value describes the binary, which the daemon lives below and cannot read.
	//
	// What makes it an injected function rather than something the daemon does
	// itself is the install's collision guard. Installing such a plugin refuses any
	// command name that collides with a built-in, and the authoritative built-in set
	// is the freshly built command tree — which lives in the main package. A second
	// enumeration of it here would drift from the tree that decides them, so the
	// daemon is handed the one implementation instead of reproducing it.
	//
	// Nil for a Server built as a struct literal, which is the in-memory test
	// harness, and nil in any process that did not wire it. A request to add a
	// command-extension plugin is then refused naming that, rather than reporting
	// success over an install that did not happen.
	InstallCLIPlugin pluginops.CLIInstaller

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
// Source names which of the daemon's diagnostic layers raised the entry, so a
// client can group them instead of showing one undifferentiated list. It is
// always present: a client must never have to tell "no source" apart from one of
// the layers, which is why it is stamped at each layer's own boundary rather
// than at the construction sites.
//
// The layers answer different questions about one file, so an entry from each
// can stand on the same line without either being a duplicate of the other: one
// says the deployment is not correct, another that the entity is not
// publishable. A client that showed them as one list would report a single
// problem twice; reading Source is what separates them.
//
// Code carries a stable, machine-matchable code for what the entry reports,
// independent of Message, so a client recognises it without matching prose. One
// key carries the code whoever assigned it, because a consumer asking what a
// finding is does not care who named it, and two keys for one question is a
// fracture rather than a distinction. Which kind of code is in hand follows from
// Source, which already names the layer that raised the entry. On the semantic
// layer the value is the string the failing condition is registered under, so it
// is what a Go consumer of the same failure reads with errors.CodeOf. On the
// build layer it is whatever code the render advisory arrived carrying — assigned
// wherever that advisory was raised, a rendering plugin or the render itself, so
// the code names what the finding is and not who found it — and such a code names
// a class of advisory rather than a registered condition, so it is not a value
// errors.CodeOf ever reports. On the publishing-conventions layer it is the
// identity of the rule that produced the finding, which is the same value the
// structured output of `aggrega lint` carries for that finding, so one rule keeps
// one identity across the two surfaces; that identity likewise names no
// registered condition and is not a value errors.CodeOf reports.
//
// The wire name is the one a machine-matchable code independent of the message
// already carries on the other structured surfaces here — the publishing linter's
// findings and the plugin diagnostic shape — so one concept keeps one spelling
// across them.
//
// It is absent when nothing that produced the entry assigned a code, which is a
// fact about the entry rather than a gap, and both kinds have that case: a
// failure naming no registered condition — a buffer no YAML parser will read, and
// a stage that reports a failure without naming a condition, have none to report
// — and an advisory whose raiser assigned none. So a client treats the key as
// optional and branches on it only when it is there — the same posture the
// end-position keys ask for.
//
// It is not the numeric code on a JSON-RPC error envelope, which names a state
// of the workspace rather than a condition and travels on a different object.
type Diagnostic struct {
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Code      string `json:"code,omitempty"`
	Source    string `json:"source"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
}

// The editor wire's severity tiers: the closed set a [Diagnostic]'s Severity
// field carries.
//
// They are declared rather than written out at each construction site because
// this daemon handles two severity vocabularies that share a word. Rendering
// plugins report halt, warn and info; the editor wire reports error, warning and
// info, and info is spelled the same on both. A bare literal at a construction
// site therefore reads as belonging to either vocabulary, while a named tier says
// which wire the value is going out on.
//
// The values are the JSON strings themselves, so the bytes a client receives are
// a function of these declarations. The [Diagnostic] documentation above names the
// same three strings, because they are the contract a client that reads the
// payload without linking this package works against.
//
// Tests asserting an expected editor severity keep their own literals on purpose.
// An assertion written against these constants would agree with them by
// construction and could no longer catch a change to the value going over the
// wire, so that near-duplicate is deliberate and is not to be unified away.
const (
	editorSeverityError   = "error"
	editorSeverityWarning = "warning"
	editorSeverityInfo    = "info"
)

// diagnosticCode returns the code of the condition err's chain names, or the
// empty string when the chain names none.
//
// CodeOf's second return is deliberately dropped here rather than at each caller.
// On this wire the empty string and the omitted key are the same statement — the
// entry names no condition — so a boolean beside it would carry nothing a client
// could act on, and a per-site choice about it is a choice eleven construction
// sites would each have to make the same way.
func diagnosticCode(err error) string {
	code, _ := cerrors.CodeOf(err)
	return code
}

// The layer sources. DiagnosticSourceSemantic covers everything the resolution
// and analysis stages report about the authored documents;
// DiagnosticSourceBuild covers the advisories a render raises, which are the
// findings `aggrega build` prints; DiagnosticSourceConventions covers the
// publishing-conventions findings, which are the findings `aggrega lint` prints.
// So each value names the command whose output the findings are rather than an
// internal stage, and a client that has run one of those commands already knows
// what an entry under that source is about.
const (
	DiagnosticSourceSemantic    = "aggrega"
	DiagnosticSourceBuild       = "aggrega-build"
	DiagnosticSourceConventions = "aggrega-lint"
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
// No field holds a Release. A request that acts on one names it, and the
// resolution is request-scoped from end to end, so there is no shared
// Release state for the pool to synchronize and none to invalidate when
// the workspace changes underneath the daemon.
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
	// toolchain is the stamp a dependency resolution performed here records into
	// the lockfile it writes, injected from the command layer
	// (RunConfig.Toolchain) for the same reason compilerVersion is. Nil for a
	// Server built as a struct literal, which is the in-memory test harness: the
	// stamp is advisory, so a lockfile written without one is complete and simply
	// records nothing about which binary produced it.
	toolchain *lockfile.ToolchainStamp
	// workspaceMarker names the marker tier that anchored the
	// workspace root via discovery. The zero value (no marker) when
	// --workspace-root was supplied explicitly, since no marker was
	// consulted in that path. Surfaced on aggrega/initialize via its
	// String() wire token.
	workspaceMarker workspaceconfig.Marker
	wsConfig        workspaceconfig.Config
	// resolvedWorkspace is the workspace resolution the daemon performed at
	// startup, kept whole rather than only as the values derived from it above.
	// It is what answers a request for the effective configuration and each
	// setting's originating layer, so that answer is the same resolution the
	// handshake's workspace root and configuration layer already report and
	// cannot disagree with them.
	//
	// Nil only for a Server built as a struct literal, which is the in-memory
	// test harness rather than any production path: Run always sets it.
	resolvedWorkspace *workspaceconfig.ResolvedWorkspace
	// installCLIPlugin installs a command-extension plugin as a top-level command,
	// injected from the command layer (RunConfig.InstallCLIPlugin) because the
	// install's collision guard reads the freshly built command tree, which lives
	// in the main package. Nil for a Server built as a struct literal, and nil in
	// any process that did not wire it; the handler refuses rather than reporting
	// an install that did not happen.
	installCLIPlugin pluginops.CLIInstaller
	// buildOnInspect is the resolved serve build-on-inspect dial (the
	// --build-on-inspect flag overriding the serve.build_on_inspect config
	// key). When false (the default), handleIrGraph returns the IR graph
	// without running the artifact-rendering stage; when true that stage runs,
	// so the advisories it raises surface as an additional diagnostic layer.
	// It selects that stage and nothing else: the preview posture is the
	// inspection one under either value, so the dial turns no gate on. Written
	// once at construction, then only read during handler execution — an
	// immutable snapshot needing no lock.
	buildOnInspect bool

	// lintOnValidate is the resolved serve.lint_on_validate dial. When false —
	// the default — a validation reports the correctness layers alone and no
	// publishing-conventions finding reaches a client. When true, a validation of
	// a catalog entity also runs the conventions rules over the buffer it
	// validated and publishes what they found as a third layer.
	//
	// It gates that layer and nothing else. aggrega/lint answers whatever the
	// request asks for under either value, because a caller sending it has asked
	// for the check by name; the dial decides only whether the check also runs
	// where nobody asked for it. Written once at construction, then only read
	// during handler execution — an immutable snapshot needing no lock.
	//
	// There is no flag counterpart, unlike buildOnInspect. The question the dial
	// answers is about a workspace's own content, and whoever has that question
	// is authoring the workspace rather than launching the daemon — an author
	// never passes this process its arguments, their editor does.
	lintOnValidate bool

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
	// other's findings. Reads compose every layer; writes only ever touch one.
	renderDiags map[string][]Diagnostic

	// conventionDiags is the third diagnostic layer, guarded by the same diagMu:
	// the publishing-conventions findings for one authored file, keyed by that
	// file. It is kept apart from the semantic layer even though both refresh on
	// the same event, and what separates them is the source stamp rather than the
	// refresh schedule: the semantic write stamps its whole slice, so a
	// conventions finding appended to it would be published as a finding about
	// correctness and a client could no longer tell the two questions apart.
	conventionDiags map[string][]Diagnostic

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

// canonicalFile renders a path naming a document into the one spelling every
// per-file store here is keyed by: that document's absolute path. A request may
// name a file relative to the workspace root or by absolute path, and both name
// the same document, so what a request is answered with depends on the document
// it named rather than on how it spelled it.
//
// Absolute is the canonical form because two consumers of the key require it and
// a workspace-relative one satisfies neither. The parse cache stats its own key
// to decide whether an entry went stale, and the daemon's working directory is
// not guaranteed to be the workspace root — root discovery walks upward from
// that directory, and an explicit root overrides discovery outright — so a
// workspace-relative key can resolve against some other directory or against
// nothing, and every entry then reads as stale. And the publishDiagnostics
// notification carries the key as the identifier a client matches against its
// own open documents, which it does by resolving it as a filesystem path.
//
// No path conversion is implemented here. pathscope.Resolve is the single home
// for the relative-to-absolute direction, the same one the Release parameter is
// resolved through, and catalog.RelativeToWorkspace is the single home for the
// opposite direction, which runTier4Validation uses where the comparison wanted
// is against the IR's own coordinate system. An absolute value is only cleaned,
// so two absolute spellings of one document — one of them carrying an interior
// traversal or a doubled separator — agree on a key.
//
// A value naming nothing is left where it stands, so such a request still
// answers as it did before: the empty string names no document; an absolute path
// stays absolute whether or not it resolves to a file, which is what keeps a
// request for a path outside the workspace answering as it did; and a relative
// path climbing out of the workspace is refused by pathscope.Resolve rather than
// silently joined onto the root, so resolving cannot turn an unknown path into a
// match for some other document. A Server holding no workspace root leaves a
// relative value untouched for the same reason — there is nothing to resolve it
// against.
func (s *Server) canonicalFile(file string) string {
	if file == "" {
		return file
	}
	if filepath.IsAbs(file) {
		return filepath.Clean(file)
	}
	if abs, ok := pathscope.Resolve(s.workspaceRoot, file); ok {
		return abs
	}
	return file
}

// setDiagnostics records the diagnostics slice for file under diagMu.
// A nil slice is stored verbatim (it reads back as an empty result).
// This is the single write entry point for the diagnostics map; all
// handler write sites route through it so the locking discipline has
// one home — which is also why the semantic layer's source is stamped
// here rather than at each construction site, and why the key's
// coordinate system is fixed here too: a write site cannot key the map
// by a spelling the matching read would then miss.
func (s *Server) setDiagnostics(file string, diags []Diagnostic) {
	stampSource(diags, DiagnosticSourceSemantic)
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	s.diagnostics[s.canonicalFile(file)] = diags
}

// dropDiagnostics removes file's diagnostics entry under diagMu. No-op
// when the entry is absent.
func (s *Server) dropDiagnostics(file string) {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	delete(s.diagnostics, s.canonicalFile(file))
}

// setConventionDiagnostics records the publishing-conventions findings for file
// under diagMu, replacing whatever that layer held for it. A nil or empty slice
// clears the entry rather than storing one, so a document whose findings were all
// fixed stops carrying them — and so does a document validated while the dial is
// off, which is what keeps the off position an absence rather than a stale
// memory of an earlier on position.
//
// It is the single write entry point for that layer, which is why the layer's
// source is stamped here rather than at the mapping site: the stamp is a fact
// about which layer published the entry, and a construction site is not entitled
// to claim otherwise.
func (s *Server) setConventionDiagnostics(file string, diags []Diagnostic) {
	key := s.canonicalFile(file)
	stampSource(diags, DiagnosticSourceConventions)
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	if s.conventionDiags == nil {
		s.conventionDiags = map[string][]Diagnostic{}
	}
	if len(diags) == 0 {
		delete(s.conventionDiags, key)
		return
	}
	s.conventionDiags[key] = diags
}

// snapshotDiagnostics returns a copy of file's composed diagnostics taken under
// diagMu: the semantic layer first, then the render layer, then the
// publishing-conventions layer. The copy is what callers marshal or return on
// the wire, so the marshal runs off the lock and never races a concurrent
// writer's whole-slice reassignment. Composition happens here, at the single
// read entry point, so no caller can publish one layer and silently drop
// another. Every layer empty returns nil.
//
// The layers are read at the one key canonicalFile fixes, so they are looked up
// in a single coordinate system. That matters here more than at any other site:
// the layers are written by different requests — the semantic and conventions
// layers by a validation, the render layer by an inspection — and each request
// names the file in whatever spelling its sender chose, so a key carrying a
// per-request spelling would compose one layer with nothing.
func (s *Server) snapshotDiagnostics(file string) []Diagnostic {
	key := s.canonicalFile(file)
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	semantic := s.diagnostics[key]
	render := s.renderDiags[key]
	conventions := s.conventionDiags[key]
	if len(semantic)+len(render)+len(conventions) == 0 {
		return nil
	}
	out := make([]Diagnostic, 0, len(semantic)+len(render)+len(conventions))
	out = append(out, semantic...)
	out = append(out, render...)
	out = append(out, conventions...)
	return out
}

// setRenderDiagnostics replaces the whole render layer with byFile and returns
// the sorted set of files whose entry the replacement touched — the union of the
// files that had render diagnostics and the files that now do — so the caller
// republishes exactly the files whose composed state changed, clearing a stale
// advisory as reliably as it publishes a new one. Whole-layer replacement is
// what a whole-closure render calls for: a per-file merge would strand an
// advisory on a file that stopped raising one.
//
// Each incoming key is canonicalised, so this layer and the semantic one are
// keyed alike however the render attributed its advisories. The returned file
// set is therefore in the canonical spelling too, which is what the caller
// publishes — the notification's file identifier and the store's key are then
// the same string, rather than two spellings of one document.
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
		key := s.canonicalFile(f)
		next[key] = stampSource(d, DiagnosticSourceBuild)
		touched[key] = true
	}
	s.renderDiags = next
	files := make([]string, 0, len(touched))
	for f := range touched {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// diagnosticsFiles returns the keys of every diagnostic layer, deduplicated and
// sorted, under diagMu. Used by the initialize startup-flush so the whole-map
// range does not race a concurrent writer. Every layer is walked so a file
// carrying only render advisories, or only conventions findings, is flushed like
// any other.
func (s *Server) diagnosticsFiles() []string {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	seen := make(map[string]bool, len(s.diagnostics)+len(s.renderDiags)+len(s.conventionDiags))
	for f := range s.diagnostics {
		seen[f] = true
	}
	for f := range s.renderDiags {
		seen[f] = true
	}
	for f := range s.conventionDiags {
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
	// The daemon's session catalog reads only what is already on disk. Nil
	// puller: Tier-3 is disabled, so no resolution through this lookup reaches a
	// registry — the same posture the command-line build resolves under, and the
	// same one this daemon already holds on its two other acquisition seams (the
	// external-input seam serves pinned references from the local store with
	// acquisition off, and the closure reconstruction carries a nil puller). An
	// editor session inspects the workspace as it stands; acquiring is what the
	// verbs a user runs deliberately are for. A daemon that fetched on its own
	// would pull content no command asked for and preview bytes a build of the
	// same Release would not produce.
	//
	// Pins are deliberately NOT threaded here. This lookup is shared across
	// concurrently-served requests, so a pin set installed on it would resolve
	// another request's reads against the wrong Release; each request forks its
	// own pinned view via ForRequest, which is where verify-on-read takes its
	// pins. The nil puller is what keeps that fork honest: a request that finds
	// no lockfile threads no pins, and an unpinned Tier-2 slot is then reported
	// unavailable for that request rather than re-fetched and previewed
	// unverified.
	lazyCatalog := catalog.NewLazyLookup(
		root,
		wsCfg.ArtifactCacheDir,
		wsCfg.Workspace.CatalogDirs,
		nil,
	)
	// The render pipeline's per-request Loaders draw on the process-wide shared
	// compilation cache (see wasm.NewCachingLoaderFromContext): a guest's
	// expensive compilation is paid once for the daemon's life and served warm on
	// every later request, and the single cache is safe for concurrent use across
	// the worker pool. It is a process-lifetime singleton the daemon neither
	// creates, injects, nor closes — it is reclaimed at process exit.
	srv := &Server{
		workspaceRoot:     root,
		configSource:      source.String(),
		compilerVersion:   cfg.CompilerVersion,
		toolchain:         cfg.Toolchain,
		workspaceMarker:   marker,
		wsConfig:          wsCfg,
		resolvedWorkspace: resolved,
		installCLIPlugin:  cfg.InstallCLIPlugin,
		buildOnInspect:    resolveBuildOnInspect(cfg.BuildOnInspect, wsCfg.Serve.BuildOnInspect),
		lintOnValidate:    wsCfg.Serve.LintOnValidate,
		lazyCatalog:       lazyCatalog,
		parseCache:        newCache(wsCfg.ParseCache.MaxEntries, wsCfg.ParseCache.MaxBytes),
		diagnostics:       map[string][]Diagnostic{},
		renderDiags:       map[string][]Diagnostic{},
		conventionDiags:   map[string][]Diagnostic{},
		in:                bufio.NewReader(cfg.Stdin),
		out:               cfg.Stdout,
		stderr:            stderr,
	}
	srv.setPositionEncoding(yamlpos.PositionEncodingUTF16)

	// No workspace-wide walk runs here. The daemon holds no Release of
	// its own, so there is nothing to resolve at construction: a request
	// that acts on a Release names one, and the query reporting which
	// Release a client should select by default performs the walk when it
	// is asked. That keeps the answer to aggrega/initialize independent of
	// tree size.
	drainTimeout := cfg.DrainTimeout
	if drainTimeout == 0 {
		drainTimeout = defaultDrainTimeout
	}
	return srv.dispatchLoop(drainTimeout)
}
