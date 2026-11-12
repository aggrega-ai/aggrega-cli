// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"sort"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// recipeAssertionStage carries, for one graph, what the stages ahead of the Recipe
// and FeatureRecipe assertions produce: the Recipe bound to each node, the name each
// node's provider reports for the resources it renders, what each node's entities
// declare sensitive, and each node's resolved locals and provides.
//
// Each stage is a method, and each method fills the fields the next one reads, so
// the order is the order the data flows in. The render runs them with the rules it
// asks between them; a surface that renders nothing runs them back to back through
// [EvaluateRecipeAssertionStage]. Either way every stage has one implementation,
// so what an assertion is evaluated against cannot differ between the two.
type recipeAssertionStage struct {
	graph *types.IRGraph

	// nodeIDs is the graph's node identities in sorted order — the order every
	// walk over this graph visits nodes in.
	nodeIDs []string

	// recipes is the Recipe bound to each node, by node identity. A node no Recipe
	// binds to is absent.
	recipes map[string]*types.Recipe

	renderedNames NodeRenderedNames
	readers       *declarationReaders
	declarations  refusalDeclarations
	provides      NodeProvides
	locals        NodeLocals
}

// bindRecipes binds each node of graph to its Recipe through recipeLookup. A node
// the lookup resolves no Recipe for is left unbound, and a lookup with no Recipe
// resolution binds nothing.
func bindRecipes(graph *types.IRGraph, recipeLookup RecipeLookup) *recipeAssertionStage {
	s := &recipeAssertionStage{graph: graph, recipes: make(map[string]*types.Recipe)}
	s.nodeIDs = graph.Spec.Nodes.Keys()
	sort.Strings(s.nodeIDs)
	for _, nodeID := range s.nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		if recipeLookup.Recipe != nil {
			if r, found := recipeLookup.Recipe(nodeID, node.Component); found {
				s.recipes[nodeID] = r
			}
		}
	}
	return s
}

// askRenderedNames asks each bound node's own provider what name it will give the
// resources it renders for that node (CollectRenderedNames). It is the one thing a
// provider is asked ahead of the assertions, because a Recipe's provides may read
// the answer.
func (s *recipeAssertionStage) askRenderedNames(ctx context.Context, engineFactory EngineFactory) error {
	names, err := CollectRenderedNames(ctx, s.graph, s.recipes, engineFactory)
	if err != nil {
		return err
	}
	s.renderedNames = names
	return nil
}

// readDeclarations reads what each node's entities declare sensitive through one
// memoised reader over lookup, before any template is evaluated: every template
// evaluated afterwards reports a failure without a value its author declared
// sensitive.
func (s *recipeAssertionStage) readDeclarations(lookup resolver.CatalogLookup) {
	s.readers = newDeclarationReaders(lookup)
	s.declarations = readRefusalDeclarations(s.graph, s.recipes, s.readers)
}

// evaluateLocalsAndProvides resolves every node's locals and then its provides
// (EvaluateAllProvides), once for the graph, so the assertions and every later
// reader consume one evaluation.
func (s *recipeAssertionStage) evaluateLocalsAndProvides() error {
	provides, locals, err := EvaluateAllProvides(s.graph, s.recipes, s.renderedNames, s.declarations)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "locals and provides evaluation")
	}
	s.provides, s.locals = provides, locals
	return nil
}

// evaluateAssertions evaluates every Recipe and FeatureRecipe assertion against
// the bound node (EvaluateRecipeAndFeatureRecipeAssertions) and gives the first
// failure the shape every surface reports it in.
func (s *recipeAssertionStage) evaluateAssertions(recipeLookup RecipeLookup) error {
	if err := EvaluateRecipeAndFeatureRecipeAssertions(s.graph, s.recipes, recipeLookup, s.provides, s.locals, s.declarations); err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "Recipe/FeatureRecipe assertions")
	}
	return nil
}

// EvaluateRecipeAssertionStage runs, over a finalised graph, the stages a build runs
// before rendering that end in the Recipe and FeatureRecipe assertions: it binds
// each node to its Recipe, asks each bound node's provider the name it gives the
// node's resources, reads what each node's entities declare sensitive, resolves
// every node's locals and provides, and evaluates the assertions against them. It
// renders nothing and writes nothing.
//
// It returns nil when every assertion passed. A failing assertion is returned in
// the shape a build reports it in, so the condition and the sentence are the same
// wherever the failure is read; a locals or provides template that cannot be
// evaluated is returned as a build returns it, since the assertions cannot run
// without what it resolves.
//
// engineFactory is how a provider is reached for the names. A nil factory asks no
// provider, and every node is then read as one whose provider reports no name.
//
// # Why the render does not call this
//
// The render runs the same stages, through the same methods, with rules of its own
// asked between them — over what every plugin reports about itself, between the
// names and the declarations, and over what each provides key resolved to, before
// the assertions. This entry runs the stages and not those rules, so a surface
// reading it is told what the assertions decide, not everything a render would
// refuse first.
func EvaluateRecipeAssertionStage(
	ctx context.Context,
	graph *types.IRGraph,
	recipeLookup RecipeLookup,
	catalogLookup resolver.CatalogLookup,
	engineFactory EngineFactory,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if graph == nil {
		return nil
	}
	s := bindRecipes(graph, recipeLookup)
	if err := s.askRenderedNames(ctx, engineFactory); err != nil {
		return err
	}
	s.readDeclarations(catalogLookup)
	if err := s.evaluateLocalsAndProvides(); err != nil {
		return err
	}
	return s.evaluateAssertions(recipeLookup)
}
