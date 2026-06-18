// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The Phase B.2 workload-secret cross-check validates a synthesized Workload's
// containers.<n>.secrets against the bound owning entity's secretsSchema slots:
// the Component on the base-Recipe path, the implemented Feature on the
// FeatureRecipe path. A declared transparent slot is admitted; an undeclared or
// blackbox slot halts with ErrWorkloadSecretsSchema. These guard both threading
// sites against regressing to an empty slot allowlist.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

func slotMap(name string, inject []string) types.OrderedMap[types.SecretSlot] {
	m := types.OrderedMap[types.SecretSlot]{}
	if name != "" {
		m.Set(name, types.SecretSlot{Inject: inject})
	}
	return m
}

// --- Base-Recipe path: orchestrator threads Component.SecretsSchema ---

func renderBaseWithSlot(t *testing.T, compSlots types.OrderedMap[types.SecretSlot]) error {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/pgprobe", BoundaryPath: "backend"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec:     types.IRSpec{Nodes: nodes, ValuesMapping: types.OrderedMap[json.RawMessage]{}},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				if typeURI == "core/datastore/pgprobe" {
					return types.Component{Spec: types.ComponentSpec{SecretsSchema: compSlots}}, true
				}
				return types.Component{}, false
			},
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "pgprobe-helm"},
				Spec: types.RecipeSpec{Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: postgres:16
    secrets:
      POSTGRES_PASSWORD:
        from: password
        as: env
`}},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	return err
}

func TestIntegration_WorkloadSecretSlots_BaseRecipe_AdmitDeclaredTransparent(t *testing.T) {
	t.Parallel()
	if err := renderBaseWithSlot(t, slotMap("password", []string{"env"})); err != nil {
		t.Fatalf("declared transparent slot should ADMIT (was: %v)", err)
	}
}

func TestIntegration_WorkloadSecretSlots_BaseRecipe_HaltUndeclared(t *testing.T) {
	t.Parallel()
	err := renderBaseWithSlot(t, slotMap("", nil)) // no slots declared
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("undeclared slot should HALT with ErrWorkloadSecretsSchema, got: %v", err)
	}
}

func TestIntegration_WorkloadSecretSlots_BaseRecipe_HaltBlackbox(t *testing.T) {
	t.Parallel()
	err := renderBaseWithSlot(t, slotMap("password", []string{})) // blackbox: inject:[]
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("blackbox slot should HALT with ErrWorkloadSecretsSchema, got: %v", err)
	}
}

// --- FeatureRecipe path: renderFeatureRecipeFiles threads Feature.SecretsSchema ---

func renderFRWithSlot(t *testing.T, featSlots types.OrderedMap[types.SecretSlot]) error {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", BoundaryPath: "backend"})

	fvm := func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("frprobe", types.IRFeatureEntry{Type: "core/frprobe"})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		return outer
	}()

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec:     types.IRSpec{Nodes: nodes, ValuesMapping: types.OrderedMap[json.RawMessage]{}, FeatureValuesMapping: fvm},
		},
		FeatureOrder:  ir.FeatureOrder{"api": {"frprobe"}},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(string) (types.Component, bool) { return types.Component{}, false },
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI == "core/frprobe:1.0.0" || typeURI == "core/frprobe" {
					return types.Feature{Spec: types.FeatureSpec{SecretsSchema: featSlots}}, true
				}
				return types.Feature{}, false
			},
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec:     types.RecipeSpec{Workload: types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: api:latest\n"}},
			}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft == "core/frprobe" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "frprobe-helm"},
					Spec: types.FeatureRecipeSpec{
						Provider:   "helm",
						Implements: "core/frprobe:1.0.0",
						Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: helper:1
    secrets:
      FR_PW:
        from: frpass
        as: env
`},
					},
				}, true
			}
			return nil, false
		},
	}
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	return err
}

func TestIntegration_WorkloadSecretSlots_FeatureRecipe_AdmitDeclaredTransparent(t *testing.T) {
	t.Parallel()
	if err := renderFRWithSlot(t, slotMap("frpass", []string{"env"})); err != nil {
		t.Fatalf("FR declared transparent slot should ADMIT (was: %v)", err)
	}
}

func TestIntegration_WorkloadSecretSlots_FeatureRecipe_HaltUndeclared(t *testing.T) {
	t.Parallel()
	err := renderFRWithSlot(t, slotMap("", nil)) // Feature declares no slots
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("FR undeclared slot should HALT with ErrWorkloadSecretsSchema, got: %v", err)
	}
}
