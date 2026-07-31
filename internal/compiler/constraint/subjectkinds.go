// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// One sealed subject type per derived subject kind.
//
// The kind set is derived rather than chosen — a kind exists if and only if some
// declared fact is keyed by it — and it is generated beside this file as
// [SubjectKinds]. Every entry there has a type here, and a test reads the
// generated set to say so: a kind that gained a type nowhere fails the build
// rather than being discovered by the first check that cannot declare itself.
//
// Each kind follows the same three-part shape:
//
//   - an exported interface embedding [Subject] and adding one unexported
//     marker method, which is what makes the kinds distinct at compile time;
//   - an unexported struct embedding provenance, which is why a subject cannot
//     be constructed outside this package;
//   - an unexported constructor taking the source file and the segments the
//     author typed, which is why locator assembly has one home.
//
// Coordinates nest; kinds do not. A kind keyed by (node, socket) is reached
// within a node, and the generated Coordinates field is where a traversal reads
// that nesting. The interface for it embeds Subject and NOT the node kind's
// interface — had it embedded the node kind, its concrete type would satisfy
// that kind too, and a check declaring "once per node" could be handed a socket
// subject. Kind distinctness is the seal the framework rests on, so the nesting
// stays in the data rather than in the type hierarchy.

// DocumentParameterSubject is the subject of a check invoked once per parameter
// the presented document's contract declares.
//
// It is a separate kind from the document itself because "once per parameter" is
// a different once: a check declaring it is invoked for each declared parameter
// name, and its subject can name the parameter's authored site, where a document
// subject names only the file.
type DocumentParameterSubject interface {
	Subject
	documentParameter()
}

type documentParameterSubject struct{ provenance }

func (documentParameterSubject) documentParameter() {}

func newDocumentParameterSubject(sourceFile string, segments ...string) DocumentParameterSubject {
	return documentParameterSubject{newProvenance(sourceFile, segments...)}
}

// DocumentAssertionSubject is the subject of a check invoked once per assertion
// an entity declares across its resolved inheritance chain.
type DocumentAssertionSubject interface {
	Subject
	documentAssertion()
}

type documentAssertionSubject struct{ provenance }

func (documentAssertionSubject) documentAssertion() {}

func newDocumentAssertionSubject(sourceFile string, segments ...string) DocumentAssertionSubject {
	return documentAssertionSubject{newProvenance(sourceFile, segments...)}
}

// NodeSubject is the subject of a check invoked once per node: one component
// instance in the flattened set, a sub-aggregate's nodes included.
//
// A component instance's provenance follows a rule of its own, because whether
// the instance has an authored site the dotted locator can name depends on how
// it was reached. See [newComponentNodeSubject].
type NodeSubject interface {
	Subject
	node()
}

type nodeSubject struct{ provenance }

func (nodeSubject) node() {}

func newNodeSubject(sourceFile string, segments ...string) NodeSubject {
	return nodeSubject{newProvenance(sourceFile, segments...)}
}

// NodeSocketSubject is the subject of a check invoked once per dependency
// socket of a node.
type NodeSocketSubject interface {
	Subject
	nodeSocket()
}

type nodeSocketSubject struct{ provenance }

func (nodeSocketSubject) nodeSocket() {}

func newNodeSocketSubject(sourceFile string, segments ...string) NodeSocketSubject {
	return nodeSocketSubject{newProvenance(sourceFile, segments...)}
}

// EdgeSubject is the subject of a check invoked once per edge: one wiring of a
// consumer's socket to a target.
type EdgeSubject interface {
	Subject
	edge()
}

type edgeSubject struct{ provenance }

func (edgeSubject) edge() {}

func newEdgeSubject(sourceFile string, segments ...string) EdgeSubject {
	return edgeSubject{newProvenance(sourceFile, segments...)}
}

// NodeAttachmentSubject is the subject of a check invoked once per attachment a
// node carries.
type NodeAttachmentSubject interface {
	Subject
	nodeAttachment()
}

type nodeAttachmentSubject struct{ provenance }

func (nodeAttachmentSubject) nodeAttachment() {}

func newNodeAttachmentSubject(sourceFile string, segments ...string) NodeAttachmentSubject {
	return nodeAttachmentSubject{newProvenance(sourceFile, segments...)}
}

// NodeAttachmentSocketSubject is the subject of a check invoked once per socket
// an attachment's own dependency block declares.
//
// Those sockets are declared on the attached Feature, a separate authoring
// surface from the component's own sockets, which is why the kind is keyed per
// attachment as well as per node.
type NodeAttachmentSocketSubject interface {
	Subject
	nodeAttachmentSocket()
}

type nodeAttachmentSocketSubject struct{ provenance }

func (nodeAttachmentSocketSubject) nodeAttachmentSocket() {}

func newNodeAttachmentSocketSubject(sourceFile string, segments ...string) NodeAttachmentSocketSubject {
	return nodeAttachmentSocketSubject{newProvenance(sourceFile, segments...)}
}

// NodeSlotSubject is the subject of a check invoked once per attachment slot a
// node's component type declares.
//
// A slot is a binding point and not a dependency socket: the two are separate
// authored surfaces with separate rules over them, so they are separate kinds.
// Collapsing them would flatten a distinction the author writes.
type NodeSlotSubject interface {
	Subject
	nodeSlot()
}

type nodeSlotSubject struct{ provenance }

func (nodeSlotSubject) nodeSlot() {}

func newNodeSlotSubject(sourceFile string, segments ...string) NodeSlotSubject {
	return nodeSlotSubject{newProvenance(sourceFile, segments...)}
}

// NodeMechanismSubject is the subject of a check invoked once per delivery
// mechanism a node's injection block declares.
type NodeMechanismSubject interface {
	Subject
	nodeMechanism()
}

type nodeMechanismSubject struct{ provenance }

func (nodeMechanismSubject) nodeMechanism() {}

func newNodeMechanismSubject(sourceFile string, segments ...string) NodeMechanismSubject {
	return nodeMechanismSubject{newProvenance(sourceFile, segments...)}
}

// NodeMechanismDeliveryKeySubject is the subject of a check invoked once per key
// a node delivers through one injection mechanism.
type NodeMechanismDeliveryKeySubject interface {
	Subject
	nodeMechanismDeliveryKey()
}

type nodeMechanismDeliveryKeySubject struct{ provenance }

func (nodeMechanismDeliveryKeySubject) nodeMechanismDeliveryKey() {}

func newNodeMechanismDeliveryKeySubject(sourceFile string, segments ...string) NodeMechanismDeliveryKeySubject {
	return nodeMechanismDeliveryKeySubject{newProvenance(sourceFile, segments...)}
}

// NodePillarLeafSubject is the subject of a check invoked once per leaf of one
// configuration pillar a node's author wrote.
type NodePillarLeafSubject interface {
	Subject
	nodePillarLeaf()
}

type nodePillarLeafSubject struct{ provenance }

func (nodePillarLeafSubject) nodePillarLeaf() {}

func newNodePillarLeafSubject(sourceFile string, segments ...string) NodePillarLeafSubject {
	return nodePillarLeafSubject{newProvenance(sourceFile, segments...)}
}

// NodeParameterSubject is the subject of a check invoked once per parameter a
// node resolved.
type NodeParameterSubject interface {
	Subject
	nodeParameter()
}

type nodeParameterSubject struct{ provenance }

func (nodeParameterSubject) nodeParameter() {}

func newNodeParameterSubject(sourceFile string, segments ...string) NodeParameterSubject {
	return nodeParameterSubject{newProvenance(sourceFile, segments...)}
}

// NodeAttachmentPillarLeafSubject is the subject of a check invoked once per
// leaf of one pillar of one attachment's configuration.
//
// The pillar is a coordinate of its own here, and the slot is a coordinate in
// its own right elsewhere: the two are not the same axis.
type NodeAttachmentPillarLeafSubject interface {
	Subject
	nodeAttachmentPillarLeaf()
}

type nodeAttachmentPillarLeafSubject struct{ provenance }

func (nodeAttachmentPillarLeafSubject) nodeAttachmentPillarLeaf() {}

func newNodeAttachmentPillarLeafSubject(sourceFile string, segments ...string) NodeAttachmentPillarLeafSubject {
	return nodeAttachmentPillarLeafSubject{newProvenance(sourceFile, segments...)}
}

// NodeLocalsKeySubject is the subject of a check invoked once per local a node's
// Recipe declares.
//
// Locals are evaluated in the order they are declared, so a check over this kind
// is invoked in that order and a rule about what a local may reference is a rule
// about it.
type NodeLocalsKeySubject interface {
	Subject
	nodeLocalsKey()
}

type nodeLocalsKeySubject struct{ provenance }

func (nodeLocalsKeySubject) nodeLocalsKey() {}

func newNodeLocalsKeySubject(sourceFile string, segments ...string) NodeLocalsKeySubject {
	return nodeLocalsKeySubject{newProvenance(sourceFile, segments...)}
}

// NodeProvidesKeySubject is the subject of a check invoked once per key a node's
// Recipe provides.
type NodeProvidesKeySubject interface {
	Subject
	nodeProvidesKey()
}

type nodeProvidesKeySubject struct{ provenance }

func (nodeProvidesKeySubject) nodeProvidesKey() {}

func newNodeProvidesKeySubject(sourceFile string, segments ...string) NodeProvidesKeySubject {
	return nodeProvidesKeySubject{newProvenance(sourceFile, segments...)}
}

// NodePipelineStepSubject is the subject of a check invoked once per step of a
// node's Recipe pipeline.
type NodePipelineStepSubject interface {
	Subject
	nodePipelineStep()
}

type nodePipelineStepSubject struct{ provenance }

func (nodePipelineStepSubject) nodePipelineStep() {}

func newNodePipelineStepSubject(sourceFile string, segments ...string) NodePipelineStepSubject {
	return nodePipelineStepSubject{newProvenance(sourceFile, segments...)}
}

// BoundaryProviderPathSubject is the subject of a check invoked once per output
// path written for one provider within one boundary.
//
// It is the one kind rooted at a boundary rather than at a document or a node,
// because the emitted file set is keyed that way: collisions between paths are
// only visible per provider within the boundary that holds them.
type BoundaryProviderPathSubject interface {
	Subject
	boundaryProviderPath()
}

type boundaryProviderPathSubject struct{ provenance }

func (boundaryProviderPathSubject) boundaryProviderPath() {}

func newBoundaryProviderPathSubject(sourceFile string, segments ...string) BoundaryProviderPathSubject {
	return boundaryProviderPathSubject{newProvenance(sourceFile, segments...)}
}

// NodeArtifactSubject is the subject of a check invoked once per artifact a node
// projects.
type NodeArtifactSubject interface {
	Subject
	nodeArtifact()
}

type nodeArtifactSubject struct{ provenance }

func (nodeArtifactSubject) nodeArtifact() {}

func newNodeArtifactSubject(sourceFile string, segments ...string) NodeArtifactSubject {
	return nodeArtifactSubject{newProvenance(sourceFile, segments...)}
}
