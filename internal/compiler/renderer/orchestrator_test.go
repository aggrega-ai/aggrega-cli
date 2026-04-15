// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func testInput() renderer.RendererInput {
	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test-app"},
			Spec: types.IRSpec{
				Nodes: map[string]types.IRNode{
					"api": {
						Component:       "core/api-server",
						Labels:          map[string]string{"component.aggrega.ai/type": "server"},
						ResolvedVersion: "1.0.0",
						BoundaryPath:    "local/default",
					},
				},
				Edges:          nil,
				ValuesMapping:  map[string]json.RawMessage{"api": json.RawMessage(`{"port":8080}`)},
				InfraMapping:   map[string]json.RawMessage{},
				FeatureMarkers: nil,
				Provenance:     types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
}

func testRecipeLookup() renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			if componentType == "core/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:   "helm",
						Implements: "core/api-server",
						Provides: map[string]string{
							"host":         `{{ .context.instance_id }}.svc.cluster.local`,
							"service_port": `8080`,
						},
						Locals:        map[string]string{"primary_compute": "api"},
						ValuesMapping: `{"image": "myapp:latest", "ports": ["8080:8080"], "environment": {"PORT": "8080"}}`,
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			return nil, false
		},
	}
}

func TestRenderAll_SimpleNode(t *testing.T) {
	t.Parallel()

	result, err := renderer.RenderAll(context.Background(), testInput(), testRecipeLookup(), &helmchart.HelmEngine{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Files) == 0 {
		t.Fatal("expected files")
	}

	foundDep := false
	for path := range result.Files {
		if strings.Contains(path, "boundaries/local/default/") && strings.Contains(path, "deployment.yaml") {
			foundDep = true
		}
	}
	if !foundDep {
		t.Errorf("expected boundary-prefixed deployment, got keys: %v", fileKeys(result.Files))
	}
}

func TestRenderAll_ProvidesResolved(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.Nodes["db"] = types.IRNode{
		Component: "core/postgres",
		Labels:    map[string]string{"component.aggrega.ai/type": "stateful-server"},
	}
	input.Graph.Spec.Edges = []types.IREdge{
		{Source: "api", Target: "db", Socket: "databases", Alias: "main-db",
			Inject: map[string]map[string]string{"env": {"DB_HOST": "host"}}},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			switch componentType {
			case "core/api-server":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						ValuesMapping: `{"image": "api:latest", "environment": {"DB_HOST": "{{ .context.inject.env.DB_HOST }}"}}`,
					},
				}, true
			case "core/postgres":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "pg-recipe"},
					Spec: types.RecipeSpec{
						Provides: map[string]string{
							"host": `{{ .context.instance_id }}.svc.cluster.local`,
						},
						ValuesMapping: `{"image": "postgres:15"}`,
					},
				}, true
			default:
				return nil, false
			}
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, &helmchart.HelmEngine{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for path, content := range result.Files {
		if strings.Contains(path, "api") && strings.Contains(path, "deployment") {
			if strings.Contains(string(content), "db.svc.cluster.local") {
				return
			}
		}
	}
	t.Error("expected DB_HOST=db.svc.cluster.local in api deployment")
}

func TestRenderAll_PhaseCWarning(t *testing.T) {
	t.Parallel()

	input := testInput()
	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "pipeline-recipe"},
				Spec: types.RecipeSpec{
					ValuesMapping: `{"image": "app:latest"}`,
					Pipeline: []types.PipelineStep{
						{ID: "step1", Plugin: "envoy-sidecar.wasm", Function: "inject"},
					},
				},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, &helmchart.HelmEngine{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected Phase C pipeline warning")
	}
	if !strings.Contains(result.Warnings[0], "not executed") {
		t.Errorf("warning: %s", result.Warnings[0])
	}
}

func TestRenderAll_Deterministic(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.Nodes["z-worker"] = types.IRNode{Component: "core/worker"}
	input.Graph.Spec.Nodes["a-db"] = types.IRNode{Component: "core/postgres"}

	lookup := renderer.RecipeLookup{
		Recipe: func(string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "generic"},
				Spec:     types.RecipeSpec{ValuesMapping: `{"image": "app:latest"}`},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	var firstKeys []string
	for i := 0; i < 10; i++ {
		result, err := renderer.RenderAll(context.Background(), input, lookup, &helmchart.HelmEngine{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		keys := fileKeys(result.Files)
		if i == 0 {
			firstKeys = keys
		} else {
			for j, k := range keys {
				if k != firstKeys[j] {
					t.Errorf("run %d: file key changed at %d: %q vs %q", i, j, k, firstKeys[j])
				}
			}
		}
	}
}

func fileKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
