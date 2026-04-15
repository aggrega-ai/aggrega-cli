// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
)

func testNodeCtx() renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:    "api-server",
		Labels:    map[string]string{"component.aggrega.ai/type": "server"},
		Component: "core/api-server",
		BoundaryPath: "aws-prod/eks-main",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion:  "0.1.0",
			AggregateName:    "test-app",
			AggregateVersion: "1.0.0",
			ReleaseName:      "prod",
			Environment:      "production",
		},
	}
}

func TestHelmEngine_Render_SimpleDeployment(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	values := map[string]interface{}{
		"image": "myapp:v1.0.0",
		"ports": []interface{}{"8080:8080"},
		"environment": map[string]interface{}{
			"PORT": "8080",
		},
	}

	out, err := engine.Render(context.Background(), values, testNodeCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Chart.yaml exists
	if _, ok := out["Chart.yaml"]; !ok {
		t.Error("missing Chart.yaml")
	}

	// Deployment exists
	depKey := "templates/deployment.yaml"
	depYAML, ok := out[depKey]
	if !ok {
		t.Fatalf("missing %s", depKey)
	}
	depStr := string(depYAML)

	// Image
	if !strings.Contains(depStr, "myapp:v1.0.0") {
		t.Error("deployment missing image")
	}
	// Env var
	if !strings.Contains(depStr, "PORT") {
		t.Error("deployment missing env var PORT")
	}
	// Container port
	if !strings.Contains(depStr, "containerPort: 8080") {
		t.Error("deployment missing containerPort")
	}

	// Service exists
	svcKey := "templates/svc.yaml"
	if _, ok := out[svcKey]; !ok {
		t.Error("missing service YAML")
	}
}

func TestHelmEngine_Render_AggreraLabels(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	values := map[string]interface{}{
		"image": "myapp:latest",
	}

	out, err := engine.Render(context.Background(), values, testNodeCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	depStr := string(out["templates/deployment.yaml"])

	// aggrega.ai/* labels
	checks := []string{
		"aggrega.ai/component-id: api-server",
		"aggrega.ai/component-type: core/api-server",
		"aggrega.ai/boundary-path: aws-prod/eks-main",
		"aggrega.ai/compiler-version: 0.1.0",
		"aggrega.ai/aggregate-name: test-app",
		"aggrega.ai/environment: production",
	}
	for _, check := range checks {
		if !strings.Contains(depStr, check) {
			t.Errorf("deployment missing label: %s", check)
		}
	}
}

func TestHelmEngine_Render_NoPortsNoService(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	values := map[string]interface{}{
		"image": "worker:latest",
	}

	out, err := engine.Render(context.Background(), values, testNodeCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No service when no ports
	for k := range out {
		if strings.Contains(k, "-svc.yaml") {
			t.Errorf("should not generate service without ports, but found %s", k)
		}
	}
}

func TestHelmEngine_Render_DefaultImage(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	out, err := engine.Render(context.Background(), map[string]interface{}{}, testNodeCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	depStr := string(out["templates/deployment.yaml"])
	if !strings.Contains(depStr, defaultImage) {
		t.Errorf("expected default image %q in deployment", defaultImage)
	}
}

func TestHelmEngine_Render_Replicas(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	values := map[string]interface{}{
		"image":    "myapp:latest",
		"replicas": float64(3), // JSON numbers are float64
	}

	out, err := engine.Render(context.Background(), values, testNodeCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	depStr := string(out["templates/deployment.yaml"])
	if !strings.Contains(depStr, "replicas: 3") {
		t.Errorf("expected replicas: 3, got:\n%s", depStr)
	}
}

func TestHelmEngine_Render_DeterministicEnvOrder(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	values := map[string]interface{}{
		"image": "app:latest",
		"environment": map[string]interface{}{
			"Z_VAR": "z",
			"A_VAR": "a",
			"M_VAR": "m",
		},
	}

	var firstOutput string
	for i := 0; i < 10; i++ {
		out, err := engine.Render(context.Background(), values, testNodeCtx())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		depStr := string(out["templates/deployment.yaml"])
		if i == 0 {
			firstOutput = depStr
		} else if depStr != firstOutput {
			t.Errorf("run %d: deployment output changed (non-deterministic)", i)
		}
	}

	// Verify sorted: A_VAR < M_VAR < Z_VAR
	aIdx := strings.Index(firstOutput, "A_VAR")
	mIdx := strings.Index(firstOutput, "M_VAR")
	zIdx := strings.Index(firstOutput, "Z_VAR")
	if aIdx >= mIdx || mIdx >= zIdx {
		t.Errorf("env vars not sorted: A=%d M=%d Z=%d", aIdx, mIdx, zIdx)
	}
}

func TestHelmEngine_Render_ChartName(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	out, err := engine.Render(context.Background(), map[string]interface{}{}, testNodeCtx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Sub-chart Chart.yaml uses the instance_id (node ID), not the aggregate name.
	// The umbrella chart (produced by ComposeBoundary) uses aggregate + boundary.
	chartStr := string(out["Chart.yaml"])
	if !strings.Contains(chartStr, "name: api-server") {
		t.Errorf("Chart.yaml should use instance_id as sub-chart name, got:\n%s", chartStr)
	}
}

func TestHelmEngine_Render_DotNotationNodeID(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	nodeCtx := testNodeCtx()
	nodeCtx.NodeID = "data-layer.main-db"

	out, err := engine.Render(context.Background(), map[string]interface{}{"image": "pg:15"}, nodeCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Container name should sanitize dots
	depStr := string(out["templates/deployment.yaml"])
	if !strings.Contains(depStr, "name: data-layer-main-db") {
		t.Errorf("container name should sanitize dots, got:\n%s", depStr)
	}
}

func TestHelmEngine_NamespaceFromAnnotation(t *testing.T) {
	t.Parallel()

	engine := &HelmEngine{}
	nc := testNodeCtx()
	nc.Annotations = map[string]string{renderer.AnnotationNamespace: "backend-zone"}
	values := map[string]interface{}{
		"image": "myapp:v1.0.0",
		"ports": []interface{}{"8080:8080"},
	}
	out, err := engine.Render(context.Background(), values, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out["templates/deployment.yaml"]), "namespace: backend-zone") {
		t.Errorf("deployment missing namespace: %s", out["templates/deployment.yaml"])
	}
	if !strings.Contains(string(out["templates/svc.yaml"]), "namespace: backend-zone") {
		t.Errorf("service missing namespace: %s", out["templates/svc.yaml"])
	}
}
