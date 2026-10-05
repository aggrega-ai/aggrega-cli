// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestConventionsEntryNamesAFindingAsTheCommandLineRowDoes holds the code a
// publishing-conventions entry carries to the identity the command line's row
// prints for the same finding, over each combination of the two identifiers a
// finding may carry — the rule that produced it and a code of its own — read as
// the raw keys a client receives.
//
// The rows and their expectations are the ones the lint operation's own table
// holds the command line's row to, written out whole here as well: the row's
// renderer is unexported in that package, so the two surfaces meet in the
// expectations, and an entry that named a finding by any other reading of its
// fields fails here while the row stays green there.
//
// Findings are built through [lintops.FindingFrom], so the short code on each is
// the one the lint operation fills. The first row is the control: a rule the
// compiler declares, carrying a code of its own besides, is named by its rule and
// carries its condition's short code and reference, whatever the code says. The
// last row is a code of its own that spells a declared rule's identity, on a
// finding naming no rule: it names the finding and borrows no short code, because
// which condition a finding names is read off its rule alone.
func TestConventionsEntryNamesAFindingAsTheCommandLineRowDoes(t *testing.T) {
	t.Parallel()

	condition := lintops.ErrCategoryMatchesDirectory()
	rule, ok := cerrors.CodeOf(condition)
	if !ok || rule == "" {
		t.Fatalf("the condition reports no descriptive code, so no row here names a declared rule: %v", condition)
	}
	short, ok := cerrors.ShortCodeOf(condition)
	if !ok || short == "" {
		t.Fatalf("the condition reports no short code, so the control row has nothing to carry: %v", condition)
	}

	for _, tc := range []struct {
		name         string
		ruleID, code string
		// wantCode is the entry's code, and empty where it carries none.
		wantCode string
		// wantShort is the entry's short code, and empty where it carries none.
		wantShort string
	}{
		{
			name:      "control: a declared rule and a code of its own — the rule, with its condition's short code",
			ruleID:    rule,
			code:      "code-of-another-linter",
			wantCode:  rule,
			wantShort: short,
		},
		{
			name:     "a rule no condition names and a code of its own: the rule",
			ruleID:   "rule-of-another-linter",
			code:     "code-of-another-linter",
			wantCode: "rule-of-another-linter",
		},
		{
			name:     "a rule no condition names and no code of its own: the rule",
			ruleID:   "rule-of-another-linter",
			wantCode: "rule-of-another-linter",
		},
		{
			name:     "a code of its own and no rule: the code",
			code:     "code-of-another-linter",
			wantCode: "code-of-another-linter",
		},
		{
			name: "neither a rule nor a code of its own: no code at all",
		},
		{
			name:     "a code of its own spelling a declared rule's identity, and no rule: the code, with no short code",
			code:     rule,
			wantCode: rule,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := lintops.FindingFrom(abi.Diagnostic{
				Severity: abi.SeverityWarn,
				Message:  "the rule's own sentence about the entity",
				RuleID:   tc.ruleID,
				Code:     tc.code,
			}, "component.yaml")
			entry := rawEntry(t, conventionDiagnostic(f))

			code, present := codeOnWire(t, entry)
			switch {
			case tc.wantCode == "" && present:
				t.Errorf("an entry for a finding carrying no identifier carries the code %q", code)
			case tc.wantCode != "" && code != tc.wantCode:
				t.Errorf("code = %q (present %v); the command line's row names the finding %q", code, present, tc.wantCode)
			}

			got, present := shortCodeOnWire(t, entry)
			switch {
			case tc.wantShort == "" && present:
				t.Errorf("an entry whose finding names no registered condition carries the short code %q", got)
			case tc.wantShort != "" && got != tc.wantShort:
				t.Errorf("shortCode = %q (present %v); the rule's condition declares %q", got, present, tc.wantShort)
			}
			if _, linked := pointerOnWire(t, entry); linked != (tc.wantShort != "") {
				t.Errorf("the reference and the short code do not stand together: reference present %v, "+
					"short code %q", linked, tc.wantShort)
			}
		})
	}
}
