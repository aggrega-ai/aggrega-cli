// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RecipeBinding resolves a component instance to the Recipe bound to it, by the
// instance's name and its catalog type — the same two strings the render stage
// binds by, so a caller supplying the build's own binding gets the Recipe that
// will actually render. A caller with no binding to offer supplies nil.
//
// It is an input to the advisory suite rather than a reading of either authoring
// document, because the binding is named on a Manifest and resolved by the caller
// that will render with it. One declared rule is keyed inside the Recipe it
// resolves; every other rule is unaffected by its absence.
type RecipeBinding func(instanceName, componentType string) (*types.Recipe, bool)

// RunAdvisories evaluates the ADVISORY rules complete before the composition is
// flattened, and returns one finding per objection.
//
// What bounds the set is where each rule's FACTS are complete, not which documents
// its subjects are authored in: the facts here are the two authoring documents
// normalized with the catalog reachable, and one rule's subject is an entry inside
// a Recipe the caller's binding resolves. So a finding here may name a third
// document, and each names the one its own subject was written in.
//
// It is the sibling of [RunGuards] and it is a separate entry point rather than a
// second return value from that one, which is the whole point of the split. A
// resolver finding carries no severity: the shape a finding takes is decided at
// the boundary it crosses, and the boundary decides advisory-versus-halting by
// which collection it appends to. [RunGuards] returns the collection a build
// halts on, so a rule asked there halts however its declaration reads. Nothing
// this function returns can halt a build, because it has no such collection to
// return — which is what makes an advisory's consequence a property of where it
// is asked rather than of whoever wired it.
//
// The rules asked here are the ones whose subject is legal authoring that is
// probably not what the author meant. Each names the sentinel it enforces, what a
// violation does to the build, the facts it reads and what it is invoked once
// per; the declarations themselves are the statement of how many there are.
//
// A near-miss reading cannot halt, and that is a property of the rule rather than
// a caution about it. Pairing two names is a heuristic, and an author may
// deliberately name a socket or an attachment that happens to sit one edit from a
// declared name — so a reading that refused would refuse legal input on a guess.
// The mitigation is to name both spellings and let the author see the pair.
//
// Findings come back rule by rule, and within a rule in the order its traversal
// invoked it, so identical input yields a byte-identical sequence.
//
// cancelled is read once per outermost coordinate of each traversal, and a walk
// that sees it turn hands back nothing rather than a truncated set — which is why
// this returns an error alongside the findings. A caller with no cancellation
// concern passes nil.
//
// recipeFor is the component-instance-to-Recipe binding the caller will render
// with. One rule asked here is keyed inside the Recipe it resolves, so a caller
// with no binding to offer passes nil and that one rule is asked about nothing
// while every other rule is unaffected.
//
// Returns no findings when the Aggregate document is absent.
func RunAdvisories(
	cancelled constraint.Cancellation,
	na types.NormalizedAggregate,
	release *types.Release,
	catalog CatalogLookup,
	recipeFor RecipeBinding,
	parameters map[string]any,
) ([]*ResolverError, error) {
	agg := na.Aggregate()
	if agg == nil {
		return nil, nil
	}
	// The presentation is built here rather than shared with the halting suite,
	// and the duplication is deliberate: sharing it would mean one function
	// holding both collections, which is exactly the arrangement that lets an
	// advisory be appended to the halting one. The cost is that the derived
	// contracts resolve once more per build. Do not "unify" the two
	// constructions without first moving the severity onto what a check raises,
	// or the separation that keeps an advisory advisory goes with it.
	contracts := newNormalizedView(na, release, catalog, parameters)
	return runDeclaredAdvisories(cancelled, contracts, recipeFor,
		fmt.Sprintf("Aggregate %s", agg.Metadata.Name))
}

// runDeclaredAdvisories asks every advisory rule whose facts are complete before
// the composition is flattened.
//
// The rules whose SUBJECT the Aggregate wrote name the Aggregate document, and
// that is their subject matter rather than a simplification: a reading of what the
// Release wrote is not attributable to the Aggregate, and a rule of that kind
// arriving here would need the second document named the way the halting suite
// names it. A rule whose subject sits inside a third document — a Recipe entry the
// caller's binding resolves — names that document per finding instead, because its
// locator is a path inside it.
//
// Each traversal call NAMES its rule as a literal argument, and that is a
// constraint of the framework rather than a style: a call written any other way
// makes the rule it invokes underivable. Which rules a caller invokes
// is derived by reading this module's source for a traversal entry called with a
// declared rule named at the call site; a helper taking a rule and calling the
// traversal itself would name a parameter there instead, and the derivation would
// report an invocation it can attribute to no rule — leaving every rule reached
// that way indistinguishable from one nothing invokes at all.
func runDeclaredAdvisories(
	cancelled constraint.Cancellation,
	view *normalizedView,
	recipeFor RecipeBinding,
	developerDocument string,
) ([]*ResolverError, error) {
	coordinates := newCoordinateSet(view).withRecipeBinding(recipeFor)
	facts := constraint.NormalizedView(view)

	var out []*ResolverError
	// A traversal that stopped on the cancellation signal ends the whole suite: a
	// partial set would report a document as satisfying readings that were never
	// asked of it.
	collect := func(objections []constraint.Objection, err error) error {
		if err != nil {
			return err
		}
		out = append(out, FindingsFromObjections(objections, developerDocument)...)
		return nil
	}

	// Once per component. The socket a near-miss reading is about is by
	// definition not one the component's type declares, so a per-socket
	// invocation would never be presented with its own subject; the set the
	// component wrote is read as a fact instead.
	if err := collect(constraint.RunOnNodes(
		cancelled, coordinates, facts, authoredSocketNameNearADeclaredOne)); err != nil {
		return nil, err
	}

	// Once per attachment a component carries.
	if err := collect(constraint.RunOnNodeAttachments(
		cancelled, coordinates, coordinates, facts, attachmentNameNearADeclaredSlot)); err != nil {
		return nil, err
	}

	// Once per key the Recipe bound to a component publishes. This one is
	// attributed per finding rather than to the Aggregate document, because the
	// entry it objects about is written in the Recipe and its locator names a path
	// inside that document — a path the Aggregate does not contain. The
	// Aggregate stands as the fallback for a subject carrying no file, which is
	// what a coordinate derived from the authoring documents carries.
	//
	// The axis is empty when the caller offered no binding, so a caller with none
	// is asked this rule about nothing while every reading above is unaffected.
	providesObjections, err := constraint.RunOnNodeProvidesKeys(
		cancelled, coordinates, coordinates, facts, publishedProvidesKeyIsDeclared)
	if err != nil {
		return nil, err
	}
	out = append(out, findingsAtTheirOwnDocument(
		providesObjections, "Recipe", developerDocument)...)

	return out, nil
}
