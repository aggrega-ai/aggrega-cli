package compose

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestComposeEngine_Render_StrictNetworking(t *testing.T) {
	tTrue := true
	rel := &types.Release{
		Spec: types.ReleaseSpec{
			Policies: &types.ReleasePolicies{StrictNetworking: &tTrue},
		},
	}
	graph := sampleGraph(t)
	eng := &ComposeEngine{}
	out, err := eng.Render(context.Background(), graph, rel, types.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	y := string(out["compose.yaml"])
	if !strings.Contains(y, "aggrega:") || !strings.Contains(y, "driver: bridge") {
		t.Fatalf("expected custom network, got:\n%s", y)
	}
	if strings.Contains(y, "ports:") {
		t.Fatalf("non-gateway services should not publish ports under strict networking, got:\n%s", y)
	}
}

func TestComposeEngine_Render_GatewayPortsUnderStrict(t *testing.T) {
	tTrue := true
	rel := &types.Release{
		Spec: types.ReleaseSpec{
			Policies: &types.ReleasePolicies{StrictNetworking: &tTrue},
		},
	}
	gwVals, _ := json.Marshal(map[string]interface{}{
		"image": "gw:latest",
		"ports": []string{"8080:8080"},
	})
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"gw": {ResolvedType: "ai-gateway", Component: "c/gw"},
			},
			ValuesMapping: map[string]json.RawMessage{
				"gw": gwVals,
			},
		},
	}
	eng := &ComposeEngine{}
	out, err := eng.Render(context.Background(), graph, rel, types.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	y := string(out["compose.yaml"])
	if !strings.Contains(y, "8080:8080") {
		t.Fatalf("expected gateway host ports, got:\n%s", y)
	}
}

func TestComposeEngine_Render_NonStrictPorts(t *testing.T) {
	tFalse := false
	rel := &types.Release{
		Spec: types.ReleaseSpec{
			Policies: &types.ReleasePolicies{StrictNetworking: &tFalse},
		},
	}
	graph := sampleGraph(t)
	eng := &ComposeEngine{}
	out, err := eng.Render(context.Background(), graph, rel, types.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	y := string(out["compose.yaml"])
	if !strings.Contains(y, "3000:3000") {
		t.Fatalf("expected published ports when strict networking off, got:\n%s", y)
	}
}

func TestComposeEngine_Render_InvalidValuesMapping(t *testing.T) {
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{"bad": {}},
			ValuesMapping: map[string]json.RawMessage{
				"bad": json.RawMessage(`not-json`),
			},
		},
	}
	_, err := (&ComposeEngine{}).Render(context.Background(), graph, nil, types.Scope{})
	if err == nil || !strings.Contains(err.Error(), `node "bad"`) {
		t.Fatalf("expected error for bad node, got %v", err)
	}
}

func sampleGraph(t *testing.T) *types.IRGraph {
	t.Helper()
	vals, err := json.Marshal(map[string]interface{}{
		"image":   "app:v1",
		"ports":   []string{"3000:3000"},
		"environment": map[string]interface{}{"FOO": "bar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"web": {ResolvedType: "server", Component: "c/web"},
			},
			ValuesMapping: map[string]json.RawMessage{"web": vals},
		},
	}
}
