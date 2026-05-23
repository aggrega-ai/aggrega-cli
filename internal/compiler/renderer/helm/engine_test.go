// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
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

func TestHelmEngine_Sourceless_DeploymentAndService(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	values := map[string]any{
		"image":       "myapp:1.2.3",
		"replicas":    float64(3),
		"ports":       []any{"8080:8080"},
		"environment": map[string]any{"PORT": "8080"},
	}
	files, err := eng.Render(context.Background(), values, nodeCtx("api", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dep, ok := files["api-deployment.yaml"]
	if !ok {
		t.Fatalf("expected api-deployment.yaml, got keys: %v", keys(files))
	}
	if !strings.Contains(string(dep), "myapp:1.2.3") {
		t.Errorf("image not in deployment: %s", dep)
	}
	if !strings.Contains(string(dep), "replicas: 3") {
		t.Errorf("replicas not flowing from values: %s", dep)
	}
	if _, ok := files["api-service.yaml"]; !ok {
		t.Errorf("expected api-service.yaml, got keys: %v", keys(files))
	}
}

func TestHelmEngine_Sourceless_NamespaceFromAnnotation(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	values := map[string]any{
		"image": "myapp:1.0",
		"ports": []any{"8080:8080"},
	}
	nc := nodeCtx("api", nil)
	nc.Annotations = map[string]string{renderer.AnnotationNamespace: "backend-zone"}
	files, err := eng.Render(context.Background(), values, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(files["api-deployment.yaml"]), "namespace: backend-zone") {
		t.Errorf("deployment missing namespace: %s", files["api-deployment.yaml"])
	}
	if !strings.Contains(string(files["api-service.yaml"]), "namespace: backend-zone") {
		t.Errorf("service missing namespace: %s", files["api-service.yaml"])
	}
}

func TestHelmEngine_Sourced_NamespaceFromAnnotation(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	nc := nodeCtx("api", "testdata/ns-chart")
	nc.Annotations = map[string]string{renderer.AnnotationNamespace: "backend-zone"}
	files, err := eng.Render(context.Background(), map[string]any{}, nc)
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

func TestHelmEngine_EmptySourceIsFatal(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	_, err := eng.Render(context.Background(), map[string]any{}, nodeCtx("api", ""))
	if !errors.Is(err, helm.ErrEmptySource) {
		t.Fatalf("expected ErrEmptySource, got %v", err)
	}
}

func TestHelmEngine_Sourced_RendersChart(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	values := map[string]any{
		"image":    "api:2.0.0",
		"replicas": float64(5),
	}
	files, err := eng.Render(context.Background(), values, nodeCtx("api", "testdata/minimal-chart"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected rendered files from sourced chart")
	}
	found := false
	for k, v := range files {
		if strings.Contains(k, "deployment.yaml") {
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
	eng := &helm.HelmEngine{}
	values := map[string]any{"image": "api:2.0.0"}
	files, err := eng.Render(context.Background(), values, nodeCtx("api", "testdata/minimal-chart"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var dep string
	for k, v := range files {
		if strings.Contains(k, "deployment.yaml") {
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

// TestHelmEngine_StructuredSourceTriggersDefensiveError pins the
// orchestrator-bug defence: when nodeCtx.Source is non-nil and
// non-string (e.g., a structured map that the orchestrator failed
// to resolve), the helm engine MUST halt with an explicit "internal
// error" message rather than silently passing the unresolved value
// downstream.
func TestHelmEngine_StructuredSourceTriggersDefensiveError(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	structured := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://r/c",
		"version": "1.0.0",
	}
	ctx := nodeCtx("api", structured)
	ctx.OriginalSource = structured
	_, err := eng.Render(context.Background(), map[string]any{}, ctx)
	if err == nil {
		t.Fatal("expected defensive error; got nil")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("error should reference the defensive contract; got %v", err)
	}
}
