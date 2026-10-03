// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// initializeParams is the request shape for `aggrega/initialize`. The
// daemon reads positionEncodings and workDoneProgress off the client's
// capabilities; other LSP initialize fields are accepted but ignored
// today.
//
// Workspace is where a client DECLARES the workspace it wants unasked-for
// diagnostics pushed for. It is not the workspace anything is answered against
// — a request that acts on a workspace names its own, on every call — so
// declaring one here buys exactly one thing: findings about that workspace
// arrive as they are produced, instead of only on the response to whichever
// request produced them.
//
// It is a pointer so that three cases are distinguishable, and each is a
// different thing a client can mean:
//
//   - Absent. The client has said nothing about pushes, so the workspace this
//     process was launched over is pushed for. That is what the operator
//     declared by pointing the process at it, and it is what a client that
//     predates this field reads: such a client keeps receiving exactly the
//     diagnostics it received before, which is what keeps adding the field from
//     silently switching them off.
//   - Present and non-empty. Pushes are confined to that workspace.
//   - Present and empty. No workspace is pushed for at all. A client that would
//     rather read every finding on the response that produced it says so this
//     way, and it is the honest shape for a caller that is not an editor and has
//     no problem list to keep in sync.
//
// A declared workspace is resolved exactly as a request-named one is, so a
// declaration naming a relative path, or a path that is not a directory, is
// refused here rather than quietly leaving pushes pointed somewhere else.
type initializeParams struct {
	Capabilities  clientCapabilities `json:"capabilities"`
	WorkspaceRoot *string            `json:"workspaceRoot,omitempty"`
}

// clientCapabilities mirrors a tiny slice of LSP's client capabilities
// shape — the position-encoding negotiation surface, and whether the
// client can handle work-in-progress notifications.
type clientCapabilities struct {
	General *generalCapabilities `json:"general,omitempty"`
	Window  *windowCapabilities  `json:"window,omitempty"`
}

// generalCapabilities carries LSP 3.17's general capability bag. The
// daemon consumes positionEncodings; everything else is ignored.
type generalCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// windowCapabilities carries LSP 3.17's window capability bag. The
// daemon consumes workDoneProgress, which says whether the client can
// handle the notifications a request's progress token would produce;
// everything else is ignored.
type windowCapabilities struct {
	WorkDoneProgress bool `json:"workDoneProgress,omitempty"`
}

// serverCapabilities is the daemon's reply on initialize. The wire
// shape is intentionally compact — the position-encoding negotiation
// result, and whether the daemon will report work in progress to this
// client. Future capabilities extend this struct without breaking
// existing clients.
//
// WorkDoneProgress is omitted when the client declared nothing, so a
// client that does not negotiate progress reads exactly the reply it
// read before the field existed.
type serverCapabilities struct {
	PositionEncoding string `json:"positionEncoding"`
	WorkDoneProgress bool   `json:"workDoneProgress,omitempty"`
}

// initializeResult is the response shape for aggrega/initialize.
// Capabilities carries the negotiated position encoding.
//
// The three workspace fields describe ONE workspace: the one this client
// declared for pushes, or — when it declared none — the one the process was
// launched over. They are not a statement about what any request will be
// answered against, because that is the workspace each request names.
//
// WorkspaceRoot reports that workspace's absolute path. ConfigSource names the
// highest-precedence configuration layer that contributed to its effective
// configuration: "workspace" | "global" | "defaults". WorkspaceMarker names the
// marker tier that anchored it: "config" | "directory" | "aggregate-glob" |
// "release-glob" | "manifest-glob"; the field is omitted on the wire when the
// root was supplied explicitly, since no marker was consulted in that path.
//
// A client that declared the empty workspace — asking for no pushes at all —
// reads an empty WorkspaceRoot, which is the honest answer: there is no
// workspace these fields could be about.
//
// Version is the version of the binary answering this session: the value
// `aggrega version` reports for that same binary. It is the build identity the
// command layer hands the daemon when it starts it (RunConfig.CompilerVersion),
// and that identity comes from the one derivation the version command reads, so
// the two cannot report different versions for one binary. The daemon derives
// no version of its own: package main is where the build stamp lives, and a
// second reading of it here would agree on some builds and not on others.
//
// It describes the running process rather than the file the process was
// started from, which is why it is on the handshake. Once the file at the
// launched path has been replaced, asking that file for its version answers for
// a binary that is not the one running. The key is always present; it carries
// the empty string only for a server built without a build identity.
type initializeResult struct {
	Capabilities    serverCapabilities `json:"capabilities"`
	WorkspaceRoot   string             `json:"workspaceRoot"`
	ConfigSource    string             `json:"configSource"`
	WorkspaceMarker string             `json:"workspaceMarker,omitempty"`
	Version         string             `json:"version"`
}

// releaseCandidatesResult is the aggrega/releaseCandidates response: the
// absolute paths of every Release-kind document under the workspace root,
// determined by content (the file's `kind: Release` header), not by filename.
// The IDE consumes this for its "which Release?" quick-pick so the editor's
// candidate set matches exactly what the daemon resolves — a Release named
// without a conventional prefix is included, and a non-Release file that
// happens to be named like one is not.
//
// Lockfiles maps each of those Releases to the absolute path of its lockfile,
// keyed by the same string Releases carries. It is on the answer so a client
// that has to know where a Release's lockfile lives — to watch it, or to tell
// its own deletion from anything else — reads the path the daemon checks rather
// than keeping a copy of the rule that places it. It is always an object, empty
// when the workspace holds no Release.
//
// UnreadLinkedDirs names each link standing where the walk would read a
// directory that leads to a directory outside the workspace root, relative to
// the root where the link itself sits and in the order the walk met them. The
// walk does not enter a directory outside the root, so a Release inside one is
// not in Releases even though an editor's file tree shows it under the link; the
// field is what lets a client say why, rather than offer fewer Releases than the
// reader can see with nothing said. It is always an array, empty when the
// workspace holds no such link.
type releaseCandidatesResult struct {
	Releases         []string          `json:"releases"`
	Lockfiles        map[string]string `json:"lockfiles"`
	UnreadLinkedDirs []string          `json:"unreadLinkedDirs"`
}

// dispatchReleaseCandidates returns the Release-candidate set of the workspace
// the request named — the content-kind walk the active-Release cascade already
// performs, surfaced as a list so the IDE delegates Release detection to the
// daemon rather than reimplementing it.
//
// The set is a property of one workspace, so the request names which: the walk
// runs over the root it carries, and two requests naming two workspaces are
// answered from two trees.
func (s *Server) dispatchReleaseCandidates(ctx context.Context, req *jsonRPCRequest) error {
	// A method with no parameters of its own still decodes, so a field a caller
	// sent is refused rather than dropped. See noParams for why an answer that
	// ignored an argument is worse than a refusal naming it.
	if err := decodeParams(req, &noParams{}); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}

	walked := walkReleases(s.workspaceRoot)
	releases := walked.releases
	if releases == nil {
		releases = []string{}
	}
	unread := walked.unreadLinkedDirs
	if unread == nil {
		unread = []string{}
	}
	return s.writeResult(ctx, req.ID, releaseCandidatesResult{
		Releases:         releases,
		Lockfiles:        LockfilesFor(releases),
		UnreadLinkedDirs: unread,
	})
}

// activeReleaseResult is the aggrega/activeRelease response: the absolute
// path of the Release the workspace's disambiguation cascade selected, and,
// where it selected none, an account of why.
//
// Release is always present on the wire and carries the empty string when the
// cascade selected none, so one field answers both which Release the workspace
// points at and whether it points at any. A field that appeared only once a
// Release resolved would leave a client unable to tell "no Release in this
// workspace" from "the field is missing for some other reason", and guessing
// between those two is exactly what this method exists to stop.
//
// Reason and Trace describe the same exhausted cascade at two lengths — a
// one-line summary for a compact surface, and the per-step account for a log
// or a panel the operator opens. Both are omitted when a Release resolved,
// because there is nothing to explain. They are on this answer rather than on
// an error because an unresolved workspace is an ordinary state and not a
// failed request.
//
// Trace is diagnostic prose written for a person to read, not a structure to
// parse: its wording, its layout and the labels it uses follow the cascade
// itself and change whenever the cascade does. Pass it through; do not match
// patterns against it or lift values out of it.
//
// Lockfile is the absolute path of the selected Release's lockfile, placed by
// the same rule every command that writes or reads one applies, so it names
// the file a pull writes and the file the graph request checks. It is omitted
// when no Release was selected, because there is then no Release to have a
// lockfile; Release being present and empty already says which case this is.
type activeReleaseResult struct {
	Release  string `json:"release"`
	Lockfile string `json:"lockfile,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Trace    string `json:"trace,omitempty"`
}

// dispatchActiveRelease reports which Release a client should select by
// default: the one the workspace's disambiguation cascade picks from the
// authoring-release configuration, the operator's marker file, and a lone
// Release in the tree, in that order. An editor showing the operator which
// Release is in force asks for it here rather than re-deriving it from one of
// the cascade's inputs, because an input the cascade did not end up using
// names a different Release than the one it chose.
//
// The answer is a property of one workspace, so the request names which.
//
// The cascade runs on every call, and that is the point rather than an
// oversight. The daemon holds neither a Release nor a workspace of its own, so
// this answer is a reading of the named workspace as it stands at the moment of
// asking: a client that asks again after writing a marker, editing the
// configuration or adding a Release file gets the new answer, where an answer
// computed once for the life of the process would keep reporting the workspace
// the daemon started in. The walk this performs is also why it is a request
// rather than part of the handshake, which deliberately answers before any
// workspace-wide walk so a large tree cannot delay it.
//
// A workspace with no Release to offer is a successful answer carrying an
// empty path, never an error. A client refreshes an active-Release indicator
// on events the operator did not ask about — the daemon restarting, the
// workspace anchor changing, a marker file being edited — so answering with an
// error would raise a remediation prompt on every one of them in a workspace
// that legitimately holds no Release. The reason travels on the answer instead,
// so a client can still say why nothing was offered.
//
// A Release the cascade found but could not load is published as a diagnostic
// on its own file before the answer is written, so an operator authoring a
// broken Release sees the error on the document rather than only inferring it
// from an empty result.
func (s *Server) dispatchActiveRelease(ctx context.Context, req *jsonRPCRequest) error {
	// A method with no parameters of its own still decodes, so a field a caller
	// sent is refused rather than dropped. See noParams for why an answer that
	// ignored an argument is worse than a refusal naming it.
	if err := decodeParams(req, &noParams{}); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}

	_, releasePath, attempt := runManifestCascade(s.workspaceRoot, s.wsConfig, s.stderr)
	s.publishCascadeFailureDiagnostics(ctx, attempt)
	result := activeReleaseResult{Release: releasePath}
	if releasePath == "" {
		result.Reason = attempt.diagnosticMessage()
		result.Trace = attempt.fullTrace()
	} else {
		result.Lockfile = lockfile.PathFor(releasePath)
	}
	return s.writeResult(ctx, req.ID, result)
}

// dispatchInitialize selects a position encoding from the client's
// offered list using SERVER preference order: UTF-8 > UTF-16 > UTF-32.
//
// Server-preference (rather than client-preference) because a UTF-8
// position is a byte offset into the very bytes yaml.v3 parsed, so
// converting a position to or from those bytes is a no-op, whereas
// UTF-16 / UTF-32 require per-rune decoding on that leg as well as on
// the node-column leg every encoding walks. The performance asymmetry between
// encodings is one-sided enough that the client's preference does
// not change the optimal choice for THIS server.
//
// UTF-16 is the LSP 3.17 default and remains the fallback when no
// negotiation occurs (s.positionEncoding is initialized to UTF-16
// in Run before dispatchInitialize ever runs). Unknown encoding
// strings in the client's list are ignored.
//
// It also records whether the client can handle work-in-progress
// notifications, and echoes that back. There is nothing to select
// between here — the client either handles them or it does not — so
// what the reply carries is the client's own declaration, which is what
// tells a client its tokens will be honoured before it sends one.
//
// The reply also names the version of the binary answering, which is a fact
// about this process rather than about any workspace; initializeResult says
// where that value comes from.
//
// # It is where a client declares the workspace it wants pushes for
//
// The handshake is the one exchange in the protocol that is about the
// connection rather than about a workspace, which is exactly why the push
// declaration belongs on it: a declaration is a standing fact about this client,
// and every other message is a question about one workspace. So this method
// takes no `workspace` parameter of the required kind — it is declared
// workspace-free in the availability table — and reads the optional declaration
// instead.
//
// What it does NOT do is anchor anything. The declared workspace decides where
// unasked-for diagnostics go and nothing else; no later request is answered
// against it, and a request naming a different workspace is answered from that
// one with no reference to this declaration.
func (s *Server) dispatchInitialize(ctx context.Context, req *jsonRPCRequest) error {
	var params initializeParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	selected := selectPositionEncoding(params.Capabilities)
	s.setPositionEncoding(selected)
	progress := clientHandlesWorkDoneProgress(params.Capabilities)
	s.setWorkDoneProgress(progress)

	// The workspace pushes are confined to, and the one the three workspace
	// fields below describe. A client that declared nothing leaves the launch
	// resolution in force, which is what it read before this field existed.
	declared, declaredErr := s.declaredPushWorkspace(params.WorkspaceRoot)
	if declaredErr != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+declaredErr.Error())
	}
	s.setPushRoot(declared.workspaceRoot)

	if err := s.writeResult(ctx, req.ID, initializeResult{
		Capabilities: serverCapabilities{
			PositionEncoding: string(selected),
			WorkDoneProgress: progress,
		},
		WorkspaceRoot:   declared.workspaceRoot,
		ConfigSource:    declared.configSource,
		WorkspaceMarker: declared.workspaceMarker.String(),
		Version:         s.compilerVersion,
	}); err != nil {
		return err
	}

	// Post-response flush of any diagnostics already populated for the workspace
	// pushes are confined to. The LSP-canonical pattern is to push diagnostics
	// AFTER the initialize response so the client's handshake completes before
	// unsolicited notifications start arriving. Nothing populates diagnostics
	// before a request asks for them, so at handshake time the layers are
	// normally empty; the loop is a no-op then and flushes only what a prior
	// request already produced.
	//
	// It flushes that workspace's layers and no other's, which follows from the
	// push rule rather than being a second statement of it: a reconnecting client
	// is owed the problem list of the workspace it just declared, and a workspace
	// some earlier request asked about is not that.
	//
	// diagnosticsFiles returns the keys sorted for deterministic ordering across
	// daemon runs (Go map iteration is randomized).
	//
	// What that workspace has already reported is forgotten first, because a
	// publication carrying nothing new is dropped and the client in front of us
	// has an empty problem list whatever an earlier one was told. A workspace
	// entry outlives the connection that filled it, so without this the flush
	// would conclude it had already reported every file and send nothing at all.
	declared.forgetPublished()
	for _, file := range declared.diagnosticsFiles() {
		if err := declared.publishDiagnostics(ctx, file); err != nil {
			fmt.Fprintf(s.stderr, "  publishDiagnostics %s: %v\n", file, err)
		}
	}
	return nil
}

// declaredPushWorkspace resolves the workspace a client declared for pushes into
// the state held for it.
//
// The three cases are the three things a client can mean by the field, and each
// is answered with a Server the reply and the startup flush are read off:
//
//   - Declared non-empty: that workspace, resolved through the same chokepoint a
//     request-named one is, so a bad declaration is refused here rather than
//     leaving pushes pointed at something else.
//   - Declared empty: no workspace, so the reply's workspace fields are empty
//     and nothing is ever pushed. The workspace-free Server is exactly that
//     statement — its root is the empty string, which is what the push predicate
//     reads as "push nowhere".
//   - Not declared: the workspace the process was launched over, which keeps a
//     client that predates the field reading the reply and receiving the pushes
//     it read and received before.
func (s *Server) declaredPushWorkspace(declared *string) (*Server, error) {
	if declared == nil {
		return s.workspaceFor(s.launchRoot)
	}
	if *declared == "" {
		return s.workspaceFree(), nil
	}
	return s.workspaceFor(*declared)
}

// clientHandlesWorkDoneProgress reports whether the client declared it
// can handle work-in-progress notifications. A client that sent no
// window capability, or sent one without the field, has declared
// nothing — which reads as unsupported, so the daemon never pushes a
// notification such a client has no handler for.
func clientHandlesWorkDoneProgress(caps clientCapabilities) bool {
	return caps.Window != nil && caps.Window.WorkDoneProgress
}

// selectPositionEncoding applies the server-preference dispatch over
// the client's offered list. Returns UTF-16 when the client offers
// nothing usable (no general capability, no positionEncodings field,
// or only unrecognized values).
func selectPositionEncoding(caps clientCapabilities) yamlpos.PositionEncoding {
	if caps.General == nil || len(caps.General.PositionEncodings) == 0 {
		return yamlpos.PositionEncodingUTF16
	}
	offered := map[yamlpos.PositionEncoding]bool{}
	for _, raw := range caps.General.PositionEncodings {
		switch yamlpos.PositionEncoding(raw) {
		case yamlpos.PositionEncodingUTF8:
			offered[yamlpos.PositionEncodingUTF8] = true
		case yamlpos.PositionEncodingUTF16:
			offered[yamlpos.PositionEncodingUTF16] = true
		case yamlpos.PositionEncodingUTF32:
			offered[yamlpos.PositionEncodingUTF32] = true
		}
	}
	for _, candidate := range []yamlpos.PositionEncoding{
		yamlpos.PositionEncodingUTF8,
		yamlpos.PositionEncodingUTF16,
		yamlpos.PositionEncodingUTF32,
	} {
		if offered[candidate] {
			return candidate
		}
	}
	return yamlpos.PositionEncodingUTF16
}

// validateParams is the request shape for `aggrega/validate`. Content
// carries the in-memory bytes the IDE editor holds for the file (which
// may differ from on-disk bytes during active editing).
//
// Release is a path relative to the workspace root naming the Release to
// validate against — the same value `aggrega/irGraph` takes, and the same
// value `aggrega build --release` takes when run from the root.
//
// Naming one asks for the checks only a Release can answer: its closure
// resolved, its lockfile read for pins, its semantic violations reported.
// Naming none asks for the checks a document answers on its own, and is a
// request in its own right rather than a malformed one — a workspace that
// holds no Release has no value to send, and its documents are still worth
// reporting on. Whichever is asked, the daemon acts on the Release the
// request named and never on one of its own, which is what keeps a
// validation and a graph query from describing two different Releases.
//
// A value that is present but malformed is refused either way. The field
// states a coordinate, and an absolute path, or one resolving outside the
// workspace root, states a coordinate the workspace does not hold.
type validateParams struct {
	File    string `json:"file"`
	Content string `json:"content"`
	Release string `json:"release"`
}

// validateResult is the response shape for `aggrega/validate`.
//
// Checked says which of the two answers this is, and it is carried only on
// the narrower one: a request that named no Release is answered from the
// document alone and carries checkedDocument, while a request that named one
// is answered against that Release and omits the field, that being the
// method's ordinary answer with nothing to qualify.
//
// The asymmetry is the point rather than an oversight. A well-formed document
// validated without a Release answers with an empty diagnostic list, which is
// indistinguishable on the wire from a clean full verdict, so a client reading
// it as "nothing is wrong" would be told the semantic checks passed when they
// never ran. The field is what separates the two, and omitting it from the
// full answer costs a client that does not read it nothing.
type validateResult struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Checked     string       `json:"checked,omitempty"`
}

// checkedDocument is the value validateResult.Checked carries when the request
// named no Release: the findings cover the document itself — whether it parses,
// whether its own shape is admissible, and which required values it still
// leaves empty — and nothing that needs a Release was run.
const checkedDocument = "document"

// dispatchValidate parses the request params, calls handleValidate,
// and writes the response envelope.
func (s *Server) dispatchValidate(ctx context.Context, req *jsonRPCRequest) error {
	var params validateParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleValidate(ctx, params)
	if ctx.Err() != nil {
		// Cancelled mid-flight (shutdown). Drop the response; the
		// dispatcher exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// handleValidate parses the supplied content via yaml.v3 and populates
// the daemon's parse cache + diagnostic cache for the file, then — when the
// request named a Release — checks the file in that Release's terms: a catalog
// entity of a kind the single-entity check accepts gets that check, resolved
// through the Release's catalog directories, and every other document gets the
// full resolver and semantic checks against the Release.
//
// The work divides in two and the request chooses between them. Parsing the
// buffer, classifying its document envelope and seeding the parse cache act on
// no Release at all; only the semantic pass needs one. So a request naming no
// Release gets the first half and says which half it got, rather than being
// refused. Refusing above the parse would take the parse cache down with it,
// and that cache is what aggrega/diagnostics, hover, completion, resolve,
// findReferences and findImplementations each read before they can answer — so
// all of them would stop working in a workspace holding no Release, which is
// precisely a workspace whose client has no value to send.
//
// Path resolution: a Release the request did name is resolved by
// resolveReleaseParam before anything else runs, so an absolute value, or one
// escaping the workspace root, is refused rather than half-answered. A request
// that named none is not put through that resolution at all, because absence
// is this handler's second request shape and not a malformed coordinate.
//
// Cancellation limit: cooperative cancellation (the shutdown drain) bites
// at the Tier-4 phase boundaries — resolve, topology, IR, analyze — and at
// the per-node loops within topology, IR, and analyze. A single heavy phase
// is NOT interruptible mid-phase: a drain-cancel arriving during the resolve
// phase waits for resolve to finish before aborting, and on a large document
// resolve dominates the runtime (parameter interpolation over the whole
// document, contract validation, and per-component boundary wiring all run as
// one straight-line block), so the wait is seconds. The initial document
// parse below is a minor fraction of that — the heavy uncancellable cost is
// the resolve sub-steps, not the parse.
//
// While this runs, the file it names is marked as under validation, and an
// aggrega/diagnostics request for that same file waits for this handler to
// return rather than reading the two stores it seeds a moment apart. See
// validationTracker for why that reader alone waits, and handleDiagnostics for
// what it is answered with. Nothing is held across the work below: the mark is a
// signal, not a lock.
func (s *Server) handleValidate(ctx context.Context, params validateParams) (validateResult, *jsonRPCError) {
	// The Release this request is about, resolved once at the boundary and
	// threaded through everything below it — the lockfile the pins come from,
	// the closure the semantic checks run over, and the filename correlation
	// that decides whether the buffer is that Release's lockfile. One value
	// reaches all three, so they cannot describe different Releases.
	//
	// It stays empty when the request named none, and each consumer below reads
	// that as "this answer is about no Release" rather than as a value gone
	// missing: the filename correlation classifies no lockfile, and the semantic
	// pass does not run. Nothing computes a Release here — an absent one is
	// answered as absent, never defaulted, so no request acts on a Release it
	// did not name.
	var releasePath, checked string
	if params.Release == "" {
		checked = checkedDocument
	} else {
		resolved, paramErr := resolveReleaseParam(s.workspaceRoot, params.Release)
		if paramErr != nil {
			return validateResult{}, paramErr
		}
		releasePath = resolved
	}

	// The request's spelling of the file is resolved once, here at the boundary,
	// and every cache and store below is reached with the result. A sender may
	// name the file relative to the workspace root, by absolute path or by its
	// file address, and all three name one document; resolving per store instead
	// would let one request populate a key the next request's spelling cannot
	// read, which reads back as a file with nothing to report rather than as a
	// request that missed.
	file := s.canonicalFile(params.File)
	// A diagnostics request naming this file while this validation runs waits for
	// it and is then answered with what it found, rather than being answered from
	// state this handler is halfway through writing. The signal is registered
	// here, above every write below it, and released when this handler RETURNS —
	// which is why it is a deferred release rather than a call beside one of the
	// writes: this handler leaves by several paths, and a release at any one of
	// them leaks the signal on the rest.
	//
	// It sits below the Release resolution above deliberately. A request refused
	// there returns without writing anything, so there is nothing for a reader to
	// wait for and nothing is registered.
	releaseValidation := s.validations.begin(file)
	defer releaseValidation()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(params.Content), &doc); err != nil {
		// One slice for both the store and the response, so the source the
		// store stamps is the source the caller receives. Two slices built
		// from the same value would leave the returned one unstamped.
		diags := []Diagnostic{yamlErrorToDiagnostic(err)}
		s.setDiagnostics(file, diags)
		// A buffer no parser will read is not a catalog entity the conventions rules
		// can be asked about, so whatever they last said about this file is cleared
		// rather than left standing beside a parse failure.
		s.setConventionDiagnostics(file, nil)
		s.parseCache.Drop(file)
		return validateResult{Diagnostics: diags, Checked: checked}, nil
	}
	s.parseCache.Put(file, &doc, []byte(params.Content))
	if s.testValidateGateHook != nil {
		s.testValidateGateHook(file)
	}
	// Per-file document-envelope validation on the buffer: any classifiable
	// aggrega document (not just the Release's Aggregate) gets the same
	// envelope verdict the CLI gives it, on its own file, at the offending
	// key, on unsaved content — and a catalog entity of a kind the single-entity
	// validation accepts gets every placeholder that validation names, at its
	// key, whether or not a Release was named. Findings early-return — which
	// short-circuits the semantic pass for that Aggregate (a structurally
	// invalid buffer makes the disk-based pipeline's verdict misleading) and
	// is the honest coverage for every other file. Foreign YAML, and
	// envelope-clean documents that owe no value, fall through to the
	// existing flow unchanged.
	if diags, hasFindings := s.envelopeDiagnosticsForBuffer(file, releasePath, []byte(params.Content), &doc); hasFindings {
		s.setDiagnostics(file, diags)
		// The document's own shape was rejected, or it still owes a value, so it is
		// not yet a document the publishing conventions have anything to say about:
		// a name that is wrong where the envelope is broken is the second problem,
		// and reporting it here would bury the first. The layer is cleared rather
		// than left standing.
		s.setConventionDiagnostics(file, nil)
		return validateResult{Diagnostics: diags, Checked: checked}, nil
	}
	// The publishing-conventions layer, refreshed on every validation of this file
	// — including the ones that find nothing, since an author who corrected a name
	// has to stop being told about it, and a document that stopped being a catalog
	// entity has to stop carrying an entity's findings. It is gated on the
	// serve.lint_on_validate dial, which defaults off; with the dial off the
	// refresh clears the layer and runs no rule.
	//
	// It sits above the two answers below rather than inside either, because a
	// catalog entity is ordinarily validated with no Release named and the
	// conventions are a question about the document itself. Both answers compose
	// the layer through the snapshot, so the findings reach the reply whichever of
	// the two the request asked for.
	s.refreshConventionDiagnostics(ctx, file, []byte(params.Content), &doc)
	if releasePath == "" {
		// The request named no Release, so the half above is the whole answer.
		// The semantic layer is cleared rather than left standing: this file may
		// carry findings from an earlier request that DID name a Release, and
		// returning those under an answer that says it checked the document alone
		// would report a Release-scoped verdict the caller did not ask for and
		// cannot attribute. Silence here is honest; a stale finding is not.
		s.setDiagnostics(file, nil)
		return validateResult{Diagnostics: s.snapshotDiagnostics(file), Checked: checked}, nil
	}
	// The Release this request named is the terms of the answer, and what it is
	// asked about depends on the document. A catalog entity of a kind the
	// single-entity check accepts gets that check, its references resolved through
	// the Release's catalog directories — the verdict the command line and the
	// tools give the same entity beside the same Release. Every other document —
	// the Release's own Aggregate above all — gets Tier 4: structured semantic
	// violations via RunTier4Pipeline, against that Release. Either way a Release
	// that cannot be read surfaces as a diagnostic on the validated file rather
	// than as a refusal: the request named a real coordinate, and what went wrong
	// is something the author can read and fix.
	var diags []Diagnostic
	if isSingleEntityDocument(&doc) {
		diags = s.runEntityValidation(ctx, releasePath, file, []byte(params.Content))
	} else {
		diags = s.runTier4Validation(ctx, releasePath, file, []byte(params.Content), &doc)
	}
	if ctx.Err() != nil {
		// Cancelled mid-pipeline. Leave the prior diagnostic cache
		// entry intact and return without overwriting it — which means
		// stamping the layer's source here, since the store is what
		// normally stamps it.
		return validateResult{Diagnostics: stampSource(diags, DiagnosticSourceSemantic)}, nil
	}
	s.setDiagnostics(file, diags)
	// The response carries the composed view, not the semantic layer alone: the
	// client replaces its whole per-file collection from this reply, so returning
	// one layer would erase any render advisory standing on the same file.
	return validateResult{Diagnostics: s.snapshotDiagnostics(file)}, nil
}

// publishCascadeFailureDiagnostics surfaces every found-but-invalid
// anchoring Release the supplied cascade attempt encountered as an editor
// diagnostic on its own file, then pushes it via the same
// aggrega/publishDiagnostics channel lockfile-drift uses. Returns the
// number of failed candidates published. The load error already carries
// the rendered, path-pointed envelope detail (the same text the CLI
// shows); the diagnostic anchors at 1:1 because a document-envelope
// finding is schema-pointed, not line-mapped (matching the Tier-D /
// lockfile-drift fallback position).
//
// It takes the attempt rather than reading one, because the attempt is
// what the cascade returns and nothing retains it: the account of how a
// Release was chosen belongs to the call that chose one.
func (s *Server) publishCascadeFailureDiagnostics(ctx context.Context, attempt cascadeAttempt) int {
	failed := attempt.failedReleaseCandidates()
	for _, fc := range failed {
		s.setDiagnostics(fc.Path, []Diagnostic{
			{Line: 1, Column: 1, Severity: editorSeverityError, Message: fc.Err.Error(), ConditionCodes: conditionCodesOf(fc.Err)},
		})
		_ = s.publishDiagnostics(ctx, fc.Path)
	}
	return len(failed)
}

// yamlErrorRegex extracts the line number from a yaml.v3 unmarshal
// error message of the form "yaml: line N: ...". Column is not
// surfaced by yaml.v3 error messages; the diagnostic defaults to
// column 1 (start of the line).
var yamlErrorRegex = regexp.MustCompile(`yaml: line (\d+):`)

// yamlErrorToDiagnostic converts a yaml.v3 unmarshal error into a
// Diagnostic with a best-effort line number. Column defaults to 1
// (never 0) because yaml.v3 errors do not carry column information.
func yamlErrorToDiagnostic(err error) Diagnostic {
	line := 1
	if matches := yamlErrorRegex.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if n, perr := strconv.Atoi(matches[1]); perr == nil && n > 0 {
			line = n
		}
	}
	return Diagnostic{
		Line:           line,
		Column:         1,
		Severity:       editorSeverityError,
		Message:        err.Error(),
		ConditionCodes: conditionCodesOf(err),
	}
}

// diagnosticsParams is the request shape for `aggrega/diagnostics`.
type diagnosticsParams struct {
	File string `json:"file"`
}

// diagnosticsResult is the response shape for `aggrega/diagnostics`.
type diagnosticsResult struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (s *Server) dispatchDiagnostics(ctx context.Context, req *jsonRPCRequest) error {
	var params diagnosticsParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleDiagnostics(ctx, params)
	if ctx.Err() != nil {
		// The request itself was cancelled — a client's $/cancelRequest, or the
		// session's shutdown abort — while the handler was waiting for the
		// validation in flight for this file. Drop the response, as every other
		// cancelled request's dispatcher does; the sender asked for the answer to
		// stop coming.
		return nil
	}
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(ctx, req.ID, result)
}

// handleDiagnostics answers with the findings held for the file, waiting first
// for any validation of that same file that is already running.
//
// # The wait, and why the answer would otherwise be wrong rather than late
//
// A validation seeds two stores at two moments inside one handler: the parse
// cache, which is what decides whether this request can be answered at all, and
// the diagnostic layers, which are what it is answered from. Between those two
// moments the decision says yes and the layers are still empty, so a reader
// landing there is told the file is clean while the validation is about to report
// errors on it. That is worse than a refusal — a reader acts on "no problems" and
// does not ask again — so a reader for a file with a validation in flight waits
// for it and is answered with what it found. Waiting removes the window rather
// than narrowing it, because what is waited for is the validating handler's
// return and not a moment inside it.
//
// The wait hangs off the request's own context and adds no cancellation of its
// own: a client's $/cancelRequest ends it because that already cancels this
// context, and so does the session's shutdown abort. The only thing layered on is
// a deadline, which is a child of the same context — so a fired deadline leaves
// the request live and is answered as a refusal, while a cancelled request is
// answered by its dispatcher dropping the response.
//
// # The two refusals, which report different states
//
// The file-not-validated code now means exactly one thing: nothing has validated
// this file and nothing is validating it. A client sends a validation and asks
// again. It no longer covers "a validation is under way", which is the separate
// validation-in-flight code, reached only when the wait ran out of budget: there
// the file is being validated right now and the client retries rather than
// sending anything. Telling them apart is what lets a client do the right thing
// in each case.
//
// The parse cache's own staleness check can produce the first refusal on a file
// that WAS validated: it stats its key on every lookup and drops an entry the
// file on disk has outgrown, so an external edit reads as not-yet-validated,
// which is the honest answer for a buffer nothing has looked at since.
//
// # The spelling
//
// The requested file is resolved to its canonical spelling before anything is
// consulted, so the answer names the document rather than the spelling: a request
// naming the same file a validation named, in another spelling, is answered with
// the same findings rather than with an empty result. Both refusals still
// name the path the request asked about, not the resolved one, because a sender
// told about some other spelling of its own value has been handed a puzzle
// instead of an answer.
func (s *Server) handleDiagnostics(ctx context.Context, params diagnosticsParams) (diagnosticsResult, *jsonRPCError) {
	file := s.canonicalFile(params.File)
	// The deadline is a CHILD of the request's context rather than a replacement
	// for it, and that is what separates the two ways a wait can end. When the
	// budget expires the child is done and the request is still live, so there is
	// a caller to refuse; when the request is cancelled both are done, and the
	// branch below reads the parent to tell which happened.
	waitCtx, cancelWait := context.WithTimeout(ctx, s.validationWait())
	defer cancelWait()
	if err := s.validations.await(waitCtx, file); err != nil {
		if ctx.Err() != nil {
			return diagnosticsResult{}, &jsonRPCError{
				Code:    errValidationInFlight,
				Message: fmt.Sprintf("cancelled while waiting for the validation running for %s", params.File),
			}
		}
		return diagnosticsResult{}, &jsonRPCError{
			Code: errValidationInFlight,
			Message: fmt.Sprintf("a validation of %s is still running after %s; retry once it finishes",
				params.File, s.validationWait()),
		}
	}
	if _, ok := s.parseCache.Get(file); !ok {
		return diagnosticsResult{}, &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", params.File),
		}
	}
	return diagnosticsResult{Diagnostics: s.snapshotDiagnostics(file)}, nil
}

// irGraphParams is the request shape for `aggrega/irGraph`. The
// release field is a workspace-relative path to the Release YAML.
type irGraphParams struct {
	Release string `json:"release"`
}

// resolveReleaseParam turns the `release` request parameter into the
// absolute path the daemon reads, refusing any value that does not
// honour the parameter's contract: a path relative to the workspace
// root, naming a document inside it.
//
// The refusal is the point, not a repair. Joining an absolute value
// onto the workspace root concatenates rather than fails, so without
// this the daemon reads — and reports diagnostics against — a path that
// was never the Release's, and the reader is told about a file that does
// not exist. Normalising the value here instead would hide the same
// mistake in every client that makes it, so the request is rejected and
// its sender corrects it.
//
// Escape is judged on where the value lands, not on how it is spelled:
// the check runs against the joined-and-cleaned path, so an interior
// `a/../b` resolves and passes while `../../elsewhere` is refused.
//
// The absolute-path test above stays here rather than folding into the
// containment call below it, which would also refuse an absolute value:
// it names the specific fault, and a sender told its value is absolute
// has more to go on than one told it lands outside the root.
func resolveReleaseParam(workspaceRoot, release string) (string, *jsonRPCError) {
	return resolveWorkspacePathParam("release", "a Release", "the request named no Release", workspaceRoot, release)
}

// resolveWorkspacePathParam turns any request parameter that names a file into
// the absolute path the daemon reads, refusing every value that does not honour
// the contract those parameters share: a path relative to the workspace root,
// naming something inside it.
//
// The contract belongs to the surface rather than to any one parameter, which is
// why the check lives here once and each caller supplies only the words its own
// refusal needs — the field the request spells, what that field names, and how to
// say the request named nothing. A second copy of the check per parameter is how
// one of them would come to accept a path the others refuse.
//
// Escape is judged on where the value lands, not on how it is spelled: the check
// runs against the joined-and-cleaned path, so an interior `a/../b` resolves and
// passes while `../../elsewhere` is refused. The absolute-path test stays separate
// from the containment call below it, which would also refuse an absolute value:
// it names the specific fault, and a sender told its value is absolute has more to
// go on than one told it lands outside the root.
func resolveWorkspacePathParam(field, subject, absentFault, workspaceRoot, value string) (string, *jsonRPCError) {
	refuse := func(fault string) *jsonRPCError {
		return &jsonRPCError{
			Code: jsonRPCInvalidParams,
			Message: "invalid params: " + field + " must be a path relative to the workspace root, naming " +
				subject + " inside it; " + fault,
		}
	}
	if value == "" {
		return "", refuse(absentFault)
	}
	if filepath.IsAbs(value) {
		return "", refuse(fmt.Sprintf("%q is an absolute path", value))
	}
	resolved, ok := pathscope.Resolve(workspaceRoot, value)
	if !ok {
		return "", refuse(fmt.Sprintf("%q resolves outside the workspace root", value))
	}
	return resolved, nil
}

// irGraphResult is the response shape for `aggrega/irGraph`. The
// graph field is the serialized types.IRGraph.
type irGraphResult struct {
	Graph json.RawMessage `json:"graph"`
}

func (s *Server) dispatchIrGraph(ctx context.Context, req *jsonRPCRequest) error {
	var params irGraphParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleIrGraph(ctx, params)
	if ctx.Err() != nil {
		// Cancelled mid-pipeline (shutdown). Drop the response; the
		// dispatcher exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	return s.writeResult(ctx, req.ID, result)
}

// irGraphPipelineOptions builds the pipeline options for the irGraph
// inspection path. Render-skip is the default: irGraph reads only the IR
// graph and discards render output, so unless the rendering stage is
// explicitly requested (the build-on-inspect dial) the pipeline stops
// after the IR graph is finalised and never loads rendering plugins. The
// returned graph is byte-identical either way — render never mutates it.
//
// The dial reaches this option and nothing else. It selects no preview
// posture: the gate sequence runs ahead of the render under the inspection
// posture either way, so turning the dial on adds the rendering stage's own
// advisories and never a check that would refuse.
func (s *Server) irGraphPipelineOptions() pipeline.Options {
	return pipeline.Options{IncludeTrace: false, SkipRender: !s.buildOnInspect}
}

// handleIrGraph loads the Release at params.Release, derives the
// Aggregate and Manifest paths, reads them from disk, and invokes
// pipeline.RunPipeline to produce the IR graph. The graph is
// returned as JSON; trace data is discarded (Options.IncludeTrace is
// false).
//
// Path resolution: params.Release is a path relative to the daemon's
// workspace root, naming a Release inside it, and resolveReleaseParam
// refuses any other shape before a single file is read. Subsequent
// filesystem reads (Manifest, Aggregate, lockfile) resolve against the
// Release's containing directory.
//
// Catalog lookup: every lookup the pipeline needs is served by the
// session-scoped lazy lookup constructed at daemon startup, which
// covers the whole catalog surface rather than Component and Feature
// alone. This handler does not read that lookup directly — it forks a
// request-scoped view seeded with this request's lockfile pins, so the
// resolver lookup, the Recipe lookup and the sub-aggregate loader all
// resolve against one immutable pin snapshot and a concurrent request
// cannot perturb it.
//
// End-to-end fixture-driven coverage lives in
// cmd/aggrega/serve_test.go: TestServeAggregaIrGraphEndToEnd, which
// spawns the daemon binary against a workspace pointing at the
// fullstack-app fixture and asserts the round-trip produces a
// well-formed IRGraph envelope. Per-handler dispatch is additionally
// unit-tested via bytes.Buffer mocks in internal/serve/server_test.go;
// the pipeline.RunPipeline call is covered by pipeline's own tests.
//
// # Why building a graph refuses where the other surfaces advise
//
// Every other surface that resolves a plugin without a recorded digest resolves
// it anyway and says so, because reading a document is worth doing on a workspace
// nobody has prepared yet. Building a graph is the exception, and it is named as
// one rather than derived from a rule: an editor draws the graph a reader then
// reasons about, and a graph built from bytes nothing vouches for would be drawn
// exactly like one that was checked. An advisory alongside it is the wrong shape,
// because the reader is looking at the picture rather than at the log. So
// previewRelease refuses before anything is resolved, and names what to run.
//
// The refusal belongs to that computation and to nothing else — not to a class of
// handler, and not to a predicate a later reader could satisfy elsewhere and
// inherit the refusal by accident. It reaches a second caller today, the
// graph-node coordinate of aggrega/attachCandidates, and it reaches it for the
// same reason rather than by inheritance: that caller builds a graph too. A
// surface that does not build one crosses no such precondition, which is what
// keeps the name-addressed coordinate answering on an unprepared workspace.
//
// # A catalog directory the Release reads and nothing can walk is refused first
//
// The graph is the editor's counterpart of `aggrega inspect` on the same Release,
// and that command loads the Release's whole catalog stack before it reads
// anything else, so a configured directory that does not exist stops it with the
// load's own refusal. The lookup a graph resolves through finds what it can, and
// would draw the graph as though the directory were empty — a graph no build of
// the same Release reaches, because the build refuses the directory too. So the
// stack is asked first, through the load's own walk (catalogDirsRefusal), and the
// refusal is the command's, ahead of the lockfile gate as the command line reaches
// its stack before any lockfile.
func (s *Server) handleIrGraph(ctx context.Context, params irGraphParams) (irGraphResult, *jsonRPCError) {
	if errEnvelope := s.catalogDirsRefusal(params.Release); errEnvelope != nil {
		return irGraphResult{}, errEnvelope
	}
	preview, errEnvelope := s.previewRelease(ctx, params.Release, s.irGraphPipelineOptions())
	if errEnvelope != nil {
		// The panel is republished from whatever the run reached, and BEFORE the
		// refusal is returned. A gate that halted still found everything ahead of it,
		// and a reader who loses those findings has to re-run to see them — so the
		// failure path is the one they are most needed on, and the refusal envelope
		// carries only what refused.
		//
		// Publishing an empty layer is a real answer here rather than a wasted call:
		// the entries on screen belong to the request before this one, and leaving
		// them standing would show a reader findings that this run could not confirm
		// with nothing to say which is which.
		s.publishPreviewAdvisories(ctx, preview)
		return irGraphResult{}, errEnvelope
	}

	// 4. Drift verify, against the closure the preview computed. Loads the
	//    lockfile fresh (catches the case where the operator re-pulled between
	//    calls). On drift, publishes a Warning diagnostic on the lockfile path AND
	//    fires the aggrega/publishDiagnostics notification. Non-blocking — irGraph
	//    still produces the IR graph; drift is a Problems-panel signal, not a halt,
	//    which is why it runs here rather than as one of the preview's own gates.
	s.verifyDriftAndPublish(ctx, preview.lockfilePath, params.Release, &preview.resolved.Closure)

	// 5. Advisories to the Problems panel. Every exit-code-neutral finding the run
	//    raised — the binding-lint findings, the static-analysis advisory stream,
	//    the governance sockets' non-halting verdicts, and the render's own coded
	//    diagnostics — reaches the terminal on a command-line build and would
	//    otherwise reach nothing here, so they are published on the file the daemon
	//    attributes semantic findings to. Non-blocking, like drift: the graph is
	//    returned regardless.
	//
	//    The authored bytes are parsed so an advisory that named an authoring key
	//    lands on that key rather than at the head of the file; one that named a
	//    graph coordinate has no key to land on and stays at the head.
	//
	//    The declined external references travel in the SAME call rather than a
	//    second one, and that is load-bearing: the publication replaces the whole
	//    layer, so a second call would erase whichever layer the first one wrote.
	//    One call, one layer, every advisory class.
	s.publishPreviewAdvisories(ctx, preview)

	// The graph comes off the finished payload: the refusal above returned, so the
	// run this marshals from is one that ended.
	graphBytes, err := json.Marshal(preview.pipeline.Completed().IRGraph)
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: fmt.Sprintf("marshal IR graph: %v", err),
		}
	}
	return irGraphResult{Graph: graphBytes}, nil
}

// releasePreview is everything one preview of a Release produces that a
// caller downstream of it needs: the paths it resolved, the lockfile it
// loaded, the external-input seam it rendered through, the closure it
// computed and the pipeline result it ended with.
//
// It exists because several request surfaces need the same computation and must
// not each build it. A second construction of this sequence would agree on
// the ordinary case and part company on the awkward one — a differently
// ordered pin seed, a lockfile loaded at a different moment — and the
// divergence would be invisible until a preview and a graph query disagreed
// about the same Release. The count of those surfaces is deliberately not
// written here: a number beside the thing it counts is a second copy of what the
// call sites already say, and correcting it merely resets the clock until the
// next one is added.
type releasePreview struct {
	releasePath  string
	lockfilePath string
	fetchSeam    *externalinputs.DeclineRecorder
	resolved     *compilerops.ResolvedClosure

	// lockfile is the record of pinned versions this computation was seeded with,
	// or nil where it could not be loaded. A caller needing a fact the record holds
	// — the recorded withdrawal markers being the case that wants it — reads it here
	// rather than loading the record again, because a second read is a second value:
	// the operator may re-pull between the two, at which point a caller would report
	// markers from a record the listing beside them was not resolved against.
	lockfile *lockfile.Lockfile

	// pipeline is the run's outcome rather than its payload, so the two paths
	// below it — the refusal that publishes findings and the success that returns
	// a graph — each read what their own verdict entitles them to and neither can
	// read the other's.
	pipeline pipeline.Outcome
}

// previewRelease resolves the release parameter, applies the lockfile-absent
// gate, forks a request-scoped catalog view seeded with this request's pins,
// and runs the one preview computation in its inspection posture.
//
// # A refusal still hands back what the run reached
//
// When the preview computation fails, the returned envelope is non-nil AND the
// returned preview carries whatever the run had produced before it stopped — the
// document it resolved, the references the render had to decline, and the
// pipeline result where the run got far enough to hand one back. The two are not
// alternatives: discarding the preview here would destroy all of that one level
// below every caller, so no caller could publish any of it however it was
// written.
//
// A caller that has nothing to do with a partial run ignores it and returns the
// envelope, which is what the callers answering questions about attachment
// candidacy do. A caller that owes a reader the Problems panel publishes from it
// first. Both readings are correct because the value is offered rather than
// imposed.
//
// The preview is nil only where nothing ran at all — an unresolvable release
// parameter, or the lockfile gate refusing before any resolution — because there
// is genuinely nothing to hand back in those cases rather than something being
// withheld.
//
// # What it deliberately does NOT do
//
// It publishes nothing of what it computes. Drift diagnostics and advisories are
// a property of the surface that asked, not of the computation: a graph query
// draws a picture a reader then reasons about and owes them the Problems panel,
// while a question about which Features may attach to a node owes nothing to
// that panel and would, by publishing, replace a layer another request wrote. So
// the publications stay with the caller that wants them — which is exactly why
// the partial result above must survive the refusal.
//
// The one thing it does publish is not a finding of its own. The lockfile gate
// withdraws a drift finding whose lockfile it has found gone, whichever surface
// asked, because absence is a fact about the file rather than about the request
// — requireLockfile carries the reasoning.
//
// # Why the pipeline options are the caller's
//
// A caller reading only the IR graph has no use for the rendering stage, and
// the graph is byte-identical whether or not the render ran — the render
// never mutates it. So a caller that needs nothing rendered passes options
// that skip it and never loads a rendering plugin, while the graph query
// honours its own build-on-inspect dial.
func (s *Server) previewRelease(ctx context.Context, release string, opts pipeline.Options) (*releasePreview, *jsonRPCError) {
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, release)
	if paramErr != nil {
		return nil, paramErr
	}

	// 1. Lockfile-absent gate. Blocking — no IR graph is produced until the
	//    Release has a lockfile, which a dependency resolution of that Release
	//    writes: aggrega/resolveDependencies on this server, or the command the
	//    refusal names, `aggrega pull --release <release>`, at a terminal. A
	//    refusal for absence also withdraws the drift finding standing on that
	//    path; requireLockfile says why.
	lockfilePath := lockfile.PathFor(releasePath)
	if envelope := s.requireLockfile(ctx, lockfilePath, release); envelope != nil {
		return nil, envelope
	}

	// 2. Build the per-request catalog view, seeded with THIS request's pins so
	//    its Tier-2 cache reads verify-on-read against this Release's lockfile
	//    and a concurrent request resolving a different Release's pins can never
	//    perturb this resolution. The pins are loaded once here and the same
	//    view threads through closure compute (step 3), drift context, and the
	//    render pipeline (step 5), so the whole request resolves against one
	//    consistent pin snapshot. A failed/absent lockfile load yields nil pins,
	//    disabling cache trust for the request — never an inherited
	//    cross-request pin set.
	//
	//    ORDERING IS LOAD-BEARING: the view's pins are set (via ForRequest)
	//    before any Tier-2 read fires. Verify-on-read depends on the pins being
	//    present before the reads; an unpinned view can return no cached entity
	//    at all — a slot it cannot verify is not used, and this daemon's lookup
	//    carries no puller to re-establish one — so the inspection would degrade
	//    to an empty result for every cache-only entity while having verified
	//    nothing. Guarded end-to-end by
	//    TestServeIrGraphVerifiesCacheAgainstPinBeforePreview.
	var pins map[string]catalog.Pin
	lf, _, lfErr := lockfile.LoadLockfile(lockfilePath)
	if lfErr != nil || lf == nil {
		lf = nil
	} else {
		pins = catalog.PinsFromLockfile(lf)
	}
	// The view is forked from the lookup over this Release's own catalog stack,
	// which is the one a build of it at a terminal reads.
	view := s.catalogFor(releasePath).ForRequest(pins)

	fetchSeam := s.irGraphExternalInputs(lf)

	// 3. Run the one preview computation, in its inspection posture. It resolves
	//    the closure itself, so this handler does not resolve one separately and
	//    then render from a second resolution of the same Release. One resolution
	//    serves both, which is what guarantees the graph and the drift verdict at
	//    step 4 describe the same computed closure rather than two.
	//
	//    Recipe, sub-aggregate and mapping-library resolution all come from the
	//    request view (no eager whole-catalog load). Resilient inspection: a
	//    resolved catalog reference momentarily absent from the lazy view (a
	//    drifted cache slot skipped on read) is a drift signal, not an authoring
	//    fault — the graph builds with the entity skipped rather than halting, and
	//    drift surfaces as a Problems-panel diagnostic at step 4. The render-skip
	//    option means irGraph returns the IR graph even when resolution is
	//    incomplete; it does not run the render-time resolution-completeness halt
	//    (that gate belongs to the surfaces that emit an artifact).
	//    Inspection posture: the same computation the build runs, with the gates
	//    that would REFUSE left off. An editor that declines to draw the graph
	//    leaves the reader with nothing, where a build leaves them a diagnostic.
	preview, err := compilerops.PreviewForInspection(
		ctx,
		// An inspection fills nothing, so a lockfile it holds was on disk before
		// the request.
		s.irGraphPreviewInputs(view, releasePath, lf, fetchSeam, lf != nil),
		opts,
	)

	// Assembled once, before the verdict is read, so the refusal and the success
	// hand back the same shape built the same way. A second literal on the failure
	// branch would be free to populate a different subset of the fields, and the
	// two would agree until one was edited.
	out := &releasePreview{
		releasePath:  releasePath,
		lockfilePath: lockfilePath,
		fetchSeam:    fetchSeam,
		lockfile:     lf,
	}
	if preview != nil {
		out.resolved = preview.Resolved
		out.pipeline = preview.PipelineOutcome()
	}

	if err != nil {
		return out, s.previewFailureEnvelope(view, releasePath, preview, err)
	}
	return out, nil
}

// publishPreviewAdvisories replaces the build diagnostic layer with the
// advisories one preview raised, positioned against the Aggregate it resolved.
//
// It exists because two call sites need it and they are the SUCCESS and FAILURE
// paths of one request. Written twice, the failure path would be free to publish
// a different set from the success path — a subset, a different file, or the
// advisories without the declined references — and nothing would compare them,
// because each reads correctly alone. One body means a refusal publishes exactly
// what a success publishes, and the only difference between the two paths is what
// is returned afterwards.
//
// A preview that reached no resolution publishes nothing rather than an empty
// layer: with no Aggregate path there is no file to attribute findings to, and
// replacing the layer with nothing would clear entries an earlier request wrote
// while having learned nothing about them.
func (s *Server) publishPreviewAdvisories(ctx context.Context, preview *releasePreview) {
	if preview == nil || preview.resolved == nil {
		return
	}
	// Read through the accessor that answers on either verdict, which is what
	// makes one body serve both paths: a run that halted publishes the findings
	// the phases ahead of it raised, and a run that finished publishes the same
	// set it returned.
	advisories := preview.pipeline.Advisories()
	s.publishRenderDiagnostics(ctx, preview.resolved.AggregatePath, advisories,
		externalInputDivergenceDiagnostics(preview.fetchSeam.Declined(), preview.releasePath),
		preview.resolved.AggBytes)
}

// MarkupContent matches the LSP shape for hover content.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// CompletionItem matches the LSP shape for completion candidates.
type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	SortText      string `json:"sortText,omitempty"`
}

// resolveParams is the request shape for `aggrega/resolve`. The
// cursor position references a YAML node in the file at uri.
//
// Release is optional. Absent, the definition is looked for in the workspace's
// own terms: among the workspace documents first, and through the workspace's
// own catalog directories where no workspace document declares the name. Named,
// it is a path relative to the workspace root naming a Release inside it, refused
// in the shapes every Release parameter is refused in, and the definition is
// looked for in that Release's terms (termsFor): through the catalog directories
// the workspace configuration gives that Release first, and among the workspace
// documents only where those directories resolve nothing — of which a document
// under one of the workspace's catalog directories that Release does not read is
// never a candidate.
type resolveParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
	Release  string           `json:"release,omitempty"`
}

// resolveResult is the response shape for `aggrega/resolve`. Location
// is null when no definition site is found.
type resolveResult struct {
	Location *yamlpos.Location `json:"location"`
}

// dispatchResolve unmarshals params and invokes resolveDefinitionSite.
// File-not-validated produces a 1001 error envelope; every other
// outcome (definition found, definition not found, cursor on a
// non-reference position) produces a result envelope with a possibly-
// nil Location.
func (s *Server) dispatchResolve(ctx context.Context, req *jsonRPCRequest) error {
	var params resolveParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	terms, errEnvelope := s.termsFor(params.Release)
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	loc, errEnvelope := s.resolveDefinitionSite(ctx, params.URI, params.Position, terms)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(ctx, req.ID, resolveResult{Location: loc})
}

// findReferencesParams is the request shape for
// `aggrega/findReferences`. The cursor position must address a
// metadata.name scalar for the result to be non-empty.
//
// Release is optional, read as resolveParams reads it: named, a reference in a
// workspace document that Release's terms do not hold is not reported.
type findReferencesParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
	Release  string           `json:"release,omitempty"`
}

// findReferencesResult is the response shape for `aggrega/findReferences`.
type findReferencesResult struct {
	Locations []yamlpos.Location `json:"locations"`
}

// dispatchFindReferences unmarshals params and invokes
// findReferencesAcrossWorkspace. File-not-validated produces 1001;
// every other outcome (matches found, no matches, cursor not on
// metadata.name) produces a result envelope.
func (s *Server) dispatchFindReferences(ctx context.Context, req *jsonRPCRequest) error {
	var params findReferencesParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	terms, errEnvelope := s.termsFor(params.Release)
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	locs, errEnvelope := s.findReferencesAcrossWorkspace(ctx, params.URI, params.Position, terms.reach)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	if locs == nil {
		locs = []yamlpos.Location{}
	}
	return s.writeResult(ctx, req.ID, findReferencesResult{Locations: locs})
}

// findImplementationsParams is the request shape for
// `aggrega/findImplementations`.
//
// Release is optional, read as resolveParams reads it: named, an implementing or
// extending document that Release's terms do not hold is not reported.
type findImplementationsParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
	Release  string           `json:"release,omitempty"`
}

// findImplementationsResult is the response shape for
// `aggrega/findImplementations`.
type findImplementationsResult struct {
	Locations []yamlpos.Location `json:"locations"`
}

// dispatchFindImplementations unmarshals params and invokes
// findImplementationsAcrossWorkspace.
func (s *Server) dispatchFindImplementations(ctx context.Context, req *jsonRPCRequest) error {
	var params findImplementationsParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	terms, errEnvelope := s.termsFor(params.Release)
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	locs, errEnvelope := s.findImplementationsAcrossWorkspace(ctx, params.URI, params.Position, terms.reach)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	if locs == nil {
		locs = []yamlpos.Location{}
	}
	return s.writeResult(ctx, req.ID, findImplementationsResult{Locations: locs})
}

// hoverParams is the request shape for `aggrega/hover`. The cursor
// position references a YAML node in the file at uri.
//
// Release is optional, read as resolveParams reads it: named, the entity shown
// is the one the reference resolves to in that Release's terms.
type hoverParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
	Release  string           `json:"release,omitempty"`
}

// hoverResult is the response shape for `aggrega/hover`. Contents is
// null when the cursor position has no hover-eligible target.
type hoverResult struct {
	Contents *MarkupContent `json:"contents"`
}

// dispatchHover unmarshals params, resolves the target entity at the
// cursor, dispatches to the appropriate per-kind renderer, and writes
// the response envelope. File-not-validated produces a 1001 error
// envelope; every other outcome produces a result envelope with a
// possibly-nil Contents.
func (s *Server) dispatchHover(ctx context.Context, req *jsonRPCRequest) error {
	if ctx.Err() != nil {
		return nil
	}
	var params hoverParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	terms, errEnvelope := s.termsFor(params.Release)
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	contents, errEnvelope := s.handleHover(ctx, params, terms)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(ctx, req.ID, hoverResult{Contents: contents})
}

// completionParams is the request shape for `aggrega/completion`.
// The optional Context carries the LSP CompletionContext (triggerKind
// + triggerCharacter) for trigger-character-aware narrowing.
//
// Release is optional, read as resolveParams reads it: named, the names offered
// are those of the workspace documents in that Release's terms and of the
// entities the catalog directories the workspace configuration gives that
// Release declare.
type completionParams struct {
	URI      string             `json:"uri"`
	Position yamlpos.Position   `json:"position"`
	Context  *completionContext `json:"context,omitempty"`
	Release  string             `json:"release,omitempty"`
}

// completionContext mirrors a slice of LSP's CompletionContext shape.
// triggerKind per LSP: 1 = Invoked (no trigger character), 2 =
// TriggerCharacter, 3 = TriggerForIncompleteCompletions. Only
// triggerCharacter is consumed today; other fields are accepted
// and ignored for forward-compatibility.
type completionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

// completionResult is the response shape for `aggrega/completion`.
type completionResult struct {
	Items []CompletionItem `json:"items"`
}

// dispatchCompletion unmarshals params, invokes handleCompletion,
// writes the response envelope. File-not-validated produces 1001;
// all other outcomes produce a result envelope with a possibly-empty
// Items slice.
func (s *Server) dispatchCompletion(ctx context.Context, req *jsonRPCRequest) error {
	if ctx.Err() != nil {
		return nil
	}
	var params completionParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	terms, errEnvelope := s.termsFor(params.Release)
	if errEnvelope != nil {
		return s.writeErrorEnvelope(ctx, req.ID, errEnvelope)
	}
	items, errEnvelope := s.handleCompletion(ctx, params, terms)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	if items == nil {
		items = []CompletionItem{}
	}
	return s.writeResult(ctx, req.ID, completionResult{Items: items})
}
