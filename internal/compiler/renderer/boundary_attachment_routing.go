// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// boundaryAttachmentRoute is where the render sorted one boundary-scope
// attachment's output: the dispatch group it joined, and whether any of it was
// pre-assembly fragments handed to that group's assembler.
//
// It is taken at the routing that sorts the output, from the values that routing
// acted on, so the record and the placement are one decision rather than two
// readings that agree.
type boundaryAttachmentRoute struct {
	group     dispatchKey
	fragments bool
}

// boundaryAttachmentRoutings is the render's retained record of where each
// boundary-scope attachment's output went and whether the group it joined is
// handed to an assembler.
//
// It is a retention rather than a read of the render's working state because the
// groups are loop-locals dropped when the render returns, while the presentation
// of the grouping outlives it. A reader asking whether an attachment's fragments
// reach a file has this and nothing else.
type boundaryAttachmentRoutings struct {
	// byAttachment is looked up by attachment and never walked, so it holds no
	// order to keep: every reader arrives with a boundary and an attachment in
	// hand, and the order a rule is asked in comes from the Release.
	byAttachment map[boundaryFRKey]constraint.AttachmentOutputRouting
}

// newBoundaryAttachmentRoutings records, for each attachment the render routed
// output for, the group it joined, whether it handed that group fragments, and
// whether that group is handed to an assembler.
//
// It is taken once every contribution has been sorted into its group, because
// whether a group assembles depends on every component that joined it: the
// election is a function of the group's candidates, which are complete only once
// the node walk has ended. Whether it assembles is read from [groupAssemblesAt],
// the one home of which branch a group is finalized by, so the rule reading this
// record and the finalization writing the group cannot disagree about a group's
// branch.
func newBoundaryAttachmentRoutings(
	routed map[boundaryFRKey]boundaryAttachmentRoute,
	groups map[dispatchKey]*pendingDispatch,
) *boundaryAttachmentRoutings {
	out := &boundaryAttachmentRoutings{
		byAttachment: make(map[boundaryFRKey]constraint.AttachmentOutputRouting, len(routed)),
	}
	for key, route := range routed {
		group := groups[route.group]
		out.byAttachment[key] = constraint.AttachmentOutputRouting{
			AssemblyPath:   route.group.Path,
			Provider:       route.group.Provider,
			Fragments:      route.fragments,
			GroupAssembles: group != nil && groupAssemblesAt(group, route.group.Path),
		}
	}
	return out
}

// boundaryAttachmentOutputRouting returns where the output of one attachment a
// boundary declares on itself was sent, and false where the render routed none.
//
// It is unexported for the reason the group's other readers are: its only reader
// is in this package — the presentation of what the grouping holds. A render that
// recorded nothing answers false, which is the same answer an attachment that
// routed nothing gets.
func (r *RenderResult) boundaryAttachmentOutputRouting(
	boundary, attachment string,
) (constraint.AttachmentOutputRouting, bool) {
	if r.attachmentRoutings == nil {
		return constraint.AttachmentOutputRouting{}, false
	}
	got, ok := r.attachmentRoutings.byAttachment[boundaryFRKey{BoundaryPath: boundary, FRInstance: attachment}]
	return got, ok
}

// boundaryFRRoutingOrder is the order the boundary-scope attachments' output is
// routed in: the order the Release declares them in — boundaries in the order the
// graph carries their attachments, each boundary's attachments in author order —
// narrowed to the tuples the render produced output for.
//
// It exists because the record of what each attachment rendered is keyed by its
// tuple and holds no order, while everything the routing appends to as it goes —
// the dispatch groups in the order each is first heard from, the boundaries and
// path declarations each group records — reads back in the order the routing
// visited. Visiting the record directly would make those orders a property of the
// run rather than of the input. The author order is the one the input states, so
// it is the one walked; a sorted walk would invent an order the author never wrote.
//
// A tuple the render produced output for but the graph declares no attachment for
// is not visited. The routing reads the attachment's declaration for every tuple
// it places and passes over one it cannot find, so such a tuple was never placed;
// walking the declarations leaves it out by construction rather than by a guard.
func boundaryFRRoutingOrder(
	declared types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]],
	rendered map[boundaryFRKey][]EmittedFile,
) []boundaryFRKey {
	var out []boundaryFRKey
	for boundary, feats := range declared.All() {
		for instance := range feats.All() {
			key := boundaryFRKey{BoundaryPath: boundary, FRInstance: instance}
			if _, ok := rendered[key]; ok {
				out = append(out, key)
			}
		}
	}
	return out
}

// routedBoundaryAttachments is the boundary axis and the boundary-attachment axis
// for the rule over where a boundary-scope attachment's output went, narrowed to
// the attachments the render routed output for.
//
// It is narrowed for the reason the axes over an attachment's environment
// override are: an attachment that routed nothing joined no group, so asking
// about it would be asking about nothing. The order is the routing's own, which is
// the Release's ([boundaryFRRoutingOrder]) — so the findings of a build with
// several offending attachments come out in an order that is a function of the
// input, and in the order their output was routed. A boundary
// coordinate carries the Release's file, because an attachment a boundary
// declares is written there and nowhere else; an empty file leaves a finding
// unpositioned rather than mispositioned.
type routedBoundaryAttachments struct {
	// boundaries are the boundaries holding a routed attachment, each once, in
	// the graph's order.
	boundaries []constraint.AuthoredCoordinate

	// attachments are the routed attachments at each boundary, in author order.
	attachments map[string][]string
}

// newRoutedBoundaryAttachments narrows the axes to the attachments routed holds,
// in the order the routing visited them.
func newRoutedBoundaryAttachments(
	releaseFile string,
	order []boundaryFRKey,
	routed map[boundaryFRKey]boundaryAttachmentRoute,
) routedBoundaryAttachments {
	o := routedBoundaryAttachments{attachments: map[string][]string{}}
	for _, key := range order {
		if _, ok := routed[key]; !ok {
			continue
		}
		if _, seen := o.attachments[key.BoundaryPath]; !seen {
			o.boundaries = append(o.boundaries, constraint.AuthoredCoordinate{Value: key.BoundaryPath, SourceFile: releaseFile})
		}
		o.attachments[key.BoundaryPath] = append(o.attachments[key.BoundaryPath], key.FRInstance)
	}
	return o
}

// Boundaries yields the boundaries holding a routed attachment.
func (o routedBoundaryAttachments) Boundaries() []constraint.AuthoredCoordinate {
	return o.boundaries
}

// BoundaryAttachments yields the routed attachments one boundary holds, and
// nothing for a boundary holding none.
func (o routedBoundaryAttachments) BoundaryAttachments(boundary string) []string {
	return o.attachments[boundary]
}

var (
	_ constraint.BoundaryEnumerator           = routedBoundaryAttachments{}
	_ constraint.BoundaryAttachmentEnumerator = routedBoundaryAttachments{}
)
