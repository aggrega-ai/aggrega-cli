// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestAdvisoryLineOmitsEachAbsentIdentifier pins the row the human report prints
// for one advisory finding, over every combination of the two identifiers a
// finding may carry: the short code of the condition its rule names, and the
// rule's own identity. Each one present follows the severity, in that order, and
// a colon closes them; each one absent is omitted, and where both are absent the
// closing colon goes with them, so the message follows the severity directly and
// no row holds an empty segment between two colons. A finding's own code stands
// in the rule's place where it names no rule, which
// [TestFindingIdentityIsTheRuleElseTheFindingsOwnCode] drives; every finding here
// carries none, so the rule's place holds the rule or nothing.
//
// The expected rows are written out whole rather than assembled the way the
// renderer assembles them, so a renderer that drifted into another shape fails
// here instead of agreeing with a copy of itself. The identifiers are read off a
// registered condition through its exported accessors, never written as
// literals, so renumbering the condition moves nothing here. A short code with no
// rule identity is not a finding [FindingFrom] produces; it is driven because the
// row is a function of the two fields however they were filled.
//
// The last row is the control: both identifiers present, as on the row of every
// rule the compiler declares, and a message carrying colons of its own, which the
// row reproduces as written — so a renderer that removed doubled colons from the
// whole line rather than declining to write an empty segment fails here too.
func TestAdvisoryLineOmitsEachAbsentIdentifier(t *testing.T) {
	t.Parallel()

	condition := ErrCategoryMatchesDirectory()
	rule, ok := cerrors.CodeOf(condition)
	if !ok || rule == "" {
		t.Fatalf("the condition reports no descriptive code, so no row here names a rule: %v", condition)
	}
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok || short == "" {
		t.Fatalf("the condition reports no short code, so no row here carries one: %v", condition)
	}
	const message = "the rule's own sentence about the entity"
	const colons = "key spec::values: is read as written"

	for _, tc := range []struct {
		name              string
		severity          string
		shortCode, ruleID string
		message           string
		want              string
	}{
		{
			name:      "a short code and a rule identity: both, then the message",
			severity:  abi.SeverityWarn,
			shortCode: short,
			ruleID:    rule,
			message:   message,
			want:      "warn: " + short + " " + rule + ": " + message,
		},
		{
			name:     "a rule identity no condition names: the identity alone",
			severity: abi.SeverityWarn,
			ruleID:   "rule-of-another-linter",
			message:  message,
			want:     "warn: rule-of-another-linter: " + message,
		},
		{
			name:      "a short code and no rule identity: the code alone",
			severity:  abi.SeverityWarn,
			shortCode: short,
			message:   message,
			want:      "warn: " + short + ": " + message,
		},
		{
			name:     "neither identifier: the severity, then the message",
			severity: abi.SeverityWarn,
			message:  message,
			want:     "warn: " + message,
		},
		{
			name:     "neither identifier, at the informational severity",
			severity: abi.SeverityInfo,
			message:  message,
			want:     "info: " + message,
		},
		{
			name:      "control: both identifiers, and a message whose own colons stand as written",
			severity:  abi.SeverityWarn,
			shortCode: short,
			ruleID:    rule,
			message:   colons,
			want:      "warn: " + short + " " + rule + ": " + colons,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := advisoryLine(Finding{
				Severity:  tc.severity,
				Message:   tc.message,
				RuleID:    tc.ruleID,
				ShortCode: tc.shortCode,
			})
			if got != tc.want {
				t.Errorf("advisoryLine row = %q\n                   want %q", got, tc.want)
			}
		})
	}
}
