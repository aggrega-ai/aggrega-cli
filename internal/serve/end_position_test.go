// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// TestErrorDataRangeRoundTrip pins the wire contract for the
// end-position fields on the JSON-RPC error envelope: a populated range
// marshals to snake_case keys and unmarshals back without loss.
func TestErrorDataRangeRoundTrip(t *testing.T) {
	t.Parallel()
	d := newErrorDataWithRange("aggregate.yaml", 5, 5, 5, 9)
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	for _, want := range []string{`"end_line":5`, `"end_column":9`, `"line":5`, `"column":5`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled %s missing %s", got, want)
		}
	}
	var back errorData
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.EndLine != 5 || back.EndColumn != 9 {
		t.Errorf("round-trip end = %d:%d; want 5:9", back.EndLine, back.EndColumn)
	}
}

// TestErrorDataFileLevelOmitsRange confirms a file-level emission — the
// shape every emission with no sub-scalar position in hand produces, a
// lockfile-scoped failure among them — drops the end-position keys
// entirely so the extension falls back to point-style.
func TestErrorDataFileLevelOmitsRange(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(newErrorData("lockfile.yaml", 0, 0))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	for _, absent := range []string{"end_line", "end_column", "line", "column"} {
		if strings.Contains(got, absent) {
			t.Errorf("file-level errorData %s should omit %q", got, absent)
		}
	}
}

// TestDiagnosticRangeRoundTrip pins the same contract on the per-file
// Diagnostic wire struct — the path Tier-4 squiggles travel.
func TestDiagnosticRangeRoundTrip(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(Diagnostic{Line: 8, Column: 9, EndLine: 8, EndColumn: 18, Severity: "error", Message: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	for _, want := range []string{`"end_line":8`, `"end_column":18`} {
		if !strings.Contains(got, want) {
			t.Errorf("marshalled %s missing %s", got, want)
		}
	}
	// A point-style diagnostic omits the end keys.
	rawPoint, _ := json.Marshal(Diagnostic{Line: 1, Column: 1, Severity: "error", Message: "y"})
	if strings.Contains(string(rawPoint), "end_line") || strings.Contains(string(rawPoint), "end_column") {
		t.Errorf("point-style diagnostic %s should omit end keys", rawPoint)
	}
}

// TestAnalyzerSelectorViolationMapsToSocketRange covers the selector
// class: a violation whose Field names a uses-socket key spans that key
// at sub-file fidelity.
func TestAnalyzerSelectorViolationMapsToSocketRange(t *testing.T) {
	t.Parallel()
	const file = "aggregate.yaml"
	content := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
spec:
  components:
    api:
      type: core/api
      uses:
        databases:
          primary:
            target: cache
`
	doc := parseYAML(t, content)
	errs := []analyzer.AnalysisError{{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    "selector violation",
		OriginFile: file,
		Field:      ".spec.components.api.uses.databases",
	}}
	diags := analyzerErrorsToDiagnostics(errs, file, doc, lineIndexFor(content))
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic; got %d", len(diags))
	}
	// "databases" key sits at line 8, column 9 (8-space indent); the
	// token spans 9 runes, so the end column is 18.
	d := diags[0]
	if d.Line != 8 || d.Column != 9 || d.EndLine != 8 || d.EndColumn != 18 {
		t.Errorf("range = %d:%d-%d:%d; want 8:9-8:18", d.Line, d.Column, d.EndLine, d.EndColumn)
	}
}

// TestAnalyzerAppliesToViolationMapsToFeatureRange covers the
// appliesTo class: a violation whose Field names a feature-instance key
// spans that key.
func TestAnalyzerAppliesToViolationMapsToFeatureRange(t *testing.T) {
	t.Parallel()
	const file = "aggregate.yaml"
	content := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
spec:
  components:
    web:
      type: core/web
      features:
        premium:
          type: core/premium
`
	doc := parseYAML(t, content)
	errs := []analyzer.AnalysisError{{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    "appliesTo violation",
		OriginFile: file,
		Field:      ".spec.components.web.features.premium",
	}}
	diags := analyzerErrorsToDiagnostics(errs, file, doc, lineIndexFor(content))
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic; got %d", len(diags))
	}
	// "premium" key sits at line 8, column 9; the token spans 7 runes,
	// so the end column is 16.
	d := diags[0]
	if d.Line != 8 || d.Column != 9 || d.EndLine != 8 || d.EndColumn != 16 {
		t.Errorf("range = %d:%d-%d:%d; want 8:9-8:16", d.Line, d.Column, d.EndLine, d.EndColumn)
	}
}

// TestAnalyzerSubsystemNestedViolationFallsBackToFileLevel covers the
// documented degradation: a subsystem-nested component carries an empty
// Field (the analyzer cannot address it with a top-level locator), so
// the diagnostic lands at file:1:1 with no end position.
//
// The empty Field is load-bearing: a top-level ".spec.components.db…"
// locator walked against this document would NOT reach the nested key —
// the walker's partial-match fallback returns the nearest ancestor
// (.spec, line 3) instead. So without the empty-Field guard the
// diagnostic would point at `spec:`, not degrade cleanly to file:1:1.
func TestAnalyzerSubsystemNestedViolationFallsBackToFileLevel(t *testing.T) {
	t.Parallel()
	const file = "aggregate.yaml"
	content := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
spec:
  subsystems:
    data:
      components:
        db:
          type: core/db
          uses:
            primary:
              target: cache
`
	doc := parseYAML(t, content)
	// Confirm the top-level locator cannot precisely reach the nested key
	// (line 7); it falls back to an ancestor — which is why the analyzer
	// emits an empty Field rather than this path.
	if node := yamlpos.FieldNode(doc, ".spec.components.db.uses.primary"); node != nil && node.Line == 7 {
		t.Errorf("top-level locator reached the nested db key (line 7); expected ancestor fallback")
	}
	// The analyzer emits an empty Field for the nested component; with no
	// locator the diagnostic must land at file:1:1 with no end position.
	errs := []analyzer.AnalysisError{{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    "selector violation",
		OriginFile: file,
		Field:      "",
	}}
	diags := analyzerErrorsToDiagnostics(errs, file, doc, lineIndexFor(content))
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic; got %d", len(diags))
	}
	d := diags[0]
	if d.Line != 1 || d.Column != 1 || d.EndLine != 0 || d.EndColumn != 0 {
		t.Errorf("range = %d:%d-%d:%d; want file-level 1:1 with no end", d.Line, d.Column, d.EndLine, d.EndColumn)
	}
}
