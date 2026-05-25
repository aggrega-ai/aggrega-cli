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
)

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
	s.parseCache.Put(params.File, &doc)
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

// invalidateParams is the request shape for `aggrega/invalidate`.
type invalidateParams struct {
	File string `json:"file"`
}

// invalidateResult is the response shape for `aggrega/invalidate`.
type invalidateResult struct {
	Invalidated bool `json:"invalidated"`
}

func (s *Server) dispatchInvalidate(req *jsonRPCRequest) error {
	var params invalidateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result := s.handleInvalidate(params)
	return s.writeResult(req.ID, result)
}

// handleInvalidate drops the parse-cache and diagnostic-cache entries
// for the named file. Reports invalidated: true unconditionally —
// dropping an entry that does not exist is a no-op, not an error.
func (s *Server) handleInvalidate(params invalidateParams) invalidateResult {
	s.parseCache.Drop(params.File)
	delete(s.diagnostics, params.File)
	return invalidateResult{Invalidated: true}
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
	cat, err := s.resolveCatalog()
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
		cat.CatalogLookup(),
		cat.RecipeLookup(),
		cat.SubAggregateLoader(),
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

// Position matches the LSP shape: line + character (UTF-16 code units),
// both 0-based.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range matches the LSP shape.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location matches the LSP shape: workspace-relative URI + position
// range.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
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
	URI      string   `json:"uri"`
	Position Position `json:"position"`
}

// resolveResult is the response shape for `aggrega/resolve`. Location
// is null when no definition site is found.
type resolveResult struct {
	Location *Location `json:"location"`
}

// dispatchResolve is the stub handler for `aggrega/resolve`. Returns
// a structurally-valid empty response; the full implementation lands
// when the resolve-family navigation handlers ship.
func (s *Server) dispatchResolve(req *jsonRPCRequest) error {
	return s.writeResult(req.ID, resolveResult{Location: nil})
}

// findReferencesResult is the response shape for `aggrega/findReferences`.
type findReferencesResult struct {
	Locations []Location `json:"locations"`
}

// dispatchFindReferences is the stub handler for `aggrega/findReferences`.
// Returns an empty location list; the full implementation lands with
// the resolve-family navigation handlers.
func (s *Server) dispatchFindReferences(req *jsonRPCRequest) error {
	return s.writeResult(req.ID, findReferencesResult{Locations: []Location{}})
}

// findImplementationsResult is the response shape for
// `aggrega/findImplementations`.
type findImplementationsResult struct {
	Locations []Location `json:"locations"`
}

// dispatchFindImplementations is the stub handler. Returns an empty
// location list; the full implementation lands with the resolve-family
// navigation handlers.
func (s *Server) dispatchFindImplementations(req *jsonRPCRequest) error {
	return s.writeResult(req.ID, findImplementationsResult{Locations: []Location{}})
}

// hoverResult is the response shape for `aggrega/hover`. Contents is
// null when the cursor position has no hover-eligible target.
type hoverResult struct {
	Contents *MarkupContent `json:"contents"`
}

// dispatchHover is the stub handler. Returns null contents; the full
// implementation lands when the hover + completion handlers ship.
func (s *Server) dispatchHover(req *jsonRPCRequest) error {
	return s.writeResult(req.ID, hoverResult{Contents: nil})
}

// completionResult is the response shape for `aggrega/completion`.
type completionResult struct {
	Items []CompletionItem `json:"items"`
}

// dispatchCompletion is the stub handler. Returns an empty candidate
// list; the full implementation lands when the hover + completion
// handlers ship.
func (s *Server) dispatchCompletion(req *jsonRPCRequest) error {
	return s.writeResult(req.ID, completionResult{Items: []CompletionItem{}})
}
