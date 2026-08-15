// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RecipeBinding resolves a component instance to the Recipe bound to it, by the
// instance's name and its catalog type — the same two strings the render stage
// binds by, so a caller supplying the build's own binding gets the Recipe that
// will actually render. A caller with no binding to offer supplies nil.
type RecipeBinding func(instanceName, componentType string) (*types.Recipe, bool)

// LintProvidesNotDeclared reports each `provides` key a bound Recipe publishes
// that its Component does not declare in spec.schema.
//
// Publishing more than the Component declares is legal and stays legal: the
// provides contract is subset-required and superset-permitted, so a Recipe that
// covers its Component's contract and emits extra keys besides is a Recipe that
// fulfils its contract. This is advisory and never reaches a build's verdict.
//
// What it exists to name is a consequence that is otherwise invisible from
// either side. A consumer reading across a `uses` edge sees the target's
// DECLARED contract, so an undeclared key is not delivered — and the consuming
// template that reads it renders an empty value rather than failing. The
// condition is stated here, at its cause, on the Recipe that published the key,
// because that is where it is nameable: at the consumer the key is a template
// reference the compiler cannot attribute to a target, and a diagnostic that
// guessed would be worse than none. So the two are deliberately not co-located —
// the empty value appears at the consumer, the explanation here — and both
// appear in the same build output.
//
// The fix is one of two, and which one is right is the author's call: declare
// the key on the Component when consumers are meant to read it, or leave it
// undeclared when it is the Recipe's own working state — in which case `locals`
// is where such state belongs.
//
// Scope and coverage. Component instances come from the resolved Aggregate's
// subsystem tree, so instances nested in subsystems are covered; components
// contributed by a sub-aggregate are not visible at this stage and are not.
// Instance names here are the same strings the render stage binds recipes by,
// so a per-instance Manifest recipe override is honoured rather than averaged
// away.
//
// One finding per (Recipe, Component, key), not per instance: the condition is a
// property of that pair, and repeating it once per instance that happens to use
// it would bury it. The instance named in the locator is the first in walk
// order that exhibits it.
//
// Walk order. Instances are walked in the order collectComponentInstances
// returns (sorted by instance name) and each Recipe's undeclared keys are
// reported in [sort.Strings] order, so repeated runs over identical input emit
// an identical advisory sequence.
//
// Returns nil when agg is nil, when catalog.Component is nil, or when recipeFor
// is nil — none of those paths has both halves of the comparison. A Component
// absent from the catalog view is skipped for the same reason: no declaration
// was read, which is not the same as a declaration of nothing.
func LintProvidesNotDeclared(na types.NormalizedAggregate, catalog CatalogLookup, recipeFor RecipeBinding) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil || recipeFor == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	reported := map[string]bool{}
	var out []*ResolverError

	for _, p := range collectComponentInstances(na) {
		comp := catalogComponentFor(p.inst.Type, catalog)
		if comp == nil {
			continue
		}
		recipe, bound := recipeFor(p.name, p.inst.Type)
		if !bound || recipe == nil {
			continue
		}

		// The declared side is read through the same accessor the inject guard and
		// the render-time fulfilment check read, so all three agree on what a
		// Component declares. The published side is the Recipe's authored provides
		// keys, which is exactly the key set provides evaluation produces — one
		// resolved entry per authored key — so what this names and what a consumer
		// loses are the same set rather than two sets that agree today.
		declared := ComponentProvidesKeys(*comp, catalog.SchemaLibraries)

		var undeclared []string
		for key := range recipe.Spec.Provides.All() {
			if declared[key] {
				continue
			}
			dedupe := fmt.Sprintf("%s\x00%s\x00%s\x00%s", recipe.Metadata.Name, recipe.Metadata.Version, p.inst.Type, key)
			if reported[dedupe] {
				continue
			}
			reported[dedupe] = true
			undeclared = append(undeclared, key)
		}
		sort.Strings(undeclared)

		base := componentFieldPath(p.subsystemPath, p.name, p.synthesized)
		for _, key := range undeclared {
			out = append(out, &ResolverError{
				Entity: entity,
				Field:  fmt.Sprintf("%s.type", base),
				Message: fmt.Sprintf(
					"Recipe %q publishes provides key %q, which Component %q does not declare in its schema.provides contract — a consumer across a uses edge reads the declared contract, so this key is not delivered and a template reading it renders empty; declare the key on the Component if consumers are meant to read it, or move it to the Recipe's locals if it is the Recipe's own working state",
					recipe.Metadata.Name, key, p.inst.Type,
				),
				Err: ErrProvidesKeyNotDeclared(),
			})
		}
	}

	return out
}
