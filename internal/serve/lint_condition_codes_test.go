// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// What a publishing-conventions entry carries for the READER: the short code of
// the condition its rule names, which is the code the command line prints at the
// head of the same finding's row, and the reference to where that condition is
// listed. A rule no condition names carries its identity alone, because the
// command line prints no code for it either.

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestConventionsEntryCarriesTheConditionItsRuleNames drives the projection over
// each kind of rule a finding can carry, and reads what reaches the wire as the
// raw keys a client receives.
//
// The expected identifiers of a registered rule are read off the condition
// through its exported accessor, never written here as literals: a literal keeps
// compiling after the condition is renumbered, at which point the assertion
// would go on demanding a code the command line no longer prints.
//
// Two registered rules are driven rather than one, so a projection that wrote
// one condition's codes onto every finding fails on the second.
func TestConventionsEntryCarriesTheConditionItsRuleNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// condition is the registered condition the rule names, or nil for a
		// finding whose rule no condition names.
		condition error
		// ruleID and code are the finding's own identifiers where condition is
		// nil; a registered rule's identifier is read off its condition.
		ruleID, code string
		// wantCode is the machine-matchable code where condition is nil.
		wantCode string
	}{
		{
			name:      "the category-label rule names its registered condition",
			condition: lintops.ErrCategoryMatchesDirectory(),
		},
		{
			name:      "the naming-shape rule names its registered condition",
			condition: lintops.ErrNameShape(),
		},
		{
			name:     "a rule from a linter's own rule set names no condition",
			ruleID:   "rule-of-another-linter",
			wantCode: "rule-of-another-linter",
		},
		{
			name:     "a finding carrying no rule identity falls back to its own code",
			code:     "code-of-another-linter",
			wantCode: "code-of-another-linter",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := lintops.Finding{
				Severity: abi.SeverityWarn,
				Message:  "the rule's own sentence about the entity",
				RuleID:   tc.ruleID,
				Code:     tc.code,
			}
			var wantShort string
			if tc.condition != nil {
				descriptive, ok := cerrors.CodeOf(tc.condition)
				if !ok {
					t.Fatalf("the condition reports no descriptive code, so this row names no rule: %v", tc.condition)
				}
				short, ok := cerrors.ShortCodeOf(tc.condition)
				if !ok || short == "" {
					t.Fatalf("the condition reports no short code, so this row has nothing to look for: %v", tc.condition)
				}
				f.RuleID = descriptive
				tc.wantCode = descriptive
				wantShort = short
			}

			entry := rawEntry(t, conventionDiagnostic(f))

			code, present := codeOnWire(t, entry)
			if !present || code != tc.wantCode {
				t.Errorf("code = %q (present %v); want the rule's identity %q", code, present, tc.wantCode)
			}

			short, present := shortCodeOnWire(t, entry)
			href, linked := pointerOnWire(t, entry)
			if wantShort == "" {
				if present {
					t.Errorf("an entry whose rule names no registered condition carries the short code %q; "+
						"the command line prints none for it, and no lookup resolves one", short)
				}
				if linked {
					t.Errorf("an entry whose rule names no registered condition carries a reference (%q), "+
						"which would open a page that does not describe it", href)
				}
				return
			}
			if !present {
				t.Fatalf("an entry whose rule names a registered condition carries no short code; the "+
					"command line prints %s for the same finding", wantShort)
			}
			if short != wantShort {
				t.Errorf("shortCode = %q; the condition the rule names declares %q", short, wantShort)
			}
			if !linked {
				t.Error("an entry carrying a short code carries no reference to where it is listed")
			} else if href != haltref.ExplanationURL() {
				t.Errorf("codeDescription.href = %q; want the published address (%q)", href, haltref.ExplanationURL())
			}
			// The identifiers travel as keys, never folded into the sentence.
			if message := messageOnWire(t, entry); strings.Contains(message, short) {
				t.Errorf("the message carries the short code %q; it travels as its own key:\n%s", short, message)
			}
		})
	}
}

// TestLintAnswerQuotesTheShortCodeTheCommandLinePrints holds the answer to an
// aggrega/lint request against the rows the lint operation writes for the same
// entity — the rows `aggrega lint` prints — finding for finding.
//
// The oracle is the command line's own bytes rather than the condition table, so
// this fails on a projection that agreed with the table and disagreed with the
// terminal: whatever short code a row opens with is the one the entry must
// carry, and an entry must carry none where its row opens with none.
func TestLintAnswerQuotesTheShortCodeTheCommandLinePrints(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	srv := conventionsServer(t, root, false)

	res := lintOverTheWire(t, srv, lintParams{Entity: rel})
	rows := commandLineRows(t, root, rel)

	compared, quoted := 0, 0
	for _, group := range res.Files {
		for _, d := range group.Diagnostics {
			tail := " " + d.Code + ": " + d.Message
			at := strings.Index(rows, tail)
			if d.Code == "" || at < 0 {
				t.Errorf("the answer carries [%s] %q and the command line printed no row for it; the rows "+
					"were:\n%s", d.Code, d.Message, rows)
				continue
			}
			head := rows[strings.LastIndex(rows[:at], "\n")+1 : at]
			want, _ := haltref.ShortCodeIn(head)
			compared++
			if d.ShortCode != want {
				t.Errorf("[%s] the answer quotes the short code %q and the command line's row opens %q",
					d.Code, d.ShortCode, head)
			}
			if (d.CodeDescription != nil) != (d.ShortCode != "") {
				t.Errorf("[%s] the reference and the short code do not stand together: shortCode %q, "+
					"codeDescription %+v", d.Code, d.ShortCode, d.CodeDescription)
			}
			if want != "" {
				quoted++
			}
		}
	}
	// Non-vacuity: an answer with no findings, or one whose rows all carried no
	// code, would satisfy every comparison above while proving nothing about the
	// short code.
	if compared == 0 || quoted == 0 {
		t.Fatalf("compared %d finding(s), %d of them with a short code on the command line's row; this "+
			"fixture must draw at least one finding whose rule names a registered condition. The rows "+
			"were:\n%s", compared, quoted, rows)
	}
}

// commandLineRows runs the lint operation over the entity at rel and returns
// what it writes for the findings, which is what `aggrega lint` prints on its
// diagnostic stream for the same entity.
func commandLineRows(t *testing.T, root, rel string) string {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace at %s: %v", root, err)
	}
	var rows bytes.Buffer
	collect := &lintops.FindingCollector{}
	// The verdict is dropped: in this mode every finding is advisory, and what an
	// oracle for the rows owes is the rows.
	_ = lintops.RunEntity(t.Context(), lintops.Config{
		EntityPath: filepath.Join(root, rel),
		Workspace:  rw,
		Out:        io.Discard,
		DiagOut:    &rows,
		Collect:    collect,
	})
	if !collect.Reached() {
		t.Fatal("the lint operation never got as far as reporting, so it printed no rows to compare against")
	}
	return rows.String()
}
