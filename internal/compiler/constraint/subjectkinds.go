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
// is — an identifier, a name, a key — rather than the bare coordinate, so a
// signature reads as the thing it returns. The suffix is a judgement and not a
// transform: an emitted path is not named for its coordinate at all, and four
// coordinates are written with a hyphen and could not be Go identifiers even
// spelled mechanically. So the choice is made once per coordinate *name* — never
// per kind, which would be a second copy of a derived set — and a test derives
// each kind's expected accessor set from the generated coordinates through that
// one mapping, refusing a coordinate the mapping has no entry for rather than
// passing over it.
//
// One accessor name means one thing wherever it appears, and which coordinate
// set a value came from is said by the kind that carries it rather than by the
// name: the socket of an attachment and the socket of a component are both
// SocketName, on two kinds a check cannot confuse.
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

	// ParameterName is the declared parameter the check was invoked for. The
	// document itself is the other coordinate, and SourceFile names it.
	ParameterName() string

	documentParameter()
}

type documentParameterSubject struct {
	provenance
	parameter string
}

func (documentParameterSubject) documentParameter() {}

func (s documentParameterSubject) ParameterName() string { return s.parameter }

func newDocumentParameterSubject(sourceFile, parameter string, segments ...string) DocumentParameterSubject {
	return documentParameterSubject{
		provenance: newProvenance(sourceFile, segments...),
		parameter:  parameter,
	}
}

// DocumentAssertionSubject is the subject of a check invoked once per assertion
// an entity declares across its resolved inheritance chain.
type DocumentAssertionSubject interface {
	Subject

	// AssertionName is the declared assertion the check was invoked for. The
	// document itself is the other coordinate, and SourceFile names it.
	AssertionName() string

	documentAssertion()
}

type documentAssertionSubject struct {
	provenance
	assertion string
}

func (documentAssertionSubject) documentAssertion() {}

func (s documentAssertionSubject) AssertionName() string { return s.assertion }

func newDocumentAssertionSubject(sourceFile, assertion string, segments ...string) DocumentAssertionSubject {
	return documentAssertionSubject{
		provenance: newProvenance(sourceFile, segments...),
		assertion:  assertion,
	}
}

// NodeSubject is the subject of a check invoked once per node: one component
// instance in the flattened set, a sub-aggregate's nodes included.
//
// A component instance's provenance follows a rule of its own, because whether
// the instance has an authored site the dotted locator can name depends on how
// it was reached. See [newComponentNodeSubject].
type NodeSubject interface {
	Subject

	// NodeID identifies the node the check was invoked for, as every keyed fact
	// takes it. It is the identifier the facts are keyed by and not the name the
	// author wrote: a node flattened out of a sub-aggregate carries a qualified
	// identifier, and the authored name is what the locator is built from.
	NodeID() string

	node()
}

// The coordinate field is nodeID rather than node because the marker method of
// this kind is node(), and a struct cannot carry a field and a method of one
// name. Every other node-rooted kind's marker is named for its innermost
// coordinate, so this is the one kind where the collision arises.
type nodeSubject struct {
	provenance
	nodeID string
}

func (nodeSubject) node() {}

func (s nodeSubject) NodeID() string { return s.nodeID }

func newNodeSubject(sourceFile, node string, segments ...string) NodeSubject {
	return nodeSubject{
		provenance: newProvenance(sourceFile, segments...),
		nodeID:     node,
	}
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

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment the check was invoked for.
	AttachmentName() string

	nodeAttachment()
}

type nodeAttachmentSubject struct {
	provenance
	node       string
	attachment string
}

func (nodeAttachmentSubject) nodeAttachment() {}

func (s nodeAttachmentSubject) NodeID() string { return s.node }

func (s nodeAttachmentSubject) AttachmentName() string { return s.attachment }

func newNodeAttachmentSubject(sourceFile, node, attachment string, segments ...string) NodeAttachmentSubject {
	return nodeAttachmentSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
	}
}

// NodeAttachmentSocketSubject is the subject of a check invoked once per socket
// an attachment's own dependency block declares.
//
// Those sockets are declared on the attached Feature, a separate authoring
// surface from the component's own sockets, which is why the kind is keyed per
// attachment as well as per node.
type NodeAttachmentSocketSubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose own dependency block declares the
	// socket.
	AttachmentName() string

	// SocketName is the socket the check was invoked for. It is declared on the
	// attached Feature, so it is not one of the component's own sockets — which
	// coordinate set it belongs to is said by the kind it is reached within.
	SocketName() string

	nodeAttachmentSocket()
}

type nodeAttachmentSocketSubject struct {
	provenance
	node       string
	attachment string
	socket     string
}

func (nodeAttachmentSocketSubject) nodeAttachmentSocket() {}

func (s nodeAttachmentSocketSubject) NodeID() string { return s.node }

func (s nodeAttachmentSocketSubject) AttachmentName() string { return s.attachment }

func (s nodeAttachmentSocketSubject) SocketName() string { return s.socket }

func newNodeAttachmentSocketSubject(
	sourceFile, node, attachment, socket string,
	segments ...string,
) NodeAttachmentSocketSubject {
	return nodeAttachmentSocketSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
		socket:     socket,
	}
}

// NodeSlotSubject is the subject of a check invoked once per attachment slot a
// node's component type declares.
//
// A slot is a binding point and not a dependency socket: the two are separate
// authored surfaces with separate rules over them, so they are separate kinds.
// Collapsing them would flatten a distinction the author writes.
type NodeSlotSubject interface {
	Subject

	// NodeID identifies the node whose component type declares the slot, as every
	// keyed fact takes it.
	NodeID() string

	// SlotName is the attachment slot the check was invoked for.
	SlotName() string

	nodeSlot()
}

type nodeSlotSubject struct {
	provenance
	node string
	slot string
}

func (nodeSlotSubject) nodeSlot() {}

func (s nodeSlotSubject) NodeID() string { return s.node }

func (s nodeSlotSubject) SlotName() string { return s.slot }

func newNodeSlotSubject(sourceFile, node, slot string, segments ...string) NodeSlotSubject {
	return nodeSlotSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		slot:       slot,
	}
}

// NodeMechanismSubject is the subject of a check invoked once per delivery
// mechanism a node's injection block declares.
type NodeMechanismSubject interface {
	Subject

	// NodeID identifies the node whose injection block declares the mechanism, as
	// every keyed fact takes it.
	NodeID() string

	// MechanismName is the delivery mechanism the check was invoked for.
	MechanismName() string

	nodeMechanism()
}

type nodeMechanismSubject struct {
	provenance
	node      string
	mechanism string
}

func (nodeMechanismSubject) nodeMechanism() {}

func (s nodeMechanismSubject) NodeID() string { return s.node }

func (s nodeMechanismSubject) MechanismName() string { return s.mechanism }

func newNodeMechanismSubject(sourceFile, node, mechanism string, segments ...string) NodeMechanismSubject {
	return nodeMechanismSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		mechanism:  mechanism,
	}
}

// NodeMechanismDeliveryKeySubject is the subject of a check invoked once per key
// a node delivers through one injection mechanism.
type NodeMechanismDeliveryKeySubject interface {
	Subject

	// NodeID identifies the node delivering the key, as every keyed fact takes it.
	NodeID() string

	// MechanismName is the injection mechanism the key is delivered through.
	MechanismName() string

	// DeliveryKey is the key the check was invoked for: the name the value is
	// delivered under, which is what the consumer reads it by.
	DeliveryKey() string

	nodeMechanismDeliveryKey()
}

type nodeMechanismDeliveryKeySubject struct {
	provenance
	node      string
	mechanism string
	key       string
}

func (nodeMechanismDeliveryKeySubject) nodeMechanismDeliveryKey() {}

func (s nodeMechanismDeliveryKeySubject) NodeID() string { return s.node }

func (s nodeMechanismDeliveryKeySubject) MechanismName() string { return s.mechanism }

func (s nodeMechanismDeliveryKeySubject) DeliveryKey() string { return s.key }

func newNodeMechanismDeliveryKeySubject(
	sourceFile, node, mechanism, key string,
	segments ...string,
) NodeMechanismDeliveryKeySubject {
	return nodeMechanismDeliveryKeySubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		mechanism:  mechanism,
		key:        key,
	}
}

// NodePillarLeafSubject is the subject of a check invoked once per leaf of one
// configuration pillar a node's author wrote.
type NodePillarLeafSubject interface {
	Subject

	// NodeID identifies the node whose author wrote the pillar, as every keyed
	// fact takes it.
	NodeID() string

	// PillarName is the configuration pillar the leaf sits in.
	PillarName() string

	// LeafName is the leaf the check was invoked for.
	LeafName() string

	nodePillarLeaf()
}

type nodePillarLeafSubject struct {
	provenance
	node   string
	pillar string
	leaf   string
}

func (nodePillarLeafSubject) nodePillarLeaf() {}

func (s nodePillarLeafSubject) NodeID() string { return s.node }

func (s nodePillarLeafSubject) PillarName() string { return s.pillar }

func (s nodePillarLeafSubject) LeafName() string { return s.leaf }

func newNodePillarLeafSubject(
	sourceFile, node, pillar, leaf string,
	segments ...string,
) NodePillarLeafSubject {
	return nodePillarLeafSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		pillar:     pillar,
		leaf:       leaf,
	}
}

// NodeParameterSubject is the subject of a check invoked once per parameter a
// node resolved.
type NodeParameterSubject interface {
	Subject

	// NodeID identifies the node that resolved the parameter, as every keyed fact
	// takes it.
	NodeID() string

	// ParameterName is the resolved parameter the check was invoked for.
	ParameterName() string

	nodeParameter()
}

type nodeParameterSubject struct {
	provenance
	node      string
	parameter string
}

func (nodeParameterSubject) nodeParameter() {}

func (s nodeParameterSubject) NodeID() string { return s.node }

func (s nodeParameterSubject) ParameterName() string { return s.parameter }

func newNodeParameterSubject(sourceFile, node, parameter string, segments ...string) NodeParameterSubject {
	return nodeParameterSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		parameter:  parameter,
	}
}

// NodeAttachmentPillarLeafSubject is the subject of a check invoked once per
// leaf of one pillar of one attachment's configuration.
//
// The pillar is a coordinate of its own here, and the slot is a coordinate in
// its own right elsewhere: the two are not the same axis.
type NodeAttachmentPillarLeafSubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose configuration holds the pillar.
	AttachmentName() string

	// PillarName is the configuration pillar the leaf sits in.
	PillarName() string

	// LeafName is the leaf the check was invoked for.
	LeafName() string

	nodeAttachmentPillarLeaf()
}

type nodeAttachmentPillarLeafSubject struct {
	provenance
	node       string
	attachment string
	pillar     string
	leaf       string
}

func (nodeAttachmentPillarLeafSubject) nodeAttachmentPillarLeaf() {}

func (s nodeAttachmentPillarLeafSubject) NodeID() string { return s.node }

func (s nodeAttachmentPillarLeafSubject) AttachmentName() string { return s.attachment }

func (s nodeAttachmentPillarLeafSubject) PillarName() string { return s.pillar }

func (s nodeAttachmentPillarLeafSubject) LeafName() string { return s.leaf }

func newNodeAttachmentPillarLeafSubject(
	sourceFile, node, attachment, pillar, leaf string,
	segments ...string,
) NodeAttachmentPillarLeafSubject {
	return nodeAttachmentPillarLeafSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
		pillar:     pillar,
		leaf:       leaf,
	}
}

// NodeLocalsKeySubject is the subject of a check invoked once per local a node's
// Recipe declares.
//
// Locals are evaluated in the order they are declared, so a check over this kind
// is invoked in that order and a rule about what a local may reference is a rule
// about it.
type NodeLocalsKeySubject interface {
	Subject

	// NodeID identifies the node whose Recipe declares the local, as every keyed
	// fact takes it.
	NodeID() string

	// LocalsKey is the declared local the check was invoked for: the key the
	// author wrote, which a later local references it by.
	LocalsKey() string

	nodeLocalsKey()
}

type nodeLocalsKeySubject struct {
	provenance
	node string
	key  string
}

func (nodeLocalsKeySubject) nodeLocalsKey() {}

func (s nodeLocalsKeySubject) NodeID() string { return s.node }

func (s nodeLocalsKeySubject) LocalsKey() string { return s.key }

func newNodeLocalsKeySubject(sourceFile, node, key string, segments ...string) NodeLocalsKeySubject {
	return nodeLocalsKeySubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		key:        key,
	}
}

// NodeProvidesKeySubject is the subject of a check invoked once per key a node's
// Recipe provides.
type NodeProvidesKeySubject interface {
	Subject

	// NodeID identifies the node whose Recipe provides the key, as every keyed
	// fact takes it.
	NodeID() string

	// ProvidesKey is the provided key the check was invoked for: the name a
	// consumer of this node reads the value under.
	ProvidesKey() string

	nodeProvidesKey()
}

type nodeProvidesKeySubject struct {
	provenance
	node string
	key  string
}

func (nodeProvidesKeySubject) nodeProvidesKey() {}

func (s nodeProvidesKeySubject) NodeID() string { return s.node }

func (s nodeProvidesKeySubject) ProvidesKey() string { return s.key }

func newNodeProvidesKeySubject(sourceFile, node, key string, segments ...string) NodeProvidesKeySubject {
	return nodeProvidesKeySubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		key:        key,
	}
}

// NodePipelineStepSubject is the subject of a check invoked once per step of a
// node's Recipe pipeline.
type NodePipelineStepSubject interface {
	Subject

	// NodeID identifies the node whose Recipe declares the pipeline, as every
	// keyed fact takes it.
	NodeID() string

	// StepID is the step the check was invoked for. The pipeline is an ordered
	// list whose entries carry an identifier, and this is that identifier rather
	// than a position: a position moves when a step is inserted above it.
	StepID() string

	nodePipelineStep()
}

type nodePipelineStepSubject struct {
	provenance
	node   string
	stepID string
}

func (nodePipelineStepSubject) nodePipelineStep() {}

func (s nodePipelineStepSubject) NodeID() string { return s.node }

func (s nodePipelineStepSubject) StepID() string { return s.stepID }

func newNodePipelineStepSubject(sourceFile, node, step string, segments ...string) NodePipelineStepSubject {
	return nodePipelineStepSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		stepID:     step,
	}
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

	// NodeID identifies the node projecting the artifact, as every keyed fact
	// takes it.
	NodeID() string

	// ArtifactName is the artifact the check was invoked for: the authored key,
	// which is the output filename.
	ArtifactName() string

	nodeArtifact()
}

type nodeArtifactSubject struct {
	provenance
	node     string
	artifact string
}

func (nodeArtifactSubject) nodeArtifact() {}

func (s nodeArtifactSubject) NodeID() string { return s.node }

func (s nodeArtifactSubject) ArtifactName() string { return s.artifact }

func newNodeArtifactSubject(sourceFile, node, artifact string, segments ...string) NodeArtifactSubject {
	return nodeArtifactSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		artifact:   artifact,
	}
}
