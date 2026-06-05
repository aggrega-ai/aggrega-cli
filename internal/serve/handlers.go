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
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// initializeParams is the request shape for `aggrega/initialize`. The
// daemon reads positionEncodings off the client's capabilities; other
// LSP initialize fields are accepted but ignored at this WI.
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
func (s *Server) dispatchInitialize(_ context.Context, req *jsonRPCRequest) error {
	var params initializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	selected := selectPositionEncoding(params.Capabilities)
	s.positionEncoding = selected
	if err := s.writeResult(req.ID, initializeResult{
		Capabilities: serverCapabilities{
			PositionEncoding: string(selected),
		},
		WorkspaceRoot:   s.workspaceRoot,
		ConfigSource:    s.configSource,
		WorkspaceMarker: s.workspaceMarker.WireName(),
	}); err != nil {
		return err
	}

	// Post-response flush of pre-populated diagnostics. The
	// at-startup drift check populates s.diagnostics before
	// dispatchLoop runs; the LSP-canonical pattern is to push those
	// diagnostics AFTER the initialize response so the client's
	// handshake completes before unsolicited notifications start
	// arriving. Sort keys for deterministic ordering across daemon
	// runs (Go map iteration is randomized).
	keys := make([]string, 0, len(s.diagnostics))
	for k := range s.diagnostics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, file := range keys {
		if err := s.publishDiagnostics(file); err != nil {
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
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleValidate(ctx, params)
	if ctx.Err() != nil {
		// Cancelled mid-flight (shutdown). Drop the response; the
		// dispatcher exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(req.ID, errEnvelope)
	}
	return s.writeResult(req.ID, result)
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
func (s *Server) handleValidate(ctx context.Context, params validateParams) (validateResult, *jsonRPCError) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(params.Content), &doc); err != nil {
		diag := yamlErrorToDiagnostic(err)
		s.diagnostics[params.File] = []Diagnostic{diag}
		s.parseCache.Drop(params.File)
		return validateResult{Diagnostics: []Diagnostic{diag}}, nil
	}
	s.parseCache.Put(params.File, &doc, []byte(params.Content))
	if s.activeRelease == nil {
		// Two sub-cases:
		//   (a) cascade exhausted but Release files exist in the workspace
		//       → operator's authoring needs a deliberate choice; emit 1002
		//       so the extension surfaces the remediation toast.
		//   (b) no Release files in the workspace at all → catalog-only or
		//       scaffold case; Tier-1 diagnostics suffice. No spurious
		//       toast.
		if s.cascadeAttempt.ReleaseCandidatesFound > 0 {
			return validateResult{}, s.emitReleaseNotResolvedEnvelope(params.File)
		}
		s.diagnostics[params.File] = nil
		return validateResult{Diagnostics: nil}, nil
	}
	// Tier 4: structured semantic violations via RunTier4Pipeline.
	diags := s.runTier4Validation(ctx, params.File, []byte(params.Content), &doc)
	if ctx.Err() != nil {
		// Cancelled mid-pipeline. Leave the prior diagnostic cache
		// entry intact and return without overwriting it.
		return validateResult{Diagnostics: diags}, nil
	}
	s.diagnostics[params.File] = diags
	return validateResult{Diagnostics: diags}, nil
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

func (s *Server) dispatchDiagnostics(_ context.Context, req *jsonRPCRequest) error {
	var params diagnosticsParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleDiagnostics(params)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(req.ID, result)
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
	return diagnosticsResult{Diagnostics: s.diagnostics[params.File]}, nil
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
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleIrGraph(ctx, params)
	if ctx.Err() != nil {
		// Cancelled mid-pipeline (shutdown). Drop the response; the
		// dispatcher exits via the drain timer.
		return nil
	}
	if errEnvelope != nil {
		return s.writeErrorEnvelope(req.ID, errEnvelope)
	}
	return s.writeResult(req.ID, result)
}

// daemonEngineFactory dispatches recipe.spec.provider to a fresh-per-
// call renderer.Engine. Mirrors the closure constructed inside
// cmd/aggrega/build.go: dispatchBuild — three concrete provider
// engines with the same fallback semantics.
func daemonEngineFactory(providerName string) (renderer.Engine, error) {
	switch providerName {
	case "helm", "":
		return &renderHelm.HelmEngine{}, nil
	case "helm-chart":
		return &renderHelmChart.HelmEngine{}, nil
	case "compose":
		return &renderCompose.ComposeEngine{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", renderer.ErrUnknownProvider, providerName)
	}
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
				Data:    newErrorData(lockfilePath, 0, 0),
			}
		}
		return irGraphResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("lockfile stat %s: %v", lockfilePath, err),
			Data:    newErrorData(lockfilePath, 0, 0),
		}
	}

	// 2. Resolve closure (factored helper — used here AND by the
	//    drift check at startup). Bundles Release + Manifest +
	//    Aggregate + raw aggregate bytes + computed closure into a
	//    single result struct; step 3 verifies and step 5 feeds the
	//    pipeline.
	resolved, envelope := s.resolveReleaseClosure(releasePath)
	if envelope != nil {
		return irGraphResult{}, envelope
	}

	// 3. Drift verify. Reuses the closure computed in step 2.
	//    Loads the lockfile fresh (catches the case where the
	//    operator re-pulled between calls). On drift, publishes a
	//    Warning diagnostic on the lockfile path AND fires the
	//    aggrega/publishDiagnostics notification. Non-blocking —
	//    irGraph still produces the IR graph; drift is a
	//    Problems-panel signal, not a halt.
	lf := s.verifyDriftAndPublish(lockfilePath, resolved.Closure)

	// 3b. Thread the freshly-loaded lockfile pins into the lazy lookup so its
	//     Tier-2 cache reads in the render pipeline (step 5) verify-on-read: a
	//     drifted cache slot is skipped (not previewed as false-green content),
	//     a genuine miss re-establishes at Tier-3. The per-request drift notice
	//     is the closure-level verify just run (step 3); the skip does not
	//     double-report it. lf is nil only when the lockfile failed to load
	//     (drift is non-blocking and still returns it); nil pins disables cache
	//     trust for the request.
	//
	//     ORDERING IS LOAD-BEARING: this SetPins MUST precede the RunPipeline
	//     reads below. Verify-on-read depends on the pins being set before the
	//     Tier-2 reads fire; moving SetPins after them (or removing it) silently
	//     degrades to no-pin -> Tier-3 re-fetch, which re-introduces drift-masking
	//     (serve would preview re-fetched content that a local-cache-drifted build
	//     halts on). The reorder is guarded by an end-to-end test that drives this
	//     handler against a drifted cache slot and asserts Tier-3 is never reached
	//     (TestServeIrGraphVerifiesCacheAgainstPinBeforePreview).
	var pins map[string]string
	if lf != nil {
		pins = lf.PinsByURI()
	}
	s.lazyCatalog.SetPins(pins)

	// 4. Recipe + sub-aggregate + mapping-library resolution all come from the
	//    lazy backing now (no eager whole-catalog load): the tolerantly-enumerated
	//    recipe shell, on par with the Component/Feature cascade already feeding
	//    AsCatalogLookup. A broken out-of-closure entity never fails this build.
	aggPath := filepath.Join(filepath.Dir(releasePath), resolved.Release.Spec.Aggregate+".yaml")
	result, err := pipeline.RunPipeline(
		ctx,
		pipeline.Inputs{
			Release:           resolved.Release,
			Manifest:          resolved.Manifest,
			AggregateRawBytes: resolved.AggBytes,
			AggregatePath:     aggPath,
			WorkspaceConfig:   s.wsConfig,
			Lockfile:          lf,
			WorkspaceRoot:     s.workspaceRoot,
			MappingLibraries:  s.lazyCatalog.MappingLibraries(),
		},
		s.lazyCatalog.AsCatalogLookup(),
		s.lazyCatalog.RecipeLookupWithManifest(resolved.Manifest),
		s.lazyCatalog.SubAggregateLoader(),
		daemonEngineFactory,
		// irGraph is resilient inspection — it returns the IR graph even when
		// resolution is incomplete, so it does not run the render-time
		// resolution-completeness halt (that gate is the CLI build's).
		pipeline.Options{IncludeTrace: false},
	)
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    jsonRPCInternalError,
			Message: fmt.Sprintf("pipeline: %v", err),
		}
	}

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
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	loc, errEnvelope := s.resolveDefinitionSite(ctx, params.URI, params.Position)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(req.ID, resolveResult{Location: loc})
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
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	locs, errEnvelope := s.findReferencesAcrossWorkspace(ctx, params.URI, params.Position)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	if locs == nil {
		locs = []yamlpos.Location{}
	}
	return s.writeResult(req.ID, findReferencesResult{Locations: locs})
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
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	locs, errEnvelope := s.findImplementationsAcrossWorkspace(ctx, params.URI, params.Position)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	if locs == nil {
		locs = []yamlpos.Location{}
	}
	return s.writeResult(req.ID, findImplementationsResult{Locations: locs})
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
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	contents, errEnvelope := s.handleHover(params)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(req.ID, hoverResult{Contents: contents})
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
// triggerCharacter is consumed at this WI; other fields are accepted
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
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	items, errEnvelope := s.handleCompletion(params)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	if items == nil {
		items = []CompletionItem{}
	}
	return s.writeResult(req.ID, completionResult{Items: items})
}
