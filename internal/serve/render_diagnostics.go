// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// publishRenderDiagnostics replaces the render diagnostic layer with the
// advisories this render raised, attributed to file, and pushes every file the
// replacement touched so a cleared advisory disappears from the editor as
// reliably as a new one appears.
//
// extra carries advisories about the render that the render step did not itself
// produce — a reference declined for want of a pin, which the host declined and
// the guest never saw. They are passed in and published here rather than through
// a call of their own because this function REPLACES the whole layer: a second
// publication would erase this one's entries, so every advisory belonging to the
// layer arrives in one call or not at all.
//
// The advisories are positioned at the head of the file rather than at the
// authored key: a render advisory names a component and a delivery key, which
// are graph coordinates, and mapping those back to a position in the authoring
// document is a separate capability the daemon does not have. Head-of-file is
// the same fallback the closure-level findings use.
//
// The text is rendered through the render package's own formatter, so an
// advisory reads identically in the editor and in a terminal build.
//
// The severity is translated rather than copied: a render advisory arrives on the
// plugin wire, whose severities are halt/warn/info, and leaves on the editor wire,
// whose severities are error/warning/info. The two vocabularies serve different
// boundaries and are deliberately separate, so this is the one place a value
// crosses between them.
//
// The code gets the opposite treatment, and the contrast with the severity one
// paragraph above is the point: a severity is a member of a closed set each wire
// declares for itself, so crossing wires means translating it, while a code is an
// opaque machine-matchable string that means the same thing on both sides.
// Translating one would mint a second spelling for a single identity. So the
// entry publishes whatever the advisory arrived carrying, verbatim, and carries
// nothing where the advisory carried nothing — the empty string and the omitted
// key being the same statement on this wire.
func (s *Server) publishRenderDiagnostics(ctx context.Context, file string, diags []renderer.Diagnostic, extra []Diagnostic) {
	byFile := map[string][]Diagnostic{}
	if file != "" && len(diags)+len(extra) > 0 {
		mapped := make([]Diagnostic, 0, len(diags)+len(extra))
		for _, d := range diags {
			mapped = append(mapped, Diagnostic{
				Line:     1,
				Column:   1,
				Severity: editorSeverity(d.Severity),
				Message:  renderer.RenderDiagnosticText(d),
				Code:     d.Code,
			})
		}
		mapped = append(mapped, extra...)
		byFile[file] = mapped
	}
	for _, f := range s.setRenderDiagnostics(byFile) {
		_ = s.publishDiagnostics(ctx, f)
	}
}

// editorSeverity translates a plugin-wire diagnostic severity into the editor
// wire's own severity. halt is the plugin wire's build-stopping severity and
// error is the editor wire's, warn maps to warning, and info is spelled the same
// on both. A value the plugin wire does not declare maps to the editor's error
// tier, so an unmodelled severity surfaces visibly instead of being downgraded.
func editorSeverity(pluginSeverity string) string {
	switch pluginSeverity {
	case abi.SeverityHalt:
		return editorSeverityError
	case abi.SeverityWarn:
		return editorSeverityWarning
	case abi.SeverityInfo:
		return editorSeverityInfo
	default:
		return editorSeverityError
	}
}
