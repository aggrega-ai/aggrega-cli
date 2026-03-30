package helm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestHelmEngine_Render_ChartMetadata(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "my-app"}}
	graph := minimalGraph(t, "web", "server", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, types.Scope{})
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
	tTrue := true
	rel := &types.Release{
		Spec: types.ReleaseSpec{
			Aggregate: "gwchart",
			Policies:  &types.ReleasePolicies{StrictNetworking: &tTrue},
		},
	}
	graph := minimalGraph(t, "ingress", "ai-gateway", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, types.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	svc := string(out["templates/ingress-svc.yaml"])
	if !strings.Contains(svc, "type: LoadBalancer") {
		t.Fatalf("expected LoadBalancer for ai-gateway, got:\n%s", svc)
	}
}

func TestHelmEngine_Render_ResolvedTypeGatewayString(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "x"}}
	graph := minimalGraph(t, "edge", "gateway", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, types.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	svc := string(out["templates/edge-svc.yaml"])
	if !strings.Contains(svc, "type: LoadBalancer") {
		t.Fatalf("expected LoadBalancer for gateway, got:\n%s", svc)
	}
}

func TestHelmEngine_Render_StandardClusterIP(t *testing.T) {
	rel := &types.Release{Spec: types.ReleaseSpec{Aggregate: "app"}}
	graph := minimalGraph(t, "api", "server", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, types.Scope{})
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
	tTrue := true
	rel := &types.Release{
		Spec: types.ReleaseSpec{
			Aggregate: "a",
			Policies:  &types.ReleasePolicies{StrictNetworking: &tTrue},
		},
	}
	graph := minimalGraph(t, "api", "server", map[string]interface{}{})
	out, err := (&HelmEngine{}).Render(context.Background(), graph, rel, types.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	dep := string(out["templates/api-deployment.yaml"])
	if !strings.Contains(dep, "aggrega.ai/strict-networking") {
		t.Fatalf("expected strict-networking annotation when policy true, got:\n%s", dep)
	}
}

func TestHelmEngine_Render_InvalidValuesMapping(t *testing.T) {
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{"bad": {}},
			ValuesMapping: map[string]json.RawMessage{
				"bad": json.RawMessage(`not-json`),
			},
		},
	}
	_, err := (&HelmEngine{}).Render(context.Background(), graph, &types.Release{Spec: types.ReleaseSpec{Aggregate: "x"}}, types.Scope{})
	if err == nil || !strings.Contains(err.Error(), `node "bad"`) {
		t.Fatalf("expected error mentioning node, got %v", err)
	}
}

func minimalGraph(t *testing.T, id, resolvedType string, values map[string]interface{}) *types.IRGraph {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				id: {ResolvedType: resolvedType, Component: "c/" + id},
			},
			ValuesMapping: map[string]json.RawMessage{id: raw},
		},
	}
}
