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
//
// # Coordinate accessors
//
// A kind also exposes one exported accessor per coordinate it carries, returning
// the coordinate value the traversal was invoked for. They are what lets a check
// key an accessor to its own instance: every fact is keyed, so a check invoked
// once per (node, socket) reads a fact for that node and that socket rather than
// for the whole set.
//
// An accessor exists because the kind carries that coordinate, and for no other
// reason — never because a predicate would find a value convenient. A value that
// is a projection of a coordinate rather than a coordinate itself is read as the
// declared fact keyed by that coordinate, which is what keeps the dependency
// visible to the placement computation: a fact carries the point it completes at,
// and a subject carries none, so reading such a value from a subject would hide
// a real dependency from the point where a check's placement is worked out.
//
// The accessor name is the coordinate's name plus a suffix naming what the value
// is — an identifier, a name — rather than the bare coordinate, so a signature
// reads as the thing it returns.
//
// Adding one does not weaken the seal. Forgery is prevented by the unexported
// marker, which a type declared outside this package cannot declare however many
// exported methods the kind also carries.

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

	// NodeID identifies the node the socket is declared on, as every keyed
	// fact takes it.
	NodeID() string

	// SocketName is the dependency socket the check was invoked for.
	SocketName() string

	nodeSocket()
}

type nodeSocketSubject struct {
	provenance
	node   string
	socket string
}

func (nodeSocketSubject) nodeSocket() {}

func (s nodeSocketSubject) NodeID() string { return s.node }

func (s nodeSocketSubject) SocketName() string { return s.socket }

func newNodeSocketSubject(sourceFile, node, socket string, segments ...string) NodeSocketSubject {
	return nodeSocketSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		socket:     socket,
	}
}

// EdgeSubject is the subject of a check invoked once per edge: one wiring of a
// consumer's socket to a target.
//
// Its single coordinate is the edge, whose identity is the node and the socket
// taken together, so it carries an accessor for each half of that pair rather
// than one naming the edge — the pair is what the fact keyed by an edge takes as
// its key, and the edge itself names no scalar a check could use.
type EdgeSubject interface {
	Subject

	// NodeID identifies the consumer the edge is authored in, as every keyed
	// fact takes it.
	NodeID() string

	// SocketName is the dependency socket the edge wires.
	SocketName() string

	edge()
}

type edgeSubject struct {
	provenance
	node   string
	socket string
}

func (edgeSubject) edge() {}

func (s edgeSubject) NodeID() string { return s.node }

func (s edgeSubject) SocketName() string { return s.socket }

func newEdgeSubject(sourceFile, node, socket string, segments ...string) EdgeSubject {
	return edgeSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		socket:     socket,
	}
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

	// BoundaryName is the boundary the emitted set was written within, as every
	// keyed fact takes it.
	BoundaryName() string

	// ProviderName is the provider within that boundary the path was written for.
	ProviderName() string

	// EmittedPath is the output path the check was invoked for. It is the one
	// coordinate here that contributes no locator segment, because it is output
	// rather than something an author wrote — so a check naming a particular path
	// reads it from here.
	EmittedPath() string

	boundaryProviderPath()
}

type boundaryProviderPathSubject struct {
	provenance
	boundary string
	provider string
	path     string
}

func (boundaryProviderPathSubject) boundaryProviderPath() {}

func (s boundaryProviderPathSubject) BoundaryName() string { return s.boundary }

func (s boundaryProviderPathSubject) ProviderName() string { return s.provider }

func (s boundaryProviderPathSubject) EmittedPath() string { return s.path }

func newBoundaryProviderPathSubject(
	sourceFile, boundary, provider, path string,
	segments ...string,
) BoundaryProviderPathSubject {
	return boundaryProviderPathSubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		provider:   provider,
		path:       path,
	}
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
