// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// This file invokes the rules declared over what each render emitted, one entry per
// emission carrier.
//
// # Why three entries and not one
//
// One rule runs over three separately authored documents: a component's own Recipe,
// the FeatureRecipe an attachment binds on a component, and the FeatureRecipe a
// boundary-scope attachment binds. Each emits under its own set of names, and the
// coordinate that identifies one of those names differs per carrier — so the rule is
// three declared values over one shared predicate, each keyed by its own carrier,
// and each invoked where its own render's emission completes.
//
// Folding all three under the node would not merely mis-file two of them. An
// attachment's render emits under the HOST component's node identity, so a
// node-keyed reading would answer about the host's own emission while the blocks in
// question are the attachment's — refusing none of the attachment's collisions and
// refusing the host's more than once. Both directions are wrong and both are silent.
//
// # Why the two attachment carriers are named in one function
//
// Each could have invoked its own value through the carrier interface, and that
// would read as the tidier arrangement. It is the wrong one, for the reason the
// stage before this states at its own pair: the framework's coverage read resolves a
// call to a declaration and follows nothing through a value, so a traversal reached
// through an interface method belongs to no calling surface. The placement table
// would then report both values as invoked and reachable from nowhere, with nothing
// saying which two of the invoked set were missing.

// oneEmittingNode is the node axis and the emitted-name axis for the rule asked
// about one node's own emission, narrowed to the node whose emission has just been
// recorded.
//
// # Why the node axis is narrowed rather than swept
//
// The traversal walks every node its node axis yields, and the retention fills as the
// node walk proceeds. Handed the build's own axis it would, at the k-th node,
// re-present the k-1 nodes already recorded — so a build of n nodes would raise the
// same finding once for every later node in the walk, and an author would read one
// authoring fault repeated a number of times that depends on how many components the
// application happens to declare.
//
// # Why the name axis is delegated rather than narrowed with it
//
// The names of the narrowed node are exactly the subjects wanted, and the build's own
// axis already yields them for that node — from the same retention the fact is
// answered from. Narrowing it here as well would mean deciding at this site which
// names the node emitted, which is a second answer to a question the retention
// already answers, and the two would be free to disagree about a name the
// deduplication folded. Delegating keeps one reader; the node narrowing is what stops
// the delegated axis being asked about any other node.
//
// # Why the node arrives whole
//
// The locator a finding is positioned at is the component instance, assembled from
// the component's authored name and the subsystem path it was reached through.
// Neither is derivable from the node identity alone and neither is this site's to
// decide, so the instance is taken from the enumeration the compilation produced.
type oneEmittingNode struct {
	// node is the instance whose emission has just been recorded, as the build's
	// own enumeration describes it.
	node constraint.NodeInstance

	// paths is the build's own per-node emitted-name axis, delegated to for the
	// narrowed node and never consulted for any other.
	paths constraint.NodeEmittedPathEnumerator
}

// Nodes yields the one node whose emission has just been recorded.
func (o oneEmittingNode) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{o.node}
}

// NodeEmittedPaths yields the narrowed node's own emitted names and nothing for any
// other node, so a traversal handed a wider node axis still reaches exactly these
// subjects.
func (o oneEmittingNode) NodeEmittedPaths(node string) []string {
	if node != o.node.ID || o.paths == nil {
		return nil
	}
	return o.paths.NodeEmittedPaths(node)
}

var (
	_ constraint.NodeEnumerator            = oneEmittingNode{}
	_ constraint.NodeEmittedPathEnumerator = oneEmittingNode{}
)

// runNodeEmissionCollisionRule asks the rule declared over one node's own emission
// about each output name that node emitted, and returns what it raised as the
// render's halt.
//
// It is called immediately after the node's emission is recorded, which is the point
// the set is both complete and still the node's own: every pass that can contribute a
// file has run and an own-scope pipeline has appended whatever it emitted, and one
// statement later the node joins its dispatch group, where its files are split by the
// intermediate flag and merged with what other renders produced.
//
// # What the position costs, stated because it is a real change
//
// The hand-written detection this replaces ran at two points of the node walk — once
// after workload synthesis and again after the own-scope pipeline. This runs once,
// at the later of the two moments. The set of inputs that halt is unchanged, because
// the own-scope pipeline cannot remove a file or change one's name or origin: it
// rebuilds every input file and appends what a step emitted, so a collision present
// before it is present after it. What moves is where the build stops — an input
// colliding at workload synthesis now runs the own pipeline first — and that work is
// template evaluation over documents already in hand, discarded on the halt either
// way.
//
// # The two absent-presentation conditions are DIFFERENT questions
//
// This rule HALTS, so it cannot treat an absent presentation as an ordinary outcome
// the way an advisory can. The presentation it reads extends one the render's CALLER
// supplies, and the chain is cumulative — so the two cases are separated rather than
// collapsed.
//
// A caller that supplied NO presentation gets no reading. That caller assembled a
// render's input directly, which is asking for a render rather than for a build, and
// a rule invoked over facts nothing produced would be worse than one not invoked.
//
// 🔴 That is a real bound and it is stated rather than implied: such a caller gets no
// collision reading, so a node whose blocks met on an output name renders for it
// instead of halting. Every build path supplies the presentation, so the bound does
// not reach a build; it reaches exactly the callers that assemble a render's input
// themselves. Refusing those instead would refuse renders that succeed today.
//
// A presentation absent BENEATH a present caller presentation is not an outcome, it
// is a chain that broke inside this package: the retention, the presentation and the
// enumeration are installed together, unconditionally, at one site. It halts rather
// than returning nothing, because the two are indistinguishable to any reader
// downstream and silence is indistinguishable from approval — and for a rule that
// refuses input today, passing it over would turn a build that halts into one that
// renders. Its wording says the fault is the compiler's rather than the author's,
// because the halt carries a catalog category and no category attributes a fault to
// the compiler reaching a check without its inputs.
//
// 🔴 The REACH of this rule does not depend on the build's node enumeration, and
// that is a deliberate difference from the sibling readings at the stage before
// this. Those rules are new behaviour, so a node they are not asked about loses
// nothing that ran before, and they take the node from the enumeration or are not
// invoked. This one replaces a detection that ran UNCONDITIONALLY on every node with
// no enumeration involved, so a node it silently skipped would be a build that halts
// today and renders after — the one direction this migration must not have.
//
// So the caller passes the node the walk is on whether or not the enumeration
// describes it, and what the enumeration contributes is the LOCATOR rather than the
// invocation: with it, a finding is positioned at the component instance the author
// wrote; without it, the finding still names the node and the blocks that met, and
// carries no position. A finding a reader cannot navigate to is worth strictly more
// than no finding, and the detail carries what the position would have shown — which
// is the same division this project's provenance rule already draws for a surface
// that cannot position a finding.
func runNodeEmissionCollisionRule(
	cancelled constraint.Cancellation,
	callerPresentationSupplied bool,
	view constraint.EmittedView,
	paths constraint.NodeEmittedPathEnumerator,
	node constraint.NodeInstance,
) error {
	if node.ID == "" {
		return nil
	}
	if view == nil || paths == nil {
		if !callerPresentationSupplied {
			return nil
		}
		return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: node.ID},
			"the files this node's own render emitted could not be checked for two of its blocks "+
				"meeting on one output name — internal error: the check's inputs were incomplete. "+
				"This is a fault in the compiler rather than in the catalog, so there is nothing to "+
				"correct in the Recipe", nil, nil)
	}
	subject := oneEmittingNode{node: node, paths: paths}
	raised, err := constraint.RunOnNodeEmittedPaths(
		cancelled, subject, subject, view, EmittedNameProducedByOneBlock,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, "output names produced by more than one input-kind block")
}

// oneEmittingNodeAttachment is the node, attachment and emitted-name axes for the
// rule asked about the emission of the render one attachment carries on a node,
// narrowed to that carrier.
//
// It narrows for the reasons [oneEmittingNode] gives, and it narrows the attachment
// axis as well because the carrier is the subject: two attachments on one node are
// independently authored, and a traversal handed the node's whole attachment set
// would ask this render's names of every attachment the node carries.
//
// The attachment coordinate is taken from the carrier rather than from anything the
// render can see, because the render cannot see it: the node identity an
// attachment's render carries is the host component's.
type oneEmittingNodeAttachment struct {
	// key is the carrier whose emission has just been recorded: this node, this
	// attachment.
	key nodeAttachmentKey

	// paths is the build's own per-carrier emitted-name axis, delegated to for the
	// narrowed carrier and never consulted for any other.
	paths constraint.NodeAttachmentEmittedPathEnumerator
}

// Nodes yields the one node carrying the attachment.
//
// Only the node's identity is supplied. On this axis that is the whole of what the
// node contributes: the file a finding is positioned in comes from the emitted-name
// coordinate, which carries the FeatureRecipe, so supplying the node's other
// coordinates from what this site happens to hold would position a finding from
// values the axis never reads.
func (o oneEmittingNodeAttachment) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: o.key.node}}
}

// Attachments yields the one attachment the emission belongs to, and nothing for any
// other node.
func (o oneEmittingNodeAttachment) Attachments(node string) []string {
	if node != o.key.node {
		return nil
	}
	return []string{o.key.attachment}
}

// NodeAttachmentEmittedPaths yields the narrowed carrier's emitted names and nothing
// for any other carrier.
func (o oneEmittingNodeAttachment) NodeAttachmentEmittedPaths(
	node string, attachment string,
) []constraint.AuthoredCoordinate {
	if node != o.key.node || attachment != o.key.attachment || o.paths == nil {
		return nil
	}
	return o.paths.NodeAttachmentEmittedPaths(node, attachment)
}

var (
	_ constraint.NodeEnumerator                      = oneEmittingNodeAttachment{}
	_ constraint.AttachmentEnumerator                = oneEmittingNodeAttachment{}
	_ constraint.NodeAttachmentEmittedPathEnumerator = oneEmittingNodeAttachment{}
)

// oneEmittingBoundaryAttachment is the boundary, attachment and emitted-name axes for
// the rule asked about the emission of the render one boundary-scope attachment
// carries, narrowed to that carrier.
//
// It is rooted at the boundary because that is where such an attachment is declared,
// which is also why its fact is keyed there. The narrowing is the reason
// [oneEmittingNodeAttachment] gives.
type oneEmittingBoundaryAttachment struct {
	// key is the carrier whose emission has just been recorded: this boundary, this
	// attachment.
	key boundaryAttachmentKey

	// paths is the build's own per-carrier emitted-name axis.
	paths constraint.BoundaryAttachmentEmittedPathEnumerator
}

// Boundaries yields the one boundary the attachment was declared on.
//
// It carries the scope path and no file, because on this axis the boundary
// contributes no locator segment: the locator names the FeatureRecipe, which is
// neither the document the attachment was declared in nor any component's Recipe. The
// file comes from the emitted-name coordinate.
func (o oneEmittingBoundaryAttachment) Boundaries() []constraint.AuthoredCoordinate {
	return []constraint.AuthoredCoordinate{{Value: o.key.boundary}}
}

// BoundaryAttachments yields the one attachment the emission belongs to, and nothing
// for any other boundary.
func (o oneEmittingBoundaryAttachment) BoundaryAttachments(boundary string) []string {
	if boundary != o.key.boundary {
		return nil
	}
	return []string{o.key.attachment}
}

// BoundaryAttachmentEmittedPaths yields the narrowed carrier's emitted names and
// nothing for any other carrier.
func (o oneEmittingBoundaryAttachment) BoundaryAttachmentEmittedPaths(
	boundary string, attachment string,
) []constraint.AuthoredCoordinate {
	if boundary != o.key.boundary || attachment != o.key.attachment || o.paths == nil {
		return nil
	}
	return o.paths.BoundaryAttachmentEmittedPaths(boundary, attachment)
}

var (
	_ constraint.BoundaryEnumerator                      = oneEmittingBoundaryAttachment{}
	_ constraint.BoundaryAttachmentEnumerator            = oneEmittingBoundaryAttachment{}
	_ constraint.BoundaryAttachmentEmittedPathEnumerator = oneEmittingBoundaryAttachment{}
)

// runAttachmentEmissionCollisionRule asks the value declared over whichever carrier
// this render was reached from about each output name that render emitted, and
// returns what it raised as the render's halt.
//
// It is called where the render's own emission is complete and before its caller has
// done anything with the result: the per-component caller composes the attachment's
// output prefix onto every name, and the boundary-scope caller routes them by the
// intermediate flag. Either would put the set beyond the point at which it is this
// attachment's own emission under the names its own blocks chose.
//
// The carrier is supplied on the state rather than derived here, because it is not
// derivable here — the node identity an attachment's render carries is the host
// component's on one carrier and a composite dispatch marker on the other, and
// neither names the attachment.
//
// # The input guard, and the arm that cannot be reached today
//
// The presentation is the carrier's own and so is the retention beneath it; the
// carrier refuses a presentation standing over no retention, because such a
// presentation would answer every name with no blocks and the rule would admit every
// emission it was asked about. A carrier reporting neither key is refused here rather
// than passing as an emission with nothing to check: today the two carriers exhaust
// the interface, and the halt is what keeps that an enforced guarantee rather than an
// arrangement a third carrier could quietly undo.
//
// Its wording says the fault is the compiler's rather than the author's, and that
// there is nothing to correct in the catalog. That is deliberate: the halt carries a
// catalog category because every category attributes a fault to a document or a
// producer and none of them says the compiler reached a check without its inputs — so
// the wording carries the attribution the category cannot, and an author meeting it is
// not sent looking through a FeatureRecipe that is not at fault.
func runAttachmentEmissionCollisionRule(
	cancelled constraint.Cancellation,
	state *PhaseBState,
) error {
	if state == nil || state.attachmentHydrated == nil {
		return nil
	}
	carrier := state.attachmentHydrated
	findingFile := artifactFindingFile(state)
	facts, err := carrier.readableEmittedFacts(findingFile)
	if err != nil || facts == nil {
		return err
	}
	axes := carrier.emittedAxes()
	if axes == nil {
		return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			"the files this FeatureRecipe emitted could not be checked for two of its blocks "+
				"meeting on one output name — internal error: the check's subject enumeration was "+
				"absent. This is a fault in the compiler rather than in the FeatureRecipe, so there "+
				"is nothing to correct in the catalog", nil, nil)
	}

	if key, rooted := carrier.nodeAttachmentCarrierKey(); rooted {
		subject := oneEmittingNodeAttachment{key: key, paths: axes}
		raised, err := constraint.RunOnNodeAttachmentEmittedPaths(
			cancelled, subject, subject, subject, facts,
			AttachmentEmittedNameProducedByOneBlock,
		)
		if err != nil {
			return err
		}
		return attachmentEmissionCollisionHalt(raised, key.attachment)
	}

	if key, rooted := carrier.boundaryAttachmentCarrierKey(); rooted {
		subject := oneEmittingBoundaryAttachment{key: key, paths: axes}
		raised, err := constraint.RunOnBoundaryAttachmentEmittedPaths(
			cancelled, subject, subject, subject, facts,
			BoundaryAttachmentEmittedNameProducedByOneBlock,
		)
		if err != nil {
			return err
		}
		return attachmentEmissionCollisionHalt(raised, key.attachment)
	}

	return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
		"the files this FeatureRecipe emitted could not be checked for two of its blocks meeting "+
			"on one output name — internal error: the render carries no identity to check it under. "+
			"This is a fault in the compiler rather than in the FeatureRecipe, so there is nothing "+
			"to correct in the catalog", nil, nil)
}

// attachmentEmissionCollisionHalt is the halt both carriers' findings are returned as,
// and it names the FeatureRecipe rather than the Recipe.
//
// One home for the wording because the two carriers give an author the same thing to
// do — two blocks of one FeatureRecipe met on an output name either way, and which
// carrier reached it is not a difference the author acts on. The attachment is named
// because it is what says which FeatureRecipe, and a node or a boundary may carry
// several.
//
// It is deliberately worded apart from the node's own halt beside it. The two refuse
// the same shape of authoring fault in different documents, and a reader — or a proof
// — has to be able to say which document the finding is about.
func attachmentEmissionCollisionHalt(
	raised []constraint.Objection, attachment string,
) error {
	return objectionsAsHalt(raised, fmt.Sprintf(
		"output names produced by more than one input-kind block of the FeatureRecipe attachment %q binds",
		attachment))
}
