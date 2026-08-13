// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

func nodeCtx(id string, source any) renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID: id,
		Source: source,
		Provenance: renderer.ProvenanceContext{
			CompilerVersion:  "0.0.0-test",
			AggregateName:    "t",
			AggregateVersion: "1.0.0",
			ReleaseName:      "r",
			Environment:      "local",
		},
	}
}

// loadChart loads a fixture chart from disk, mirroring how the host projects a
// chart source into the shared render core the provider guest also renders
// through.
func loadChart(t *testing.T, path string) *chart.Chart {
	t.Helper()
	ch, err := loader.Load(path)
	if err != nil {
		t.Fatalf("load chart %q: %v", path, err)
	}
	return ch
}

func TestHelmEngine_Sourced_NamespaceFromAnnotation(t *testing.T) {
	t.Parallel()
	nc := nodeCtx("api", "testdata/ns-chart")
	nc.Annotations = map[string]string{renderer.AnnotationNamespace: "backend-zone"}
	files, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/ns-chart"),
		map[string]any{}, nc, helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, v := range files {
		if strings.Contains(string(v), "namespace: backend-zone") {
			return
		}
	}
	t.Errorf("no rendered file carries namespace: backend-zone; files: %v", keys(files))
}

func TestHelmEngine_Sourced_RendersChart(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"image":    "api:2.0.0",
		"replicas": float64(5),
	}
	files, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/minimal-chart"),
		values, nodeCtx("api", "testdata/minimal-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected rendered files from sourced chart")
	}
	found := false
	for k, v := range files {
		if strings.Contains(k, "deployment") {
			found = true
			if !strings.Contains(string(v), "api:2.0.0") {
				t.Errorf("values not flowing into sourced chart: %s", v)
			}
			if !strings.Contains(string(v), "replicas: 5") {
				t.Errorf("replicas not flowing into sourced chart: %s", v)
			}
		}
	}
	if !found {
		t.Fatalf("no deployment.yaml in rendered files: %v", keys(files))
	}
}

func TestHelmEngine_Sourced_CarriesAggregaLabels(t *testing.T) {
	t.Parallel()
	// Sourced-mode chart output must carry aggrega.ai/* provenance labels —
	// sourceless mode injects them during generation; sourced mode post-processes.
	values := map[string]any{"image": "api:2.0.0"}
	files, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/minimal-chart"),
		values, nodeCtx("api", "testdata/minimal-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var dep string
	for k, v := range files {
		if strings.Contains(k, "deployment") {
			dep = string(v)
		}
	}
	if dep == "" {
		t.Fatal("no deployment.yaml")
	}
	if !strings.Contains(dep, "aggrega.ai/aggregate-name: t") {
		t.Errorf("missing aggrega.ai/aggregate-name label:\n%s", dep)
	}
	if !strings.Contains(dep, "aggrega.ai/component-id: api") {
		t.Errorf("missing aggrega.ai/component-id label:\n%s", dep)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
