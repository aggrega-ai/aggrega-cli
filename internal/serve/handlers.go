// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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

// initializeResult is the response shape for `aggrega/initialize`.
type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
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
func (s *Server) dispatchInitialize(req *jsonRPCRequest) error {
	var params initializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	selected := selectPositionEncoding(params.Capabilities)
	s.positionEncoding = selected
	return s.writeResult(req.ID, initializeResult{
		Capabilities: serverCapabilities{
			PositionEncoding: string(selected),
		},
	})
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
func (s *Server) dispatchValidate(req *jsonRPCRequest) error {
	var params validateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleValidate(params)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
	}
	return s.writeResult(req.ID, result)
}

// handleValidate parses the supplied content via yaml.v3 and populates
// the daemon's parse cache + diagnostic cache for the file. The
// validation runs at Tier 1 (parse-only) when no active Release is
// selected; Tier 4 (full resolver + semantic checks via the pipeline
// path) when an active Release is selected. The activeRelease branch
// is wired but unreachable in the scaffold — the multi-Manifest
// disambiguation cascade populates activeRelease in a successor
// phase.
func (s *Server) handleValidate(params validateParams) (validateResult, *jsonRPCError) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(params.Content), &doc); err != nil {
		diag := yamlErrorToDiagnostic(err)
		s.diagnostics[params.File] = []Diagnostic{diag}
		s.parseCache.Drop(params.File)
		return validateResult{Diagnostics: []Diagnostic{diag}}, nil
	}
	s.parseCache.Put(params.File, &doc, []byte(params.Content))
	var diags []Diagnostic
	if s.activeRelease != nil {
		// Tier 4: full resolver + semantic checks via the pipeline path.
		// Wired when the active-Release cascade populates activeRelease;
		// at this scaffold, activeRelease is always nil and this branch
		// is unreachable.
		diags = s.runTier4Validation(params.File, &doc)
	}
	s.diagnostics[params.File] = diags
	return validateResult{Diagnostics: diags}, nil
}

// runTier4Validation is a placeholder for the active-Release Tier-4
// path. Wired when the active-Release cascade ships; returns empty
// diagnostics in the scaffold because activeRelease is always nil.
func (s *Server) runTier4Validation(file string, doc *yaml.Node) []Diagnostic {
	_ = file
	_ = doc
	return nil
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

func (s *Server) dispatchDiagnostics(req *jsonRPCRequest) error {
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

func (s *Server) dispatchIrGraph(req *jsonRPCRequest) error {
	var params irGraphParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result, errEnvelope := s.handleIrGraph(params)
	if errEnvelope != nil {
		return s.writeError(req.ID, errEnvelope.Code, errEnvelope.Message)
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
func (s *Server) handleIrGraph(params irGraphParams) (irGraphResult, *jsonRPCError) {
	releasePath := filepath.Join(s.workspaceRoot, params.Release)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load release %s: %v", params.Release, err),
		}
	}
	releaseDir := filepath.Dir(releasePath)
	manifestPath := filepath.Join(releaseDir, rel.Spec.Manifest+".yaml")
	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("load manifest %s: %v", manifestPath, err),
		}
	}
	aggPath := filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: fmt.Sprintf("read aggregate %s: %v", aggPath, err),
		}
	}
	recipeCat, err := s.resolveRecipeCatalog()
	if err != nil {
		return irGraphResult{}, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: err.Error(),
		}
	}

	result, err := pipeline.RunPipeline(
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     aggPath,
			WorkspaceConfig:   s.wsConfig,
		},
		s.lazyCatalog.AsCatalogLookup(),
		recipeCat.RecipeLookup(),
		recipeCat.SubAggregateLoader(),
		daemonEngineFactory,
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
func (s *Server) dispatchResolve(req *jsonRPCRequest) error {
	var params resolveParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	loc, errEnvelope := s.resolveDefinitionSite(params.URI, params.Position)
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
func (s *Server) dispatchFindReferences(req *jsonRPCRequest) error {
	var params findReferencesParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	locs, errEnvelope := s.findReferencesAcrossWorkspace(params.URI, params.Position)
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
func (s *Server) dispatchFindImplementations(req *jsonRPCRequest) error {
	var params findImplementationsParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	locs, errEnvelope := s.findImplementationsAcrossWorkspace(params.URI, params.Position)
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
func (s *Server) dispatchHover(req *jsonRPCRequest) error {
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
func (s *Server) dispatchCompletion(req *jsonRPCRequest) error {
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
