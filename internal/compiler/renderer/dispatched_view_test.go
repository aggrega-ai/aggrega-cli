// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// declaredAtDispatchedStage returns the accessor names the framework declares at
// the dispatched stage, derived from the generated views rather than listed here.
//
// The derivation is the difference between the stage's own view and the view it
// extends: a stage's view embeds the one before it, so every name present in the
// first and absent from the second is declared AT this stage. A name added at an
// earlier stage therefore does not perturb this, and a name added at this stage
// appears here with no edit.
func declaredAtDispatchedStage(t *testing.T) []string {
	t.Helper()
	dispatched := reflect.TypeOf((*constraint.DispatchedView)(nil)).Elem()
	hydrated := reflect.TypeOf((*constraint.HydratedView)(nil)).Elem()

	inherited := map[string]bool{}
	for i := 0; i < hydrated.NumMethod(); i++ {
		inherited[hydrated.Method(i).Name] = true
	}
	var own []string
	for i := 0; i < dispatched.NumMethod(); i++ {
		if name := dispatched.Method(i).Name; !inherited[name] {
			own = append(own, name)
		}
	}
	sort.Strings(own)
	return own
}

// delegatedAtDispatchedStage returns the accessor names the presentation answers
// from what the render ALREADY holds, derived from the seam it delegates them
// through rather than from a list.
//
// The seam's own method set is the split, and reading it is what keeps this guard
// from carrying a note of which facts are answered how — a note that would go
// stale the moment a further fact is declared at this stage.
func delegatedAtDispatchedStage(t *testing.T) []string {
	t.Helper()
	seam := reflect.TypeOf((*assemblyGroupFacts)(nil)).Elem()
	names := make([]string, 0, seam.NumMethod())
	for i := 0; i < seam.NumMethod(); i++ {
		names = append(names, seam.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

// TestTheDelegationSeamCoversEveryDeclaredDispatchedFact holds this stage's
// answering source to the framework's declarations instead of to a list somebody
// maintains.
//
// A name declared at this stage with nothing to answer it from would leave the
// presentation silently behind — the declaration would exist, the accessor would
// return a zero value, and no build would fail. So the sets are compared in both
// directions and the failure names the offending side.
//
// # Every fact here is delegated, and none is retained for this stage
//
// The other stages hold their own retention beside a delegation seam, and their
// guards read both. This stage has one source: the coordinates both its facts
// read are the group attribution the render records when the last node is sorted
// into its group, so nothing is retained for this stage and there is no split to
// derive. The comparison is therefore against the seam alone.
//
// That is a property of what this stage answers rather than a simplification, and
// the failure below says so: a retention taken for this stage later is a second
// answering source, and this guard reports a fact answered from it as unanswered
// until the source is read here too. Widening it then is the intended repair.
//
// # What this catches that the compiler does not
//
// The presentation's method set is the union of the earlier view and the seam,
// so a declared fact neither of them carries already fails to satisfy the
// framework's contract where the file under test asserts it. That protection is a
// property of the present shape rather than of the declaration: a presentation
// answering a fact from a field of its own — which is the shape the stage after
// this one takes — keeps compiling while a declaration goes unanswered.
//
// The reverse direction has no compile-time equivalent at all. A seam method left
// behind by a withdrawn fact is an extra method on an interface: the concrete type
// implements it, the presentation embeds it, and every assertion holds. Nothing
// but this comparison observes that it answers a question no declaration asks.
func TestTheDelegationSeamCoversEveryDeclaredDispatchedFact(t *testing.T) {
	t.Parallel()

	declared := declaredAtDispatchedStage(t)
	delegated := delegatedAtDispatchedStage(t)

	// A comparison of empty sets would pass while proving nothing, so every set's
	// non-emptiness is asserted before the comparison rather than assumed.
	if len(declared) == 0 {
		t.Fatalf("no accessor is declared at this stage; the derivation from the generated views is broken, not the presentation")
	}
	if len(delegated) == 0 {
		t.Fatalf("the delegation seam declares no method, so this stage's answering source is not derivable")
	}

	answered := map[string]bool{}
	for _, name := range delegated {
		answered[name] = true
	}
	for _, name := range declared {
		if !answered[name] {
			t.Errorf("nothing answers %q: it is declared at this stage and the delegation seam does not carry it. "+
				"Either the seam is missing the method, or a retention was taken for this stage and this "+
				"comparison reads only the seam", name)
		}
	}

	declaredSet := map[string]bool{}
	for _, name := range declared {
		declaredSet[name] = true
	}
	for _, name := range delegated {
		if !declaredSet[name] {
			t.Errorf("the delegation seam carries %q, which is declared at no stage past the one before this; "+
				"a seam method left behind by a withdrawn fact answers a question nobody asks", name)
		}
	}

	t.Logf("declared at this stage %v; delegated %v", declared, delegated)
}

// TestTheDispatchedPresentationRefusesAPartialConstruction asserts the
// presentation is absent rather than partial when a source it reads is missing.
//
// An accessor returning a plausible zero value is silent where an absent
// presentation is loud, so the constructor refuses instead of answering a whole
// class of facts from nothing — and a rule reading an emptily-answering
// presentation cannot tell an empty answer from a real one.
func TestTheDispatchedPresentationRefusesAPartialConstruction(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		earlier constraint.HydratedView
		groups  assemblyGroupFacts
	}{
		{"no earlier presentation", nil, assemblyGroups{result: &RenderResult{}}},
		{"no group seam", stubHydratedView{}, nil},
		{"neither source", nil, nil},
	} {
		if got := newDispatchedView(tc.earlier, tc.groups); got != nil {
			t.Errorf("%s: a presentation was built anyway", tc.name)
		}
	}

	// The positive control. Without it a constructor that refused everything
	// would satisfy every row above while presenting nothing at all.
	if got := newDispatchedView(stubHydratedView{}, assemblyGroups{result: &RenderResult{}}); got == nil {
		t.Error("the presentation refused both sources present, so the refusals above prove nothing")
	}
}

// stubHydratedView stands in for the earlier presentation where only this stage's
// own construction is under test. It answers nothing, so reading an earlier fact
// through it is a nil dereference rather than a wrong answer — which is the
// point: a test that reached for one would be reaching past what this file covers.
type stubHydratedView struct {
	constraint.HydratedView
}
