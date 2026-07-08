// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestDescend_ShortFormRecipeURIRecognizedViaKindHint is the payoff proof:
// a short-form Recipe reference (no "/recipe/" path segment, the shape a
// Manifest components-mapping value commonly takes) is descended for its
// source because the recorded kind hint says it is a recipe — lexical
// classification alone would skip it.
func TestDescend_ShortFormRecipeURIRecognizedViaKindHint(t *testing.T) {
	t.Parallel()
	uri := "mariadb-bitnami:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			uri: makeRecipe(uri, "oci://registry-1.docker.io/bitnamicharts/mariadb"),
		},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("artifacts = %d; want 1 (short-form recipe recognized via kind hint)", len(got))
	}
}

// TestDescend_ShortFormWithoutHintSkipped is the control: the SAME short-form
// URI with no kind hint is skipped, because lexical classification finds no
// "/recipe/" segment. This pins that the hint — not the URI string — is what
// makes the short-form reference descend.
func TestDescend_ShortFormWithoutHintSkipped(t *testing.T) {
	t.Parallel()
	uri := "mariadb-bitnami:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			uri: makeRecipe(uri, "oci://registry-1.docker.io/bitnamicharts/mariadb"),
		},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}} // no KindHint

	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("artifacts = %d; want 0 (lexical classify skips a short-form URI with no hint)", len(got))
	}
}

// TestDescend_ShortFormFeatureRecipeViaKindHint covers the feature-recipe
// position: a features-mapping value descends as a FeatureRecipe via its hint.
func TestDescend_ShortFormFeatureRecipeViaKindHint(t *testing.T) {
	t.Parallel()
	uri := "k8s-secret-from-parameter:1.0.0"
	loader := &fakeRecipeLoader{
		features: map[string]*types.FeatureRecipe{
			uri: {
				APIVersion: "aggrega.ai/v1alpha1",
				Kind:       "FeatureRecipe",
				Metadata:   types.FeatureRecipeMetadata{Name: uri, Version: "1.0.0"},
				Spec:       types.FeatureRecipeSpec{Target: "helm", Source: "oci://hub.aggrega.ai/charts/k8s-secret"},
			},
		},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "feature_recipe"}}}

	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("artifacts = %d; want 1 (short-form feature-recipe recognized via kind hint)", len(got))
	}
}

// TestDescend_PathFormNoHintUnchanged pins no regression: a path-form Recipe
// URI containing "/recipe/" still descends via lexical fallback when no hint
// is present.
func TestDescend_PathFormNoHintUnchanged(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			uri: makeRecipe(uri, "oci://hub.aggrega.ai/charts/mariadb"),
		},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}} // no KindHint

	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("artifacts = %d; want 1 (path-form URI still recognized via lexical fallback)", len(got))
	}
}

// TestComputeClosure_ManifestMappingValuesCarryKindHint proves the provenance
// wiring end-to-end: the Manifest walker records the recipe / feature_recipe
// kind on mapping VALUES (by their position) and the component / feature kind on
// mapping KEYS, so a registry-backed pull can bind every entity to its OCI
// location. Recipe / feature_recipe hints are authoritative over component /
// feature when a bare URI is shared across kinds.
func TestComputeClosure_ManifestMappingValuesCarryKindHint(t *testing.T) {
	t.Parallel()
	man := loadManifest(t, `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: m
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/datastore/mariadb:1.0.0: mariadb-bitnami:1.0.0
    features:
      type:
        app/literal-credential:1.0.0: literal-credential-helm:1.0.0
`)
	rel := &types.Release{}
	cfg := workspaceconfig.Default()
	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{
			"core/datastore/mariadb:1.0.0":  resolutionOK("1.0.0", "sha256:a", "tier1-local"),
			"mariadb-bitnami:1.0.0":         resolutionOK("1.0.0", "sha256:b", "tier1-local"),
			"app/literal-credential:1.0.0":  resolutionOK("1.0.0", "sha256:c", "tier1-local"),
			"literal-credential-helm:1.0.0": resolutionOK("1.0.0", "sha256:d", "tier1-local"),
		},
		plugins: map[string]closure.Resolution{},
	}

	c, err := closure.ComputeClosure(rel, man, nil, cfg, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	kindByURI := map[string]string{}
	for _, e := range c.Entities {
		kindByURI[e.URI] = e.KindHint
	}
	cases := map[string]string{
		"mariadb-bitnami:1.0.0":         "recipe",         // components-mapping value
		"literal-credential-helm:1.0.0": "feature_recipe", // features-mapping value
		"core/datastore/mariadb:1.0.0":  "component",      // mapping key — a Component
		"app/literal-credential:1.0.0":  "feature",        // mapping key — a Feature
	}
	for uri, want := range cases {
		if _, present := kindByURI[uri]; !present {
			t.Fatalf("entity %q absent from closure; entities=%v", uri, kindByURI)
		}
		if got := kindByURI[uri]; got != want {
			t.Errorf("KindHint for %q = %q; want %q", uri, got, want)
		}
	}
}
