// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// providerDescentRelease names a manifest so the closure carries a Recipe entity
// (via the components-mapping value) whose provider the descent reads.
const providerDescentRelease = `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: prod
  version: 1.0.0
spec:
  aggregate: hub.aggrega.ai/aggregate/example/app:1.0.0
  manifest: hub.aggrega.ai/manifest/example/app:1.0.0
`

const providerDescentManifest = `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        hub.aggrega.ai/component/core/svc:1.0.0: hub.aggrega.ai/recipe/example/svc:1.0.0
`

// recipeWithProvider builds a Recipe fixture declaring spec.provider, for the
// descent's RecipeLoader.
func recipeWithProvider(t *testing.T, name, provider string) *types.Recipe {
	t.Helper()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  provider: " + provider + "\n  implements: core/svc\n"
	var r types.Recipe
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal recipe: %v", err)
	}
	return &r
}

// runProviderDescent computes the base closure for the manifest-bearing Release,
// resolving the Recipe + sub-aggregate URIs, then runs the provider descent against
// the supplied loader and lookup, returning the augmented closure (or the error).
func runProviderDescent(t *testing.T, lookup *fakeLookup, loader closure.RecipeLoader) (closure.Closure, error) {
	t.Helper()
	rel := loadRelease(t, providerDescentRelease)
	man := loadManifest(t, providerDescentManifest)
	cl, err := closure.ComputeClosure(rel, man, nil, workspaceconfig.Config{}, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	derr := closure.DescendRecipeProviders(&cl, loader, lookup)
	return cl, derr
}

const (
	recipeURI        = "hub.aggrega.ai/recipe/example/svc:1.0.0"
	registryProvider = "compose"
	localProvider    = "helm"
)

// providerDescentLookup resolves the manifest/aggregate/component/recipe URIs the
// fixture reaches, plus the two providers: `compose` registry-resolved (tier-3) and
// `helm` Tier-1-local.
func providerDescentLookup() *fakeLookup {
	ents := map[string]closure.Resolution{
		"hub.aggrega.ai/aggregate/example/app:1.0.0": resolutionOK("1.0.0", "sha256:agg", "tier-3-pull"),
		"hub.aggrega.ai/manifest/example/app:1.0.0":  resolutionOK("1.0.0", "sha256:man", "tier-3-pull"),
		"hub.aggrega.ai/component/core/svc:1.0.0":     resolutionOK("1.0.0", "sha256:svc", "tier-3-pull"),
		recipeURI:                                     resolutionOK("1.0.0", "sha256:recipe", "tier-3-pull"),
		registryProvider:                              resolutionOK("1.0.0", "sha256:compose", "tier-3-pull"),
		localProvider:                                 resolutionOK("1.0.0", "sha256:helm", "tier-1-catalog"),
	}
	return &fakeLookup{entities: ents}
}

func entityByURI(cl closure.Closure, uri string) (closure.ResolvedRef, bool) {
	for _, e := range cl.Entities {
		if e.URI == uri {
			return e, true
		}
	}
	return closure.ResolvedRef{}, false
}

// TestProviderDescent_RegistryResolvedPinned proves a registry-resolved provider is
// walked from the recipe's spec.provider, resolved through the catalog-entity
// cascade, and pinned into the closure with its version + digest + provider hint.
func TestProviderDescent_RegistryResolvedPinned(t *testing.T) {
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{
		recipeURI: recipeWithProvider(t, "svc", registryProvider),
	}}
	cl, err := runProviderDescent(t, providerDescentLookup(), loader)
	if err != nil {
		t.Fatalf("provider descent: %v", err)
	}

	got, ok := entityByURI(cl, registryProvider)
	if !ok {
		t.Fatalf("registry-resolved provider %q not pinned into the closure", registryProvider)
	}
	if got.ResolvedVersion != "1.0.0" || got.Digest != "sha256:compose" {
		t.Errorf("provider pin = {%q, %q}; want {1.0.0, sha256:compose}", got.ResolvedVersion, got.Digest)
	}
	if got.KindHint != "provider" {
		t.Errorf("provider KindHint = %q; want provider", got.KindHint)
	}
	if len(got.ReferencedFrom) != 1 || got.ReferencedFrom[0] != recipeURI {
		t.Errorf("provider ReferencedFrom = %v; want [%s]", got.ReferencedFrom, recipeURI)
	}
}

// TestProviderDescent_Tier1LocalNotPinned proves a Tier-1-local provider is NOT
// pinned into the closure — it resolves at build through the local catalog walk, so
// pinning it would diverge a local-authoring build from its pre-change behaviour.
func TestProviderDescent_Tier1LocalNotPinned(t *testing.T) {
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{
		recipeURI: recipeWithProvider(t, "svc", localProvider),
	}}
	cl, err := runProviderDescent(t, providerDescentLookup(), loader)
	if err != nil {
		t.Fatalf("provider descent: %v", err)
	}
	if _, ok := entityByURI(cl, localProvider); ok {
		t.Fatalf("Tier-1-local provider %q must not be pinned; it resolves locally at build", localProvider)
	}
}

// TestProviderDescent_Deterministic descends the same Release twice and asserts the
// pinned entity set is byte-identical (Invariant 8): the provider pin sorts
// deterministically among the other entity rows regardless of descent order.
func TestProviderDescent_Deterministic(t *testing.T) {
	build := func() closure.Closure {
		loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{
			recipeURI: recipeWithProvider(t, "svc", registryProvider),
		}}
		cl, err := runProviderDescent(t, providerDescentLookup(), loader)
		if err != nil {
			t.Fatalf("provider descent: %v", err)
		}
		return cl
	}
	first := build()
	second := build()
	if !reflect.DeepEqual(first.Entities, second.Entities) {
		t.Fatalf("provider descent is non-deterministic:\n first=%#v\nsecond=%#v", first.Entities, second.Entities)
	}
}

// TestProviderDescent_NilLoaderNoop confirms a nil loader leaves the closure
// untouched (the caller wired no recipe-body access).
func TestProviderDescent_NilLoaderNoop(t *testing.T) {
	cl, err := runProviderDescent(t, providerDescentLookup(), nil)
	if err != nil {
		t.Fatalf("nil-loader descent: %v", err)
	}
	if _, ok := entityByURI(cl, registryProvider); ok {
		t.Fatalf("nil-loader descent must not pin a provider")
	}
}
