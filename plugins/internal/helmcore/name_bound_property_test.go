// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
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
	return helmcore.NameBoundOutput(in.Proposed), nil
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

	instances := append(namesegmenttest.Corpus(),
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
				got, err := renderer.SettleComposedComponentIdentity(context.Background(), oracle, scope, "helm", parts...)
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
				for _, verdict := range helmcore.NameBoundOutput(got).Names {
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

	out := helmcore.NameBoundOutput("web")
	kinds := make([]string, 0, len(out.Names))
	for _, v := range out.Names {
		kinds = append(kinds, v.Kind)
		if !v.Usable {
			t.Errorf("an ordinary authored name is refused as a %s: %s", v.Kind, v.Reason)
		}
	}
	for _, want := range []string{helmcore.ReleaseNameKind, rendercore.ComponentIDLabelKind} {
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
	for _, v := range helmcore.NameBoundOutput("ABCDEF").Names {
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
	for _, v := range helmcore.NameBoundOutput(long).Names {
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
		for _, again := range helmcore.NameBoundOutput(long[:*v.Bound]).Names {
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
		Names: []rendercore.DerivedNameVerdict{rendercore.ComponentIDLabelVerdict(in.Proposed)},
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
				scope, "helm-chart", parts...)
			if err != nil {
				t.Errorf("scope %q parts %v: the exchange must converge: %v", scope, parts, err)
				continue
			}
			if got != labels.ComposeComponentIdentity(scope, labels.Unbounded, parts...) {
				shortened++
			}
			if v := rendercore.ComponentIDLabelVerdict(got); !v.Usable {
				t.Errorf("scope %q parts %v settled on %q (%d characters), which the label rule still refuses: %s",
					scope, parts, got, len(got), v.Reason)
			}
		}
	}
	if shortened == 0 {
		t.Error("no case reached the shortening path, so this proves only that short names are short")
	}
}
