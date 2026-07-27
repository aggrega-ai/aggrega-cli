// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// step5bGraph builds a single-node graph with values + a Recipe binding.
func step5bGraph(t *testing.T, nodeID string, values, infra types.OrderedMap[any]) *types.IRGraph {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(nodeID, types.IRNode{Component: "core/server"})

	vm := types.OrderedMap[json.RawMessage]{}
	if values.Len() > 0 {
		b, _ := json.Marshal(values.TemplateAlphabeticalMapDeep())
		vm.Set(nodeID, b)
	}
	im := types.OrderedMap[json.RawMessage]{}
	if infra.Len() > 0 {
		b, _ := json.Marshal(infra.TemplateAlphabeticalMapDeep())
		im.Set(nodeID, b)
	}
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			ValuesMapping:        vm,
			InfraMapping:         im,
			FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
		},
	}
}

// TestStep5b_RecipeAssertionPasses pins the happy path.
func TestStep5b_RecipeAssertionPasses(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t, "replicas", 3), omAny(t))
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm-server", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Assertions: omString(t,
				"replicas-ok",
				`{{ if lt (.Values.replicas | int) 1 }}{{ fail "..." }}{{ end }}`,
			),
		},
	}
	err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		map[string]*types.Recipe{"web": recipe},
		RecipeLookup{},
		NodeProvides{},
		NodeLocals{},
	)
	if err != nil {
		t.Errorf("expected nil; got %v", err)
	}
}

// TestStep5b_RecipeAssertionFailsHaltsBuild pins the fail path.
func TestStep5b_RecipeAssertionFailsHaltsBuild(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t, "replicas", 0), omAny(t))
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm-server", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Assertions: omString(t,
				"replicas-positive",
				`{{ if lt (.Values.replicas | int) 1 }}{{ fail "chart requires replicas >= 1" }}{{ end }}`,
			),
		},
	}
	err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		map[string]*types.Recipe{"web": recipe},
		RecipeLookup{},
		NodeProvides{},
		NodeLocals{},
	)
	if err == nil || !errors.Is(err, ErrAssertionFailed) {
		t.Fatalf("expected ErrAssertionFailed; got %v", err)
	}
	for _, want := range []string{"helm-server", "web", "replicas-positive", "chart requires replicas >= 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error; got %q", want, err.Error())
		}
	}
}

// TestStep5b_ProvidesAvailableInContext pins the Step 5b-only field
// availability: assertions can reference .Provides.
func TestStep5b_ProvidesAvailableInContext(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t), omAny(t))
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm-server", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Assertions: omString(t,
				"host-set",
				`{{ if eq .Provides.host "" }}{{ fail "host must be set" }}{{ end }}`,
			),
		},
	}
	provides := NodeProvides{}
	pm := types.OrderedMap[string]{}
	pm.Set("host", "db.svc.cluster.local")
	provides["web"] = pm
	if err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		map[string]*types.Recipe{"web": recipe},
		RecipeLookup{},
		provides,
		NodeLocals{},
	); err != nil {
		t.Errorf("expected nil (host populated via .Provides); got %v", err)
	}
}

// TestStep5b_RecipeMetadataAvailableInContext pins .Recipe access.
func TestStep5b_RecipeMetadataAvailableInContext(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t), omAny(t))
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm-server", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Assertions: omString(t,
				"recipe-name-set",
				`{{ if eq .Recipe.Metadata.name "" }}{{ fail "name missing" }}{{ end }}`,
			),
		},
	}
	if err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		map[string]*types.Recipe{"web": recipe},
		RecipeLookup{},
		NodeProvides{},
		NodeLocals{},
	); err != nil {
		t.Errorf("expected nil; got %v", err)
	}
}

// TestStep5b_LocalsAvailableInContext pins .Locals access through the whole
// chain the values actually travel: the Pass-1 preamble resolves the Recipe's
// Locals against per-node values + infra, and this pass reads that one
// evaluation into AssertionContext.Locals rather than re-evaluating the
// templates.
func TestStep5b_LocalsAvailableInContext(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t, "name", "myapp"), omAny(t))
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm-server", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Locals: omString(t,
				"derived_name", `app-{{ .values.name }}`,
			),
			Assertions: omString(t,
				"locals-derived",
				`{{ if ne .Locals.derived_name "app-myapp" }}{{ fail "locals not propagated" }}{{ end }}`,
			),
		},
	}
	nodeRecipes := map[string]*types.Recipe{"web": recipe}
	allProvides, allLocals, err := EvaluateAllProvides(graph, nodeRecipes)
	if err != nil {
		t.Fatalf("Pass 1: %v", err)
	}
	webLocals := allLocals["web"]
	if got, _ := webLocals.Get("derived_name"); got != "app-myapp" {
		t.Fatalf("Pass-1 preamble locals: got %q, want %q", got, "app-myapp")
	}
	if err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		nodeRecipes,
		RecipeLookup{},
		allProvides,
		allLocals,
	); err != nil {
		t.Errorf("expected nil (locals populated); got %v", err)
	}
}

// TestStep5b_MultiInstanceEvaluatesEachInstance pins per-instance
// semantics for Recipe assertions: N instances bound to the same Recipe
// produce N evaluations; failure provenance names the failing instance.
func TestStep5b_MultiInstanceEvaluatesEachInstance(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("web-1", types.IRNode{Component: "core/server"})
	nodes.Set("web-2", types.IRNode{Component: "core/server"})
	nodes.Set("web-3", types.IRNode{Component: "core/server"})

	vm := types.OrderedMap[json.RawMessage]{}
	for id, replicas := range map[string]int{"web-1": 3, "web-2": 0, "web-3": 5} {
		b, _ := json.Marshal(map[string]any{"replicas": replicas})
		vm.Set(id, b)
	}

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes:                nodes,
			ValuesMapping:        vm,
			InfraMapping:         types.OrderedMap[json.RawMessage]{},
			FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
		},
	}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm-server"},
		Spec: types.RecipeSpec{
			Assertions: omString(t,
				"replicas-positive",
				`{{ if lt (.Values.replicas | int) 1 }}{{ fail "chart requires replicas >= 1" }}{{ end }}`,
			),
		},
	}
	nodeRecipes := map[string]*types.Recipe{
		"web-1": recipe,
		"web-2": recipe,
		"web-3": recipe,
	}
	err := EvaluateRecipeAndFeatureRecipeAssertions(graph, nodeRecipes, RecipeLookup{}, NodeProvides{}, NodeLocals{})
	if err == nil || !strings.Contains(err.Error(), "web-2") {
		t.Errorf("expected error naming web-2; got %v", err)
	}
}

// TestStep5b_RecipeMetadataProviderAvailable pins symmetry with FeatureRecipe:
// Recipe assertions can reference .Recipe.Metadata.capability just like
// FeatureRecipe assertions.
func TestStep5b_RecipeMetadataProviderAvailable(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t), omAny(t))
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "helm-server", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "helm",
			Assertions: omString(t,
				"provider-must-be-helm",
				`{{ if ne .Recipe.Metadata.capability "helm" }}{{ fail "provider must be helm" }}{{ end }}`,
			),
		},
	}
	if err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		map[string]*types.Recipe{"web": recipe},
		RecipeLookup{},
		NodeProvides{},
		NodeLocals{},
	); err != nil {
		t.Errorf("expected nil (provider populated); got %v", err)
	}
}

// TestStep5b_FeatureRecipeMetadataProviderAvailable pins the
// FeatureRecipe-side sibling of the Recipe-side
// TestStep5b_RecipeMetadataProviderAvailable test above. Verifies the
// FeatureRecipe assertion evaluation surfaces `.Recipe.Metadata.capability`
// from the FeatureRecipe's Spec.Provider — exercising the FR code-path
// at assertions_step5b.go:109-127 specifically. Pins the FR-side
// symmetry that was previously implicit: code-side symmetry was
// already resolved; the test-side was asymmetric — only the Recipe
// side was pinned.
func TestStep5b_FeatureRecipeMetadataProviderAvailable(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t), omAny(t))
	fvm := types.OrderedMap[types.IRFeatureEntry]{}
	fvm.Set("audit-on-web", types.IRFeatureEntry{
		Type:   "ops/audit",
		Values: omAny(t),
	})
	graph.Spec.FeatureValuesMapping.Set("web", fvm)

	recipe := &types.Recipe{Metadata: types.RecipeMetadata{Name: "helm-server"}}
	fr := types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "audit-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Assertions: omString(t,
				"provider-must-be-helm",
				`{{ if ne .Recipe.Metadata.capability "helm" }}{{ fail "provider must be helm" }}{{ end }}`,
			),
		},
	}
	rl := RecipeLookup{
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return &fr, true },
	}
	if err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		map[string]*types.Recipe{"web": recipe},
		rl,
		NodeProvides{},
		NodeLocals{},
	); err != nil {
		t.Errorf("expected nil (FeatureRecipe provider populated); got %v", err)
	}
}

// TestStep5b_FeatureRecipeAssertionFailsHaltsBuild pins FR-side eval.
func TestStep5b_FeatureRecipeAssertionFailsHaltsBuild(t *testing.T) {
	t.Parallel()
	graph := step5bGraph(t, "web", omAny(t), omAny(t))
	fvm := types.OrderedMap[types.IRFeatureEntry]{}
	fvm.Set("audit-on-web", types.IRFeatureEntry{
		Type:   "ops/audit",
		Values: omAny(t, "level", "off"),
	})
	graph.Spec.FeatureValuesMapping.Set("web", fvm)

	recipe := &types.Recipe{Metadata: types.RecipeMetadata{Name: "helm-server"}}
	fr := types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "audit-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Assertions: omString(t,
				"audit-must-be-on",
				`{{ if eq .Values.level "off" }}{{ fail "audit chart requires level != off" }}{{ end }}`,
			),
		},
	}
	rl := RecipeLookup{
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return &fr, true },
	}
	err := EvaluateRecipeAndFeatureRecipeAssertions(
		graph,
		map[string]*types.Recipe{"web": recipe},
		rl,
		NodeProvides{},
		NodeLocals{},
	)
	if err == nil || !errors.Is(err, ErrAssertionFailed) {
		t.Fatalf("expected ErrAssertionFailed; got %v", err)
	}
	if !strings.Contains(err.Error(), "audit-fr") {
		t.Errorf("expected audit-fr in error; got %q", err.Error())
	}
}
