// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
)

// TestComponentIDLabelVerdict_JudgesTheValueThatWillBeStamped is the clause the
// whole verdict turns on: what is measured is the value the label carries, not
// the value the question arrived carrying.
//
// The two differ on one path only. An identity a person wrote is written into
// the identity alphabet where it is stamped, so the character joining a module's
// name to a component's own costs three written where it cost one proposed. An
// identity the compiler composed is stamped exactly as proposed. Measuring the
// proposal on the first path accepts an identity just inside the limit and stamps
// a value just outside it, at exit zero, with nothing reporting it.
//
// Each row states its own arithmetic in its name, so a failure says which
// direction moved rather than only that a number did.
func TestComponentIDLabelVerdict_JudgesTheValueThatWillBeStamped(t *testing.T) {
	t.Parallel()

	// A qualified identity: a module's name, a dot, and the component's own. The
	// dot is the one character no authored name may carry, and it is the reason the
	// written form is longer than the proposal.
	const qualified = "documents."
	qualifiedOf := func(n int) string {
		return qualified + strings.Repeat("a", n-len(qualified))
	}

	for _, tc := range []struct {
		name     string
		proposed string
		authored bool
		usable   bool
		bound    int // read only when usable is false
	}{
		{
			name:     "an authored qualified identity whose written form is exactly the limit is usable",
			proposed: qualifiedOf(componentIDLabelMaxLength - 2),
			authored: true,
			usable:   true,
		},
		{
			name:     "an authored qualified identity one byte longer is refused, at the measured bound",
			proposed: qualifiedOf(componentIDLabelMaxLength - 1),
			authored: true,
			usable:   false,
			bound:    componentIDLabelMaxLength - 2,
		},
		{
			name:     "an authored identity carrying no join is measured as proposed",
			proposed: strings.Repeat("a", componentIDLabelMaxLength),
			authored: true,
			usable:   true,
		},
		{
			name:     "an authored identity carrying no join is refused at the limit itself",
			proposed: strings.Repeat("a", componentIDLabelMaxLength+1),
			authored: true,
			usable:   false,
			bound:    componentIDLabelMaxLength,
		},
		{
			// The composed form opens with its scope behind a two-hyphen join, and
			// writing it again would write that join out — five bytes for the opening
			// becoming seven. Judging it verbatim is what keeps one identity spelled
			// one way, and this row goes red the day both branches are written.
			name:     "a composed identity is judged verbatim, however it would write",
			proposed: "bfr--" + strings.Repeat("a", componentIDLabelMaxLength-5),
			authored: false,
			usable:   true,
		},
		{
			name:     "a composed identity over the limit is refused at the limit itself",
			proposed: "bfr--" + strings.Repeat("a", componentIDLabelMaxLength-4),
			authored: false,
			usable:   false,
			bound:    componentIDLabelMaxLength,
		},
		{
			name:     "an empty identity is nothing to refuse",
			proposed: "",
			authored: true,
			usable:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ComponentIDLabelVerdict(tc.proposed, tc.authored)
			if got.Usable != tc.usable {
				t.Fatalf("proposed %d bytes (authored=%v): usable=%v, want %v (reason %q)",
					len(tc.proposed), tc.authored, got.Usable, tc.usable, got.Reason)
			}
			if tc.usable {
				return
			}
			if got.Bound == nil {
				t.Fatalf("a refusal a shorter identity would satisfy must state the length that would work; "+
					"reason %q", got.Reason)
			}
			if *got.Bound != tc.bound {
				t.Errorf("stated bound %d, want %d", *got.Bound, tc.bound)
			}
			// The bound has to be ACTIONABLE, which is the whole reason it is measured
			// rather than restated: an author who writes a name of the stated length has
			// to be accepted. Asserting the number alone would pass on a bound nobody
			// could act on.
			again := ComponentIDLabelVerdict(tc.proposed[:*got.Bound], tc.authored)
			if !again.Usable {
				t.Errorf("an identity cut to the stated bound of %d is still refused: %s",
					*got.Bound, again.Reason)
			}
		})
	}
}

// TestComponentIDLabelVerdict_TheLimitAloneWouldNotHaveCaughtTheAuthoredCase is
// the proof that the assertion above can fail.
//
// A criterion satisfied by "the verdict now reads the flag" passes while nothing
// measures differently. What this pins is the gap itself: an authored qualified
// identity sits INSIDE the limit as proposed and OUTSIDE it as stamped, so a
// verdict measuring the proposal returns usable and the label is written illegal.
// Both halves are asserted, so the case cannot quietly stop being the case.
func TestComponentIDLabelVerdict_TheLimitAloneWouldNotHaveCaughtTheAuthoredCase(t *testing.T) {
	t.Parallel()

	proposed := "documents." + strings.Repeat("a", componentIDLabelMaxLength-1-len("documents."))
	if len(proposed) > componentIDLabelMaxLength {
		t.Fatalf("the case needs an identity inside the limit as proposed; this one is %d", len(proposed))
	}
	stamped := labels.EncodeNodeIdentity(proposed)
	if len(stamped) <= componentIDLabelMaxLength {
		t.Fatalf("the case needs a stamped value outside the limit; %q is %d", stamped, len(stamped))
	}

	got := ComponentIDLabelVerdict(proposed, true)
	if got.Usable {
		t.Fatalf("an identity of %d stamped as %d was accepted; the label would carry %d bytes at exit zero",
			len(proposed), len(stamped), len(stamped))
	}
	if got.Bound == nil {
		t.Fatal("the refusal states no length that would work, so an author is sent to guess")
	}
	// The measured bound and the limit are DIFFERENT numbers here, and reporting the
	// limit would send an author to a length that is refused again.
	if *got.Bound == componentIDLabelMaxLength {
		t.Errorf("the stated bound is the limit itself; an identity of %d is stamped as %d and refused again",
			componentIDLabelMaxLength, len(labels.EncodeNodeIdentity(proposed[:componentIDLabelMaxLength])))
	}
	for _, want := range []string{"at most 63", "of 62", "stamped as 64"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("the reason does not carry %q, so the gap between the two lengths is not readable; got %q",
				want, got.Reason)
		}
	}
	t.Logf("proposed %q (%d) is stamped as %q (%d); bound %d; reason: %s",
		proposed, len(proposed), stamped, len(stamped), *got.Bound, got.Reason)
}

// TestComponentIDLabelVerdict_RefusesOnWhatTheIdentityAsProposedCarries covers
// the two objections no shortening repairs, and they are judged on the identity
// AS PROPOSED rather than on the value stamped — which is where they part company
// with the length.
//
// The split is deliberate and this test is what pins it. Writing the identity can
// only remove a character the label charset refuses, so judging the written form
// would accept identities the proposed form turns away. Keeping the stricter read
// keeps every refusal this verdict already made: a refusal withdrawn is a change
// in what a build accepts, which is a wider question than the length, and it
// concerns names carrying no composition separator at all.
//
// The trailing-hyphen row is the one that would move, so it is asserted in the
// direction that holds and commented with what the other direction would mean.
func TestComponentIDLabelVerdict_RefusesOnWhatTheIdentityAsProposedCarries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		proposed string
		usable   bool
	}{
		{name: "an underscore at the start survives the writing and is refused", proposed: "_api", usable: false},
		{name: "a hyphen at the start survives the writing and is refused", proposed: "-api", usable: false},
		{name: "an underscore at the end survives the writing and is refused", proposed: "api_", usable: false},
		{name: "an ordinary authored name passes through byte for byte", proposed: "payments-api", usable: true},
		{name: "a qualified identity is stamped as letters, digits and hyphens", proposed: "documents.backend", usable: true},
		{
			// A trailing hyphen is the one shape where the two reads disagree, so it is
			// the row that pins which one is in force. The writing would escape a hyphen
			// it cannot leave at the end, making a stamped value that ends on a letter
			// and that the target would accept — so a verdict reading the written form
			// would accept this identity. It is refused instead, because the grammar
			// checks read the identity as proposed and withdrawing a refusal is a change
			// in what a build accepts rather than a tidying of this one.
			name:     "a trailing hyphen is refused as proposed, though its written form would be accepted",
			proposed: "api-", usable: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ComponentIDLabelVerdict(tc.proposed, true); got.Usable != tc.usable {
				t.Errorf("proposed %q stamped as %q: usable=%v, want %v (reason %q)",
					tc.proposed, labels.EncodeNodeIdentity(tc.proposed), got.Usable, tc.usable, got.Reason)
			}
		})
	}
}
