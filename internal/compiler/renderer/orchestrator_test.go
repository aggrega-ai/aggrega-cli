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

// testHelmChartFactory returns an EngineFactory that always returns the
// helm-chart engine — used by tests that predate the per-node factory dispatch.
func testHelmChartFactory() renderer.EngineFactory {
	return func(string) (renderer.Engine, error) {
		return &helmchart.HelmEngine{}, nil
	}
}

func testInput() renderer.RendererInput {
	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test-app"},
			Spec: types.IRSpec{
				Nodes: map[string]types.IRNode{
					"api": {
						Component:       "core/server/api-server",
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
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:   "helm",
						Implements: "core/server/api-server",
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

	result, err := renderer.RenderAll(context.Background(), testInput(), testRecipeLookup(), testHelmChartFactory())
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
		Component: "core/database/postgres",
		Labels:    map[string]string{"component.aggrega.ai/type": "stateful-server"},
	}
	input.Graph.Spec.Edges = []types.IREdge{
		{Source: "api", Target: "db", Socket: "databases", Alias: "main-db",
			Inject: map[string]map[string]string{"env": {"DB_HOST": "host"}}},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			switch componentType {
			case "core/server/api-server":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						ValuesMapping: `{"image": "api:latest", "environment": {"DB_HOST": "{{ .context.inject.env.DB_HOST }}"}}`,
					},
				}, true
			case "core/database/postgres":
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

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmChartFactory())
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

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmChartFactory())
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
	input.Graph.Spec.Nodes["a-db"] = types.IRNode{Component: "core/database/postgres"}

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
		result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmChartFactory())
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

func TestRenderAll_FeatureRecipeResourceGeneration(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.FeatureValuesMapping = map[string]map[string]types.IRFeatureEntry{
		"api": {
			"mon": {
				Type:   "core/monitoring",
				Values: map[string]interface{}{"scrape_port": float64(9090), "tier": "prod"},
			},
		},
	}
	input.FeatureOrder = ir.FeatureOrder{"api": []string{"mon"}}

	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:      "helm-chart",
						Implements:    "core/server/api-server",
						ValuesMapping: `{"image": "myapp:latest", "ports": ["8080:8080"]}`,
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/monitoring" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "ops/mon"},
					Spec: types.FeatureRecipeSpec{
						Provider:      "helm-chart",
						Implements:    "core/monitoring",
						Locals:        map[string]string{"scrape_target": `{{ .values.tier }}-{{ .values.scrape_port }}`},
						ValuesMapping: `{"image": "prom/mon:v1", "ports": ["{{ .values.scrape_port }}:9090"]}`,
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

	keys := fileKeys(result.Files)

	// Base Recipe files live under charts/api/ (umbrella nesting)
	foundBaseDep := false
	// FR files live under charts/api/features/mon/
	foundFRDep := false
	frFiles := []string{}
	for _, k := range keys {
		if strings.Contains(k, "boundaries/local/default/charts/api/templates/deployment.yaml") &&
			!strings.Contains(k, "features/") {
			foundBaseDep = true
		}
		if strings.Contains(k, "boundaries/local/default/charts/api/features/mon/") {
			frFiles = append(frFiles, k)
			if strings.Contains(k, "templates/deployment.yaml") {
				foundFRDep = true
			}
		}
	}
	if !foundBaseDep {
		t.Errorf("missing base Recipe deployment under charts/api/, got: %v", keys)
	}
	if !foundFRDep {
		t.Errorf("missing FR deployment under charts/api/features/mon/, got: %v", keys)
	}
	if len(frFiles) < 2 {
		t.Errorf("expected multiple FR files under features/mon/, got %d: %v", len(frFiles), frFiles)
	}

	// FR rendered image should reflect FR valuesMapping
	for _, k := range frFiles {
		if strings.Contains(k, "deployment.yaml") {
			if !strings.Contains(string(result.Files[k]), "prom/mon:v1") {
				t.Errorf("FR deployment missing FR image, got:\n%s", result.Files[k])
			}
		}
	}
}

func TestRenderAll_InjectOnlyFRProducesNoExtraFiles(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.FeatureValuesMapping = map[string]map[string]types.IRFeatureEntry{
		"api": {"dns": {Type: "core/dns-resolver"}},
	}
	input.FeatureOrder = ir.FeatureOrder{"api": []string{"dns"}}

	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec:     types.RecipeSpec{ValuesMapping: `{"image": "app:latest"}`},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/dns-resolver" {
				// Inject-only FR (only injectMapping, no source/valuesMapping) — must NOT generate resources.
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "ops/dns"},
					Spec: types.FeatureRecipeSpec{
						Provider:      "helm-chart",
						InjectMapping: `{"env": {"RESOLVER": "corefile"}}`,
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
	for k := range result.Files {
		if strings.Contains(k, "features/") {
			t.Errorf("inject-only FR must not produce resource files, found: %s", k)
		}
	}
}

func TestRenderAll_CredentialFeatureRecipeRendering(t *testing.T) {
	t.Parallel()

	input := testInput()
	// Attach a credential secret to the api node.
	input.Graph.Spec.Secrets = map[string]map[string]types.IRSecretEntry{
		"api": {
			"root_password": {
				Type: "core/db-password",
				Values: map[string]interface{}{
					"length": float64(32),
				},
				Inject: map[string]map[string]string{
					"env": {"DB_PASSWORD": "ref:root_password"},
				},
			},
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:      "helm-chart",
						Implements:    "core/server/api-server",
						ValuesMapping: `{"image": "myapp:latest", "ports": ["8080:8080"]}`,
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/db-password" {
				// Credential FR: its valuesMapping references .secret_name AND
				// the top-level .inject map populated by buildSecretInject.
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "ops/db-password"},
					Spec: types.FeatureRecipeSpec{
						Provider:      "helm-chart",
						Implements:    "core/db-password",
						ValuesMapping: `{"image": "pw-gen:v1", "environment": {"SECRET_NAME": "{{ .secret_name }}", "BINDING": "{{ .inject.env.DB_PASSWORD }}"}}`,
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

	// Credential FR files live under secrets/<secretName>/ inside charts/<nodeID>/
	var secretDep string
	for k := range result.Files {
		if strings.Contains(k, "boundaries/local/default/charts/api/secrets/root_password/") &&
			strings.Contains(k, "templates/deployment.yaml") {
			secretDep = k
			break
		}
	}
	if secretDep == "" {
		t.Fatalf("missing credential FR deployment under secrets/root_password/, keys: %v", fileKeys(result.Files))
	}

	body := string(result.Files[secretDep])
	// .secret_name propagates into the rendered manifest
	if !strings.Contains(body, "SECRET_NAME") || !strings.Contains(body, "root_password") {
		t.Errorf("expected .secret_name=root_password in rendered manifest, got:\n%s", body)
	}
	// .inject top-level was built from the IR secret entry's inject block
	if !strings.Contains(body, "BINDING") || !strings.Contains(body, "ref:root_password") {
		t.Errorf("expected .inject.env.DB_PASSWORD=ref:root_password in rendered manifest, got:\n%s", body)
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
