// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// selfInjectNodes enumerates the single node a row of the pair below describes.
type selfInjectNodes struct {
	node constraint.NodeInstance
}

func (n selfInjectNodes) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{n.node}
}

// selfInjectView answers the facts the declared self-inject rules read, for one
// component instance, out of the two authored values that carry them: the
// instance itself and the catalog entry its type names.
//
// Every answer is computed here the way the fact's own source field says it is
// read, rather than being set row by row. A stub whose booleans were assigned per
// row would be asserting the distinction instead of deriving it, and would keep
// passing if the authored surface stopped supporting the distinction at all.
//
// It embeds the view interface rather than implementing the whole of it, so a
// fact a check reads and this stub does not answer fails at the call instead of
// returning a plausible zero. A stub answering every fact with a zero value
// would let a check reading the wrong fact pass here.
type selfInjectView struct {
	constraint.NormalizedView

	node     string
	instance types.ComponentInstance
	catalog  CatalogLookup
}

// instanceFor refuses a node this view does not carry, for the reason the
// document view refuses another document: answering this instance's facts under
// another node's name would be silently wrong.
func (v selfInjectView) instanceFor(node string) types.ComponentInstance {
	if node != v.node {
		panic("selfInjectView asked about node " + node + "; it carries " + v.node)
	}
	return v.instance
}

func (v selfInjectView) AuthoredInjectBlockPresent(node string) bool {
	return v.instanceFor(node).Inject != nil
}

// AuthoredInjectMechanismNames is answered beside the authorship fact, and it is
// answered through the same enumeration production reads, so this view carries
// the indiscernibility the rule turns on rather than merely being asserted to.
// A rule reformulated onto this fact can then be run against this view and shown
// to accept the document it exists to refuse.
func (v selfInjectView) AuthoredInjectMechanismNames(node string) []string {
	return InjectMechanismNames(v.instanceFor(node).Inject)
}

func (v selfInjectView) InstanceComponentType(node string) (string, bool) {
	return v.instanceFor(node).Type, true
}

func (v selfInjectView) CatalogComponentPresent(componentType string) bool {
	_, found := v.catalog.Component(componentType)
	return found
}

func (v selfInjectView) DeclaredInjectMechanismNames(componentType string) []string {
	entry, found := v.catalog.Component(componentType)
	if !found {
		return nil
	}
	return entry.Spec.Inject
}

// TestSelfInjectSeparatesAnEmptyBlockFromAnAbsentOne pins the distinction the
// self-inject allowlist rule turns on, and the reason it is worth pinning is
// that the distinction is invisible in the mechanism names the block yields.
//
// A component type declaring no injection allowlist reserves delivery to its
// Recipe, so an instance of it that writes an injection block at all is
// refused. Writing an EMPTY block is still writing one: the author has
// dictated delivery on a type that permits none, and the refusal names the
// block. Omitting the block entirely dictates nothing and is accepted.
//
// Both cases yield the same mechanism names — none — so a rule reading only
// the mechanisms an instance declares cannot tell them apart while they
// require opposite verdicts. Any reformulation of this rule has to read
// whether the block was written, not what it carries; the two assertions
// below are one pair and neither is meaningful alone.
//
// The rule is read through the framework's own node traversal rather than by
// calling its predicate, and both cases are driven from ONE instance — so what
// follows is one authored document read under two verdicts rather than two
// fixtures that happen to differ.
func TestSelfInjectSeparatesAnEmptyBlockFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		inject    *types.InjectBlock
		wantRaise bool
	}{
		{
			name:      "no block written",
			inject:    nil,
			wantRaise: false,
		},
		{
			name:      "block written and left empty",
			inject:    &types.InjectBlock{},
			wantRaise: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			instance := types.ComponentInstance{
				Type:   "core/datastore/postgres", // declares no injection allowlist
				Inject: tc.inject,
			}

			objections, err := constraint.RunOnNodes[constraint.NormalizedView](
				nil,
				selfInjectNodes{node: constraint.NodeInstance{ID: "db", AuthoredName: "db"}},
				selfInjectView{node: "db", instance: instance, catalog: selfInjectCatalog()},
				selfInjectBlockAllowed,
			)
			if err != nil {
				t.Fatalf("the declared rule's traversal returned an error: %v", err)
			}

			if tc.wantRaise {
				if len(objections) != 1 {
					t.Fatalf("the declared rule raised %d objection(s) for an instance that wrote an "+
						"injection block on a type permitting none; want exactly 1.\n"+
						"INVARIANT: the declared rule reads whether the block was written, so it refuses "+
						"a block written and left empty.\n"+
						"CONSEQUENCE: raising nothing here means the declared rule reads what the block "+
						"carries after all, and it accepts the document the rule exists to refuse.\n"+
						"got: %v", len(objections), objections)
				}
				if !errors.Is(objections[0].Sentinel(), ErrSelfInjectForbidden()) {
					t.Errorf("the declared objection carries %v; want the forbidden-self-injection rule.\n"+
						"INVARIANT: both readings of this rule are identified by one sentinel.\n"+
						"CONSEQUENCE: the two readings stop being comparable, and a caller matching the "+
						"sentinel sees one of them only.", objections[0].Sentinel())
				}
				return
			}
			if len(objections) != 0 {
				t.Fatalf("the declared rule raised %d objection(s) for an instance that wrote no "+
					"injection block; want 0.\n"+
					"INVARIANT: dictating no delivery is not dictating forbidden delivery, and the "+
					"declared rule separates that from a block written and left empty.\n"+
					"CONSEQUENCE: raising here refuses a document every component that simply has no "+
					"injection block can ship.\ngot: %v", len(objections), objections)
			}
		})
	}

	// The two inputs above are indiscernible by the mechanism names they
	// declare. This is the half that makes the pair a finding rather than two
	// unrelated rows: the verdicts differ and this input does not.
	absent := InjectMechanismNames(nil)
	empty := InjectMechanismNames(&types.InjectBlock{})
	if len(absent) != 0 || len(empty) != 0 {
		t.Fatalf("the mechanism names were %v for no block and %v for an empty block; want both empty.\n"+
			"INVARIANT: neither an absent block nor an empty one dictates any delivery mechanism.\n"+
			"CONSEQUENCE: if either is non-empty the indiscernibility below is not the one described, "+
			"and a rule reformulated around mechanism names would behave differently than measured.",
			absent, empty)
	}

	// The same indiscernibility, read through the surface a declared rule
	// actually reads. The check above establishes it of the enumeration; this
	// establishes it of the view, and only the second one is what a declared
	// rule would be reformulated against.
	viewFor := func(inject *types.InjectBlock) selfInjectView {
		return selfInjectView{
			node:     "db",
			instance: types.ComponentInstance{Type: "core/datastore/postgres", Inject: inject},
			catalog:  selfInjectCatalog(),
		}
	}
	viewAbsent := viewFor(nil).AuthoredInjectMechanismNames("db")
	viewEmpty := viewFor(&types.InjectBlock{}).AuthoredInjectMechanismNames("db")
	if len(viewAbsent) != 0 || len(viewEmpty) != 0 {
		t.Fatalf("the view reported mechanisms %v for no block and %v for an empty block; want both empty.\n"+
			"INVARIANT: the mechanism fact is blind to the distinction the authorship fact carries.\n"+
			"CONSEQUENCE: if the view can already tell them apart, the authorship fact is not what "+
			"separates them and this pair no longer measures what it says it does.",
			viewAbsent, viewEmpty)
	}
	// The authorship fact, on the same two views, does separate them — which is
	// the positive half, and without it the two assertions above are satisfied
	// by a view that reports nothing at all.
	if viewFor(nil).AuthoredInjectBlockPresent("db") {
		t.Errorf("the view reported an injection block for an instance that wrote none.\n" +
			"INVARIANT: authorship is reported positively, so no block written reports false.\n" +
			"CONSEQUENCE: a rule reading it refuses every component that has no injection block.")
	}
	if !viewFor(&types.InjectBlock{}).AuthoredInjectBlockPresent("db") {
		t.Errorf("the view reported no injection block for an instance that wrote an empty one.\n" +
			"INVARIANT: writing the block is what is reported, whatever the block carries.\n" +
			"CONSEQUENCE: the fact collapses onto the mechanism names and separates nothing.")
	}
}
