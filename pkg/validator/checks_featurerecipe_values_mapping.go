// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// featureRecipeValuesMappingHasASource refuses a FeatureRecipe that declares a
// values overlay and no source to overlay it onto.
//
// Sourced-mode authoring presents a source pointer alongside a values overlay. A
// FeatureRecipe with an overlay and no source has no chart or module for the
// overlay to apply to, so the overlay is a contract nothing will ever read.
//
// It reads the two facts complete once one authored document has been parsed and
// envelope-checked, which is the earliest point either is available and the point
// its declared view names. Neither question needs the entity resolved, the
// catalog reached, or anything normalized: both are answers about keys the author
// wrote in the file being loaded, so the rule runs as the document arrives.
//
// It carries no capability gate, and the absence is deliberate rather than an
// omission. An eval FeatureRecipe declaring an overlay is already refused by the
// eval rule — the overlay is one of the fields that rule forbids — and the load
// invokes that rule first and returns the first failure. So the only eval
// documents reaching this one declare no overlay, and this rule returns at its
// first condition. A gate here would restate, as a second copy, a precedence the
// caller's ordering already settles.
//
// It is invoked once per document rather than once per anything inside it. The
// violation is a property of the pair of keys rather than of either one, so there
// is nothing inside the document to be invoked per, and the finding is positioned
// at the file: a subject standing for the whole document names no authored
// element inside it, so there is no narrower position to give.
var featureRecipeValuesMappingHasASource = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrFeatureRecipeValuesMappingRequiresSource(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		if !v.FeatureRecipeValuesMappingDeclared(s.SourceFile()) {
			return nil
		}
		if v.FeatureRecipeSourceDeclared(s.SourceFile()) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(
				ErrFeatureRecipeValuesMappingRequiresSource(),
				valuesMappingRequiresSourceAdvice,
			),
		}
	},
}
