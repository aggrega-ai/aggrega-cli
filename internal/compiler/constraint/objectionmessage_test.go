// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"fmt"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The message a rule reports through, and the two sentinels it has to hold.
//
// A rule's message is the rule's registered wording, then the values the check
// supplied. The wording is read off the condition in the sentinel's chain, which
// is what every diagnostic naming a condition does, so a rule cannot come to
// state its own wording differently from the way the rest of the compiler states
// it.
//
// That read has a second case, and it is the one a test is needed for: a sentinel
// that carries no registered condition. A rule under construction has one, and so
// does every rule a test stands in for. Such a rule still has words, and a message
// that dropped them would open with a parenthesis and tell a reader which values
// were wrong without telling them which rule said so.
//
// The tests below hold both cases to the same shape. The sibling tests covering
// the caption convention raise through a sentinel of the second kind, so the
// first kind — which is the kind every shipped rule raises — is what these add.

// errRegisteredRule stands for a rule whose identity is registered, which is what
// every rule the compiler ships raises through.
//
// It is registered here rather than borrowed from a package that declares a real
// condition, so this file states its own subject and no assertion below moves when
// a real condition's wording is edited. Its code and short code have to be
// distinct from those of the conditions this test binary links and from nothing
// else: registration is per-process, so neither value is an allocation.
var errRegisteredRule = cerrors.Register("registered-rule", cerrors.Meta{
	ShortCode: "E9301",
	Message:   "rule broken",
})

// TestAMessageReadsAsTheRulesWordingThenItsValues drives the message over the
// shapes a rule can raise, through a sentinel whose identity is registered.
//
// The rows are the grid rather than a sample: no detail, one bare value, several,
// one captioned, a mix, a value carrying a comma and a space, and an empty value.
// A value carrying the separator is the row that catches a renderer that joined
// without quoting, and an empty value is the row that catches one that let a value
// vanish into the separator.
func TestAMessageReadsAsTheRulesWordingThenItsValues(t *testing.T) {
	t.Parallel()

	p := newProvenance("aggregate.yaml", authoredKeySpec, authoredKeyComponents, "web")

	for _, tc := range []struct {
		name   string
		detail []DetailValue
		want   string
	}{
		{
			name: "no detail reads as the wording alone",
			want: "rule broken",
		},
		{
			name:   "one bare value",
			detail: []DetailValue{Uncaptioned(".workload")},
			want:   `rule broken (".workload")`,
		},
		{
			name:   "several bare values keep the order the rule supplied",
			detail: []DetailValue{Uncaptioned("first"), Uncaptioned("second")},
			want:   `rule broken ("first", "second")`,
		},
		{
			name:   "one captioned value reads as its caption then the value",
			detail: []DetailValue{Captioned("artifact", "policy.yaml")},
			want:   `rule broken (artifact "policy.yaml")`,
		},
		{
			name: "a bare value and a captioned one mix in one message",
			detail: []DetailValue{
				Uncaptioned(".workload"),
				Captioned("artifact", "policy.yaml"),
			},
			want: `rule broken (".workload", artifact "policy.yaml")`,
		},
		{
			name:   "a value carrying the separator stays one value",
			detail: []DetailValue{Uncaptioned("a value with spaces, and a comma"), Uncaptioned("plain")},
			want:   `rule broken ("a value with spaces, and a comma", "plain")`,
		},
		{
			name:   "an empty value stays visible",
			detail: []DetailValue{Uncaptioned(""), Captioned("socket", "")},
			want:   `rule broken ("", socket "")`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ObjectionMessage(p.RaiseWith(errRegisteredRule, tc.detail...))
			if got != tc.want {
				t.Errorf("the message reads\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

// TestARulesWordingReadsTheSameWhetherOrNotItsIdentityIsRegistered is the
// equality that keeps the two sentinel cases one shape.
//
// A rule reports the same way whichever kind of sentinel it raises through, so a
// registration added to a rule that already ships must not respell any message it
// already reports — and neither must one removed. The two are compared against
// each other AND against a spelling composed here, because two paths that agree
// say only that they agree: the composed spelling is what says the shape they
// agree on is the one a rule's readers already have.
func TestARulesWordingReadsTheSameWhetherOrNotItsIdentityIsRegistered(t *testing.T) {
	t.Parallel()

	p := newProvenance("recipes/app.yaml", authoredKeySpec, authoredKeyArtifacts)

	for _, values := range [][]string{
		nil,
		{"db_size"},
		{"env", "ssm_param"},
		{"a value with spaces, and a comma", ""},
	} {
		registered := ObjectionMessage(p.Raise(errRegisteredRule, values...))
		unregistered := ObjectionMessage(p.Raise(errRuleBroken, values...))

		if registered != unregistered {
			t.Errorf("a rule raising %v reports\n  %s\nwith its identity registered and\n  %s\nwithout.\n"+
				"INVARIANT: whether a rule's identity is registered is invisible in the message it reports.\n"+
				"CONSEQUENCE: otherwise registering a rule that already ships silently respells every "+
				"message it already reported.", values, registered, unregistered)
		}
		if want := composedRuleMessage(values); registered != want {
			t.Errorf("a rule raising %v reports\n  %s\nwant\n  %s\n"+
				"CONSEQUENCE: every rule reports differently than its readers already have it.",
				values, registered, want)
		}
	}

	// The calibration. A comparison nobody showed failing is a comparison that
	// might not be able to fail, and the way this one would matter is a value
	// captioned where the rule's wording already names it.
	captioned := ObjectionMessage(p.RaiseWith(errRegisteredRule, Captioned("pillar", "db_size")))
	if unwanted := composedRuleMessage([]string{"db_size"}); captioned == unwanted {
		t.Errorf("a captioned value rendered as %s, which the uncaptioned spelling also produces — "+
			"so the comparisons above cannot see a caption appearing where none belongs, and their "+
			"agreement is vacuous", captioned)
	}
}

// TestTheWordingComesFromTheRuleAndNotFromAFrameAboveIt pins which text a
// sentinel contributes when something has wrapped it on its way to the raise.
//
// A wrapping frame says whatever that frame chose to say, and a rule's wording is
// what the rule was registered under. Reading the outermost frame instead would
// make a message state a rule's wording as whatever the last hand to touch the
// error wrote, and the two agree on the day the wrap is added — which is exactly
// what would hide the divergence the day either is edited.
func TestTheWordingComesFromTheRuleAndNotFromAFrameAboveIt(t *testing.T) {
	t.Parallel()

	p := newProvenance("aggregate.yaml", authoredKeySpec, authoredKeyComponents, "web")
	wrapped := fmt.Errorf("a frame above the rule saying something else: %w", errRegisteredRule)

	got := ObjectionMessage(p.Raise(wrapped, "db_size"))
	if want := composedRuleMessage([]string{"db_size"}); got != want {
		t.Errorf("a wrapped sentinel reports\n  %s\nwant\n  %s", got, want)
	}
	if strings.Contains(got, "a frame above the rule") {
		t.Errorf("the wrapping frame's own text must not be reported as the rule's wording; got %s", got)
	}
}

// composedRuleMessage spells the message independently of the code under test:
// the rule's wording, then every value quoted, comma-separated, in one
// parenthesis — and the wording alone where there is no value.
//
// It is deliberately a second expression of that shape. A comparison taking the
// shape from the code it checks agrees with whatever that code came to do, which
// is the one thing it must not do, so the copy is the instrument.
func composedRuleMessage(values []string) string {
	if len(values) == 0 {
		return errRegisteredRule.Error()
	}
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}
	return errRegisteredRule.Error() + " (" + strings.Join(quoted, ", ") + ")"
}
