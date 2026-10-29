// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// attachmentRoutingView presents where each boundary-scope attachment's output
// went, and nothing else: the rule under test reads that one fact, so any other
// read panics through the embedded interface rather than answering a zero value.
type attachmentRoutingView struct {
	constraint.DispatchedView
	routing map[boundaryFRKey]constraint.AttachmentOutputRouting
}

func (v attachmentRoutingView) BoundaryAttachmentOutputRouting(
	boundary string, attachment string,
) (constraint.AttachmentOutputRouting, bool) {
	got, ok := v.routing[boundaryFRKey{BoundaryPath: boundary, FRInstance: attachment}]
	return got, ok
}

// declaredBoundaryFeatures builds the graph's record of the attachments each
// boundary declares on itself, in the order given.
func declaredBoundaryFeatures(boundaries ...[]string) types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]] {
	var out types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]
	for _, entry := range boundaries {
		var feats types.OrderedMap[types.IRBoundaryFeature]
		for _, instance := range entry[1:] {
			feats.Set(instance, types.IRBoundaryFeature{})
		}
		out.Set(entry[0], feats)
	}
	return out
}

// TestBoundaryAttachmentFragmentsReachAnAssembler_RefusesOnlyFragmentsNothingFolds
// drives the rule over where a boundary-scope attachment's output went through the
// traversal a build invokes it through, over every cell of its grid: the fact
// answered or not, fragments or none, a group handed to an assembler or not.
//
// One attachment per cell, at one boundary, in one run. The rule must raise for
// the one cell holding the fault — fragments in a group nothing assembles — and
// for no other, and the finding must be positioned at that attachment's own entry
// in the Release and name the boundary, the Feature, the dispatch path and the
// provider. The cells it must pass over are the controls: fragments a bound
// assembler folds, files placed as files in a group that is not assembled (a
// provider whose output is not folded), files in an assembled group, and an
// attachment the render routed nothing for.
func TestBoundaryAttachmentFragmentsReachAnAssembler_RefusesOnlyFragmentsNothingFolds(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the rule panicked: %v", r)
		}
	}()

	const boundary = "edge"
	cells := []struct {
		attachment string
		routed     bool
		routing    constraint.AttachmentOutputRouting
		refused    bool
	}{
		{"unassembled", true, constraint.AttachmentOutputRouting{AssemblyPath: "out/edge/compose", Provider: "compose", Fragments: true}, true},
		{"assembled", true, constraint.AttachmentOutputRouting{AssemblyPath: "out/stack/compose", Provider: "compose", Fragments: true, GroupAssembles: true}, false},
		{"placed-flat", true, constraint.AttachmentOutputRouting{AssemblyPath: "out/edge/helm", Provider: "helm"}, false},
		{"placed-assembled", true, constraint.AttachmentOutputRouting{AssemblyPath: "out/edge/terraform", Provider: "terraform", GroupAssembles: true}, false},
		{"unrouted", false, constraint.AttachmentOutputRouting{}, false},
	}

	routing := map[boundaryFRKey]constraint.AttachmentOutputRouting{}
	routed := map[boundaryFRKey]boundaryAttachmentRoute{}
	rendered := map[boundaryFRKey][]EmittedFile{}
	names := []string{boundary}
	for _, c := range cells {
		names = append(names, c.attachment)
		key := boundaryFRKey{BoundaryPath: boundary, FRInstance: c.attachment}
		// Every cell is on the axis, so the unrouted one is asked and must answer
		// nothing — rather than being left out, which would prove nothing about it.
		routed[key] = boundaryAttachmentRoute{}
		rendered[key] = nil
		if c.routed {
			routing[key] = c.routing
		}
	}
	order := boundaryFRRoutingOrder(declaredBoundaryFeatures(names), rendered)
	axes := newRoutedBoundaryAttachments("release.yaml", order, routed)
	var view constraint.DispatchedView = attachmentRoutingView{routing: routing}

	raised, err := constraint.RunOnBoundaryAttachments(
		func() error { return nil }, axes, axes, view,
		BoundaryAttachmentFragmentsReachAnAssembler,
	)
	if err != nil {
		t.Fatalf("the traversal failed: %v", err)
	}

	var refused []string
	for _, c := range cells {
		if c.refused {
			refused = append(refused, ".spec.boundaries."+boundary+".features."+c.attachment)
		}
	}
	var got []string
	for _, o := range raised {
		got = append(got, o.Locator())
		if !errors.Is(o.Sentinel(), ErrBoundaryContributionNotAssembled()) {
			t.Errorf("the finding at %s carries %v; want the boundary contribution condition", o.Locator(), o.Sentinel())
		}
		if o.SourceFile() != "release.yaml" {
			t.Errorf("the finding is positioned in %q; want the Release", o.SourceFile())
		}
		message := constraint.ObjectionMessage(o)
		for _, want := range []string{
			`boundary "edge"`, `feature "unassembled"`, `dispatch path "out/edge/compose"`, `provider "compose"`,
		} {
			if !strings.Contains(message, want) {
				t.Errorf("the refusal does not carry %s:\n%s", want, message)
			}
		}
	}
	if !reflect.DeepEqual(got, refused) {
		t.Fatalf("the rule raised at %v; want exactly %v", got, refused)
	}
}

// TestTheRoutingRecordReadsTheGroupsFinalizationBranch holds the record the rule
// reads to the branch a group's finalization takes: a group is handed to an
// assembler exactly where its elected engine can assemble and it has a path to
// assemble at, and a group no component joined elects nothing.
func TestTheRoutingRecordReadsTheGroupsFinalizationBranch(t *testing.T) {
	t.Parallel()

	group := func(engine Provider) *pendingDispatch {
		pd := &pendingDispatch{}
		if engine != nil {
			pd.assemblerCandidates = []assemblerCandidate{{engine: engine}}
		}
		return pd
	}
	for _, tc := range []struct {
		name      string
		group     *pendingDispatch
		path      string
		assembles bool
	}{
		{"a group no component joined", group(nil), "out/edge/compose", false},
		{"a group whose elected engine folds", group(layoutEngine{"merge"}), "out/edge/compose", true},
		{"a group at no path", group(layoutEngine{"merge"}), "", false},
		{"a group whose elected engine offers no assembly", group(flatEngineOf("merge")), "out/edge/compose", false},
		{"a group the record has no entry for", nil, "out/edge/compose", false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := dispatchKey{Path: tc.path, Provider: "compose"}
			groups := map[dispatchKey]*pendingDispatch{}
			if tc.group != nil {
				groups[key] = tc.group
			}
			attachment := boundaryFRKey{BoundaryPath: "edge", FRInstance: "gw"}
			record := newBoundaryAttachmentRoutings(
				map[boundaryFRKey]boundaryAttachmentRoute{attachment: {group: key, fragments: true}}, groups,
			)
			got, ok := (&RenderResult{attachmentRoutings: record}).boundaryAttachmentOutputRouting("edge", "gw")
			if !ok {
				t.Fatalf("the record holds no entry for the routed attachment")
			}
			want := constraint.AttachmentOutputRouting{
				AssemblyPath: tc.path, Provider: "compose", Fragments: true, GroupAssembles: tc.assembles,
			}
			if got != want {
				t.Errorf("the record holds %+v; want %+v", got, want)
			}
			if tc.group != nil && got.GroupAssembles != groupAssemblesAt(tc.group, tc.path) {
				t.Errorf("the record says the group assembles: %v, and the finalization branch says %v",
					got.GroupAssembles, groupAssemblesAt(tc.group, tc.path))
			}
		})
	}
}

// TestTheRoutedAttachmentAxisFollowsTheRelease holds the routing order and the
// axis the rule is invoked over to the order the graph carries the boundaries and
// each boundary's attachments — so the findings of a build with several offenders
// come out in an order that is a function of the input — narrowed to what the
// render produced output for and the routing placed, and positions every boundary
// coordinate at the Release. A tuple the render produced output for that no
// boundary declares is not visited at all.
func TestTheRoutedAttachmentAxisFollowsTheRelease(t *testing.T) {
	t.Parallel()

	declared := declaredBoundaryFeatures(
		[]string{"zone", "zeta", "alpha", "unrouted"},
		[]string{"apex", "only"},
		[]string{"quiet", "nothing"},
	)
	rendered := map[boundaryFRKey][]EmittedFile{
		{BoundaryPath: "zone", FRInstance: "zeta"}:     nil,
		{BoundaryPath: "zone", FRInstance: "alpha"}:    nil,
		{BoundaryPath: "apex", FRInstance: "only"}:     nil,
		{BoundaryPath: "quiet", FRInstance: "nothing"}: nil,
		{BoundaryPath: "ghost", FRInstance: "unseen"}:  nil,
	}
	order := boundaryFRRoutingOrder(declared, rendered)
	if want := []boundaryFRKey{
		{BoundaryPath: "zone", FRInstance: "zeta"},
		{BoundaryPath: "zone", FRInstance: "alpha"},
		{BoundaryPath: "apex", FRInstance: "only"},
		{BoundaryPath: "quiet", FRInstance: "nothing"},
	}; !reflect.DeepEqual(order, want) {
		t.Errorf("the routing visits %v; want the declared order %v", order, want)
	}
	// The routing placed all but "quiet/nothing", so the axis carries the rest.
	routed := map[boundaryFRKey]boundaryAttachmentRoute{
		{BoundaryPath: "zone", FRInstance: "zeta"}:  {},
		{BoundaryPath: "zone", FRInstance: "alpha"}: {},
		{BoundaryPath: "apex", FRInstance: "only"}:  {},
	}
	axes := newRoutedBoundaryAttachments("release.yaml", order, routed)

	var boundaries []string
	for _, b := range axes.Boundaries() {
		boundaries = append(boundaries, b.Value)
		if b.SourceFile != "release.yaml" {
			t.Errorf("boundary %s is positioned in %q; want the Release", b.Value, b.SourceFile)
		}
	}
	if want := []string{"zone", "apex"}; !reflect.DeepEqual(boundaries, want) {
		t.Errorf("the axis carries boundaries %v; want %v", boundaries, want)
	}
	if got, want := axes.BoundaryAttachments("zone"), []string{"zeta", "alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the axis carries %v at zone; want %v", got, want)
	}
	if got := axes.BoundaryAttachments("quiet"); len(got) != 0 {
		t.Errorf("the axis carries %v at a boundary that routed nothing; want none", got)
	}
}
