// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package constraint

import (
	"fmt"
	"strings"
	"testing"
)

// The caption convention, and the two things a test of it has to establish.
//
// A rule's registered text names one thing, so the value that thing refers to is
// raised bare and every other value the rule supplies carries a caption. That is
// what makes a message readable when a rule names two values of the same shape:
// two bare quoted filenames, or two bare quoted words, are indistinguishable to
// the reader the message is for.
//
// The second thing matters as much and is easier to leave unproven: a rule that
// captions nothing must read exactly as it did when captions were not expressible
// at all. A framework change that quietly moved every other rule's message would
// be a change to output nobody asked for, on the axis this compiler treats as a
// contract.

// TestADetailValueReadsByWhetherTheRuleCaptionedIt drives the rendering over the
// four shapes a rule can raise.
//
// The rows are the grid rather than a sample: no detail at all, one bare value,
// several bare values, one captioned value, and a mix — because the mix is the
// shape the convention exists for and the shape a renderer handling only the two
// pure cases would get wrong.
func TestADetailValueReadsByWhetherTheRuleCaptionedIt(t *testing.T) {
	t.Parallel()

	p := newProvenance("aggregate.yaml", authoredKeySpec, authoredKeyComponents, "web")

	for _, tc := range []struct {
		name   string
		detail []DetailValue
		want   string
	}{
		{
			name: "no detail reads as the registered wording alone",
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
			// The shape the convention is for. The registered wording names the
			// pillar reference, so it stands bare; the artifact is a second value
			// the wording does not name, so it says what it is. Rendered the other
			// way round — both bare — a reader is handed two quoted strings and no
			// way to tell which is the reference and which the file.
			name: "a bare value and a captioned one mix in one message",
			detail: []DetailValue{
				Uncaptioned(".workload"),
				Captioned("artifact", "policy.yaml"),
			},
			want: `rule broken (".workload", artifact "policy.yaml")`,
		},
		{
			// A value the check supplied empty stays visible rather than vanishing
			// into the separator, captioned or not.
			name: "an empty value stays visible",
			detail: []DetailValue{
				Uncaptioned(""),
				Captioned("socket", ""),
			},
			want: `rule broken ("", socket "")`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ObjectionMessage(p.RaiseWith(errRuleBroken, tc.detail...))
			if got != tc.want {
				t.Errorf("the message reads\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}
}

// TestAnUncaptionedRaiseIsSpelledTheSameThroughBothForms is the byte-identity
// assertion for every rule that captions nothing.
//
// The two forms are compared against each other AND against a spelling composed
// here, and the second comparison is what makes the first mean anything: two
// paths through one construction agree by construction, so an agreement between
// them says only that the widening happened, never that what it produced is the
// spelling a rule's readers already have.
//
// The composed spelling is a deliberate second copy of the format. A comparison
// taking the format from the code it checks agrees with whatever that code came to
// do, which is the one thing this must not do — so the copy is the instrument and
// unifying it would blind the test. It is also calibrated below rather than
// trusted: a comparison nobody showed failing is a comparison that might not be
// able to.
func TestAnUncaptionedRaiseIsSpelledTheSameThroughBothForms(t *testing.T) {
	t.Parallel()

	p := newProvenance("recipes/app.yaml", authoredKeySpec, authoredKeyArtifacts)

	for _, values := range [][]string{
		nil,
		{"db_size"},
		{"env", "ssm_param"},
		{"a value with spaces, and a comma", ""},
	} {
		narrow := ObjectionMessage(p.Raise(errRuleBroken, values...))

		wide := make([]DetailValue, 0, len(values))
		for _, v := range values {
			wide = append(wide, Uncaptioned(v))
		}
		widened := ObjectionMessage(p.RaiseWith(errRuleBroken, wide...))

		if narrow != widened {
			t.Errorf("the two raise forms spell %v as\n  %s\nand\n  %s\n"+
				"INVARIANT: a rule that captions nothing reads the same whichever form it raised "+
				"through.\nCONSEQUENCE: otherwise reaching for the wide form to caption ONE value "+
				"silently respells every other value the rule already reported.",
				values, narrow, widened)
		}
		if want := composedUncaptioned(values); narrow != want {
			t.Errorf("an uncaptioned raise of %v reads\n  %s\nwant\n  %s\n"+
				"CONSEQUENCE: every rule that captions nothing — which is nearly all of them — "+
				"reports differently than its readers already have it.", values, narrow, want)
		}
	}

	// The calibration. The comparison above must be able to fail, and the way it
	// would matter is precisely a value captioned where the rule's registered
	// wording already names it: that is the change a rule author can make by
	// reaching for the wide form without asking whether the value needs a caption.
	// If this needle passes the comparison, the assertions above prove nothing.
	captioned := ObjectionMessage(p.RaiseWith(errRuleBroken, Captioned("pillar", "db_size")))
	if unwanted := composedUncaptioned([]string{"db_size"}); captioned == unwanted {
		t.Errorf("a captioned value rendered as %s, which the uncaptioned spelling also produces — "+
			"so the comparison above cannot see a caption appearing where none belongs, and its "+
			"agreement is vacuous", captioned)
	}
}

// composedUncaptioned spells the uncaptioned message independently of the code
// under test: the registered wording, then every value quoted, comma-separated,
// in one parenthesis — and the wording alone where there is no value.
//
// It is deliberately a second expression of that format; see the caller.
func composedUncaptioned(values []string) string {
	if len(values) == 0 {
		return errRuleBroken.Error()
	}
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}
	return errRuleBroken.Error() + " (" + strings.Join(quoted, ", ") + ")"
}
