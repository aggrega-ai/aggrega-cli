// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestSurfaceNodeDiagnostics_RoutesDegradationClass proves the render-node
// diagnostic router sends the target-degradation class to the structured
// degradation sink (attributed to the node and provider), routes other advisories
// to the free-form warning sink, and joins error-severity diagnostics into a
// returned error.
func TestSurfaceNodeDiagnostics_RoutesDegradationClass(t *testing.T) {
	t.Parallel()
	var warnings []string
	var degradations []renderer.Diagnostic
	nodeCtx := renderer.NodeRenderContext{
		NodeID:          "api",
		ProviderName:    "compose",
		WarningSink:     func(w string) { warnings = append(warnings, w) },
		DegradationSink: func(d renderer.Diagnostic) { degradations = append(degradations, d) },
	}
	diags := []renderer.Diagnostic{
		{Severity: "warning", Code: renderer.TargetDegradationCode, SourceRef: "scheduling",
			Message: `target "compose" does not support scheduling and omitted it`},
		{Severity: "warning", Message: "some other advisory"},
		{Severity: "info", Message: "an info note"},
	}

	e := &Engine{}
	if err := e.surfaceNodeDiagnostics(nodeCtx, diags); err != nil {
		t.Fatalf("surfaceNodeDiagnostics returned error for advisory-only diags: %v", err)
	}

	if len(degradations) != 1 {
		t.Fatalf("degradation sink got %d, want 1: %+v", len(degradations), degradations)
	}
	d := degradations[0]
	if d.Code != renderer.TargetDegradationCode || d.SourceRef != "scheduling" {
		t.Errorf("degradation diagnostic malformed: %+v", d)
	}
	if d.Location == nil || d.Location.URI != "api" || d.Location.Kind != "compose" {
		t.Errorf("degradation diagnostic not attributed to node/provider: %+v", d.Location)
	}
	// The other advisory and the info note route to the free-form sink; the
	// degradation class does not.
	if len(warnings) != 2 {
		t.Fatalf("warning sink got %d, want 2 (non-degradation advisories): %+v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w, "does not support scheduling") {
			t.Errorf("degradation warning leaked into the free-form sink: %q", w)
		}
	}
}

// TestSurfaceNodeDiagnostics_ErrorSeverityHalts proves an error-severity
// diagnostic is joined into a returned error (build-halting), not routed to a sink.
func TestSurfaceNodeDiagnostics_ErrorSeverityHalts(t *testing.T) {
	t.Parallel()
	nodeCtx := renderer.NodeRenderContext{
		NodeID:          "api",
		ProviderName:    "compose",
		WarningSink:     func(string) {},
		DegradationSink: func(renderer.Diagnostic) {},
	}
	diags := []renderer.Diagnostic{{Severity: "error", Message: "boom"}}
	if err := (&Engine{}).surfaceNodeDiagnostics(nodeCtx, diags); err == nil {
		t.Fatal("expected an error for an error-severity diagnostic, got nil")
	}
}
