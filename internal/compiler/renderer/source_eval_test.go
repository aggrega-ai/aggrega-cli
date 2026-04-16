// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"
)

func TestEvaluateRecipeSource_NilPassesThrough(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource(nil, "n", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestEvaluateRecipeSource_StringWithInfraRef(t *testing.T) {
	t.Parallel()
	// Acceptance scenario: source.version-like template resolves from infra.
	infra := map[string]interface{}{"source_version": "26.0.0"}
	got, err := EvaluateRecipeSource(`{{ .infra.source_version | default "25.0.8" }}`, "api", nil, infra, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "26.0.0" {
		t.Errorf("got %v, want %q", got, "26.0.0")
	}
}

func TestEvaluateRecipeSource_StringDefaultWhenInfraMissing(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource(`{{ .infra.source_version | default "25.0.8" }}`, "api", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "25.0.8" {
		t.Errorf("expected default to win, got %v", got)
	}
}

func TestEvaluateRecipeSource_LiteralStringShortCircuits(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource("./charts/my-chart", "api", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "./charts/my-chart" {
		t.Errorf("literal should pass through, got %v", got)
	}
}

func TestEvaluateRecipeSource_EmptyStringReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource("", "api", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "" {
		t.Errorf("got %v", got)
	}
}

func TestEvaluateRecipeSource_StructuredSourcePartialEval(t *testing.T) {
	t.Parallel()
	source := map[string]interface{}{
		"registry": "oci://example.com/charts",
		"version":  `{{ .infra.v }}`,
		"size":     42, // non-string — passes through unchanged
	}
	infra := map[string]interface{}{"v": "1.2.3"}
	got, err := EvaluateRecipeSource(source, "api", nil, infra, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	if m["registry"] != "oci://example.com/charts" {
		t.Errorf("registry mutated: %v", m["registry"])
	}
	if m["version"] != "1.2.3" {
		t.Errorf("version: got %v, want 1.2.3", m["version"])
	}
	if m["size"] != 42 {
		t.Errorf("non-string field changed: %v", m["size"])
	}
}

func TestEvaluateRecipeSource_RejectsInjectRef(t *testing.T) {
	t.Parallel()
	_, err := EvaluateRecipeSource(`{{ .context.inject.env.PATH }}`, "api", nil, nil, nil)
	if err == nil {
		t.Fatal("expected self-containment error")
	}
	if !strings.Contains(err.Error(), "self-contained") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestEvaluateRecipeSource_RejectsUsesRef(t *testing.T) {
	t.Parallel()
	source := map[string]interface{}{
		"path": `{{ (index .context.uses "db").provides.host }}`,
	}
	_, err := EvaluateRecipeSource(source, "api", nil, nil, nil)
	if err == nil {
		t.Fatal("expected self-containment error on structured source sub-field")
	}
}

func TestEvaluateRecipeSource_ProvidesAvailable(t *testing.T) {
	t.Parallel()
	provides := map[string]string{"host": "api.svc"}
	got, err := EvaluateRecipeSource(`{{ .provides.host }}`, "api", nil, nil, provides)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "api.svc" {
		t.Errorf("got %v", got)
	}
}
