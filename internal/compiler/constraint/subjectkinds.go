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
// A kind whose site can sit in either authoring document — a connection, an
// attachment written on one — also declares a second constructor taking which
// document the site recorded, named for the first with "WrittenIn" added. The
// document is a provenance input rather than a coordinate, so the first
// constructor stays the one a kind's coordinates are paired against, and it
// delegates to the second rather than building the subject a second time.
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

// DocumentSchemaPillarSubject is the subject of a check invoked once per
// top-level key the presented document's schema block declares.
//
// The coordinate is the key the author wrote rather than the pillar it was meant
// to name, and that is the whole reason the kind exists: the key set is what an
// author writes and the pillar set is what the contract admits, so a key naming
// no pillar has a coordinate here and would have none on a kind keyed by pillar.
//
// It is one kind over both authored document kinds that carry the block. Which
// document a key was read from is the document-kind axis, which the facts carry; the
// coordinate is the same site in either — a key inside one document's schema
// block — so a second kind would draw a distinction neither the locator nor a
// rule over it can see.
type DocumentSchemaPillarSubject interface {
	Subject

	// SchemaPillarKey is the top-level schema key the check was invoked for, as
	// the author wrote it. The document itself is the other coordinate, and
	// SourceFile names it.
	SchemaPillarKey() string

	documentSchemaPillar()
}

type documentSchemaPillarSubject struct {
	provenance
	key string
}

func (documentSchemaPillarSubject) documentSchemaPillar() {}

func (s documentSchemaPillarSubject) SchemaPillarKey() string { return s.key }

func newDocumentSchemaPillarSubject(
	sourceFile, key string,
	segments ...string,
) DocumentSchemaPillarSubject {
	return documentSchemaPillarSubject{
		provenance: newProvenance(sourceFile, segments...),
		key:        key,
	}
}

// DocumentOutputLabelSubject is the subject of a check invoked once per
// output-label key the presented Recipe declares.
//
// It is a separate kind from the document itself for the reason a declared
// parameter is: "once per declared key" is a different once, and the subject can
// name the block the key was written in where a document subject names only the
// file.
//
// Its coordinate is composite, and it carries an accessor for each element of
// that composite rather than one naming the declaration. A Recipe declares such
// keys in two blocks — one carrying literal values written in place, one naming
// keys whose values are taken from the component being rendered — and one key
// legitimately appears in each. So the key alone names up to two declarations,
// which is exactly the pair a rule about the two blocks meeting on a key exists
// to refuse, and the block alone names the whole block rather than an entry in
// it. The two together name one.
//
// It is one kind over both blocks rather than one per block, because the two
// are the same coordinate shape at two authored sites: a key inside one of the
// Recipe's own label blocks. Which block a key was written in is carried as the
// coordinate's own value, which is what lets one condition cover both and still
// name the block the author typed.
type DocumentOutputLabelSubject interface {
	Subject

	// OutputLabelBlock is the block the key is declared in, as the author wrote
	// it: one of [OutputLabelBlockLiteral] or [OutputLabelBlockPromoted]. It is
	// what tells a rule which of the two the author is being sent to, which the
	// key alone cannot say.
	OutputLabelBlock() string

	// OutputLabelKey is the label key the check was invoked for, as the author
	// wrote it. The document itself is the other coordinate, and SourceFile
	// names it.
	OutputLabelKey() string

	documentOutputLabel()
}

type documentOutputLabelSubject struct {
	provenance
	block string
	key   string
}

func (documentOutputLabelSubject) documentOutputLabel() {}

func (s documentOutputLabelSubject) OutputLabelBlock() string { return s.block }

func (s documentOutputLabelSubject) OutputLabelKey() string { return s.key }

func newDocumentOutputLabelSubject(
	sourceFile, block, key string,
	segments ...string,
) DocumentOutputLabelSubject {
	return documentOutputLabelSubject{
		provenance: newProvenance(sourceFile, segments...),
		block:      block,
		key:        key,
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

// DocumentMappingEntrySubject is the subject of a check invoked once per entry
// of the presented Manifest's recipe mapping table.
//
// It is a separate kind from the document itself for the reason a declared
// binding is: "once per entry" is a different once, and the subject can name the
// entry's authored site where a document subject names only the file.
//
// Its coordinate is composite, and it carries an accessor for each element of
// that composite rather than one naming the entry. An entry's identity is the
// tier it is written under, the axis it keys on and the key the author wrote,
// taken together — a tier holds two axes and an axis holds many keys, so no one
// of the three names an entry, and the entry itself names no scalar a check
// could use. The three together name exactly one, because each axis is a mapping
// and a mapping holds one value per key.
type DocumentMappingEntrySubject interface {
	Subject

	// MappingTier is the mapping tier the entry is written under, as the
	// author wrote it: one of [MappingTierComponents] or [MappingTierFeatures].
	// It is what tells a rule whether the entry names a Recipe or a
	// FeatureRecipe, which the key alone cannot say.
	MappingTier() string

	// MappingAxis is the keying axis within that tier the entry is written
	// under, as the author wrote it: one of [MappingAxisType] or
	// [MappingAxisName]. It is in the identity because one tier holds both, so
	// one key legitimately appears on each and the two are separate entries.
	MappingAxis() string

	// MappingKey is the key the entry is declared under — the component type,
	// feature type or instance name the entry maps. The document itself is the
	// other coordinate, and SourceFile names it.
	MappingKey() string

	documentMappingEntry()
}

type documentMappingEntrySubject struct {
	provenance
	tier string
	axis string
	key  string
}

func (documentMappingEntrySubject) documentMappingEntry() {}

func (s documentMappingEntrySubject) MappingTier() string { return s.tier }

func (s documentMappingEntrySubject) MappingAxis() string { return s.axis }

func (s documentMappingEntrySubject) MappingKey() string { return s.key }

func newDocumentMappingEntrySubject(
	sourceFile, tier, axis, key string,
	segments ...string,
) DocumentMappingEntrySubject {
	return documentMappingEntrySubject{
		provenance: newProvenance(sourceFile, segments...),
		tier:       tier,
		axis:       axis,
		key:        key,
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
	return newEdgeSubjectWrittenIn("", sourceFile, node, socket, alias, segments...)
}

// newEdgeSubjectWrittenIn is [newEdgeSubject] for a connection whose site records
// which authoring document it was written in; document is empty where it records
// none.
func newEdgeSubjectWrittenIn(document, sourceFile, node, socket, alias string, segments ...string) EdgeSubject {
	return edgeSubject{
		provenance: newProvenanceIn(document, sourceFile, segments...),
		node:       node,
		socket:     socket,
		alias:      alias,
	}
}

// OriginatedConnectionSubject is the subject of a check invoked once per
// connection together with the origin that drew it.
//
// It is the sibling of [EdgeSubject] and not a replacement for it. An edge's
// identity is the node, the socket and the alias; an originated connection's is
// that triple plus the surface the connection was drawn from — because the
// triple alone names up to three separately authored connections at once, and a
// rule reading a connection's AUTHORED content needs to be invoked for each of
// them rather than once for all three.
//
// So a rule over what a connection RESOLVED to declares [EdgeSubject], whose key
// is the key those facts take, and a rule over what an author WROTE on one
// declares this kind.
type OriginatedConnectionSubject interface {
	Subject

	// NodeID identifies the consumer the connection is drawn from, as every
	// keyed fact takes it.
	NodeID() string

	// OriginKind is which surface drew the connection: one of
	// [OriginComponent], [OriginAttachment] or [OriginRelease].
	OriginKind() string

	// OriginAttachment is the attachment instance name the connection was drawn
	// from, empty for every kind but [OriginAttachment].
	OriginAttachment() string

	// SocketName is the dependency socket the connection wires.
	SocketName() string

	// AliasName names the connection within its socket. It is carried whether or
	// not the author wrote it as a key, for the reason [EdgeSubject] states.
	AliasName() string

	// TargetNodeID identifies the node the connection resolves to, as every
	// keyed fact takes it. It is in the identity because two origins writing
	// one socket and one alias may name different targets, and each delivers
	// against the target it named.
	TargetNodeID() string

	originatedConnection()
}

type originatedConnectionSubject struct {
	provenance
	node       string
	kind       string
	attachment string
	socket     string
	alias      string
	target     string
}

func (originatedConnectionSubject) originatedConnection() {}

func (s originatedConnectionSubject) NodeID() string { return s.node }

func (s originatedConnectionSubject) OriginKind() string { return s.kind }

func (s originatedConnectionSubject) OriginAttachment() string { return s.attachment }

func (s originatedConnectionSubject) SocketName() string { return s.socket }

func (s originatedConnectionSubject) AliasName() string { return s.alias }

func (s originatedConnectionSubject) TargetNodeID() string { return s.target }

func newOriginatedConnectionSubject(
	sourceFile, node, kind, attachment, socket, alias, target string,
	segments ...string,
) OriginatedConnectionSubject {
	return newOriginatedConnectionSubjectWrittenIn(
		"", sourceFile, node, kind, attachment, socket, alias, target, segments...)
}

// newOriginatedConnectionSubjectWrittenIn is [newOriginatedConnectionSubject] for
// a connection whose origin's site records which authoring document it was
// written in; document is empty where it records none.
func newOriginatedConnectionSubjectWrittenIn(
	document, sourceFile, node, kind, attachment, socket, alias, target string,
	segments ...string,
) OriginatedConnectionSubject {
	return originatedConnectionSubject{
		provenance: newProvenanceIn(document, sourceFile, segments...),
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
	return newNodeAttachmentSubjectWrittenIn("", sourceFile, node, attachment, segments...)
}

// newNodeAttachmentSubjectWrittenIn is [newNodeAttachmentSubject] for an
// attachment whose site records which authoring document it was written in —
// the block the attachment was written in, a connection entry included.
func newNodeAttachmentSubjectWrittenIn(document, sourceFile, node, attachment string, segments ...string) NodeAttachmentSubject {
	return nodeAttachmentSubject{
		provenance: newProvenanceIn(document, sourceFile, segments...),
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

// AuthoredAnnotationSubject is the subject of a check invoked once per
// annotation entry an author wrote, in either authoring document.
//
// Its single coordinate is the entry, whose identity is the authoring document,
// the scope the entry sits in, the component key it was written under, the
// annotation key and the subkey inside the value, all taken together — so it
// carries an accessor for each element of that identity rather than one naming
// the entry, on the same terms [OriginatedConnectionSubject] does. No one of them
// names an entry: one annotation key is legitimately written at a scope and
// again on a component inside that scope, in both documents, with a subkey each.
//
// It is rooted at the AUTHORED SITE rather than at the component the entry takes
// effect on, and the two are not equivalent. An annotation written on a scope
// reaches every component that scope holds, so keyed by component it would be
// asked once per component about one authored string, and not at all for a scope
// holding none — which is silence a caller cannot tell from approval. Worse, the
// composition folds a Release's components into one set keyed by bare name,
// so where two scopes write against one component name every entry but one
// becomes unreachable and the rule enumerates nothing for them. Keyed here, the
// check is asked exactly once per entry the author wrote, and the locator names
// the site they can edit.
//
// It is one kind over both authoring documents rather than one per document,
// because the coordinate is the same site in either: an annotation entry inside
// a scope, or inside a component of one. Which document it was written in is
// carried as the coordinate's own value, which is what lets one condition cover
// both and still name the scope nesting the author typed — and what keeps a
// rule about the authoring side askable at all, since that side IS the document.
//
// The document is a separate accessor from the scope rather than a prefix inside
// it. The two documents compose their scope paths with separators of their own,
// so a single joined value would rest on those separators staying different,
// with no symbol comparing them and nothing to fail the day either moved.
type AuthoredAnnotationSubject interface {
	Subject

	// AuthoringDocumentKind is the document the entry was written in: one of
	// [AuthoringDocumentAggregate] or [AuthoringDocumentRelease]. It is what
	// tells a rule which authoring side wrote the value, which no other
	// coordinate says.
	AuthoringDocumentKind() string

	// AnnotationScopePath is the scope the entry sits in, as every keyed fact
	// takes it. It is the scope's path from its own document's scope set rather
	// than a bare name, because both documents' scopes nest and two at different
	// depths may carry one name. It is empty for an entry written at the
	// document's top level, which is not a scope any file spells.
	AnnotationScopePath() string

	// AnnotationComponentName is the component key the entry was written under,
	// empty for an entry written scope-wide.
	//
	// The empty case is a shape a component-rooted key cannot express rather
	// than an edge of it: a scope-wide entry takes effect on every component the
	// scope holds and none of those is a place anyone can edit, so the scope's
	// own block is the site and it names no component.
	AnnotationComponentName() string

	// AnnotationKey is the annotation key the author wrote the entry under.
	AnnotationKey() string

	// AnnotationSubkey is the key written inside a structured annotation value,
	// empty for a value written bare. It contributes no locator segment: a
	// subkey is a dotted route into the Feature the binding targets and may be
	// one authored key carrying the separator, so a locator built from it could
	// name a path the file does not contain.
	AnnotationSubkey() string

	authoredAnnotation()
}

type authoredAnnotationSubject struct {
	provenance
	document  string
	scope     string
	component string
	key       string
	subkey    string
}

func (authoredAnnotationSubject) authoredAnnotation() {}

func (s authoredAnnotationSubject) AuthoringDocumentKind() string { return s.document }

func (s authoredAnnotationSubject) AnnotationScopePath() string { return s.scope }

func (s authoredAnnotationSubject) AnnotationComponentName() string { return s.component }

func (s authoredAnnotationSubject) AnnotationKey() string { return s.key }

func (s authoredAnnotationSubject) AnnotationSubkey() string { return s.subkey }

func newAuthoredAnnotationSubject(
	sourceFile, document, scope, component, key, subkey string,
	segments ...string,
) AuthoredAnnotationSubject {
	return authoredAnnotationSubject{
		provenance: newProvenance(sourceFile, segments...),
		document:   document,
		scope:      scope,
		component:  component,
		key:        key,
		subkey:     subkey,
	}
}

// BoundaryPlacementSubject is the subject of a check invoked once per thing a
// Release's boundary places: a component it names, a component named inside a
// routing entry, or a whole sub-module it routes.
//
// Its single coordinate is the placement, whose identity is the authoring
// document, the boundary it sits in, what it addresses and the block it was
// written in, all taken together — so it carries an accessor for each element of
// that identity rather than one naming the placement, on the same terms
// [AuthoredAnnotationSubject] and [OriginatedConnectionSubject] do.
//
// No shorter tuple names a placement, and the reason is the fault this kind
// exists to see. One boundary can name a component directly under the id it
// carries once the composition is flattened and reach the same component again
// through a routing entry — two entries the author wrote at two keys, addressing
// one component — and a routed sub-module's dotted prefix can equal a component's
// id. So the axis is in the identity rather than beside it.
//
// It is rooted at the AUTHORED ENTRY rather than at the component the entry
// addresses, and that is what makes it admissible at all. The component-keyed
// coordinate [BoundaryComponentReferenceSubject] carries is de-duplicated by
// construction — its own declaration says one id names exactly one entry across
// the whole document — so a rule over a document that declares one twice could
// not be invoked for both entries, and the second would be silence a caller
// cannot tell from approval.
//
// It carries the authoring document even though a Release's boundaries are the
// only scope that places anything today. The value is not decoration: the locator
// assembly selects the scope nesting from it through the closed pair every other
// authored-site traversal selects through, so hard-coding the boundary nesting
// here would be a second statement of the document-to-nesting correspondence with
// nothing comparing the two.
type BoundaryPlacementSubject interface {
	Subject

	// AuthoringDocumentKind is the document the placement was written in: one of
	// [AuthoringDocumentAggregate] or [AuthoringDocumentRelease].
	AuthoringDocumentKind() string

	// BoundaryPath is the boundary the placement was written in, as every keyed
	// fact takes it. It is the boundary's path from its own document's scope set
	// rather than a bare name, because boundaries nest and two at different
	// depths may carry one name. It is empty for a placement inside the boundary
	// normalization synthesized to hold entries written at the document's top
	// level, which is not a boundary any file spells.
	BoundaryPath() string

	// PlacementReference is what the entry addresses: the id a component entry
	// names in the flattened composition, or the dotted prefix of a routed
	// sub-module.
	//
	// The suffix says the value is a reference rather than a component id for the
	// reason [BoundaryComponentReferenceSubject.ComponentReference] states — the
	// axis is what a Release ASKED FOR, and an entry addressing something
	// nothing answers to is enumerated exactly as one that resolves is.
	PlacementReference() string

	// PlacementAxis is the block the entry was written in: one of
	// [PlacementNamedComponent], [PlacementRoutedComponent] or
	// [PlacementRoutedModule].
	PlacementAxis() string

	boundaryPlacement()
}

type boundaryPlacementSubject struct {
	provenance
	document  string
	boundary  string
	reference string
	axis      string
}

func (boundaryPlacementSubject) boundaryPlacement() {}

func (s boundaryPlacementSubject) AuthoringDocumentKind() string { return s.document }

func (s boundaryPlacementSubject) BoundaryPath() string { return s.boundary }

func (s boundaryPlacementSubject) PlacementReference() string { return s.reference }

func (s boundaryPlacementSubject) PlacementAxis() string { return s.axis }

func newBoundaryPlacementSubject(
	sourceFile, document, boundary, reference, axis string,
	segments ...string,
) BoundaryPlacementSubject {
	return boundaryPlacementSubject{
		provenance: newProvenance(sourceFile, segments...),
		document:   document,
		boundary:   boundary,
		reference:  reference,
		axis:       axis,
	}
}

// ReleaseEdgeConfigurationSubject is the subject of a check invoked once per
// entry the Release's per-edge `uses` surface writes.
//
// It is a separate kind from the connection rather than a narrowing of it, and
// the difference is what the kind exists for. The connection axis enumerates one
// instance per connection the composition DREW, whichever surface drew it; this
// one enumerates one instance per entry the Release WROTE. The two sets overlap
// and neither contains the other: an entry that configures a connection some
// other document drew appears in both, a connection an Aggregate drew and no
// Release entry configures appears only on the connection axis, and an entry
// whose connection nothing drew appears only here. A rule about what the operator
// authored, asked of the connection axis, would never be invoked for that last
// case — which is the case such a rule exists for.
//
// The coordinates carry the boundary because the Release writes this surface
// inside a boundary's own component entry, so the boundary is where an author
// edits the entry. They carry the authoring document for the reason
// [BoundaryPlacementSubject]'s do: the locator assembly selects the scope nesting
// from it through the closed pair every other authored-site traversal selects
// through, so hard-coding the boundary nesting here would be a second statement
// of the document-to-nesting correspondence with nothing comparing the two.
type ReleaseEdgeConfigurationSubject interface {
	Subject

	// AuthoringDocumentKind is the document the entry was written in: one of
	// [AuthoringDocumentAggregate] or [AuthoringDocumentRelease].
	AuthoringDocumentKind() string

	// BoundaryPath is the boundary the entry was written in, as every keyed fact
	// takes it. It is the boundary's path from its own document's scope set
	// rather than a bare name, because boundaries nest and two at different
	// depths may carry one name. It is empty for an entry inside the boundary
	// normalization synthesized to hold components written at the document's top
	// level, which is not a boundary any file spells.
	BoundaryPath() string

	// NodeID is the consuming component the entry was keyed under, by the
	// identity every keyed fact takes.
	//
	// It is what the entry ADDRESSES rather than a component the composition is
	// known to hold: an entry naming a component nothing answers to is
	// enumerated exactly as one naming a component that exists, and which it is
	// is a question the flattened node set answers.
	NodeID() string

	// SocketName is the dependency socket the entry sits under.
	SocketName() string

	// AliasName names the entry within its socket, which is the key the author
	// wrote.
	AliasName() string

	releaseEdgeConfiguration()
}

type releaseEdgeConfigurationSubject struct {
	provenance
	document string
	boundary string
	node     string
	socket   string
	alias    string
}

func (releaseEdgeConfigurationSubject) releaseEdgeConfiguration() {}

func (s releaseEdgeConfigurationSubject) AuthoringDocumentKind() string { return s.document }

func (s releaseEdgeConfigurationSubject) BoundaryPath() string { return s.boundary }

func (s releaseEdgeConfigurationSubject) NodeID() string { return s.node }

func (s releaseEdgeConfigurationSubject) SocketName() string { return s.socket }

func (s releaseEdgeConfigurationSubject) AliasName() string { return s.alias }

func newReleaseEdgeConfigurationSubject(
	sourceFile, document, boundary, node, socket, alias string,
	segments ...string,
) ReleaseEdgeConfigurationSubject {
	return releaseEdgeConfigurationSubject{
		provenance: newProvenance(sourceFile, segments...),
		document:   document,
		boundary:   boundary,
		node:       node,
		socket:     socket,
		alias:      alias,
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
	return newNodeAttachmentSocketSubjectWrittenIn("", sourceFile, node, attachment, socket, segments...)
}

// newNodeAttachmentSocketSubjectWrittenIn is [newNodeAttachmentSocketSubject] for
// a socket whose attachment's site records which authoring document it was
// written in.
func newNodeAttachmentSocketSubjectWrittenIn(
	document, sourceFile, node, attachment, socket string,
	segments ...string,
) NodeAttachmentSocketSubject {
	return nodeAttachmentSocketSubject{
		provenance: newProvenanceIn(document, sourceFile, segments...),
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
	return newNodeSlotSubjectWrittenIn("", sourceFile, node, slot, segments...)
}

// newNodeSlotSubjectWrittenIn is [newNodeSlotSubject] for a slot whose fill's
// site records which authoring document it was written in.
func newNodeSlotSubjectWrittenIn(document, sourceFile, node, slot string, segments ...string) NodeSlotSubject {
	return nodeSlotSubject{
		provenance: newProvenanceIn(document, sourceFile, segments...),
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

// NodeAttachmentLocalsKeySubject is the subject of a check invoked once per local
// the FeatureRecipe one attachment binds on a node declares.
//
// It is keyed per attachment as well as per node because the block is authored on
// the FeatureRecipe the attachment binds, a different document from the node's own
// Recipe. Two attachments on one node carry independently authored locals blocks
// whose keys are chosen with no knowledge of each other, and neither can read the
// node's own block: a base Recipe's resolved locals are merged over an
// attachment's AFTER that block has resolved, so they are not readable from inside
// it. A kind carrying only the node and the key could name none of that.
//
// Locals are evaluated in the order they are declared, so a check over this kind
// is invoked in that order and a rule about what a local may reference is a rule
// about it.
type NodeAttachmentLocalsKeySubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose FeatureRecipe declares the local.
	AttachmentName() string

	// LocalsKey is the declared local the check was invoked for: the key the
	// author wrote, which a later local in the same block references it by.
	LocalsKey() string

	nodeAttachmentLocalsKey()
}

type nodeAttachmentLocalsKeySubject struct {
	provenance
	node       string
	attachment string
	key        string
}

func (nodeAttachmentLocalsKeySubject) nodeAttachmentLocalsKey() {}

func (s nodeAttachmentLocalsKeySubject) NodeID() string { return s.node }

func (s nodeAttachmentLocalsKeySubject) AttachmentName() string { return s.attachment }

func (s nodeAttachmentLocalsKeySubject) LocalsKey() string { return s.key }

func newNodeAttachmentLocalsKeySubject(
	sourceFile, node, attachment, key string,
	segments ...string,
) NodeAttachmentLocalsKeySubject {
	return nodeAttachmentLocalsKeySubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
		key:        key,
	}
}

// BoundaryAttachmentLocalsKeySubject is the subject of a check invoked once per
// local the FeatureRecipe one boundary-scope attachment binds declares.
//
// It is rooted at a boundary rather than at a node because that is where the
// attachment is declared. Such an attachment is written once and reaches every
// component the boundary holds, so a node-rooted walk would ask about one authored
// block once per component — the same authored text presented as many times as the
// boundary has members — and would ask about it not at all for a boundary holding
// none, which is silence a caller cannot tell from approval.
//
// The keys in that block are chosen independently of any component's, so this is a
// separate kind rather than the node-rooted one reached differently: no
// node-rooted key can name a local authored against a boundary.
type BoundaryAttachmentLocalsKeySubject interface {
	Subject

	// BoundaryPath is the boundary the attachment was declared on, as every keyed
	// fact takes it. It is the scope's path from the document's own scope set
	// rather than a bare name, because boundaries nest and two scopes at
	// different depths may carry one name.
	BoundaryPath() string

	// AttachmentName is the attachment whose FeatureRecipe declares the local:
	// the name the operator wrote it under.
	AttachmentName() string

	// LocalsKey is the declared local the check was invoked for: the key the
	// author wrote, which a later local in the same block references it by.
	LocalsKey() string

	boundaryAttachmentLocalsKey()
}

type boundaryAttachmentLocalsKeySubject struct {
	provenance
	boundary   string
	attachment string
	key        string
}

func (boundaryAttachmentLocalsKeySubject) boundaryAttachmentLocalsKey() {}

func (s boundaryAttachmentLocalsKeySubject) BoundaryPath() string { return s.boundary }

func (s boundaryAttachmentLocalsKeySubject) AttachmentName() string { return s.attachment }

func (s boundaryAttachmentLocalsKeySubject) LocalsKey() string { return s.key }

func newBoundaryAttachmentLocalsKeySubject(
	sourceFile, boundary, attachment, key string,
	segments ...string,
) BoundaryAttachmentLocalsKeySubject {
	return boundaryAttachmentLocalsKeySubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		attachment: attachment,
		key:        key,
	}
}

// NodeTemplateFieldSubject is the subject of a check invoked once per inline
// template field one node's own Recipe writes.
//
// The innermost coordinate is the AUTHORED FIELD LABEL, on the reading the
// boundary-rooted form below sets out: a Recipe carries several such fields and
// gains more over time, so a kind per field would put the field identity in
// structure and a field added later would be silently ungated if any of its four
// declarations were forgotten.
//
// One reference set serves the whole carrier, which is the condition that makes
// the family pay here: every field this kind reaches is evaluated against the
// node's own render context, one type, so what a template on any of them may name
// is one question with one answer. The fields differ in the delimiter pair their
// text is parsed with rather than in what they may name, and that difference is a
// property of the field the reading beside the enumeration answers.
//
// It is rooted at the node because that is what selects the document: the fields
// are written in the Recipe the node binds, so a node is the coordinate that says
// which Recipe's text is being asked about.
type NodeTemplateFieldSubject interface {
	Subject

	// NodeID identifies the node whose Recipe writes the field, as every keyed
	// fact takes it.
	NodeID() string

	// TemplateField is the inline template field the check was invoked for,
	// labelled the way an author reads it back — the authored key, carrying the
	// entry's own key or the step's id where the block is keyed.
	TemplateField() string

	nodeTemplateField()
}

type nodeTemplateFieldSubject struct {
	provenance
	node  string
	field string
}

func (nodeTemplateFieldSubject) nodeTemplateField() {}

func (s nodeTemplateFieldSubject) NodeID() string { return s.node }

func (s nodeTemplateFieldSubject) TemplateField() string { return s.field }

func newNodeTemplateFieldSubject(
	sourceFile, node, field string,
	segments ...string,
) NodeTemplateFieldSubject {
	return nodeTemplateFieldSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		field:      field,
	}
}

// NodeAttachmentTemplateFieldSubject is the subject of a check invoked once per
// inline template field the FeatureRecipe one attachment binds on a node writes.
//
// The innermost coordinate is the AUTHORED FIELD LABEL, on the reading the two
// sibling template-field kinds set out: a FeatureRecipe carries several such
// fields and gains more over time, so a kind per field would put the field
// identity in structure and a field added later would be silently ungated if any
// of its four declarations were forgotten.
//
// It carries BOTH the node and the attachment, and neither is redundant. The
// attachment says which FeatureRecipe's text is being asked about; the node says
// which render context that text is judged against, because a per-component
// attachment is evaluated once for each component it reaches and its context is
// derived from that component's. A kind carrying only the attachment could name
// the document and not the surface.
//
// One reference set serves the whole carrier, which is the condition that makes
// this family pay: every field this kind reaches is evaluated against the
// attachment's own render context, one type, so what a template on any of them may
// name is one question with one answer. The fields differ in the delimiter pair
// their text is parsed with rather than in what they may name, and that difference
// is a property of the field the reading beside the enumeration answers.
type NodeAttachmentTemplateFieldSubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose FeatureRecipe writes the field.
	AttachmentName() string

	// TemplateField is the inline template field the check was invoked for,
	// labelled the way an author reads it back — the authored key, carrying the
	// entry's own key or the step's id where the block is keyed.
	TemplateField() string

	nodeAttachmentTemplateField()
}

type nodeAttachmentTemplateFieldSubject struct {
	provenance
	node       string
	attachment string
	field      string
}

func (nodeAttachmentTemplateFieldSubject) nodeAttachmentTemplateField() {}

func (s nodeAttachmentTemplateFieldSubject) NodeID() string { return s.node }

func (s nodeAttachmentTemplateFieldSubject) AttachmentName() string { return s.attachment }

func (s nodeAttachmentTemplateFieldSubject) TemplateField() string { return s.field }

func newNodeAttachmentTemplateFieldSubject(
	sourceFile, node, attachment, field string,
	segments ...string,
) NodeAttachmentTemplateFieldSubject {
	return nodeAttachmentTemplateFieldSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
		field:      field,
	}
}

// BoundaryAttachmentTemplateFieldSubject is the subject of a check invoked once
// per inline template field the FeatureRecipe one attachment declared on a
// boundary writes.
//
// The innermost coordinate is the AUTHORED FIELD LABEL, and that is what makes
// the kind a family rather than one kind per template-bearing field. A
// FeatureRecipe carries several such fields and gains more over time; a kind per
// field would put the field identity in structure, so a field added later would
// need its own kind, fact, traversal entry and declaration — and would be
// silently ungated if any of the four were forgotten. Keyed by the label, the
// field axis is data, and what a check covers is enumerable rather than a count
// of how many declarations somebody remembered to write.
//
// The kind pays where ONE reference set serves the whole carrier, and that is the
// condition to check before reaching for it again. Within one carrier the render
// context is one type, so what a template on any of these fields may name is one
// question with one answer. A carrier whose fields are evaluated against several
// context types has no such single answer, and there the mapping is necessarily
// keyed per field — which is the per-surface granularity this shape exists to
// avoid, wearing the family's clothes.
//
// It is rooted at a boundary because that is where such an attachment is
// declared: it is written once and reaches every component the boundary holds,
// so a node-rooted walk would present one authored document once per component
// and not at all for a boundary holding none.
type BoundaryAttachmentTemplateFieldSubject interface {
	Subject

	// BoundaryPath is the boundary the attachment was declared on, as every keyed
	// fact takes it. It is the scope's path from the document's own scope set
	// rather than a bare name, because boundaries nest and two scopes at
	// different depths may carry one name.
	BoundaryPath() string

	// AttachmentName is the attachment whose FeatureRecipe writes the field: the
	// name the operator declared it under.
	AttachmentName() string

	// TemplateField is the inline template field the check was invoked for,
	// labelled the way an author reads it back — the authored key, carrying the
	// entry's own key or the step's id where the block is keyed.
	TemplateField() string

	boundaryAttachmentTemplateField()
}

type boundaryAttachmentTemplateFieldSubject struct {
	provenance
	boundary   string
	attachment string
	field      string
}

func (boundaryAttachmentTemplateFieldSubject) boundaryAttachmentTemplateField() {}

func (s boundaryAttachmentTemplateFieldSubject) BoundaryPath() string { return s.boundary }

func (s boundaryAttachmentTemplateFieldSubject) AttachmentName() string { return s.attachment }

func (s boundaryAttachmentTemplateFieldSubject) TemplateField() string { return s.field }

func newBoundaryAttachmentTemplateFieldSubject(
	sourceFile, boundary, attachment, field string,
	segments ...string,
) BoundaryAttachmentTemplateFieldSubject {
	return boundaryAttachmentTemplateFieldSubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		attachment: attachment,
		field:      field,
	}
}

// BoundaryPathTemplateFieldSubject is the subject of a check invoked once per
// path template one boundary of a Release declares.
//
// The innermost coordinate is the AUTHORED FIELD the template was written under,
// which is what makes the kind a family rather than one kind per path-bearing
// field. A boundary writes several such fields and may gain more; a kind per
// field would put the field identity in structure, so a field added later would
// need its own kind, fact, traversal entry and declaration — and would be
// silently ungated if any of the four were forgotten. Keyed by the field, the
// axis is data, and what a check covers is enumerable rather than a count of how
// many declarations somebody remembered to write.
//
// # This carrier is the family's degenerate case, and saying so is the point
//
// The shape pays most where ONE reference set serves a whole carrier. Here it
// does not: the two fields are evaluated against one context type through two
// different admitted sets, because a whole-boundary template is evaluated once
// for the scope while a per-component segment is evaluated once for each
// component placed in it. So the mapping is necessarily keyed per field, and what
// the family buys on this carrier is enumerable coverage alone. The innermost
// coordinate is named for the path templates specifically rather than for
// template fields generally, so that difference is legible on the enumeration a
// reader reads coverage off.
//
// It is rooted at a boundary because that is where such a template is declared:
// it is written once on the scope and reaches every component the scope holds, so
// a node-rooted walk would present one authored field once per component and not
// at all for a boundary holding none.
type BoundaryPathTemplateFieldSubject interface {
	Subject

	// BoundaryPath is the boundary the template was declared on, as every keyed
	// fact takes it. It is the scope's path from the document's own scope set
	// rather than a bare name, because boundaries nest and two scopes at
	// different depths may carry one name.
	BoundaryPath() string

	// PathTemplateField is the authored field the check was invoked for, spelled
	// the way the Release document writes it.
	PathTemplateField() string

	boundaryPathTemplateField()
}

type boundaryPathTemplateFieldSubject struct {
	provenance
	boundary string
	field    string
}

func (boundaryPathTemplateFieldSubject) boundaryPathTemplateField() {}

func (s boundaryPathTemplateFieldSubject) BoundaryPath() string { return s.boundary }

func (s boundaryPathTemplateFieldSubject) PathTemplateField() string { return s.field }

func newBoundaryPathTemplateFieldSubject(
	sourceFile, boundary, field string,
	segments ...string,
) BoundaryPathTemplateFieldSubject {
	return boundaryPathTemplateFieldSubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		field:      field,
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

// NodePathSubject is the subject of a check invoked once per output name one
// node's own render emitted, before anything is grouped for assembly.
//
// It is rooted at a node because that is the scope an emission is complete in: a
// Recipe's source, its workload, its artifacts entries and its pipeline steps all
// emit under names chosen independently of one another, and nothing declares
// those names distinct. So the node is what holds a whole emission, and the
// output name is what several of its blocks can meet on.
//
// It is a different kind from the one keyed by a boundary, a provider and a path,
// and neither reaches the other's coordinates. This one is one node's own output
// under the names that node chose; that one is what a group assembled, under
// paths an assembly may have renamed, nested or merged away — so a rule about an
// emission two blocks of one Recipe collided in cannot be asked there, and a rule
// about two providers meeting in one directory cannot be asked here.
type NodePathSubject interface {
	Subject

	// NodeID identifies the node whose own render emitted the file, as every
	// keyed fact takes it.
	NodeID() string

	// EmittedPath is the output name the check was invoked for: the name the
	// node's own render placed the file under, before any assembly moved it. It
	// contributes no locator segment, because it is output rather than something
	// an author wrote — so a check naming a particular file reads it from here.
	EmittedPath() string

	nodePath()
}

type nodePathSubject struct {
	provenance
	node string
	path string
}

func (nodePathSubject) nodePath() {}

func (s nodePathSubject) NodeID() string { return s.node }

func (s nodePathSubject) EmittedPath() string { return s.path }

func newNodePathSubject(sourceFile, node, path string, segments ...string) NodePathSubject {
	return nodePathSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		path:       path,
	}
}

// NodeAttachmentPathSubject is the subject of a check invoked once per output name
// the render one attachment carries on a node emitted, before anything is grouped
// for assembly.
//
// It is the carrier-keyed form of [NodePathSubject] and exists for the reason that
// kind's own documentation gives for being rooted where it is, applied to a
// different document: the blocks that can meet on a name here are the FeatureRecipe
// the attachment binds, and its source, workload, artifacts entries and pipeline
// steps all emit under names chosen independently of one another.
//
// It is keyed per attachment as well as per node because the render runs under the
// HOST component's node identity over blocks the FeatureRecipe declares. A kind
// carrying only the node and the name would therefore be asked about the host's own
// emission while the blocks in question are the attachment's — admitting a collision
// the attachment never wrote wherever the host has one, and refusing none of the
// attachment's own. Two attachments on one node are independently authored, and
// nothing makes one's output names distinct from the other's.
type NodeAttachmentPathSubject interface {
	Subject

	// NodeID identifies the node carrying the attachment, as every keyed fact
	// takes it.
	NodeID() string

	// AttachmentName is the attachment whose FeatureRecipe emitted the file.
	AttachmentName() string

	// EmittedPath is the output name the check was invoked for: the name the
	// attachment's own render placed the file under, before the caller composed
	// the attachment's prefix onto it. It contributes no locator segment, because
	// it is output rather than something an author wrote.
	EmittedPath() string

	nodeAttachmentPath()
}

type nodeAttachmentPathSubject struct {
	provenance
	node       string
	attachment string
	path       string
}

func (nodeAttachmentPathSubject) nodeAttachmentPath() {}

func (s nodeAttachmentPathSubject) NodeID() string { return s.node }

func (s nodeAttachmentPathSubject) AttachmentName() string { return s.attachment }

func (s nodeAttachmentPathSubject) EmittedPath() string { return s.path }

func newNodeAttachmentPathSubject(
	sourceFile, node, attachment, path string,
	segments ...string,
) NodeAttachmentPathSubject {
	return nodeAttachmentPathSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		attachment: attachment,
		path:       path,
	}
}

// BoundaryAttachmentPathSubject is the subject of a check invoked once per output
// name the render one boundary-scope attachment carries emitted, before anything is
// grouped for assembly.
//
// It is rooted at a boundary rather than at a node because that is where such an
// attachment is declared. It is written once and reaches every component the
// boundary holds, so a node-rooted walk would ask about one authored document once
// per component and not at all for a boundary holding none — silence a caller cannot
// tell from approval. The render carries a composite dispatch marker rather than any
// component's node identity, so no node-rooted key can name it.
//
// The output names in that render are chosen independently of any component's and of
// any other attachment's, so this is a separate kind rather than either sibling
// reached differently.
type BoundaryAttachmentPathSubject interface {
	Subject

	// BoundaryPath is the boundary the attachment was declared on, as every keyed
	// fact takes it. It is the scope's path from the document's own scope set
	// rather than a bare name, because boundaries nest and two scopes at different
	// depths may carry one name.
	BoundaryPath() string

	// AttachmentName is the attachment whose FeatureRecipe emitted the file: the
	// name the operator wrote it under.
	AttachmentName() string

	// EmittedPath is the output name the check was invoked for: the name that
	// render placed the file under, before the caller routed it by the flag saying
	// whether an assembly consumes it. It contributes no locator segment, because
	// it is output rather than something an author wrote.
	EmittedPath() string

	boundaryAttachmentPath()
}

type boundaryAttachmentPathSubject struct {
	provenance
	boundary   string
	attachment string
	path       string
}

func (boundaryAttachmentPathSubject) boundaryAttachmentPath() {}

func (s boundaryAttachmentPathSubject) BoundaryPath() string { return s.boundary }

func (s boundaryAttachmentPathSubject) AttachmentName() string { return s.attachment }

func (s boundaryAttachmentPathSubject) EmittedPath() string { return s.path }

func newBoundaryAttachmentPathSubject(
	sourceFile, boundary, attachment, path string,
	segments ...string,
) BoundaryAttachmentPathSubject {
	return boundaryAttachmentPathSubject{
		provenance: newProvenance(sourceFile, segments...),
		boundary:   boundary,
		attachment: attachment,
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
// the Release document and the empty locator, which routes a reader to
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

// NodeArtifactsEntrySubject is the subject of a check invoked once per entry a
// node's own Recipe writes in its artifacts block, named by the key the author
// wrote rather than by anything that key resolves to.
//
// # Why it is a separate kind from the artifact it resolves to
//
// [NodeArtifactSubject] is keyed by the OUTPUT name, which for an entry importing
// a library bundle is one name per file that bundle holds — so those coordinates
// exist only once the import has succeeded, and a check keyed by them is invoked
// no times at all for an entry whose import failed. That is precisely the input a
// rule about the import itself exists to refuse, and a rule invoked zero times
// reports exactly what a rule that ran and approved reports.
//
// This kind is keyed by what the author typed, which is there from the moment the
// Recipe is bound and does not depend on the resolution the rules over it judge.
// The two kinds therefore coincide for an inline entry and for one supplied by a
// file of the workspace, where the authored key IS the output name, and diverge
// for exactly the authoring form the rules over this kind govern. Keying one kind
// both ways would leave every fact declared over it answering about whichever
// reading was asked last.
//
// # What the locator names, and what the rule owes because of it
//
// A finding is positioned at .spec.artifacts.<key> in the Recipe's own file. An
// authored artifacts key is the output's filename, so it ordinarily carries the
// locator dialect's separator as ONE key the dialect cannot address, and the
// locator then names the artifacts block and stops — the same bound
// [RunOnNodeArtifacts] carries and for the same reason, since the truncation is a
// property of what an artifacts key IS rather than of when the key is read. So a
// rule over this kind owes what a rule over that one owes: it names the entry in
// its detail, CAPTIONED, because the block says where to look and only the detail
// can say which entry inside it.
type NodeArtifactsEntrySubject interface {
	Subject

	// NodeID identifies the node whose Recipe writes the entry, as every keyed
	// fact takes it.
	NodeID() string

	// ArtifactsEntryKey is the artifacts key the check was invoked for: the key
	// the author wrote, before anything resolves what it stands for.
	ArtifactsEntryKey() string

	nodeArtifactsEntry()
}

type nodeArtifactsEntrySubject struct {
	provenance
	node  string
	entry string
}

func (nodeArtifactsEntrySubject) nodeArtifactsEntry() {}

func (s nodeArtifactsEntrySubject) NodeID() string { return s.node }

func (s nodeArtifactsEntrySubject) ArtifactsEntryKey() string { return s.entry }

func newNodeArtifactsEntrySubject(
	sourceFile, node, entry string, segments ...string,
) NodeArtifactsEntrySubject {
	return nodeArtifactsEntrySubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		entry:      entry,
	}
}

// NodeRecipeImportFieldSubject is the subject of a check invoked once per
// spec-root field a node's own Recipe supplies by `$import:`, named by the key
// the author wrote.
//
// # Why it is a separate kind from the template field beside it
//
// [NodeTemplateFieldSubject] is keyed by a field carrying template text the
// author wrote in this document, and a field supplied by an import carries none:
// the content arrives when the render resolves the reference. So an imported
// field contributes no instance of that kind at all, and a check keyed by it is
// invoked no times for exactly the fields a rule about the reference exists to
// refuse — which reports what a rule that ran and approved reports.
//
// This kind is keyed by the field name, which is there from the moment the Recipe
// is bound and does not depend on the resolution the rules over it judge. The two
// kinds are disjoint rather than overlapping, because the authoring surface admits
// no field that is both written inline and imported.
//
// # What the locator names
//
// A finding is positioned at .spec.<field> in the Recipe's own file. The field
// names are the Recipe's own spec-root keys, which the authoring surface fixes and
// which carry no separator, so the locator is exact rather than truncated — and a
// rule over this kind therefore owes no captioned stand-in for a position it could
// not reach.
//
// The Recipe is what the locator names rather than the Aggregate binding the node,
// because the fault is in the Recipe's text: a node key spliced onto it would point
// an author at one instance when the edit is in the entry every instance shares.
type NodeRecipeImportFieldSubject interface {
	Subject

	// NodeID identifies the node whose Recipe supplies the field, as every keyed
	// fact takes it.
	NodeID() string

	// RecipeImportField is the Recipe spec-root key the check was invoked for:
	// the key the author wrote the `$import:` under.
	RecipeImportField() string

	nodeRecipeImportField()
}

type nodeRecipeImportFieldSubject struct {
	provenance
	node  string
	field string
}

func (nodeRecipeImportFieldSubject) nodeRecipeImportField() {}

func (s nodeRecipeImportFieldSubject) NodeID() string { return s.node }

func (s nodeRecipeImportFieldSubject) RecipeImportField() string { return s.field }

func newNodeRecipeImportFieldSubject(
	sourceFile, node, field string, segments ...string,
) NodeRecipeImportFieldSubject {
	return nodeRecipeImportFieldSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		field:      field,
	}
}

// NodeRecipeSourceFieldSubject is the subject of a check invoked once per source
// entry a node's own Recipe declares a position for, named by the spec-root key
// that position is.
//
// # Why the kind's content is a position rather than a name
//
// Every other Recipe-rooted kind is keyed by something the author chose — a
// provides key, a local, a step id, an artifacts key, the field an `$import:` was
// written under. This one is keyed by a position the authoring surface fixes:
// exactly one source entry exists per Recipe, whether or not anything was written
// at it. So the coordinate's value is the same key on every instance, and the
// instance count is the bound-Recipe count rather than anything the document
// declares.
//
// That is what the kind is for. A rule about an entry the author may have left
// behind empty has to be invoked where the entry WOULD be, and a kind keyed by
// what the author named cannot reach a position they named nothing at.
//
// # Why it is a separate kind from the import field beside it
//
// [NodeRecipeImportFieldSubject] is keyed by a spec-root field supplied by
// `$import:`, and a source entry carries a path rather than an import — so it is
// absent from that axis by construction. Widening that axis to admit it would make
// it mean one rule rather than the document, which its own enumeration refuses in
// terms.
//
// # Why it is a separate kind from the node
//
// [NodeSubject] is the component instance, and its locator names the Aggregate the
// instance was authored in. A source entry is written in the Recipe, so a finding
// keyed by the node would send an author to a document that holds no source entry
// at all — and to a position inside it that names the instance rather than the
// entry every instance shares.
//
// # What the locator names
//
// A finding is positioned at .spec.source in the Recipe's own file. The key is the
// authoring surface's own and carries no separator, so the locator is exact rather
// than truncated, and a rule over this kind owes no captioned stand-in for a
// position it could not reach.
//
// The node contributes no segment, on the reading every node-rooted kind whose
// locator names a Recipe carries: the fault is in the Recipe's text rather than in
// any one node that binds it, so a node key spliced onto a Recipe-rooted locator
// would point an author at an instance when the edit is in the entry every instance
// shares.
type NodeRecipeSourceFieldSubject interface {
	Subject

	// NodeID identifies the node whose Recipe declares the position, as every
	// keyed fact takes it.
	NodeID() string

	// RecipeSourceField is the Recipe spec-root key the check was invoked for:
	// the key a source entry is written under.
	RecipeSourceField() string

	nodeRecipeSourceField()
}

type nodeRecipeSourceFieldSubject struct {
	provenance
	node  string
	field string
}

func (nodeRecipeSourceFieldSubject) nodeRecipeSourceField() {}

func (s nodeRecipeSourceFieldSubject) NodeID() string { return s.node }

func (s nodeRecipeSourceFieldSubject) RecipeSourceField() string { return s.field }

func newNodeRecipeSourceFieldSubject(
	sourceFile, node, field string, segments ...string,
) NodeRecipeSourceFieldSubject {
	return nodeRecipeSourceFieldSubject{
		provenance: newProvenance(sourceFile, segments...),
		node:       node,
		field:      field,
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
// a rule over what the Release did with a module would be asked once per
// component, would report one authoring fault as many, and would name a
// component where the author's remedy is a single entry — and it would be asked
// no times at all for a module contributing nothing, which is silence a caller
// cannot tell from approval. Keyed here it is asked exactly once per module the
// composition brought in, whatever that module holds.
//
// Its provenance is the document that DECLARES the module, which is the parent's
// document rather than the module's own: the root Aggregate for a module the
// Aggregate imports directly, and the importing module's document for one
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

// BoundaryComponentReferenceSubject is the subject of a check invoked once per
// component entry the Release's boundaries declare.
//
// The coordinate is the id the entry addresses in the flattened composition, and
// it is one coordinate rather than an entry nested under its boundary because the
// id is the entry's identity: one id declared twice is refused when the Release
// document loads, so an id names exactly one entry across the whole document. The
// boundary reaches the locator as provenance and keys nothing, which is the
// division the connection kinds already draw for the surface that drew a
// connection.
//
// # What the coordinate is, and what it deliberately is not
//
// It is what the Release ASKED FOR, never what the composition holds. A rule
// over this kind is invoked for an entry addressing an id nothing answers to
// exactly as it is for one that resolves, because the Release wrote both — and
// an axis carrying only the entries that resolve would never present such a rule
// its own subject, the offending entry being by construction the one that is
// absent from the resolved set.
//
// The two axes a boundary names components on are already reconciled in the
// coordinate. One names the component under the id it carries once the
// composition is flattened; the other routes a sub-module and names a component
// inside it under the local name it carries there. Both arrive here as the id,
// so a rule cannot be written against one axis and quietly ignore the other, and
// none has to know a second axis exists.
//
// # The locator, and where the key reaches the author instead
//
// An entry the boundary names directly is located at the boundary's own component
// block. The id the author typed there is the flattened one, which carries the
// locator dialect's separator for any component a sub-module contributed — so the
// locator names the block and stops, and the id reaches the author as the value
// the rule raises. That is not a loss of the key: a locator is a position, and a
// reader given the block is a reader who can find the entry inside it.
//
// An entry a routing entry declared is located at the local name the author typed
// inside that entry, which carries no separator, so its locator spells the whole
// path. A rule raising about such an entry owes the id in its detail all the same,
// because the id is what the entry addresses and the local name alone does not say
// what the composition was asked for.
type BoundaryComponentReferenceSubject interface {
	Subject

	// ComponentReference is the id the entry addresses in the flattened
	// composition, as every keyed fact takes it. For an entry a routing entry
	// declared it is the local name resolved through the routed module's own
	// prefix, and not the local name.
	ComponentReference() string

	boundaryComponentReference()
}

type boundaryComponentReferenceSubject struct {
	provenance
	reference string
}

func (boundaryComponentReferenceSubject) boundaryComponentReference() {}

func (s boundaryComponentReferenceSubject) ComponentReference() string { return s.reference }

func newBoundaryComponentReferenceSubject(
	sourceFile, reference string,
	segments ...string,
) BoundaryComponentReferenceSubject {
	return boundaryComponentReferenceSubject{
		provenance: newProvenance(sourceFile, segments...),
		reference:  reference,
	}
}
