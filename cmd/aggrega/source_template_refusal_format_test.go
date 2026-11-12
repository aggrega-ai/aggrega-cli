// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestFormatCompilerError_SourceTemplateRefusalNamesFieldEntryAndCause renders, as
// a terminal prints it, the refusal a Recipe gets when its chart version reads a
// locals entry that calls a function the source-coordinate surface does not
// register.
//
// What the reader must meet is one diagnostic headed by the parse condition's
// short code, naming the source field, the entry and the function. A refusal
// printed as two bare condition sentences separated by a rule — the shape a
// failure carrying two identities as a join is rendered in — names none of them,
// so the absence of the rule is asserted beside each name.
func TestFormatCompilerError_SourceTemplateRefusalNamesFieldEntryAndCause(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm/probe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "helm",
			Source: map[string]any{
				"kind":    "helm-https-chart",
				"url":     "https://charts.example.test",
				"chart":   "probe",
				"version": "{{ .locals.chart_version }}",
			},
		},
	}
	recipe.Spec.Locals.Set("chart_version", "0.{{ add1 (int .infra.port) }}.0")

	_, err := closure.EvaluateSourceSpec(map[string]any{}, map[string]any{"port": 8080}, recipe)
	if err == nil {
		t.Fatal("the source evaluated; want the refusal")
	}
	out := formatCompilerError(err)
	if !strings.HasPrefix(out, "[E0111]") {
		t.Errorf("the diagnostic should open with the parse condition's short code; got:\n%s", out)
	}
	if strings.Contains(out, "\n---\n") {
		t.Errorf("the refusal is printed as several findings; got:\n%s", out)
	}
	for _, want := range []string{
		"helm/probe:1.0.0.spec.locals.chart_version",
		"read by helm/probe:1.0.0.spec.source.version",
		`function "add1" not defined`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the diagnostic omits %q; got:\n%s", want, out)
		}
	}
}
