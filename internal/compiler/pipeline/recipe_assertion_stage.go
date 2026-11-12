// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderPluginLookup returns the path a run loads plugin bytes through for its
// provider engines and its pipeline steps: the caller's override where it supplied
// one (a fixture pointing a plugin URI at a built artifact), and otherwise the
// entity-fronted artifact cascade, configured with the run's recorded pins.
func renderPluginLookup(inputs Inputs, catalogLookup resolver.CatalogLookup) pluginpath.Lookup {
	if inputs.PluginPathLookup != nil {
		return inputs.PluginPathLookup
	}
	return buildEntityPluginLookup(inputs, catalogLookup)
}

// providerEngines constructs the provider engines a run reaches its providers
// through: the hybrid dispatch over the catalog's provider bindings, loading each
// guest through the process-wide shared compilation cache. It returns the engine
// factory, the provider-version resolver that shares its plugin resolution, and the
// closer that releases each engine's reused plugin instance, which the caller
// defers once it has finished with the engines.
//
// It is one construction for every run that asks a provider anything, so a
// validation asking a provider the names it gives a node's resources reaches the
// engine a build of the same documents renders through.
func providerEngines(ctx context.Context, catalogLookup resolver.CatalogLookup, pluginLookup pluginpath.Lookup) (renderer.EngineFactory, renderer.ProviderVersionResolver, func(context.Context) error) {
	sharedLoader := wasm.NewCachingLoaderFromContext(ctx, wasm.ExternalInputsFromContext(ctx))
	return enginefactory.NewHybrid(catalogLookup, pluginLookup, sharedLoader)
}

// evaluateRecipeAssertions runs, over the graph the pre-render phases finalised,
// the stages a build runs ahead of its render to reach the Recipe and FeatureRecipe
// assertions (renderer.EvaluateRecipeAssertionStage), with the provider engines a
// build constructs, and releases those engines before it returns. It renders
// nothing and writes nothing.
//
// It returns nil when every assertion passed, and otherwise the failure in the
// shape a build reports it in.
func evaluateRecipeAssertions(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
	graph *types.IRGraph,
) error {
	engines, _, closeEngines := providerEngines(ctx, catalogLookup, renderPluginLookup(inputs, catalogLookup))
	defer func() { _ = closeEngines(ctx) }()
	return renderer.EvaluateRecipeAssertionStage(ctx, graph, recipeLookup, catalogLookup, engines)
}
