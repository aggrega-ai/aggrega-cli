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
//	                               encoding, receives the workspace the
//	                               client declares for pushed diagnostics,
//	                               and reports that workspace's root, the
//	                               configuration layer that supplied it,
//	                               and the marker that anchored it
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
//	                               dependencies into the cache, pins the
//	                               external inputs its render consumes into
//	                               the content-addressed store, writes the
//	                               per-Release lockfile, and reports what it
//	                               pinned
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
//	aggrega/import                 converts a foreign source description into
//	                               draft authoring files through the importer the
//	                               catalog resolves for the source's format,
//	                               writing them into a directory the request names
//	                               or handing the content back
//	aggrega/logout                 removes the stored credential for the registry
//	                               the request names, from the store every project
//	                               on the machine shares; a host the store holds
//	                               nothing for is removed successfully
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
// aggrega/validate is the only method that populates a file's parse cache entry
// and its diagnostic layers. aggrega/diagnostics, aggrega/hover,
// aggrega/completion, aggrega/resolve, aggrega/findReferences and
// aggrega/findImplementations all read that state and refuse with the
// file-not-validated application code when it is absent, so a client sends
// validate for a file before any of them. That state belongs to the workspace
// the request named — each workspace holds its own parse cache and its own
// diagnostic layers — and within a workspace the entries are keyed by the
// document's canonical path alone; see Paths on the wire below for which
// spelling that is. One daemon serves one client at a time, so the key needs no
// per-client dimension.
//
// The parse cache and the diagnostic layers are TWO stores, seeded at two
// moments inside one validation, and one reader spans both: aggrega/diagnostics
// decides out of the parse cache and answers out of the layers. So a reader of a
// file whose validation is already running WAITS for that validation and is
// answered with what it found. The five other readers decide and answer out of
// the parse cache alone — one store, complete when it is written — so they have
// nothing to wait for and never wait.
//
// Waiting is why the file-not-validated code reports one state rather than two.
// It means nothing has validated this file and nothing is validating it, and a
// client answers it by sending a validation. A file that IS being validated is
// reported by the separate validation-in-flight code, and only when the reader's
// wait ran out of budget; a client answers that one by asking again. See
// handleDiagnostics for the wait, and validationTracker for why it holds nothing
// across a validation and cannot deadlock against the worker pool.
//
// # The workspace is a parameter, not a property of the process
//
// Every request that acts on a workspace names one, in a `workspace`
// parameter carrying that workspace's absolute root. The daemon holds no
// workspace of its own and remembers none between requests, so the same
// question asked about two workspaces gets two answers and neither can
// see the other's entities, Releases or diagnostics. A request that
// names none is refused, naming the parameter. Three methods take no
// such parameter because their subject is not a workspace: the schema
// report and the document skeleton, both derived from the schema set
// compiled into this binary, and the handshake, which declares a
// workspace rather than acting on one.
//
// The state each workspace earns is KEYED rather than replaced, so a
// second workspace gets its own entry instead of evicting the first's.
// A question about somewhere else therefore costs whoever is typing
// nothing.
//
// # Where a pushed diagnostic goes
//
// The daemon pushes aggrega/publishDiagnostics only for the workspace
// the client declared at the handshake — its optional `workspace`
// field, defaulting to the workspace the process was launched over.
// Every other request answers with its findings in its own response and
// pushes nothing. Pushing is what keeps an editor's problem list current
// while somebody types, and it is the wrong thing to do with an answer
// nobody asked to see: without the confinement, a question about a
// second workspace would fill a reader's problem list with findings
// about files they never opened.
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
// path. See canonicalFile, which converts nothing itself. A value naming
// nothing is keyed as it arrived, so such a request still draws
// file-not-validated naming the path it asked about.
//
// Three questions about a path are asked here and each has one home, so a
// caller picks by which question it is asking rather than by which helper
// is nearest. pathscope.Resolve composes a root and a relative name into
// the path to open, refusing one that would escape the root.
// catalog.RelativeToWorkspace normalises a path into the IR's
// workspace-relative coordinate, with no containment check, because that
// is the byte-identity property the IR wire shape requires — a catalog
// outside the root legitimately normalises to a climb there.
// pathscope.ReportedName names a path for a reader outside this process:
// relative to the root for a document inside it, absolute for one outside,
// and never empty for a path the daemon holds.
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

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
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

	// ResolveImportPlugin resolves an importer or parser plugin URI to its module
	// bytes, supplied by the command layer for the same reason InstallCLIPlugin
	// is: the resolution it performs is assembled there.
	//
	// The plugin cascade it binds — the entity-fronted resolution, the
	// content-cache tier, and the per-URI pins the installed command-extension
	// plugins carry — is the one the install path, the plugin listing and the
	// runtime plugin runner all build from, so a URI one path accepts every other
	// path resolves identically. A second cascade built here would agree with that
	// one on the day it was written and drift silently afterwards.
	//
	// Nil for a Server built as a struct literal, which is the in-memory test
	// harness, and nil in any process that did not wire it. An import request is
	// then refused naming that, rather than reporting a conversion that did not
	// happen.
	ResolveImportPlugin importops.PluginBytesResolver

	// BuildOnInspect, when non-nil, overrides the serve.build_on_inspect
	// config-file key for this daemon invocation: the command layer sets it
	// from the --build-on-inspect flag only when the operator passed the
	// flag, so an unset flag (nil) leaves the config-file value in force. It
	// controls whether the IR-graph inspection request runs the
	// artifact-rendering stage — true publishes the advisories that stage
	// raises as an additional diagnostic layer; false (the default) returns
	// the graph without running it. Nothing is written to disk either way.
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
//
// ShortCode and CodeDescription are what the entry carries for the READER rather
// than for the program, and they are documented on [ConditionCodes], where the
// division between the three keys is stated once.
//
// Classification, Suggest and KeyPath are the facts a producer computed that the
// message does not contain, and they are documented at their declarations below.
// They are keys rather than sentences folded into Message for the reason Message
// itself is carried verbatim: a fact a program can read is worth more than the
// same fact spelled inside prose a program would have to parse.
type Diagnostic struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`

	// The identifiers of what the entry reports. Embedded so the keys sit on the
	// entry itself rather than nested under one of their own, and so the only way
	// to fill them is a constructor that decides all three together.
	ConditionCodes

	// Classification is the version-impact verdict the entry reports, when the
	// diagnostic it was projected from carried one. It is the producer's verdict
	// passed through rather than recomputed here: deciding what a change does to a
	// version is a rule's judgement, and a second derivation at this boundary
	// would be a copy that agrees until it does not.
	Classification string `json:"classification,omitempty"`

	// Suggest is the remediation the producer offered alongside the diagnostic,
	// carried through as the producer worded it.
	//
	// It is the part of a finding a reader ACTS on, so dropping it leaves a client
	// showing what is wrong and withholding what to do — which makes the reader
	// diagnose the same finding a second time on a surface that already had the
	// answer. It is absent where the producer offered no hint, which is a fact
	// about that producer rather than a gap here.
	Suggest string `json:"suggest,omitempty"`

	// KeyPath is the dotted path into the authored document that the diagnostic's
	// own location carried, when its producer located the issue structurally
	// rather than by line.
	//
	// It is the producer's coordinate, and it answers a different question from
	// the position keys above: those say where the entry is DRAWN, this says what
	// the producer NAMED. A client that re-anchors an entry after an edit, or
	// groups entries by the document element they concern, needs the coordinate
	// and cannot recover it from a line. An entry whose producer carried no such
	// location — a Go condition reported through this wire — carries none.
	KeyPath string `json:"keyPath,omitempty"`

	Source    string `json:"source"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
}

// ConditionCodes are the identifiers a [Diagnostic] carries for what it reports:
// the one a program matches on, the one a reader quotes, and where a reader goes
// for more.
//
// # Why there are three keys and not one
//
// A condition carries two identifiers and they are shaped for two jobs. The
// descriptive code is qualified and hyphenated, so it says what the condition is
// and is what a consumer branches on. The short code is the letter E and four
// digits, so it fits at the head of a printed line and is what a reader can quote
// in a report, type into a search, or look up. Neither replaces the other, and a
// single key carrying whichever one the layer happened to have would be one key
// answering two questions — so a client holding it could not tell which it had.
//
// CodeDescription is the third because it answers neither question: it is where
// the condition is explained, which is a fact about the documentation rather than
// about the entry. It is the protocol's own structure for exactly this, so a
// protocol-aware client renders a "learn more" affordance without being taught
// anything about this compiler, and no client needs its own copy of the
// explanations.
//
// # When each key is absent
//
// All three are optional, and what decides their presence is what UNDERLIES the
// entry rather than which layer published it. That distinction is worth stating
// because the layer is nearly a proxy for it and is not one: an entry whose code
// names a registered halting condition carries all three keys wherever it is
// published, and an entry whose code names something else carries the descriptive
// code alone.
//
// An entry naming no condition at all carries no key. A buffer no YAML parser will
// read has none behind it, and every key would be a claim about a condition that
// does not exist.
//
// The codes that are NOT conditions are a class of render advisory and the
// identity of a conventions rule. Neither is registered, so neither has a short
// code, and neither is documented on the page the pointer resolves to — a short
// code minted for them would be an identifier no lookup resolves, and a pointer
// would resolve to a page that does not describe what the entry reported. Those
// are the ordinary content of the build and publishing-conventions layers, which
// is why the layer nearly predicts the keys; the build layer nonetheless carries
// one entry that does name a registered condition, and it carries all three.
//
// So a client tells the three cases apart from the keys alone, and does it without
// knowing anything about the layers: no code at all, a code that resolves to
// nothing further, and a code with a quotable form and somewhere to read about it.
type ConditionCodes struct {
	// Code is the machine-matchable code, described in full on [Diagnostic].
	Code string `json:"code,omitempty"`

	// ShortCode is the condition's compact code — the letter E and four digits —
	// which is the identifier a reader quotes and looks up.
	//
	// It travels beside Code rather than replacing it because the two reach
	// different consumers, and because Code's contract is that a client matches on
	// it: changing what that key carried would break every consumer of it without
	// a single one failing loudly.
	//
	// The key is spelled the way this wire spells a name of more than one word,
	// which is how nearly every key on it is spelled. The two position keys on the
	// diagnostic are the exception and not the rule — they predate the convention
	// and are the shape a client already binds to — so a new key follows the wire
	// rather than its two nearest neighbours, and sits beside the protocol's own
	// key below without introducing a third spelling into one object.
	ShortCode string `json:"shortCode,omitempty"`

	// CodeDescription is where the condition is explained.
	//
	// The key is spelled as the protocol spells it, rather than in this wire's own
	// convention for its own keys, because it IS the protocol's field: a client
	// that knows the protocol and nothing about this compiler renders it, and a
	// key renamed to match the neighbours would be a bespoke field wearing the
	// protocol's meaning.
	CodeDescription *CodeDescription `json:"codeDescription,omitempty"`
}

// CodeDescription is the protocol's structure for a reference to open for more
// information about a diagnostic.
type CodeDescription struct {
	// Href is the address the reference resolves to.
	Href string `json:"href"`
}

// conditionCodesOf returns the identifiers of the condition err's chain names, or
// the zero value when the chain names none.
//
// It is the one place the three keys are decided together, and that is what it is
// for. Reading the codes at each construction site would let a site report a code
// while omitting the quotable one — which is a diagnostic a reader cannot look up,
// reported by a surface that had the answer in hand. Deciding all three here means
// a site cannot report a partial answer by omission.
//
// The pointer is present exactly when the short code is, because the page it
// resolves to is keyed on the short code: an entry with no short code has nothing
// to look up there, and a pointer sent anyway would resolve to a page that does
// not document what the entry reports.
//
// The second return of each read is deliberately dropped here rather than at each
// caller. On this wire the empty string and the omitted key are the same statement
// — the entry names no condition — so a boolean beside it would carry nothing a
// client could act on, and a per-site choice about it is a choice every
// construction site would have to make the same way.
func conditionCodesOf(err error) ConditionCodes {
	code, _ := cerrors.CodeOf(err)
	shortCode, _ := cerrors.ShortCodeOf(err)
	codes := ConditionCodes{Code: code, ShortCode: shortCode}
	if shortCode != "" {
		codes.CodeDescription = &CodeDescription{Href: haltref.ExplanationURL()}
	}
	return codes
}

// classCode returns the identifiers for an entry whose code names a CLASS rather
// than a registered condition: a render advisory's class, or the identity of a
// publishing-conventions rule.
//
// It carries the descriptive code and nothing else, and the omission is the
// statement rather than a gap. Such a code names no registered condition, so
// there is no short code to quote and nothing on the halting-conditions page to
// point at — an identifier or a pointer invented for it would be one no lookup
// resolves.
//
// It exists as a named constructor rather than as a bare literal so that the
// decision is made once and reads as a decision. The keys cannot be filled in a
// [Diagnostic] literal, so every site that has a code states which of the two
// kinds it holds.
func classCode(code string) ConditionCodes {
	return ConditionCodes{Code: code}
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

// session is the daemon's process-level state: everything one running daemon
// holds that is NOT about a workspace. One session exists per process, for the
// life of the dispatch loop.
//
// The split between this and [Server] is the daemon's central shape. A workspace
// is an argument to each request rather than a property of the process, so the
// state a workspace owns cannot live here: putting it here is what would make
// two callers asking about two workspaces share one answer. What lives here is
// the transport, the negotiated protocol state, the injected build identity, and
// the registry of the workspaces this process has been asked about.
//
// Concurrency. The daemon dispatches handlers through a bounded worker pool
// whose size is configurable via concurrency.pool_size (default 4). When the
// pool size is greater than 1, handlers run concurrently, so every field a
// handler touches is synchronized to support that. The synchronized surfaces
// here are:
//
//   - positionEncoding is held in an atomic value: the handshake handler stores
//     it, every position-mapping site loads it.
//
//   - workDoneProgress is an atomic bool, for the same reason.
//
//   - pushRoot is held in an atomic value: the handshake handler stores what the
//     client declared, and every publication site loads it to decide whether the
//     workspace it is about is the one pushes are allowed for.
//
//   - shuttingDown is an atomic bool: the dispatch loop's acceptance gate and
//     the shutdown handler store it, the acceptance gate loads it.
//
//   - The per-request cancel registry (cancelTracker) is internally
//     synchronized by its own mutex: each in-flight handler tracks and untracks
//     its id under the lock, so concurrent track / untrack across the pool need
//     no external lock.
//
//   - The workspace registry is internally synchronized by its own mutex, which
//     it needs because it is the one map here that grows during handler
//     execution: a request naming a workspace nothing has asked about yet
//     installs an entry for it.
//
// The remaining fields (compilerVersion, toolchain, the injected functions, the
// launch resolution, serverCtx/serverCancel, the cancelTracker and registry
// pointers, the streams) are written once at construction before the worker pool
// starts, then only read during handler execution, so they are immutable
// snapshots needing no lock. The reader goroutine reads stdin and writes
// channels; it never touches mutable state. The id-order stdout serializer is
// the sole owner of the output writer, so no worker touches out directly.
type session struct {
	// compilerVersion is the binary build identity injected from the command
	// layer (RunConfig.CompilerVersion) and stamped into the IR provenance the
	// daemon produces, so inspect and Tier-4 output report the same version the
	// CLI does.
	compilerVersion string
	// toolchain is the stamp a dependency resolution performed here records into
	// the lockfile it writes, injected from the command layer
	// (RunConfig.Toolchain) for the same reason compilerVersion is. Nil for a
	// session built as a struct literal, which is the in-memory test harness: the
	// stamp is advisory, so a lockfile written without one is complete and simply
	// records nothing about which binary produced it.
	toolchain *lockfile.ToolchainStamp

	// launchRoot and launchCfg are the workspace resolution the process performed
	// when it started, from --workspace-root or from discovery upward out of the
	// working directory.
	//
	// This is NOT the workspace any request acts on: a request names its own, and
	// neither field is consulted to answer one. Between them they have exactly
	// two jobs. launchCfg sizes the worker pool, which is a property of the
	// process and cannot be a property of a request — a pool cannot be resized
	// per request, so a request naming a workspace whose configuration asks for a
	// different pool would be asking for one. And launchRoot is the DEFAULT
	// DECLARATION for pushes: a client that declares no workspace at the
	// handshake is pushed the diagnostics of the workspace the process was
	// pointed at, which is what the operator declared by pointing it there.
	//
	// They are the resolution's two derived values rather than the resolution
	// itself, because nothing at this level needs the whole of it: each workspace
	// entry keeps its own, which is what answers a request for the effective
	// configuration.
	launchRoot string
	launchCfg  workspaceconfig.Config

	// buildOnInspectFlag is the --build-on-inspect flag as the operator passed
	// it, or nil when they did not. It is a property of how the process was
	// launched rather than of a workspace, which is why it lives here while the
	// dial it resolves into lives on each workspace: the flag overrides whatever
	// that workspace's own configuration says.
	buildOnInspectFlag *bool

	// installCLIPlugin installs a command-extension plugin as a top-level command,
	// injected from the command layer (RunConfig.InstallCLIPlugin) because the
	// install's collision guard reads the freshly built command tree, which lives
	// in the main package. Nil for a Server built as a struct literal, and nil in
	// any process that did not wire it; the handler refuses rather than reporting
	// an install that did not happen.
	installCLIPlugin pluginops.CLIInstaller
	// resolveImportPlugin resolves an importer or parser plugin URI to its module
	// bytes, injected from the command layer (RunConfig.ResolveImportPlugin)
	// because the plugin cascade it binds is assembled there and shared with the
	// install path, the listing and the runtime plugin runner. Nil for a Server
	// built as a struct literal, and nil in any process that did not wire it; the
	// handler refuses rather than reporting a conversion that did not happen.
	resolveImportPlugin importops.PluginBytesResolver

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
	// pickup it exposes is wired to the $/cancelRequest notification,
	// which the dispatch loop takes inline at its acceptance boundary
	// rather than submitting to the pool — see cancelRegistry.
	// Constructed lazily in dispatchLoop alongside serverCtx; the
	// in-memory test harness may construct it earlier.
	cancelTracker *cancelRegistry

	// pushRootValue holds the workspace root the daemon may push diagnostics
	// for: the one the client declared at the handshake, or the launch
	// resolution's root when it declared none.
	//
	// It is the whole of the push rule, and the rule is about who asked. A
	// diagnostic that arrives unasked-for belongs on the problem list of the
	// workspace whoever is reading has open; a diagnostic about some other
	// workspace, produced because a second caller asked a question, does not. So
	// a publication whose workspace is not this one is dropped and the findings
	// travel on that request's own response instead — which keeps one client's
	// question out of another client's problem list.
	//
	// The empty string means no workspace may be pushed for, which is what a
	// client that declares the empty root has asked for. Held in an atomic value
	// because the handshake handler that stores it and the publication sites that
	// load it run on different worker goroutines under the pool.
	pushRootValue atomic.Value

	// workspaces is every workspace this process has been asked about, each with
	// the state held for it. It is the one map here that grows during handler
	// execution, and it carries its own lock for that reason.
	//
	// Entries are KEYED rather than replaced, which is the difference between
	// this and anchoring. A second workspace gets its own entry; it does not
	// evict the first's. So a developer whose editor has been open for an hour
	// does not pay a cold start because something else asked a question about
	// somewhere else.
	workspaces *workspaceRegistry

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

	// workDoneProgressValue holds whether the client said it can handle
	// the work-in-progress notifications a progress token would produce.
	// Defaults to false — an undeclared capability is an unsupported one,
	// which is the protocol's own reading — so a session that negotiates
	// nothing is never sent a notification it did not ask for. The
	// aggrega/initialize handler stores it from the client's
	// window.workDoneProgress capability, and the dispatch path loads it
	// via the workDoneProgress accessor to decide whether a request may
	// carry a token at all. Held in an atomic value for the same reason
	// the position encoding is: the initialize handler that stores it and
	// the read site run on different worker goroutines under the pool.
	workDoneProgressValue atomic.Bool

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

	// testValidateGateHook, when non-nil, runs inside handleValidate at the
	// moment the parse cache has been seeded and the diagnostic layers have not
	// — the window a concurrent reader of the same file used to be answered
	// from. The production setup never sets this field; it exists so a test can
	// hold a validation open exactly there and drive readers into that window
	// deterministically, rather than racing for it and covering the window only
	// on the runs that happened to land. A nil hook is a no-op.
	testValidateGateHook func(file string)

	// validationWaitBudget overrides how long a diagnostics request waits for a
	// validation of its file that is already running. Zero — the production
	// value, since nothing outside the in-memory test harness sets it — means
	// the package default, so a real daemon always waits that. Tests pass a
	// short value so the deadline's own behaviour exercises in subsecond wall
	// time instead of holding a test open for the real budget.
	validationWaitBudget time.Duration
}

// Server is what one request is answered against: the process-level [session],
// shared by every request, together with the ONE workspace that request named.
//
// It is constructed per request, from the workspace the request carries, and it
// is where every handler's receiver points. **Nothing here is a property of the
// process.** The daemon remembers no workspace between requests and holds none
// as the answer to "which workspace is this about" — a request that acts on a
// workspace names it, so two requests naming two workspaces are answered
// against two of these and cannot see each other's entities, Releases or
// diagnostics.
//
// Concurrency. The workspace fields are written once when the workspace's entry
// is created, before it is published into the registry, then only read during
// handler execution — so they are immutable snapshots needing no lock, exactly
// as they were when there was one of them. What each entry owns and synchronizes
// for itself:
//
//   - The parse cache and the catalog lookup are each internally synchronized by
//     their own mutex; callers need no external lock. A handler resolving a
//     Release does NOT pin the workspace's catalog: it forks a request-scoped
//     view (ForRequest) carrying its own lockfile-pin snapshot and pin-dependent
//     caches, so one request's pins can never interleave into another's
//     multi-step resolution. The workspace's catalog holds only the
//     pin-independent, workspace-stable Tier-1 indexes, snapshotted into each
//     view; it is reset only on invalidation of that workspace.
//
//   - The per-file diagnostics maps are guarded by diagMu, which is this
//     workspace's own. Publication copies the file's slice under the lock and
//     marshals outside it. Two workspaces answered concurrently take two
//     different locks, so neither waits on the other.
//
//   - The in-flight validation tracker is internally synchronized by its own
//     mutex and holds it for O(1) map work only — never across a validation,
//     which would serialize the daemon behind one file and undo the pool. It is
//     what makes the two stores above readable as one answer: a diagnostics
//     request for a file whose validation is running waits for that validation
//     rather than reading between its two writes.
//
// No field holds a Release. A request that acts on one names it, and the
// resolution is request-scoped from end to end, so there is no shared Release
// state for the pool to synchronize.
type Server struct {
	*session

	// workspaceRoot is the absolute, cleaned root of the workspace this request
	// acts on: the value the request named, resolved through the same chokepoint
	// the process's own launch resolution runs through. Empty only for a request
	// whose subject is not a workspace at all.
	workspaceRoot string

	// configSource names the configuration layer that supplied this workspace's
	// settings. Surfaced on the handshake for the workspace a client declared.
	configSource string

	// workspaceMarker names the marker tier that anchored this root via
	// discovery. The zero value (no marker) when the root was supplied
	// explicitly, since no marker was consulted in that path. Surfaced on the
	// handshake via its String() wire token.
	workspaceMarker workspaceconfig.Marker

	wsConfig workspaceconfig.Config

	// resolvedWorkspace is this workspace's resolution kept whole rather than
	// only as the values derived from it above. It is what answers a request for
	// the effective configuration and each setting's originating layer, so that
	// answer is the same resolution the handshake's workspace root and
	// configuration layer report for the same root and cannot disagree with it.
	//
	// Nil only for a Server built as a struct literal, which is the in-memory
	// test harness, and for a request whose subject is not a workspace.
	resolvedWorkspace *workspaceconfig.ResolvedWorkspace

	// buildOnInspect is the resolved serve build-on-inspect dial (the
	// --build-on-inspect flag overriding this workspace's serve.build_on_inspect
	// config key). When false (the default), handleIrGraph returns the IR graph
	// without running the artifact-rendering stage; when true that stage runs,
	// so the advisories it raises surface as an additional diagnostic layer.
	// It selects that stage and nothing else: the preview posture is the
	// inspection one under either value, so the dial turns no gate on.
	//
	// It is resolved per workspace because the config key is a workspace's own,
	// while the flag that overrides it is the process's — so two workspaces
	// answered by one daemon read their own key and both honour the flag.
	buildOnInspect bool

	// lintOnValidate is the resolved serve.lint_on_validate dial for this
	// workspace. When false — the default — a validation reports the correctness
	// layers alone and no publishing-conventions finding reaches a client. When
	// true, a validation of a catalog entity also runs the conventions rules over
	// the buffer it validated and publishes what they found as a third layer.
	//
	// It gates that layer and nothing else. aggrega/lint answers whatever the
	// request asks for under either value, because a caller sending it has asked
	// for the check by name; the dial decides only whether the check also runs
	// where nobody asked for it.
	//
	// There is no flag counterpart, unlike buildOnInspect. The question the dial
	// answers is about a workspace's own content, and whoever has that question
	// is authoring the workspace rather than launching the daemon — an author
	// never passes this process its arguments, their editor does.
	lintOnValidate bool

	// lazyCatalog is this workspace's catalog lookup: the per-URI Tier-1/2/3
	// cascade for the WHOLE catalog surface — Component + Feature (on-demand
	// cascade) plus the tolerantly-enumerated Recipe / FeatureRecipe / Aggregate /
	// Plugin / SchemaLibrary / MappingLibrary / WorkloadLibrary / ArtifactsLibrary shell. Constructed once when
	// the workspace's entry is created, via catalog.NewLazyLookup; no
	// per-request reconstruction, no eager whole-catalog load. It holds the
	// workspace-stable, pin-independent Tier-1 filesystem indexes; Reset() on
	// invalidation re-walks that changed workspace and leaves every other
	// workspace's indexes standing.
	//
	// A Release-resolving handler does NOT read or pin this lookup directly. It
	// forks a request-scoped view via ForRequest, seeded with that request's
	// lockfile pins and snapshotting this workspace's Tier-1 indexes, and threads
	// the view through closure compute and the render pipeline (AsCatalogLookup,
	// RecipeLookupWithManifest, SubAggregateLoader, MappingLibraries, ClosureLookup,
	// the closure-scoped extends + Pass-2). Each request therefore resolves against
	// its own immutable pin snapshot, so a concurrent request's pins never perturb
	// it — the consistency the worker pool depends on.
	lazyCatalog *catalog.LazyLookup

	parseCache *cache

	// diagMu guards every read and write of this workspace's diagnostics maps.
	// The critical section is O(1) map work plus, at publish time, a copy of one
	// file's slice header; it never spans a pipeline run or any I/O. Writers
	// always reassign a whole slice (never mutate one in place), so a header copy
	// taken under the lock is a safe snapshot to marshal after releasing it.
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

	// validations records which of this workspace's files are being validated
	// right now, so a diagnostics request naming one of them waits for the
	// validation and is answered with its findings rather than from a store that
	// validation is halfway through writing. It carries its own mutex and its
	// zero value is usable, so a workspace nothing has validated needs no
	// construction step. See validationTracker for why only the diagnostics
	// reader waits, and for why this cannot deadlock against the worker pool.
	//
	// It is per workspace for the same reason the stores above are: two
	// workspaces can hold a file at one relative path, and a shared tracker
	// would make a reader in one wait out a validation in the other.
	validations validationTracker

	// conventionDiags is the third diagnostic layer, guarded by the same diagMu:
	// the publishing-conventions findings for one authored file, keyed by that
	// file. It is kept apart from the semantic layer even though both refresh on
	// the same event, and what separates them is the source stamp rather than the
	// refresh schedule: the semantic write stamps its whole slice, so a
	// conventions finding appended to it would be published as a finding about
	// correctness and a client could no longer tell the two questions apart.
	conventionDiags map[string][]Diagnostic
}

// workspaceRegistry holds the state the daemon keeps for each workspace it has
// been asked about, keyed by that workspace's root.
//
// # The key, and where it is derived
//
// The key is the workspace root in ONE canonical spelling: its cleaned absolute
// path. It is derived at the request boundary, in [session.workspaceFor], from
// the value the request named — and nowhere else, for the same reason the
// per-file stores fix their key at the boundary rather than per handler. Two
// spellings of one root would otherwise install two entries, and a client would
// then be answered from a cold cache for a workspace the daemon already had warm.
//
// A relative or empty value is refused rather than resolved. A file parameter has
// a workspace root to be relative to, so resolving one is lossless; a workspace
// root has nothing, so a relative value names no workspace and repairing it would
// mean guessing which one.
//
// # Why keyed rather than cleared
//
// Each entry holds a catalog lookup with its Tier-1 filesystem indexes, a parse
// cache and three diagnostic layers — all of it earned by walking a tree. An
// entry that were replaced whenever a different workspace was named would turn
// one question about somewhere else into a cold start for whoever is typing.
type workspaceRegistry struct {
	// mu guards byRoot. The critical section is a map read plus, on a miss, the
	// workspace resolution and the construction of one entry. That resolution
	// reads configuration files, so the lock is held across I/O — deliberately:
	// two concurrent first requests for one root would otherwise each build an
	// entry and one would be discarded along with whatever it had already cached.
	mu     sync.Mutex
	byRoot map[string]*Server
}

// newWorkspaceRegistry returns an empty registry.
func newWorkspaceRegistry() *workspaceRegistry {
	return &workspaceRegistry{byRoot: map[string]*Server{}}
}

// get returns the entry held for key, or nil when none is.
func (r *workspaceRegistry) get(key string) *Server {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byRoot[key]
}

// roots returns every key the registry holds, sorted, so a caller enumerating
// the workspaces reports the same sequence on every run rather than one
// reshuffled by map iteration.
func (r *workspaceRegistry) roots() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.byRoot))
	for root := range r.byRoot {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

// positionEncoding returns the negotiated position encoding. It loads
// the atomic value the initialize handler set; an unset value (a session
// constructed without setPositionEncoding) reports the UTF-16 protocol
// default rather than an empty encoding.
func (s *session) positionEncoding() yamlpos.PositionEncoding {
	v, ok := s.positionEncodingValue.Load().(yamlpos.PositionEncoding)
	if !ok {
		return yamlpos.PositionEncodingUTF16
	}
	return v
}

// setPositionEncoding stores the negotiated position encoding. Called
// by the initialize handler when the client negotiates an encoding.
func (s *session) setPositionEncoding(enc yamlpos.PositionEncoding) {
	s.positionEncodingValue.Store(enc)
}

// workDoneProgress reports whether this session may be sent
// work-in-progress notifications. A session that never handled an
// initialize, or one whose client declared nothing, reports false.
func (s *session) workDoneProgress() bool {
	return s.workDoneProgressValue.Load()
}

// setWorkDoneProgress stores whether the client can handle
// work-in-progress notifications. Called by the initialize handler with
// what the client declared.
func (s *session) setWorkDoneProgress(supported bool) {
	s.workDoneProgressValue.Store(supported)
}

// pushRoot returns the workspace root the daemon may push diagnostics for: the
// one the client declared at the handshake, or the launch resolution's root when
// it declared none.
//
// The empty string means none may be pushed for. That is what a client asking
// for no pushes gets, and it is also what a session holding no launch
// resolution reports — the in-memory test harness, where a push has no declared
// workspace to belong to until a test declares one.
func (s *session) pushRoot() string {
	root, _ := s.pushRootValue.Load().(string)
	return root
}

// setPushRoot stores the workspace root pushes are confined to. Called by the
// initialize handler with what the client declared, and at construction with the
// launch resolution's root as the default declaration.
func (s *session) setPushRoot(root string) {
	s.pushRootValue.Store(root)
}

// pushesAllowed reports whether this workspace is the one the client declared at
// the handshake, and therefore whether an unasked-for diagnostic about it
// belongs on that client's problem list.
//
// A workspace-free request reports false, since there is no workspace for a
// publication to be about.
func (s *Server) pushesAllowed() bool {
	return s.workspaceRoot != "" && s.workspaceRoot == s.pushRoot()
}

// workspaceFor returns the state held for the workspace root a request named,
// creating it on first use.
//
// This is the single place a workspace root becomes a workspace, and the single
// place the registry key is derived — the cleaned absolute path. A handler never
// resolves one for itself, which is what keeps two handlers from installing two
// entries for one root under two spellings.
//
// The resolution goes through workspaceconfig.Resolve, the same chokepoint every
// aggrega command and the process's own launch resolution use, with the root
// supplied explicitly so discovery is not consulted: the caller named a root, so
// walking upward from somewhere else to find a different one would answer a
// question nobody asked. A root whose tree carries its own configuration file
// therefore contributes that file's settings exactly as it would on the command
// line.
//
// Three refusals, each naming what to send instead:
//
//   - An empty value. The parameter states which workspace the request is about,
//     and no value states none.
//   - A relative value. A file parameter has a workspace root to be relative to,
//     so resolving one is lossless; a workspace root has nothing, so a relative
//     value names no workspace and repairing it would mean guessing which one.
//   - A value that is not an existing directory. A workspace is a tree, so a
//     path naming a file or naming nothing cannot be one, and answering from it
//     would report an empty workspace where the fault is a mistyped path.
func (s *session) workspaceFor(root string) (*Server, error) {
	if root == "" {
		return nil, fmt.Errorf("the `workspaceRoot` parameter is required: send the absolute path of " +
			"the workspace root this request is about")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("the `workspaceRoot` parameter must be an absolute path, and %q is "+
			"relative: a workspace root has nothing to be relative to", root)
	}
	key := filepath.Clean(root)
	if ws := s.workspaces.get(key); ws != nil {
		return ws, nil
	}
	st, statErr := os.Stat(key)
	if statErr != nil {
		return nil, fmt.Errorf("the `workspaceRoot` parameter names %s, which cannot be read: %w", key, statErr)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("the `workspaceRoot` parameter names %s, which is not a directory: a "+
			"workspace root is a tree", key)
	}

	s.workspaces.mu.Lock()
	defer s.workspaces.mu.Unlock()
	// Re-check under the lock: two concurrent first requests for one root both
	// reach here, and the second must be handed the entry the first installed
	// rather than a fresh one whose caches start cold.
	if ws := s.workspaces.byRoot[key]; ws != nil {
		return ws, nil
	}
	resolved, err := workspaceconfig.Resolve("", key)
	if err != nil {
		return nil, fmt.Errorf("resolve the workspace at %s: %w", key, err)
	}
	ws := s.newWorkspace(resolved)
	s.workspaces.byRoot[key] = ws
	return ws, nil
}

// newWorkspace builds the state held for one resolved workspace. Every field is
// written here, before the entry is published into the registry, which is what
// makes the entry an immutable snapshot a concurrent reader needs no lock for.
//
// The catalog lookup takes a nil puller, so Tier-3 is disabled and no resolution
// through it reaches a registry — the same posture the command-line build
// resolves under, and the same one the daemon's two other acquisition seams
// hold. An editor session inspects a workspace as it stands; acquiring is what
// the verbs a user runs deliberately are for. Pins are deliberately not threaded
// here either: this lookup is shared across concurrently-served requests for
// this workspace, so a pin set installed on it would resolve another request's
// reads against the wrong Release. Each request forks its own pinned view via
// ForRequest.
func (s *session) newWorkspace(resolved *workspaceconfig.ResolvedWorkspace) *Server {
	cfg := resolved.Config()
	root := resolved.Root()
	return &Server{
		session:           s,
		workspaceRoot:     root,
		configSource:      resolved.Source().String(),
		workspaceMarker:   resolved.Marker(),
		wsConfig:          cfg,
		resolvedWorkspace: resolved,
		buildOnInspect:    resolveBuildOnInspect(s.buildOnInspectFlag, cfg.Serve.BuildOnInspect),
		lintOnValidate:    cfg.Serve.LintOnValidate,
		lazyCatalog:       catalog.NewLazyLookup(root, cfg.ArtifactCacheDir, cfg.Workspace.CatalogDirs, nil),
		parseCache:        newCache(cfg.ParseCache.MaxEntries, cfg.ParseCache.MaxBytes),
		diagnostics:       map[string][]Diagnostic{},
		renderDiags:       map[string][]Diagnostic{},
		conventionDiags:   map[string][]Diagnostic{},
	}
}

// workspaceFree returns a Server for a request whose subject is not a workspace
// at all — the schema report, the document skeleton, and the handshake, which
// declares a workspace rather than acting on one.
//
// Its workspace fields are the zero value, so a handler that read one would read
// an empty root rather than some other caller's workspace. Which methods reach
// it is declared in the availability table and nowhere else.
func (s *session) workspaceFree() *Server {
	return &Server{session: s}
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

// setRenderDiagnostics replaces the whole build-advisory layer with byFile and
// returns the sorted set of files whose entry the replacement touched — the union
// of the files that had an advisory on this layer and the files that now do — so
// the caller republishes exactly the files whose composed state changed, clearing
// a stale advisory as reliably as it publishes a new one. Whole-layer replacement
// is what a whole-closure build calls for: a per-file merge would strand an
// advisory on a file that stopped raising one.
//
// The layer carries every advisory a build raised, not the render's alone, which
// is what the source it publishes under names. Read the layer's contents from the
// source it publishes under rather than from this identifier.
//
// Each incoming key is canonicalised, so this layer and the semantic one are
// keyed alike however an advisory was attributed. The returned file set is
// therefore in the canonical spelling too, which is what the caller publishes —
// the notification's file identifier and the store's key are then the same
// string, rather than two spellings of one document.
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

// Run resolves the workspace the process was launched over, constructs the
// process-level session, and enters the JSON-RPC dispatch loop. Returns when
// stdin reaches EOF, the drain-then-abort shutdown sequence completes (the first
// aggrega/shutdown drains accepted in-flight work; a second aborts it), or an
// unrecoverable I/O error occurs.
//
// ctx is the context the process was launched with, which ends when the process
// is first asked to stop. Its withdrawal reaches the dispatch loop as the first
// shutdown signal: the daemon stops accepting work, lets accepted work finish,
// and returns. It is passed to the loop rather than made the parent of the
// session context, because the session context's cancellation is the abort that
// cuts accepted work short and the two steps must stay distinct.
//
// # The launch resolution is not the workspace requests act on
//
// A request that acts on a workspace names one, and the state held for it is
// created when that root is first named. So this resolution anchors nothing: no
// handler reads it, and a request naming a workspace other than this one is
// answered from that workspace with no reference to this one.
//
// It has two jobs and no others. It sizes the worker pool, which is a property
// of the process — a pool cannot be resized per request. And it is the default
// declaration for pushes: a client that declares no workspace at the handshake
// is pushed the diagnostics of the workspace the process was pointed at, which
// is the workspace the operator declared by pointing it there.
//
// Launch anchoring itself is unchanged:
//   - When cfg.WorkspaceRoot is non-empty, Run uses the explicit path and skips
//     discovery. The cobra wiring is expected to have validated the path; Run
//     re-validates as defense-in-depth.
//   - When cfg.WorkspaceRoot is empty, Run walks upward from the daemon's cwd
//     looking for any of the five marker types per discoverWorkspaceRoot.
//
// Config loading uses the three-level precedence cascade per
// workspaceconfig.LoadWithGlobal — workspace file > global file > built-in
// defaults — and every workspace a request later names is resolved through that
// same call, so one workspace's settings are read the way the command line reads
// them rather than inherited from whichever tree the process happened to start
// in.
//
// On unrecoverable startup failure (root not discoverable, config malformed,
// explicit AGGREGA_CONFIG pointing at a missing file), Run returns an error
// before entering the dispatch loop. The cobra entry point surfaces that error
// to the process exit code; the IDE client interprets non-zero exit as a
// "language server failed to start" condition.
func Run(ctx context.Context, cfg RunConfig) error {
	if cfg.WorkspaceRoot != "" {
		st, statErr := os.Stat(cfg.WorkspaceRoot)
		if statErr != nil {
			return fmt.Errorf("explicit workspace root %s: %w", cfg.WorkspaceRoot, statErr)
		}
		if !st.IsDir() {
			return fmt.Errorf("explicit workspace root %s is not a directory", cfg.WorkspaceRoot)
		}
	}

	// Single resolution path shared with the CLI and with every workspace a
	// request names: discover the root (or honor the explicit one) and
	// cascade-merge the config tree.
	resolved, err := workspaceconfig.Resolve("", cfg.WorkspaceRoot)
	if err != nil {
		return err
	}

	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	// The render pipeline's per-request Loaders draw on the process-wide shared
	// compilation cache (see wasm.NewCachingLoaderFromContext): a guest's
	// expensive compilation is paid once for the daemon's life and served warm on
	// every later request, and the single cache is safe for concurrent use across
	// the worker pool. It is a process-lifetime singleton the daemon neither
	// creates, injects, nor closes — it is reclaimed at process exit. It keys on
	// the guest bytes, so it is shared across workspaces for the same reason it is
	// shared across requests: it memoizes a pure function of those bytes.
	sess := &session{
		compilerVersion:     cfg.CompilerVersion,
		toolchain:           cfg.Toolchain,
		launchRoot:          resolved.Root(),
		launchCfg:           resolved.Config(),
		buildOnInspectFlag:  cfg.BuildOnInspect,
		installCLIPlugin:    cfg.InstallCLIPlugin,
		resolveImportPlugin: cfg.ResolveImportPlugin,
		workspaces:          newWorkspaceRegistry(),
		in:                  bufio.NewReader(cfg.Stdin),
		out:                 cfg.Stdout,
		stderr:              stderr,
	}
	sess.setPositionEncoding(yamlpos.PositionEncodingUTF16)
	// The default push declaration. A client that declares a workspace at the
	// handshake replaces it; one that declares nothing keeps it, and is pushed
	// the diagnostics of the workspace this process was pointed at.
	sess.setPushRoot(resolved.Root())
	// The launch workspace's entry is installed eagerly, and only so that the
	// default push declaration names a workspace that exists. It walks nothing:
	// the catalog lookup is lazy and the caches start empty, exactly as they do
	// for a workspace a request names later.
	sess.workspaces.byRoot[filepath.Clean(resolved.Root())] = sess.newWorkspace(resolved)

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
	return sess.dispatchLoop(ctx, drainTimeout)
}
