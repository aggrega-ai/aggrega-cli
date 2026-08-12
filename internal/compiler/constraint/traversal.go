// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// One traversal per enumerated subject kind.
//
// A check declares what it is invoked once per and never writes the loop. These
// functions are that loop: each walks the coordinates of one derived kind, hands
// the check a subject carrying provenance the check did not assemble, and
// collects what the check raised.
//
// Every one of them takes a Check value rather than a bare predicate, so a check
// reaching a traversal has necessarily declared the rule it enforces and what a
// violation does to the build. There is no way to run a predicate through here
// without a declaration.
//
// # Order
//
// Each traversal preserves the order its enumerators return and nests the outer
// coordinate before the inner one. Nothing here sorts, and nothing here ranges a
// map: every loop walks a slice. Enumeration order reaches the diagnostics a
// check raises, so the order each enumerator promises is what makes output
// byte-identical across runs of the same input; the enumerator interfaces carry
// that obligation.
//
// # Cancellation
//
// Every traversal takes a Cancellation and reads it as the first statement of
// its outermost loop body, so a long walk stops within one outer iteration
// rather than at the end of the enumeration. A stopped walk returns no
// objections and the reason the signal gave: a partial set handed back as though
// it were complete would report a subset of the violations as all of them.
//
// The read sits in the loop because there is nowhere else it can be. An
// enumerator hands back a materialised slice, so a signal offered to one could
// only decline to *build* the slice and would do nothing at all once it exists —
// and the walk is where the time goes.
//
// # Provenance
//
// A subject's locator is assembled here and never by a check. Three shapes
// cover every kind:
//
//   - A coordinate authored inside a component instance's block nests under the
//     component's own segments. Its locator is empty when the component's is —
//     see componentInnerSegments.
//   - A coordinate authored at a document's spec level names that block
//     directly, whatever subsystem the node was reached through.
//   - A coordinate that is not authored at all — a provider, an emitted output
//     path — contributes no segment, and the coordinate it sits within is what
//     the locator names.

// The authoring keys the locator dialect names. They are the keys the author
// actually writes, so they are declared once here rather than spelled at each
// use. The component block's own keys belong to the component-locator rule and
// are assembled there.
const (
	authoredKeySpec       = "spec"
	authoredKeyUses       = "uses"
	authoredKeyFeatures   = "features"
	authoredKeyInject     = "inject"
	authoredKeyValues     = "values"
	authoredKeyParameters = "parameters"
	authoredKeyLocals     = "locals"
	authoredKeyProvides   = "provides"
	authoredKeyPipeline   = "pipeline"
	authoredKeyArtifacts  = "artifacts"
	authoredKeyBoundaries = "boundaries"
)

// componentInnerSegments returns the segments naming an element authored inside
// a component instance's block: the component's own segments followed by tail.
//
// It returns no segments when the component has none, and that propagation is
// the point of the helper rather than a detail of it. A component in a subsystem
// the author declared has an authored site the dotted form cannot express, so
// anything authored inside it is equally inexpressible; emitting the tail alone
// would name a path — ".uses.database" — that no file contains. An empty
// locator routes a consumer to file-level positioning instead, which is the
// mechanical form of not inventing a locator.
func componentInnerSegments(node NodeInstance, tail ...string) []string {
	base := componentAuthoredSegments(node.AuthoredName, node.SubsystemPath, node.SynthesizedSubsystem)
	if len(base) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(tail))
	out = append(out, base...)
	return append(out, tail...)
}

// documentSpecSegments returns the segments naming an element authored at a
// document's spec level: the spec, the block that holds it, and the name the
// author wrote.
//
// A coordinate reached this way is not affected by how its node was reached,
// because it is not authored inside the node at all. A node's resolved
// parameters are the resolution of the contract its document declares, and a
// node's locals, provides, pipeline steps and artifacts are authored in its
// Recipe.
func documentSpecSegments(block string, tail ...string) []string {
	out := make([]string, 0, 2+len(tail))
	out = append(out, authoredKeySpec, block)
	return append(out, tail...)
}

// runOverNodes is the one place the node nesting lives. Fourteen of the sixteen
// enumerated kinds are reached within a node, and the outer walk is identical
// for all of them: node order outermost, the per-node subjects in the order
// their own enumerator returned, everything raised accumulated in that order.
//
// subjects yields the subjects of one node rather than running the check itself,
// so a traversal contributes only how a kind's coordinates and provenance are
// built and never how invocation works.
//
// The cancellation read is here for the same reason the nesting is: fourteen
// kinds would otherwise each carry their own copy of it, and fourteen copies of
// one rule are fourteen chances for one of them to drift.
func runOverNodes[V any, S Subject](
	cancelled Cancellation,
	nodes NodeEnumerator,
	view V,
	run func(V, S) []Objection,
	subjects func(NodeInstance) []S,
) ([]Objection, error) {
	var out []Objection
	for _, node := range nodes.Nodes() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		for _, subject := range subjects(node) {
			out = append(out, run(view, subject)...)
		}
	}
	return out, nil
}

// RunOnNodes invokes check once per node, in the order nodes returns them.
//
// A node's locator follows the component-instance provenance rule: a component
// the author wrote at the top level of its document names .spec.components.<name>
// even when normalization reached it through a synthesized subsystem, while a
// component in a subsystem the author declared carries no locator at all.
func RunOnNodes[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	view V,
	check Check[V, NodeSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeSubject {
		return []NodeSubject{newComponentNodeSubject(
			node.SourceFile, node.ID, node.AuthoredName, node.SubsystemPath, node.SynthesizedSubsystem,
		)}
	})
}

// RunOnNodeSockets invokes check once per dependency socket of each node, in
// node order and then socket order.
//
// A socket's locator is .spec.components.<name>.uses.<socket>, the entry in the
// component's dependency block.
func RunOnNodeSockets[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	sockets NodeSocketEnumerator,
	view V,
	check Check[V, NodeSocketSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeSocketSubject {
		names := sockets.NodeSockets(node.ID)
		out := make([]NodeSocketSubject, 0, len(names))
		for _, socket := range names {
			out = append(out, newNodeSocketSubject(
				node.SourceFile, node.ID, socket,
				componentInnerSegments(node, authoredKeyUses, socket)...,
			))
		}
		return out
	})
}

// RunOnEdges invokes check once per edge, in the order edges returns them.
//
// The edge set is walked flat rather than per node, because the derived kind
// carries one coordinate and the fact keyed by it takes the node and the socket
// together: one order over the whole set, promised by one enumerator.
//
// An edge's locator is .spec.components.<name>.uses.<socket> — the same
// authored entry a socket names, because an edge is the wiring the author wrote
// there.
func RunOnEdges[V View](
	cancelled Cancellation,
	edges EdgeEnumerator,
	view V,
	check Check[V, EdgeSubject],
) ([]Objection, error) {
	var out []Objection
	for _, edge := range edges.Edges() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		subject := newEdgeSubject(
			edge.Node.SourceFile, edge.Node.ID, edge.Socket,
			componentInnerSegments(edge.Node, authoredKeyUses, edge.Socket)...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out, nil
}

// RunOnNodeAttachments invokes check once per attachment each node carries, in
// node order and then attachment order.
//
// An attachment's locator is .spec.components.<name>.features.<attachment>.
func RunOnNodeAttachments[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	view V,
	check Check[V, NodeAttachmentSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentSubject {
		names := attachments.Attachments(node.ID)
		out := make([]NodeAttachmentSubject, 0, len(names))
		for _, attachment := range names {
			out = append(out, newNodeAttachmentSubject(
				node.SourceFile, node.ID, attachment,
				componentInnerSegments(node, authoredKeyFeatures, attachment)...,
			))
		}
		return out
	})
}

// RunOnNodeAttachmentSockets invokes check once per socket an attachment's own
// dependency block declares, in node order, then attachment order, then socket
// order.
//
// The locator is .spec.components.<name>.features.<attachment>.uses.<socket>:
// these sockets are declared on the attached Feature, a separate authoring
// surface from the component's own.
func RunOnNodeAttachmentSockets[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	sockets AttachmentSocketEnumerator,
	view V,
	check Check[V, NodeAttachmentSocketSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentSocketSubject {
		var out []NodeAttachmentSocketSubject
		for _, attachment := range attachments.Attachments(node.ID) {
			for _, socket := range sockets.AttachmentSockets(node.ID, attachment) {
				out = append(out, newNodeAttachmentSocketSubject(
					node.SourceFile, node.ID, attachment, socket,
					componentInnerSegments(
						node, authoredKeyFeatures, attachment, authoredKeyUses, socket,
					)...,
				))
			}
		}
		return out
	})
}

// RunOnNodeSlots invokes check once per attachment slot each node's component
// type declares, in node order and then slot order.
//
// A slot's locator is .spec.components.<name>.features.<slot>, the entry in the
// attachment block that fills it — which is the site the author writes whether
// or not the slot is currently filled.
func RunOnNodeSlots[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	slots SlotEnumerator,
	view V,
	check Check[V, NodeSlotSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeSlotSubject {
		names := slots.Slots(node.ID)
		out := make([]NodeSlotSubject, 0, len(names))
		for _, slot := range names {
			out = append(out, newNodeSlotSubject(
				node.SourceFile, node.ID, slot,
				componentInnerSegments(node, authoredKeyFeatures, slot)...,
			))
		}
		return out
	})
}

// RunOnNodeMechanisms invokes check once per delivery mechanism each node's
// injection block declares, in node order and then mechanism order.
//
// A mechanism's locator is .spec.components.<name>.inject.<mechanism>.
func RunOnNodeMechanisms[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	mechanisms MechanismEnumerator,
	view V,
	check Check[V, NodeMechanismSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeMechanismSubject {
		names := mechanisms.Mechanisms(node.ID)
		out := make([]NodeMechanismSubject, 0, len(names))
		for _, mechanism := range names {
			out = append(out, newNodeMechanismSubject(
				node.SourceFile, node.ID, mechanism,
				componentInnerSegments(node, authoredKeyInject, mechanism)...,
			))
		}
		return out
	})
}

// RunOnNodeMechanismDeliveryKeys invokes check once per key a node delivers
// through one injection mechanism, in node order, then mechanism order, then key
// order.
//
// The locator is .spec.components.<name>.inject.<mechanism>.<key>.
func RunOnNodeMechanismDeliveryKeys[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	mechanisms MechanismEnumerator,
	keys DeliveryKeyEnumerator,
	view V,
	check Check[V, NodeMechanismDeliveryKeySubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeMechanismDeliveryKeySubject {
		var out []NodeMechanismDeliveryKeySubject
		for _, mechanism := range mechanisms.Mechanisms(node.ID) {
			for _, key := range keys.DeliveryKeys(node.ID, mechanism) {
				out = append(out, newNodeMechanismDeliveryKeySubject(
					node.SourceFile, node.ID, mechanism, key,
					componentInnerSegments(
						node, authoredKeyInject, mechanism, key,
					)...,
				))
			}
		}
		return out
	})
}

// RunOnNodePillarLeaves invokes check once per leaf of each configuration pillar
// a node's author wrote, in node order, then pillar order, then leaf order.
//
// The locator is .spec.components.<name>.values.<pillar>.<leaf>.
func RunOnNodePillarLeaves[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	pillars NodePillarEnumerator,
	leaves NodePillarLeafEnumerator,
	view V,
	check Check[V, NodePillarLeafSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodePillarLeafSubject {
		var out []NodePillarLeafSubject
		for _, pillar := range pillars.NodePillars(node.ID) {
			for _, leaf := range leaves.NodePillarLeaves(node.ID, pillar) {
				out = append(out, newNodePillarLeafSubject(
					node.SourceFile, node.ID, pillar, leaf,
					componentInnerSegments(
						node, authoredKeyValues, pillar, leaf,
					)...,
				))
			}
		}
		return out
	})
}

// RunOnNodeParameters invokes check once per parameter each node resolved, in
// node order and then parameter order.
//
// The locator is .spec.parameters.<name> in the node's own file, and it is
// deliberately not nested under the component. A parameter is declared at its
// document's spec level and resolved per node, so the authored site is that
// declaration — and for a node flattened out of a sub-aggregate the node's file
// is the sub-aggregate that declares its parameters. A component instance has no
// parameter block for a locator to name.
func RunOnNodeParameters[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	parameters ParameterEnumerator,
	view V,
	check Check[V, NodeParameterSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeParameterSubject {
		names := parameters.Parameters(node.ID)
		out := make([]NodeParameterSubject, 0, len(names))
		for _, name := range names {
			out = append(out, newNodeParameterSubject(
				node.SourceFile, node.ID, name,
				documentSpecSegments(authoredKeyParameters, name)...,
			))
		}
		return out
	})
}

// RunOnNodeAttachmentPillarLeaves invokes check once per leaf of each pillar of
// each attachment's configuration, in node order, then attachment order, then
// pillar order, then leaf order.
//
// The locator is
// .spec.components.<name>.features.<attachment>.values.<pillar>.<leaf>. The
// pillar is a coordinate here and the slot is a coordinate in its own right
// elsewhere: the two are not the same axis.
func RunOnNodeAttachmentPillarLeaves[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	pillars AttachmentPillarEnumerator,
	leaves AttachmentPillarLeafEnumerator,
	view V,
	check Check[V, NodeAttachmentPillarLeafSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentPillarLeafSubject {
		var out []NodeAttachmentPillarLeafSubject
		for _, attachment := range attachments.Attachments(node.ID) {
			for _, pillar := range pillars.AttachmentPillars(node.ID, attachment) {
				for _, leaf := range leaves.AttachmentPillarLeaves(node.ID, attachment, pillar) {
					out = append(out, newNodeAttachmentPillarLeafSubject(
						node.SourceFile, node.ID, attachment, pillar, leaf,
						componentInnerSegments(
							node, authoredKeyFeatures, attachment, authoredKeyValues, pillar, leaf,
						)...,
					))
				}
			}
		}
		return out
	})
}

// RunOnNodeLocalsKeys invokes check once per local each node's Recipe declares,
// in node order and then declaration order.
//
// Declaration order is the contract rather than a convenience: locals are
// evaluated in the order they are written, so a check about what a local may
// reference is invoked in the order that question is decided.
//
// The locator is .spec.locals.<key> in the Recipe's own file, which is a
// different document from the Aggregate the node was authored in — so the file
// comes from the enumerated entry and not from the node.
func RunOnNodeLocalsKeys[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	locals LocalsKeyEnumerator,
	view V,
	check Check[V, NodeLocalsKeySubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeLocalsKeySubject {
		entries := locals.LocalsKeys(node.ID)
		out := make([]NodeLocalsKeySubject, 0, len(entries))
		for _, entry := range entries {
			out = append(out, newNodeLocalsKeySubject(
				entry.SourceFile, node.ID, entry.Value,
				documentSpecSegments(authoredKeyLocals, entry.Value)...,
			))
		}
		return out
	})
}

// RunOnNodeProvidesKeys invokes check once per key each node's Recipe provides,
// in node order and then provides order.
//
// The locator is .spec.provides.<key> in the Recipe's own file.
func RunOnNodeProvidesKeys[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	provides ProvidesKeyEnumerator,
	view V,
	check Check[V, NodeProvidesKeySubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeProvidesKeySubject {
		entries := provides.ProvidesKeys(node.ID)
		out := make([]NodeProvidesKeySubject, 0, len(entries))
		for _, entry := range entries {
			out = append(out, newNodeProvidesKeySubject(
				entry.SourceFile, node.ID, entry.Value,
				documentSpecSegments(authoredKeyProvides, entry.Value)...,
			))
		}
		return out
	})
}

// RunOnNodePipelineSteps invokes check once per step of each node's Recipe
// pipeline, in node order and then pipeline order.
//
// The locator is .spec.pipeline.<id> in the Recipe's own file. The pipeline is
// an ordered list whose entries carry an identifier, so the segment names that
// identifier rather than a position: a position moves when a step is inserted
// above it, and the identifier does not.
func RunOnNodePipelineSteps[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	steps PipelineStepEnumerator,
	view V,
	check Check[V, NodePipelineStepSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodePipelineStepSubject {
		entries := steps.PipelineSteps(node.ID)
		out := make([]NodePipelineStepSubject, 0, len(entries))
		for _, entry := range entries {
			out = append(out, newNodePipelineStepSubject(
				entry.SourceFile, node.ID, entry.Value,
				documentSpecSegments(authoredKeyPipeline, entry.Value)...,
			))
		}
		return out
	})
}

// RunOnNodeArtifacts invokes check once per artifact each node projects, in node
// order and then artifact order.
//
// The locator is .spec.artifacts.<name> in the Recipe's own file, where the
// authored key is the output filename.
func RunOnNodeArtifacts[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	artifacts ArtifactEnumerator,
	view V,
	check Check[V, NodeArtifactSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeArtifactSubject {
		entries := artifacts.Artifacts(node.ID)
		out := make([]NodeArtifactSubject, 0, len(entries))
		for _, entry := range entries {
			out = append(out, newNodeArtifactSubject(
				entry.SourceFile, node.ID, entry.Value,
				documentSpecSegments(authoredKeyArtifacts, entry.Value)...,
			))
		}
		return out
	})
}

// RunOnBoundaryProviderPaths invokes check once per output path written for one
// provider within one boundary, in boundary order, then provider order, then
// emission order.
//
// The locator names the boundary alone — .spec.boundaries.<boundary> — and that
// is the whole of what is authored here. A provider is selected rather than
// written at this site, and an emitted path is output rather than authoring, so
// neither contributes a segment. Both are still coordinates the subject carries,
// which is what lets a check raising about a particular path name it in its
// detail: coordinate state and locator segments answer different questions, and
// this kind is where the same value is legitimate as one and refused as the
// other.
func RunOnBoundaryProviderPaths[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	providers ProviderEnumerator,
	paths EmittedPathEnumerator,
	view V,
	check Check[V, BoundaryProviderPathSubject],
) ([]Objection, error) {
	var out []Objection
	for _, boundary := range boundaries.Boundaries() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		segments := documentSpecSegments(authoredKeyBoundaries, boundary.Value)
		for _, provider := range providers.Providers(boundary.Value) {
			for _, path := range paths.EmittedPaths(boundary.Value, provider) {
				subject := newBoundaryProviderPathSubject(
					boundary.SourceFile, boundary.Value, provider, path, segments...,
				)
				out = append(out, check.Run(view, subject)...)
			}
		}
	}
	return out, nil
}
