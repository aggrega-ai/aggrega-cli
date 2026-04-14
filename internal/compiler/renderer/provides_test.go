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

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"db": {Component: "core/postgres"},
			},
		},
	}

	recipes := map[string]*types.Recipe{
		"db": {
			Metadata: types.RecipeMetadata{Name: "postgres-recipe"},
			Spec: types.RecipeSpec{
				Provides: map[string]string{
					"host":         `{{ .Context.InstanceID }}.svc.cluster.local`,
					"service_port": `5432`,
				},
				Outputs: map[string]string{"primary_compute": "postgres"},
			},
		},
	}

	result, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["db"]["host"] != "db.svc.cluster.local" {
		t.Errorf("host: %q", result["db"]["host"])
	}
	if result["db"]["service_port"] != "5432" {
		t.Errorf("service_port: %q", result["db"]["service_port"])
	}
}

func TestEvaluateAllProvides_SelfContainmentViolation(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"api": {},
			},
		},
	}

	recipes := map[string]*types.Recipe{
		"api": {
			Metadata: types.RecipeMetadata{Name: "bad-recipe"},
			Spec: types.RecipeSpec{
				Provides: map[string]string{
					"url": `{{ .context.inject.env.TARGET }}`,
				},
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

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"api": {},
			},
		},
	}

	result, err := EvaluateAllProvides(graph, map[string]*types.Recipe{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result["api"]) != 0 {
		t.Errorf("expected empty provides for api, got %v", result["api"])
	}
}

func TestEvaluateAllProvides_SprigFunction(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"api": {},
			},
		},
	}

	recipes := map[string]*types.Recipe{
		"api": {
			Metadata: types.RecipeMetadata{Name: "sprig-recipe"},
			Spec: types.RecipeSpec{
				Provides: map[string]string{
					"host": `{{ .Context.InstanceID | upper }}`,
				},
			},
		},
	}

	result, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["api"]["host"] != "API" {
		t.Errorf("expected 'API', got %q", result["api"]["host"])
	}
}
