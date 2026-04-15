// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer/helm"
)

func nodeCtx(id string, source interface{}) renderer.NodeRenderContext {
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
	values := map[string]interface{}{
		"image":       "myapp:1.2.3",
		"replicas":    float64(3),
		"ports":       []interface{}{"8080:8080"},
		"environment": map[string]interface{}{"PORT": "8080"},
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

func TestHelmEngine_EmptySourceIsFatal(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	_, err := eng.Render(context.Background(), map[string]interface{}{}, nodeCtx("api", ""))
	if !errors.Is(err, helm.ErrEmptySource) {
		t.Fatalf("expected ErrEmptySource, got %v", err)
	}
}

func TestHelmEngine_Sourced_RendersChart(t *testing.T) {
	t.Parallel()
	eng := &helm.HelmEngine{}
	values := map[string]interface{}{
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

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
