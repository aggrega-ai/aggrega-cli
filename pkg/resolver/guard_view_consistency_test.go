// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// notFoundCatalog is a CatalogLookup whose Component lookup always misses — the
// resolved-but-absent condition a bare catalog reference turns into a loud halt.
func notFoundCatalog() CatalogLookup {
	return CatalogLookup{Component: func(string) (types.Component, bool) {
		return types.Component{}, false
	}}
}

func oneBareComponent(typeRef string, inject *types.InjectBlock) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: typeRef, Inject: inject})
	return &types.Aggregate{Metadata: types.AggregateMetadata{Name: "demo"}, Spec: types.AggregateSpec{Components: comps}}
}

// TestGuardEntityViewInconsistency_Classifies pins the shared classifier: a
// relative-path type is a legitimate skip (nil); a bare catalog reference
// absent from the view is the loud halt.
func TestGuardEntityViewInconsistency_Classifies(t *testing.T) {
	t.Parallel()
	if verr := guardEntityViewInconsistency("E", "F", "./local-variant"); verr != nil {
		t.Errorf("relative-path type must classify as a legitimate skip (nil); got %v", verr)
	}
	if verr := guardEntityViewInconsistency("E", "F", "../sibling"); verr != nil {
		t.Errorf("parent-relative-path type must classify as a legitimate skip (nil); got %v", verr)
	}
	verr := guardEntityViewInconsistency("E", "F", "core/absent:1.0.0")
	if verr == nil || !errors.Is(verr, ErrGuardEntityViewInconsistency()) {
		t.Fatalf("bare catalog ref must halt with ErrGuardEntityViewInconsistency; got %v", verr)
	}
}

// TestTheRequiredSocketGuardLeavesTheAbsentCatalogEntryToTheRuleThatOwnsIt pins
// the one-home property for a fault that two paths can see.
//
// The required-socket guard reaches a component whose type the catalog holds no
// entry for, and such a type declares no socket set for that guard to read. It
// therefore ends on that component rather than classifying it: the classification
// is a separate rule with its own sentinel, asked once per component naming such
// a type. A guard that also classified would report one fault twice.
//
// 🔴 Both halves are asserted over ONE document, and the pair is what makes this
// a property rather than two observations: the guard's silence alone would also
// be produced by a guard that never reached the component, and the rule's report
// alone says nothing about whether a second path reports it too.
func TestTheRequiredSocketGuardLeavesTheAbsentCatalogEntryToTheRuleThatOwnsIt(t *testing.T) {
	t.Parallel()

	agg := oneBareComponent("core/absent:1.0.0", nil)
	na := normalized(t, agg)
	catalog := notFoundCatalog()

	if errs := guardRequiredUsesSockets(na, nil, catalog); len(errs) != 0 {
		t.Errorf("the required-socket guard reported on a type the catalog holds no entry for: %v.\n"+
			"INVARIANT: the classification has one home, and it is not this guard.\n"+
			"CONSEQUENCE: one fault is reported twice, once with a sentinel a caller matching on "+
			"the socket rule does not expect.", errs)
	}

	view := newNormalizedView(na, nil, catalog, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, componentTypeResolvesToACatalogEntry)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	if len(objections) != 1 {
		t.Fatalf("the rule that owns the classification raised %d objection(s); want 1.\n"+
			"INVARIANT: a component naming a catalog identity nobody found is reported once.\n"+
			"CONSEQUENCE: with the guard's own raise removed, nothing reports the fault at all.",
			len(objections))
	}
	if !errors.Is(objections[0].Sentinel(), ErrGuardEntityViewInconsistency()) {
		t.Errorf("the objection carries %v; want the catalog-entry rule's own sentinel.\n"+
			"INVARIANT: the fault keeps its identity when its home moves.\n"+
			"CONSEQUENCE: a caller matching on the sentinel stops seeing this refusal.",
			objections[0].Sentinel())
	}
	if want := ".spec.components.svc"; objections[0].Locator() != want {
		t.Errorf("the objection is positioned at %q; want %q", objections[0].Locator(), want)
	}
}
