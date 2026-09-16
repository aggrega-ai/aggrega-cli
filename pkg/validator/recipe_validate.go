// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ValidateRecipe enforces the Recipe shape rules at load time. Called from
// catalog loading immediately after yaml.Unmarshal succeeds, before the
// Recipe is admitted to the catalog map. Returns nil on pass.
//
// A failure is a *ValidationError against the Recipe kind, except where the rule
// that raised it names an authored key inside the document: such a failure is
// that same value carrying both halves of its position, so a consumer reading it
// as a *ValidationError still does, through the chain. Which rules those are is
// a property of what each names rather than a list to keep here — a rule about
// the document as a whole has only the file to give.
//
// Rules enforced here: values-mapping and input-kinds, plus the eval-provider
// constraint. The workload-requires-provider-workloadSchema constraint is
// enforced at render time by the central input-kind gate (a synthesized Recipe
// bound to a provider that does not implement workload synthesis halts loudly
// there). The filename-collision rule (a collision across input-kind origins)
// is enforced at emission time by feature rendering.
//
// The eval-provider carve-out is two-sided:
//
//  1. Reject non-empty source / workload / artifacts / valuesMapping (eval has
//     no rendering surface).
//  2. Exempt eval Recipes from the input-kinds rule (a Recipe declaring only
//     `provides:` is legitimate eval-mode authoring).
//
// What is deliberately NOT checked: the contents of `source:`. Whether a source
// is present is a shape question and is answered here; what its keys mean is
// not. A structured `source:` is an open dictionary, and the keys inside it —
// `registry`, `chart`, `appVersion`, `workspace`, or anything else a provider
// defines — are carried through uninterpreted, exposed to the Recipe's templates
// as `.source.*`, and forwarded verbatim to the provider engine that renders the
// Recipe. The core is blind to them by construction: it holds no list of
// provider-specific field names to check against, and the block is the
// provider's contract to define and to check. Adding a key check here would move
// that contract into the core and make the core opinionated about execution
// layers it is meant to know nothing about.
func ValidateRecipe(r *types.Recipe, sourcePath string) error {
	if r == nil {
		return nil
	}

	// The output-label rules are engine-agnostic and run before the eval
	// carve-out: an eval Recipe declares no rendering surface but must still not
	// claim a reserved provenance key, whether it names one literally or
	// promotes it from a component label.
	if err := validateRecipeOutputLabels(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeOutputComponentLabels(r, sourcePath); err != nil {
		return err
	}

	if err := validateRecipeEvalProvider(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeValuesMapping(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeInputKinds(r, sourcePath); err != nil {
		return err
	}
	return nil
}

// validateRecipeEvalProvider enforces the eval-provider constraint: a Recipe
// binding the eval capability declares no rendering surface.
//
// The rule itself is declared rather than written out here: see
// recipeEvalDeclaresNoRenderingSurface, which names the facts it reads — the
// declared capability among them — and is invoked once per presented document.
// What this function does is present the document the load is holding and render
// whatever came back into the failure shape this package's callers read.
//
// It is invoked for every Recipe rather than only for an eval one, because the
// capability gate belongs to the rule. The values-mapping rule below is reached
// on an eval Recipe as a result, and reaching it changes nothing: this rule
// refuses any eval Recipe declaring an overlay and runs first, so an eval Recipe
// arriving there declares none.
//
// The rule raises at most one objection, for the reason validateRecipeValuesMapping
// states.
func validateRecipeEvalProvider(r *types.Recipe, sourcePath string) error {
	var presented constraint.DocumentView = newRecipeDocumentView(sourcePath, r)
	failures := shapeRuleErrorsFrom(
		constraint.RunOnDocument(
			sourcePath,
			presented,
			recipeEvalDeclaresNoRenderingSurface,
		),
		ruleSubject{
			Kind:    envelope.KindRecipe,
			Name:    r.Metadata.Name,
			Version: r.Metadata.Version,
			Rule:    "eval",
		},
	)
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}

// validateRecipeOutputLabels enforces the reserved-namespace rule over
// spec.outputLabels, the block whose entries carry a label key and the value
// written in place beside it.
//
// The rule itself is declared rather than written out here: see
// recipeOutputLabelAvoidsAReservedNamespace, which names what it reads and is
// invoked once per declared key. What this function does is present the document
// the load is holding, walk the literal block's keys, and render whatever came
// back into the failure shape this package's callers read.
//
// The keys reach the walk in sorted order, which is the order the fact enumerating
// them declares — the block is a mapping and has no author order to carry, so the
// sort is what makes the first-reported key the same one on every run.
func validateRecipeOutputLabels(r *types.Recipe, sourcePath string) error {
	presented := newRecipeDocumentView(sourcePath, r)
	// The presentation is held as the view the rule declared rather than as its
	// own concrete type, because the entry takes the check and the view together
	// and infers one type from both. Handing over the concrete type would infer
	// that type and then refuse the rule, which declares the view.
	var view constraint.DocumentView = presented
	return firstOutputLabelFailure(
		constraint.RunOnDocumentOutputLabels(
			sourcePath,
			constraint.OutputLabelBlockLiteral,
			presented.RecipeOutputLabelKeys(sourcePath),
			view,
			recipeOutputLabelAvoidsAReservedNamespace,
		),
		r, sourcePath, "output-labels",
	)
}

// isReservedLabelKey reports whether key sits in a platform-reserved label
// namespace. It is the single home for the plain-prefix test, so the literal
// and the promoted output-label rules cannot drift apart on what "reserved"
// means.
//
// The test is the bare root only. A key that merely contains a reserved root
// deeper in its own namespace — component.aggrega.ai/hosting, say — is a
// distinct keyspace the platform does not own, and stays authorable.
func isReservedLabelKey(key string) bool {
	return strings.HasPrefix(key, types.ReservedLabelPrefixAggregaAI) ||
		strings.HasPrefix(key, types.ReservedLabelPrefixK8sRecommended)
}

// validateRecipeOutputComponentLabels enforces the two load-time rules on
// spec.outputComponentLabels, the block naming component label keys whose
// values are promoted onto rendered output.
//
// Both rules are declared rather than written out here: see
// recipeOutputLabelAvoidsAReservedNamespace, which this block shares with the
// literal one, and recipePromotedOutputLabelIsNotAlsoLiteral, which is this
// block's own. What this function does is present the document the load is
// holding and invoke each in turn.
//
// The reserved rule is applied across the whole block before the collision rule,
// so a recipe violating both always reports the reserved namespace — the more
// fundamental fault, and the one whose fix does not depend on the other block's
// contents. That precedence is why the two are invoked as two walks rather than
// one: a single walk asking both questions per key would report whichever fault
// the earlier key carried, so a block whose first key collides and whose second
// claims a reserved namespace would report the collision. Two walks settle the
// first rule over every key before the second is asked anything.
//
// Entries reach each walk in author order, which is the order the fact
// enumerating them declares: the block is a sequence, so the author wrote an
// order and it is the one the findings appear in.
func validateRecipeOutputComponentLabels(r *types.Recipe, sourcePath string) error {
	presented := newRecipeDocumentView(sourcePath, r)
	var view constraint.DocumentView = presented
	keys := presented.RecipeOutputComponentLabelKeys(sourcePath)

	if failure := firstOutputLabelFailure(
		constraint.RunOnDocumentOutputLabels(
			sourcePath,
			constraint.OutputLabelBlockPromoted,
			keys,
			view,
			recipeOutputLabelAvoidsAReservedNamespace,
		),
		r, sourcePath, "output-component-labels",
	); failure != nil {
		return failure
	}

	return firstOutputLabelFailure(
		constraint.RunOnDocumentOutputLabels(
			sourcePath,
			constraint.OutputLabelBlockPromoted,
			keys,
			view,
			recipePromotedOutputLabelIsNotAlsoLiteral,
		),
		r, sourcePath, "output-component-labels",
	)
}

// firstOutputLabelFailure renders the first objection an output-label rule
// raised into the failure this function's callers return, positioned at the file
// AND at the authored key.
//
// It returns the FIRST finding rather than the set, because the caller returns
// one error and a block with two offending keys is two edits the author makes
// from the same file. The rules are invoked once per key regardless, so which
// finding is first is the order the keys were presented in rather than an
// artifact of the walk.
//
// The two halves of the position travel in separate fields of one location
// because a single string cannot be both: folding the key into the file would
// displace the path a reader opens the file with, and dropping the file for the
// key would leave a reader holding a path into a document nothing names. What the
// locator carries is bounded by the dialect rather than by this rule — a label
// key routinely contains the dialect's own separator, and the assembly then names
// the block rather than the entry. The key itself is never lost: it is what the
// advice opens with.
func firstOutputLabelFailure(
	objections []constraint.Objection,
	r *types.Recipe,
	sourcePath string,
	rule string,
) error {
	failures := shapeRuleErrorsFrom(objections, ruleSubject{
		Kind:    envelope.KindRecipe,
		Name:    r.Metadata.Name,
		Version: r.Metadata.Version,
		Rule:    rule,
	})
	if len(failures) == 0 {
		return nil
	}
	// The conversion renders one failure per objection in the order they were
	// raised, so the first failure and the first objection are about one key.
	return cerrors.Wrap(
		failures[0],
		categoryFor(envelope.KindRecipe),
		cerrors.SourceLocation{File: sourcePath, Locator: objections[0].Locator()},
		failures[0].Error(),
	)
}

// validateRecipeValuesMapping enforces the values-mapping rule: valuesMapping requires
// source.
//
// The rule itself is declared rather than written out here: see
// recipeValuesMappingHasASource, which names the facts it reads and is invoked
// once per presented document. What this function does is present the document
// the load is holding and render whatever came back into the failure shape this
// package's callers read.
//
// The rule raises at most one objection, because it is invoked once per document
// and objects about the document as a whole, so the first failure is the only one
// there can be. Taking the first rather than returning the set is what keeps this
// function's result the single failure every other rule here returns.
func validateRecipeValuesMapping(r *types.Recipe, sourcePath string) error {
	// The presentation is held as the view the rule declared rather than as its
	// own concrete type, because the entry takes the check and the view together
	// and infers one type from both. Handing over the concrete type would infer
	// that type and then refuse the rule, which declares the view.
	var presented constraint.DocumentView = newRecipeDocumentView(sourcePath, r)
	failures := shapeRuleErrorsFrom(
		constraint.RunOnDocument(
			sourcePath,
			presented,
			recipeValuesMappingHasASource,
		),
		ruleSubject{
			Kind:    envelope.KindRecipe,
			Name:    r.Metadata.Name,
			Version: r.Metadata.Version,
			Rule:    "values-mapping",
		},
	)
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}

// validateRecipeInputKinds enforces the input-kinds rule: at least one input kind
// must be present.
//
// The three input kinds (any one suffices):
//
//  1. Sourced — source + valuesMapping (and ValuesMapping has its own paired rules above).
//  2. Synthesized — workload (inline OR $import: file).
//  3. Artifacts — at least one entry under artifacts.
//
// The rule itself is declared rather than written out here: see
// recipeDeclaresAnInputKind, which names the facts it reads and carries the eval
// carve-out as part of the rule rather than as a property of this call site.
// What this function does is present the document the load is holding and render
// whatever came back into the failure shape this package's callers read.
//
// The rule raises at most one objection, for the reason validateRecipeValuesMapping
// states.
func validateRecipeInputKinds(r *types.Recipe, sourcePath string) error {
	var presented constraint.DocumentView = newRecipeDocumentView(sourcePath, r)
	failures := shapeRuleErrorsFrom(
		constraint.RunOnDocument(
			sourcePath,
			presented,
			recipeDeclaresAnInputKind,
		),
		ruleSubject{
			Kind:    envelope.KindRecipe,
			Name:    r.Metadata.Name,
			Version: r.Metadata.Version,
			Rule:    "input-kinds",
		},
	)
	if len(failures) == 0 {
		return nil
	}
	return failures[0]
}

// sourceIsEmpty reports whether the Recipe's Spec.Source field carries no
// value. Source is any and can be a string URI, a structured object,
// or nil; any non-nil non-empty value counts as a declared source.
func sourceIsEmpty(src any) bool {
	if src == nil {
		return true
	}
	if s, ok := src.(string); ok && s == "" {
		return true
	}
	return false
}
