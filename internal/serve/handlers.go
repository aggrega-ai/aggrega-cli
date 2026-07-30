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
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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

type initializeResult struct {
	Capabilities    serverCapabilities `json:"capabilities"`
	WorkspaceRoot   string             `json:"workspaceRoot"`
	ConfigSource    string             `json:"configSource"`
	WorkspaceMarker string             `json:"workspaceMarker,omitempty"`
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
	// notifications start arriving. This handler does not trigger the
	// active-Release cascade (initialize needs no Release context), so at
	// handshake time the map is normally empty; the loop is a no-op
	// then and flushes only what a prior request already produced.
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
type validateParams struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

// validateResult is the response shape for `aggrega/validate`.
type validateResult struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

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
// the daemon's parse cache + diagnostic cache for the file. The
// validation runs at Tier 1 (parse-only) when no active Release is
// selected; Tier 4 (full resolver + semantic checks via the pipeline
// path) when an active Release is selected.
//
// When the workspace has Release files but the disambiguation cascade
// did not select one (multiple Releases without a marker or config
// pointer), the Tier-4 branch emits a 1002 errReleaseNotResolved
// envelope so the extension can surface a non-modal remediation toast
// with action buttons. The activeRelease == nil + workspaceFiles == 0
// scaffold case (no Release at all) returns Tier-1 diagnostics only:
// the 1002 path fires only when the cascade attempted to find a
// Release and could not.
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
	// Every Release-context read below (the active-Release nil check, the
	// cascade-attempt diagnostics, runTier4Validation's activeReleasePath
	// reads, and classifyByFilename via envelopeDiagnosticsForBuffer)
	// depends on the disambiguation cascade having run. Trigger it here,
	// on first use, so those reads never see a zero-value Release the
	// cascade would have populated. aggrega/initialize deliberately does
	// not reach this path, so the handshake answers before the cascade's
	// workspace walk runs.
	s.ensureCascade()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(params.Content), &doc); err != nil {
		// One slice for both the store and the response, so the source the
		// store stamps is the source the caller receives. Two slices built
		// from the same value would leave the returned one unstamped.
		diags := []Diagnostic{yamlErrorToDiagnostic(err)}
		s.setDiagnostics(params.File, diags)
		s.parseCache.Drop(params.File)
		return validateResult{Diagnostics: diags}, nil
	}
	s.parseCache.Put(params.File, &doc, []byte(params.Content))
	// Per-file document-envelope validation on the buffer: any classifiable
	// aggrega document (not just the active Aggregate) gets the same
	// envelope verdict the CLI gives it, on its own file, at the offending
	// key, on unsaved content. Findings early-return — which short-circuits
	// Tier-4 for the active Aggregate (a structurally invalid buffer makes
	// the disk-based semantic pipeline's verdict misleading) and is the
	// honest coverage for every other file. Foreign YAML and envelope-clean
	// documents fall through to the existing flow unchanged.
	if diags, hasFindings := s.envelopeDiagnosticsForBuffer(params.File, []byte(params.Content), &doc); hasFindings {
		s.setDiagnostics(params.File, diags)
		return validateResult{Diagnostics: diags}, nil
	}
	if s.activeRelease == nil {
		// A Release the cascade FOUND but could not load (envelope
		// violation, parse failure) is distinct from a genuinely-absent
		// Release: squiggle it on its own file via the same publish
		// channel lockfile-drift uses, so the Operator authoring a broken
		// Release sees the error there rather than only in stderr. Covers
		// all three cascade load steps (authoring_release / single-Release
		// heuristic / marker target).
		failed := s.publishCascadeFailureDiagnostics(ctx)
		// Three sub-cases:
		//   (a) cascade exhausted but Release files exist, OR a found-but-
		//       invalid candidate exists → operator's authoring needs a
		//       deliberate choice; emit 1002 so the extension toasts.
		//   (b) no Release files and nothing found-but-invalid → catalog-only
		//       or scaffold case; Tier-1 diagnostics suffice. No spurious toast.
		if s.cascadeAttempt.ReleaseCandidatesFound > 0 || failed > 0 {
			return validateResult{}, s.emitReleaseNotResolvedEnvelope(params.File)
		}
		s.setDiagnostics(params.File, nil)
		return validateResult{Diagnostics: nil}, nil
	}
	// Tier 4: structured semantic violations via RunTier4Pipeline.
	diags := s.runTier4Validation(ctx, params.File, []byte(params.Content), &doc)
	if ctx.Err() != nil {
		// Cancelled mid-pipeline. Leave the prior diagnostic cache
		// entry intact and return without overwriting it — which means
		// stamping the layer's source here, since the store is what
		// normally stamps it.
		return validateResult{Diagnostics: stampSource(diags, DiagnosticSourceSemantic)}, nil
	}
	s.setDiagnostics(params.File, diags)
	// The response carries the composed view, not the semantic layer alone: the
	// client replaces its whole per-file collection from this reply, so returning
	// one layer would erase any render advisory standing on the same file.
	return validateResult{Diagnostics: s.snapshotDiagnostics(params.File)}, nil
}

// publishCascadeFailureDiagnostics surfaces every found-but-invalid
// anchoring Release the cascade encountered as an editor diagnostic on
// its own file, then pushes it via the same aggrega/publishDiagnostics
// channel lockfile-drift uses. Returns the number of failed candidates
// published. The load error already carries the rendered, path-pointed
// envelope detail (the same text the CLI shows); the diagnostic anchors
// at 1:1 because a document-envelope finding is schema-pointed, not
// line-mapped (matching the Tier-D / lockfile-drift fallback position).
func (s *Server) publishCascadeFailureDiagnostics(ctx context.Context) int {
	failed := s.cascadeAttempt.failedReleaseCandidates()
	for _, fc := range failed {
		s.setDiagnostics(fc.Path, []Diagnostic{
			{Line: 1, Column: 1, Severity: "error", Message: fc.Err.Error()},
		})
		_ = s.publishDiagnostics(ctx, fc.Path)
	}
	return len(failed)
}

// emitReleaseNotResolvedEnvelope constructs the 1002 jsonRPCError
// envelope that handlers needing Release context emit when
// activeRelease == nil and the cascade has been attempted. The
// envelope's Message carries the one-line cascade summary the
// extension renders in its toast; the envelope's Data carries the
// validated file's path plus the multi-line cascade trace the
// extension auto-logs to its OutputChannel.
func (s *Server) emitReleaseNotResolvedEnvelope(file string) *jsonRPCError {
	return &jsonRPCError{
		Code:    errReleaseNotResolved,
		Message: s.cascadeDiagnosticMessage(),
		Data:    newErrorDataWithCascade(file, s.cascadeAttemptFullTrace()),
	}
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
		Severity: "error",
		Message:  err.Error(),
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
func (s *Server) handleDiagnostics(params diagnosticsParams) (diagnosticsResult, *jsonRPCError) {
	if _, ok := s.parseCache.Get(params.File); !ok {
		return diagnosticsResult{}, &jsonRPCError{
			Code:    errFileNotValidated,
			Message: fmt.Sprintf("file not validated: %s", params.File),
		}
	}
	return diagnosticsResult{Diagnostics: s.snapshotDiagnostics(params.File)}, nil
}

// irGraphParams is the request shape for `aggrega/irGraph`. The
// release field is a workspace-relative path to the Release YAML.
type irGraphParams struct {
	Release string `json:"release"`
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

// handleIrGraph loads the Release at params.Release, derives the
// Aggregate and Manifest paths, reads them from disk, and invokes
// pipeline.RunPipeline to produce the IR graph. The graph is
// returned as JSON; trace data is discarded (Options.IncludeTrace is
// false).
//
// Path resolution: params.Release is interpreted relative to the
// daemon's workspace root. Subsequent filesystem reads (Manifest,
// Aggregate, lockfile data when the lockfile machinery ships)
// resolve against the Release's containing directory.
//
// Catalog lookup asymmetry: the resolver.CatalogLookup argument
// (Component + Feature) comes from the session-scoped lazy lookup
// constructed at daemon startup — per-URI cascade through Tier-1
// (filesystem) → Tier-2 (workspace cache) → Tier-3 (OCI registry).
// The renderer.RecipeLookup + topology.SubAggregateLoader arguments
// come from an eager *catalog.Catalog loaded on first use; implements-
// keyed Recipe lookup with three-form version precedence is a
// separate design problem that the lazy primitive does not address.
//
// End-to-end fixture-driven coverage lives in
// cmd/aggrega/serve_test.go: TestServeAggregaIrGraphEndToEnd, which
// spawns the daemon binary against a workspace pointing at the
// fullstack-app fixture and asserts the round-trip produces a
// well-formed IRGraph envelope. Per-handler dispatch is additionally
// unit-tested via bytes.Buffer mocks in internal/serve/server_test.go;
// the pipeline.RunPipeline call is covered by pipeline's own tests.
// irGraphPipelineOptions builds the pipeline options for the irGraph
// inspection path. Render-skip is the default: irGraph reads only the IR
// graph and discards render output, so unless render-time diagnostics are
// explicitly requested (the render-diagnostics dial) the pipeline stops
// after the IR graph is finalised and never loads rendering plugins. The
// returned graph is byte-identical either way — render never mutates it.
func (s *Server) irGraphPipelineOptions() pipeline.Options {
	return pipeline.Options{IncludeTrace: false, SkipRender: !s.renderDiagnostics}
}

func (s *Server) handleIrGraph(ctx context.Context, params irGraphParams) (irGraphResult, *jsonRPCError) {
	releasePath := filepath.Join(s.workspaceRoot, params.Release)

	// 1. Lockfile-absent gate. Blocking — no IR graph produced
	//    until the operator regenerates the lockfile (via the
	//    extension's "Generate Lockfile" toast button, which runs
	//    aggrega pull in a terminal, or by running aggrega pull
	//    manually).
	lockfilePath := lockfile.PathFor(releasePath)
	if _, err := os.Stat(lockfilePath); err != nil {
		if os.IsNotExist(err) {
			return irGraphResult{}, &jsonRPCError{
				Code:    errLockfileAbsent,
				Message: fmt.Sprintf("lockfile %s not found; run `aggrega pull --release %s` to generate", lockfilePath, params.Release),
				Data:    &errorData{File: lockfilePath, Release: params.Release},
			}
		}
		return irGraphResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("lockfile stat %s: %v", lockfilePath, err),
			Data:    newErrorData(lockfilePath, 0, 0),
		}
	}

	// 2. Build the per-request catalog view, seeded with THIS request's pins so
	//    its Tier-2 cache reads verify-on-read against this Release's lockfile
	//    and a concurrent request resolving a different Release's pins can never
	//    perturb this resolution. The pins are loaded once here and the same
	//    view threads through closure compute (step 3), drift context, and the
	//    render pipeline (step 5), so the whole request resolves against one
	//    consistent pin snapshot. A failed/absent lockfile load yields nil pins,
	//    disabling cache trust for the request (the cascade re-establishes at
	//    Tier-3) — never an inherited cross-request pin set.
	//
	//    ORDERING IS LOAD-BEARING: the view's pins are set (via ForRequest)
	//    before any Tier-2 read fires. Verify-on-read depends on the pins being
	//    present before the reads; resolving against an unpinned view silently
	//    degrades to no-pin -> Tier-3 re-fetch, re-introducing drift-masking
	//    (serve would preview re-fetched content a local-cache-drifted build
	//    halts on). Guarded end-to-end by
	//    TestServeIrGraphVerifiesCacheAgainstPinBeforePreview.
	var pins map[string]catalog.Pin
	lf, _, lfErr := lockfile.LoadLockfile(lockfilePath)
	if lfErr != nil || lf == nil {
		lf = nil
	} else {
		pins = catalog.PinsFromLockfile(lf)
	}
	view := s.lazyCatalog.ForRequest(pins)

	// 3. Resolve closure against the request view (factored helper — used here
	//    AND by the drift check at startup). Bundles Release + Manifest +
	//    Aggregate + raw aggregate bytes + computed closure into a single result
	//    struct; step 4 verifies and step 5 feeds the pipeline.
	resolved, envelope := s.resolveReleaseClosure(view, releasePath, lf)
	if envelope != nil {
		return irGraphResult{}, envelope
	}

	// 4. Drift verify. Reuses the closure computed in step 3.
	//    Loads the lockfile fresh (catches the case where the
	//    operator re-pulled between calls). On drift, publishes a
	//    Warning diagnostic on the lockfile path AND fires the
	//    aggrega/publishDiagnostics notification. Non-blocking —
	//    irGraph still produces the IR graph; drift is a
	//    Problems-panel signal, not a halt.
	lf = s.verifyDriftAndPublish(ctx, lockfilePath, resolved.Closure)

	// 5. Build the IR graph through the shared compiler-operations layer. Recipe
	//    + sub-aggregate + mapping-library resolution all come from the request
	//    view (no eager whole-catalog load). Resilient inspection: a resolved
	//    catalog reference momentarily absent from the lazy view (a drifted cache
	//    slot skipped on read) is a drift signal, not an authoring fault — the
	//    graph builds with the entity skipped rather than halting, and drift
	//    surfaces as a Problems-panel diagnostic via the verify step above. The
	//    render-skip options mean irGraph returns the IR graph even when
	//    resolution is incomplete; it does not run the render-time
	//    resolution-completeness halt (that gate is the CLI build's).
	result, err := compilerops.BuildToBuffer(
		ctx,
		compilerops.BuildInputs{
			View:                             view,
			Resolved:                         resolved,
			WorkspaceConfig:                  s.wsConfig,
			WorkspaceRoot:                    s.workspaceRoot,
			Lockfile:                         lf,
			CompilerVersion:                  s.compilerVersion,
			TolerateCatalogViewInconsistency: true,
		},
		s.irGraphPipelineOptions(),
	)
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: fmt.Sprintf("pipeline: %v", err),
		}
	}

	// 6. Render advisories to the Problems panel. The render step's coded
	//    diagnostics — a target that cannot honor an authored field, an
	//    attachment delivering a declared-sensitive datum as a literal — reach
	//    the terminal on a CLI build and would otherwise reach nothing here, so
	//    they are published on the file the daemon attributes semantic findings
	//    to. Non-blocking, like drift: the graph is returned regardless. The
	//    render only runs when the render-diagnostics dial is on, so with it off
	//    this replaces an empty layer with an empty layer.
	s.publishRenderDiagnostics(ctx, resolved.AggregatePath, result.RenderDiagnostics)

	graphBytes, err := json.Marshal(result.IRGraph)
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: fmt.Sprintf("marshal IR graph: %v", err),
		}
	}
	return irGraphResult{Graph: graphBytes}, nil
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
