// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// recipeInputKindAdvice is what an author does about a Recipe declaring no input
// kind: the three authoring forms, any one of which satisfies the rule.
//
// It is named here rather than written inline so the rule's remedy has one
// spelling.
const recipeInputKindAdvice = "declare one of: source (with valuesMapping), workload, artifacts"

// recipeDeclaresAnInputKind refuses a Recipe that declares no input kind at all.
//
// A Recipe renders from one of three authored surfaces, any one of which
// suffices: a source pointer the provider renders, a workload body it
// synthesizes, or artifact entries it emits directly. A Recipe declaring none of
// them has no rendering surface, so binding it to a component would produce
// nothing and say nothing about why. A component that needs no compute of its
// own — a service the build does not provision — still declares one: a workload
// of the reference form, which names the endpoint its consumers reach.
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
