// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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
//	Tier B — best-effort sub-file: position mapped to the component
//	         or feature attachment site (selector and appliesTo
//	         violations). Future work, bundled alongside daemon
//	         end-position support for diagnostic ranges.
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
func (s *Server) runTier4Validation(file string, doc *yaml.Node) []Diagnostic {
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

	tier4, runErr := pipeline.RunTier4Pipeline(
		pipeline.Inputs{
			Release:           resolved.Release,
			Manifest:          resolved.Manifest,
			AggregateRawBytes: resolved.AggBytes,
			AggregatePath:     aggPath,
			WorkspaceConfig:   s.wsConfig,
		},
		s.lazyCatalog.AsCatalogLookup(),
		recipeCat.SubAggregateLoader(),
	)

	var diags []Diagnostic

	// Resolver-class violations (Step 1) — Tier-A position recovery
	// from the structured ResolveDiagnostics slice. The slice is
	// populated uniformly for both single- and multi-violation cases.
	if tier4 != nil && len(tier4.ResolveDiagnostics) > 0 {
		for _, ve := range tier4.ResolveDiagnostics {
			diags = append(diags, resolverViolationToDiagnostic(ve, doc))
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
		// Analyzer violations (Step 4) — Tier-B class. Position-recovery
		// for selector / appliesTo violations is future work; emit at
		// file:1:1 with a "Closure: " prefix until the Tier-B walker
		// lands.
		for i := range tier4.Analysis.Errors {
			tier4.Analysis.Errors[i].OriginFile = file
			diags = append(diags, Diagnostic{
				Line:     1,
				Column:   1,
				Severity: "error",
				Message:  "Closure: " + tier4.Analysis.Errors[i].Error(),
			})
		}
	}

	return diags
}

// resolverViolationToDiagnostic maps a resolver-class violation to a
// source-positioned diagnostic. The Field locator (e.g.,
// ".spec.parameters.foo" or ".spec.components.bar.extends") is walked
// against the parsed yaml.Node to find the matching key; when found,
// the diagnostic carries the key's Line/Column. When the Field walks
// to a path the document does not contain (orthogonal to the
// authoring shape), or when the Field is empty, the diagnostic falls
// back to file:1:1.
func resolverViolationToDiagnostic(ve *resolver.ResolverError, doc *yaml.Node) Diagnostic {
	line, col := 1, 1
	if pos := walkYAMLToField(doc, ve.Field); pos != nil {
		line = pos.line
		col = pos.col
	}
	return Diagnostic{
		Line:     line,
		Column:   col,
		Severity: "error",
		Message:  ve.Message,
	}
}

// yamlPos is the internal pair returned by walkYAMLToField: 1-based
// line and 1-based column matching yaml.v3's native coordinates.
type yamlPos struct {
	line int
	col  int
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
func walkYAMLToField(doc *yaml.Node, field string) *yamlPos {
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
// segment list. Returns the position of the LAST segment's key node,
// or the deepest reached key node when the walk exhausts the document
// before consuming all segments. Returns nil when the first segment
// doesn't match.
func walkSegments(n *yaml.Node, segments []string) *yamlPos {
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
		// Match found. If this is the last segment, return the key's
		// position. Otherwise descend into the value and look up the
		// remaining segments. If the descent fails to find the next
		// segment, fall back to THIS key's position — better than
		// nothing because it's at least in the right neighborhood.
		if len(rest) == 0 {
			return &yamlPos{line: keyNode.Line, col: keyNode.Column}
		}
		if inner := walkSegments(valNode, rest); inner != nil {
			return inner
		}
		return &yamlPos{line: keyNode.Line, col: keyNode.Column}
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
