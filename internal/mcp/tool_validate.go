// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// validateArgs is the validate tool's argument shape.
type validateArgs struct {
	Release        string   `json:"release"`
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	ResponseFormat string   `json:"response_format"`
}

// validateResultPayload is the validate tool's structured payload: whether the
// Release validated clean and, if not, the diagnostics in the order the
// semantic-validation gates produce them.
type validateResultPayload struct {
	Release     string   `json:"release"`
	Valid       bool     `json:"valid"`
	Diagnostics []string `json:"diagnostics"`
}

var validateToolSpec = toolSpec{
	name: "validate",
	description: "Run the compiler's semantic-validation pipeline over a Release and report its diagnostics without " +
		"rendering or writing anything. Pass the Release path as `release` (absolute, or relative to the workspace). " +
		"The tool resolves the Release's Manifest and Aggregate, computes the catalog closure, and runs the closure-scoped " +
		"extends and schema-pillar gates, the resolve/topology/IR pipeline, and the resolution-completeness checks — the " +
		"same core the CLI validates with — then returns whether the Release is valid and, if not, each diagnostic verbatim. " +
		"Use it as the fast inner-loop check while authoring: fix the reported diagnostics and re-run. For a diagnostic you " +
		"do not understand, pass its text to explain-error. Optional `workspace_root` and `catalog_dirs` override workspace " +
		"and catalog discovery; `response_format` chooses detailed (every diagnostic) or concise (count plus the first).",
	inputSchema: `{
  "type": "object",
  "properties": {
    "release": {
      "type": "string",
      "description": "Path to the Release YAML to validate, absolute or relative to the workspace root."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "detailed returns every diagnostic; concise returns the count and the first. Defaults to detailed."
    }
  },
  "required": ["release"]
}`,
	invoke: validateInvoke,
}

func validateInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a validateArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid validate arguments: " + err.Error()}
		}
	}
	if strings.TrimSpace(a.Release) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "validate requires a 'release' path"}
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "release"))
	}
	releasePath := tw.absReleasePath(a.Release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
	})
	if cerr != nil {
		return errorResult(explainToolError(
			"Validation could not resolve the Release closure; fix the resolution failure before semantic validation runs.",
			cerr, closureStageKind(cerr.Stage)))
	}

	vr := compilerops.Validate(ctx, compilerops.ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   resolved.AggregatePath,
		WorkspaceConfig: tw.cfg,
		CompilerVersion: s.serverVersion,
	})

	diags := validateDiagnostics(vr)
	payload := validateResultPayload{Release: releasePath, Valid: len(diags) == 0, Diagnostics: diags}
	text := renderValidateText(payload, parseResponseFormat(a.ResponseFormat))
	return textResult(text, payload)
}

// validateDiagnostics flattens a ValidateResult into the ordered list of
// diagnostic strings, in the order the semantic-validation gates run:
// closure-scoped extends, then the schema-pillar gate, then the resolve/analyze
// pipeline, then the resolution-completeness findings. It surfaces each error
// verbatim — the same text the CLI validation produces — so a consumer reads
// the compiler's own message. The gate order matches the shared layer's
// documented field order, so this is the single mapping every front end shares.
func validateDiagnostics(vr *compilerops.ValidateResult) []string {
	var out []string
	for _, e := range vr.ClosureExtendsErrors {
		out = append(out, "catalog extends: "+e.Error())
	}
	if vr.PillarError != nil {
		out = append(out, vr.PillarError.Error())
	}
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			out = append(out, ve.Error())
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				out = append(out, ae.Error())
			}
		}
	}
	// A non-resolver pre-analyzer phase error carries no per-violation structure;
	// surface it only when the resolver produced no structured diagnostic, so a
	// resolver failure is not double-reported. Cancellation is not a diagnostic.
	if vr.RunErr != nil && !isCancellation(vr.RunErr) &&
		(vr.Tier4 == nil || len(vr.Tier4.ResolveDiagnostics) == 0) {
		out = append(out, vr.RunErr.Error())
	}
	for _, f := range vr.ResolutionFindings {
		out = append(out, f.Error())
	}
	return out
}

// isCancellation reports whether err is a shutdown-drain cancellation, for
// which validation yields no diagnostics.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// renderValidateText renders the human-readable validate result. The concise
// form reports the count and the first diagnostic; the detailed form lists all.
func renderValidateText(p validateResultPayload, format string) string {
	if p.Valid {
		return fmt.Sprintf("Release %s validated clean: no diagnostics.", p.Release)
	}
	if format == responseFormatConcise {
		return fmt.Sprintf("Release %s has %d validation diagnostic(s). First: %s",
			p.Release, len(p.Diagnostics), p.Diagnostics[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Release %s has %d validation diagnostic(s):\n", p.Release, len(p.Diagnostics))
	for i, d := range p.Diagnostics {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, d)
	}
	return strings.TrimRight(b.String(), "\n")
}

// closureStageKind maps a closure-resolution stage to the entity kind whose
// documentation a failure at that stage points at.
func closureStageKind(stage compilerops.ClosureStage) string {
	switch stage {
	case compilerops.ClosureStageResolveManifest, compilerops.ClosureStageComposeManifest:
		return "manifest"
	case compilerops.ClosureStageLoadAggregate:
		return "aggregate"
	default:
		return "release"
	}
}
