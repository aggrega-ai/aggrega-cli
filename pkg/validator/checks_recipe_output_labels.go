// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// This file declares the two rules over a Recipe's output-label surface. They
// are declared together rather than one per file because they read the same two
// authored blocks and are ordered against each other: one is settled across a
// whole block before the other is asked anything. Reading either alone leaves a
// reader unable to see that ordering, which is the part a caller has to
// preserve.

// reservedOutputLabelAdvice is what an author does about a label key claiming a
// platform-reserved namespace: the key at fault, the block it was written in,
// and the two roots the platform owns.
//
// It is the occurrence's advice rather than part of the condition's registered
// sentence because both values are properties of the occurrence, and a
// registered sentence is one static string per condition. The block is the half
// that could not live anywhere else: one condition covers both label blocks, so
// its sentence cannot say which of the two the author typed, and an author sent
// to a Recipe declaring both has two places to look.
func reservedOutputLabelAdvice(block, key string) string {
	return fmt.Sprintf(
		"key %q in %s uses a reserved namespace (%q and %q are reserved for platform provenance)",
		key, block, types.ReservedLabelPrefixAggregaAI, types.ReservedLabelPrefixK8sRecommended)
}

// promotedOutputLabelCollisionAdvice is what an author does about one key
// declared in both label blocks: which key, and that the fix is a choice between
// the two sources rather than an edit to either.
//
// It names the blocks through the framework's own vocabulary rather than
// spelling them, so the words an author reads here are the words a finding in
// the other rule is positioned at.
func promotedOutputLabelCollisionAdvice(key string) string {
	return fmt.Sprintf(
		"key %q is promoted through %s and also declared literally in %s; "+
			"keep whichever source the label should come from and drop the other",
		key, constraint.OutputLabelBlockPromoted, constraint.OutputLabelBlockLiteral)
}

// recipeOutputLabelAvoidsAReservedNamespace refuses a Recipe declaring an
// output-label key under a platform-reserved namespace.
//
// The reserved roots are the compiler-owned provenance keyspace. A recipe
// stamping one would spoof provenance, and reserving them is also what keeps a
// validated output-label set disjoint from the fixed provenance set, so the
// merge at render time has no collision to settle. Nothing else about a key or
// its value is policed here: charset is the target runtime's to reject, and a
// key outside the reserved roots is the author's to choose.
//
// It is one rule over both blocks rather than one per block, and the two are the
// same rule rather than two that agree: the keyspace a recipe may not claim is a
// property of the platform, not of which block a key reached it through. So the
// block is the coordinate the finding names and never a second condition.
//
// It reads nothing about the document. Whether a key claims a reserved root is a
// prefix test on the key itself, and the test has one home in this package so the
// literal and the promoted block cannot come to disagree on what "reserved"
// means.
//
// It is invoked once per declared key, which is why what it reports is the key
// rather than the block: an author handed a block-level finding has to work out
// which of its keys was meant, and a block declaring two reserved keys is two
// edits.
var recipeOutputLabelAvoidsAReservedNamespace = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentOutputLabelSubject,
]{
	Sentinel: ErrRecipeOutputLabelReservedNamespace(),
	Severity: constraint.SeverityFatal,
	SubjectOnly: "reads OutputLabelKey, the authored key the subject carries, and " +
		"OutputLabelBlock, the block it was written in. The rule is a prefix test on " +
		"that key against the roots the platform owns, so there is nothing about the " +
		"document to read and no fact to narrow to.",
	Run: func(
		_ constraint.DocumentView,
		s constraint.DocumentOutputLabelSubject,
	) []constraint.Objection {
		if !isReservedLabelKey(s.OutputLabelKey()) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(
				ErrRecipeOutputLabelReservedNamespace(),
				reservedOutputLabelAdvice(s.OutputLabelBlock(), s.OutputLabelKey()),
			),
		}
	},
}

// recipePromotedOutputLabelIsNotAlsoLiteral refuses a Recipe promoting a
// component label key it also declares literally.
//
// The two blocks stamp onto one label surface, so a key named in both would
// carry a value from two sources at once — the component instance's own label
// and the literal written in place — with nothing to say which wins. Refusing
// the overlap keeps every stamped key traceable to exactly one source.
//
// It is asked about the promoted block alone, and the gate is part of the rule
// rather than a property of where it is invoked from. A key of the literal block
// is trivially declared in the literal block, so a declaration without the gate
// would refuse every literal key it was ever handed — and a reader of the
// declaration could not see that the caller was what stopped it.
//
// It reads the literal block's key set and nothing else. That set is a fact the
// presentation carries, so the rule asks one question of it rather than walking
// the document: is the key it was invoked for among the keys written in place.
//
// It is invoked once per promoted key, and the first finding is therefore the
// first colliding key the author wrote.
var recipePromotedOutputLabelIsNotAlsoLiteral = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentOutputLabelSubject,
]{
	Sentinel: ErrRecipeOutputComponentLabelCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentOutputLabelSubject,
	) []constraint.Objection {
		if s.OutputLabelBlock() != constraint.OutputLabelBlockPromoted {
			return nil
		}
		for _, literal := range v.RecipeOutputLabelKeys(s.SourceFile()) {
			if literal != s.OutputLabelKey() {
				continue
			}
			return []constraint.Objection{
				s.RaiseWithAdvice(
					ErrRecipeOutputComponentLabelCollision(),
					promotedOutputLabelCollisionAdvice(s.OutputLabelKey()),
				),
			}
		}
		return nil
	},
}
