// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmengine

import (
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestSurfaceNodeDiagnostics_RoutesUnsupportedFieldClass proves the render-node
// diagnostic router sends the unsupported-field class to the structured
// unsupported-field sink, routes every other advisory to the warning sink, and
// locates both at the node and provider.
func TestSurfaceNodeDiagnostics_RoutesUnsupportedFieldClass(t *testing.T) {
	t.Parallel()
	var warnings []renderer.Diagnostic
	var unsupportedFields []renderer.Diagnostic
	nodeCtx := renderer.NodeRenderContext{
		NodeID:               "api",
		ProviderName:         "compose",
		WarningSink:          func(d renderer.Diagnostic) { warnings = append(warnings, d) },
		UnsupportedFieldSink: func(d renderer.Diagnostic) { unsupportedFields = append(unsupportedFields, d) },
	}
	diags := []renderer.Diagnostic{
		{Severity: abi.SeverityWarn, Code: renderer.UnsupportedFieldCode, SourceRef: "scheduling",
			Message: `target "compose" does not support scheduling and omitted it`},
		{Severity: abi.SeverityWarn, Code: "guest-said-so", SourceRef: "replicas", Suggest: "raise it",
			Message: "some other advisory"},
		{Severity: abi.SeverityInfo, Message: "an info note"},
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
	if d.Location == nil || d.Location.Instance != "api" || d.Location.URI != "" || d.Location.Kind != "compose" {
		t.Errorf("unsupported-field diagnostic not attributed to node/provider: %+v", d.Location)
	}
	// The other advisory and the info note route to the warning sink; the
	// unsupported-field class does not.
	if len(warnings) != 2 {
		t.Fatalf("warning sink got %d, want 2 (other advisories): %+v", len(warnings), warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w.Message, "does not support scheduling") {
			t.Errorf("unsupported-field warning leaked into the warning sink: %+v", w)
		}
	}
	// The advisory reaching the warning sink keeps everything the guest reported
	// AND gains the node and provider as a position — which is the whole point of
	// the sink carrying a diagnostic rather than a rendered line: a consumer that
	// reads no terminal has these as fields instead of as substrings.
	other := warnings[0]
	if other.Severity != abi.SeverityWarn || other.Code != "guest-said-so" ||
		other.SourceRef != "replicas" || other.Suggest != "raise it" {
		t.Errorf("the guest's own fields did not survive the routing: %+v", other)
	}
	if other.Location == nil || other.Location.Instance != "api" || other.Location.URI != "" || other.Location.Kind != "compose" {
		t.Errorf("warning-sink advisory not located at node/provider: %+v", other.Location)
	}
	// And it renders through the one display formatter as the line the terminal
	// has always shown for this class, so carrying more has moved no byte.
	const want = `node "api" (provider "compose"): some other advisory`
	if got := renderer.RenderDiagnosticText(other); got != want {
		t.Errorf("display line moved: got %q, want %q", got, want)
	}
}

// TestSurfaceNodeDiagnostics_GuestLocationSurvives pins the one case where the
// crossing must NOT supply the position: a guest that located its own finding
// knows something about where the fault sits that the crossing does not, so
// overwriting it would replace a precise position with a coarse one.
func TestSurfaceNodeDiagnostics_GuestLocationSurvives(t *testing.T) {
	t.Parallel()
	var warnings []renderer.Diagnostic
	nodeCtx := renderer.NodeRenderContext{
		NodeID:       "api",
		ProviderName: "compose",
		WarningSink:  func(d renderer.Diagnostic) { warnings = append(warnings, d) },
	}
	diags := []renderer.Diagnostic{{
		Severity: abi.SeverityWarn,
		Message:  "the guest knows where this is",
		Location: &renderer.DiagnosticLocation{Instance: "api", Kind: "compose", KeyPath: "spec.replicas"},
	}}
	if err := (&Engine{}).surfaceNodeDiagnostics(nodeCtx, diags); err != nil {
		t.Fatalf("surfaceNodeDiagnostics: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warning sink got %d, want 1", len(warnings))
	}
	if got := warnings[0].Location.KeyPath; got != "spec.replicas" {
		t.Errorf("the guest's own position was overwritten: KeyPath = %q, want %q", got, "spec.replicas")
	}
}

// TestSurfaceNodeDiagnostics_HaltSeverityStops proves a halt-severity
// diagnostic is joined into a returned error (build-halting), not routed to a sink.
func TestSurfaceNodeDiagnostics_HaltSeverityStops(t *testing.T) {
	t.Parallel()
	nodeCtx := renderer.NodeRenderContext{
		NodeID:               "api",
		ProviderName:         "compose",
		WarningSink:          func(renderer.Diagnostic) {},
		UnsupportedFieldSink: func(renderer.Diagnostic) {},
	}
	diags := []renderer.Diagnostic{{Severity: abi.SeverityHalt, Message: "boom"}}
	if err := (&Engine{}).surfaceNodeDiagnostics(nodeCtx, diags); err == nil {
		t.Fatal("expected an error for a halt-severity diagnostic, got nil")
	}
}
