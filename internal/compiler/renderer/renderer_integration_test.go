// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestIntegration_ProvidesSelfContainmentViolation(t *testing.T) {
	t.Parallel()

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec: types.IRSpec{
				Nodes: map[string]types.IRNode{
					"api": {Component: "core/server/api-server"},
				},
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "bad-recipe"},
				Spec: types.RecipeSpec{
					Provides:      map[string]string{"host": `{{ .context.inject.env.TARGET }}`},
					ValuesMapping: `{"image": "app:latest"}`,
				},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmChartFactory())
	if err == nil {
		t.Fatal("expected error for provides self-containment violation")
	}
	if !errors.Is(err, renderer.ErrProvidesSelfContainment) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestIntegration_InjectMappingDeepMerge(t *testing.T) {
	t.Parallel()

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec: types.IRSpec{
				Nodes: map[string]types.IRNode{
					"api": {Component: "core/server/api-server", BoundaryPath: "local"},
				},
				Edges: []types.IREdge{
					{Source: "api", Target: "db", Socket: "databases", Alias: "main-db",
						Inject: map[string]map[string]string{"env": {"DB_URL": "host"}}},
				},
				ValuesMapping: map[string]json.RawMessage{},
				FeatureValuesMapping: map[string]map[string]types.IRFeatureEntry{
					"api": {"dns-resolver": {Type: "core/dns-resolver"}},
				},
			},
		},
		FeatureOrder: ir.FeatureOrder{"api": {"dns-resolver"}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
	// Add db node for provides
	input.Graph.Spec.Nodes["db"] = types.IRNode{Component: "core/database/postgres"}

	lookup := renderer.RecipeLookup{
		Recipe: func(ct string) (*types.Recipe, bool) {
			switch ct {
			case "core/server/api-server":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						ValuesMapping: `{"image": "api:latest", "environment": {"DB_URL": "{{ .context.inject.env.DB_URL }}"}}`,
					},
				}, true
			case "core/database/postgres":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "pg-recipe"},
					Spec: types.RecipeSpec{
						Provides:      map[string]string{"host": "original-host.svc"},
						ValuesMapping: `{"image": "pg:15"}`,
					},
				}, true
			default:
				return nil, false
			}
		},
		FeatureRecipe: func(ft string) (*types.FeatureRecipe, bool) {
			if ft == "core/dns-resolver" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "dns-resolver-recipe"},
					Spec: types.FeatureRecipeSpec{
						InjectMapping: `{"env": {"DB_URL": "rewritten-by-feature.svc.cluster.local"}}`,
					},
				}, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmChartFactory())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The injectMapping should have rewritten DB_URL
	for path, content := range result.Files {
		if strings.Contains(path, "api") && strings.Contains(path, "deployment") {
			if strings.Contains(string(content), "rewritten-by-feature.svc.cluster.local") {
				return // success — Phase A deep-merge worked
			}
			t.Errorf("expected rewritten DB_URL in api deployment, got:\n%s", string(content))
			return
		}
	}
	t.Error("no api deployment found")
}

func TestIntegration_AggreraLabelsPresent(t *testing.T) {
	t.Parallel()

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "labeled-app"},
			Spec: types.IRSpec{
				Nodes: map[string]types.IRNode{
					"api": {
						Component:       "core/server/api-server",
						Labels:          map[string]string{"component.aggrega.ai/type": "server"},
						BoundaryPath:    "prod/eks",
						ResolvedVersion: "1.0.0",
					},
				},
				Provenance: types.IRProvenance{CompilerVersion: "0.2.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "labeled-app"}},
			Release:   &types.Release{Metadata: types.ReleaseMetadata{Name: "prod", Environment: "production"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "r"},
				Spec:     types.RecipeSpec{ValuesMapping: `{"image": "app:latest"}`},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmChartFactory())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for path, content := range result.Files {
		if strings.Contains(path, "deployment") {
			s := string(content)
			checks := []string{
				"aggrega.ai/component-id: api",
				"aggrega.ai/boundary-path: prod/eks",
				"aggrega.ai/compiler-version: 0.2.0",
				"aggrega.ai/aggregate-name: labeled-app",
				"aggrega.ai/environment: production",
			}
			for _, check := range checks {
				if !strings.Contains(s, check) {
					t.Errorf("deployment missing label: %s\n%s", check, s)
				}
			}
			return
		}
	}
	t.Error("no deployment found")
}

// TestIntegration_ValuesAndInfraFlow proves the correctness fix from WI-3:
// .values from the Aggregate and .infra from the Release boundary must flow
// through the valuesMapping template to reach the rendered K8s manifest.
func TestIntegration_ValuesAndInfraFlow(t *testing.T) {
	t.Parallel()

	valuesRaw, _ := json.Marshal(map[string]interface{}{"port": 8080})
	infraRaw, _ := json.Marshal(map[string]interface{}{"replicas": 3})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "flow-test"},
			Spec: types.IRSpec{
				Nodes: map[string]types.IRNode{
					"api": {
						Component:    "core/server/api-server",
						Labels:       map[string]string{"component.aggrega.ai/type": "server"},
						BoundaryPath: "local",
					},
				},
				ValuesMapping: map[string]json.RawMessage{"api": valuesRaw},
				InfraMapping:  map[string]json.RawMessage{"api": infraRaw},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "flow-test"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec: types.RecipeSpec{
					// Template reads .values.port and .infra.replicas — the critical flow
					ValuesMapping: `{"image": "api:latest", "ports": ["{{ .values.port }}:{{ .values.port }}"], "replicas": {{ .infra.replicas }}}`,
				},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmChartFactory())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for path, content := range result.Files {
		if strings.Contains(path, "api") && strings.Contains(path, "deployment") {
			s := string(content)
			if !strings.Contains(s, "containerPort: 8080") {
				t.Errorf("expected containerPort: 8080 (from .values.port), got:\n%s", s)
			}
			if !strings.Contains(s, "replicas: 3") {
				t.Errorf("expected replicas: 3 (from .infra.replicas), got:\n%s", s)
			}
			return
		}
	}
	t.Error("no api deployment found")
}
