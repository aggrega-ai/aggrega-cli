// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// The enumerator surface: what the framework needs enumerated, declared here and
// supplied by the caller.
//
// The framework owns traversal — a check is invoked once per instance of its
// declared subject, and a check never writes that loop. But the framework does
// not reach into the compilation to obtain an instance set. Each traversable
// coordinate is an interface declared here and implemented by the caller that
// holds the compilation product, so this package depends on nothing but the
// shared type layer and the caller supplies the how. That is the same shape the
// views already take, which is why there is one pattern here rather than two.
//
// # An enumerator speaks in coordinate values, never in a compilation product
//
// Every fact is keyed by strings — a node identifier, a socket name, a pillar
// name — so an enumerator hands back those strings and the product carrying
// them stays on the implementor's side of the interface. That is what lets one
// traversal serve a coordinate whose product is a different type at different
// points in the compilation: the traversal never names the product.
//
// No enumerator takes or returns an authoring document, and none may be given
// one. A view exposes named facts and never a raw document, and an enumerator
// handing out a document would reopen from the traversal side exactly what the
// view closes from the check side.
//
// # Order is the implementor's declared promise, and it is load-bearing
//
// Output is byte-identical for identical input, and enumeration order reaches
// output: a check invoked once per instance raises in the order it is invoked,
// so the order instances arrive in is the order diagnostics appear in.
//
// The accessor layer avoids this obligation by refusing to enumerate at all —
// a keyed lookup iterates nothing. An enumerator cannot avoid it, so it carries
// it explicitly: every implementation returns instances in an order derived
// from something ordered. Author order, taken from an ordered map, is the
// right answer wherever the authored document has one, because several facts
// declare author order as the fact itself rather than as an incidental property
// of it. A name sort is acceptable where the carrier genuinely holds no order.
// Ranging a plain Go map is never acceptable: it randomises per run, and the
// result is output that differs between two builds of the same input.
//
// The framework does not sort. It preserves the order each enumerator returns
// and nests outer coordinates before inner ones, which is stated per traversal.
// Sorting here would discard the author order the facts declare.
//
// # Where an axis is also named by a fact
//
// Six axes are named by a fact as well as by an enumerator — the attachment
// names, an attachment's own socket names, the injection mechanisms, one
// mechanism's delivery keys, the declared locals keys, and the emitted path
// set. The two are not two routes to one question from a check's point of view:
// a check reads the fact to decide its predicate and cannot reach an enumerator
// at all. They are two suppliers on the *implementor's* side, and the near
// duplication is deliberate rather than an oversight, so nobody should
// "unify" it by routing a check at an enumerator. Implement such an enumerator
// *from* the fact rather than beside it, and the two cannot drift.

// NodeInstance is one node the framework enumerates, together with the
// provenance of the component instance it stands for.
//
// The identifier and the authored name are different values and the difference
// is load-bearing. ID is what the keyed facts take, and it records how the node
// was reached — a node flattened out of a sub-aggregate carries a qualified
// identifier. AuthoredName is what the author typed. A locator is built from
// what the author typed, so the two cannot be collapsed.
type NodeInstance struct {
	// ID identifies the node to every keyed fact.
	ID string

	// SourceFile is the workspace-relative file the component instance was
	// authored in, empty when it has no single-file origin.
	SourceFile string

	// AuthoredName is the component name the author wrote.
	AuthoredName string

	// SubsystemPath is the subsystem path the node was reached through, empty
	// for a node authored at the top level of its document.
	SubsystemPath string

	// SynthesizedSubsystem reports whether that subsystem was synthesized by
	// normalization rather than declared by the author. It discriminates the
	// two cases the locator rule turns on, so a subsystem the author declares
	// and happens to name the same as the synthesized one is not mistaken for
	// it.
	SynthesizedSubsystem bool
}

// EdgeInstance is one connection: which node's which socket, under which alias.
//
// The triple is the edge's identity rather than a nesting. The derived kind
// carries one coordinate, and the fact keyed by it takes the node, the socket
// and the alias together as its key — so an edge is enumerated as one instance,
// and the enumerator promises one order over the whole edge set rather than an
// order per node or per socket.
//
// The alias is part of the identity because a socket carries many connections,
// each with its own target and its own configuration. Were the identity the node
// and the socket alone, two connections on one socket would key one fact between
// them: a rule would be invoked once for what the author wrote twice, would read
// one connection's content for both, and could name only the socket in what it
// raised.
//
// The node arrives whole rather than as an identifier because an edge's
// authored site sits inside its consumer's block, so building the edge's
// locator needs the node's provenance.
type EdgeInstance struct {
	// Node is the consumer the edge is authored in.
	Node NodeInstance

	// Socket is the dependency socket the edge wires.
	Socket string

	// Alias names the connection within its socket. It is what every keyed fact
	// takes as the third element of the edge's key, and it is carried whether or
	// not the author wrote it as a key.
	Alias string

	// SynthesizedAlias reports whether that alias was derived by normalization
	// rather than written by the author as a key. The short authoring form lists
	// targets directly and gives each connection the target's own name as its
	// alias, so no key in the file spells it; the long form names an alias per
	// connection. It discriminates the two cases the locator rule turns on, so an
	// alias the author declares and happens to name the same as a derived one is
	// not mistaken for it.
	SynthesizedAlias bool

	// Carrier is the surface that drew the connection, with the site that
	// surface's own block was written at. It positions the finding and never
	// keys anything: the facts a rule over this kind reads are keyed by the
	// triple above, and the carrier is not among their key.
	//
	// The zero value is an enumeration that does not track carriers, and it
	// leaves the locator at the consuming component's own dependency block —
	// which is the right answer exactly when every connection the enumeration
	// yields was drawn there. An enumeration that yields connections drawn
	// elsewhere fills it, or it names a `uses` block those components do not
	// have.
	Carrier ConnectionCarrier
}

// ConnectionCarrier names the surface that drew a connection into the graph — a
// component's own dependency block, an attachment's, or the deployment's edge
// surface — together with where that surface's own block was written.
//
// The identity half is carried because the edge triple does not identify a
// connection. Two attachments on one node may each write one socket and one
// alias, and the component itself may write the same pair, so a key of node,
// socket and alias names up to three separately authored connections at once. A
// rule keyed that way reads one of them for all of them and reports the others
// as approved.
//
// The kind and the attachment name are two fields rather than one, because the
// three kinds are a closed vocabulary while the attachment name is whatever the
// author typed. Folding the deployment into the name field would need a reserved
// spelling no author may use, and a reserved spelling is a rule nothing enforces.
//
// # Why the provenance half travels with the carrier
//
// A connection an attachment drew is authored inside that attachment's block,
// and that block sits at one of four sites: a component's own attachment block,
// a scope's block in the application, a scope's block in the deployment, or a
// component's entry inside the deployment. The consuming node's own segments
// name none of those. Naming them anyway spells a path the file does not
// contain, and an author sent to a `uses` block their component does not have
// cannot act on the finding.
//
// The site is settled where the connection is drawn and is gone afterwards: the
// finalised graph records which attachment instance drew an edge and never which
// surface declared that attachment. So it travels here, from the reading that
// settled it, rather than being recovered by matching an edge against the
// authoring documents a second time.
//
// The zero value is a carrier nothing recorded. It names no site and no
// document, which routes a finding to file-level positioning rather than to an
// invented path, and it is what an enumeration that does not track carriers
// leaves behind.
type ConnectionCarrier struct {
	// Kind is which surface drew the connection: one of [CarrierComponent],
	// [CarrierAttachment] or [CarrierDeployment], and empty when nothing
	// recorded it.
	Kind string

	// Attachment is the attachment instance name the connection was drawn from,
	// empty for every kind but [CarrierAttachment].
	//
	// It is the instance name and not the Feature type. Two attachments of one
	// Feature type on one node are two carriers, each with its own dependency
	// block, so the type would name both.
	Attachment string

	// Site names where the carrier's own block was written. The zero site names
	// no scope and no component, which is what a carrier nothing recorded has to
	// read as.
	Site AuthoredSite

	// Document is which authoring document that block sits in, one of
	// [AuthoringDocumentAggregate] or [AuthoringDocumentRelease]. It selects the
	// scope nesting the site path is spelled with, and it is carried rather than
	// derived because the kind does not settle it: an attachment's dependency
	// block is written in the application's scope tree when the developer
	// declared it and in the deployment's when the operator did.
	Document string

	// SourceFile is the workspace-relative file the carrier's block was written
	// in, empty when it has no single-file origin. It is the carrier's own file
	// and not the consuming node's, because a connection an operator drew is
	// written on a document the node was not.
	SourceFile string
}

// The carrier kinds. The set is closed: a connection is drawn by a component's
// own dependency block, by an attachment's, or by the deployment's edge surface,
// and there is no fourth surface that draws one.
const (
	// CarrierComponent is a connection the consuming component's own dependency
	// block drew.
	CarrierComponent = "component"

	// CarrierAttachment is a connection an attachment's own dependency block
	// drew, wherever that attachment was declared.
	CarrierAttachment = "attachment"

	// CarrierDeployment is a connection the deployment's edge surface drew into
	// a declared-but-unbound socket.
	CarrierDeployment = "deployment"
)

// The authoring documents a carrier's own block can be written in.
const (
	// AuthoringDocumentAggregate is the application's own document, whose scopes
	// nest as subsystems.
	AuthoringDocumentAggregate = "aggregate"

	// AuthoringDocumentRelease is the deployment's document, whose scopes nest as
	// boundaries.
	AuthoringDocumentRelease = "release"
)

// CarriedConnectionInstance is one connection together with the carrier that
// drew it: which node's which socket, under which alias, from which surface.
//
// It is the edge's identity with the carrier added, and the addition is what
// makes it an identity at all — see [ConnectionCarrier] for the collision it
// closes. The kind carries one coordinate, whose key is the whole of node,
// carrier kind, carrier attachment, socket, alias and resolved target, so a
// connection is enumerated as one instance and the enumerator promises one order
// over the whole set.
type CarriedConnectionInstance struct {
	// Node is the consumer the connection is drawn from.
	Node NodeInstance

	// Carrier is the surface that drew the connection, with the site that
	// surface's own block was written at.
	Carrier ConnectionCarrier

	// Socket is the dependency socket the connection wires.
	Socket string

	// Alias names the connection within its socket, carried whether or not the
	// author wrote it as a key.
	Alias string

	// Target is the node the connection resolves to, by the identity every keyed
	// fact takes.
	//
	// It is part of the identity because two carriers writing one socket and one
	// alias may name different targets, and each delivers against the target it
	// named. A key without it would answer one carrier's question with the
	// other's target — measured, not hypothetical: a component's own connection
	// and an attachment's on the same socket and alias reach the graph as two
	// connections pointing at two components.
	Target string

	// SynthesizedAlias reports whether that alias was derived by normalization
	// rather than written by the author as a key, exactly as it does on
	// [EdgeInstance].
	SynthesizedAlias bool
}

// AuthoredCoordinate is one enumerated coordinate value together with the
// workspace-relative file the author wrote it in.
//
// Only an axis authored in a *different* document from its outer coordinate
// needs one. An axis authored in the same document inherits that document, and
// supplying the file again would be a second answer to one question — two
// answers that can disagree. So a node's sockets, its attachments and its own
// configuration pillars enumerate as plain names, while the entries of a node's
// Recipe, the boundaries of a Release and an attachment's configuration pillars
// carry their own file.
//
// An attachment's pillars are on the second list for a reason worth stating
// where the type is declared, because the axis reads at first sight like the
// node's own: the two pillars of an attachment are authored on two different
// documents. The developer writes an attachment's values in the Aggregate that
// declares the component, and the operator writes its infrastructure
// configuration on the Release — which is the only surface whose attachment
// block admits one. So the pillar names them, not the node.
type AuthoredCoordinate struct {
	// Value is the coordinate value.
	//
	// On most axes it is also the name the author wrote. Where an axis enumerates
	// something a build produced rather than something a document declares — an
	// artifact, named by the output the entry resolves to — the two diverge, and
	// this carries the produced name.
	Value string

	// SourceFile is the workspace-relative file the value was authored in,
	// empty when it has no single-file origin.
	SourceFile string
}

// AuthoredSite names the place inside a document where something was written:
// the scope it sits in, and the component it was keyed under, if any.
//
// It exists because a file is not a position. An axis whose entries can be
// written at more than one site in one document — a scope-wide entry reaching
// every component the scope holds, or an entry naming one of them — needs the
// site as well, or a locator can only spell whichever of the two shapes it was
// written against and will spell that one for both.
//
// It carries no axis. Which nesting a scope path belongs to is settled by what
// is being named rather than by the site: an attachment's values are the
// developer's and are written in the application's own scope tree, while its
// infrastructure configuration is the operator's and is written in the
// deployment's. A field here would be a second answer to a question already
// settled, and two answers can disagree.
type AuthoredSite struct {
	// ScopePath is the scope the entry was written in, joined the way that
	// axis joins its levels.
	//
	// Empty means there is no scope to name — the entry was written at its
	// document's top level, or inside a scope normalization synthesized to
	// hold entries the author wrote at the top level. Neither is a scope any
	// file spells, so both name the top-level shape rather than a generated
	// one. The two collapse to one value deliberately: a locator's question is
	// which key the file contains, and the answer is the same for both.
	ScopePath string

	// Component is the component name the entry was keyed under, empty when
	// the entry was written scope-wide and names no component at all.
	//
	// The empty case is the one a component-shaped path cannot express rather
	// than an edge of it. A scope-wide entry takes effect on every component
	// the scope holds, and none of those is a place anyone can edit: the one
	// editable site is the scope's own block, which names no component.
	Component string
}

// AttachmentLeaf is one leaf of one pillar of one attachment's configuration:
// the leaf's own path, and the site the entry that supplied it was written at.
//
// The site is carried per leaf rather than per attachment because ownership is
// settled leaf by leaf. A scope-wide entry and a component-scoped one on the
// same attachment each keep the leaves the other does not mention, so two
// leaves of one pillar are routinely written in two different places, and a
// site taken once for the attachment would name the wrong one for whichever
// leaf it did not come from.
type AttachmentLeaf struct {
	// Value is the leaf's dotted path inside the pillar, which is the
	// coordinate every fact keyed at this leaf takes.
	Value string

	// Site names where the entry that supplied this leaf was written.
	Site AuthoredSite
}

// NodeEnumerator supplies the node set: every component instance in the
// flattened set, a sub-aggregate's nodes included.
//
// It is the root of every node-keyed traversal, so its order is the outermost
// order for most of the enumerated kinds. The share is deliberately not counted
// here: it moves whenever a kind is declared, and a stale count beside the
// interface would keep reading as a fact about the tree.
type NodeEnumerator interface {
	// Nodes returns every node, in a deterministic order the implementation
	// declares.
	Nodes() []NodeInstance
}

// EdgeEnumerator supplies the edge set: every connection wiring a consumer's
// socket to a target, one instance per connection rather than one per socket.
type EdgeEnumerator interface {
	// Edges returns every edge, in a deterministic order the implementation
	// declares.
	Edges() []EdgeInstance
}

// CarriedConnectionEnumerator supplies the carried-connection set: every
// connection the graph holds, each paired with the carrier that drew it.
//
// It is a separate axis from [EdgeEnumerator] rather than a replacement for it,
// and the two answer different questions. The edge axis is keyed by node, socket
// and alias, which is the key the facts about a connection's resolved shape are
// keyed by; this axis is keyed by that triple plus the carrier, which is what a
// rule reading a connection's AUTHORED content needs, because the authored
// content of two connections sharing the triple is two different payloads.
//
// A carrier key naming more than one connection is possible and is the
// implementor's to state: one attachment instance name declared at two scopes
// draws one connection from each, and the graph records the two identically. The
// fact keyed here answers for all of them rather than for the first, so no
// authored payload goes unexamined.
type CarriedConnectionEnumerator interface {
	// CarriedConnections returns every connection with its carrier, in a
	// deterministic order the implementation declares.
	CarriedConnections() []CarriedConnectionInstance
}

// BoundaryEnumerator supplies the boundary set.
type BoundaryEnumerator interface {
	// Boundaries returns every boundary, in a deterministic order the
	// implementation declares.
	Boundaries() []AuthoredCoordinate
}

// SubsystemEnumerator supplies the subsystem set: every scope the application's
// author declared, at every depth.
//
// Each value is the scope's path from the document's own scope set rather than a
// bare name, because subsystems nest and two scopes at different depths may carry
// one name. An implementation handing back bare names would present two distinct
// scopes under one key.
//
// It is the sibling of [BoundaryEnumerator] on the other authoring document and
// not a generalisation of it: the two scopes are declared in separate documents,
// so one enumerator covering both would hand the framework scopes from two
// documents under one axis.
type SubsystemEnumerator interface {
	// Subsystems returns every subsystem, in a deterministic order the
	// implementation declares.
	Subsystems() []AuthoredCoordinate
}

// ModuleEnumerator supplies the sub-module set: every module the composition
// brought in, at every depth.
//
// Each value is the module's dotted prefix rather than a bare name, because
// modules nest and two modules at different depths may carry one name. The
// prefix is also the identity the module's own components carry in the flattened
// set, which is what lets a rule keyed here read a fact about what the module
// contributed.
//
// The coordinate carries the file that DECLARES the module, which is the
// importing document rather than the module's own: a module is written as an
// entry in its parent, so that is the site a rule about the module can name. It
// is therefore an axis whose file genuinely differs from its outer coordinate's,
// which is the case [AuthoredCoordinate] exists for.
//
// A module the composition did not bring in is absent, and the two ways that
// happens are both deliberate. A module a false activation toggle removed was
// never composed, so nothing it declares reached the build and no rule over the
// build has anything to say about it. A module nothing loaded contributed
// nothing for the same reason.
type ModuleEnumerator interface {
	// Modules returns every sub-module the composition brought in, in a
	// deterministic order the implementation declares.
	Modules() []AuthoredCoordinate
}

// NodeSocketEnumerator supplies the dependency sockets of one node: the set the
// node's component type DECLARES, never the set the node populates.
//
// Which of the two the axis means is the axis's contract rather than a detail of
// it, because the two sets genuinely differ and a rule needs whichever it needs.
// A rule over a socket that must carry a connection has to be asked about a
// socket the node wrote nothing for — that is the case it exists to catch — so
// an axis enumerating what the node populated would never present that rule its
// own subject. The set a node actually authored is a fact on the view instead,
// and a rule comparing the two reads the fact against this enumeration.
//
// A socket the node authored that its type declares no entry for is therefore
// absent from this axis. Such a socket is authoring the product accepts, so its
// absence here is the axis holding its meaning rather than a gap: a rule over
// such a socket reads the authored set, which reaches every socket the node
// wrote whether or not the type declares one.
type NodeSocketEnumerator interface {
	// NodeSockets returns the socket names a node's component type declares, in
	// a deterministic order the implementation declares.
	NodeSockets(node string) []string
}

// AttachmentEnumerator supplies the attachments one node carries.
type AttachmentEnumerator interface {
	// Attachments returns a node's attachment names, in a deterministic order
	// the implementation declares.
	Attachments(node string) []string
}

// AttachmentSocketEnumerator supplies the sockets one attachment WIRES in its own
// dependency block: the set the attachment populates, never the set the Feature
// it attaches declares.
//
// Which of the two the axis means is the axis's contract rather than a detail of
// it, and this axis is the mirror image of [NodeSocketEnumerator] above — so
// neither can be inferred from the other and both say which they are. A rule over
// what an attachment wires reports a socket the attached Feature declares no
// entry for, and that socket is by construction absent from the declared set, so
// an axis enumerating the declared set would never present such a rule its own
// subject. What the Feature declares is a fact keyed by the Feature type instead,
// which the rule reads and compares the socket it was invoked for against.
//
// The socket names are drawn from the attached Feature, a separate authoring
// surface from the component's own sockets, which is why the axis is keyed per
// attachment as well as per node: one socket name means different things under
// two attachments of different Features.
type AttachmentSocketEnumerator interface {
	// AttachmentSockets returns the socket names an attachment wires, in a
	// deterministic order the implementation declares.
	AttachmentSockets(node string, attachment string) []string
}

// SlotEnumerator supplies the attachment slots one node's component type
// declares.
//
// A slot is a binding point and not a dependency socket: separate authoring
// surfaces with separate rules over them, hence a separate axis.
type SlotEnumerator interface {
	// Slots returns a node's slot names, in a deterministic order the
	// implementation declares.
	Slots(node string) []string
}

// MechanismEnumerator supplies the delivery mechanisms one node's injection
// block declares.
type MechanismEnumerator interface {
	// Mechanisms returns a node's mechanism names, in a deterministic order the
	// implementation declares.
	Mechanisms(node string) []string
}

// DeliveryKeyEnumerator supplies the keys one node delivers through one
// injection mechanism.
type DeliveryKeyEnumerator interface {
	// DeliveryKeys returns the keys delivered through one mechanism, in a
	// deterministic order the implementation declares.
	DeliveryKeys(node string, mechanism string) []string
}

// NodePillarEnumerator supplies the configuration pillars one node's author
// wrote.
type NodePillarEnumerator interface {
	// NodePillars returns a node's pillar names, in a deterministic order the
	// implementation declares.
	NodePillars(node string) []string
}

// NodePillarLeafEnumerator supplies the leaves of one configuration pillar of
// one node.
type NodePillarLeafEnumerator interface {
	// NodePillarLeaves returns the leaf names of one pillar, in a deterministic
	// order the implementation declares.
	NodePillarLeaves(node string, pillar string) []string
}

// ParameterEnumerator supplies the parameters one node resolved.
//
// The parameter fact is keyed rather than enumerating, deliberately: a
// flattened sub-aggregate node carries its resolved set in a shape that holds
// no order, so an accessor handing back the whole set would put byte-identical
// output at the mercy of map iteration. That obligation does not disappear
// here — it moves, and this is where it lands. An implementation over an
// unordered carrier owes a defensible sort.
type ParameterEnumerator interface {
	// Parameters returns the parameter names a node resolved, in a
	// deterministic order the implementation declares.
	Parameters(node string) []string
}

// AttachmentPillarEnumerator supplies the configuration pillars of one
// attachment.
//
// Each entry carries its own file, because the two pillars are written on two
// different authoring documents: an attachment's values are the developer's and
// are written in the Aggregate that declares the component, while its
// infrastructure configuration is the operator's and is written on the Release.
// Attributing both to the node's document would name, for one of them, a file
// whose attachment block cannot hold the key the locator spells.
type AttachmentPillarEnumerator interface {
	// AttachmentPillars returns one attachment's pillars, each with the file it
	// was authored in, in a deterministic order the implementation declares.
	AttachmentPillars(node string, attachment string) []AuthoredCoordinate
}

// AttachmentPillarLeafEnumerator supplies the leaves of one pillar of one
// attachment's configuration.
//
// A leaf carries no file of its own: it is a path inside the pillar, so it is
// written wherever that pillar is written and inherits the pillar's document. A
// file here would be a second answer to a question the pillar already answers.
//
// It does carry its own site, and that is not the same question. The pillar
// says which document; the site says where inside it, and the two differ per
// leaf: an attachment's configuration is assembled from every scope that
// reaches the node, so one pillar's leaves can come from a scope's own block
// and from a component's entry under that scope at once. The document is
// common to them and the site is not, which is why one sits on the pillar and
// the other on the leaf.
type AttachmentPillarLeafEnumerator interface {
	// AttachmentPillarLeaves returns the leaves of one attachment pillar, each
	// with the site it was written at, in a deterministic order the
	// implementation declares.
	AttachmentPillarLeaves(node string, attachment string, pillar string) []AttachmentLeaf
}

// LocalsKeyEnumerator supplies the locals one node's Recipe declares.
//
// Each entry carries its own file, because a Recipe is a different document
// from the Aggregate the node was authored in.
type LocalsKeyEnumerator interface {
	// LocalsKeys returns a node's declared locals, in the order they are
	// declared — which is the order they are evaluated in, so a rule about
	// what a local may reference is a rule about this order.
	LocalsKeys(node string) []AuthoredCoordinate
}

// AttachmentLocalsKeyEnumerator supplies the locals the FeatureRecipe one
// attachment binds on a node declares.
//
// The block an attachment carries is supplied here and never through
// [LocalsKeyEnumerator]: an implementation folding an attachment's keys into a
// node's own answer would present two independently authored blocks under one
// key, and the author-order prefix a rule reads off that answer would then span
// two documents.
//
// Each entry carries its own file, because a FeatureRecipe is a different
// document from the Aggregate the attachment was written in.
type AttachmentLocalsKeyEnumerator interface {
	// AttachmentLocalsKeys returns the locals one attachment's FeatureRecipe
	// declares on a node, in the order they are declared — which is the order
	// they are evaluated in, so a rule about what one of them may reference is a
	// rule about this order.
	AttachmentLocalsKeys(node string, attachment string) []AuthoredCoordinate
}

// BoundaryAttachmentLocalsKeyEnumerator supplies the locals the FeatureRecipe one
// boundary-scope attachment binds declares.
//
// The axis is keyed per boundary and not per node for the reason the boundary's
// own attachment axis is: such an attachment is declared once at the boundary and
// reaches every component the boundary holds, so enumerating its locals per
// component would present one authored block many times over.
type BoundaryAttachmentLocalsKeyEnumerator interface {
	// BoundaryAttachmentLocalsKeys returns the locals one boundary-scope
	// attachment's FeatureRecipe declares, in the order they are declared.
	BoundaryAttachmentLocalsKeys(boundary string, attachment string) []AuthoredCoordinate
}

// ProvidesKeyEnumerator supplies the keys one node's Recipe provides.
type ProvidesKeyEnumerator interface {
	// ProvidesKeys returns a node's provides keys, in a deterministic order the
	// implementation declares.
	ProvidesKeys(node string) []AuthoredCoordinate
}

// The pipeline-step axis is supplied by three enumerators, one per authored
// pipeline, and the split is a property of the authoring rather than a
// convenience.
//
// A pipeline is authored on the entity that carries it, and three entities carry
// one: a component's Recipe, the FeatureRecipe an attachment binds on a node,
// and the FeatureRecipe an operator attaches at a boundary. Those are three
// separate documents, and the identifiers inside them are chosen independently —
// nothing declares them unique across documents, and a step identifier is
// therefore meaningful only together with the pipeline it was written in.
//
// So the coordinates that identify a step are the coordinates that identify its
// pipeline plus the step's own, and each of the three gets its own enumerator
// and its own subject kind. Widening one enumerator to cover all three would
// hand the framework steps from several documents under one key, and a fact
// keyed that way would answer with whichever pipeline was asked last.

// PipelineStepEnumerator supplies the steps of one node's own Recipe pipeline,
// and only those.
//
// The pipelines an attachment carries are supplied by
// [AttachmentPipelineStepEnumerator] and
// [BoundaryAttachmentPipelineStepEnumerator], never here: an implementation
// folding an attachment's steps into this answer would present them under a key
// that cannot tell them from the node's own.
type PipelineStepEnumerator interface {
	// PipelineSteps returns the steps of a node's own Recipe pipeline,
	// identified by the identifier each step declares, in pipeline order.
	PipelineSteps(node string) []AuthoredCoordinate
}

// AttachmentPipelineStepEnumerator supplies the steps of the pipeline one
// attachment's FeatureRecipe carries on a node.
//
// Each entry carries its own file, because a FeatureRecipe is a different
// document from the Aggregate the attachment was written in.
type AttachmentPipelineStepEnumerator interface {
	// AttachmentPipelineSteps returns the steps of one attachment's pipeline,
	// identified by the identifier each step declares, in pipeline order.
	AttachmentPipelineSteps(node string, attachment string) []AuthoredCoordinate
}

// BoundaryAttachmentPipelineStepEnumerator supplies the steps of the pipeline
// one boundary-scope attachment's FeatureRecipe carries.
//
// The axis is keyed per boundary and not per node for the same reason the
// boundary's own attachment axis is: such an attachment is declared once at the
// boundary and reaches every component the boundary holds, so enumerating its
// pipeline per component would present one authored pipeline many times over.
type BoundaryAttachmentPipelineStepEnumerator interface {
	// BoundaryAttachmentPipelineSteps returns the steps of one boundary-scope
	// attachment's pipeline, identified by the identifier each step declares, in
	// pipeline order.
	BoundaryAttachmentPipelineSteps(boundary string, attachment string) []AuthoredCoordinate
}

// The artifact axis is supplied by three enumerators, one per authored artifacts
// block, and the split is a property of the authoring rather than a convenience.
//
// An artifacts block is authored on the entity that carries it, and three
// entities carry one: a component's Recipe, the FeatureRecipe an attachment binds
// on a node, and the FeatureRecipe an operator attaches at a boundary. Those are
// three separate documents, and the output names inside them are chosen
// independently — nothing declares them unique across documents, and an artifact
// name is therefore meaningful only together with the block it was written in.
//
// So the coordinates that identify an artifact are the coordinates that identify
// its block plus the artifact's own name, and each of the three gets its own
// enumerator and its own subject kind. Widening one enumerator to cover all three
// would hand the framework entries from several documents under one key, and a
// fact keyed that way would answer with whichever block was asked last.

// ArtifactEnumerator supplies the artifacts one node's own Recipe projects, and
// only those.
//
// The artifacts an attachment projects are supplied by
// [AttachmentArtifactEnumerator] and [BoundaryAttachmentArtifactEnumerator],
// never here: an implementation folding an attachment's entries into this answer
// would present them under a key that cannot tell them from the node's own.
type ArtifactEnumerator interface {
	// Artifacts returns a node's artifact names, in a deterministic order the
	// implementation declares.
	Artifacts(node string) []AuthoredCoordinate
}

// AttachmentArtifactEnumerator supplies the artifacts the FeatureRecipe one
// attachment binds on a node projects.
//
// Each entry carries its own file, because a FeatureRecipe is a different
// document from the Aggregate the attachment was written in.
type AttachmentArtifactEnumerator interface {
	// AttachmentArtifacts returns the artifacts one attachment projects, named by
	// the output name each entry resolves to, in a deterministic order the
	// implementation declares.
	AttachmentArtifacts(node string, attachment string) []AuthoredCoordinate
}

// BoundaryAttachmentArtifactEnumerator supplies the artifacts the FeatureRecipe
// one boundary-scope attachment binds projects.
//
// The axis is keyed per boundary and not per node for the reason the boundary's
// own attachment axis is: such an attachment is declared once at the boundary and
// reaches every component the boundary holds, so enumerating its artifacts per
// component would present one authored block many times over.
type BoundaryAttachmentArtifactEnumerator interface {
	// BoundaryAttachmentArtifacts returns the artifacts one boundary-scope
	// attachment projects, named by the output name each entry resolves to, in a
	// deterministic order the implementation declares.
	BoundaryAttachmentArtifacts(boundary string, attachment string) []AuthoredCoordinate
}

// AssemblyPathEnumerator supplies the evaluated assembly paths a build's nodes
// were grouped at.
//
// Each value is the path a group assembles at, which is output rather than
// something an author wrote — so the coordinate carries the deployment document
// as its source file and the path contributes no locator segment. It is an
// [AuthoredCoordinate] all the same, because that is how the file travels with
// the value, and a subject that knows the file positions a finding at it rather
// than nowhere.
//
// It is the sibling of [BoundaryEnumerator] on the produced side rather than a
// generalisation of it: a boundary is a site an operator edits, an assembly path
// is what several of those sites can evaluate to together, and one axis covering
// both would present a rule with two different kinds of thing under one key.
type AssemblyPathEnumerator interface {
	// AssemblyPaths returns every evaluated assembly path, deduplicated, in a
	// deterministic order the implementation declares.
	AssemblyPaths() []AuthoredCoordinate
}

// AssemblyGroupProviderEnumerator supplies the providers that assembled at one
// evaluated assembly path.
//
// It is distinct from [ProviderEnumerator], which answers within a boundary.
// Several boundaries can evaluate to one assembly path, so the provider set at a
// path is not the provider set of any one boundary that fed it — and it is
// exactly the difference that makes a path reached by two providers visible.
type AssemblyGroupProviderEnumerator interface {
	// AssemblyGroupProviders returns the provider names that assembled at one
	// assembly path, in a deterministic order the implementation declares.
	AssemblyGroupProviders(assemblyPath string) []string
}

// ProviderEnumerator supplies the providers rendered within one boundary.
type ProviderEnumerator interface {
	// Providers returns the provider names within a boundary, in a
	// deterministic order the implementation declares.
	Providers(boundary string) []string
}

// EmittedPathEnumerator supplies the output paths written for one provider
// within one boundary.
type EmittedPathEnumerator interface {
	// EmittedPaths returns the paths written for one provider within one
	// boundary, in path order.
	//
	// The sibling axes leave their order to the implementation to declare; this
	// one fixes it, because the order in which files are placed into a group is
	// not a property of the input. An implementation inheriting that placement
	// order would answer differently for identical input.
	EmittedPaths(boundary string, provider string) []string
}

// NodeEmittedPathEnumerator supplies the output names one node's own render
// emitted, before anything is grouped for assembly.
//
// It is distinct from [EmittedPathEnumerator], which answers for a provider
// within a boundary once assembly has flushed its groups. A node's own emission
// is complete earlier and under the names that node chose, and an assembly is
// free to rename, nest or merge those away — so neither axis is the other read
// at a different moment, and the names one returns are not a subset of the
// other's.
type NodeEmittedPathEnumerator interface {
	// NodeEmittedPaths returns the output names one node's own render emitted,
	// deduplicated, in name order.
	//
	// The sibling node-rooted axes leave their order to the implementation to
	// declare; this one fixes it, for the reason the assembled axis fixes its
	// own. The order in which a render's passes place files is not a property of
	// the input in the way an authored block's order is, and a name two passes
	// both emitted would otherwise appear at whichever position the first pass to
	// reach it happened to run at. Name order is derived from something ordered
	// and is therefore the same on every run.
	NodeEmittedPaths(node string) []string
}

// NodeAttachmentEmittedPathEnumerator supplies the output names the render one
// attachment carries on a node emitted, before anything is grouped for assembly.
//
// It is the carrier-keyed sibling of [NodeEmittedPathEnumerator]. The two are
// separate axes rather than one read at two keys: an attachment's render runs under
// the host component's node identity, so folding its names into the node's own
// answer would present them under a key that cannot tell them from the node's — and
// a collision rule reading that answer would attribute one document's fault to
// another.
type NodeAttachmentEmittedPathEnumerator interface {
	// NodeAttachmentEmittedPaths returns the output names the render one
	// attachment carries on a node emitted, deduplicated, in name order.
	//
	// The order is fixed here rather than left to the implementation, for the
	// reason [NodeEmittedPathEnumerator] fixes its own: the order in which a
	// render's passes place files is not a property of the input, so a name two
	// passes both emitted would otherwise appear at whichever position the first
	// pass to reach it happened to run at.
	//
	// Each name carries its own file, and that is the difference from the
	// node-rooted axis rather than an inconsistency with it. The blocks that met on
	// one of these names are authored in the FeatureRecipe the attachment binds,
	// which is a different document from the one the node was written in — so the
	// document an author edits travels with the entry, exactly as it does on the
	// attachment's artifact axis. The name itself contributes no locator segment,
	// because it is output.
	NodeAttachmentEmittedPaths(node string, attachment string) []AuthoredCoordinate
}

// BoundaryAttachmentEmittedPathEnumerator supplies the output names the render one
// boundary-scope attachment carries emitted, before anything is grouped for
// assembly.
//
// The axis is keyed per boundary and not per node for the reason every
// boundary-scope axis is: such an attachment is declared once at the boundary and
// reaches every component it holds, so enumerating per component would present one
// render's names many times over and none at all for a boundary holding no
// components.
type BoundaryAttachmentEmittedPathEnumerator interface {
	// BoundaryAttachmentEmittedPaths returns the output names the render one
	// boundary-scope attachment carries emitted, deduplicated, in name order.
	//
	// The order is fixed here rather than left to the implementation, on the
	// reading its two sibling emitted axes carry.
	//
	// Each name carries its own file, the FeatureRecipe the attachment binds, for
	// the reason the node-carried attachment axis gives: that is the document an
	// author edits, and it is neither the deployment the attachment was declared in
	// nor any component's Recipe.
	BoundaryAttachmentEmittedPaths(boundary string, attachment string) []AuthoredCoordinate
}

// BoundaryAttachmentEnumerator supplies the Feature attachments declared on one
// boundary itself.
//
// Those attachments are declared at the boundary rather than against a named
// component, which is why the axis is keyed per boundary and not per node: one
// of them reaches every component the boundary holds, so enumerating it per
// component would present one authored attachment many times over.
type BoundaryAttachmentEnumerator interface {
	// BoundaryAttachments returns a boundary's own attachment names, in a
	// deterministic order the implementation declares.
	BoundaryAttachments(boundary string) []string
}

// SubsystemAttachmentEnumerator supplies the Feature attachments declared on one
// subsystem itself.
//
// The axis is keyed per subsystem and not per node for the reason the boundary's
// own attachment axis is: such an attachment is declared at the scope rather than
// against a named component, and it reaches every component that scope holds, so
// enumerating it per component would present one authored attachment many times
// over and would present it not at all for a scope holding none.
type SubsystemAttachmentEnumerator interface {
	// SubsystemAttachments returns a subsystem's own attachment names, in a
	// deterministic order the implementation declares.
	SubsystemAttachments(subsystem string) []string
}
