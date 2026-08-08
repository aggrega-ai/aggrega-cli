// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func omSrcInfra(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func TestEvaluateRecipeSource_NilPassesThrough(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource(nil, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestEvaluateRecipeSource_StringWithInfraRef(t *testing.T) {
	t.Parallel()
	infra := omSrcInfra("source_version", "26.0.0")
	got, err := EvaluateRecipeSource(`{{ .infra.source_version | default "25.0.8" }}`, "api", types.OrderedMap[any]{}, infra, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "26.0.0" {
		t.Errorf("got %v, want %q", got, "26.0.0")
	}
}

func TestEvaluateRecipeSource_StringDefaultWhenInfraMissing(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource(`{{ .infra.source_version | default "25.0.8" }}`, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "25.0.8" {
		t.Errorf("expected default to win, got %v", got)
	}
}

func TestEvaluateRecipeSource_LiteralStringShortCircuits(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource("./charts/my-chart", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "./charts/my-chart" {
		t.Errorf("literal should pass through, got %v", got)
	}
}

func TestEvaluateRecipeSource_EmptyStringReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeSource("", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "" {
		t.Errorf("got %v", got)
	}
}

func TestEvaluateRecipeSource_StructuredSourcePartialEval(t *testing.T) {
	t.Parallel()
	source := map[string]any{
		"registry": "oci://example.com/charts",
		"version":  `{{ .infra.v }}`,
		"size":     42, // non-string — passes through unchanged
	}
	infra := omSrcInfra("v", "1.2.3")
	got, err := EvaluateRecipeSource(source, "api", types.OrderedMap[any]{}, infra, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	m, ok := got.(map[string]any)
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
	_, err := EvaluateRecipeSource(`{{ .context.inject.env.PATH }}`, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err == nil {
		t.Fatal("expected self-containment error")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".context.inject.env.PATH") {
		t.Errorf("diagnostic does not name the authored path: %s", diagnosticChainText(err))
	}
}

func TestEvaluateRecipeSource_RejectsUsesRef(t *testing.T) {
	t.Parallel()
	source := map[string]any{
		"path": `{{ (index .context.uses "db").provides.host }}`,
	}
	_, err := EvaluateRecipeSource(source, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err == nil {
		t.Fatal("expected self-containment error on structured source sub-field")
	}
}

// The source surface shares LocalsContext with Recipe locals, so it refuses
// .provides for the same reason: source and locals are both evaluated ahead of the
// node's provides, and the shared context type declares no such pillar.
func TestEvaluateRecipeSource_RejectsProvidesRef(t *testing.T) {
	t.Parallel()
	_, err := EvaluateRecipeSource(`{{ .provides.host }}`, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err == nil {
		t.Fatal("expected a halt on the reverse reference, got nil")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".provides.host") {
		t.Errorf("diagnostic does not name the authored path: %s", diagnosticChainText(err))
	}
	// A structured source's string sub-field is checked identically.
	structured := map[string]any{"version": `{{ .provides.chart_version }}`}
	if _, err := EvaluateRecipeSource(structured, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{}); err == nil {
		t.Error("expected the same halt on a structured source sub-field")
	}
}
