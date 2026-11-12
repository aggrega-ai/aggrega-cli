// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// schemaPillarKeyAdvice is what an author does about a schema key naming no data
// pillar: the key at fault, the keys the block admits, and where the pillar set is
// written down.
//
// It is the occurrence's advice rather than part of the condition's registered
// sentence because the key is a property of the occurrence, and a registered
// sentence is one static string per condition — it can name neither the key met
// nor the keys the block admits, and both are what the author acts on.
//
// It names the admitted set a second time after the sentence has already said the
// key names no pillar. That is a cost rather than an oversight, and it is the price
// of the split: reducing the advice to the offending key alone would delete from
// the diagnostic the one thing that tells an author what to write instead.
func schemaPillarKeyAdvice(key string) string {
	return fmt.Sprintf(
		"rename %q to one of the pillar keys, or remove it and the contract beneath it; "+
			"allowed: values, infra, provides. "+
			"See vocabulary/data-pillars.md for the canonical pillar set.", key)
}

// schemaPillarKeyObjections is the rule itself: a top-level key of an entity's
// schema block names one of the data pillars, or the entity is refused.
//
// The key set is closed for the same reason each pillar's own content is validated
// closed: the block enumerates a legal surface, so a key outside it is an authoring
// error rather than a permitted extension. Admitting one would leave the author
// believing a contract is in force while every consumer of the entity walks past
// it, which is the silent outcome a closed set removes.
//
// It is one function over both authored kinds that carry the block, and the two
// declarations below hand it the key each was invoked for. The rule does not vary
// by kind: the block is written under the same key on each, admits the same three
// pillars on each, and is consumed the same way. What varies is which document the
// key was read from, and that is the document-kind axis the two declarations express.
func schemaPillarKeyObjections(
	s constraint.DocumentSchemaPillarSubject,
	key string,
) []constraint.Objection {
	if allowedSchemaPillars[key] {
		return nil
	}
	return []constraint.Objection{
		s.RaiseWithAdvice(ErrUnknownSchemaPillar(), schemaPillarKeyAdvice(key)),
	}
}

// componentSchemaKeyNamesAPillar refuses a Component whose schema block declares a
// top-level key naming no data pillar.
//
// It is one of two declarations of one rule, and the plurality is forced by the
// check type rather than chosen: a check carries exactly one subject type parameter
// and reads one presentation, and the two authored kinds that carry a schema block
// are two document kinds. A fact is filed under exactly one of them, so a single
// declaration would govern one kind and read as though it governed the rule — which
// is worse than an absent check, because nothing would ever prompt a second look at
// the kind it silently passed. Both declarations raise the same condition, so the
// diagnostic an author reads does not turn on which kind they wrote.
//
// It is invoked once per authored key, which is why what it reports is the key
// rather than the block: an author handed a block-level finding has to work out
// which of its keys was meant, and a block declaring two offending keys is two
// renames.
var componentSchemaKeyNamesAPillar = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSchemaPillarSubject,
]{
	Sentinel: ErrUnknownSchemaPillar(),
	Severity: constraint.SeverityFatal,
	SubjectOnly: "reads SchemaPillarKey, the authored key the subject carries. " +
		"The rule is a membership test on that key against a set the block's own " +
		"contract closes, so there is nothing about the document to read and no " +
		"fact to narrow to.",
	Run: func(
		_ constraint.DocumentView,
		s constraint.DocumentSchemaPillarSubject,
	) []constraint.Objection {
		return schemaPillarKeyObjections(s, s.SchemaPillarKey())
	},
}

// featureSchemaKeyNamesAPillar refuses a Feature whose schema block declares a
// top-level key naming no data pillar.
//
// It is the Feature document's declaration of the rule componentSchemaKeyNamesAPillar
// declares for a Component. Why there are two, and why both raise one condition, is
// stated there; it is not repeated here, because a rule's reasoning has one home.
var featureSchemaKeyNamesAPillar = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSchemaPillarSubject,
]{
	Sentinel: ErrUnknownSchemaPillar(),
	Severity: constraint.SeverityFatal,
	SubjectOnly: "reads SchemaPillarKey, the authored key the subject carries, " +
		"for the reason the Component document's declaration states.",
	Run: func(
		_ constraint.DocumentView,
		s constraint.DocumentSchemaPillarSubject,
	) []constraint.Objection {
		return schemaPillarKeyObjections(s, s.SchemaPillarKey())
	},
}
