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
// is — an identifier, a name, a key, a path — rather than the bare coordinate, so
// a signature reads as the thing it returns. The suffix is a judgement and not a
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

// DocumentAnnotationBindingSubject is the subject of a check invoked once per
// annotation binding the presented Manifest's registry declares.
//
// It is a separate kind from the document itself for the same reason a declared
// parameter is: "once per binding" is a different once, and the subject can name
// the binding's authored site where a document subject names only the file.
//
// The coordinate value is the annotation key the author wrote — the key the
// binding is declared under, which is what identifies one binding among the
// registry's entries.
type DocumentAnnotationBindingSubject interface {
	Subject

	// AnnotationBindingKey is the annotation key whose binding the check was
	// invoked for. The document itself is the other coordinate, and SourceFile
	// names it.
	AnnotationBindingKey() string

	documentAnnotationBinding()
}

type documentAnnotationBindingSubject struct {
	provenance
	binding string
}

func (documentAnnotationBindingSubject) documentAnnotationBinding() {}

func (s documentAnnotationBindingSubject) AnnotationBindingKey() string { return s.binding }

func newDocumentAnnotationBindingSubject(
	sourceFile, binding string,
	segments ...string,
) DocumentAnnotationBindingSubject {
	return documentAnnotationBindingSubject{
		provenance: newProvenance(sourceFile, segments...),
		binding:    binding,
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

// EdgeSubject is the subject of a check invoked once per connection: one wiring
// of a consumer's socket to a target.
//
// Its single coordinate is the edge, whose identity is the node, the socket and
// the alias taken together, so it carries an accessor for each element of that
// triple rather than one naming the edge — the triple is what the fact keyed by
// an edge takes as its key, and the edge itself names no scalar a check could
// use.
//
// The alias is in the identity because one socket carries many connections. A
// check invoked once per socket would be invoked once for what the author wrote
// twice, and could name only the socket in what it raised.
type EdgeSubject interface {
	Subject

	// NodeID identifies the consumer the edge is authored in, as every keyed
	// fact takes it.
	NodeID() string

	// SocketName is the dependency socket the edge wires.
	SocketName() string

	// AliasName names the connection within its socket. It is carried whether or
	// not the author wrote it as a key, because a coordinate keys a fact and a
	// locator names an authored site — so an alias no key spells is still the
	// value every fact keyed by this edge takes.
	AliasName() string

	edge()
}

type edgeSubject struct {
	provenance
	node   string
	socket string
	alias  string
}

func (edgeSubject) edge() {}

func (s edgeSubject) NodeID() string { return s.node }

func (s edgeSubject) SocketName() string { return s.socket }

func (s edgeSubject) AliasName() string { return s.alias }

func newEdgeSubject(sourceFile, node, socket, alias string, segments ...string) EdgeSubject {
	return edgeSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		socket:     socket,
		alias:      alias,
	}
}

// CarriedConnectionSubject is the subject of a check invoked once per connection
// together with the carrier that drew it.
//
// It is the sibling of [EdgeSubject] and not a replacement for it. An edge's
// identity is the node, the socket and the alias; a carried connection's is that
// triple plus the surface the connection was drawn from — because the triple
// alone names up to three separately authored connections at once, and a rule
// reading a connection's AUTHORED content needs to be invoked for each of them
// rather than once for all three.
//
// So a rule over what a connection RESOLVED to declares [EdgeSubject], whose key
// is the key those facts take, and a rule over what an author WROTE on one
// declares this kind.
type CarriedConnectionSubject interface {
	Subject

	// NodeID identifies the consumer the connection is drawn from, as every
	// keyed fact takes it.
	NodeID() string

	// CarrierKind is which surface drew the connection: one of
	// [CarrierComponent], [CarrierAttachment] or [CarrierDeployment].
	CarrierKind() string

	// CarrierAttachment is the attachment instance name the connection was drawn
	// from, empty for every kind but [CarrierAttachment].
	CarrierAttachment() string

	// SocketName is the dependency socket the connection wires.
	SocketName() string

	// AliasName names the connection within its socket. It is carried whether or
	// not the author wrote it as a key, for the reason [EdgeSubject] states.
	AliasName() string

	// TargetNodeID identifies the node the connection resolves to, as every
	// keyed fact takes it. It is in the identity because two carriers writing
	// one socket and one alias may name different targets, and each delivers
	// against the target it named.
	TargetNodeID() string

	carriedConnection()
}

type carriedConnectionSubject struct {
	provenance
	node       string
	kind       string
	attachment string
	socket     string
	alias      string
	target     string
}

func (carriedConnectionSubject) carriedConnection() {}

func (s carriedConnectionSubject) NodeID() string { return s.node }

func (s carriedConnectionSubject) CarrierKind() string { return s.kind }

func (s carriedConnectionSubject) CarrierAttachment() string { return s.attachment }

func (s carriedConnectionSubject) SocketName() string { return s.socket }

func (s carriedConnectionSubject) AliasName() string { return s.alias }

func (s carriedConnectionSubject) TargetNodeID() string { return s.target }

func newCarriedConnectionSubject(
	sourceFile, node, kind, attachment, socket, alias, target string,
	segments ...string,
) CarriedConnectionSubject {
	return carriedConnectionSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		kind:       kind,
		attachment: attachment,
		socket:     socket,
		alias:      alias,
		target:     target,
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

// SubsystemAttachmentSubject is the subject of a check invoked once per Feature
// attachment declared on a subsystem itself.
//
// It is rooted at a subsystem rather than at a node because that is where the
// attachment is written. Such an attachment applies to every component the
// subsystem holds, so it is authored once and takes effect many times — and the
// two ways of keying it are not equivalent. Keyed by the component it reaches, a
// rule would be asked once per component, would name a component rather than the
// site the author wrote, and would be asked no times at all for a subsystem
// holding no components, which is silence a caller cannot tell from approval.
// Keyed here it is asked exactly once per authored attachment.
//
// It is the twin of the kind rooted at a boundary: the same shape of authored
// scope holding the same shape of attachment, on the other authoring document.
// Neither is reached by the other's traversal, because the two scopes are declared
// in separate documents by separate hands.
type SubsystemAttachmentSubject interface {
	Subject

	// SubsystemPath is the subsystem the attachment was declared on, as every
	// keyed fact takes it. It is the scope's path from the document's own scope
	// set rather than a bare name, because subsystems nest and two scopes at
	// different depths may carry one name.
	SubsystemPath() string

	// AttachmentName is the attachment the check was invoked for: the name the
	// author wrote it under.
	AttachmentName() string

	subsystemAttachment()
}

type subsystemAttachmentSubject struct {
	provenance
	subsystem  string
	attachment string
}

func (subsystemAttachmentSubject) subsystemAttachment() {}

func (s subsystemAttachmentSubject) SubsystemPath() string { return s.subsystem }

func (s subsystemAttachmentSubject) AttachmentName() string { return s.attachment }

func newSubsystemAttachmentSubject(
	sourceFile, subsystem, attachment string,
	segments ...string,
) SubsystemAttachmentSubject {
	return subsystemAttachmentSubject{
		provenance: newProvenance(sourceFile, segments...),
		subsystem:  subsystem,
		attachment: attachment,
	}
}

// NodeAttachmentSocketSubject is the subject of a check invoked once per socket
// an attachment WIRES in its own dependency block — never once per socket the
// attached Feature declares. The axis is the enumerator's contract and is stated
// there; it is named here too because the two sets differ and a reader arriving
// at the subject must not have to guess which one reached it.
//
// The socket names are drawn from the attached Feature, a separate authoring
// surface from the component's own sockets, which is why the kind is keyed per
// attachment as well as per node: one socket name means different things under
// two attachments of different Features.
type NodeAttachmentSocketSubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose own dependency block wires the
	// socket.
	AttachmentName() string

	// SocketName is the socket the check was invoked for: one the attachment
	// wired. Its name comes from the attached Feature's surface rather than the
	// component's own, so it is not one of the component's sockets — which
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

// NodeAttachmentPipelineStepSubject is the subject of a check invoked once per
// step of the pipeline one attachment's FeatureRecipe carries on a node.
//
// It is keyed per attachment as well as per node because the pipeline is
// authored on the FeatureRecipe the attachment binds, a different document from
// the node's own Recipe. Two attachments on one node therefore carry
// independently authored pipelines, and nothing makes their step identifiers
// distinct from each other or from the node's own — so the attachment is what
// tells one pipeline from another, and a kind carrying only the node and the
// step could not.
type NodeAttachmentPipelineStepSubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose FeatureRecipe declares the
	// pipeline.
	AttachmentName() string

	// StepID is the step the check was invoked for: the identifier the entry
	// carries rather than its position, because a position moves when a step is
	// inserted above it. Which pipeline the identifier belongs to is said by the
	// kind it is reached within.
	StepID() string

	nodeAttachmentPipelineStep()
}

type nodeAttachmentPipelineStepSubject struct {
	provenance
	node       string
	attachment string
	stepID     string
}

func (nodeAttachmentPipelineStepSubject) nodeAttachmentPipelineStep() {}

func (s nodeAttachmentPipelineStepSubject) NodeID() string { return s.node }

func (s nodeAttachmentPipelineStepSubject) AttachmentName() string { return s.attachment }

func (s nodeAttachmentPipelineStepSubject) StepID() string { return s.stepID }

func newNodeAttachmentPipelineStepSubject(
	sourceFile, node, attachment, step string,
	segments ...string,
) NodeAttachmentPipelineStepSubject {
	return nodeAttachmentPipelineStepSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
		stepID:     step,
	}
}

// BoundaryProviderPathSubject is the subject of a check invoked once per output
// path written for one provider within one boundary.
//
// It is rooted at a boundary rather than at a document or a node because the
// emitted file set is keyed that way: collisions between paths are only visible
// per provider within the boundary that holds them.
type BoundaryProviderPathSubject interface {
	Subject

	// BoundaryPath is the boundary the emitted set was written within, as every
	// keyed fact takes it. It is the scope's path from the document's own scope
	// set rather than a bare name, because boundaries nest and two scopes at
	// different depths may carry one name.
	BoundaryPath() string

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

func (s boundaryProviderPathSubject) BoundaryPath() string { return s.boundary }

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

// AssemblyPathProviderSubject is the subject of a check invoked once per
// assembly group: one provider's contribution at one evaluated assembly path.
//
// It is rooted at the assembly path rather than at a boundary because the group
// is what several boundaries collapse into. Boundaries whose own path
// declarations evaluate alike under one provider are merged into a single group
// and assemble as one, so a rule about the group keyed per boundary would be
// asked once per contributor about a thing there is only one of — and a rule
// about what several groups did to one path could not be asked at all, because
// no boundary holds that view.
//
// Neither coordinate reaches the locator. An assembly path is evaluated output
// rather than a key any document holds, and a provider is selected by the entity
// a component names rather than written at this site — so a subject here carries
// the deployment document and the empty locator, which routes a reader to
// file-level positioning rather than to a path the file does not contain. The
// boundaries behind the group are a fact rather than a coordinate, and a group
// several of them fed has no single authored site to name in any case; a rule
// that has to say which sites an operator can edit reads that fact and names
// them in its detail.
type AssemblyPathProviderSubject interface {
	Subject

	// AssemblyPath is the evaluated final path the group assembles at, as every
	// keyed fact takes it.
	AssemblyPath() string

	// ProviderName is the provider that rendered the group.
	ProviderName() string

	assemblyPathProvider()
}

type assemblyPathProviderSubject struct {
	provenance
	assemblyPath string
	provider     string
}

func (assemblyPathProviderSubject) assemblyPathProvider() {}

func (s assemblyPathProviderSubject) AssemblyPath() string { return s.assemblyPath }

func (s assemblyPathProviderSubject) ProviderName() string { return s.provider }

func newAssemblyPathProviderSubject(
	sourceFile, assemblyPath, provider string,
) AssemblyPathProviderSubject {
	return assemblyPathProviderSubject{
		provenance:   newProvenance(sourceFile),
		assemblyPath: assemblyPath,
		provider:     provider,
	}
}

// BoundaryAttachmentSubject is the subject of a check invoked once per Feature
// attachment declared on a boundary itself.
//
// It is rooted at a boundary rather than at a node because that is where the
// attachment is written. Such an attachment applies to every component the
// boundary holds, so it is authored once and takes effect many times — and the
// two ways of keying it are not equivalent. Keyed by the component it reaches,
// a rule would be asked once per component, would name a component rather than
// the site the operator wrote, and would be asked no times at all for a
// boundary holding no components, which is silence a caller cannot tell from
// approval. Keyed here it is asked exactly once per authored attachment.
type BoundaryAttachmentSubject interface {
	Subject

	// BoundaryPath is the boundary the attachment was declared on, as every
	// keyed fact takes it. It is the scope's path from the document's own scope
	// set rather than a bare name, because boundaries nest and two scopes at
	// different depths may carry one name.
	BoundaryPath() string

	// AttachmentName is the attachment the check was invoked for: the name the
	// operator wrote it under.
	AttachmentName() string

	boundaryAttachment()
}

type boundaryAttachmentSubject struct {
	provenance
	boundary   string
	attachment string
}

func (boundaryAttachmentSubject) boundaryAttachment() {}

func (s boundaryAttachmentSubject) BoundaryPath() string { return s.boundary }

func (s boundaryAttachmentSubject) AttachmentName() string { return s.attachment }

func newBoundaryAttachmentSubject(
	sourceFile, boundary, attachment string,
	segments ...string,
) BoundaryAttachmentSubject {
	return boundaryAttachmentSubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		attachment: attachment,
	}
}

// BoundaryAttachmentPipelineStepSubject is the subject of a check invoked once
// per step of the pipeline one boundary-scope attachment's FeatureRecipe
// carries.
//
// It is rooted at a boundary rather than at a node for the reason the
// boundary-attachment kind is: the attachment is declared at the boundary, and
// it reaches every component the boundary holds, so a component-rooted key
// would present one authored pipeline many times over and would have no answer
// at all for a boundary holding no components.
//
// The step identifiers here are unrelated to any component's, so this is a
// separate kind rather than the node-rooted one reached differently: no
// node-rooted key can name a pipeline authored against a boundary.
type BoundaryAttachmentPipelineStepSubject interface {
	Subject

	// BoundaryPath is the boundary the attachment was declared on, as every
	// keyed fact takes it. It is the scope's path from the document's own scope
	// set rather than a bare name, because boundaries nest and two scopes at
	// different depths may carry one name.
	BoundaryPath() string

	// AttachmentName is the attachment whose FeatureRecipe declares the
	// pipeline: the name the operator wrote it under.
	AttachmentName() string

	// StepID is the step the check was invoked for: the identifier the entry
	// carries rather than its position, because a position moves when a step is
	// inserted above it.
	StepID() string

	boundaryAttachmentPipelineStep()
}

type boundaryAttachmentPipelineStepSubject struct {
	provenance
	boundary   string
	attachment string
	stepID     string
}

func (boundaryAttachmentPipelineStepSubject) boundaryAttachmentPipelineStep() {}

func (s boundaryAttachmentPipelineStepSubject) BoundaryPath() string { return s.boundary }

func (s boundaryAttachmentPipelineStepSubject) AttachmentName() string { return s.attachment }

func (s boundaryAttachmentPipelineStepSubject) StepID() string { return s.stepID }

func newBoundaryAttachmentPipelineStepSubject(
	sourceFile, boundary, attachment, step string,
	segments ...string,
) BoundaryAttachmentPipelineStepSubject {
	return boundaryAttachmentPipelineStepSubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		attachment: attachment,
		stepID:     step,
	}
}

// NodeArtifactSubject is the subject of a check invoked once per artifact a node
// projects.
type NodeArtifactSubject interface {
	Subject

	// NodeID identifies the node projecting the artifact, as every keyed fact
	// takes it.
	NodeID() string

	// ArtifactName is the artifact the check was invoked for, named by the
	// output name the entry resolves to.
	//
	// An entry written inline and an entry supplied by an imported file each
	// resolve to their own authored key, so for those forms the two are one
	// value. An entry supplied by a library bundle expands into one artifact per
	// file the bundle holds, each nested under the key as <key>/<filename> — so
	// there the authored key names no artifact on its own. Keying a check by the
	// output name is what lets it reach every artifact a node projects rather
	// than only the ones whose two names coincide.
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

// NodeAttachmentArtifactSubject is the subject of a check invoked once per
// artifact the FeatureRecipe one attachment binds on a node projects.
//
// It is keyed per attachment as well as per node because the artifacts block is
// authored on the FeatureRecipe the attachment binds, a different document from
// the node's own Recipe. Two attachments on one node therefore carry
// independently authored artifacts blocks, and nothing makes their output
// filenames distinct from each other or from the node's own — so the attachment
// is what tells one block from another, and a kind carrying only the node and the
// artifact could not.
type NodeAttachmentArtifactSubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose FeatureRecipe projects the artifact.
	AttachmentName() string

	// ArtifactName is the artifact the check was invoked for, named by the output
	// name the entry resolves to — on the same reading the node's own artifact
	// kind carries: an inline entry and an imported file each resolve to their
	// own authored key, and a library bundle to one name per file it holds, each
	// nested under that key. Which artifacts block the name belongs to is said by
	// the kind it is reached within.
	ArtifactName() string

	nodeAttachmentArtifact()
}

type nodeAttachmentArtifactSubject struct {
	provenance
	node       string
	attachment string
	artifact   string
}

func (nodeAttachmentArtifactSubject) nodeAttachmentArtifact() {}

func (s nodeAttachmentArtifactSubject) NodeID() string { return s.node }

func (s nodeAttachmentArtifactSubject) AttachmentName() string { return s.attachment }

func (s nodeAttachmentArtifactSubject) ArtifactName() string { return s.artifact }

func newNodeAttachmentArtifactSubject(
	sourceFile, node, attachment, artifact string,
	segments ...string,
) NodeAttachmentArtifactSubject {
	return nodeAttachmentArtifactSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
		artifact:   artifact,
	}
}

// BoundaryAttachmentArtifactSubject is the subject of a check invoked once per
// artifact the FeatureRecipe one boundary-scope attachment binds projects.
//
// It is rooted at a boundary rather than at a node because that is where the
// attachment is declared. Such an attachment is written once and reaches every
// component the boundary holds, so a node-rooted walk would ask about one
// authored entry once per component — the same authored text presented as many
// times as the boundary has members — and would ask about it not at all for a
// boundary holding none, which is silence a caller cannot tell from approval.
//
// The output filenames in that block are chosen independently of any component's,
// so this is a separate kind rather than the node-rooted one reached differently:
// no node-rooted key can name an artifact authored against a boundary.
type BoundaryAttachmentArtifactSubject interface {
	Subject

	// BoundaryPath is the boundary the attachment was declared on, as every
	// keyed fact takes it. It is the scope's path from the document's own scope
	// set rather than a bare name, because boundaries nest and two scopes at
	// different depths may carry one name.
	BoundaryPath() string

	// AttachmentName is the attachment whose FeatureRecipe projects the artifact:
	// the name the operator wrote it under.
	AttachmentName() string

	// ArtifactName is the artifact the check was invoked for, named by the output
	// name the entry resolves to — on the same reading every artifact kind
	// carries: an inline entry and an imported file each resolve to their own
	// authored key, and a library bundle to one name per file it holds, each
	// nested under that key.
	ArtifactName() string

	boundaryAttachmentArtifact()
}

type boundaryAttachmentArtifactSubject struct {
	provenance
	boundary   string
	attachment string
	artifact   string
}

func (boundaryAttachmentArtifactSubject) boundaryAttachmentArtifact() {}

func (s boundaryAttachmentArtifactSubject) BoundaryPath() string { return s.boundary }

func (s boundaryAttachmentArtifactSubject) AttachmentName() string { return s.attachment }

func (s boundaryAttachmentArtifactSubject) ArtifactName() string { return s.artifact }

func newBoundaryAttachmentArtifactSubject(
	sourceFile, boundary, attachment, artifact string,
	segments ...string,
) BoundaryAttachmentArtifactSubject {
	return boundaryAttachmentArtifactSubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		attachment: attachment,
		artifact:   artifact,
	}
}

// ModuleSubject is the subject of a check invoked once per sub-module the
// composition brought in: one entry of an Aggregate's `aggregates` block, at
// every depth.
//
// It is rooted at the module rather than at the components the module
// contributes, and the two keyings are not equivalent. Keyed by the component,
// a rule over what the deployment did with a module would be asked once per
// component, would report one authoring fault as many, and would name a
// component where the author's remedy is a single entry — and it would be asked
// no times at all for a module contributing nothing, which is silence a caller
// cannot tell from approval. Keyed here it is asked exactly once per module the
// composition brought in, whatever that module holds.
//
// Its provenance is the document that DECLARES the module, which is the parent's
// document rather than the module's own: the root Aggregate for a module the
// application imports directly, and the importing module's document for one
// nested inside it. That is where the entry a rule would send an author to fix
// actually sits.
type ModuleSubject interface {
	Subject

	// ModulePath is the module the check was invoked for, as every keyed fact
	// takes it: the dotted prefix the module's components carry in the
	// flattened set. It is the path through the nesting rather than one
	// authored key, because modules nest and two modules at different depths
	// may carry one name.
	ModulePath() string

	module()
}

type moduleSubject struct {
	provenance
	modulePath string
}

func (moduleSubject) module() {}

func (s moduleSubject) ModulePath() string { return s.modulePath }

func newModuleSubject(sourceFile, module string, segments ...string) ModuleSubject {
	return moduleSubject{
		provenance: newProvenance(sourceFile, segments...),
		modulePath: module,
	}
}
