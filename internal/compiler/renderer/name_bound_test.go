// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
)

// boundOracle is a stub provider engine answering the name-bound question from a
// caller-supplied rule, so the exchange between shortening and refusal can be
// driven without a Wasm guest.
//
// It records every proposal it was shown, in order, which is what lets the tests
// below assert the NUMBER of passes rather than only the final answer: the
// determinism the contract states is about how many times the two sides spoke,
// not just about what they ended up agreeing.
type boundOracle struct {
	answer func(proposed string) DerivedNameVerdict
	seen   []string
}

func (o *boundOracle) NameBound(_ context.Context, in NameBoundInput) (NameBoundOutput, error) {
	o.seen = append(o.seen, in.Proposed)
	return NameBoundOutput{Names: []DerivedNameVerdict{o.answer(in.Proposed)}}, nil
}

// silentEngine implements nothing. It stands for every provider guest that does
// not advertise the export, which must be treated as accepting whatever the
// compiler composed.
type silentEngine struct{}

// boundTo is the pointer form a stated bound travels in, so a test can state one
// without a named variable per case.
func boundTo(n int) *int { return &n }

// TestSettleComposedComponentIdentity_ConvergesWhenShorteningLandsOnAGrammarEdge
// is the proof that the loop is the mechanism rather than a retry.
//
// The oracle refuses twice for two DIFFERENT reasons, which is the shape the
// design exists for: first the composition is too long, and then — at the very
// length the oracle named — the shortened form ends on a character this oracle's
// grammar refuses in that position, so it comes back with a smaller bound. A
// single-pass design would either stamp the second name or halt on it; only the
// loop reaches a name both sides accept.
//
// The pass count is asserted, not just the result. A loop that happened to
// converge in five passes where three suffice would still return the right name
// and would be a different exchange, and the contract is about both.
func TestSettleComposedComponentIdentity_ConvergesWhenShorteningLandsOnAGrammarEdge(t *testing.T) {
	t.Parallel()

	// The first refusal is a length; the second is a grammar edge at that length.
	// Both are above the shortest name the scheme can compose from these parts,
	// so what this case exercises is the exchange rather than the floor — the
	// floor has its own case below.
	const firstBound = 48
	const secondBound = 40
	oracle := &boundOracle{answer: func(proposed string) DerivedNameVerdict {
		switch {
		case len(proposed) > firstBound:
			return DerivedNameVerdict{Kind: "release name", Usable: false, Bound: boundTo(firstBound),
				Reason: "too long"}
		case len(proposed) > secondBound:
			return DerivedNameVerdict{Kind: "release name", Usable: false, Bound: boundTo(secondBound),
				Reason: "the shortened form ends where this grammar will not"}
		default:
			return DerivedNameVerdict{Kind: "release name", Usable: true}
		}
	}}

	got, err := SettleComposedComponentIdentity(context.Background(), oracle,
		labels.BoundaryFeatureScope, "stub", nil,
		strings.Repeat("cluster", 12), strings.Repeat("bridge", 12))
	if err != nil {
		t.Fatalf("the exchange must converge on a name both sides accept: %v", err)
	}
	if len(got) > secondBound {
		t.Errorf("settled on %q, which is %d characters and over the %d the oracle last named",
			got, len(got), secondBound)
	}
	if len(oracle.seen) != 3 {
		t.Errorf("the exchange took %d passes (%q); it must take three — the unbounded composition, "+
			"the one shortened to the length bound, and the one shortened to the grammar bound",
			len(oracle.seen), oracle.seen)
	}

	// Determinism is a contract term: identical parts and an identical oracle must
	// yield an identical number of passes and an identical result.
	again := &boundOracle{answer: oracle.answer}
	repeat, err := SettleComposedComponentIdentity(context.Background(), again,
		labels.BoundaryFeatureScope, "stub", nil,
		strings.Repeat("cluster", 12), strings.Repeat("bridge", 12))
	if err != nil {
		t.Fatalf("the second run must reach the same answer: %v", err)
	}
	if repeat != got {
		t.Errorf("two runs of identical input settled on %q and %q", got, repeat)
	}
	if len(again.seen) != len(oracle.seen) {
		t.Errorf("two runs of identical input took %d and %d passes", len(oracle.seen), len(again.seen))
	}
}

// TestSettleComposedComponentIdentity_HaltsOnAGuestThatNeverConverges is the
// backstop proven with a deliberately non-converging stub.
//
// The oracle here returns a bound and then refuses the very name that bound
// produced, forever. That is a guest defect rather than a hard input: nothing the
// compiler composes can satisfy it, and nothing about the exchange says so except
// that it does not end. The build must stop and NAME the guest rather than
// truncate to a name the guest never accepted — a silently truncated name would
// be stamped on real resources with nothing in the output recording that no
// agreement was reached.
func TestSettleComposedComponentIdentity_HaltsOnAGuestThatNeverConverges(t *testing.T) {
	t.Parallel()

	// The canonical non-converging shape: the guest names a bound the composition
	// CAN reach, and then refuses the very name that bound produced. Nothing about
	// any single answer is malformed, the compiler makes the shortening it was
	// asked for every pass, and the exchange still never ends. A bound stated
	// BELOW what the scheme can reach would be a different case entirely — that one
	// is the target answering correctly and has its own halt — so the stub here
	// stays inside reach on purpose.
	oracle := &boundOracle{answer: func(proposed string) DerivedNameVerdict {
		return DerivedNameVerdict{Kind: "release name", Usable: false,
			Bound: boundTo(len(proposed)), Reason: "still not acceptable"}
	}}

	_, err := SettleComposedComponentIdentity(context.Background(), oracle,
		labels.BoundaryFeatureScope, "stubbornprovider", nil, "cluster", "bridge")
	if err == nil {
		t.Fatal("a guest that never accepts a name must stop the build, not be truncated around")
	}
	if !errors.Is(err, ErrNameBoundNotConverging()) {
		t.Errorf("the halt must carry the non-convergence condition; got %v", err)
	}
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0264" {
		t.Errorf("the halt must carry the condition's short code; got %q (present=%v)", code, ok)
	}
	if !strings.Contains(err.Error(), "stubbornprovider") {
		t.Errorf("the halt must name the guest a reader has to go and fix; got %v", err)
	}
	if len(oracle.seen) != nameBoundMaxPasses {
		t.Errorf("the exchange ran %d passes; the bound is %d and a bound that does not bind is not one",
			len(oracle.seen), nameBoundMaxPasses)
	}
}

// TestSettleComposedComponentIdentity_HaltsWhenNoLengthWouldHelp pins the other
// refusal: a verdict carrying NO bound.
//
// It is a distinct outcome from non-convergence and must not be folded into it. A
// guest that will not converge is defective; a guest saying "no length repairs
// this" is answering correctly, and the two send a reader to different places —
// one to the plugin's maintainer, the other to the name.
func TestSettleComposedComponentIdentity_HaltsWhenNoLengthWouldHelp(t *testing.T) {
	t.Parallel()

	oracle := &boundOracle{answer: func(string) DerivedNameVerdict {
		return DerivedNameVerdict{Kind: "release name", Usable: false,
			Reason: "the first character is one this alphabet does not admit"}
	}}

	_, err := SettleComposedComponentIdentity(context.Background(), oracle,
		labels.AttachmentScope, "stub", nil, "host", "instance")
	if !errors.Is(err, ErrComponentIdentityTargetRefuses()) {
		t.Fatalf("a refusal no length repairs must carry the target-refuses condition; got %v", err)
	}
	if len(oracle.seen) != 1 {
		t.Errorf("the exchange took %d passes; a refusal carrying no bound leaves nothing to try, "+
			"so it must stop on the first", len(oracle.seen))
	}
	if !strings.Contains(err.Error(), "release name") {
		t.Errorf("the halt must carry the target's own name for what refused; got %v", err)
	}
}

// TestSettleComposedComponentIdentity_HaltsOnAZeroBoundRatherThanLooping pins the
// distinction between an absent bound and a bound of zero.
//
// Zero is a stated length — no identity of any length reaches an acceptable name
// — and it must not be composed against. Composing against it would re-enter the
// unbounded composition, produce the same name again, and walk the exchange to
// the pass limit, reporting a guest defect where the guest answered plainly.
func TestSettleComposedComponentIdentity_HaltsOnAZeroBoundRatherThanLooping(t *testing.T) {
	t.Parallel()

	oracle := &boundOracle{answer: func(string) DerivedNameVerdict {
		return DerivedNameVerdict{Kind: "logical id", Usable: false, Bound: boundTo(0),
			Reason: "this target accepts no name of this kind"}
	}}

	_, err := SettleComposedComponentIdentity(context.Background(), oracle,
		labels.AttachmentScope, "stub", nil, "host", "instance")
	if !errors.Is(err, ErrComponentIdentityTargetRefuses()) {
		t.Fatalf("a zero bound must stop on the target's own answer; got %v", err)
	}
	if errors.Is(err, ErrNameBoundNotConverging()) {
		t.Error("a zero bound is a plain answer, not a guest defect; reporting non-convergence " +
			"sends the reader to the plugin's maintainer for something the plugin got right")
	}
	if len(oracle.seen) != 1 {
		t.Errorf("the exchange took %d passes; a zero bound leaves nothing to compose", len(oracle.seen))
	}
}

// TestSettleComposedComponentIdentity_TakesTheSmallestBoundAcrossKinds asserts
// the reading rule when several kinds refuse at once.
//
// An identity has to satisfy every kind at the same time, so the smallest bound
// is the only one that can be right. Taking any other would compose a name one
// kind accepts and the next refuses, and the exchange would then take a pass per
// kind rather than one.
func TestSettleComposedComponentIdentity_TakesTheSmallestBoundAcrossKinds(t *testing.T) {
	t.Parallel()

	var asked []string
	engine := multiVerdictOracle{answer: func(proposed string) []DerivedNameVerdict {
		asked = append(asked, proposed)
		if len(proposed) <= 40 {
			return []DerivedNameVerdict{
				{Kind: "release name", Usable: true},
				{Kind: "component-id label value", Usable: true},
			}
		}
		return []DerivedNameVerdict{
			{Kind: "component-id label value", Usable: false, Bound: boundTo(52), Reason: "too long"},
			{Kind: "release name", Usable: false, Bound: boundTo(40), Reason: "much too long"},
		}
	}}

	got, err := SettleComposedComponentIdentity(context.Background(), &engine,
		labels.BoundaryFeatureScope, "stub", nil, strings.Repeat("region", 10), strings.Repeat("bridge", 10))
	if err != nil {
		t.Fatalf("the exchange must converge: %v", err)
	}
	if len(got) > 40 {
		t.Errorf("settled on %q at %d characters, over the smallest bound any kind stated", got, len(got))
	}
	if len(asked) != 2 {
		t.Errorf("the exchange took %d passes; reading the smallest bound at once must settle it in two, "+
			"and a pass per refusing kind means the reading rule is wrong", len(asked))
	}
}

// multiVerdictOracle answers with several kinds at once, which the single-verdict
// stub above cannot express.
type multiVerdictOracle struct {
	answer func(proposed string) []DerivedNameVerdict
}

func (o *multiVerdictOracle) NameBound(_ context.Context, in NameBoundInput) (NameBoundOutput, error) {
	return NameBoundOutput{Names: o.answer(in.Proposed)}, nil
}

// TestNameBound_AnEngineThatDoesNotAnswerIsNeverAsked is the no-op proof for
// every guest that does not advertise the export.
//
// Both halves are asserted. A composed identity is used exactly as composed, so
// no shipped provider's names move; and an authored identity is not refused, so
// no build that passed before this existed fails now. Without that, adding the
// capability would rename output for every provider that never implemented it.
func TestNameBound_AnEngineThatDoesNotAnswerIsNeverAsked(t *testing.T) {
	t.Parallel()

	parts := []string{strings.Repeat("cluster", 20), strings.Repeat("bridge", 20)}
	got, err := SettleComposedComponentIdentity(context.Background(), silentEngine{},
		labels.BoundaryFeatureScope, "silent", nil, parts...)
	if err != nil {
		t.Fatalf("a provider that answers no such question must not stop a build: %v", err)
	}
	if want := labels.ComposeComponentIdentity(labels.BoundaryFeatureScope, labels.Unbounded, parts...); got != want {
		t.Errorf("settled on %q; a provider that states no bound gets the composition as composed, %q", got, want)
	}

	if err := SettleAuthoredComponentIdentity(context.Background(), silentEngine{},
		strings.Repeat("A", 200), "spec.components.long", nil); err != nil {
		t.Errorf("a provider that answers no such question must refuse no authored name: %v", err)
	}
}

// TestSettleAuthoredComponentIdentity_RefusesAndNamesTheAuthoredField is the
// provenance split's refusal half.
//
// What is asserted is not that a halt happened but WHERE it points. An authored
// name is refused rather than shortened because the person able to change it is
// the person who wrote it — so the halt has to carry the field they wrote, and a
// halt naming only a node identity or a composed value would send them looking
// for a coordinate no document contains.
func TestSettleAuthoredComponentIdentity_RefusesAndNamesTheAuthoredField(t *testing.T) {
	t.Parallel()

	oracle := &boundOracle{answer: func(string) DerivedNameVerdict {
		return DerivedNameVerdict{Kind: "release name", Usable: false, Bound: boundTo(12),
			Reason: "a release name is at most 53 characters"}
	}}

	err := SettleAuthoredComponentIdentity(context.Background(), oracle,
		"PAYMENTS", "spec.components.PAYMENTS", nil)
	if !errors.Is(err, ErrComponentIdentityTargetRefuses()) {
		t.Fatalf("an authored name the target refuses must halt; got %v", err)
	}
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0263" {
		t.Errorf("the refusal must carry the condition's short code; got %q (present=%v)", code, ok)
	}
	for _, want := range []string{
		"spec.components.PAYMENTS", // the field the developer wrote
		"release name",             // the target's own name for what refused
		"at most 53 characters",    // the target's own reason, carried verbatim
		"at most 12 characters",    // the budget, so a new name is chosen once rather than twice
		"rename the component",     // the remedy
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q; got %v", want, err)
		}
	}

	// It is asked ONCE. An authored name is refused rather than shortened, so a
	// second pass would be the compiler trying to repair a name it must not touch.
	if len(oracle.seen) != 1 {
		t.Errorf("the authored path asked %d times; it must ask once and refuse", len(oracle.seen))
	}
	if oracle.seen[0] != "PAYMENTS" {
		t.Errorf("the authored path proposed %q; it must put the authored name itself", oracle.seen[0])
	}
}

// TestSettleComposedComponentIdentity_ReportsABoundTheSchemeCannotReach pins the
// third refusal, and it exists because the second one would otherwise absorb it
// and blame the wrong party.
//
// The composition reserves a run for the last part and a digest before it
// measures anything, so from a given set of parts there is a shortest name it can
// produce. A target bounding below that is answering correctly — nothing is wrong
// with the guest — and the exchange would never end. Reporting non-convergence
// there would send a reader to the plugin's maintainer for something the plugin
// got right; what they can actually act on is the parts, which they wrote.
func TestSettleComposedComponentIdentity_ReportsABoundTheSchemeCannotReach(t *testing.T) {
	t.Parallel()

	// Twelve characters is far below the run the scheme reserves for the last part
	// plus its digest, so no composition from these parts reaches it.
	oracle := &boundOracle{answer: func(proposed string) DerivedNameVerdict {
		if len(proposed) <= 12 {
			return DerivedNameVerdict{Kind: "release name", Usable: true}
		}
		return DerivedNameVerdict{Kind: "release name", Usable: false, Bound: boundTo(12),
			Reason: "this target is very strict"}
	}}

	_, err := SettleComposedComponentIdentity(context.Background(), oracle,
		labels.BoundaryFeatureScope, "strict", nil, "cluster", "bridgebridge")
	if !errors.Is(err, ErrComponentIdentityTargetRefuses()) {
		t.Fatalf("an unreachable bound must be reported as the target refusing; got %v", err)
	}
	if errors.Is(err, ErrNameBoundNotConverging()) {
		t.Error("an unreachable bound is a correct answer from the guest, not a guest defect; " +
			"reporting non-convergence blames the plugin for the compiler's own floor")
	}
	if !strings.Contains(err.Error(), "shorter than any identity the compiler can compose") {
		t.Errorf("the halt must say the bound is below what the scheme can reach; got %v", err)
	}
	if !strings.Contains(err.Error(), "shorten the parts") {
		t.Errorf("the halt must name the remedy the author can carry out; got %v", err)
	}
	// It stops on the SECOND pass: the unbounded proposal, then the one composed
	// against the stated bound, which is what shows the bound to be unreachable.
	if len(oracle.seen) != 1 {
		t.Errorf("the exchange asked %d times; the unreachable bound is visible from the composition "+
			"itself, so no second question is owed", len(oracle.seen))
	}
}
