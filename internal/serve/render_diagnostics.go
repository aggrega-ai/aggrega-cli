// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// publishRenderDiagnostics replaces the render diagnostic layer with the
// advisories this render raised, attributed to file, and pushes every file the
// replacement touched so a cleared advisory disappears from the editor as
// reliably as a new one appears.
//
// The advisories are positioned at the head of the file rather than at the
// authored key: a render advisory names a component and a delivery key, which
// are graph coordinates, and mapping those back to a position in the authoring
// document is a separate capability the daemon does not have. Head-of-file is
// the same fallback the closure-level findings use.
//
// The text is rendered through the render package's own formatter, so an
// advisory reads identically in the editor and in a terminal build.
func (s *Server) publishRenderDiagnostics(ctx context.Context, file string, diags []renderer.Diagnostic) {
	byFile := map[string][]Diagnostic{}
	if file != "" && len(diags) > 0 {
		mapped := make([]Diagnostic, 0, len(diags))
		for _, d := range diags {
			mapped = append(mapped, Diagnostic{
				Line:     1,
				Column:   1,
				Severity: d.Severity,
				Message:  renderer.RenderDiagnosticText(d),
			})
		}
		byFile[file] = mapped
	}
	for _, f := range s.setRenderDiagnostics(byFile) {
		_ = s.publishDiagnostics(ctx, f)
	}
}
