// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// subchartMarker is the data value only the condition-gated subchart's
// ConfigMap carries; the parent's ConfigMap carries a different role. A
// rendered output containing it proves the gated subchart rendered.
const subchartMarker = "role: gated-subchart"

// renderedContains reports whether any emitted file's content contains marker.
func renderedContains(files map[string][]byte, marker string) bool {
	for _, v := range files {
		if strings.Contains(string(v), marker) {
			return true
		}
	}
	return false
}

// TestHelmEngine_Sourced_ConditionPrunesSubchart pins condition-driven subchart
// pruning against the Helm CLI's own behavior: a dependency gated by
// `condition: gated-sub.enabled` renders only when the merged values resolve
// that path to true. The condition is driven by the INJECTED (Phase B) values
// arg, not merely the chart's defaults — the case a `uses`-driven disable must
// satisfy. Oracle (helm template rr . --set gated-sub.enabled=<b>): false emits
// only the parent ConfigMap; true emits the parent plus the gated-sub ConfigMap.
func TestHelmEngine_Sourced_ConditionPrunesSubchart(t *testing.T) {
	t.Parallel()

	t.Run("injected condition false prunes the subchart", func(t *testing.T) {
		files, err := helmcore.RenderLoadedChart(context.Background(),
			loadChart(t, "testdata/condition-chart"),
			map[string]any{"gated-sub": map[string]any{"enabled": false}},
			nodeCtx("app", "testdata/condition-chart"),
			helmcore.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("render with condition false: %v", err)
		}
		if renderedContains(files, subchartMarker) {
			t.Fatalf("condition false but the gated subchart still rendered; files: %v", sortedFileKeys(files))
		}
		if !renderedContains(files, "role: parent") {
			t.Fatalf("parent ConfigMap missing; files: %v", sortedFileKeys(files))
		}
		// Faithful to `helm template rr . --set gated-sub.enabled=false`: exactly
		// the single parent object, nothing from the pruned subchart.
		if len(files) != 1 {
			t.Fatalf("expected exactly 1 file (parent only) when pruned, got %d: %v", len(files), sortedFileKeys(files))
		}
	})

	t.Run("injected condition true renders the subchart", func(t *testing.T) {
		files, err := helmcore.RenderLoadedChart(context.Background(),
			loadChart(t, "testdata/condition-chart"),
			map[string]any{"gated-sub": map[string]any{"enabled": true}},
			nodeCtx("app", "testdata/condition-chart"),
			helmcore.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("render with condition true: %v", err)
		}
		if !renderedContains(files, subchartMarker) {
			t.Fatalf("condition true but the gated subchart did not render; files: %v", sortedFileKeys(files))
		}
		if !renderedContains(files, "role: parent") {
			t.Fatalf("parent ConfigMap missing; files: %v", sortedFileKeys(files))
		}
		// Faithful to `helm template rr . --set gated-sub.enabled=true`: parent
		// plus the gated-sub object.
		if len(files) != 2 {
			t.Fatalf("expected exactly 2 files (parent + subchart) when enabled, got %d: %v", len(files), sortedFileKeys(files))
		}
	})
}

// TestHelmEngine_Sourced_ConditionPruningDeterministic pins byte-identity of the
// pruned render across runs: the pruning transform must not perturb determinism.
func TestHelmEngine_Sourced_ConditionPruningDeterministic(t *testing.T) {
	t.Parallel()
	render := func() map[string][]byte {
		files, err := helmcore.RenderLoadedChart(context.Background(),
			loadChart(t, "testdata/condition-chart"),
			map[string]any{"gated-sub": map[string]any{"enabled": false}},
			nodeCtx("app", "testdata/condition-chart"),
			helmcore.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return files
	}
	first, second := render(), render()
	if len(first) != len(second) {
		t.Fatalf("file count differs across runs: %v vs %v", sortedFileKeys(first), sortedFileKeys(second))
	}
	for k, v := range first {
		w, ok := second[k]
		if !ok {
			t.Errorf("file %q present in first run, absent in second", k)
			continue
		}
		if string(v) != string(w) {
			t.Errorf("file %q differs across runs:\nfirst:\n%s\nsecond:\n%s", k, v, w)
		}
	}
}

// TestHelmEngine_Sourced_NoDependenciesUnaffected pins non-regression: a chart
// with no dependency subcharts renders identically with the dependency-pruning
// pass in place — the pass is a no-op when there is nothing to prune.
func TestHelmEngine_Sourced_NoDependenciesUnaffected(t *testing.T) {
	t.Parallel()
	files, err := helmcore.RenderLoadedChart(context.Background(),
		loadChart(t, "testdata/minimal-chart"),
		map[string]any{"replicas": 2, "image": "nginx:1"},
		nodeCtx("app", "testdata/minimal-chart"),
		helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("render minimal chart: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly 1 file from the dependency-free chart, got %d: %v", len(files), sortedFileKeys(files))
	}
	if !renderedContains(files, "kind: Deployment") {
		t.Fatalf("minimal chart Deployment missing; files: %v", sortedFileKeys(files))
	}
}
