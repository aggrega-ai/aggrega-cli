// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

func sensitivityNotConveyedDiag() renderer.Diagnostic {
	d := renderer.SensitivityNotConveyedDiagnostic("sens-mapped", "values.dbPassword")
	d.Location = &renderer.DiagnosticLocation{URI: "mapped", Kind: "helm"}
	return d
}

// TestEmitRenderWarnings_SensitivityNotConveyedSurvivesUnsupportedFieldSuppression
// is the guard on the third coded class's separation. All three ride one
// out-of-band stream, so nothing structural stops a suppression written against
// the stream rather than against a code from silencing them together — and
// silencing a notice about the author's own security-relevant declaration as a
// side effect of a target-fidelity preference is exactly the outcome a separate
// code exists to prevent. The test fails the moment suppression stops keying on
// the code.
func TestEmitRenderWarnings_SensitivityNotConveyedSurvivesUnsupportedFieldSuppression(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Diagnostics: []renderer.Diagnostic{unsupportedFieldDiag(), sensitivityNotConveyedDiag()},
	}

	var suppressed bytes.Buffer
	emitRenderWarnings(&suppressed, result, true)
	out := suppressed.String()
	if strings.Contains(out, "does not support scheduling") {
		t.Errorf("unsupported-field warning must be suppressed by the flag:\n%s", out)
	}
	if !strings.Contains(out, "values.dbPassword") {
		t.Errorf("sensitivity-not-conveyed advisory must survive unsupported-field suppression:\n%s", out)
	}
	if !strings.Contains(out, `node "mapped" (provider "helm")`) {
		t.Errorf("sensitivity-not-conveyed advisory not node/provider-attributed:\n%s", out)
	}
	// The positive control on the suppression itself: with the flag off, the
	// class that was dropped above is present. Without it, a display emitting
	// nothing at all would satisfy the first assertion.
	var shown bytes.Buffer
	emitRenderWarnings(&shown, result, false)
	if !strings.Contains(shown.String(), "does not support scheduling") {
		t.Errorf("the suppressed class is absent with the flag off, so the assertion above measured nothing:\n%s",
			shown.String())
	}
	if renderer.SensitivityNotConveyedCode == renderer.UnsupportedFieldCode {
		t.Fatalf("the two classes must not share a code; both are %q", renderer.SensitivityNotConveyedCode)
	}
}

// TestMergeRenderDiagnostics_CarriesSensitivityNotConveyed is the carrier half,
// and it is the half that makes the advisory reachable at all by a caller with
// no terminal — the daemon, the assistant-facing tool protocol and the editor
// each read this one list rather than the render's two streams.
func TestMergeRenderDiagnostics_CarriesSensitivityNotConveyed(t *testing.T) {
	t.Parallel()
	result := &renderer.RenderResult{
		Diagnostics: []renderer.Diagnostic{sensitivityNotConveyedDiag()},
	}

	merged := mergeRenderDiagnostics(result)
	if len(merged) != 1 {
		t.Fatalf("merged carries %d entries, want 1: %+v", len(merged), merged)
	}
	if merged[0].Code != renderer.SensitivityNotConveyedCode {
		t.Errorf("the entry lost its class code: %q", merged[0].Code)
	}
	if merged[0].SourceRef != "values.dbPassword" {
		t.Errorf("the entry lost the authored coordinate a consumer acts on: %q", merged[0].SourceRef)
	}
	if merged[0].Location == nil || merged[0].Location.URI != "mapped" {
		t.Errorf("the entry lost its position in the merge: %+v", merged[0].Location)
	}
	// And what a caller reads matches what a terminal printed, line for line,
	// because both render through the one formatter.
	var shown bytes.Buffer
	emitRenderWarnings(&shown, result, false)
	if want := "  warning: " + renderer.RenderDiagnosticText(merged[0]) + "\n"; shown.String() != want {
		t.Errorf("the carrier and the display disagree:\n display: %q\n carrier: %q", shown.String(), want)
	}
}
