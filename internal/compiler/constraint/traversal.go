// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import "strings"

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
// A subject's locator is assembled here and never by a check. The shapes below
// cover every kind, and they are left uncounted here on purpose: a number beside
// the list is a second statement of the list's own length, and it is the number
// that goes stale when a shape is added.
//
//   - A coordinate authored inside a component instance's block nests under the
//     component's own segments. Its locator is empty when the component's is —
//     see componentInnerSegments.
//   - A coordinate authored at a document's spec level names that block
//     directly, whatever subsystem the node was reached through.
//   - A coordinate authored inside a nesting scope names every level of that
//     nesting, with the container's own key repeated between the levels — see
//     nestedScopeSegments. Both authoring documents declare such a scope, so both
//     go through that shape.
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
	authoredKeySubsystems = "subsystems"
)

// scopeNesting describes one authoring axis whose scopes nest: the key the
// author writes each level under, and the separator the compiler joins that
// axis's path with.
//
// The two travel together because they are two facts about one axis, and a call
// site free to pair them itself could name one axis's key alongside the other's
// separator. That mistake does not fail: splitting a path on a separator it does
// not contain yields the whole path as one level, so the locator would carry a
// single flattened segment — the exact shape nestedScopeSegments exists to
// avoid, arrived at through the helper meant to prevent it.
type scopeNesting struct {
	containerKey string
	separator    string
}

// The two nesting axes, one per authoring document. Each separator is the one
// the compiler composes that axis's path with, so a value handed to a traversal
// splits back into the levels the author wrote.
var (
	boundaryNesting  = scopeNesting{containerKey: authoredKeyBoundaries, separator: "/"}
	subsystemNesting = scopeNesting{containerKey: authoredKeySubsystems, separator: "."}
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

// nestedScopeSegments returns the segments naming an element authored inside a
// scope that nests: the spec, then the container's key and one path part for
// every level of the nesting, then tail.
//
// A nesting scope's path is one joined string — "a.b" for the subsystem named b
// inside the subsystem named a, "a/b" for the boundary named b inside the
// boundary named a — and neither authoring document spells it that way: each
// level sits under the container's own key again, so the site of the element
// inside that scope is .spec.<container>.a.<container>.b.<tail>. Joining the
// path in as a single segment would name .spec.<container>.a.b.<tail> — worse,
// .spec.<container>.a/b.<tail> on the axis whose separator is a slash — a key no
// file contains, so the container's key is repeated between the levels here.
//
// It serves both authoring documents, which is why the axis arrives as a value
// rather than as a hard-coded key and separator. The two axes nest identically
// and differ only in what they are spelled with, so one rule covers both and a
// second copy of it could not drift from this one.
//
// scopePath is the coordinate value the enumerator handed over, and an empty one
// yields no segments at all — the mechanical form of not inventing a locator. A
// scope with no path is a scope this rule cannot name, and an empty locator
// routes a consumer to file-level positioning instead of to a key that is not
// there.
func nestedScopeSegments(nesting scopeNesting, scopePath string, tail ...string) []string {
	if scopePath == "" {
		return nil
	}
	levels := strings.Split(scopePath, nesting.separator)
	out := make([]string, 0, 1+2*len(levels)+len(tail))
	out = append(out, authoredKeySpec)
	for _, level := range levels {
		out = append(out, nesting.containerKey, level)
	}
	return append(out, tail...)
}

// runOverNodes is the one place the node nesting lives. Most enumerated kinds
// are reached within a node, and the outer walk is identical for all of them:
// node order outermost, the per-node subjects in the order their own enumerator
// returned, everything raised accumulated in that order.
//
// subjects yields the subjects of one node rather than running the check itself,
// so a traversal contributes only how a kind's coordinates and provenance are
// built and never how invocation works.
//
// The cancellation read is here for the same reason the nesting is: every kind
// reached within a node would otherwise carry its own copy of it, and copies of
// one rule are chances for one of them to drift. Neither the share of kinds nor
// the number of copies avoided is counted here, because both move whenever a
// kind is declared and a stale count reads as a fact about the tree.
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

// runOverAuthoredScopes is the one place the authored-scope nesting lives, and it
// is the exact sibling of runOverNodes for the kinds rooted at a scope rather
// than at a node: scope order outermost, the per-scope subjects in the order
// their own enumerators returned, everything raised accumulated in that order.
//
// It exists for the same reason runOverNodes does rather than because several
// callers happen to share a loop. The cancellation contract — read the signal as
// the first statement of the outermost loop body, and hand back no objections
// when it turns — is one rule, and a second copy of it would agree on the day it
// was written and be free to drift afterwards with nothing comparing them.
//
// It takes the enumerated scopes rather than an enumerator, which is what lets
// one loop serve both authoring documents. A boundary and a subsystem are
// separate axes with separate enumerators, because each enumerator names the axis
// it supplies and one covering both would hand the framework scopes from two
// documents under a single key — but the walk over either is the same walk, and it
// is written once here.
func runOverAuthoredScopes[V any, S Subject](
	cancelled Cancellation,
	scopes []AuthoredCoordinate,
	view V,
	run func(V, S) []Objection,
	subjects func(AuthoredCoordinate) []S,
) ([]Objection, error) {
	var out []Objection
	for _, scope := range scopes {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		for _, subject := range subjects(scope) {
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

// RunOnNodeAttachmentPipelineSteps invokes check once per step of the pipeline
// each attachment's FeatureRecipe carries, in node order, then attachment order,
// then pipeline order.
//
// The locator is .spec.pipeline.<id> in the FeatureRecipe's own file, which is a
// different document from the Aggregate the attachment was written in — so the
// file comes from the enumerated entry and not from the node, exactly as it does
// for the coordinates a node's Recipe authors. The segment names the step's
// declared identifier rather than a position, because a position moves when a
// step is inserted above it.
//
// The attachment reaches the subject as a coordinate without reaching the
// locator, and that is not an inconsistency: the locator names the document the
// author edits to change the step, while the coordinate is what tells this
// pipeline from the node's own and from another attachment's when a fact is
// keyed.
func RunOnNodeAttachmentPipelineSteps[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	steps AttachmentPipelineStepEnumerator,
	view V,
	check Check[V, NodeAttachmentPipelineStepSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentPipelineStepSubject {
		var out []NodeAttachmentPipelineStepSubject
		for _, attachment := range attachments.Attachments(node.ID) {
			for _, entry := range steps.AttachmentPipelineSteps(node.ID, attachment) {
				out = append(out, newNodeAttachmentPipelineStepSubject(
					entry.SourceFile, node.ID, attachment, entry.Value,
					documentSpecSegments(authoredKeyPipeline, entry.Value)...,
				))
			}
		}
		return out
	})
}

// RunOnBoundaryAttachmentPipelineSteps invokes check once per step of the
// pipeline each boundary-scope attachment's FeatureRecipe carries, in boundary
// order, then attachment order, then pipeline order.
//
// The locator is .spec.pipeline.<id> in the FeatureRecipe's own file, on the same
// reasoning as the node-carried attachment pipeline: the step is authored in the
// FeatureRecipe, which is the document an author edits to change it, and that
// document is neither the Release the attachment was declared in nor any
// component's Recipe.
//
// So the boundary contributes no locator segment here, and that is the point
// rather than an omission. It is the one boundary-rooted kind whose locator names
// a different document from the one the boundary is written in, which is why it
// carries no counterpart to the nesting the other two express: a boundary key
// spliced onto a FeatureRecipe-rooted locator would name a path that document
// does not have. The boundary remains a coordinate, because that is what tells
// this pipeline from another boundary's when a fact is keyed.
func RunOnBoundaryAttachmentPipelineSteps[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	attachments BoundaryAttachmentEnumerator,
	steps BoundaryAttachmentPipelineStepEnumerator,
	view V,
	check Check[V, BoundaryAttachmentPipelineStepSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryAttachmentPipelineStepSubject {
			var out []BoundaryAttachmentPipelineStepSubject
			for _, attachment := range attachments.BoundaryAttachments(boundary.Value) {
				for _, entry := range steps.BoundaryAttachmentPipelineSteps(boundary.Value, attachment) {
					out = append(out, newBoundaryAttachmentPipelineStepSubject(
						entry.SourceFile, boundary.Value, attachment, entry.Value,
						documentSpecSegments(authoredKeyPipeline, entry.Value)...,
					))
				}
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
// The locator names the boundary alone — .spec.boundaries.<boundary>, with the
// container's key repeated for each level of a nested boundary — and that is the
// whole of what is authored here. A provider is selected rather than written at
// this site, and an emitted path is output rather than authoring, so neither
// contributes a segment. Both are still coordinates the subject carries, which is
// what lets a check raising about a particular path name it in its detail:
// coordinate state and locator segments answer different questions, and this kind
// is where the same value is legitimate as one and refused as the other.
func RunOnBoundaryProviderPaths[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	providers ProviderEnumerator,
	paths EmittedPathEnumerator,
	view V,
	check Check[V, BoundaryProviderPathSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryProviderPathSubject {
			segments := nestedScopeSegments(boundaryNesting, boundary.Value)
			var out []BoundaryProviderPathSubject
			for _, provider := range providers.Providers(boundary.Value) {
				for _, path := range paths.EmittedPaths(boundary.Value, provider) {
					out = append(out, newBoundaryProviderPathSubject(
						boundary.SourceFile, boundary.Value, provider, path, segments...,
					))
				}
			}
			return out
		})
}

// RunOnBoundaryAttachments invokes check once per Feature attachment declared on
// a boundary itself, in boundary order and then attachment order.
//
// The locator is .spec.boundaries.<boundary>.features.<attachment>, with the
// container's key repeated for each level of a nested boundary, which is the site
// the operator wrote. It deliberately names the attachment rather than the
// components the attachment reaches: those are the many places it takes effect,
// and only one of them is a place anyone can edit.
func RunOnBoundaryAttachments[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	attachments BoundaryAttachmentEnumerator,
	view V,
	check Check[V, BoundaryAttachmentSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryAttachmentSubject {
			names := attachments.BoundaryAttachments(boundary.Value)
			out := make([]BoundaryAttachmentSubject, 0, len(names))
			for _, attachment := range names {
				out = append(out, newBoundaryAttachmentSubject(
					boundary.SourceFile, boundary.Value, attachment,
					nestedScopeSegments(
						boundaryNesting, boundary.Value, authoredKeyFeatures, attachment,
					)...,
				))
			}
			return out
		})
}

// RunOnSubsystemAttachments invokes check once per Feature attachment declared on
// a subsystem itself, in subsystem order and then attachment order.
//
// The locator is .spec.subsystems.<subsystem>.features.<attachment>, with the
// container's key repeated for each level of a nested scope, which is the site the
// author wrote. It deliberately names the attachment rather than the components
// the attachment reaches: those are the many places it takes effect, and only one
// of them is a place anyone can edit. Keying it after that projection would drop
// the scope from the locator and would ask nothing at all of a scope holding no
// components.
//
// Unlike a component instance, a scope reached here always has an expressible
// authored site, so this traversal carries no counterpart to the component
// locator's synthesized-container rule. Normalization synthesizes a scope to hold
// an application's top-level components and gives it no attachment surface of its
// own, and the authoring surface declares attachments on a component or on a scope
// and nowhere else — so every attachment this traversal reaches was written inside
// a scope the author named.
func RunOnSubsystemAttachments[V View](
	cancelled Cancellation,
	subsystems SubsystemEnumerator,
	attachments SubsystemAttachmentEnumerator,
	view V,
	check Check[V, SubsystemAttachmentSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, subsystems.Subsystems(), view, check.Run,
		func(subsystem AuthoredCoordinate) []SubsystemAttachmentSubject {
			names := attachments.SubsystemAttachments(subsystem.Value)
			out := make([]SubsystemAttachmentSubject, 0, len(names))
			for _, attachment := range names {
				out = append(out, newSubsystemAttachmentSubject(
					subsystem.SourceFile, subsystem.Value, attachment,
					nestedScopeSegments(
						subsystemNesting, subsystem.Value, authoredKeyFeatures, attachment,
					)...,
				))
			}
			return out
		})
}
