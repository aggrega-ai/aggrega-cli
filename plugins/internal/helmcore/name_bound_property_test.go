// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// helmOracle answers the name-bound question with the SHIPPED helm provider's
// own logic rather than a stub.
//
// Driving the real answer is the point: a stub proves the loop terminates
// against whatever the stub says, and says nothing about whether the loop and
// the provider that actually ships agree. The two halves are written on opposite
// sides of the plugin boundary and are meant to be able to disagree, so the only
// proof that they do not is to run one against the other.
type helmOracle struct{ passes int }

func (o *helmOracle) NameBound(_ context.Context, in renderer.NameBoundInput) (renderer.NameBoundOutput, error) {
	o.passes++
	return helmcore.NameBoundOutput(in), nil
}

// TestComposedIdentitiesSettleWithinWhatTheHelmTargetAccepts is the clause the
// item turns on: the outcome is that the name is never too long, not that a halt
// catches it.
//
// Every composed identity in the corpus is settled through the real exchange and
// then re-put to the same provider. The assertion is that the provider accepts
// what came back — every kind, not only the length — so a settled identity is one
// the target agreed to rather than one that merely satisfied the compiler.
//
// The corpus is the shared segment corpus crossed with parts long enough to force
// shortening, so both paths are exercised: identities that fit as composed, and
// identities that only exist because the loop shortened them.
func TestComposedIdentitiesSettleWithinWhatTheHelmTargetAccepts(t *testing.T) {
	t.Parallel()

	instances := append(namesegmenttest.Corpus('-'),
		strings.Repeat("instance", 12),
		strings.Repeat("i", 200),
		"bridge", "b", "a-b", "a_b", "a.b",
	)
	leading := [][]string{
		nil,
		{"cluster"},
		{"cluster", "east"},
		{strings.Repeat("c", 90)},
		{strings.Repeat("region", 20), strings.Repeat("zone", 20)},
		{"a/b", "c.d"},
	}

	settled := 0
	shortened := 0
	for _, scope := range []labels.ComponentIdentityScope{labels.BoundaryFeatureScope, labels.AttachmentScope} {
		for _, instance := range instances {
			for _, lead := range leading {
				parts := append(append([]string{}, lead...), instance)

				oracle := &helmOracle{}
				got, err := renderer.SettleComposedComponentIdentity(context.Background(), oracle, scope, "helm", nil, parts...)
				if err != nil {
					t.Errorf("scope %q parts %v: the exchange must reach a name the target accepts: %v",
						scope, parts, err)
					continue
				}
				settled++
				if got != labels.ComposeComponentIdentity(scope, labels.Unbounded, parts...) {
					shortened++
				}

				// Re-put the settled identity to the same provider. Asserting the
				// provider's own verdict rather than a length here is what makes this a
				// proof about the agreement instead of about one number: a grammar
				// refusal, a label-value refusal and a length refusal all fail it.
				for _, verdict := range helmcore.NameBoundOutput(rendercore.NameBoundInput{Proposed: got}).Names {
					if !verdict.Usable {
						t.Errorf("scope %q parts %v settled on %q, which the provider still refuses as a %s: %s",
							scope, parts, got, verdict.Kind, verdict.Reason)
					}
				}
			}
		}
	}

	// A corpus that never reached the shortening path would pass this while proving
	// nothing about it, and nothing in the assertions above would say so.
	if shortened == 0 {
		t.Errorf("none of the %d settled identities were shortened, so the shortening path is unexercised "+
			"and this proves only that short names are short", settled)
	}
	t.Logf("settled %d identities, %d of them by shortening", settled, shortened)
}

// TestHelmVerdicts_ReportEveryKindTheProviderDerives asserts the provider answers
// about every kind it derives rather than only the one that refused.
//
// A guest reporting only its refusals would leave a host unable to tell "usable"
// from "I did not look", and would need a second call to learn the rest.
func TestHelmVerdicts_ReportEveryKindTheProviderDerives(t *testing.T) {
	t.Parallel()

	out := helmcore.NameBoundOutput(rendercore.NameBoundInput{Proposed: "web", Authored: true})
	kinds := make([]string, 0, len(out.Names))
	for _, v := range out.Names {
		kinds = append(kinds, v.Kind)
		if !v.Usable {
			t.Errorf("an ordinary authored name is refused as a %s: %s", v.Kind, v.Reason)
		}
	}
	for _, want := range []string{helmcore.ReleaseNameKind, helmcore.ServiceNameKind, rendercore.ComponentIDLabelKind} {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the provider reports no verdict for %q; it derives one, so a host reading these "+
				"verdicts would settle a name that kind refuses", want)
		}
	}
}

// TestHelmVerdicts_SeparateALengthRefusalFromAGrammarRefusal pins the two answers
// the whole design rests on telling apart.
//
// A name that is merely too long comes back with a bound, and the compiler
// shortens. A name whose first character the alphabet refuses comes back with
// none, and the compiler stops — because every escape opens with a hyphen, so no
// shortening reaches it. Collapsing the two would either shorten a name forever
// or refuse one a shorter form would have satisfied.
func TestHelmVerdicts_SeparateALengthRefusalFromAGrammarRefusal(t *testing.T) {
	t.Parallel()

	// Six capitals: the shipping breach. The first character is escaped, so no
	// length repairs it.
	for _, v := range helmcore.NameBoundOutput(rendercore.NameBoundInput{Proposed: "ABCDEF", Authored: true}).Names {
		if v.Kind != helmcore.ReleaseNameKind {
			continue
		}
		if v.Usable {
			t.Fatal("a six-capital name must be refused: it encodes to 54 characters and opens with a hyphen")
		}
		if v.Bound != nil {
			t.Errorf("a name whose first character is escaped came back bounded to %d; no length repairs it, "+
				"and offering a bound sends the author to shorten a name that would still be refused", *v.Bound)
		}
	}

	// Lowercase and long: a length refusal, which a shorter name does repair.
	long := strings.Repeat("a", 60)
	sawBound := false
	for _, v := range helmcore.NameBoundOutput(rendercore.NameBoundInput{Proposed: long, Authored: true}).Names {
		if v.Kind != helmcore.ReleaseNameKind {
			continue
		}
		if v.Usable {
			t.Fatalf("a %d-character lowercase name must be refused as a release name", len(long))
		}
		if v.Bound == nil {
			t.Fatal("a name refused only for its length must state the length that would work")
		}
		sawBound = true
		if *v.Bound < 1 {
			t.Errorf("the stated bound is %d, which leaves nothing to compose", *v.Bound)
		}
		// The bound has to be actionable: a name cut to it must actually pass.
		for _, again := range helmcore.NameBoundOutput(rendercore.NameBoundInput{Proposed: long[:*v.Bound], Authored: true}).Names {
			if again.Kind == helmcore.ReleaseNameKind && !again.Usable {
				t.Errorf("a name cut to the stated bound of %d is still refused: %s", *v.Bound, again.Reason)
			}
		}
	}
	if !sawBound {
		t.Error("the provider reported no release-name verdict for a name well over the limit")
	}
}

// chartOracle answers with the SHIPPED helm-chart provider's logic: the
// component-id label value alone, since that provider copies a chart's own
// resources through rather than deriving a release name of its own.
//
// It is here rather than beside that provider because the property under test is
// the same one — a composed identity settles within what the target accepts —
// and stating it twice in two packages would be two copies of one claim.
type chartOracle struct{}

func (chartOracle) NameBound(_ context.Context, in renderer.NameBoundInput) (renderer.NameBoundOutput, error) {
	return renderer.NameBoundOutput{
		Names: []rendercore.DerivedNameVerdict{rendercore.ComponentIDLabelVerdict(in.Proposed, in.Authored)},
	}, nil
}

// TestComposedIdentitiesSettleWithinTheLabelValueBound is the regression guard
// for every target that stamps the component identity into a bounded label and
// derives no other name from it.
//
// Such a target has exactly one thing to say, and saying nothing is not the same
// as saying it is unbounded: the compiler holds no target's limits, so a target
// that stamps a bounded name and declines the question gets a name nobody
// bounded. This asserts the settled identity is one the label rule accepts, over
// a corpus whose parts are long enough to force shortening.
func TestComposedIdentitiesSettleWithinTheLabelValueBound(t *testing.T) {
	t.Parallel()

	shortened := 0
	for _, scope := range []labels.ComponentIdentityScope{labels.BoundaryFeatureScope, labels.AttachmentScope} {
		for _, parts := range [][]string{
			{"cluster", "bridge"},
			{strings.Repeat("region", 20), strings.Repeat("bridge", 20)},
			{strings.Repeat("c", 90), strings.Repeat("i", 90)},
			{"a/b", "c.d", strings.Repeat("instance", 12)},
		} {
			got, err := renderer.SettleComposedComponentIdentity(context.Background(), chartOracle{},
				scope, "helm-chart", nil, parts...)
			if err != nil {
				t.Errorf("scope %q parts %v: the exchange must converge: %v", scope, parts, err)
				continue
			}
			if got != labels.ComposeComponentIdentity(scope, labels.Unbounded, parts...) {
				shortened++
			}
			if v := rendercore.ComponentIDLabelVerdict(got, false); !v.Usable {
				t.Errorf("scope %q parts %v settled on %q (%d characters), which the label rule still refuses: %s",
					scope, parts, got, len(got), v.Reason)
			}
		}
	}
	if shortened == 0 {
		t.Error("no case reached the shortening path, so this proves only that short names are short")
	}
}

// qualifiedIdentity is a component identity of n bytes carrying one join: a
// module's name, a dot, and the component's own. It is the only shape a compiler
// can produce that carries the dot, and it is what makes the written form longer
// than the identity as authored.
func qualifiedIdentity(n int) string {
	const module = "documents."
	return module + strings.Repeat("a", n-len(module))
}

// TestSettleAuthoredComponentIdentity_HaltsWhereTheWrittenLabelWouldNotFit is the
// halt this repair exists to produce, driven through the real settle path and the
// real label verdict rather than a stub.
//
// Both sides of the wall are asserted, because only the pair says the wall is in
// the right place. An identity whose written form is exactly the limit builds; one
// byte more halts. Before this the shorter one built and the longer one ALSO
// built, stamping a label value the target refuses at exit zero with nothing
// reporting it — which is the silent outcome the halt replaces.
//
// The provider driven here reports the label kind alone, which is deliberate: a
// provider that also derives a release name refuses this identity for that name
// first and at a much smaller bound, so the label kind's own answer would never be
// the one reported and the assertion would be about the other kind.
func TestSettleAuthoredComponentIdentity_HaltsWhereTheWrittenLabelWouldNotFit(t *testing.T) {
	t.Parallel()

	// One byte under: written as exactly the limit, so it builds.
	fits := qualifiedIdentity(61)
	if err := renderer.SettleAuthoredComponentIdentity(context.Background(), chartOracle{},
		fits, "spec.components."+fits, nil); err != nil {
		t.Fatalf("an identity of %d written as %d is within the limit and must build: %v",
			len(fits), len(labels.EncodeNodeIdentity(fits)), err)
	}

	// One byte over: written past the limit, so it halts.
	over := qualifiedIdentity(62)
	err := renderer.SettleAuthoredComponentIdentity(context.Background(), chartOracle{},
		over, "spec.components."+over, nil)
	if err == nil {
		t.Fatalf("an identity of %d is written as %d and must halt; the label would carry %d bytes",
			len(over), len(labels.EncodeNodeIdentity(over)), len(labels.EncodeNodeIdentity(over)))
	}

	for _, want := range []string{
		"spec.components." + over,                // the field the author wrote
		rendercore.ComponentIDLabelKind,          // the target's own name for what refused
		"a label value is at most 63 characters", // the target's own reason
		"this identity of 62 is stamped as 64",   // the gap the author cannot otherwise see
		"choose a name of at most 61 characters", // the MEASURED budget
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not carry %q; got %v", want, err)
		}
	}
	// The limit is NOT the budget here, and reporting it would send the author to a
	// length that is refused a second time.
	if strings.Contains(err.Error(), "at most 63 characters that the target accepts") {
		t.Errorf("the halt states the limit as the budget; an identity of 63 is written as 65 and refused again: %v", err)
	}
	t.Logf("halt: %v", err)
}

// TestNameBoundOutput_TheReleaseNameVerdictIsBlindToTheAuthoredFlag is the guard
// against the repair that was refused.
//
// The label verdict now judges the value it will stamp, which for an authored
// identity means writing it first. The release name is DERIVED from the proposal,
// so the same writing applied to the proposal would be applied a second time by
// the derivation — one escape inside another — and the release-name verdict would
// refuse legal names well inside its own limit. Nothing about the label repair may
// reach it, so this asserts the release-name verdict is byte-for-byte the same
// answer whichever state the proposal is in.
//
// The concrete numbers are pinned alongside, because the identical-answer property
// would also hold if BOTH answers had moved together.
func TestNameBoundOutput_TheReleaseNameVerdictIsBlindToTheAuthoredFlag(t *testing.T) {
	t.Parallel()

	corpus := append(namesegmenttest.Corpus('-'),
		"web", "payments-api", "documents.backend", "PAYMENTS",
		qualifiedIdentity(55), qualifiedIdentity(61), qualifiedIdentity(62),
		strings.Repeat("a", 60), strings.Repeat("a", 200),
		"bfr--cluster--bridge", "cfr--host--instance",
	)
	compared := 0
	for _, proposed := range corpus {
		asAuthored := releaseVerdictKey(releaseVerdictOf(t, proposed, true))
		asComposed := releaseVerdictKey(releaseVerdictOf(t, proposed, false))
		if asAuthored != asComposed {
			t.Errorf("proposed %q: the release-name verdict differs by the authored flag:\n authored: %s\n composed: %s",
				proposed, asAuthored, asComposed)
		}
		compared++
	}
	if compared != len(corpus) {
		t.Fatalf("compared %d of %d cases; a corpus partly skipped reads exactly like a clean one",
			compared, len(corpus))
	}

	// The derivation reads the UNWRITTEN identity, and these are the numbers that
	// say so. A proposal written before it reached here would be written again by
	// the derivation: the hyphen the first writing emitted gets an escape of its
	// own, and the reported encoded length would be larger.
	over := qualifiedIdentity(55)
	v := releaseVerdictOf(t, over, true)
	if v.Usable {
		t.Fatalf("an identity of %d must be refused as a release name", len(over))
	}
	if !strings.Contains(v.Reason, "this identity of 55 encodes to 57") {
		t.Errorf("the release-name reason does not report 55 encoding to 57, so the derivation is not reading "+
			"the identity as authored; got %q", v.Reason)
	}
}

// releaseVerdictKey is a verdict's whole content as one comparable string.
//
// A verdict carries its bound behind a POINTER, so two verdicts of identical
// content compare unequal as structs: each call allocates its own int. Comparing
// the structs would therefore report every pair as differing and the test would
// fail for a reason that has nothing to do with what it asserts. The bound is
// spelled out here, absence included, because absent and zero are different
// answers and a key that folded them together would pass on a verdict that had
// moved between the two.
func releaseVerdictKey(v rendercore.DerivedNameVerdict) string {
	bound := "absent"
	if v.Bound != nil {
		bound = strconv.Itoa(*v.Bound)
	}
	return v.Kind + " usable=" + strconv.FormatBool(v.Usable) + " bound=" + bound + " reason=" + v.Reason
}

// releaseVerdictOf reads the release-name verdict out of the provider's whole
// answer, so the test drives the shipped assembly rather than the one kind.
func releaseVerdictOf(t *testing.T, proposed string, authored bool) rendercore.DerivedNameVerdict {
	t.Helper()
	for _, v := range helmcore.NameBoundOutput(rendercore.NameBoundInput{Proposed: proposed, Authored: authored}).Names {
		if v.Kind == helmcore.ReleaseNameKind {
			return v
		}
	}
	t.Fatalf("the provider reported no release-name verdict for %q", proposed)
	return rendercore.DerivedNameVerdict{}
}

// TestComponentIDLabelVerdict_ComposedIdentitiesAreJudgedVerbatim is the other
// half of the same guard, on the branch the repair must leave alone.
//
// A composed identity was composed through an encoding of its own and is stamped
// exactly as proposed, so the verdict judges it as it stands. Writing it again
// would write out the two-hyphen join it was composed with, and every composed
// identity within a few bytes of the limit would start being refused. The corpus
// here is the composed shape itself, at and around the limit.
func TestComponentIDLabelVerdict_ComposedIdentitiesAreJudgedVerbatim(t *testing.T) {
	t.Parallel()

	separating := 0
	for _, opening := range []string{"bfr--", "cfr--"} {
		for _, n := range []int{53, 62, 63} {
			proposed := opening + strings.Repeat("a", n-len(opening))
			if v := rendercore.ComponentIDLabelVerdict(proposed, false); !v.Usable {
				t.Errorf("a composed identity of %d (%q) was refused: %s", n, proposed, v.Reason)
			}
			// A case only separates the two branches where writing the value again
			// would push it OVER the limit — otherwise the row passes on either
			// branch and proves nothing about which one ran. The two-hyphen opening
			// costs two bytes written, so the separating cases are the ones within two
			// bytes of the limit; the shorter rows are ordinary coverage.
			if len(labels.EncodeNodeIdentity(proposed)) > 63 {
				separating++
			}
		}
	}
	if separating == 0 {
		t.Error("no case in this corpus would have been refused had the composed branch been written again, " +
			"so it proves only that short composed identities are short")
	}
}
