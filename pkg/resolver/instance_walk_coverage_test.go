// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// aggWithSubsystemComponent builds an Aggregate whose only component is inside a
// subsystem "svc" — the shape that exercises whether a walk recurses into
// subsystems rather than iterating the top-level `components` alone.
func aggWithSubsystemComponent(name string, inst types.ComponentInstance) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set(name, inst)
	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("svc", types.Subsystem{Components: comps})
	return &types.Aggregate{Metadata: types.AggregateMetadata{Name: "t"}, Spec: types.AggregateSpec{Subsystems: subs}}
}

// TestTheNodeAxisPresentsAComponentDeclaredInsideASubsystem pins the coordinate
// every node-keyed rule is invoked per: a component the document declares inside
// a subsystem is presented, and the locator it is presented under names the
// subsystem the author wrote rather than a flattened root path.
//
// It is asserted over a rule rather than over the axis alone because a rule is
// what a reader cares about, and it is asserted in both directions: the same
// component with nothing to report must draw nothing, or a rule that reported
// unconditionally would satisfy the first half.
func TestTheNodeAxisPresentsAComponentDeclaredInsideASubsystem(t *testing.T) {
	t.Parallel()

	ask := func(inject *types.InjectBlock) []string {
		agg := aggWithSubsystemComponent("db", types.ComponentInstance{
			Type:   "core/datastore/postgres", // declares no injection allowlist
			Inject: inject,
		})
		na := normalized(t, agg)
		view := newNormalizedView(na, nil, selfInjectCatalog(), nil)
		objections, err := constraint.RunOnNodes[constraint.NormalizedView](
			nil, newCoordinateSet(view), view, selfInjectBlockAllowed)
		if err != nil {
			t.Fatalf("the node walk returned an error: %v", err)
		}
		return locatorsOf(objections)
	}

	// The rule positions at the injection block the author wrote, so the expected
	// locator is the subsystem-scoped component path with that block's key on it.
	// The segments before it are what this test is about.
	want := []string{".spec.subsystems.svc.components.db.inject"}
	if got := ask(&types.InjectBlock{Env: []any{map[string]any{"in": "X", "value": "1"}}}); !equalStrings(got, want) {
		t.Errorf("raised sequence = %v; want %v — the locator names the subsystem the author "+
			"wrote", got, want)
	}
	if got := ask(nil); len(got) != 0 {
		t.Errorf("the same component with no injection block must draw nothing; got %v", got)
	}
}

// TestCollectComponentInstances_RootOnlyFoldsToDefault pins the walker's
// completeness over a root-only aggregate: Normalize folds the top-level
// components into the synthesized `default` subsystem, so the walker yields every
// instance honestly tagged with subsystemPath "default" AND the synthesized
// flag. The author-read locator elides the synthesized scope — a folded
// component renders at the root shape it was typed at, never under `default`.
func TestCollectComponentInstances_RootOnlyFoldsToDefault(t *testing.T) {
	t.Parallel()
	root := types.OrderedMap[types.ComponentInstance]{}
	root.Set("a", types.ComponentInstance{Type: "x"})
	root.Set("b", types.ComponentInstance{Type: "x"})
	root.Set("c", types.ComponentInstance{Type: "x"})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: root}}

	got := collectComponentInstances(normalized(t, agg))
	if len(got) != 3 {
		t.Fatalf("walker must yield every root instance (folded into default); want 3, got %d", len(got))
	}
	for _, p := range got {
		// Honest IR: the walker still tags the real folded location.
		if p.subsystemPath != "default" {
			t.Errorf("%s: root component must fold into the synthesized default subsystem; want subsystemPath %q, got %q", p.name, "default", p.subsystemPath)
		}
		if !p.synthesized {
			t.Errorf("%s: folded root component must carry the synthesized-provenance flag", p.name)
		}
		// Elision: the site an author reads is root-shaped, never `default`.
		want := constraint.AuthoredSite{Component: p.name}
		if site := constraint.ComponentSite(p.name, p.subsystemPath, p.synthesized); site != want {
			t.Errorf("%s: synthesized-default component must be sited root-shaped %+v; got %+v", p.name, want, site)
		}
	}
	// Counter-case: a subsystem a user *declares* with the name "default" is not
	// synthesized, so its components keep the qualified site — the key is the
	// provenance flag, not the path string.
	if got := constraint.ComponentSite("a", "default", false); got != (constraint.AuthoredSite{ScopePath: "default", Component: "a"}) {
		t.Errorf("declared default site: got %+v", got)
	}
}

// TestSynthesizedDefault_HonestIRWhileDiagnosticElided proves the two halves of
// the synthesized-default contract hold simultaneously: the normalized Aggregate
// still carries the literal `default` subsystem marked Synthesized (honest
// structure — no elision in the data), WHILE the author-read diagnostic locator
// for a folded component is root-shaped (elision at addressing only).
func TestSynthesizedDefault_HonestIRWhileDiagnosticElided(t *testing.T) {
	t.Parallel()
	root := types.OrderedMap[types.ComponentInstance]{}
	root.Set("api", types.ComponentInstance{Type: "x"})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: root}}
	na := normalized(t, agg)

	// Honest structure: the literal default subsystem is present and marked.
	def, ok := na.Aggregate().Spec.Subsystems.Get("default")
	if !ok || !def.Synthesized {
		t.Fatalf("normalized Aggregate must carry a literal `default` subsystem marked Synthesized; ok=%v synthesized=%v", ok, def.Synthesized)
	}
	if _, rooted := na.Aggregate().Spec.Components.Get("api"); rooted {
		t.Error("the folded root surface must be empty in the normalized view")
	}

	// Elision: the same folded component is sited root-shaped in diagnostics.
	var site constraint.AuthoredSite
	for _, p := range collectComponentInstances(na) {
		if p.name == "api" {
			site = constraint.ComponentSite(p.name, p.subsystemPath, p.synthesized)
		}
	}
	if site != (constraint.AuthoredSite{Component: "api"}) {
		t.Errorf("diagnostic site must be root-shaped while the IR stays honest; got %+v", site)
	}
}

// TestCollectComponentInstances_SubsystemsPathAware pins the walker's
// path-awareness over a subsystems-bearing aggregate (Normalize is a no-op): it
// yields every component instance — including nested subsystems — each tagged
// with its dotted subsystem path, and the component's site keeps that path.
func TestCollectComponentInstances_SubsystemsPathAware(t *testing.T) {
	t.Parallel()
	innerComps := types.OrderedMap[types.ComponentInstance]{}
	innerComps.Set("c", types.ComponentInstance{Type: "x"})
	innerSubs := types.OrderedMap[types.Subsystem]{}
	innerSubs.Set("inner", types.Subsystem{Components: innerComps})
	svcComps := types.OrderedMap[types.ComponentInstance]{}
	svcComps.Set("b", types.ComponentInstance{Type: "x"})
	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("svc", types.Subsystem{Components: svcComps, Subsystems: innerSubs})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Subsystems: subs}}

	got := collectComponentInstances(normalized(t, agg))
	if len(got) != 2 {
		t.Fatalf("walker must yield every subsystem instance (nested included); want 2, got %d", len(got))
	}
	wantPath := map[string]string{"b": "svc", "c": "svc.inner"}
	for _, p := range got {
		if wantPath[p.name] != p.subsystemPath {
			t.Errorf("%s: want subsystemPath %q; got %q", p.name, wantPath[p.name], p.subsystemPath)
		}
	}
	if got := constraint.ComponentSite("c", "svc.inner", false); got != (constraint.AuthoredSite{ScopePath: "svc.inner", Component: "c"}) {
		t.Errorf("nested site: got %+v", got)
	}
}
