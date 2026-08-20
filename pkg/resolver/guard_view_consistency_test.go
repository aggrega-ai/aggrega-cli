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

// TestTheAbsentCatalogEntryIsClassifiedByTheRuleThatOwnsIt pins the one-home
// property for a fault that more than one rule can see.
//
// A component whose type the catalog holds no entry for declares no socket set,
// so every rule reading what a type declares ends on that component rather than
// classifying it. The classification is a separate rule with its own sentinel,
// asked once per component naming such a type; a second rule that also
// classified would report one fault twice.
//
// 🔴 The other half of the pair — that the required-socket rule stays silent on
// such a component — is asserted where that rule is proved, over the composed
// application whose facts it reads. The silence alone would also be produced by a
// rule that never reached the component, which is why the assertion sits beside a
// fixture that can show the rule being invoked.
func TestTheAbsentCatalogEntryIsClassifiedByTheRuleThatOwnsIt(t *testing.T) {
	t.Parallel()

	agg := oneBareComponent("core/absent:1.0.0", nil)
	na := normalized(t, agg)
	catalog := notFoundCatalog()

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
