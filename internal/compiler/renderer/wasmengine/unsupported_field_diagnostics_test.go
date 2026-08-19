// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestSurfaceNodeDiagnostics_RoutesUnsupportedFieldClass proves the render-node
// diagnostic router sends the unsupported-field class to the structured
// unsupported-field sink (attributed to the node and provider), routes other advisories
// to the free-form warning sink, and joins halt-severity diagnostics into a
// returned error.
func TestSurfaceNodeDiagnostics_RoutesUnsupportedFieldClass(t *testing.T) {
	t.Parallel()
	var warnings []string
	var unsupportedFields []renderer.Diagnostic
	nodeCtx := renderer.NodeRenderContext{
		NodeID:               "api",
		ProviderName:         "compose",
		WarningSink:          func(w string) { warnings = append(warnings, w) },
		UnsupportedFieldSink: func(d renderer.Diagnostic) { unsupportedFields = append(unsupportedFields, d) },
	}
	diags := []renderer.Diagnostic{
		{Severity: "warn", Code: renderer.UnsupportedFieldCode, SourceRef: "scheduling",
			Message: `target "compose" does not support scheduling and omitted it`},
		{Severity: "warn", Message: "some other advisory"},
		{Severity: "info", Message: "an info note"},
	}

	e := &Engine{}
	if err := e.surfaceNodeDiagnostics(nodeCtx, diags); err != nil {
		t.Fatalf("surfaceNodeDiagnostics returned error for advisory-only diags: %v", err)
	}

	if len(unsupportedFields) != 1 {
		t.Fatalf("unsupported-field sink got %d, want 1: %+v", len(unsupportedFields), unsupportedFields)
	}
	d := unsupportedFields[0]
	if d.Code != renderer.UnsupportedFieldCode || d.SourceRef != "scheduling" {
		t.Errorf("unsupported-field diagnostic malformed: %+v", d)
	}
	if d.Location == nil || d.Location.URI != "api" || d.Location.Kind != "compose" {
		t.Errorf("unsupported-field diagnostic not attributed to node/provider: %+v", d.Location)
	}
	// The other advisory and the info note route to the free-form sink; the
	// unsupported-field class does not.
	if len(warnings) != 2 {
		t.Fatalf("warning sink got %d, want 2 (other advisories): %+v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w, "does not support scheduling") {
			t.Errorf("unsupported-field warning leaked into the free-form sink: %q", w)
		}
	}
}

// TestSurfaceNodeDiagnostics_HaltSeverityStops proves a halt-severity
// diagnostic is joined into a returned error (build-halting), not routed to a sink.
func TestSurfaceNodeDiagnostics_HaltSeverityStops(t *testing.T) {
	t.Parallel()
	nodeCtx := renderer.NodeRenderContext{
		NodeID:               "api",
		ProviderName:         "compose",
		WarningSink:          func(string) {},
		UnsupportedFieldSink: func(renderer.Diagnostic) {},
	}
	diags := []renderer.Diagnostic{{Severity: "halt", Message: "boom"}}
	if err := (&Engine{}).surfaceNodeDiagnostics(nodeCtx, diags); err == nil {
		t.Fatal("expected an error for a halt-severity diagnostic, got nil")
	}
}
