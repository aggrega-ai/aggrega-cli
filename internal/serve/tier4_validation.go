// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// runTier4Validation runs Steps 1-4 of the compiler pipeline against
// the daemon's active Release context and maps per-phase violations
// into source-positioned diagnostics for the requested file.
//
// Position-mapping fidelity:
//
//	Tier A — clean sub-file: scalar position recovered from the
//	         parsed yaml.Node for resolver-class violations whose
//	         Field locator names a path the daemon can walk
//	         (parameter type mismatch, missing required parameter,
//	         dangling extends reference). Uses the parsed yaml.Node
//	         to find the matching key; returns its Line/Column.
//	Tier B — best-effort sub-file: selector and appliesTo violations
//	         carry a Field locator naming the uses-socket or feature-
//	         instance key; the diagnostic spans that key. Falls back to
//	         file:1:1 when the locator names a path the walker cannot
//	         reach (subsystem-nested components, sequence-indexed keys).
//	Tier C — file-level only: renderer / file-context errors;
//	         fall back to {1, 1} with a "<filename>: " prefix.
//	         Distinguished from Tier D in a future iteration.
//	Tier D — closure-level: catalog load failures, lockfile drift,
//	         cross-file IR assembly errors; fall back to {1, 1}
//	         with the "Dependencies: " prefix.
//
// When the request file is not the active Release's Aggregate (e.g.,
// the operator validates a sibling catalog Component while the
// Release points at a different Aggregate), the Tier-4 pipeline is
// not relevant to the requested file and the function returns an
// empty slice — the caller's existing Tier-1 diagnostics for the
// file already populated stay in place.
func (s *Server) runTier4Validation(ctx context.Context, file string, content []byte, doc *yaml.Node) []Diagnostic {
	// Build the per-request catalog view, seeded with the active Release's
	// lockfile pins, so every Tier-4 resolution read below verifies-on-read
	// against THIS Release's pins and a concurrent request resolving a different
	// Release's pins can never perturb this validation. The pins are loaded once
	// here and the same view threads through closure compute, closure-scoped
	// extends + Pass-2, the Tier-4 pipeline, and resolution findings, so the
	// whole validation sees one consistent pin snapshot. A failed/absent/invalid
	// lockfile yields nil pins (no-pin -> Tier-3 re-establish), never an
	// inherited cross-request pin set.
	//
	// This is a QUIET load — pins only, no drift verify and no diagnostic
	// publication. Lockfile drift is surfaced by the at-startup check and the
	// irGraph handler, and those notifications persist in the client; re-publishing
	// here (validate fires per edit) would re-send an un-deduplicated drift notice
	// at editing cadence without adding a needed signal.
	//
	// ORDERING IS LOAD-BEARING: the view's pins are set (via ForRequest) before
	// any Tier-2 read fires. Resolving against an unpinned view degrades to a
	// no-pin Tier-3 re-fetch on a drifted cache, masking the drift a
	// verify-on-read skip would surface — guarded by an end-to-end test
	// (TestServeValidateVerifiesCacheAgainstPinBeforeTier4).
	var pins map[string]catalog.Pin
	if lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(s.activeReleasePath)); err == nil && lf != nil {
		pins = catalog.PinsFromLockfile(lf)
	}
	view := s.lazyCatalog.ForRequest(pins)

	// Quiet load — pins only, no drift verify — so the closure axes are not
	// reconstructed here (nil lockfile). Drift is surfaced by the at-startup check
	// and the irGraph handler, which reconstruct and verify.
	resolved, envelope := s.resolveReleaseClosure(view, s.activeReleasePath, nil)
	if envelope != nil {
		// Closure-level failure (catalog load, Release/Manifest load,
		// Aggregate read) — Tier-D fall-back at file:1:1. The code comes off
		// the envelope's cause rather than off its Message: the message is
		// rendered text, and the condition the failure names is only reachable
		// through the failure itself.
		return []Diagnostic{{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  "Dependencies: " + envelope.Message,
			Code:     diagnosticCode(envelope.cause),
		}}
	}

	// The Tier-4 branch is meaningful only when the request file IS
	// the active Aggregate. Validating a different file with the
	// active Release context produces no semantic diagnostics for
	// THAT file from this pipeline.
	aggPath := resolved.AggregatePath
	if file != aggPath {
		return nil
	}

	// Both the root aggregate's path (passed via Inputs.AggregatePath)
	// and the validated `file` are rendered in the same workspace-
	// relative coordinate system the catalog accessors produce for
	// sub-aggregate paths, so per-node SourceFile and the per-file
	// comparison sit in one form. catalog.RelativeToWorkspace is the
	// single source of truth for the conversion; both sides call it.
	relAggPath := catalog.RelativeToWorkspace(s.workspaceRoot, aggPath)
	relFile := catalog.RelativeToWorkspace(s.workspaceRoot, file)

	// Run the semantic-validation core through the shared compiler-operations
	// layer (closure-scoped extends gate, closure-scoped schema-pillar gate,
	// Steps 1-4 pipeline, resolution-completeness findings). The gates read
	// through the pinned request view so every resolution verifies-on-read
	// against this Release's pins. The daemon owns the diagnostic MAPPING below;
	// the shared layer returns the raw outcome in gate order.
	vr := compilerops.Validate(ctx, compilerops.ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   relAggPath,
		WorkspaceConfig: s.wsConfig,
		CompilerVersion: s.compilerVersion,
	})

	// Closure-scoped extends — early-warning extends gate. A dangling parent (or
	// a cycle / malformed chain) on an in-closure Component/Feature surfaces as a
	// Tier-D closure-level diagnostic; an out-of-closure broken extends is never
	// resolved.
	if len(vr.ClosureExtendsErrors) > 0 {
		return []Diagnostic{{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  fmt.Sprintf("Dependencies: catalog extends: %v", vr.ClosureExtendsErrors[0]),
			Code:     diagnosticCode(vr.ClosureExtendsErrors[0]),
		}}
	}

	// Closure-scoped Pass-2 — early-warning schema-pillar gate. An in-closure
	// schema-invalid Component/Feature (or a $ref to a skipped/missing library)
	// surfaces as a Tier-D closure-level diagnostic, with the skip context
	// explaining a missing library. build remains the authoritative validator.
	if vr.PillarError != nil {
		return []Diagnostic{{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  "Dependencies: " + s.skipContext(view, vr.PillarError.Error()),
			Code:     diagnosticCode(vr.PillarError),
		}}
	}

	// Cancellation (shutdown drain) surfaces as a context error from the
	// pipeline. Return no diagnostics rather than a spurious closure-level
	// entry; the caller drops the response.
	if errors.Is(vr.RunErr, context.Canceled) || errors.Is(vr.RunErr, context.DeadlineExceeded) {
		return nil
	}

	// Source-position recovery walks the parsed document and computes
	// scalar end positions in the validated file's coordinate space.
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding())

	var diags []Diagnostic
	tier4 := vr.Tier4

	// Resolver-class violations (Step 1) — Tier-A position recovery
	// from the structured ResolveDiagnostics slice. The slice is
	// populated uniformly for both single- and multi-violation cases.
	if tier4 != nil && len(tier4.ResolveDiagnostics) > 0 {
		for _, ve := range tier4.ResolveDiagnostics {
			diags = append(diags, resolverFindingToDiagnostic(ve, "error", doc, idx))
		}
	} else if vr.RunErr != nil {
		// Non-resolver pre-analyzer phase error (topology / IR / guards
		// without per-violation structure). Tier-D fall-back at file:1:1.
		diags = append(diags, Diagnostic{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  tier4PhaseErrorMessage(vr.RunErr),
			Code:     diagnosticCode(vr.RunErr),
		})
	}

	// Advisory findings — exit-code-neutral, so they publish at warning
	// severity and never contribute to the verdict. They ride the same
	// position-recovery path as the halting violations above, so the editor
	// squiggles the offending authoring key rather than the head of the file.
	if tier4 != nil {
		for _, a := range tier4.LintAdvisories {
			diags = append(diags, resolverFindingToDiagnostic(a, "warning", doc, idx))
		}
	}

	if tier4 != nil && tier4.Analysis != nil {
		diags = append(diags, analyzerErrorsToDiagnostics(tier4.Analysis.Errors, relFile, doc, idx)...)
	}

	// Resolution-completeness diagnostics — the de-fallback regression guard.
	// Resolution is mapping-only; an unmapped instance no longer silently
	// resolves by spec.implements. serve keeps its resilient no-halt posture
	// but surfaces each unmapped instance as a diagnostic carrying the
	// implementing-recipe pin candidates, rather than a silent miss. This runs
	// the SAME shared producer the CLI build joins into a halt, over the
	// complete enumerated recipe set, so the editor's candidate suggestions
	// match the build's exactly.
	for _, ferr := range vr.ResolutionFindings {
		diags = append(diags, Diagnostic{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  "Dependencies: " + ferr.Error(),
			Code:     diagnosticCode(ferr),
		})
	}

	// Input-kind capability errors — computed after the pipeline, this class
	// keeps serve's verdict inclusive of an input-kind failure so the daemon
	// reaches the same verdict as the other front ends.
	for _, e := range vr.InputKindErrors {
		diags = append(diags, Diagnostic{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  "Dependencies: input-kind: " + e.Error(),
			Code:     diagnosticCode(e),
		})
	}

	return diags
}

// analyzerErrorsToDiagnostics maps the analyzer's structured
// violation slice to Diagnostics for the requested file. Selector and
// appliesTo violations (Step 4) carry a Field locator naming the
// authoring site; this helper walks it to a sub-file position and
// spans the offending key (Tier-B fidelity). When the Field is empty
// or walks to a path the document does not contain, the diagnostic
// falls back to file:1:1 with the "Dependencies: " prefix.
//
// Cross-file filter: each AnalysisError carries an OriginFile
// field naming the source file where the violating authoring
// lives. The catalog accessors that supplied those paths render
// them in whatever form the catalog was constructed with — workspace-
// relative when Load/LoadAll received a non-empty workspace root (the
// production daemon); absolute when no workspace root was wired.
// Callers convert the validated file to the same form before
// invocation; this helper compares string-for-string and drops
// entries whose OriginFile points to a different file from the
// validated one. Per-file diagnostic collection means cross-file
// errors surface when the user opens the offending file. The Field
// walk therefore runs against the requested file's document, which is
// the origin file for every entry that survives the filter.
//
// Mutates the input slice in place to backfill OriginFile when
// empty. Callers that want to preserve the input shape should
// copy before invoking.
func analyzerErrorsToDiagnostics(errs []analyzer.AnalysisError, file string, doc *yaml.Node, idx yamlpos.LineIndex) []Diagnostic {
	var out []Diagnostic
	for i := range errs {
		if errs[i].OriginFile == "" {
			errs[i].OriginFile = file
		}
		if errs[i].OriginFile != file {
			continue
		}
		line, col := 1, 1
		var endLine, endCol int
		if span, ok := yamlpos.FieldRange(doc, idx, errs[i].Field); ok {
			line, col, endLine, endCol = span.Line, span.Column, span.EndLine, span.EndColumn
		}
		out = append(out, Diagnostic{
			Line:      line,
			Column:    col,
			EndLine:   endLine,
			EndColumn: endCol,
			Severity:  "error",
			// The code comes off Sentinel rather than off the entry: an
			// AnalysisError reports its own Message and does not unwrap to the
			// condition it names, so a chain walk over the entry itself finds
			// nothing to report.
			Message: "Dependencies: " + errs[i].Error(),
			Code:    diagnosticCode(errs[i].Sentinel),
		})
	}
	return out
}

// resolverFindingToDiagnostic maps a resolver-class finding to a
// source-positioned diagnostic at the supplied severity. The Field locator
// (e.g., ".spec.parameters.foo" or ".spec.components.bar.extends") is walked
// against the parsed yaml.Node to find the matching key; when found,
// the diagnostic carries the key's start and end position so the
// editor renders a squiggle spanning the whole token. When the Field
// walks to a path the document does not contain (orthogonal to the
// authoring shape), or when the Field is empty, the diagnostic falls
// back to file:1:1.
//
// Severity is the caller's, not the finding's: halting violations and
// exit-code-neutral advisories share this one position-recovery path, so an
// advisory lands on its authoring site with the same fidelity a violation gets
// rather than acquiring a second walker that drifts from this one.
func resolverFindingToDiagnostic(ve *resolver.ResolverError, severity string, doc *yaml.Node, idx yamlpos.LineIndex) Diagnostic {
	line, col := 1, 1
	var endLine, endCol int
	if span, ok := yamlpos.FieldRange(doc, idx, ve.Field); ok {
		line, col, endLine, endCol = span.Line, span.Column, span.EndLine, span.EndColumn
	}
	return Diagnostic{
		Line:      line,
		Column:    col,
		EndLine:   endLine,
		EndColumn: endCol,
		Severity:  severity,
		Message:   ve.Message,
		Code:      diagnosticCode(ve),
	}
}

// The field-path-to-position walk this file used to carry lives at
// yamlpos.FieldNode / yamlpos.FieldRange. It was moved rather than shared
// from here because the envelope layer needs the same walk and must not
// depend on the daemon: with one implementation, a violation reported to an
// editor and the same violation reported on a terminal cannot point at
// different keys. Do not reintroduce a local walker — the guard test in
// yamlpos pins the caller set for exactly that reason.

// tier4PhaseErrorMessage formats a non-resolver pre-analyzer phase
// error into the Tier-D message shape. RunTier4Pipeline wraps phase
// errors with "Step N (phase): ..." framing; strip the framing for
// brevity and prefix with "Dependencies: " so the message reads naturally
// in the IDE.
func tier4PhaseErrorMessage(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, "): "); idx > 0 && strings.HasPrefix(msg, "Step ") {
		msg = msg[idx+3:]
	}
	return fmt.Sprintf("Dependencies: %s", msg)
}
