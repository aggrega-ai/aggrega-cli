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

// EdgeInstance is one edge: which node's which socket.
//
// The pair is the edge's identity rather than a nesting. The derived kind
// carries one coordinate, and the fact keyed by it takes the node and the
// socket together as its key — so an edge is enumerated as one instance, and
// the enumerator promises one order over the whole edge set rather than an
// order per node.
//
// The node arrives whole rather than as an identifier because an edge's
// authored site sits inside its consumer's block, so building the edge's
// locator needs the node's provenance.
type EdgeInstance struct {
	// Node is the consumer the edge is authored in.
	Node NodeInstance

	// Socket is the dependency socket the edge wires.
	Socket string
}

// AuthoredCoordinate is one enumerated coordinate value together with the
// workspace-relative file the author wrote it in.
//
// Only an axis authored in a *different* document from its outer coordinate
// needs one. An axis authored in the same document inherits that document, and
// supplying the file again would be a second answer to one question — two
// answers that can disagree. So a node's sockets, attachments and pillars
// enumerate as plain names, while the entries of a node's Recipe and the
// boundaries of a Release carry their own file.
type AuthoredCoordinate struct {
	// Value is the coordinate value, which is also the name the author wrote.
	Value string

	// SourceFile is the workspace-relative file the value was authored in,
	// empty when it has no single-file origin.
	SourceFile string
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

// EdgeEnumerator supplies the edge set: every wiring of a consumer's socket to
// a target.
type EdgeEnumerator interface {
	// Edges returns every edge, in a deterministic order the implementation
	// declares.
	Edges() []EdgeInstance
}

// BoundaryEnumerator supplies the boundary set.
type BoundaryEnumerator interface {
	// Boundaries returns every boundary, in a deterministic order the
	// implementation declares.
	Boundaries() []AuthoredCoordinate
}

// NodeSocketEnumerator supplies the dependency sockets of one node.
type NodeSocketEnumerator interface {
	// NodeSockets returns the socket names declared for a node, in a
	// deterministic order the implementation declares.
	NodeSockets(node string) []string
}

// AttachmentEnumerator supplies the attachments one node carries.
type AttachmentEnumerator interface {
	// Attachments returns a node's attachment names, in a deterministic order
	// the implementation declares.
	Attachments(node string) []string
}

// AttachmentSocketEnumerator supplies the sockets one attachment's own
// dependency block declares.
//
// Those sockets are declared on the attached Feature, a separate authoring
// surface from the component's own sockets, which is why the axis is keyed per
// attachment as well as per node.
type AttachmentSocketEnumerator interface {
	// AttachmentSockets returns an attachment's own socket names, in a
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
type AttachmentPillarEnumerator interface {
	// AttachmentPillars returns one attachment's pillar names, in a
	// deterministic order the implementation declares.
	AttachmentPillars(node string, attachment string) []string
}

// AttachmentPillarLeafEnumerator supplies the leaves of one pillar of one
// attachment's configuration.
type AttachmentPillarLeafEnumerator interface {
	// AttachmentPillarLeaves returns the leaf names of one attachment pillar,
	// in a deterministic order the implementation declares.
	AttachmentPillarLeaves(node string, attachment string, pillar string) []string
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

// ArtifactEnumerator supplies the artifacts one node projects.
type ArtifactEnumerator interface {
	// Artifacts returns a node's artifact names, in a deterministic order the
	// implementation declares.
	Artifacts(node string) []AuthoredCoordinate
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
	// boundary, in emission order.
	EmittedPaths(boundary string, provider string) []string
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
