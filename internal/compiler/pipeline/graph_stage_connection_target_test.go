// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
)

// The proofs in this file cover the pair of rules about the component at the far
// end of a dependency connection, evaluated over the finalised graph.
//
// They sit in this package rather than beside the declarations because the
// presentation and the enumeration the rules need are both carried on an
// unexported output, and reaching them is the point: a proof over an enumeration
// a test assembles proves the test's account of the graph rather than the
// compiler's. They read the violations the pre-render pipeline accumulated, which
// is the slice the build gates on — so what they assert is that the production
// path invokes the rules, not merely that the rules would object if something
// asked them.
//
// The classification is asserted from both sides in every case. A rule reporting
// nothing and a rule never invoked are indistinguishable from a count alone, and
// two rules over one absence can each be silently wrong in the other's favour, so
// each proof names the sentinel it wants AND the sibling it must not get.

// danglingSentinelsIn partitions what the pipeline accumulated by the two
// sentinels this pair raises, and returns the locators under each.
func danglingSentinelsIn(t *testing.T, out *stepsOneToFourOutput) (absent, removed []string) {
	t.Helper()
	for _, o := range out.Violations {
		switch {
		case errors.Is(o.Sentinel(), topology.ErrDanglingUsesTarget()):
			absent = append(absent, o.Locator())
		case errors.Is(o.Sentinel(), topology.ErrUsesPrunedComponent()):
			removed = append(removed, o.Locator())
		}
	}
	return absent, removed
}

// misdirectTwoTargetsAtNothing points two of the fixture's connections at names
// no component answers to, one written in each of the two authoring forms the
// dependency block admits.
//
// Two rather than one, because a proof that the rule reports every offending
// connection cannot be taken on a document with one offence — a reading that
// returned after the first would pass it, and returning after the first is
// exactly what the hand-written reading this rule replaced did. The two forms
// rather than one because the locator differs between them, and a proof over one
// form would say nothing about the other. The splice is in-memory: the fixture on
// disk is shared with other proofs and must keep producing exactly what they
// expect.
func misdirectTwoTargetsAtNothing(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const (
		shortFormAnchor   = "        databases:\n          - data.main-db\n"
		shortFormMisnamed = "        databases:\n          - data.ghost\n"
		longFormAnchor    = "            target: backend\n"
		longFormMisnamed  = "            target: phantom\n"
	)
	for _, anchor := range []string{shortFormAnchor, longFormAnchor} {
		if !bytes.Contains(aggregate, []byte(anchor)) {
			t.Fatalf("fixture no longer carries the wiring %q; the splice would be silently inert", anchor)
		}
	}
	spliced := bytes.Replace(aggregate, []byte(shortFormAnchor), []byte(shortFormMisnamed), 1)
	return bytes.Replace(spliced, []byte(longFormAnchor), []byte(longFormMisnamed), 1)
}

// switchOffTheTargetOfAConnection turns off the component another component
// depends on, which is the only way to produce a target that is absent AND
// recorded as removed.
func switchOffTheTargetOfAConnection(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const (
		anchor      = "    backend:\n      type: core/compute/api-server\n"
		switchedOff = "    backend:\n      active: \"false\"\n      type: core/compute/api-server\n"
	)
	if !bytes.Contains(aggregate, []byte(anchor)) {
		t.Fatalf("fixture no longer declares the component this splice switches off; the splice would be silently inert")
	}
	return bytes.Replace(aggregate, []byte(anchor), []byte(switchedOff), 1)
}

// TestEveryConnectionReachingNothingIsReportedInOneRun proves the rule is
// invoked from the production path over the whole connection set, and that it
// accumulates.
//
// The count is the load-bearing half. The reading this rule replaced returned on
// the first offending connection, so an author clearing a document met one finding
// per build; a proof on a single-offence document cannot tell that behaviour from
// this one. The locators are asserted by value because they are the other half of
// the win: the reading that returned carried no authored position at all.
func TestEveryConnectionReachingNothingIsReportedInOneRun(t *testing.T) {
	out := compileFixture(t, misdirectTwoTargetsAtNothing(t, readAggregate(t)))

	absent, removed := danglingSentinelsIn(t, out)
	if len(absent) != 2 {
		t.Fatalf("the rule reported %d connection(s) reaching nothing, want 2: %v", len(absent), absent)
	}
	if len(removed) != 0 {
		t.Errorf("no target here was switched off, yet the sibling rule reported %v", removed)
	}
	want := []string{
		".spec.components.backend.uses.databases",
		".spec.components.frontend.uses.apis.my-api",
	}
	for i, locator := range want {
		if absent[i] != locator {
			t.Errorf("finding %d is positioned at %q, want %q", i, absent[i], locator)
		}
	}
	for _, o := range out.Violations {
		if !errors.Is(o.Sentinel(), topology.ErrDanglingUsesTarget()) {
			continue
		}
		if o.SourceFile() == "" {
			t.Errorf("finding at %q names no file; it could not be positioned in an editor", o.Locator())
		}
		if message := constraint.ObjectionMessage(o); message == "" {
			t.Errorf("finding at %q renders to nothing", o.Locator())
		}
	}
}

// TestAConnectionIntoASwitchedOffComponentSaysSo proves the two rules keep the
// classification the author needs: a target removed by an activation toggle is
// reported as removed and never as a name nothing answers to.
//
// Both directions are asserted because the pair is complementary by construction
// and either half can fail silently into the other. A predicate that stopped
// asking about removal would report this input under the wrong sentence, and a
// count alone would not notice.
func TestAConnectionIntoASwitchedOffComponentSaysSo(t *testing.T) {
	out := compileFixture(t, switchOffTheTargetOfAConnection(t, readAggregate(t)))

	absent, removed := danglingSentinelsIn(t, out)
	if len(removed) != 1 {
		t.Fatalf("the rule reported %d connection(s) into a switched-off component, want 1: %v", len(removed), removed)
	}
	if removed[0] != ".spec.components.frontend.uses.apis.my-api" {
		t.Errorf("the finding is positioned at %q, want %q", removed[0], ".spec.components.frontend.uses.apis.my-api")
	}
	if len(absent) != 0 {
		t.Errorf("a switched-off target must not also be reported as a name nothing answers to; got %v", absent)
	}
}

// TestACleanCompositionRaisesNeitherConnectionTargetRule is the control every
// assertion above needs: on the fixture as committed, neither rule objects.
//
// Without it the proofs above are satisfied by a pair of rules that object to
// every connection, which would refuse every workspace and pass each of them.
func TestACleanCompositionRaisesNeitherConnectionTargetRule(t *testing.T) {
	out := compileFixture(t, readAggregate(t))

	absent, removed := danglingSentinelsIn(t, out)
	if len(absent) != 0 || len(removed) != 0 {
		t.Fatalf("the committed fixture must satisfy both rules; got %v reaching nothing and %v switched off", absent, removed)
	}
	if len(out.IRGraph.Spec.Edges) < 2 {
		t.Fatalf("the fixture graph holds %d connection(s); a control cannot vouch for rules that were barely asked", len(out.IRGraph.Spec.Edges))
	}
}
