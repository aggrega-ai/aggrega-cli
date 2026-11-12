// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// A container naming one environment variable both as a plain variable and as an
// attachments entry delivered as env is refused by the rule declared over the
// Workload, asked inside the render after the Workload is parsed and before any
// provider is handed it. These drive the whole render over a Recipe's Workload and
// over a FeatureRecipe's, with the fills every other Workload check needs in
// place, so the only fault either carries is the pair: an admitting control per
// path renders the same fixture with the two names apart.
//
// Each render is handed a graph presentation, which is what publishes the
// mid-render presentation the rule reads; a render assembled without one is asked
// for a render rather than for a build and does not ask the rule.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// envPairWorkload is a Workload whose main container takes name from the slot
// given and, when pair is set, names it as a plain variable as well.
func envPairWorkload(image, name, slot string, pair bool) string {
	w := "apiVersion: aggrega.ai/v1beta1\nkind: Workload\ncontainers:\n  main:\n    image: " + image + "\n"
	if pair {
		w += "    variables:\n      " + name + ": plain\n"
	} else {
		w += "    variables:\n      APP_MODE: standard\n"
	}
	w += "    attachments:\n      " + name + ":\n        from: " + slot + "\n        as: env\n"
	return w
}

// referencedSecretFill is a referenced-secret attachment whose FeatureRecipe
// publishes the name and the data key a Workload reference reads.
func referencedSecretFill(key string) types.IRFeatureEntry {
	vals := types.OrderedMap[any]{}
	vals.Set("key", key)
	vals.Set("origin", "supersecret")
	return types.IRFeatureEntry{Type: "core/referenced-secret", Values: vals}
}

// referencedSecretRecipe is the FeatureRecipe the fill binds.
func referencedSecretRecipe() *types.FeatureRecipe {
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "k8s-secret-from-parameter"},
		Spec:     types.FeatureRecipeSpec{Capability: "helm", Implements: "core/referenced-secret:1.0.0", Provides: fullContractProvides()},
	}
}

// renderRecipeEnvPair renders one node whose own Recipe's Workload reads the
// declared `password` slot, filled by a referenced secret, and returns the render's
// error.
func renderRecipeEnvPair(t *testing.T, pair bool) error {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/pgprobe", BoundaryPath: "backend"})

	fills := types.OrderedMap[types.IRFeatureEntry]{}
	fills.Set("password", referencedSecretFill("POSTGRES_PASSWORD"))
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("db", fills)

	input := renderer.RendererInput{
		GraphView: derivedKeyGraphFacts{},
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				if typeURI == "core/datastore/pgprobe" {
					return types.Component{Spec: types.ComponentSpec{FeaturesSchema: slotMap("password", []string{"env"})}}, true
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
				Spec: types.RecipeSpec{Workload: types.WorkloadTemplate{
					Inline: envPairWorkload("postgres:16", "POSTGRES_PASSWORD", "password", pair),
				}},
			}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft == "core/referenced-secret" {
				return referencedSecretRecipe(), true
			}
			return nil, false
		},
	}
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	return err
}

// renderFeatureRecipeEnvPair renders one node carrying two attachments: one binding
// a FeatureRecipe whose Workload reads the Feature's own `frpass` slot, and a
// referenced secret filling that slot. It returns the render's error.
func renderFeatureRecipeEnvPair(t *testing.T, pair bool) error {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", BoundaryPath: "backend"})

	attached := types.OrderedMap[types.IRFeatureEntry]{}
	attached.Set("frpass", referencedSecretFill("FR_PW"))
	attached.Set("frprobe", types.IRFeatureEntry{Type: "core/frprobe"})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", attached)

	input := renderer.RendererInput{
		GraphView: derivedKeyGraphFacts{},
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
			},
		},
		FeatureOrder:  ir.FeatureOrder{"api": {"frpass", "frprobe"}},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(string) (types.Component, bool) { return types.Component{}, false },
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI == "core/frprobe:1.0.0" || typeURI == "core/frprobe" {
					return types.Feature{Spec: types.FeatureSpec{FeaturesSchema: slotMap("frpass", []string{"env"})}}, true
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
				Spec: types.RecipeSpec{Workload: types.WorkloadTemplate{
					Inline: "apiVersion: aggrega.ai/v1beta1\nkind: Workload\ncontainers:\n  main:\n    image: api:latest\n",
				}},
			}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			switch ft {
			case "core/frprobe":
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "frprobe-helm"},
					Spec: types.FeatureRecipeSpec{
						Capability: "helm",
						Implements: "core/frprobe:1.0.0",
						Workload: types.WorkloadTemplate{
							Inline: envPairWorkload("helper:1", "FR_PW", "frpass", pair),
						},
					},
				}, true
			case "core/referenced-secret":
				return referencedSecretRecipe(), true
			}
			return nil, false
		},
	}
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	return err
}

// TestIntegration_WorkloadEnvironmentPair_HaltsOnEveryOwner holds each path's
// refusal to the condition and to the words a reader is shown, and each path's
// admitting control to a clean render.
//
// The control is what makes the refusal attributable: the same fixture with the
// two names apart renders, so the slot, the fill and every other Workload check
// pass, and a halt on the pair is the pair's.
func TestIntegration_WorkloadEnvironmentPair_HaltsOnEveryOwner(t *testing.T) {
	t.Parallel()

	for _, path := range []struct {
		name   string
		render func(*testing.T, bool) error
		detail string
	}{
		{
			name:   "a Recipe's own Workload",
			render: renderRecipeEnvPair,
			detail: `("POSTGRES_PASSWORD", container "main")`,
		},
		{
			name:   "the Workload a FeatureRecipe an attachment binds renders",
			render: renderFeatureRecipeEnvPair,
			detail: `("FR_PW", container "main", attachment "frprobe")`,
		},
	} {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()

			if err := path.render(t, false); err != nil {
				t.Fatalf("the admitting control did not render, so a halt on the pair would not be "+
					"attributable to the pair: %v", err)
			}

			err := path.render(t, true)
			if !errors.Is(err, validator.ErrDuplicateContainerEnvironmentVariable()) {
				t.Fatalf("a container naming one variable both ways rendered or halted for another reason: %v", err)
			}
			msg := cerrors.CauseChain(err)
			for _, want := range []string{
				"a container declares one environment variable twice " + path.detail,
				"Rename one of the two, or write them under complementary template conditions",
				".spec.workload",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the halt does not carry %q:\n%s", want, msg)
				}
			}
		})
	}
}
