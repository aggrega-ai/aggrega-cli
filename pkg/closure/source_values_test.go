// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestEvaluateSourceSpecFields_ReadsValuesAndInfra pins that a source-version
// template resolves against BOTH the .values and .infra pillars. The .values
// pillar was hardcoded empty before — supplying a value and seeing it resolve is
// the discriminating proof that the channel is now wired (pre-fix the template
// resolved empty regardless of the supplied values).
func TestEvaluateSourceSpecFields_ReadsValuesAndInfra(t *testing.T) {
	t.Parallel()
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/db",
		"version": "{{ .values.source_version }}-{{ .infra.flavor }}",
	}
	values := map[string]any{"source_version": "25.0.8"}
	infra := map[string]any{"flavor": "bitnami"}

	spec, err := closure.EvaluateSourceSpecFields(values, infra, source, types.OrderedMap[string]{}, "db-recipe:1.0.0")
	if err != nil {
		t.Fatalf("EvaluateSourceSpecFields: %v", err)
	}
	if spec == nil || spec.Version != "25.0.8-bitnami" {
		t.Fatalf("Version = %q; want 25.0.8-bitnami (the .values + .infra pillars must both resolve)", specVersion(spec))
	}
}

// TestEvaluateSourceSpecFields_ValuesMissingHalts pins that an unsupplied
// .values key still halts loudly (missingkey=error) — the channel pipes the
// value when present and surfaces a precise lock-time error when the template
// references a key neither pillar supplies, rather than silently rendering empty.
func TestEvaluateSourceSpecFields_ValuesMissingHalts(t *testing.T) {
	t.Parallel()
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/db",
		"version": "{{ .values.source_version }}",
	}
	_, err := closure.EvaluateSourceSpecFields(map[string]any{}, map[string]any{}, source, types.OrderedMap[string]{}, "db-recipe:1.0.0")
	if err == nil {
		t.Fatal("a source template referencing an unsupplied .values key must halt, not render empty")
	}
}

func specVersion(s *closure.SourceSpec) string {
	if s == nil {
		return "<nil>"
	}
	return s.Version
}
