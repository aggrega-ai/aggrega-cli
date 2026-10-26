// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
)

// sourceKindAdvice is what an author does about a source naming a kind no adapter
// serves: name one that is served. It opens with the kind as written, because a
// shape-rule failure carries the occurrence in its advice and that word is the one
// the author corrects.
//
// The served kinds are read from their one declaration rather than spelled here, so
// the advice and the decision it accompanies cannot come to name different sets.
// One spelling serves the rule on both authored kinds that write a source.
func sourceKindAdvice(kind string) string {
	return fmt.Sprintf("the source names kind %q; name a kind a source adapter serves (%s), or write "+
		"the source as a single URI whose shape names its kind",
		kind, strings.Join(sourcekind.ServedKinds(), ", "))
}

// authoredKindUnserved is the predicate the rule over a source's kind asks on
// both authored kinds: a kind was written, and nothing serves it.
//
// It raises nothing, so each kind's rule raises its own objection inside its own
// declaration, and the two cannot come to disagree about what an unserved kind is.
func authoredKindUnserved(kind string, written, served bool) bool {
	return written && !served
}

// recipeSourceKindHasAnAdapter refuses a Recipe whose structured source names a
// kind this program serves no adapter for — a misspelling, or a kind no adapter
// ships for.
//
// The kind selects the adapter that fetches the Recipe's artifact, so a kind
// nothing serves is a source nothing can fetch. It is asked as the document is
// read, which is the earliest point both facts it reads are available: the kind
// is a word the author wrote in the file being loaded, and the served set is fixed
// when the program is assembled. Asking it there refuses the Recipe on every
// surface that reads it — checking it on its own, and every Release that reaches
// it, before anything is fetched — rather than at the first fetch, which only a
// pull makes.
//
// The finding's advice carries the kind as written, because that is the word the
// author corrects, and names the served set.
var recipeSourceKindHasAnAdapter = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: sourcekind.ErrUnknownKind(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		kind, written := v.RecipeAuthoredSourceKind(s.SourceFile())
		if !authoredKindUnserved(kind, written, v.RecipeSourceKindServed(kind)) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(sourcekind.ErrUnknownKind(), sourceKindAdvice(kind)),
		}
	},
}

// featureRecipeSourceKindHasAnAdapter is the FeatureRecipe's rule over its source
// kind: the Recipe's rule, read through the FeatureRecipe's own facts, because a
// fact is filed under one document kind. Why it is asked where it is asked is
// what the Recipe's rule states.
var featureRecipeSourceKindHasAnAdapter = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: sourcekind.ErrUnknownKind(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		kind, written := v.FeatureRecipeAuthoredSourceKind(s.SourceFile())
		if !authoredKindUnserved(kind, written, v.FeatureRecipeSourceKindServed(kind)) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(sourcekind.ErrUnknownKind(), sourceKindAdvice(kind)),
		}
	},
}
