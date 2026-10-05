// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestFindingIdentityIsTheRuleElseTheFindingsOwnCode pins the one identifier a
// finding is named by, over each combination of the two a finding may carry —
// the rule that produced it and a code of its own — and holds the command line's
// two renderings of a finding to it: the row an advisory prints, and the message
// a halting finding that names no condition leads with.
//
// Every expectation is written out whole rather than assembled the way the
// renderers assemble it, so a renderer that read the two fields itself and
// drifted from [Finding.Identity] fails here instead of agreeing with a copy of
// itself. The language server's entry is held to the same rows by its own table,
// in the package that assembles it, because that projection is unexported there.
//
// Findings are built through [FindingFrom], so the short code on each is the one
// the lint operation fills rather than one this test assigns.
//
// The first row is the control: a rule the compiler declares, carrying a code of
// its own besides, is named by its rule and led by its condition's short code,
// and a halt of it names that condition. The last row is the one place the
// identity and the condition part ways: a code of its own that spells a declared
// rule's identity, on a finding naming no rule, names the finding and borrows no
// short code, because which condition a finding names is read off its rule alone.
func TestFindingIdentityIsTheRuleElseTheFindingsOwnCode(t *testing.T) {
	t.Parallel()

	condition := ErrCategoryMatchesDirectory()
	rule, ok := cerrors.CodeOf(condition)
	if !ok || rule == "" {
		t.Fatalf("the condition reports no descriptive code, so no row here names a declared rule: %v", condition)
	}
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok || short == "" {
		t.Fatalf("the condition reports no short code, so the control row has nothing to lead with: %v", condition)
	}
	const message = "the rule's own sentence about the entity"

	for _, tc := range []struct {
		name         string
		ruleID, code string
		wantIdentity string
		wantRow      string
		// wantHaltMessage is the message a halting finding naming no condition
		// carries; it is unused for a row whose rule names one, which the halt
		// raises as that condition.
		wantHaltMessage string
	}{
		{
			name:         "control: a declared rule and a code of its own — the rule, led by its condition's short code",
			ruleID:       rule,
			code:         "code-of-another-linter",
			wantIdentity: rule,
			wantRow:      "warn: " + short + " " + rule + ": " + message,
		},
		{
			name:            "a rule no condition names and a code of its own: the rule",
			ruleID:          "rule-of-another-linter",
			code:            "code-of-another-linter",
			wantIdentity:    "rule-of-another-linter",
			wantRow:         "warn: rule-of-another-linter: " + message,
			wantHaltMessage: "rule-of-another-linter: " + message,
		},
		{
			name:            "a rule no condition names and no code of its own: the rule",
			ruleID:          "rule-of-another-linter",
			wantIdentity:    "rule-of-another-linter",
			wantRow:         "warn: rule-of-another-linter: " + message,
			wantHaltMessage: "rule-of-another-linter: " + message,
		},
		{
			name:            "a code of its own and no rule: the code",
			code:            "code-of-another-linter",
			wantIdentity:    "code-of-another-linter",
			wantRow:         "warn: code-of-another-linter: " + message,
			wantHaltMessage: "code-of-another-linter: " + message,
		},
		{
			name:            "neither a rule nor a code of its own: no identity, and the row names none",
			wantRow:         "warn: " + message,
			wantHaltMessage: message,
		},
		{
			name:            "a code of its own spelling a declared rule's identity, and no rule: the code, with no short code",
			code:            rule,
			wantIdentity:    rule,
			wantRow:         "warn: " + rule + ": " + message,
			wantHaltMessage: rule + ": " + message,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			advisory := FindingFrom(abi.Diagnostic{
				Severity: abi.SeverityWarn,
				Message:  message,
				RuleID:   tc.ruleID,
				Code:     tc.code,
			}, "component.yaml")

			if got := advisory.Identity(); got != tc.wantIdentity {
				t.Errorf("Identity() = %q; want %q", got, tc.wantIdentity)
			}
			if got := advisoryLine(advisory); got != tc.wantRow {
				t.Errorf("advisoryLine row = %q\n                   want %q", got, tc.wantRow)
			}

			halting := advisory
			halting.Severity = abi.SeverityHalt
			err := raiseFinding(halting, "Component")
			if err == nil {
				t.Fatal("raiseFinding returned no error for a halting finding")
			}
			if _, named := ConditionForRule(tc.ruleID); named {
				if got, _ := cerrors.CodeOf(err); got != tc.wantIdentity {
					t.Errorf("the halt names the condition %q; the rule names %q", got, tc.wantIdentity)
				}
				if got, _ := cerrors.ShortCodeOf(err); got != short {
					t.Errorf("the halt carries the short code %q; the rule's condition declares %q", got, short)
				}
				return
			}
			if code, coded := cerrors.CodeOf(err); coded {
				t.Errorf("a halting finding whose rule names no condition names the condition %q", code)
			}
			var occurrence *cerrors.CompilerError
			if !errors.As(err, &occurrence) {
				t.Fatalf("the halt is not a compiler diagnostic, so its message cannot be read: %v", err)
			}
			if got := occurrence.Message(); got != tc.wantHaltMessage {
				t.Errorf("the halt's message = %q\n                want %q", got, tc.wantHaltMessage)
			}
		})
	}
}
