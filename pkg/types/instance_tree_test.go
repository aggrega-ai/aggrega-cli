// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"errors"
	"fmt"
	"testing"
)

// subsystemFixture builds a two-level subsystem tree in a fixed author order:
//
//	a          components c1, c2   features f1
//	  a1       components c3
//	b          components c4
func subsystemFixture() OrderedMap[Subsystem] {
	innerComps := OrderedMap[ComponentInstance]{}
	innerComps.Set("c3", ComponentInstance{Type: "core/server"})
	inner := Subsystem{Components: innerComps}

	aComps := OrderedMap[ComponentInstance]{}
	aComps.Set("c1", ComponentInstance{Type: "core/server"})
	aComps.Set("c2", ComponentInstance{Type: "core/server"})
	aFeats := OrderedMap[FeatureInstance]{}
	aFeats.Set("f1", FeatureInstance{Type: "core/metrics"})
	aSubs := OrderedMap[Subsystem]{}
	aSubs.Set("a1", inner)
	a := Subsystem{Components: aComps, Features: aFeats, Subsystems: aSubs}

	bComps := OrderedMap[ComponentInstance]{}
	bComps.Set("c4", ComponentInstance{Type: "core/server"})
	b := Subsystem{Components: bComps}

	roots := OrderedMap[Subsystem]{}
	roots.Set("a", a)
	roots.Set("b", b)
	return roots
}

func dottedPath(parentPath, name string, _ bool) string {
	if parentPath == "" {
		return name
	}
	return parentPath + "." + name
}

// TestWalkInstanceTree_CallbackOrderIsFixed pins the one property every consumer
// depends on: per container the walk composes the path, enters, visits each
// component instance in author order, leaves, and only then descends into child
// containers — with roots and every level in author order.
//
// The order is load-bearing rather than incidental: several consumers emit
// diagnostics straight from the visit sequence with no re-sort, so a change here
// changes what a user reads and breaks byte-identical output.
func TestWalkInstanceTree_CallbackOrderIsFixed(t *testing.T) {
	t.Parallel()
	var trace []string
	err := WalkInstanceTree(subsystemFixture(), SubsystemTree(), "R", struct{}{},
		InstanceTreeVisitor[Subsystem, ComponentInstance, struct{}]{
			Path: dottedPath,
			Enter: func(_ Subsystem, name, path string, acc struct{}) struct{} {
				trace = append(trace, fmt.Sprintf("enter %s @%s", name, path))
				return acc
			},
			Visit: func(_ ComponentInstance, name, path string, _ bool, _ struct{}) error {
				trace = append(trace, fmt.Sprintf("visit %s @%s", name, path))
				return nil
			},
			Leave: func(_ Subsystem, name, path string, _ struct{}) {
				trace = append(trace, fmt.Sprintf("leave %s @%s", name, path))
			},
		})
	if err != nil {
		t.Fatalf("WalkInstanceTree: %v", err)
	}

	want := []string{
		"enter a @R.a",
		"visit c1 @R.a",
		"visit c2 @R.a",
		"leave a @R.a",
		"enter a1 @R.a.a1",
		"visit c3 @R.a.a1",
		"leave a1 @R.a.a1",
		"enter b @R.b",
		"visit c4 @R.b",
		"leave b @R.b",
	}
	if len(trace) != len(want) {
		t.Fatalf("trace = %v (%d entries), want %v (%d)", trace, len(trace), want, len(want))
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Errorf("trace[%d] = %q, want %q", i, trace[i], want[i])
		}
	}
}

// TestWalkInstanceTree_ContainerWithNoInstancesStillEntersAndLeaves pins the
// reason the container-level callbacks exist at all: a container carrying only
// scope-wide attachments and no component instances has real work to do, and a
// per-instance callback could never fire for it.
func TestWalkInstanceTree_ContainerWithNoInstancesStillEntersAndLeaves(t *testing.T) {
	t.Parallel()
	feats := OrderedMap[FeatureInstance]{}
	feats.Set("metrics", FeatureInstance{Type: "core/metrics"})
	roots := OrderedMap[Subsystem]{}
	roots.Set("observability", Subsystem{Features: feats})

	var trace []string
	err := WalkInstanceTree(roots, SubsystemTree(), "", struct{}{},
		InstanceTreeVisitor[Subsystem, ComponentInstance, struct{}]{
			Path: JoinSubsystemPath,
			Enter: func(sub Subsystem, name, path string, acc struct{}) struct{} {
				trace = append(trace, fmt.Sprintf("enter %s @%s features=%d", name, path, sub.Features.Len()))
				return acc
			},
			Visit: func(_ ComponentInstance, name, _ string, _ bool, _ struct{}) error {
				trace = append(trace, "visit "+name)
				return nil
			},
			Leave: func(sub Subsystem, name, path string, _ struct{}) {
				trace = append(trace, fmt.Sprintf("leave %s @%s features=%d", name, path, sub.Features.Len()))
			},
		})
	if err != nil {
		t.Fatalf("WalkInstanceTree: %v", err)
	}
	want := []string{
		"enter observability @observability features=1",
		"leave observability @observability features=1",
	}
	if len(trace) != len(want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Errorf("trace[%d] = %q, want %q", i, trace[i], want[i])
		}
	}
}

// TestWalkInstanceTree_AccumulatorFlowsOuterToInnerOnly pins that a container's
// accumulator reaches its own components and its descendants, and never a sibling.
// A cascade that leaked sideways would give a component the annotations of an
// unrelated scope.
func TestWalkInstanceTree_AccumulatorFlowsOuterToInnerOnly(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	err := WalkInstanceTree(subsystemFixture(), SubsystemTree(), "", "root",
		InstanceTreeVisitor[Subsystem, ComponentInstance, string]{
			Path: dottedPath,
			Enter: func(_ Subsystem, name, _ string, parent string) string {
				return parent + "/" + name
			},
			Visit: func(_ ComponentInstance, name, _ string, _ bool, acc string) error {
				seen[name] = acc
				return nil
			},
		})
	if err != nil {
		t.Fatalf("WalkInstanceTree: %v", err)
	}
	want := map[string]string{
		"c1": "root/a",
		"c2": "root/a",
		"c3": "root/a/a1",
		"c4": "root/b",
	}
	for name, wantAcc := range want {
		if seen[name] != wantAcc {
			t.Errorf("component %s saw accumulator %q, want %q", name, seen[name], wantAcc)
		}
	}
}

var errStopWalk = errors.New("stop")

// TestWalkInstanceTree_VisitErrorAbortsImmediately pins that a failing visit stops
// the whole walk — no further instance, no further container — and that the error
// surfaces unwrapped so a caller's own wrapping is what a user sees.
func TestWalkInstanceTree_VisitErrorAbortsImmediately(t *testing.T) {
	t.Parallel()
	var visited []string
	err := WalkInstanceTree(subsystemFixture(), SubsystemTree(), "", struct{}{},
		InstanceTreeVisitor[Subsystem, ComponentInstance, struct{}]{
			Path: dottedPath,
			Visit: func(_ ComponentInstance, name, _ string, _ bool, _ struct{}) error {
				visited = append(visited, name)
				if name == "c2" {
					return errStopWalk
				}
				return nil
			},
		})
	if !errors.Is(err, errStopWalk) {
		t.Fatalf("err = %v, want %v", err, errStopWalk)
	}
	if len(visited) != 2 || visited[0] != "c1" || visited[1] != "c2" {
		t.Errorf("visited = %v, want [c1 c2]", visited)
	}
}

// TestWalkInstanceTree_NilCallbacksAreSkipped pins that every callback is
// optional: with no Path the container path stays the root path, and with no Enter
// the parent's accumulator flows down untouched.
func TestWalkInstanceTree_NilCallbacksAreSkipped(t *testing.T) {
	t.Parallel()
	var paths []string
	var accs []int
	err := WalkInstanceTree(subsystemFixture(), SubsystemTree(), "fixed", 7,
		InstanceTreeVisitor[Subsystem, ComponentInstance, int]{
			Visit: func(_ ComponentInstance, _, path string, _ bool, acc int) error {
				paths = append(paths, path)
				accs = append(accs, acc)
				return nil
			},
		})
	if err != nil {
		t.Fatalf("WalkInstanceTree: %v", err)
	}
	if len(paths) != 4 {
		t.Fatalf("visited %d instances, want 4", len(paths))
	}
	for i := range paths {
		if paths[i] != "fixed" {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], "fixed")
		}
		if accs[i] != 7 {
			t.Errorf("accs[%d] = %d, want 7", i, accs[i])
		}
	}
}

// TestWalkInstanceTree_SynthesizedProvenanceReachesVisit pins that the container's
// synthesized flag reaches both the path composer and the per-instance callback,
// which is how a consumer renders a component folded into a generated scope at the
// authoring shape the author actually typed.
func TestWalkInstanceTree_SynthesizedProvenanceReachesVisit(t *testing.T) {
	t.Parallel()
	comps := OrderedMap[ComponentInstance]{}
	comps.Set("api", ComponentInstance{Type: "core/server"})
	roots := OrderedMap[Subsystem]{}
	roots.Set("default", Subsystem{Components: comps, Synthesized: true})

	var gotPath string
	var gotSynthesized bool
	var pathSawSynthesized bool
	err := WalkInstanceTree(roots, SubsystemTree(), ".spec", struct{}{},
		InstanceTreeVisitor[Subsystem, ComponentInstance, struct{}]{
			Path: func(parentPath, name string, synthesized bool) string {
				pathSawSynthesized = synthesized
				if synthesized {
					return parentPath
				}
				return parentPath + ".subsystems." + name
			},
			Visit: func(_ ComponentInstance, _, path string, synthesized bool, _ struct{}) error {
				gotPath = path
				gotSynthesized = synthesized
				return nil
			},
		})
	if err != nil {
		t.Fatalf("WalkInstanceTree: %v", err)
	}
	if !pathSawSynthesized {
		t.Error("path composer did not receive the synthesized flag")
	}
	if !gotSynthesized {
		t.Error("visit did not receive the synthesized flag")
	}
	if gotPath != ".spec" {
		t.Errorf("path = %q, want %q", gotPath, ".spec")
	}
}

// TestWalkInstanceTree_ServesTheBoundaryTree pins that ONE walk serves both
// container families. The Release boundary tree is isomorphic to the Aggregate
// subsystem tree on every axis the walk reads — nested containers, contained
// component instances, and synthesized provenance — so [BoundaryTree] instantiates
// the same walk over [Boundary] and [BoundaryComponent], with the boundary tree's
// own slash-separated path spelling.
func TestWalkInstanceTree_ServesTheBoundaryTree(t *testing.T) {
	t.Parallel()
	innerComps := OrderedMap[BoundaryComponent]{}
	innerComps.Set("payments", BoundaryComponent{SubDirectory: "pay"})
	inner := Boundary{Components: innerComps, SyncWave: 3}

	outerComps := OrderedMap[BoundaryComponent]{}
	outerComps.Set("api", BoundaryComponent{SubDirectory: "svc"})
	innerBoundaries := OrderedMap[Boundary]{}
	innerBoundaries.Set("eks-main", inner)
	outer := Boundary{Components: outerComps, Boundaries: innerBoundaries, Directory: "prod"}

	roots := OrderedMap[Boundary]{}
	roots.Set("aws-prod", outer)

	type record struct {
		name         string
		path         string
		subDirectory string
		syncWave     int
	}
	var got []record
	err := WalkInstanceTree(roots, BoundaryTree(), "", 0,
		InstanceTreeVisitor[Boundary, BoundaryComponent, int]{
			Path: func(parentPath, name string, _ bool) string {
				if parentPath == "" {
					return name
				}
				return parentPath + "/" + name
			},
			// The sync wave is a container field, so it reaches the instance through
			// the accumulator rather than through a widened callback signature.
			Enter: func(b Boundary, _, _ string, _ int) int { return b.SyncWave },
			Visit: func(bc BoundaryComponent, name, path string, _ bool, syncWave int) error {
				got = append(got, record{name: name, path: path, subDirectory: bc.SubDirectory, syncWave: syncWave})
				return nil
			},
		})
	if err != nil {
		t.Fatalf("WalkInstanceTree: %v", err)
	}
	want := []record{
		{name: "api", path: "aws-prod", subDirectory: "svc", syncWave: 0},
		{name: "payments", path: "aws-prod/eks-main", subDirectory: "pay", syncWave: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestComponentInstances_AuthorOrderComponentsBeforeNestedSubtree pins the
// iteration the resolver guards and the flatten path both read: every component of
// a subsystem, then that subsystem's whole subtree, then the next sibling — all in
// author order, with the dotted subsystem path and synthesized provenance attached.
func TestComponentInstances_AuthorOrderComponentsBeforeNestedSubtree(t *testing.T) {
	t.Parallel()
	agg := &Aggregate{}
	agg.Spec.Subsystems = subsystemFixture()

	refs := Normalize(agg).ComponentInstances()
	type pair struct{ name, path string }
	got := make([]pair, 0, len(refs))
	for _, r := range refs {
		got = append(got, pair{r.Name, r.SubsystemPath})
		if r.Synthesized {
			t.Errorf("component %s reported synthesized provenance for an authored subsystem", r.Name)
		}
	}
	want := []pair{
		{"c1", "a"},
		{"c2", "a"},
		{"c3", "a.a1"},
		{"c4", "b"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// referenceDescent is a deliberate, independent re-implementation of the
// straightforward recursive descent: for each container in author order, emit its
// component instances, then descend. It exists to cross-check
// [WalkInstanceTree]'s emission order against an implementation that shares no
// code with it.
//
// It is NOT dead duplication and must not be "unified" with the walk it checks:
// folding it onto the walk would make the test assert the walk against itself,
// which is exactly the blinding this cross-check exists to prevent. The property
// under test — that the shared walk emits in the same order a hand-rolled descent
// does — is what lets every consumer move onto the walk without moving a single
// diagnostic or a single rendered byte.
func referenceDescent(parentPath string, containers OrderedMap[Subsystem], out *[]string) {
	for name, sub := range containers.All() {
		path := name
		if parentPath != "" {
			path = parentPath + "." + name
		}
		for instanceName := range sub.Components.All() {
			*out = append(*out, path+"/"+instanceName)
		}
		referenceDescent(path, sub.Subsystems, out)
	}
}

// TestWalkInstanceTree_MatchesHandRolledDescent pins the walk's emission order
// against [referenceDescent] over a tree with siblings at two nesting levels and
// multiple components per container.
func TestWalkInstanceTree_MatchesHandRolledDescent(t *testing.T) {
	t.Parallel()
	// A deeper fixture than the shared one: two roots, two children under one of
	// them, and a grandchild, so sibling ordering and depth ordering are both
	// exercised.
	leafComps := OrderedMap[ComponentInstance]{}
	leafComps.Set("g1", ComponentInstance{})
	leaf := Subsystem{Components: leafComps}
	a1Subs := OrderedMap[Subsystem]{}
	a1Subs.Set("deep", leaf)
	a1Comps := OrderedMap[ComponentInstance]{}
	a1Comps.Set("x1", ComponentInstance{})
	a1Comps.Set("x2", ComponentInstance{})
	a1 := Subsystem{Components: a1Comps, Subsystems: a1Subs}
	a2 := Subsystem{}
	aSubs := OrderedMap[Subsystem]{}
	aSubs.Set("a1", a1)
	aSubs.Set("a2", a2)
	aComps := OrderedMap[ComponentInstance]{}
	aComps.Set("c1", ComponentInstance{})
	a := Subsystem{Components: aComps, Subsystems: aSubs}
	bComps := OrderedMap[ComponentInstance]{}
	bComps.Set("c2", ComponentInstance{})
	roots := OrderedMap[Subsystem]{}
	roots.Set("a", a)
	roots.Set("b", Subsystem{Components: bComps})

	var want []string
	referenceDescent("", roots, &want)

	var got []string
	err := WalkInstanceTree(roots, SubsystemTree(), "", struct{}{},
		InstanceTreeVisitor[Subsystem, ComponentInstance, struct{}]{
			Path: JoinSubsystemPath,
			Visit: func(_ ComponentInstance, name, path string, _ bool, _ struct{}) error {
				got = append(got, path+"/"+name)
				return nil
			},
		})
	if err != nil {
		t.Fatalf("WalkInstanceTree: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("walk emitted %v, hand-rolled descent emitted %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: walk emitted %q, hand-rolled descent emitted %q", i, got[i], want[i])
		}
	}
}

func TestJoinSubsystemPath(t *testing.T) {
	t.Parallel()
	if got := JoinSubsystemPath("", "a", false); got != "a" {
		t.Errorf("JoinSubsystemPath(\"\", \"a\") = %q, want %q", got, "a")
	}
	if got := JoinSubsystemPath("a", "b", false); got != "a.b" {
		t.Errorf("JoinSubsystemPath(\"a\", \"b\") = %q, want %q", got, "a.b")
	}
	// The synthesized flag does not change the dotted path: consumers that render a
	// generated scope differently key on the flag, not on the path string.
	if got := JoinSubsystemPath("", "default", true); got != "default" {
		t.Errorf("JoinSubsystemPath(\"\", \"default\", true) = %q, want %q", got, "default")
	}
}
