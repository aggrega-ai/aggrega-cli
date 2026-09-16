// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// valuesMappingRequiresSourceAdvice is what an author does about a values
// overlay declared without a source.
//
// It is the occurrence's advice rather than part of the condition's registered
// sentence, and it is named here rather than written inline so the rule's remedy
// has one spelling.
const valuesMappingRequiresSourceAdvice = "declare a source: pointer alongside valuesMapping " +
	"(or remove the valuesMapping if no overlay is intended)"

// recipeValuesMappingHasASource refuses a Recipe that declares a values overlay
// and no source to overlay it onto.
//
// Sourced-mode authoring presents a source pointer alongside a values overlay. A
// Recipe with an overlay and no source has no chart or module for the overlay to
// apply to, so the overlay is a contract nothing will ever read.
//
// It reads the two facts complete once one authored document has been parsed and
// envelope-checked, which is the earliest point either is available and the point
// its declared view names. Neither question needs the entity resolved, the
// catalog reached, or anything normalized: both are answers about keys the author
// wrote in the file being loaded, so the rule runs as the document arrives.
//
// It is invoked once per document rather than once per anything inside it. The
// violation is a property of the pair of keys rather than of either one, so there
// is nothing inside the document to be invoked per, and the finding is positioned
// at the file: a subject standing for the whole document names no authored
// element inside it, so there is no narrower position to give.
var recipeValuesMappingHasASource = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrRecipeValuesMappingRequiresSource(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		if !v.RecipeValuesMappingDeclared(s.SourceFile()) {
			return nil
		}
		if v.RecipeSourceDeclared(s.SourceFile()) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(
				ErrRecipeValuesMappingRequiresSource(),
				valuesMappingRequiresSourceAdvice,
			),
		}
	},
}
