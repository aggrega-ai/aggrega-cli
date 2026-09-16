// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// This file declares the two rules over a Recipe's input-kind surface. They are
// declared together rather than one per file because they are the two halves of
// a single carve-out: one refuses the rendering surface an eval Recipe must not
// declare, the other exempts an eval Recipe from having to declare one. Reading
// either alone leaves a reader believing the other does not exist, and the
// capability the carve-out turns on then has two spellings instead of the one
// below.

// evalCapability is the capability a Recipe or a FeatureRecipe declares when it
// evaluates values in memory and renders nothing.
//
// It is named once and read by every rule the carve-out reaches, on both authored
// kinds. A second spelling would let those rules disagree about which documents
// the carve-out covers, and the disagreement would surface as a legitimate
// document refused rather than as anything failing.
//
// The comparison against it is exact, which is the whole of what an author needs
// to know about it: a capability declaration may carry a version pin, and a
// pinned declaration is a different string from the bare name.
const evalCapability = "eval"

// evalForbiddenFieldAdvice is what an author does about an eval Recipe or an eval
// FeatureRecipe declaring a rendering surface: the fields at fault, named as the
// author wrote them.
//
// It is the occurrence's advice rather than part of the condition's registered
// sentence because which fields were declared is a property of the occurrence,
// and a registered sentence is one static string per condition. One builder
// serves both kinds because the sentence they close with is the same sentence;
// what differs between them is the list handed in, which belongs to the rule.
//
// The order is each rule's own and arrives already fixed: it is the order that
// rule names the fields in, and it is not derived from anything, because
// identical input produces identical output and a document declaring several
// fields names them the same way on every run and in every reader's diff. The
// two kinds order differently, and the difference is deliberate on both sides —
// each matches the order the reference documentation for that kind's condition
// names the blocks in, so a reader meeting the diagnostic and the page meets one
// list rather than two. Neither is the order the kind's own declaration lists
// those fields in, and neither may be reordered to match it.
func evalForbiddenFieldAdvice(declared []string) string {
	return fmt.Sprintf("forbidden fields declared: %s", strings.Join(declared, ", "))
}

// recipeInputKindAdvice is what an author does about a Recipe declaring no input
// kind: the three authoring forms, any one of which satisfies the rule.
//
// It is named here rather than written inline so the rule's remedy has one
// spelling.
const recipeInputKindAdvice = "declare one of: source (with valuesMapping), workload, artifacts"

// recipeEvalDeclaresNoRenderingSurface refuses a Recipe that binds the eval
// capability and declares a rendering surface.
//
// An eval Recipe evaluates values in memory and emits nothing, so it has nowhere
// to render a chart, a workload or an artifact to. A rendering field declared on
// one is a contract nothing will ever read, and the author almost always meant a
// different capability.
//
// It reads the capability alongside the four shape facts rather than being
// invoked only where a caller has already decided the Recipe is an eval one.
// The gate is part of the rule: a declaration reading the four fields alone
// would state, as a rule, that no Recipe may declare a rendering surface, which
// is false of every other capability. Where the gate lives in the caller a
// reader of the declaration cannot see it at all.
//
// Every fact it reads is complete once one authored document has been parsed and
// envelope-checked, which is the point its declared view names. Nothing here
// needs the entity resolved, the catalog reached, or the capability's provider
// bound — what a capability selects is a later question, and this asks only what
// the author wrote.
//
// It is invoked once per document rather than once per declared field. The
// violation is a property of the capability and the field set taken together
// rather than of any one field, and the remedy is a single decision about which
// capability the Recipe meant — so the fields are named together in one finding
// rather than reported one at a time.
var recipeEvalDeclaresNoRenderingSurface = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrRecipeEvalProviderConstraint(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		if v.RecipeDeclaredCapability(s.SourceFile()) != evalCapability {
			return nil
		}

		var declared []string
		if v.RecipeSourceDeclared(s.SourceFile()) {
			declared = append(declared, "source")
		}
		if v.RecipeWorkloadDeclared(s.SourceFile()) {
			declared = append(declared, "workload")
		}
		if v.RecipeArtifactsDeclared(s.SourceFile()) {
			declared = append(declared, "artifacts")
		}
		if v.RecipeValuesMappingDeclared(s.SourceFile()) {
			declared = append(declared, "valuesMapping")
		}
		if len(declared) == 0 {
			return nil
		}

		return []constraint.Objection{
			s.RaiseWithAdvice(
				ErrRecipeEvalProviderConstraint(),
				evalForbiddenFieldAdvice(declared),
			),
		}
	},
}

// recipeDeclaresAnInputKind refuses a Recipe that declares no input kind at all.
//
// A Recipe renders from one of three authored surfaces, any one of which
// suffices: a source pointer the provider renders, a workload body it
// synthesizes, or artifact entries it emits directly. A Recipe declaring none of
// them has no rendering surface, so binding it to a component would produce
// nothing and say nothing about why.
//
// An eval Recipe is exempt, and the exemption is the rule's own rather than a
// property of where it is invoked from. Such a Recipe legitimately declares only
// the values it publishes: it renders nothing by design, so the surface this
// rule requires is one it is separately forbidden to declare. Reading the
// capability here is what keeps those two statements from contradicting each
// other on the same document.
//
// Every fact it reads is complete once one authored document has been parsed and
// envelope-checked, which is the point its declared view names. It asks only
// which blocks the author wrote, never what any of them resolves to.
//
// It is invoked once per document rather than once per absent block. The
// violation is the absence of all three at once, which is a property of the
// document rather than of any one block, and a subject standing for the whole
// document names no authored element inside it — there is no narrower position
// to give, because the fix is a block the author has not written yet.
var recipeDeclaresAnInputKind = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrRecipeNoInputKind(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		if v.RecipeDeclaredCapability(s.SourceFile()) == evalCapability {
			return nil
		}
		if v.RecipeSourceDeclared(s.SourceFile()) ||
			v.RecipeWorkloadDeclared(s.SourceFile()) ||
			v.RecipeArtifactsDeclared(s.SourceFile()) {
			return nil
		}

		return []constraint.Objection{
			s.RaiseWithAdvice(ErrRecipeNoInputKind(), recipeInputKindAdvice),
		}
	},
}
