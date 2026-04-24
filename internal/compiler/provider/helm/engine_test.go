// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestHelmEngine_Render_ChartMetadata(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "my-app", Boundaries: types.OrderedMap[types.Boundary]{}}}
	graph := minimalGraph(t, "web", "server", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, "")
	if err != nil {
		t.Fatal(err)
	}
	chart := string(out["Chart.yaml"])
	if !strings.Contains(chart, "name: my-app") {
		t.Fatalf("Chart.yaml name: got:\n%s", chart)
	}
	if !strings.Contains(chart, "version: 0.1.0") {
		t.Fatalf("Chart.yaml version: got:\n%s", chart)
	}
}

func TestHelmEngine_Render_GatewayLoadBalancer(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "gwchart", Boundaries: types.OrderedMap[types.Boundary]{}}}
	graph := minimalGraph(t, "ingress", "ai-gateway", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, "")
	if err != nil {
		t.Fatal(err)
	}
	svc := string(out["templates/ingress-svc.yaml"])
	if !strings.Contains(svc, "type: LoadBalancer") {
		t.Fatalf("expected LoadBalancer for ai-gateway, got:\n%s", svc)
	}
}

func TestHelmEngine_Render_ResolvedTypeGatewayString(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "x", Boundaries: types.OrderedMap[types.Boundary]{}}}
	graph := minimalGraph(t, "edge", "gateway", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, "")
	if err != nil {
		t.Fatal(err)
	}
	svc := string(out["templates/edge-svc.yaml"])
	if !strings.Contains(svc, "type: LoadBalancer") {
		t.Fatalf("expected LoadBalancer for gateway, got:\n%s", svc)
	}
}

func TestHelmEngine_Render_StandardClusterIP(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "app", Boundaries: types.OrderedMap[types.Boundary]{}}}
	graph := minimalGraph(t, "api", "server", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, "")
	if err != nil {
		t.Fatal(err)
	}
	svc := string(out["templates/api-svc.yaml"])
	if !strings.Contains(svc, "type: ClusterIP") {
		t.Fatalf("expected ClusterIP for non-gateway, got:\n%s", svc)
	}
	if strings.Contains(svc, "LoadBalancer") {
		t.Fatalf("unexpected LoadBalancer in:\n%s", svc)
	}
}

func TestHelmEngine_Render_StrictNetworkingAnnotation(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "a", Boundaries: types.OrderedMap[types.Boundary]{}}}
	graph := minimalGraph(t, "api", "server", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, "")
	if err != nil {
		t.Fatal(err)
	}
	dep := string(out["templates/api-deployment.yaml"])
	if !strings.Contains(dep, "aggrega.ai/strict-networking") {
		t.Fatalf("expected strict-networking annotation, got:\n%s", dep)
	}
}

func TestHelmEngine_Render_ValuesImageEnvironmentAndInjectedEnv(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "kong-app", Boundaries: types.OrderedMap[types.Boundary]{}}}
	vals := map[string]interface{}{
		"image": "kong:3.4",
		"environment": map[string]interface{}{
			"KONG_DATABASE": "off",
		},
		"_aggrega_injected_env_PROMPT_GUARD": "true",
	}
	graph := minimalGraph(t, "proxy", "server", vals)
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, "")
	if err != nil {
		t.Fatal(err)
	}
	dep := string(out["templates/proxy-deployment.yaml"])
	if !strings.Contains(dep, "kong:3.4") {
		t.Fatalf("expected image kong:3.4 in:\n%s", dep)
	}
	if !strings.Contains(dep, "name: KONG_DATABASE") || !strings.Contains(dep, "off") {
		t.Fatalf("expected KONG_DATABASE env in:\n%s", dep)
	}
	if !strings.Contains(dep, "name: PROMPT_GUARD") || !(strings.Contains(dep, `value: "true"`) || strings.Contains(dep, "value: true")) {
		t.Fatalf("expected PROMPT_GUARD injected env in:\n%s", dep)
	}
}

func TestHelmEngine_Render_InvalidValuesMapping(t *testing.T) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("bad", types.IRNode{Labels: types.OrderedMap[string]{}, Component: "c/bad", ResolvedVersion: "1.0.0"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("bad", json.RawMessage(`not-json`))
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:         nodes,
			ValuesMapping: valuesMapping,
		},
	}
	_, err := (&HelmEngine{}).Render(context.Background(), graph, &types.Release{Spec: types.ReleaseSpec{Aggregate: "x", Boundaries: types.OrderedMap[types.Boundary]{}}}, "")
	if err == nil || !strings.Contains(err.Error(), `node "bad"`) {
		t.Fatalf("expected error mentioning node, got %v", err)
	}
}

func minimalGraph(t *testing.T, id, ontologyType string, values map[string]interface{}) *types.IRGraph {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, ontologyType)
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(id, types.IRNode{Labels: labels, Component: "c/" + id, ResolvedVersion: "1.0.0"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set(id, raw)
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,

			ValuesMapping: valuesMapping,
		},
	}
}
