// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EvaluateRecipeAndFeatureRecipeAssertions runs Step 5b: per-instance
// evaluation of every Recipe and FeatureRecipe assertion in the IR graph,
// against the binding established in nodeRecipes and the provides values
// from allProvides. Walk order matches Step 4b's deterministic-sorted-nodeID.
//
// Per node with a bound Recipe:
//
//  1. If recipe.Spec.Assertions has entries, walk them in author order.
//     Each assertion sees the AssertionContext with Provides, Locals,
//     and Recipe populated on top of the Step 4b base shape. First fail
//     halts.
//  2. For each Feature attached to the node, look up the FeatureRecipe
//     via recipeLookup.FeatureRecipe; if featurerecipe.Spec.Assertions
//     has entries, evaluate similarly with the FeatureRecipe's metadata
//     and source pointer on the Recipe field of the context. First fail halts.
//
// Locals evaluation: Step 5b evaluates each Recipe's Locals against the
// per-node restricted LocalsContext (instance_id + values + infra +
// provides) before constructing the assertion's AssertionContext. The
// resulting Locals OrderedMap[string] flattens into a map[string]any
// for the template-render boundary.
//
// Returns nil on pass. Returns a non-nil error wrapping ErrAssertionFailed
// on the first failure.
func EvaluateRecipeAndFeatureRecipeAssertions(
	graph *types.IRGraph,
	nodeRecipes map[string]*types.Recipe,
	recipeLookup RecipeLookup,
	allProvides NodeProvides,
) error {
	if graph == nil {
		return nil
	}
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		recipe := nodeRecipes[nodeID]
		if recipe == nil {
			continue
		}
		base := ExtractNodeBaseContext(nodeID, graph)
		providesForNode := allProvides[nodeID]
		providesMap := orderedStringMapToAny(providesForNode)

		// Step 5b evaluates Recipe Locals inline here so they're available to
		// assertion templates via AssertionContext.Locals. The orchestrator's
		// main render loop re-evaluates the same Locals during rendering;
		// double evaluation is accepted today because Locals templates are
		// pure (same input -> same output). A future refactor may
		// consolidate via a pre-loop pass into a shared map[nodeID]locals.
		valuesOM := orderedAnyFromMap(base.Values)
		infraOM := orderedAnyFromMap(base.Infra)
		locals, lerr := EvaluateRecipeLocals(recipe, nodeID, valuesOM, infraOM, providesForNode)
		if lerr != nil {
			return fmt.Errorf("Recipe %q instance %q locals evaluation: %w", recipe.Metadata.Name, nodeID, lerr)
		}
		localsMap := orderedStringMapToAny(locals)

		recipeCtx := &AssertionRecipeContext{
			Metadata: map[string]any{
				"name":     recipe.Metadata.Name,
				"version":  recipe.Metadata.Version,
				"provider": recipe.Spec.Target,
			},
			Source: recipe.Spec.Source,
		}

		if recipe.Spec.Assertions.Len() > 0 {
			ctx := AssertionContext{
				Values:   base.Values,
				Infra:    base.Infra,
				Uses:     base.Uses,
				Secrets:  base.Secrets,
				Metadata: base.Metadata,
				Provides: providesMap,
				Locals:   localsMap,
				Recipe:   recipeCtx,
			}
			if err := walkAssertions("Recipe", recipe.Metadata.Name, nodeID, recipe.Spec.Assertions, ctx); err != nil {
				return err
			}
		}

		if recipeLookup.FeatureRecipe == nil {
			continue
		}
		fvm, hasFeatures := graph.Spec.FeatureValuesMapping.Get(nodeID)
		if !hasFeatures || fvm.Len() == 0 {
			continue
		}
		for instanceName, entry := range fvm.All() {
			fr, found := recipeLookup.FeatureRecipe(instanceName, entry.Type)
			if !found || fr.Spec.Assertions.Len() == 0 {
				continue
			}
			frRecipeCtx := &AssertionRecipeContext{
				Metadata: map[string]any{
					"name":     fr.Metadata.Name,
					"version":  fr.Metadata.Version,
					"provider": fr.Spec.Target,
				},
				Source: fr.Spec.Source,
			}
			ctx := AssertionContext{
				Values:   entry.Values.TemplateAlphabeticalMapDeep(),
				Infra:    entry.Infra.TemplateAlphabeticalMapDeep(),
				Uses:     base.Uses,
				Secrets:  base.Secrets,
				Metadata: featureMetadata(base.Metadata, entry, instanceName),
				Provides: providesMap,
				Locals:   localsMap,
				Recipe:   frRecipeCtx,
			}
			if err := walkAssertions("FeatureRecipe", fr.Metadata.Name, nodeID, fr.Spec.Assertions, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// orderedStringMapToAny converts an OrderedMap[string] into a plain
// map[string]any for template-render-boundary consumption. Returns an
// empty map (not nil) so missingkey=error halts on missing keys rather
// than panicking on nil-map indexing.
func orderedStringMapToAny(m types.OrderedMap[string]) map[string]any {
	out := make(map[string]any, m.Len())
	for k, v := range m.All() {
		out[k] = v
	}
	return out
}

// orderedAnyFromMap rebuilds an OrderedMap[any] from a plain map[string]any
// for callers that need OrderedMap as input (e.g., EvaluateRecipeLocals).
// Key iteration order is alphabetical (the input plain map has no author
// order to preserve).
func orderedAnyFromMap(m map[string]any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Set(k, m[k])
	}
	return out
}
