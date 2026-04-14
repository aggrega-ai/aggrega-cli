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
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"gw": {
					Labels:          map[string]string{types.ComponentTypeLabel: "ai-gateway"},
					Component:       "c/gw",
					ResolvedVersion: "1.0.0",
				},
			},
			ValuesMapping: map[string]json.RawMessage{
				"gw": gwVals,
			},
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
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"gw": {
					Labels:          map[string]string{types.ComponentTypeLabel: "ai-gateway"},
					Component:       "kong",
					ResolvedVersion: "1.0.0",
				},
			},
			ValuesMapping: map[string]json.RawMessage{"gw": vals},
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
	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"bad": {Labels: map[string]string{}, Component: "c/bad", ResolvedVersion: "1.0.0"},
			},
			ValuesMapping: map[string]json.RawMessage{
				"bad": json.RawMessage(`not-json`),
			},
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
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"web": {
					Labels:          map[string]string{types.ComponentTypeLabel: "server"},
					Component:       "c/web",
					ResolvedVersion: "1.0.0",
				},
			},
			ValuesMapping: map[string]json.RawMessage{"web": vals},
		},
	}
}
