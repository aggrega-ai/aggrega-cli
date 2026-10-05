// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EvaluateRecipeAndFeatureRecipeAssertions runs Step 5b: per-instance
// evaluation of every Recipe and FeatureRecipe assertion in the IR graph,
// against the binding established in nodeRecipes and the provides and locals
// values from allProvides and allLocals. Walk order matches Step 4b's
// deterministic-sorted-nodeID.
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
// Locals: this pass reads the node's locals as Pass 1 resolved them rather than
// evaluating the templates again, so an assertion and the artifact it guards read
// one evaluation and cannot disagree. The OrderedMap[string] flattens into a
// map[string]any for the template-render boundary.
//
// Returns nil on pass. Returns a non-nil error wrapping ErrAssertionFailed
// on the first failure. The failure states the file the lookup records for the
// Recipe or FeatureRecipe and the failing assertion's key in it, so a surface that
// places a finding where its author edits it lands on that assertion.
//
// declared answers what each node's entities declare sensitive, so an assertion
// whose failure text could carry a declared value reports without it: a Recipe's
// against the node's own declarations, a FeatureRecipe's against the attachment's
// Feature for the values and infra and the node's for the rest. Its zero value
// declares nothing.
func EvaluateRecipeAndFeatureRecipeAssertions(
	graph *types.IRGraph,
	nodeRecipes map[string]*types.Recipe,
	recipeLookup RecipeLookup,
	allProvides NodeProvides,
	allLocals NodeLocals,
	declared refusalDeclarations,
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

		localsMap := orderedStringMapToAny(allLocals[nodeID])

		recipeCtx := &AssertionRecipeContext{
			Metadata: map[string]any{
				"name":       recipe.Metadata.Name,
				"version":    recipe.Metadata.Version,
				"capability": recipe.Spec.Capability,
			},
			Source: recipe.Spec.Source,
		}

		if recipe.Spec.Assertions.Len() > 0 {
			ctx := AssertionContext{
				Values:   base.Values,
				Infra:    base.Infra,
				Uses:     base.Uses,
				Features: base.Features,
				Metadata: base.Metadata,
				Provides: providesMap,
				Locals:   localsMap,
				Recipe:   recipeCtx,
			}
			if err := walkAssertions("Recipe", recipe.Metadata.Name, nodeID, recipe.Spec.Assertions, ctx,
				declared.node(nodeID).assertionKeys(), recipeLookup.recipeDocument(recipe)); err != nil {
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
					"name":       fr.Metadata.Name,
					"version":    fr.Metadata.Version,
					"capability": fr.Spec.Capability,
				},
				Source: fr.Spec.Source,
			}
			ctx := AssertionContext{
				Values:   entry.Values.TemplateAlphabeticalMapDeep(),
				Infra:    entry.Infra.TemplateAlphabeticalMapDeep(),
				Uses:     base.Uses,
				Features: base.Features,
				Metadata: featureMetadata(base.Metadata, entry, instanceName),
				Provides: providesMap,
				Locals:   localsMap,
				Recipe:   frRecipeCtx,
			}
			if err := walkAssertions("FeatureRecipe", fr.Metadata.Name, nodeID, fr.Spec.Assertions, ctx,
				attachmentAssertionKeys(declared.node(nodeID), declared.attachment(entry.Type)),
				recipeLookup.featureRecipeDocument(fr)); err != nil {
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
