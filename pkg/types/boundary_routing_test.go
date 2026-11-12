// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// routingBoundary builds a boundary that names one component directly and routes
// a sub-module holding one component of its own plus a nested module holding
// another, so one fixture covers every position a routed component can occupy.
func routingBoundary() types.Boundary {
	inner := types.AggregateRouting{}
	inner.Components.Set("cache", types.BoundaryComponent{Description: "inner-cache"})

	data := types.AggregateRouting{}
	data.Components.Set("store", types.BoundaryComponent{Description: "routed-store"})
	data.Aggregates.Set("inner", inner)

	b := types.Boundary{Directory: "./dist/cluster"}
	b.Components.Set("api", types.BoundaryComponent{Description: "named-api"})
	b.Aggregates.Set("data", data)
	return b
}

func TestJoinQualifiedID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ prefix, name, want string }{
		{"", "store", "store"},
		{"data", "store", "data.store"},
		{"data.inner", "cache", "data.inner.cache"},
	} {
		if got := types.JoinQualifiedID(tc.prefix, tc.name); got != tc.want {
			t.Errorf("JoinQualifiedID(%q, %q) = %q; want %q", tc.prefix, tc.name, got, tc.want)
		}
	}
}

// TestIsUnderModulePrefix pins the whole-segment comparison. A raw string prefix
// test would place "database.x" inside "data", which would make an unrelated
// module's components collide with a routed one.
func TestIsUnderModulePrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id, prefix string
		want       bool
	}{
		{"data", "data", true},
		{"data.store", "data", true},
		{"data.inner.cache", "data", true},
		{"data.inner.cache", "data.inner", true},
		{"database.store", "data", false},
		{"datastore", "data", false},
		{"data", "data.inner", false},
		{"anything", "", true},
	} {
		if got := types.IsUnderModulePrefix(tc.id, tc.prefix); got != tc.want {
			t.Errorf("IsUnderModulePrefix(%q, %q) = %v; want %v", tc.id, tc.prefix, got, tc.want)
		}
	}
}

// TestBoundaryComponentsReconcilesBothAxes pins the order and the keys: the
// boundary's own entries first, then each routed module in author order, each
// module's own components before the modules nested in it, and every routed
// component keyed by the id it carries in the flattened graph rather than the
// local name it was authored under.
func TestBoundaryComponentsReconcilesBothAxes(t *testing.T) {
	t.Parallel()
	components := types.BoundaryComponents(routingBoundary())
	want := []string{"api", "data.store", "data.inner.cache"}
	got := components.Keys()
	if len(got) != len(want) {
		t.Fatalf("keys: got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys: got %v; want %v", got, want)
		}
	}
	if c, ok := components.Get("data.inner.cache"); !ok || c.Description != "inner-cache" {
		t.Errorf("a component of a nested routed module must arrive with its own payload; got %+v (present=%v)", c, ok)
	}
}

// TestBoundaryComponentsWithoutRoutingIsTheOwnAxis pins that a boundary routing
// nothing is handed back its own map, so the common shape neither copies nor
// reorders.
func TestBoundaryComponentsWithoutRoutingIsTheOwnAxis(t *testing.T) {
	t.Parallel()
	b := types.Boundary{}
	b.Components.Set("api", types.BoundaryComponent{Description: "named-api"})
	got := types.BoundaryComponents(b)
	if len(got.Keys()) != 1 || got.Keys()[0] != "api" {
		t.Errorf("keys: got %v; want [api]", got.Keys())
	}
}

// TestBoundaryComponentDeclarationsKeepsACollisionVisible is why the traversal is
// separate from the reconciled view. A component named directly AND named again
// through a routing entry places one node twice; the reconciled map can only hold
// one entry for the id, so the collision has to remain visible somewhere or the
// document's ambiguity is silently resolved by whichever entry was folded last.
func TestBoundaryComponentDeclarationsKeepsACollisionVisible(t *testing.T) {
	t.Parallel()

	data := types.AggregateRouting{}
	data.Components.Set("store", types.BoundaryComponent{Description: "routed"})
	b := types.Boundary{}
	b.Components.Set("data.store", types.BoundaryComponent{Description: "named"})
	b.Aggregates.Set("data", data)

	declarations := types.BoundaryComponentDeclarations(b)
	seen := 0
	for _, d := range declarations {
		if d.ID == "data.store" {
			seen++
		}
	}
	if seen != 2 {
		t.Errorf("both declarations of one id must be visible; saw %d in %+v", seen, declarations)
	}
	if folded := types.BoundaryComponents(b); folded.Len() != 1 {
		t.Errorf("the reconciled view holds one entry per id; got %d", folded.Len())
	}
}

func TestBoundaryRoutedModules(t *testing.T) {
	t.Parallel()
	got := types.BoundaryRoutedModules(routingBoundary())
	want := []string{"data", "data.inner"}
	if len(got) != len(want) {
		t.Fatalf("modules: got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("modules: got %v; want %v", got, want)
		}
	}
	if mods := types.BoundaryRoutedModules(types.Boundary{}); mods != nil {
		t.Errorf("a boundary routing nothing has no modules; got %v", mods)
	}
}

// TestBoundaryRoutedModulesIncludesAnEntryNamingNoComponents pins the primary
// routing shape: an entry with an empty body places the whole module, and it is
// the only record of that placement, so it has to be reported.
func TestBoundaryRoutedModulesIncludesAnEntryNamingNoComponents(t *testing.T) {
	t.Parallel()
	b := types.Boundary{}
	b.Aggregates.Set("data", types.AggregateRouting{})
	got := types.BoundaryRoutedModules(b)
	if len(got) != 1 || got[0] != "data" {
		t.Errorf("modules: got %v; want [data]", got)
	}
	if components := types.BoundaryComponents(b); components.Len() != 0 {
		t.Errorf("an entry naming no components contributes none; got %v", components.Keys())
	}
}

// TestBoundaryComponentLocatorNamesTheAuthoredKey pins that a diagnostic points at
// the key the author wrote. A routed component locates through its routing entry
// under its local name — the qualified id is a key nowhere in the document, and the
// Release schema forbids a dot in a routed component key.
func TestBoundaryComponentLocatorNamesTheAuthoredKey(t *testing.T) {
	t.Parallel()
	b := routingBoundary()
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"api", []string{"components", "api"}},
		{"data.store", []string{"aggregates", "data", "components", "store"}},
		{"data.inner.cache", []string{"aggregates", "data", "aggregates", "inner", "components", "cache"}},
		{"absent", []string{"components", "absent"}},
	} {
		got := types.BoundaryComponentLocator(b, tc.name)
		if len(got) != len(tc.want) {
			t.Errorf("locator(%q): got %v; want %v", tc.name, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("locator(%q): got %v; want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

// TestSetBoundaryComponentWritesBackToTheDeclaringAxis is the counterpart pin: a
// mutated routed component must land in the routing entry that declares it. Writing
// it to the boundary's own axis under its qualified id would leave one component
// declared twice — once as authored, once as a compiler artifact.
func TestSetBoundaryComponentWritesBackToTheDeclaringAxis(t *testing.T) {
	t.Parallel()
	b := routingBoundary()

	types.SetBoundaryComponent(&b, "data.store", types.BoundaryComponent{Description: "mutated-store"})
	types.SetBoundaryComponent(&b, "data.inner.cache", types.BoundaryComponent{Description: "mutated-cache"})
	types.SetBoundaryComponent(&b, "api", types.BoundaryComponent{Description: "mutated-api"})

	if b.Components.Len() != 1 {
		t.Errorf("the boundary's own axis must not gain an entry; keys: %v", b.Components.Keys())
	}
	if c, _ := b.Components.Get("api"); c.Description != "mutated-api" {
		t.Errorf("a directly-named component must be written on its own axis; got %q", c.Description)
	}

	components := types.BoundaryComponents(b)
	if c, ok := components.Get("data.store"); !ok || c.Description != "mutated-store" {
		t.Errorf("routed write did not land: got %+v (present=%v)", c, ok)
	}
	if c, ok := components.Get("data.inner.cache"); !ok || c.Description != "mutated-cache" {
		t.Errorf("nested routed write did not land: got %+v (present=%v)", c, ok)
	}

	// The locators must be unchanged by the writes: a write that relocated an entry
	// would silently move where a later diagnostic points.
	locator := types.BoundaryComponentLocator(b, "data.store")
	if len(locator) != 4 || locator[0] != "aggregates" || locator[3] != "store" {
		t.Errorf("a routed component's locator must survive a write-back; got %v", locator)
	}
}

// TestSetBoundaryComponentIntroducesOnTheOwnAxis pins where a component the caller
// is introducing goes, since neither axis declares it yet.
func TestSetBoundaryComponentIntroducesOnTheOwnAxis(t *testing.T) {
	t.Parallel()
	b := routingBoundary()
	types.SetBoundaryComponent(&b, "fresh", types.BoundaryComponent{Description: "new"})
	if c, ok := b.Components.Get("fresh"); !ok || c.Description != "new" {
		t.Errorf("an unknown name is introduced on the boundary's own axis; got %+v (present=%v)", c, ok)
	}
}
