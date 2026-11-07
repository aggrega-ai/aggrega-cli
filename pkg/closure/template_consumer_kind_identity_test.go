// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"context"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// recipesOnlySource is a LibrarySource holding Recipes and FeatureRecipes by
// reference and nothing else.
type recipesOnlySource struct {
	recipes        map[string]types.Recipe
	featureRecipes map[string]types.FeatureRecipe
}

func (recipesOnlySource) ClosureComponent(context.Context, ResolvedRef) (types.Component, bool) {
	return types.Component{}, false
}
func (recipesOnlySource) ClosureFeature(context.Context, ResolvedRef) (types.Feature, bool) {
	return types.Feature{}, false
}
func (recipesOnlySource) SchemaLibrary(string) (types.SchemaLibrary, bool) {
	return types.SchemaLibrary{}, false
}
func (s recipesOnlySource) RecipeByURI(uri string) (types.Recipe, bool) {
	r, ok := s.recipes[uri]
	return r, ok
}
func (s recipesOnlySource) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	fr, ok := s.featureRecipes[uri]
	return fr, ok
}
func (recipesOnlySource) MappingLibrary(string) (types.MappingLibrary, bool) {
	return types.MappingLibrary{}, false
}
func (recipesOnlySource) WorkloadLibrary(string) (types.WorkloadLibrary, bool) {
	return types.WorkloadLibrary{}, false
}
func (recipesOnlySource) ArtifactLibrary(string) (types.ArtifactLibrary, bool) {
	return types.ArtifactLibrary{}, false
}
func (recipesOnlySource) HoldResolvedLibrary(LibraryAxis, ResolvedLibrary) {}

// TestEachEntityContributesTemplateConsumersAsTheKindItWasCollectedAs holds the
// template-library consumer scan to the identity the walk collected each entity
// under. A Recipe and a FeatureRecipe share one reference, and a Component shares
// its reference with a Recipe the closure does not reach. The Recipe and the
// FeatureRecipe each contribute their own consumers; the Component contributes
// none, because the Recipe sharing its name is not in the closure. A scan reading
// every entity as a Recipe first would take the FeatureRecipe for the Recipe and
// drop its consumers, and would scan the out-of-closure Recipe for the Component.
func TestEachEntityContributesTemplateConsumersAsTheKindItWasCollectedAs(t *testing.T) {
	t.Parallel()
	src := recipesOnlySource{
		recipes: map[string]types.Recipe{
			"helm/shared:1.0.0": {},
			"core/web:1.0.0":    {},
		},
		featureRecipes: map[string]types.FeatureRecipe{"helm/shared:1.0.0": {}},
	}
	cl := &Closure{Entities: []ResolvedRef{
		{URI: "core/web:1.0.0", KindHint: KindComponent},
		{URI: "helm/shared:1.0.0", KindHint: KindFeatureRecipe},
		{URI: "helm/shared:1.0.0", KindHint: KindRecipe},
	}}
	got := collectTemplateConsumers(cl, src,
		func(uri string, _ types.Recipe) []TemplateConsumer {
			return []TemplateConsumer{{URI: uri, Reference: "read as a Recipe"}}
		},
		func(uri string, _ types.FeatureRecipe) []TemplateConsumer {
			return []TemplateConsumer{{URI: uri, Reference: "read as a FeatureRecipe"}}
		})
	want := []TemplateConsumer{
		{URI: "helm/shared:1.0.0", Kind: KindRecipe, Reference: "read as a Recipe"},
		{URI: "helm/shared:1.0.0", Kind: KindFeatureRecipe, Reference: "read as a FeatureRecipe"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("template consumers = %+v; want %+v", got, want)
	}
}
