// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
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
//	         with the "Closure: " prefix.
//
// When the request file is not the active Release's Aggregate (e.g.,
// the operator validates a sibling catalog Component while the
// Release points at a different Aggregate), the Tier-4 pipeline is
// not relevant to the requested file and the function returns an
// empty slice — the caller's existing Tier-1 diagnostics for the
// file already populated stay in place.
func (s *Server) runTier4Validation(ctx context.Context, file string, content []byte, doc *yaml.Node) []Diagnostic {
	resolved, envelope := s.resolveReleaseClosure(s.activeReleasePath)
	if envelope != nil {
		// Closure-level failure (catalog load, Release/Manifest load,
		// Aggregate read) — Tier-D fall-back at file:1:1.
		return []Diagnostic{{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  "Closure: " + envelope.Message,
		}}
	}

	// The Tier-4 branch is meaningful only when the request file IS
	// the active Aggregate. Validating a different file with the
	// active Release context produces no semantic diagnostics for
	// THAT file from this pipeline.
	releaseDir := filepath.Dir(s.activeReleasePath)
	aggPath := filepath.Join(releaseDir, resolved.Release.Spec.Aggregate+".yaml")
	if file != aggPath {
		return nil
	}

	recipeCat, err := s.resolveRecipeCatalog()
	if err != nil {
		return []Diagnostic{{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  "Closure: catalog load: " + err.Error(),
		}}
	}

	// Both the root aggregate's path (passed via Inputs.AggregatePath)
	// and the validated `file` are rendered in the same workspace-
	// relative coordinate system the catalog accessors produce for
	// sub-aggregate paths, so per-node SourceFile and the per-file
	// comparison sit in one form. catalog.RelativeToWorkspace is the
	// single source of truth for the conversion; both sides call it.
	relAggPath := catalog.RelativeToWorkspace(s.workspaceRoot, aggPath)
	relFile := catalog.RelativeToWorkspace(s.workspaceRoot, file)

	tier4, runErr := pipeline.RunTier4Pipeline(
		ctx,
		pipeline.Inputs{
			Release:           resolved.Release,
			Manifest:          resolved.Manifest,
			AggregateRawBytes: resolved.AggBytes,
			AggregatePath:     relAggPath,
			WorkspaceConfig:   s.wsConfig,
		},
		s.lazyCatalog.AsCatalogLookup(),
		recipeCat.SubAggregateLoader(),
	)

	// Cancellation (shutdown drain) surfaces as a context error from the
	// pipeline. Return no diagnostics rather than a spurious closure-level
	// entry; the caller drops the response.
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return nil
	}

	// Source-position recovery walks the parsed document and computes
	// scalar end positions in the validated file's coordinate space.
	idx := yamlpos.BuildLineIndexWithEncoding(content, s.positionEncoding)

	var diags []Diagnostic

	// Resolver-class violations (Step 1) — Tier-A position recovery
	// from the structured ResolveDiagnostics slice. The slice is
	// populated uniformly for both single- and multi-violation cases.
	if tier4 != nil && len(tier4.ResolveDiagnostics) > 0 {
		for _, ve := range tier4.ResolveDiagnostics {
			diags = append(diags, resolverViolationToDiagnostic(ve, doc, idx))
		}
	} else if runErr != nil {
		// Non-resolver pre-analyzer phase error (topology / IR / guards
		// without per-violation structure). Tier-D fall-back at file:1:1.
		diags = append(diags, Diagnostic{
			Line:     1,
			Column:   1,
			Severity: "error",
			Message:  tier4PhaseErrorMessage(runErr),
		})
	}

	if tier4 != nil && tier4.Analysis != nil {
		diags = append(diags, analyzerErrorsToDiagnostics(tier4.Analysis.Errors, relFile, doc, idx)...)
	}

	return diags
}

// analyzerErrorsToDiagnostics maps the analyzer's structured
// violation slice to Diagnostics for the requested file. Selector and
// appliesTo violations (Step 4) carry a Field locator naming the
// authoring site; this helper walks it to a sub-file position and
// spans the offending key (Tier-B fidelity). When the Field is empty
// or walks to a path the document does not contain, the diagnostic
// falls back to file:1:1 with the "Closure: " prefix.
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
		if node := walkYAMLToField(doc, errs[i].Field); node != nil {
			line, col, endLine, endCol = diagRangeForNode(idx, node)
		}
		out = append(out, Diagnostic{
			Line:      line,
			Column:    col,
			EndLine:   endLine,
			EndColumn: endCol,
			Severity:  "error",
			Message:   "Closure: " + errs[i].Error(),
		})
	}
	return out
}

// resolverViolationToDiagnostic maps a resolver-class violation to a
// source-positioned diagnostic. The Field locator (e.g.,
// ".spec.parameters.foo" or ".spec.components.bar.extends") is walked
// against the parsed yaml.Node to find the matching key; when found,
// the diagnostic carries the key's start and end position so the
// editor renders a squiggle spanning the whole token. When the Field
// walks to a path the document does not contain (orthogonal to the
// authoring shape), or when the Field is empty, the diagnostic falls
// back to file:1:1.
func resolverViolationToDiagnostic(ve *resolver.ResolverError, doc *yaml.Node, idx yamlpos.LineIndex) Diagnostic {
	line, col := 1, 1
	var endLine, endCol int
	if node := walkYAMLToField(doc, ve.Field); node != nil {
		line, col, endLine, endCol = diagRangeForNode(idx, node)
	}
	return Diagnostic{
		Line:      line,
		Column:    col,
		EndLine:   endLine,
		EndColumn: endCol,
		Severity:  "error",
		Message:   ve.Message,
	}
}

// diagRangeForNode computes the 1-based start and end position of a
// matched key node in the validated file's coordinate space, using the
// same LineIndex/encoding the rest of the daemon's position machinery
// uses. The end position spans the key token via ScalarEnd. A
// multi-line value (block scalar) has no single-line span, so the end
// is left zero and the caller renders the diagnostic point-style.
func diagRangeForNode(idx yamlpos.LineIndex, node *yaml.Node) (line, col, endLine, endCol int) {
	start := idx.NodeToPosition(node)
	line, col = start.Line+1, start.Character+1
	if strings.Contains(node.Value, "\n") {
		return line, col, 0, 0
	}
	end := idx.ScalarEnd(node)
	return line, col, end.Line + 1, end.Character + 1
}

// walkYAMLToField walks the parsed yaml.Node to find the key node at
// the supplied JSON-path-style Field. Returns nil when the path is
// empty or does not match any key in the document.
//
// Field syntax (mirroring resolver/ResolverError.Field):
//
//   - Leading "." marks an absolute path from the document root.
//   - Segments are dot-separated.
//   - Each segment is treated as a literal key in a mapping node.
//
// Sequence indices and special-character keys are not yet supported;
// when encountered, the walk falls back to nil and the caller emits
// the diagnostic at file:1:1. Future work extends the walker to
// handle "[<index>]" syntax and quoted keys when authoring shapes
// surface them.
func walkYAMLToField(doc *yaml.Node, field string) *yaml.Node {
	if field == "" {
		return nil
	}
	// Strip leading "." from the absolute path.
	path := strings.TrimPrefix(field, ".")
	if path == "" {
		return nil
	}
	segments := strings.Split(path, ".")

	// Unwrap document node to get to the root mapping.
	root := doc
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}

	return walkSegments(root, segments)
}

// walkSegments recursively walks the YAML tree following the supplied
// segment list. Returns the LAST segment's key node, or the deepest
// reached key node when the walk exhausts the document before consuming
// all segments. Returns nil when the first segment doesn't match.
func walkSegments(n *yaml.Node, segments []string) *yaml.Node {
	if n == nil || len(segments) == 0 {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	target := segments[0]
	rest := segments[1:]
	for i := 0; i+1 < len(n.Content); i += 2 {
		keyNode := n.Content[i]
		valNode := n.Content[i+1]
		if keyNode.Value != target {
			continue
		}
		// Match found. If this is the last segment, return the key node.
		// Otherwise descend into the value and look up the remaining
		// segments. If the descent fails to find the next segment, fall
		// back to THIS key node — better than nothing because it's at
		// least in the right neighborhood.
		if len(rest) == 0 {
			return keyNode
		}
		if inner := walkSegments(valNode, rest); inner != nil {
			return inner
		}
		return keyNode
	}
	return nil
}

// tier4PhaseErrorMessage formats a non-resolver pre-analyzer phase
// error into the Tier-D message shape. RunTier4Pipeline wraps phase
// errors with "Step N (phase): ..." framing; strip the framing for
// brevity and prefix with "Closure: " so the message reads naturally
// in the IDE.
func tier4PhaseErrorMessage(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, "): "); idx > 0 && strings.HasPrefix(msg, "Step ") {
		msg = msg[idx+3:]
	}
	return fmt.Sprintf("Closure: %s", msg)
}
