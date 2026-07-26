// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The Phase B.2 workload-secret cross-check validates a synthesized Workload's
// containers.<n>.secrets against the bound owning entity's featuresSchema slots:
// the Component on the base-Recipe path, the implemented Feature on the
// FeatureRecipe path. A declared transparent slot is admitted; an undeclared or
// blackbox slot halts with ErrWorkloadFeaturesSchema. These guard both threading
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

func slotMap(name string, inject []string) types.OrderedMap[types.AttachmentSlot] {
	m := types.OrderedMap[types.AttachmentSlot]{}
	if name != "" {
		m.Set(name, types.AttachmentSlot{Inject: inject})
	}
	return m
}

// --- Base-Recipe path: orchestrator threads Component.FeaturesSchema ---

// renderBaseWithSlot renders a base Recipe whose workload references the
// `password` slot. When fillReferenceable is true the slot is filled by a
// core/referenced-secret credential (its FeatureRecipe publishes provides
// {name, key}) so the workload reference resolves; when false the slot has no
// referenceable fill.
func renderBaseWithSlot(t *testing.T, compSlots types.OrderedMap[types.AttachmentSlot], fillReferenceable bool) error {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/pgprobe", BoundaryPath: "backend"})

	spec := types.IRSpec{Nodes: nodes, ValuesMapping: types.OrderedMap[json.RawMessage]{}}
	if fillReferenceable {
		vals := types.OrderedMap[any]{}
		vals.Set("key", "POSTGRES_PASSWORD")
		vals.Set("origin", "supersecret")
		secrets := types.OrderedMap[types.IRSecretEntry]{}
		secrets.Set("password", types.IRSecretEntry{Type: "core/referenced-secret", Values: vals})
		nodeSecrets := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
		nodeSecrets.Set("db", secrets)
		spec.Secrets = nodeSecrets
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec:     spec,
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				if typeURI == "core/datastore/pgprobe" {
					return types.Component{Spec: types.ComponentSpec{FeaturesSchema: compSlots}}, true
				}
				return types.Component{}, false
			},
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI == "core/referenced-secret" {
					return referencedSecretFeature(), true
				}
				return types.Feature{}, false
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
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft == "core/referenced-secret" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "k8s-secret-from-parameter"},
					Spec:     types.FeatureRecipeSpec{Capability: "helm", Implements: "core/referenced-secret:1.0.0", Provides: fullContractProvides()},
				}, true
			}
			return nil, false
		},
	}
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	return err
}

func TestIntegration_WorkloadSecretSlots_BaseRecipe_AdmitDeclaredTransparent(t *testing.T) {
	t.Parallel()
	// Transparent slot + referenceable fill: the workload reference resolves.
	if err := renderBaseWithSlot(t, slotMap("password", []string{"env"}), true); err != nil {
		t.Fatalf("declared transparent slot with a referenceable fill should ADMIT (was: %v)", err)
	}
}

func TestIntegration_WorkloadSecretSlots_BaseRecipe_HaltNonReferenceable(t *testing.T) {
	t.Parallel()
	// Transparent slot but no referenceable fill: nothing to point at.
	err := renderBaseWithSlot(t, slotMap("password", []string{"env"}), false)
	if !errors.Is(err, validator.ErrSlotNotReferenceable) {
		t.Fatalf("transparent slot without a referenceable fill should HALT with ErrSlotNotReferenceable, got: %v", err)
	}
}

func TestIntegration_WorkloadSecretSlots_BaseRecipe_HaltUndeclared(t *testing.T) {
	t.Parallel()
	err := renderBaseWithSlot(t, slotMap("", nil), false) // no slots declared
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema) {
		t.Fatalf("undeclared slot should HALT with ErrWorkloadFeaturesSchema, got: %v", err)
	}
}

func TestIntegration_WorkloadSecretSlots_BaseRecipe_HaltBlackbox(t *testing.T) {
	t.Parallel()
	err := renderBaseWithSlot(t, slotMap("password", []string{}), false) // blackbox: inject:[]
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema) {
		t.Fatalf("blackbox slot should HALT with ErrWorkloadFeaturesSchema, got: %v", err)
	}
}

// --- FeatureRecipe path: renderFeatureRecipeFiles threads Feature.FeaturesSchema ---

// renderFRWithSlot renders a FeatureRecipe-synthesized workload that references
// the `frpass` slot. When fillReferenceable is true the slot is filled by a
// core/referenced-secret credential so the FR workload reference resolves
// (the FR path renders against a shallow copy of the node context, which
// carries the resolved provides); when false the slot has no referenceable fill.
func renderFRWithSlot(t *testing.T, featSlots types.OrderedMap[types.AttachmentSlot], fillReferenceable bool) error {
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

	spec := types.IRSpec{Nodes: nodes, ValuesMapping: types.OrderedMap[json.RawMessage]{}, FeatureValuesMapping: fvm}
	if fillReferenceable {
		vals := types.OrderedMap[any]{}
		vals.Set("key", "FR_PW")
		vals.Set("origin", "supersecret")
		secrets := types.OrderedMap[types.IRSecretEntry]{}
		secrets.Set("frpass", types.IRSecretEntry{Type: "core/referenced-secret", Values: vals})
		nodeSecrets := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
		nodeSecrets.Set("api", secrets)
		spec.Secrets = nodeSecrets
	}

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec:     spec,
		},
		FeatureOrder:  ir.FeatureOrder{"api": {"frprobe"}},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(string) (types.Component, bool) { return types.Component{}, false },
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI == "core/frprobe:1.0.0" || typeURI == "core/frprobe" {
					return types.Feature{Spec: types.FeatureSpec{FeaturesSchema: featSlots}}, true
				}
				if typeURI == "core/referenced-secret" {
					return referencedSecretFeature(), true
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
						Capability: "helm",
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
			if ft == "core/referenced-secret" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "k8s-secret-from-parameter"},
					Spec:     types.FeatureRecipeSpec{Capability: "helm", Implements: "core/referenced-secret:1.0.0", Provides: fullContractProvides()},
				}, true
			}
			return nil, false
		},
	}
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	return err
}

func TestIntegration_WorkloadSecretSlots_FeatureRecipe_AdmitDeclaredTransparent(t *testing.T) {
	t.Parallel()
	if err := renderFRWithSlot(t, slotMap("frpass", []string{"env"}), true); err != nil {
		t.Fatalf("FR declared transparent slot with a referenceable fill should ADMIT (was: %v)", err)
	}
}

func TestIntegration_WorkloadSecretSlots_FeatureRecipe_HaltNonReferenceable(t *testing.T) {
	t.Parallel()
	err := renderFRWithSlot(t, slotMap("frpass", []string{"env"}), false)
	if !errors.Is(err, validator.ErrSlotNotReferenceable) {
		t.Fatalf("FR transparent slot without a referenceable fill should HALT with ErrSlotNotReferenceable, got: %v", err)
	}
}

func TestIntegration_WorkloadSecretSlots_FeatureRecipe_HaltUndeclared(t *testing.T) {
	t.Parallel()
	err := renderFRWithSlot(t, slotMap("", nil), false) // Feature declares no slots
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema) {
		t.Fatalf("FR undeclared slot should HALT with ErrWorkloadFeaturesSchema, got: %v", err)
	}
}
