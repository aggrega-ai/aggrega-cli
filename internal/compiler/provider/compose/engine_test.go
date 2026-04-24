// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestComposeEngine_Render_StrictNetworking(t *testing.T) {
	graph := sampleGraph(t)
	eng := &ComposeEngine{}
	out, err := eng.Render(context.Background(), graph, nil, "")
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
	gwVals, _ := json.Marshal(map[string]interface{}{})
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "ai-gateway")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gw", types.IRNode{Labels: labels, Component: "c/gw", ResolvedVersion: "1.0.0"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("gw", gwVals)
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,

			ValuesMapping: valuesMapping,
		},
	}
	eng := &ComposeEngine{}
	out, err := eng.Render(context.Background(), graph, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	y := string(out["compose.yaml"])
	if !strings.Contains(y, "8080:8080") {
		t.Fatalf("expected gateway host ports, got:\n%s", y)
	}
}

func TestComposeEngine_Render_StrictNonGatewayNoPorts(t *testing.T) {
	// In v2, strict networking is always enabled. Non-gateway services do not publish ports.
	graph := sampleGraph(t)
	eng := &ComposeEngine{}
	out, err := eng.Render(context.Background(), graph, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	y := string(out["compose.yaml"])
	if strings.Contains(y, "ports:") {
		t.Fatalf("non-gateway should not publish ports under strict networking, got:\n%s", y)
	}
}

func TestComposeEngine_Render_ValuesImageEnvironmentAndInjectedEnv(t *testing.T) {
	vals, err := json.Marshal(map[string]interface{}{
		"image": "kong:3.4",
		"environment": map[string]interface{}{
			"KONG_DATABASE": "off",
		},
		"_aggrega_injected_env_PROMPT_GUARD": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "ai-gateway")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("gw", types.IRNode{Labels: labels, Component: "kong", ResolvedVersion: "1.0.0"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("gw", vals)
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,

			ValuesMapping: valuesMapping,
		},
	}
	out, err := (&ComposeEngine{}).Render(context.Background(), graph, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	y := string(out["compose.yaml"])
	if !strings.Contains(y, "kong:3.4") {
		t.Fatalf("expected image kong:3.4 in:\n%s", y)
	}
	if !strings.Contains(y, "KONG_DATABASE") || !strings.Contains(y, "off") {
		t.Fatalf("expected KONG_DATABASE=off in:\n%s", y)
	}
	if !strings.Contains(y, "PROMPT_GUARD") || !strings.Contains(y, "true") {
		t.Fatalf("expected injected PROMPT_GUARD in:\n%s", y)
	}
}

func TestComposeEngine_Render_InvalidValuesMapping(t *testing.T) {
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
	_, err := (&ComposeEngine{}).Render(context.Background(), graph, nil, "")
	if err == nil || !strings.Contains(err.Error(), `node "bad"`) {
		t.Fatalf("expected error for bad node, got %v", err)
	}
}

func sampleGraph(t *testing.T) *types.IRGraph {
	t.Helper()
	vals, err := json.Marshal(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("web", types.IRNode{Labels: labels, Component: "c/web", ResolvedVersion: "1.0.0"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("web", vals)
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,

			ValuesMapping: valuesMapping,
		},
	}
}
