// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// oneRecordedArtifact is the node axis and the artifact axis for a rule asked
// about a single artifact entry, narrowed to exactly the entry whose text the
// walk has just recorded.
//
// # Why the subject set is narrowed to one rather than swept
//
// The traversal it is handed to walks every node an axis yields and asks the
// artifact axis for each. Handed the build's own axes it would, at the k-th entry
// of a node, re-present the k-1 entries already recorded — so a node projecting n
// artifacts would be asked about n(n+1)/2 subjects and would raise the same
// finding once for every later entry in the same Recipe. An author would read one
// authoring fault repeated a number of times that depends on where in the block
// it was written.
//
// # Why one value serves both axes
//
// The pair is the subject: this node, this entry. Two values could be narrowed to
// two different entries and nothing would compare them, and the failure would be
// a rule invoked about an entry other than the one just recorded — which reads
// exactly like a rule that ran and approved. One value cannot be mispaired.
//
// # Why the output name is taken from the walk rather than read back
//
// It is the same variable the recording call on the line above was given, so the
// name a rule is invoked over and the name its facts are keyed by are one value
// rather than two readings that agree today. Reading the recorded key set back
// and filtering it would look more careful and would be weaker: it would
// introduce a case where the filter finds nothing and the rule is silently
// skipped, which is the one outcome indistinguishable from approval.
//
// # What the node coordinate carries, and what it deliberately does not
//
// Only the node's identity is supplied. On this axis that is the whole of what a
// node contributes: the traversal keys every fact by the node id, takes the file
// a finding is positioned in from the artifact coordinate rather than from the
// node, and the rule declared over this kind states that the node is deliberately
// absent from its detail because the fault is in the Recipe's text rather than in
// any one node that binds it. Supplying the node's other coordinates from what
// this site happens to hold would position a finding from values the axis never
// reads, which is how a locator comes to point somewhere plausible and wrong —
// and an absent label leaves a finding unpositioned rather than mispositioned.
type oneRecordedArtifact struct {
	// nodeID is the node whose Recipe projected the entry.
	nodeID string

	// outputName is the name the entry resolved to, which is the coordinate every
	// artifact fact is keyed by. It is the authored key itself for an inline or an
	// imported entry, and for a bundle-supplied one the key with the bundle's own
	// filename nested under it, where the key alone names no output.
	outputName string

	// sourceFile positions a finding at the document the entry was written in. It
	// labels rather than answers: no rule reads it.
	sourceFile string
}

// Nodes yields the one node the entry belongs to.
func (o oneRecordedArtifact) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: o.nodeID}}
}

// Artifacts yields the one recorded entry, and nothing for any other node — so a
// traversal handed a wider node axis still reaches exactly this subject.
func (o oneRecordedArtifact) Artifacts(node string) []constraint.AuthoredCoordinate {
	if node != o.nodeID {
		return nil
	}
	return []constraint.AuthoredCoordinate{{Value: o.outputName, SourceFile: o.sourceFile}}
}

var (
	_ constraint.NodeEnumerator     = oneRecordedArtifact{}
	_ constraint.ArtifactEnumerator = oneRecordedArtifact{}
)

// runArtifactPillarRule asks the rule declared over this stage's facts about one
// artifact entry, and returns what it raised as the render's halt.
//
// It is called from inside the artifacts walk, between the point the entry's text
// is recorded and the point that text is evaluated. That is the only place both
// halves of the question are answerable: the text exists from the line above, and
// evaluation has not yet turned an undeclared pillar into either a placeholder
// written into the output or a halt naming the first hop of the expression rather
// than the expression itself.
//
// # It is the only pillar reading on this carrier
//
// The pre-check standing beside it in the walk once held the same two conditions
// and no longer does, so a reference to a pillar the document does not declare is
// refused here or not at all. What that pre-check keeps is the parse condition,
// because a template that does not parse is not this rule's finding — evaluation
// reports the parse error against the same entry, so refusing it here as well
// would report one authoring fault twice.
//
// The walk this sits in is reached by two renders: a node's own Recipe, and the
// render an attachment's or a boundary-scope FeatureRecipe carries. Only the
// first contributes a subject HERE, because the facts this value reads are keyed
// by the node alone — a FeatureRecipe's entries flow through the same walk under
// the host component's node identity, so a fact keyed that way would answer about
// the host while reading the attachment's template. Those entries are reached
// instead by the two sibling values keyed by their own carrier, invoked beside
// this one at the same seam.
//
// So the three values between them cover every entry the walk projects, and they
// share one presentation: a render that published none skips all three, and no
// reading refuses a pillar reference in that build. That state is reachable only
// by assembling a render's input directly, which nothing outside this module can
// do, and the one assembler inside it publishes the presentation unconditionally.
//
// # The two nil conditions are different questions
//
// An absent presentation says this render is not the one THIS value's facts are
// declared over, which is the FeatureRecipe render and is an ordinary outcome:
// the caller skips this value, and the value declared over the carrier that
// render does hold is asked instead.
//
// An absent retention beneath a present presentation is not an outcome, it is a
// broken arrangement — the presentation would answer every entry's text with the
// empty string, the rule would collect no reference from it, and it would admit
// every template it was asked about. That is why it halts rather than returning
// nothing: the two are indistinguishable to any reader downstream, and silence is
// indistinguishable from approval. The condition is unreachable today because the
// retention and the presentation are installed together, unconditionally, at one
// site; the halt is what makes that an enforced guarantee rather than an
// arrangement a later change could quietly undo.
//
// Its wording says the fault is the compiler's rather than the author's, and that
// there is nothing to correct in the catalog. That is deliberate: the halt carries
// a catalog category because every category attributes a fault to a document or a
// producer and none of them says the compiler reached a check without its inputs —
// so the wording carries the attribution the category cannot, and an author meeting
// it is not sent looking through a Recipe that is not at fault.
func runArtifactPillarRule(
	cancelled constraint.Cancellation,
	state *PhaseBState,
	outputName string,
) error {
	if state == nil || state.hydratedFacts == nil {
		return nil
	}
	findingFile := artifactFindingFile(state)
	if state.hydrated == nil {
		return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			fmt.Sprintf("artifact %q could not be checked against the pillars its Recipe declares — "+
				"internal error: the check's inputs were incomplete. This is a fault in the compiler "+
				"rather than in the Recipe, so there is nothing to correct in the catalog", outputName), nil, nil)
	}

	subject := oneRecordedArtifact{
		nodeID:     state.NodeID,
		outputName: outputName,
		sourceFile: findingFile,
	}
	raised, err := constraint.RunOnNodeArtifacts(
		cancelled, subject, subject, state.hydratedFacts, ArtifactPillarDeclared,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, fmt.Sprintf(
		"artifact %q references pillars its Recipe does not declare", outputName))
}

// oneRecordedAttachmentArtifact is the node, attachment and artifact axes for a
// rule asked about one artifact entry the FeatureRecipe an attachment binds on a
// node projects, narrowed to exactly the entry whose text the walk has just
// recorded.
//
// It is the carrier-keyed form of [oneRecordedArtifact] and narrows for the same
// reasons, which apply unchanged: the retention fills as the walk proceeds, so a
// presentation asked with the build's own axes would re-present at the k-th entry
// the k-1 already recorded and would report one authoring fault once per later
// entry in the same block; and one value serving all three axes cannot be
// narrowed to a different entry on one axis than on another, which is a failure
// that reads exactly like a rule that ran and approved.
//
// The attachment coordinate is what tells this block from the node's own and from
// another attachment's on the same node. It is taken from the carrier the render's
// state holds rather than from anything the walk can see, because the walk cannot
// see it: the node identity an attachment's render carries is the host
// component's.
type oneRecordedAttachmentArtifact struct {
	// key is the carrier the entry belongs to: this node, this attachment.
	key nodeAttachmentKey

	// outputName is the name the entry resolved to, which is the coordinate every
	// artifact fact is keyed by. It is the authored key itself for an inline or an
	// imported entry, and for a bundle-supplied one the key with the bundle's own
	// filename nested under it, where the key alone names no output.
	outputName string

	// sourceFile positions a finding at the document the entry was written in,
	// which on this axis is the FeatureRecipe rather than the node's Recipe. It
	// labels rather than answers: no rule reads it.
	sourceFile string
}

// Nodes yields the one node carrying the attachment.
//
// Only the node's identity is supplied, on the reading [oneRecordedArtifact]
// states: the traversal keys every fact by the node id and takes the file a
// finding is positioned in from the artifact coordinate, so supplying a node's
// other coordinates from what this site happens to hold would position a finding
// from values the axis never reads.
func (o oneRecordedAttachmentArtifact) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: o.key.node}}
}

// Attachments yields the one attachment the entry belongs to, and nothing for any
// other node — so a traversal handed a wider node axis still reaches exactly this
// subject.
func (o oneRecordedAttachmentArtifact) Attachments(node string) []string {
	if node != o.key.node {
		return nil
	}
	return []string{o.key.attachment}
}

// AttachmentArtifacts yields the one recorded entry, and nothing for any other
// carrier.
func (o oneRecordedAttachmentArtifact) AttachmentArtifacts(
	node string, attachment string,
) []constraint.AuthoredCoordinate {
	if node != o.key.node || attachment != o.key.attachment {
		return nil
	}
	return []constraint.AuthoredCoordinate{{Value: o.outputName, SourceFile: o.sourceFile}}
}

var (
	_ constraint.NodeEnumerator               = oneRecordedAttachmentArtifact{}
	_ constraint.AttachmentEnumerator         = oneRecordedAttachmentArtifact{}
	_ constraint.AttachmentArtifactEnumerator = oneRecordedAttachmentArtifact{}
)

// oneRecordedBoundaryAttachmentArtifact is the boundary, attachment and artifact
// axes for a rule asked about one artifact entry the FeatureRecipe a
// boundary-scope attachment binds projects, narrowed to exactly the entry whose
// text the walk has just recorded.
//
// It is rooted at the boundary because that is where such an attachment is
// declared, which is also why its facts are keyed there. The narrowing is the
// reason [oneRecordedAttachmentArtifact] gives.
type oneRecordedBoundaryAttachmentArtifact struct {
	// key is the carrier the entry belongs to: this boundary, this attachment.
	key boundaryAttachmentKey

	// outputName is the name the entry resolved to.
	outputName string

	// sourceFile positions a finding at the FeatureRecipe the entry was written
	// in. It labels rather than answers.
	sourceFile string
}

// Boundaries yields the one boundary the attachment was declared on.
//
// It carries the scope path and no file, because on this axis the boundary
// contributes no locator segment: the locator names the artifacts block inside
// the FeatureRecipe, which is neither the document the attachment was declared in
// nor any component's Recipe. The file comes from the artifact coordinate.
func (o oneRecordedBoundaryAttachmentArtifact) Boundaries() []constraint.AuthoredCoordinate {
	return []constraint.AuthoredCoordinate{{Value: o.key.boundary}}
}

// BoundaryAttachments yields the one attachment the entry belongs to, and nothing
// for any other boundary.
func (o oneRecordedBoundaryAttachmentArtifact) BoundaryAttachments(boundary string) []string {
	if boundary != o.key.boundary {
		return nil
	}
	return []string{o.key.attachment}
}

// BoundaryAttachmentArtifacts yields the one recorded entry, and nothing for any
// other carrier.
func (o oneRecordedBoundaryAttachmentArtifact) BoundaryAttachmentArtifacts(
	boundary string, attachment string,
) []constraint.AuthoredCoordinate {
	if boundary != o.key.boundary || attachment != o.key.attachment {
		return nil
	}
	return []constraint.AuthoredCoordinate{{Value: o.outputName, SourceFile: o.sourceFile}}
}

var (
	_ constraint.BoundaryEnumerator                   = oneRecordedBoundaryAttachmentArtifact{}
	_ constraint.BoundaryAttachmentEnumerator         = oneRecordedBoundaryAttachmentArtifact{}
	_ constraint.BoundaryAttachmentArtifactEnumerator = oneRecordedBoundaryAttachmentArtifact{}
)

// runAttachmentArtifactPillarRule asks the value declared over whichever carrier
// this render was reached from about one artifact entry, and returns what it raised
// as the render's halt.
//
// It is called from the same seam inside the artifacts walk the node's own value is
// called from, and for the same reason: that is the only span where the entry's text
// exists and nothing has evaluated it yet. The carrier is supplied on the state
// rather than derived here, because it is not derivable here — the node identity an
// attachment's render carries is the host component's on one carrier and a composite
// dispatch marker on the other, and neither names the attachment.
//
// # Why both traversals are named in one function
//
// Each carrier could have invoked its own value through the carrier interface, and
// that would read as the tidier arrangement. It is the wrong one, because of what
// the framework's own coverage read can see: that read resolves a call to a
// declaration and follows nothing through a value, so a traversal reached through an
// interface method belongs to no calling surface. The placement table would then
// report both of these as invoked and reachable from nowhere, with nothing saying
// which two of the invoked set were missing — an instrument whose subject is where a
// rule is enforced, made blind to the rule this widened.
//
// Naming both here also puts them where a reader compares them, which is the shape
// the stage-wide rule orchestration beside this package uses for the same reason.
//
// # The input guard, and the arm that cannot be reached today
//
// The presentation is the carrier's own and so is the retention beneath it; the
// carrier refuses a presentation standing over no retention, on the reading the
// node's own value states about its own pair. A carrier reporting neither key is
// refused here rather than passing as an entry with nothing to check: today the two
// carriers exhaust the interface, and the halt is what keeps that an enforced
// guarantee rather than an arrangement a third carrier could quietly undo.
func runAttachmentArtifactPillarRule(
	cancelled constraint.Cancellation,
	state *PhaseBState,
	outputName string,
) error {
	if state == nil || state.attachmentHydrated == nil {
		return nil
	}
	carrier := state.attachmentHydrated
	findingFile := artifactFindingFile(state)
	facts, err := carrier.readableFacts(findingFile, outputName)
	if err != nil || facts == nil {
		return err
	}

	if key, rooted := carrier.nodeAttachmentCarrierKey(); rooted {
		subject := oneRecordedAttachmentArtifact{
			key:        key,
			outputName: outputName,
			sourceFile: findingFile,
		}
		raised, err := constraint.RunOnNodeAttachmentArtifacts(
			cancelled, subject, subject, subject, facts, AttachmentArtifactPillarDeclared,
		)
		if err != nil {
			return err
		}
		return attachmentArtifactPillarHalt(raised, key.attachment, outputName)
	}

	if key, rooted := carrier.boundaryAttachmentCarrierKey(); rooted {
		subject := oneRecordedBoundaryAttachmentArtifact{
			key:        key,
			outputName: outputName,
			sourceFile: findingFile,
		}
		raised, err := constraint.RunOnBoundaryAttachmentArtifacts(
			cancelled, subject, subject, subject, facts, BoundaryAttachmentArtifactPillarDeclared,
		)
		if err != nil {
			return err
		}
		return attachmentArtifactPillarHalt(raised, key.attachment, outputName)
	}

	return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
		fmt.Sprintf("artifact %q could not be checked against the pillars the FeatureRecipe "+
			"declares — internal error: the render carries no identity to check it under. This is a "+
			"fault in the compiler rather than in the FeatureRecipe, so there is nothing to correct "+
			"in the catalog", outputName), nil, nil)
}

// attachmentArtifactPillarHalt is the halt both carriers' findings are returned
// as, and it names the FeatureRecipe rather than the Recipe.
//
// One home for the wording because the two carriers give an author the same thing
// to do — the entry is in a FeatureRecipe's artifacts block either way, and which
// carrier reached it is not a difference the author acts on. The attachment is
// named because it is what says which FeatureRecipe, and a node or a boundary may
// carry several.
//
// It is deliberately worded apart from the node's own halt beside it. The two
// refuse the same shape of authoring fault in different documents, and a reader —
// or a proof — has to be able to say which document the finding is about.
func attachmentArtifactPillarHalt(
	raised []constraint.Objection, attachment, outputName string,
) error {
	return objectionsAsHalt(raised, fmt.Sprintf(
		"artifact %q references pillars the FeatureRecipe attachment %q binds does not declare",
		outputName, attachment))
}

// runArtifactContentPinRule asks the pin rule declared over this stage's facts
// about one artifact entry, and returns what it raised as the render's halt.
//
// It sits at the same seam in the artifacts walk the pillar rule does and is called
// ahead of it, because content that has drifted from its recorded pin is content
// nothing verified: an entry is refused before anything reads it for any other
// purpose, so a build meeting both faults reports the drift rather than a pillar
// reference inside unverified bytes.
//
// Both halves of the question are answerable only in that span. The text is
// recorded on the line above, after the walk resolved whichever form the entry
// took, and the pin is recorded beside it for the one form that carries one; before
// the recording neither exists, and after evaluation the text has been replaced by
// what evaluating it produced, which no recorded pin describes.
//
// The two nil conditions differ exactly as they do for the pillar rule: an absent
// presentation says this render is not the one these facts are declared over, which
// is the ordinary state of a FeatureRecipe's render, while an absent retention
// beneath a present presentation would leave the rule reading an empty text and no
// pin — admitting every entry it was asked about — so it halts rather than passing.
func runArtifactContentPinRule(
	cancelled constraint.Cancellation,
	state *PhaseBState,
	outputName string,
) error {
	if state == nil || state.hydratedFacts == nil {
		return nil
	}
	findingFile := artifactFindingFile(state)
	if state.hydrated == nil {
		return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			fmt.Sprintf("artifact %q could not be checked against the content digest its Recipe "+
				"recorded — internal error: the check's inputs were incomplete. This is a fault in "+
				"the compiler rather than in the Recipe, so there is nothing to correct in the "+
				"catalog", outputName), nil, nil)
	}

	subject := oneRecordedArtifact{
		nodeID:     state.NodeID,
		outputName: outputName,
		sourceFile: findingFile,
	}
	raised, err := constraint.RunOnNodeArtifacts(
		cancelled, subject, subject, state.hydratedFacts, ArtifactContentMatchesItsRecordedPin,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, fmt.Sprintf(
		"artifact %q content does not match the digest its Recipe recorded", outputName))
}

// runAttachmentArtifactContentPinRule asks the pin rule declared over whichever
// carrier this render was reached from about one artifact entry, and returns what it
// raised as the render's halt.
//
// Both traversals are named here for the reason the pillar rule's carrier entry
// gives: the framework's coverage read resolves a call to a declaration and follows
// nothing through a value, so a traversal reached through an interface method would
// belong to no calling surface and the placement table would report the rule as
// invoked and reachable from nowhere.
func runAttachmentArtifactContentPinRule(
	cancelled constraint.Cancellation,
	state *PhaseBState,
	outputName string,
) error {
	if state == nil || state.attachmentHydrated == nil {
		return nil
	}
	carrier := state.attachmentHydrated
	findingFile := artifactFindingFile(state)
	facts, err := carrier.readableFacts(findingFile, outputName)
	if err != nil || facts == nil {
		return err
	}

	if key, rooted := carrier.nodeAttachmentCarrierKey(); rooted {
		subject := oneRecordedAttachmentArtifact{
			key:        key,
			outputName: outputName,
			sourceFile: findingFile,
		}
		raised, err := constraint.RunOnNodeAttachmentArtifacts(
			cancelled, subject, subject, subject, facts,
			AttachmentArtifactContentMatchesItsRecordedPin,
		)
		if err != nil {
			return err
		}
		return attachmentArtifactContentPinHalt(raised, key.attachment, outputName)
	}

	if key, rooted := carrier.boundaryAttachmentCarrierKey(); rooted {
		subject := oneRecordedBoundaryAttachmentArtifact{
			key:        key,
			outputName: outputName,
			sourceFile: findingFile,
		}
		raised, err := constraint.RunOnBoundaryAttachmentArtifacts(
			cancelled, subject, subject, subject, facts,
			BoundaryAttachmentArtifactContentMatchesItsRecordedPin,
		)
		if err != nil {
			return err
		}
		return attachmentArtifactContentPinHalt(raised, key.attachment, outputName)
	}

	return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
		fmt.Sprintf("artifact %q could not be checked against the content digest the FeatureRecipe "+
			"recorded — internal error: the render carries no identity to check it under. This is a "+
			"fault in the compiler rather than in the FeatureRecipe, so there is nothing to correct "+
			"in the catalog", outputName), nil, nil)
}

// attachmentArtifactContentPinHalt is the halt both carriers' findings are returned
// as, and it names the FeatureRecipe rather than the Recipe.
//
// One home for the wording because the two carriers give an author the same thing to
// do — re-freeze the entry's pin if the change was intended, or restore the file —
// and which carrier reached it is not a difference the author acts on. The
// attachment is named because it is what says which FeatureRecipe.
func attachmentArtifactContentPinHalt(
	raised []constraint.Objection, attachment, outputName string,
) error {
	return objectionsAsHalt(raised, fmt.Sprintf(
		"artifact %q content does not match the digest the FeatureRecipe attachment %q binds recorded",
		outputName, attachment))
}

// oneNodesAttachments is the node axis and the attachment axis for a rule asked
// about the attachments of a single node, narrowed to the node whose injection
// pipe the walk has just filled.
//
// # Why the subject set is narrowed to one node rather than swept
//
// The traversal it is handed to walks every node its node axis yields. Handed the
// build's own axis it would, at the k-th node of the walk, re-present the k-1
// nodes already recorded — so a build of n nodes would ask about n(n+1)/2 subjects
// and would raise the same finding once for every later node in the walk. An
// author would read one authoring fault repeated a number of times that depends on
// how many components the application happens to declare.
//
// # Why the attachment axis is delegated rather than narrowed with it
//
// The attachments of the narrowed node are exactly the subjects wanted, and the
// build's own axis already yields them for that node. Narrowing it as well would
// mean deciding here which attachments exist, which is a second answer to a
// question the graph's own enumeration already answers — and the two would be free
// to disagree about an attachment the cascade contributed. Delegating keeps the
// attachment axis one answer, and the node narrowing is what stops the delegated
// axis being asked about any other node.
//
// # Why the node arrives whole
//
// The whole point of reaching through the build's own enumeration is the node's
// provenance: the locator a finding is positioned at is the attachment's entry
// inside the component's own block, and it is assembled from the component's
// authored name and the subsystem path it was reached through. Neither is
// derivable from the node identity alone, and neither is this site's to decide —
// so the instance is taken from the enumeration the compilation produced rather
// than rebuilt from what the render happens to hold.
type oneNodesAttachments struct {
	// node is the instance the walk has reached, as the build's own enumeration
	// describes it.
	node constraint.NodeInstance

	// attachments is the build's own per-node attachment axis, delegated to for
	// the narrowed node and never consulted for any other.
	attachments constraint.AttachmentEnumerator
}

// Nodes yields the one node the walk has reached.
func (o oneNodesAttachments) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{o.node}
}

// Attachments yields the narrowed node's own attachments and nothing for any
// other node, so a traversal handed a wider node axis still reaches exactly these
// subjects.
func (o oneNodesAttachments) Attachments(node string) []string {
	if node != o.node.ID || o.attachments == nil {
		return nil
	}
	return o.attachments.Attachments(node)
}

var (
	_ constraint.NodeEnumerator       = oneNodesAttachments{}
	_ constraint.AttachmentEnumerator = oneNodesAttachments{}
)

// runNodeInjectDeliveryRule asks the rule declared over one node's attributed
// injection deliveries about each attachment attached to that node, and returns
// what it raised as the render's halt.
//
// It is called from inside the node walk, immediately after the attachments have
// delivered into the node's pipe and before anything reads that pipe. That is the
// only place the question is answerable and the last place acting on it still
// saves work: the deliveries are recorded on the line above, and everything after
// this point renders against a pipe whose keys a duplicate has already made
// ambiguous.
//
// Placing it here rather than after the whole node walk is what keeps the build
// stopping where it stopped before the rule was declared. A reading gathered at
// the end of the walk would refuse the same inputs and would refuse them later,
// after every other node had rendered — so an author would wait for work whose
// output was always going to be discarded, and a node whose render fails for an
// unrelated reason would report that failure instead of this one.
//
// # The two absent inputs are different questions
//
// An absent presentation, an absent enumeration, or a node the enumeration does
// not describe all leave the rule uninvoked, and that is the honest outcome
// rather than a silent skip: the presentation and the enumeration are installed
// together, unconditionally, by the one assembler inside this package, and the
// enumeration describes every node the walk visits — so all three states are
// reachable only by assembling a render's input directly. A caller that does so is
// asking for a render rather than for a build, and a rule invoked over coordinates
// the facts were not keyed by, or positioned from an instance carrying no
// provenance, would be worse than one not invoked.
//
// The cancellation signal is the caller's own, so a walk over a large attachment
// set stops within one subject of the caller asking it to.
func runNodeInjectDeliveryRule(
	cancelled constraint.Cancellation,
	view constraint.HydratedView,
	coordinates constraint.AttachmentEnumerator,
	node constraint.NodeInstance,
) error {
	if view == nil || coordinates == nil || node.ID == "" {
		return nil
	}
	subject := oneNodesAttachments{node: node, attachments: coordinates}
	raised, err := constraint.RunOnNodeAttachments(
		cancelled, subject, subject, view, AttachmentInjectKeyNotDeliveredByAnotherSource,
	)
	if err != nil {
		return err
	}
	return objectionsAsDeveloperHalt(
		raised, "attachments delivering a key another inject source already delivered")
}

// runInjectMappingMechanismRule asks the rule declared over one node's recorded
// injectMapping pairings about each attachment attached to that node, and returns
// what it raised as the render's halt.
//
// It is called from inside the node walk, immediately after every attachment's
// injectMapping has been evaluated and its pairing recorded, and before anything
// downstream reads the rewritten pipe. That is the earliest point the whole
// question is answerable: the component's declarations are read once at the top
// of the phase, and the mechanisms an output names exist only once that output
// has rendered.
//
// # What moving the reading to here changes, and what it does not
//
// The reading it replaces ran inside the parse, so it stopped the phase at the
// FIRST attachment whose output left a declared mechanism unnamed and the
// attachments after it were never evaluated. Gathered here, every attachment's
// mapping is evaluated before the question is asked. The inputs that halt are
// unchanged — a build reaching this line with any pairing unmapped halts, and one
// with none renders — and every declared mechanism an attachment leaves unmapped
// is now reported rather than only the first.
//
// What does change is which fault an input carrying TWO faults reports. A
// FeatureRecipe later in the cascade whose locals or whose mapping template fails
// to evaluate now halts the phase before this line is reached, where the reading
// inside the parse would have refused the earlier attachment's unmapped mechanism
// first. Both are catalog faults in the same node's attachment set and both halt,
// so the build stops either way; the work between the two points is template
// evaluation over documents already in hand, with no guest crossing and no bytes,
// and it is discarded on either fault.
//
// Reaching it any later would be worse for the reason the sibling reading beside
// it gives: the pipe this phase rewrites is what every sub-pass below renders
// against, so a reading gathered after the node walk would refuse the same inputs
// after every other node had rendered.
//
// # The two absent inputs are different questions
//
// An absent presentation, an absent enumeration, or a node the enumeration does
// not describe all leave the rule uninvoked, which is the honest outcome rather
// than a silent skip: the presentation and the enumeration are installed together,
// unconditionally, by the one assembler inside this package, and the enumeration
// describes every node the walk visits — so all three states are reachable only by
// assembling a render's input directly. A rule invoked over coordinates the facts
// were not keyed by, or positioned from an instance carrying no provenance, would
// be worse than one not invoked.
//
// The cancellation signal is the caller's own, so a walk over a large attachment
// set stops within one subject of the caller asking it to.
func runInjectMappingMechanismRule(
	cancelled constraint.Cancellation,
	view constraint.HydratedView,
	coordinates constraint.AttachmentEnumerator,
	node constraint.NodeInstance,
) error {
	if view == nil || coordinates == nil || node.ID == "" {
		return nil
	}
	subject := oneNodesAttachments{node: node, attachments: coordinates}
	raised, err := constraint.RunOnNodeAttachments(
		cancelled, subject, subject, view, AttachmentInjectMappingCoversEveryAuthoredMechanism,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(
		raised, "attachments whose injectMapping leaves a declared inject mechanism unmapped")
}

// declaredProvidesContract is the node axis and the provides-key axis for the
// rule over a component type's output contract: every node whose contract the
// render read, and the keys that contract declares for each.
//
// # Why the axis is the declared contract rather than the published block
//
// The framework's own provides-key axis enumerates what a Recipe publishes,
// which is the axis every other reading over that coordinate wants. This rule
// wants the opposite side of the same contract: a key the Recipe never published
// is absent from the published enumeration by construction, so a rule invoked
// over it would be asked about nothing for exactly the inputs it exists to
// refuse — and a rule invoked zero times reports what a rule that ran and
// approved reports.
//
// # Both axes are read from the retention the render already built
//
// The node set is the key set of the retained contract map, and the keys are that
// map's own values. Neither is re-derived: the map is built one loop above, from
// the catalog the render holds, and it is the same value the consumer-context
// projection later narrows each target's bag with. So the keys a rule is invoked
// over and the keys a consumer is allowed to read are one reading rather than two
// that agree today.
//
// That is also what carries the two conditions under which no contract is
// enforced. A node with no bound Recipe and a node whose component type the
// catalog does not answer for are both absent from the map, and absent for
// reasons that are properties of the axis rather than verdicts of the rule: there
// is no realizer to hold to a contract in the first case, and no contract was
// read in the second. Absence of a declaration is not a declaration of nothing —
// which is the same reading the projection makes of the same absence.
//
// # The node order is the walk's, not the map's
//
// A map holds no order, so the node sequence is taken from the sorted identifier
// list the loop above walked rather than from the retention's keys. Findings then
// arrive in the order the build already reported them in, and they arrive in the
// same order on every run — which the deterministic-output contract requires of
// anything that reaches a diagnostic.
//
// # The position is the Recipe, and it comes from the render's own binding
//
// A finding is positioned at the provides block of the Recipe bound to the node,
// because that block is where the missing entry is added. The Recipe's name
// stands in for its path: the catalog loader does not retain the path on the
// parsed document, and a logical identifier a reader can act on is what the
// position field admits where no file applies. The binding is read from the one
// map the render resolved it into, so the document a finding names and the
// document the render rendered are one answer.
type declaredProvidesContract struct {
	// nodeOrder is the identifier sequence the contract loop walked, sorted. It
	// may name nodes the retention does not hold; those contribute no subject.
	nodeOrder []string

	// declared is the retained per-node contract: the keys a node's component
	// type declares as its output. Its key set is the node axis.
	declared NodeDeclaredProvides

	// recipes is the render's own per-node Recipe binding, read for the document
	// a finding is positioned at and for nothing else.
	recipes map[string]*types.Recipe
}

// Nodes yields every node whose contract the render read, in the walk's order.
func (d declaredProvidesContract) Nodes() []constraint.NodeInstance {
	out := make([]constraint.NodeInstance, 0, len(d.declared))
	for _, nodeID := range d.nodeOrder {
		if _, held := d.declared[nodeID]; !held {
			continue
		}
		out = append(out, constraint.NodeInstance{ID: nodeID})
	}
	return out
}

// ProvidesKeys yields the keys one node's contract declares, sorted, each
// positioned at the provides block of the Recipe bound to that node.
//
// Sorted because a declared contract is a resolved set holding no authoring
// order, so there is no order left to preserve and sorting is what makes the
// sequence the same on every run.
func (d declaredProvidesContract) ProvidesKeys(node string) []constraint.AuthoredCoordinate {
	contract, held := d.declared[node]
	if !held || len(contract) == 0 {
		return nil
	}
	recipe := d.recipes[node]
	if recipe == nil {
		return nil
	}
	keys := make([]string, 0, len(contract))
	for key := range contract {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]constraint.AuthoredCoordinate, 0, len(keys))
	for _, key := range keys {
		out = append(out, constraint.AuthoredCoordinate{Value: key, SourceFile: recipe.Metadata.Name})
	}
	return out
}

var (
	_ constraint.NodeEnumerator        = declaredProvidesContract{}
	_ constraint.ProvidesKeyEnumerator = declaredProvidesContract{}
)

// runProvidesContractRule asks the rule declared over what each node's Recipe
// resolved about every key that node's component type declares, and returns what
// it raised as the render's halt.
//
// It is called immediately after the contract retention is built and before
// anything downstream reads a resolved provides bag. That is where the question
// is answerable and the last place acting on it still saves work: the resolved
// bag exists from the pass above, and everything after this point renders against
// a bag a shortfall has already left a hole in — the consumer's reference to the
// missing key would deliver nothing and render an unset value with no diagnostic,
// which is the silence this halt exists to replace.
//
// Placing it here rather than after the node walk is what keeps the build
// stopping where it stopped before the rule was declared.
//
// # The two absent-presentation conditions are DIFFERENT questions
//
// This rule's presentation extends one the render's CALLER supplies, and the
// chain is cumulative — so an absent caller presentation leaves every later one
// unbuilt. That is an ordinary outcome for a caller asking for a render rather
// than for a build: it assembled a render's input directly, and a rule invoked
// over facts nothing produced would be worse than one not invoked. Such a caller
// leaves this reading uninvoked, which is what the sibling reading at this stage
// does with the same absence and for the same reason.
//
// 🔴 That is a real bound on this refusal and it is stated rather than implied:
// a caller supplying no graph presentation gets no producer-fulfilment reading,
// so a Recipe short of its Component's contract renders for that caller instead
// of halting. Every build path supplies the presentation, so the bound does not
// reach a build; it reaches exactly the callers that assemble a render's input
// themselves. Refusing those instead would refuse renders that succeed today,
// which is a wider change than this reading is.
//
// An absent presentation BENEATH a present caller presentation is not an outcome,
// it is a broken arrangement inside this package: the retention and the step
// descriptor memo are installed unconditionally at one site, so a chain that
// stops between the caller's presentation and this one means one of them went
// missing. It halts rather than returning nothing, because the two are
// indistinguishable to any reader downstream and silence is indistinguishable
// from approval. The wording says the fault is the compiler's rather than the
// author's, because every category attributes a fault to a document or a producer
// and none of them says a check was reached without its inputs — so the wording
// carries the attribution the category cannot, and an author meeting it is not
// sent looking through a catalog that is not at fault.
func runProvidesContractRule(
	cancelled constraint.Cancellation,
	callerPresentationSupplied bool,
	view constraint.HydratedView,
	axes declaredProvidesContract,
) error {
	if view == nil {
		if !callerPresentationSupplied {
			return nil
		}
		return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{},
			"the output contract each component type declares could not be checked against what its "+
				"Recipe publishes — internal error: the check's inputs were incomplete. This is a fault "+
				"in the compiler rather than in the catalog, so there is nothing to correct in the "+
				"Component or the Recipe", nil, nil)
	}
	raised, err := constraint.RunOnNodeProvidesKeys(
		cancelled, axes, axes, view, RecipePublishesEveryDeclaredProvidesKey,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, "Recipes short of the output contract their Component declares")
}

// objectionsAsDeveloperHalt renders what a rule about what an application's own
// author wrote raised as the render's halt, attributing the fault to the
// application document.
//
// It is the fourth sibling of [objectionsAsHalt] and shares the whole of its
// per-objection rendering; what it does not share is the category, which is the
// one thing the four of them genuinely differ on. A rule about what an entity
// declares sends an author to a catalog document, a rule about output the compiler
// produced to no document at all, a rule about where a boundary's output landed to
// the deployment document — and this one to the application, because an injection
// block and the attachment that duplicates its key are both written there and the
// remedy is to remove one of them.
func objectionsAsDeveloperHalt(objections []constraint.Objection, envelope string) error {
	return objectionsAsCategorisedHalt(objections, cerrors.DeveloperError, envelope)
}

// artifactFindingFile is the document an artifact finding is positioned at.
//
// It prefers the Recipe's own path and falls back to the Recipe's name. The path
// is what an author opens, so it wins wherever the render has one; today the
// catalog loader does not retain it on the parsed Recipe, and a finding positioned
// at nothing would leave an author with a reference and no document to look in.
// A name is a logical identifier a reader can still act on, which is what the
// position field admits where no file applies.
func artifactFindingFile(state *PhaseBState) string {
	if state == nil {
		return ""
	}
	if state.RecipePath != "" {
		return state.RecipePath
	}
	return state.RecipeName
}
