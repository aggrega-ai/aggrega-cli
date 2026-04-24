// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestCheckProvidesSelfContainment_Clean(t *testing.T) {
	t.Parallel()
	err := CheckProvidesSelfContainment(`{{ .context.instance_id }}-db`, "test-recipe")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestCheckProvidesSelfContainment_ForbiddenInject(t *testing.T) {
	t.Parallel()
	err := CheckProvidesSelfContainment(`{{ .context.inject.env.DB_URL }}`, "bad-recipe")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrProvidesSelfContainment) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestCheckProvidesSelfContainment_ForbiddenUses(t *testing.T) {
	t.Parallel()
	err := CheckProvidesSelfContainment(`{{ .context.uses.my_db.provides.host }}`, "bad-recipe")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrProvidesSelfContainment) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestCheckProvidesSelfContainment_StringLiteralOK(t *testing.T) {
	t.Parallel()
	// The word "inject" in a static string (not inside {{ }}) should not trigger
	err := CheckProvidesSelfContainment(`static string with inject word`, "test-recipe")
	if err != nil {
		t.Errorf("expected no error for static string, got: %v", err)
	}
}

func TestEvaluateAllProvides_Simple(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/database/postgres"})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .context.instance_id }}.svc.cluster.local`)
	provides.Set("service_port", `5432`)
	locals := types.OrderedMap[string]{}
	locals.Set("primary_compute", "postgres")

	recipes := map[string]*types.Recipe{
		"db": {
			Metadata: types.RecipeMetadata{Name: "postgres-recipe"},
			Spec: types.RecipeSpec{
				Provides: provides,
				Locals:   locals,
			},
		},
	}

	result, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dbProv := result["db"]
	if host, _ := dbProv.Get("host"); host != "db.svc.cluster.local" {
		t.Errorf("host: %q", host)
	}
	if sp, _ := dbProv.Get("service_port"); sp != "5432" {
		t.Errorf("service_port: %q", sp)
	}
}

func TestEvaluateAllProvides_SelfContainmentViolation(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}
	provides := types.OrderedMap[string]{}
	provides.Set("url", `{{ .context.inject.env.TARGET }}`)

	recipes := map[string]*types.Recipe{
		"api": {
			Metadata: types.RecipeMetadata{Name: "bad-recipe"},
			Spec: types.RecipeSpec{
				Provides: provides,
			},
		},
	}

	_, err := EvaluateAllProvides(graph, recipes)
	if err == nil {
		t.Fatal("expected error for self-containment violation")
	}
	if !errors.Is(err, ErrProvidesSelfContainment) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestEvaluateAllProvides_NoRecipe(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}

	result, err := EvaluateAllProvides(graph, map[string]*types.Recipe{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	apiProv := result["api"]
	if apiProv.Len() != 0 {
		t.Errorf("expected empty provides for api, got %v", apiProv.Keys())
	}
}

func TestEvaluateAllProvides_SprigFunction(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .context.instance_id | upper }}`)

	recipes := map[string]*types.Recipe{
		"api": {
			Metadata: types.RecipeMetadata{Name: "sprig-recipe"},
			Spec: types.RecipeSpec{
				Provides: provides,
			},
		},
	}

	result, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	apiProv := result["api"]
	host, _ := apiProv.Get("host")
	if host != "API" {
		t.Errorf("expected 'API', got %q", host)
	}
}
