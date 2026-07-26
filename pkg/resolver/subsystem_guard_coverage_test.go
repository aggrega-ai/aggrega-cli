// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// aggWithSubsystemComponent builds an Aggregate whose only component is inside a
// subsystem "svc" — the shape that exercises whether a guard recurses into
// subsystems. Before the shared instance-walker, the root-only guards iterated
// an empty top-level `components` and never reached this component.
func aggWithSubsystemComponent(name string, inst types.ComponentInstance) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set(name, inst)
	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("svc", types.Subsystem{Components: comps})
	return &types.Aggregate{Metadata: types.AggregateMetadata{Name: "t"}, Spec: types.AggregateSpec{Subsystems: subs}}
}

// subsysDBPath is the accurate authoring locator the path-aware walker must
// render for component "db" inside subsystem "svc" — NOT a flattened
// `.spec.components["db"]`.
const subsysDBPath = `.spec.subsystems.svc.components["db"]`

func requireSubsystemHalt(t *testing.T, errs []*ResolverError, want error, wantPath string) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("RED→GREEN: a subsystem component must now trip the guard (it was root-only before the walker)")
	}
	if !errors.Is(errs[0], want) {
		t.Fatalf("want errors.Is(err, %v); got %v", want, errs[0])
	}
	if !strings.Contains(errs[0].Field, wantPath) {
		t.Errorf("diagnostic must carry the accurate subsystem path %q; got %q", wantPath, errs[0].Field)
	}
}

// TestSubsystemGuardCoverage_SelfInject — a black-box self-inject violation on a
// component inside a subsystem now halts (was a root-only gap).
func TestSubsystemGuardCoverage_SelfInject(t *testing.T) {
	t.Parallel()
	agg := aggWithSubsystemComponent("db", types.ComponentInstance{
		Type:   "core/datastore/postgres",
		Inject: &types.InjectBlock{Env: map[string]any{"X": "1"}},
	})
	requireSubsystemHalt(t, guardSelfInject(normalized(t, agg), selfInjectCatalog()), ErrSelfInjectForbidden, subsysDBPath)
}

// TestSubsystemGuardCoverage_Required — an unsatisfied requiredFeature on a
// subsystem component now halts (the fourth root-only gap, surfaced and fixed by
// the shared walker).
func TestSubsystemGuardCoverage_Required(t *testing.T) {
	t.Parallel()
	agg := aggWithSubsystemComponent("db", types.ComponentInstance{Type: "core/needs-logging"})
	cat := CatalogLookup{Component: func(u string) (types.Component, bool) {
		if u == "core/needs-logging" {
			return types.Component{Spec: types.ComponentSpec{RequiredFeatures: []string{"core/logging"}}}, true
		}
		return types.Component{}, false
	}}
	requireSubsystemHalt(t, guardRequiredFeatures(normalized(t, agg), &types.Release{}, cat), ErrMissingRequiredFeature, subsysDBPath)
}

// TestSubsystemGuardCoverage_Merge — a feature-type merge conflict on a
// subsystem component now halts (was a root-only gap). The Release boundary
// attaches the same feature name with a conflicting type for the same instance.
func TestSubsystemGuardCoverage_Merge(t *testing.T) {
	t.Parallel()
	aggFeats := types.OrderedMap[types.FeatureInstance]{}
	aggFeats.Set("logging", types.FeatureInstance{Type: "core/logging-a"})
	agg := aggWithSubsystemComponent("db", types.ComponentInstance{Type: "core/server", Features: aggFeats})

	relFeats := types.OrderedMap[types.BoundaryFeature]{}
	relFeats.Set("logging", types.BoundaryFeature{Type: "core/logging-b"})
	bc := types.OrderedMap[types.BoundaryComponent]{}
	bc.Set("db", types.BoundaryComponent{Features: relFeats})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("main", types.Boundary{Components: bc})
	rel := &types.Release{}
	rel.Spec.Boundaries = boundaries

	requireSubsystemHalt(t, guardFeatureMerge(normalized(t, agg), rel), ErrFeatureMergeConflict, subsysDBPath)
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
		// Elision: the author-read locator is root-shaped, never `default`.
		want := `.spec.components["` + p.name + `"]`
		if locator := componentFieldPath(p.subsystemPath, p.name, p.synthesized); locator != want {
			t.Errorf("%s: synthesized-default component must render root-shaped %q; got %q", p.name, want, locator)
		}
	}
	// Counter-case: a subsystem a user *declares* with the name "default" is not
	// synthesized, so its components still render the qualified locator — the
	// key is the provenance flag, not the path string.
	if got := componentFieldPath("default", "a", false); got != `.spec.subsystems.default.components["a"]` {
		t.Errorf("declared default path render: got %q", got)
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

	// Elision: the same folded component renders root-shaped in diagnostics.
	var locator string
	for _, p := range collectComponentInstances(na) {
		if p.name == "api" {
			locator = componentFieldPath(p.subsystemPath, p.name, p.synthesized)
		}
	}
	if locator != `.spec.components["api"]` {
		t.Errorf("diagnostic locator must be root-shaped while the IR stays honest; got %q", locator)
	}
}

// TestCollectComponentInstances_SubsystemsPathAware pins the walker's
// path-awareness over a subsystems-bearing aggregate (Normalize is a no-op): it
// yields every component instance — including nested subsystems — each tagged
// with its dotted subsystem path, and componentFieldPath renders the accurate
// nested locator.
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
	if got := componentFieldPath("svc.inner", "c", false); got != `.spec.subsystems.svc.subsystems.inner.components["c"]` {
		t.Errorf("nested path render: got %q", got)
	}
}
