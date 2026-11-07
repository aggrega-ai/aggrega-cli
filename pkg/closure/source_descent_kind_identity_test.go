// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestARecipeAndAFeatureRecipeOfOneNameAreEachResolvedForTheirOwnInstances holds the
// source descent to the identity it is handed instances by. A Recipe and a
// FeatureRecipe share one reference; a component instance uses the Recipe and a
// feature instance uses the FeatureRecipe, each supplying its own chart version.
// Each source is resolved for its own entity's instance only, so the descent yields
// exactly two artifacts — the Recipe's chart at the component's version, the
// FeatureRecipe's chart at the feature instance's — each referenced by its own entity
// alone. Handing either entity the other's instance would resolve each source a
// second time, for an instance that never uses it.
func TestARecipeAndAFeatureRecipeOfOneNameAreEachResolvedForTheirOwnInstances(t *testing.T) {
	t.Parallel()
	const uri = "helm/shared:1.0.0"
	source := func(chart string) map[string]any {
		return map[string]any{
			"kind":    "helm-oci-chart",
			"url":     "oci://hub.aggrega.ai/charts/" + chart,
			"version": "{{ .infra.chart_version }}",
		}
	}
	loader := &fakeRecipeLoader{
		recipes:  map[string]*types.Recipe{uri: makeRecipe(uri, source("recipe-chart"))},
		features: map[string]*types.FeatureRecipe{uri: makeFeatureRecipe(uri, source("feature-chart"))},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: uri, KindHint: closure.KindRecipe},
		{URI: uri, KindHint: closure.KindFeatureRecipe},
	}}
	used := map[closure.EntityKey][]closure.ComponentSourceInfra{
		{Kind: closure.KindRecipe, URI: uri}: {{Component: "db", Infra: map[string]any{"chart_version": "1.0.0"}}},
		{Kind: closure.KindFeatureRecipe, URI: uri}: {
			{Component: "web.features.tls", Infra: map[string]any{"chart_version": "2.0.0"}},
		},
	}

	got, err := closure.DescendRecipeSources(used, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}

	type reached struct{ url, version, entity, component string }
	var all []reached
	for _, sa := range got {
		for _, ref := range sa.ReferencedBy {
			all = append(all, reached{url: sa.URL, version: sa.PinnedVersion, entity: ref.Entity, component: ref.Component})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].url < all[j].url || (all[i].url == all[j].url && all[i].version < all[j].version)
	})
	want := []reached{
		{url: "oci://hub.aggrega.ai/charts/feature-chart", version: "2.0.0", entity: closure.KindFeatureRecipe, component: "web.features.tls"},
		{url: "oci://hub.aggrega.ai/charts/recipe-chart", version: "1.0.0", entity: closure.KindRecipe, component: "db"},
	}
	if len(got) != len(want) || len(all) != len(want) {
		t.Fatalf("the descent yielded %d artifact(s) reached %d time(s): %+v; want exactly %+v", len(got), len(all), all, want)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Errorf("artifact %d: got %+v; want %+v", i, all[i], want[i])
		}
	}
}
