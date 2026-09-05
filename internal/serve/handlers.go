// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
// daemon reads positionEncodings off the client's capabilities; other
// LSP initialize fields are accepted but ignored today.
type initializeParams struct {
	Capabilities clientCapabilities `json:"capabilities"`
}

// clientCapabilities mirrors a tiny slice of LSP's client capabilities
// shape — only the position-encoding negotiation surface.
type clientCapabilities struct {
	General *generalCapabilities `json:"general,omitempty"`
}

// generalCapabilities carries LSP 3.17's general capability bag. The
// daemon consumes positionEncodings; everything else is ignored.
type generalCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// serverCapabilities is the daemon's reply on initialize. The wire
// shape is intentionally compact — only the position-encoding
// negotiation result. Future capabilities extend this struct without
// breaking existing clients.
type serverCapabilities struct {
	PositionEncoding string `json:"positionEncoding"`
}

// initializeResult is the response shape for aggrega/initialize.
// Capabilities carries the negotiated position encoding. WorkspaceRoot
// reports the absolute path the daemon resolved as the workspace
// anchor (via discovery or explicit --workspace-root). ConfigSource
// names the highest-precedence configuration layer that contributed
// to the effective Config: "workspace" | "global" | "defaults".
// WorkspaceMarker names the marker tier that anchored the workspace
// root: "config" | "directory" | "aggregate-glob" | "release-glob" |
// "manifest-glob"; the field is omitted on the wire when the daemon
// anchored via an explicit --workspace-root (no marker consulted).
// Additive only: clients that read only Capabilities continue to
// work; the new fields are opt-in for clients that surface them.
type initializeResult struct {
	Capabilities    serverCapabilities `json:"capabilities"`
	WorkspaceRoot   string             `json:"workspaceRoot"`
	ConfigSource    string             `json:"configSource"`
	WorkspaceMarker string             `json:"workspaceMarker,omitempty"`
}

// releaseCandidatesResult is the aggrega/releaseCandidates response: the
// absolute paths of every Release-kind document under the workspace root,
// determined by content (the file's `kind: Release` header), not by filename.
// The IDE consumes this for its "which Release?" quick-pick so the editor's
// candidate set matches exactly what the daemon resolves — a Release named
// without a conventional prefix is included, and a non-Release file that
// happens to be named like one is not.
type releaseCandidatesResult struct {
	Releases []string `json:"releases"`
}

// dispatchReleaseCandidates returns the workspace's Release-candidate set —
// the content-kind walk the active-Release cascade already performs, surfaced
// as a list so the IDE delegates Release detection to the daemon rather than
// reimplementing it. Takes no params: the set is a property of the daemon's
// workspace root, which it resolved at startup.
func (s *Server) dispatchReleaseCandidates(ctx context.Context, req *jsonRPCRequest) error {
	releases := walkForReleases(s.workspaceRoot)
	if releases == nil {
		releases = []string{}
	}
	return s.writeResult(ctx, req.ID, releaseCandidatesResult{Releases: releases})
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
type activeReleaseResult struct {
	Release string `json:"release"`
	Reason  string `json:"reason,omitempty"`
	Trace   string `json:"trace,omitempty"`
}

// dispatchActiveRelease reports which Release a client should select by
// default: the one the workspace's disambiguation cascade picks from the
// authoring-release configuration, the operator's marker file, and a lone
// Release in the tree, in that order. An editor showing the operator which
// Release is in force asks for it here rather than re-deriving it from one of
// the cascade's inputs, because an input the cascade did not end up using
// names a different Release than the one it chose.
//
// Takes no params: the answer is a property of the daemon's workspace root.
//
// The cascade runs on every call, and that is the point rather than an
// oversight. The daemon holds no Release of its own, so this answer is a
// reading of the workspace as it stands at the moment of asking: a client that
// asks again after writing a marker, editing the configuration or adding a
// Release file gets the new answer, where an answer computed once for the life
// of the process would keep reporting the workspace the daemon started in. The
// walk this performs is also why it is a request rather than part of the
// handshake, which deliberately answers before any workspace-wide walk so a
// large tree cannot delay it.
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
	_, releasePath, attempt := runManifestCascade(s.workspaceRoot, s.wsConfig, s.stderr)
	s.publishCascadeFailureDiagnostics(ctx, attempt)
	result := activeReleaseResult{Release: releasePath}
	if releasePath == "" {
		result.Reason = attempt.diagnosticMessage()
		result.Trace = attempt.fullTrace()
	}
	return s.writeResult(ctx, req.ID, result)
}

// dispatchInitialize selects a position encoding from the client's
// offered list using SERVER preference order: UTF-8 > UTF-16 > UTF-32.
//
// Server-preference (rather than client-preference) because UTF-8 is
// yaml.v3's native byte representation — every reference target the
// resolve family handles lands in UTF-8 bytes already, so UTF-8
// position math is a no-op per scalar lookup, whereas UTF-16 / UTF-32
// require per-rune decoding. The performance asymmetry between
// encodings is one-sided enough that the client's preference does
// not change the optimal choice for THIS server.
//
// UTF-16 is the LSP 3.17 default and remains the fallback when no
// negotiation occurs (s.positionEncoding is initialized to UTF-16
// in Run before dispatchInitialize ever runs). Unknown encoding
// strings in the client's list are ignored.
func (s *Server) dispatchInitialize(ctx context.Context, req *jsonRPCRequest) error {
	var params initializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	selected := selectPositionEncoding(params.Capabilities)
	s.setPositionEncoding(selected)
	if err := s.writeResult(ctx, req.ID, initializeResult{
		Capabilities: serverCapabilities{
			PositionEncoding: string(selected),
		},
		WorkspaceRoot:   s.workspaceRoot,
		ConfigSource:    s.configSource,
		WorkspaceMarker: s.workspaceMarker.String(),
	}); err != nil {
		return err
	}

	// Post-response flush of any diagnostics already populated. The
	// LSP-canonical pattern is to push diagnostics AFTER the initialize
	// response so the client's handshake completes before unsolicited
	// notifications start arriving. Nothing populates diagnostics before a
	// request asks for them, so at handshake time the map is normally
	// empty; the loop is a no-op then and flushes only what a prior
	// request already produced.
	// diagnosticsFiles returns the keys sorted for deterministic
	// ordering across daemon runs (Go map iteration is randomized).
	for _, file := range s.diagnosticsFiles() {
		if err := s.publishDiagnostics(ctx, file); err != nil {
			fmt.Fprintf(s.stderr, "  publishDiagnostics %s: %v\n", file, err)
		}
	}
	return nil
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
// and whether its own shape is admissible — and nothing that needs a Release
// was run.
const checkedDocument = "document"

// dispatchValidate parses the request params, calls handleValidate,
// and writes the response envelope.
func (s *Server) dispatchValidate(ctx context.Context, req *jsonRPCRequest) error {
	var params validateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
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
// request named a Release — runs the full resolver and semantic checks
// against that Release.
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
	// name the file relative to the workspace root or by absolute path, and the
	// two name one document; resolving per store instead would let one request
	// populate a key the next request's spelling cannot read, which reads back as
	// a file with nothing to report rather than as a request that missed.
	file := s.canonicalFile(params.File)

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(params.Content), &doc); err != nil {
		// One slice for both the store and the response, so the source the
		// store stamps is the source the caller receives. Two slices built
		// from the same value would leave the returned one unstamped.
		diags := []Diagnostic{yamlErrorToDiagnostic(err)}
		s.setDiagnostics(file, diags)
		s.parseCache.Drop(file)
		return validateResult{Diagnostics: diags, Checked: checked}, nil
	}
	s.parseCache.Put(file, &doc, []byte(params.Content))
	// Per-file document-envelope validation on the buffer: any classifiable
	// aggrega document (not just the Release's Aggregate) gets the same
	// envelope verdict the CLI gives it, on its own file, at the offending
	// key, on unsaved content. Findings early-return — which short-circuits
	// the semantic pass for that Aggregate (a structurally invalid buffer
	// makes the disk-based pipeline's verdict misleading) and is the honest
	// coverage for every other file. Foreign YAML and envelope-clean
	// documents fall through to the existing flow unchanged.
	if diags, hasFindings := s.envelopeDiagnosticsForBuffer(file, releasePath, []byte(params.Content), &doc); hasFindings {
		s.setDiagnostics(file, diags)
		return validateResult{Diagnostics: diags, Checked: checked}, nil
	}
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
	// Tier 4: structured semantic violations via RunTier4Pipeline, against the
	// Release this request named. A Release that cannot be loaded surfaces as a
	// closure-level diagnostic from that path, on the validated file, rather
	// than as a refusal: the request named a real coordinate, and what went
	// wrong is something the author can read and fix.
	diags := s.runTier4Validation(ctx, releasePath, file, []byte(params.Content), &doc)
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
			{Line: 1, Column: 1, Severity: editorSeverityError, Message: fc.Err.Error(), Code: diagnosticCode(fc.Err)},
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
		Line:     line,
		Column:   1,
		Severity: editorSeverityError,
		Message:  err.Error(),
		Code:     diagnosticCode(err),
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
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleDiagnostics(params)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(ctx, req.ID, result)
}

// handleDiagnostics returns the cached diagnostics for the file. If
// the file is not in the parse cache (either never validated, or
// evicted by mtime check during Get), returns error code 1001
// file-not-validated.
//
// The mtime check fires via parseCache.Get's transparent eviction —
// when an entry's cachedAt is older than the file's on-disk mtime,
// Get drops the entry and reports miss, matching the AC 5 contract.
//
// The requested file is resolved to its canonical spelling before either
// lookup, so the answer names the document rather than the spelling: a request
// naming the same file the validation named, in the other spelling, is answered
// with the same findings rather than with an empty result. The refusal below
// still names the path the request asked about, not the resolved one, because a
// sender told about some other spelling of its own value has been handed a
// puzzle instead of an answer.
func (s *Server) handleDiagnostics(params diagnosticsParams) (diagnosticsResult, *jsonRPCError) {
	file := s.canonicalFile(params.File)
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
	if release == "" {
		return "", invalidReleaseParam("the request named no Release")
	}
	if filepath.IsAbs(release) {
		return "", invalidReleaseParam(fmt.Sprintf("%q is an absolute path", release))
	}
	resolved, ok := pathscope.Resolve(workspaceRoot, release)
	if !ok {
		return "", invalidReleaseParam(fmt.Sprintf("%q resolves outside the workspace root", release))
	}
	return resolved, nil
}

// invalidReleaseParam builds the refusal a malformed `release` value
// draws. Every branch names the contract first and the specific fault
// second, so a client author reading one message learns what to send
// without consulting anything else.
func invalidReleaseParam(fault string) *jsonRPCError {
	return &jsonRPCError{
		Code:    jsonRPCInvalidParams,
		Message: "invalid params: release must be a path relative to the workspace root, naming a Release inside it; " + fault,
	}
}

// irGraphResult is the response shape for `aggrega/irGraph`. The
// graph field is the serialized types.IRGraph.
type irGraphResult struct {
	Graph json.RawMessage `json:"graph"`
}

func (s *Server) dispatchIrGraph(ctx context.Context, req *jsonRPCRequest) error {
	var params irGraphParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
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
func (s *Server) handleIrGraph(ctx context.Context, params irGraphParams) (irGraphResult, *jsonRPCError) {
	preview, errEnvelope := s.previewRelease(ctx, params.Release, s.irGraphPipelineOptions())
	if errEnvelope != nil {
		return irGraphResult{}, errEnvelope
	}

	// 4. Drift verify, against the closure the preview computed. Loads the
	//    lockfile fresh (catches the case where the operator re-pulled between
	//    calls). On drift, publishes a Warning diagnostic on the lockfile path AND
	//    fires the aggrega/publishDiagnostics notification. Non-blocking — irGraph
	//    still produces the IR graph; drift is a Problems-panel signal, not a halt,
	//    which is why it runs here rather than as one of the preview's own gates.
	s.verifyDriftAndPublish(ctx, preview.lockfilePath, preview.resolved.Closure)

	// 5. Render advisories to the Problems panel. The render step's coded
	//    diagnostics — a target that cannot honor an authored field, an
	//    attachment delivering a declared-sensitive datum as a literal — reach
	//    the terminal on a CLI build and would otherwise reach nothing here, so
	//    they are published on the file the daemon attributes semantic findings
	//    to. Non-blocking, like drift: the graph is returned regardless. The
	//    render only runs when the build-on-inspect dial is on, so with it off
	//    this replaces an empty layer with an empty layer.
	//
	//    The declined external references travel in the SAME call rather than a
	//    second one, and that is load-bearing: the publication replaces the whole
	//    render layer, so a second call would erase whichever layer the first one
	//    wrote. One call, one layer, both advisory classes.
	s.publishRenderDiagnostics(ctx, preview.resolved.AggregatePath, preview.pipeline.RenderDiagnostics,
		externalInputDivergenceDiagnostics(preview.fetchSeam.Declined(), preview.releasePath))

	graphBytes, err := json.Marshal(preview.pipeline.IRGraph)
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
// It exists because two request surfaces need the same computation and must
// not each build it. A second construction of this sequence would agree on
// the ordinary case and part company on the awkward one — a differently
// ordered pin seed, a lockfile loaded at a different moment — and the
// divergence would be invisible until a preview and a graph query disagreed
// about the same Release.
type releasePreview struct {
	releasePath  string
	lockfilePath string
	fetchSeam    *externalinputs.DeclineRecorder
	resolved     *compilerops.ResolvedClosure
	pipeline     *pipeline.Result
}

// previewRelease resolves the release parameter, applies the lockfile-absent
// gate, forks a request-scoped catalog view seeded with this request's pins,
// and runs the one preview computation in its inspection posture.
//
// # What it deliberately does NOT do
//
// It publishes nothing. Drift diagnostics and render advisories are a
// property of the surface that asked, not of the computation: a graph query
// draws a picture a reader then reasons about and owes them the Problems
// panel, while a question about which Features may attach to a node owes
// nothing to that panel and would, by publishing, replace a layer another
// request wrote. So the publications stay with the caller that wants them.
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

	// 1. Lockfile-absent gate. Blocking — no IR graph produced
	//    until the operator regenerates the lockfile (via the
	//    extension's "Generate Lockfile" toast button, which runs
	//    aggrega pull in a terminal, or by running aggrega pull
	//    manually).
	lockfilePath := lockfile.PathFor(releasePath)
	if _, err := os.Stat(lockfilePath); err != nil {
		if os.IsNotExist(err) {
			return nil, &jsonRPCError{
				Code:    errLockfileAbsent,
				Message: fmt.Sprintf("lockfile %s not found; run `aggrega pull --release %s` to generate", lockfilePath, release),
				Data:    newLockfileErrorData(lockfilePath, release),
			}
		}
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("lockfile stat %s: %v", lockfilePath, err),
			Data:    newLockfileErrorData(lockfilePath, release),
		}
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
	view := s.lazyCatalog.ForRequest(pins)

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
		s.irGraphPreviewInputs(view, releasePath, lf, fetchSeam),
		opts,
	)
	if err != nil {
		return nil, s.previewFailureEnvelope(view, releasePath, preview, err)
	}

	return &releasePreview{
		releasePath:  releasePath,
		lockfilePath: lockfilePath,
		fetchSeam:    fetchSeam,
		resolved:     preview.Resolved,
		pipeline:     preview.Pipeline,
	}, nil
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
type resolveParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
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
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	loc, errEnvelope := s.resolveDefinitionSite(ctx, params.URI, params.Position)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(ctx, req.ID, resolveResult{Location: loc})
}

// findReferencesParams is the request shape for
// `aggrega/findReferences`. The cursor position must address a
// metadata.name scalar for the result to be non-empty.
type findReferencesParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
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
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	locs, errEnvelope := s.findReferencesAcrossWorkspace(ctx, params.URI, params.Position)
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
type findImplementationsParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
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
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	locs, errEnvelope := s.findImplementationsAcrossWorkspace(ctx, params.URI, params.Position)
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
type hoverParams struct {
	URI      string           `json:"uri"`
	Position yamlpos.Position `json:"position"`
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
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	contents, errEnvelope := s.handleHover(params)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(ctx, req.ID, hoverResult{Contents: contents})
}

// completionParams is the request shape for `aggrega/completion`.
// The optional Context carries the LSP CompletionContext (triggerKind
// + triggerCharacter) for trigger-character-aware narrowing.
type completionParams struct {
	URI      string             `json:"uri"`
	Position yamlpos.Position   `json:"position"`
	Context  *completionContext `json:"context,omitempty"`
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
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	items, errEnvelope := s.handleCompletion(params)
	if errEnvelope != nil {
		return s.writeError(ctx, req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	if items == nil {
		items = []CompletionItem{}
	}
	return s.writeResult(ctx, req.ID, completionResult{Items: items})
}
