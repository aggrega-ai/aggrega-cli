// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// emptySourceAdvice is what an author does about a source entry written and left
// empty: omit it, or give it a real value.
//
// It is the occurrence's advice rather than part of the condition's registered
// sentence, and it is named here rather than written inline so the rule's remedy
// has one spelling. It names both paths because the remedy is a single edit either
// way, and which of the two to make is the whole of what the author has to decide.
const emptySourceAdvice = "omit the source field for sourceless mode, or name a non-empty path or " +
	"URI for sourced mode"

// recipeSourceEntryNamesAValue refuses a Recipe that writes its source entry and
// leaves it naming nothing.
//
// A Recipe selects between two authoring modes at that entry: sourceless, where the
// output is built entirely from the Recipe's other input kinds, and sourced, where
// the entry names a chart, module or file to render from. Omitting the entry selects
// the first and giving it a real value selects the second. Writing the entry and
// leaving it empty selects neither, and the commonest way to reach that state is
// deleting a URI and leaving the key behind — where the author meant sourced mode and
// a build admitting the entry as sourceless would ship without the artifact, never
// saying which of the two readings it took.
//
// It is asked as the document is read, which is the earliest point both facts it
// reads are available — both are answers about the key the author wrote in the file
// being loaded — and the point every later reading of the source depends on. Asked
// there, the Recipe is refused on every surface that loads it, before a source kind
// is inferred from the empty value or the empty value is read as a path.
//
// # Why two facts rather than one
//
// The fact answering whether a source is declared is false for an absent entry and
// for an empty one alike, which is the right answer for every rule asking whether
// the Recipe renders from a source and cannot tell this fault from a Recipe that
// simply declares none. So the presence of the entry is read beside it, and the rule
// is the pair: written, and declaring nothing. Folding the pair into one fact would
// report two authoring situations as one.
//
// # Why it is keyed at the entry's position
//
// It is declared over the subject standing for the Recipe's source entry rather
// than over the document, so its finding is positioned at .spec.source by the
// framework itself: the fault is one key the author left behind, and a finding
// naming only the file would leave the author a sentence with no line to go to.
// The position is presented whether or not the author wrote anything at it,
// which is what lets the rule be asked where an entry left empty sits.
//
// # What it raises
//
// No detail value at all, and that is the finding's shape rather than an omission.
// The registered sentence already names the fault and the two modes it is ambiguous
// between, the locator names the entry, and the value the entry holds is the empty
// string — quoting it would put an empty pair of quotes where a reader expects the
// thing they wrote.
var recipeSourceEntryNamesAValue = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentRecipeSourceFieldSubject,
]{
	Sentinel: ErrEmptySource(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentRecipeSourceFieldSubject,
	) []constraint.Objection {
		if !v.RecipeSourceEntryWritten(s.SourceFile()) {
			return nil
		}
		if v.RecipeSourceDeclared(s.SourceFile()) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(ErrEmptySource(), emptySourceAdvice),
		}
	},
}
