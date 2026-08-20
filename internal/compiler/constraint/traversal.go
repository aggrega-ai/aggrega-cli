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
//   - A coordinate whose authoring site is not fixed by the coordinates above it
//     carries that site itself, and the locator is built from what it carries —
//     see authoredSiteSegments. An attachment's configuration leaf is the case:
//     it is assembled from every scope reaching its node, so which of them wrote
//     any one leaf is settled by the fold and by nothing the traversal can see.
//
// Each shape below states the locator it builds as a template, and every one of
// them is bounded by the same rule: a segment has to be a key the dialect can
// address, so an authored key that itself contains the separator ends the locator
// at its container. The rule and what it costs a reader live with the assembly, at
// authoredLocator, rather than being restated per shape — the shapes whose names
// the authoring surface constrains to separator-free text are exact as written,
// and the two that name a filename or a mount path say so where they are declared.

// The authoring keys the locator dialect names. They are the keys the author
// actually writes, so they are declared once here rather than spelled at each
// use. Which keys a component's own site takes, and when, is the
// component-locator rule's question and is decided there; the key itself is
// declared here with the rest, because an author writes it on both of the
// documents that declare components.
const (
	authoredKeySpec       = "spec"
	authoredKeyUses       = "uses"
	authoredKeyFeatures   = "features"
	authoredKeyInject     = "inject"
	authoredKeyValues     = "values"
	authoredKeyInfra      = "infra"
	authoredKeyParameters = "parameters"
	authoredKeyLocals     = "locals"
	authoredKeyProvides   = "provides"
	authoredKeyPipeline   = "pipeline"
	authoredKeyArtifacts  = "artifacts"
	authoredKeyBoundaries = "boundaries"
	authoredKeySubsystems = "subsystems"
	authoredKeyComponents = "components"
	authoredKeyAggregates = "aggregates"
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

// The nesting axes. Each separator is the one the compiler composes that axis's
// path with, so a value handed to a traversal splits back into the levels the
// author wrote.
//
// The first two are the two authoring documents' own scopes, and both are
// assembled by nestedScopeSegments. The third is the composition axis, and it is
// paired here for the reason the type exists — so no call site pairs one axis's
// key with another's separator — while being assembled differently, by
// moduleAuthoredSegments. The difference is not a variant of one rule: a scope of
// the first two nests inside ONE document, so every level of its path is a key in
// that one file, whereas a module's levels are spread across one document per
// level and only the last of them is a key in the file the locator names.
var (
	boundaryNesting  = scopeNesting{containerKey: authoredKeyBoundaries, separator: "/"}
	subsystemNesting = scopeNesting{containerKey: authoredKeySubsystems, separator: "."}
	moduleNesting    = scopeNesting{containerKey: authoredKeyAggregates, separator: "."}
)

// componentInnerSegments returns the segments naming an element authored inside
// a component instance's block: the component's own segments followed by tail.
//
// It returns no segments when the component has none, and that propagation is
// the point of the helper rather than a detail of it: an element authored inside
// a block is named relative to that block, so without the block there is nothing
// to name it relative to and emitting the tail alone would produce a path —
// ".uses.database" — that no file contains. An empty locator routes a consumer to
// file-level positioning instead, which is the mechanical form of not inventing
// a locator.
//
// Both arms of the component rule name an authored site, so an empty base does
// not arise from it as things stand. The guard is what keeps that from being a
// property callers rely on: it belongs to the composition rather than to any one
// producer, and a producer that could not name its component would otherwise
// have its tail emitted rootless.
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

// pillarLeafPathSeparator is what a configuration pillar's leaf path is composed
// with: the dialect the pillar tree is read and written through keys a leaf by a
// flat dotted path, with one part per level of the pillar's nesting.
//
// It is spelled here rather than taken from the locator's own separator. The two
// agree on a character and answer different questions — one is how a leaf path
// was composed, the other how a locator is written — so a change to either must
// be free to leave the other alone.
const pillarLeafPathSeparator = "."

// pillarLeafSegments returns the segments one configuration leaf contributes:
// one per level of the leaf's own path.
//
// A leaf names a path into its pillar and not a single key, so handing it over
// whole would offer the locator assembly one segment standing for several keys —
// and that assembly refuses a segment it cannot tell from several, because a
// locator has to have exactly one reading. Splitting it here is what keeps the
// depth the leaf genuinely names: every level is a key the document spells, one
// under the next, so the locator stays a path the document can be walked by.
//
// This is the one place a value reaching locator assembly is a path rather than a
// key. Every other coordinate the traversals contribute is a single authored key,
// which is why the split belongs at this site instead of in the assembly: only
// the site that knows which of the two it holds can say so.
func pillarLeafSegments(leafPath string) []string {
	return strings.Split(leafPath, pillarLeafPathSeparator)
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
// component in a subsystem the author declared names that subsystem level by
// level under the container's own key before naming the component.
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

// RunOnEdges invokes check once per connection, in the order edges returns them.
//
// The edge set is walked flat rather than per node or per socket, because the
// derived kind carries one coordinate and the fact keyed by it takes the node,
// the socket and the alias together: one order over the whole set, promised by
// one enumerator.
//
// An edge's locator follows the authoring form the alias came from, and the two
// forms name different depths because they are different text. The long form
// writes an alias per connection as a key, so the locator names it:
// .spec.components.<name>.uses.<socket>.<alias>. The short form lists targets as
// array elements and the alias is the target's own name, which no key in the file
// spells — so the alias contributes no segment and the locator stays
// .spec.components.<name>.uses.<socket>, which is what the author wrote. The
// coordinate is carried in full either way: a locator names an authored site and
// a coordinate keys a fact, so a segment the file does not contain is dropped
// while the alias itself still reaches the check.
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
		aliasSegment := edge.Alias
		if edge.SynthesizedAlias {
			aliasSegment = ""
		}
		subject := newEdgeSubject(
			edge.Node.SourceFile, edge.Node.ID, edge.Socket, edge.Alias,
			componentInnerSegments(edge.Node, authoredKeyUses, edge.Socket, aliasSegment)...,
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
//
// What <key> spells is the mechanism's own business — an environment variable
// name under one, a mount path under another — so it is one of the two coordinates
// that can carry the dialect's separator. A key that does carries it as ONE
// authored key, which the dialect cannot address, so the locator names the
// mechanism's block and stops. The key still reaches the author, as the subject's
// coordinate and as a value a rule over this kind has to raise: a locator naming
// the block is a position an author can follow, and the key is what says which
// entry inside it. Which of a rule's values carries a caption and which stands
// bare is in [DetailValue].
//
// A rule raises the key whether or not this particular one carries the separator,
// because the rule cannot see which case it is in and a reader told the same thing
// twice is better served than one told it never.
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

// pillarNesting reports the axis an attachment pillar's document nests its
// scopes with, and whether the pillar is one this rule can name at all.
//
// This is the one place the mapping lives. An attachment's two pillars are
// written on two different documents, and each document nests with its own
// container key: the values the developer wrote sit in the application, which
// nests subsystems, and the infrastructure configuration the operator wrote
// sits on the deployment, which nests boundaries. The file each pillar carries
// says the same thing about the same pillar, so the two are one fact read twice
// and a disagreement between them would be a locator naming a key in a document
// that does not contain it.
//
// A pillar this rule does not know yields no axis rather than a default one,
// and the difference is not defensive. A zero axis would join a scope path with
// an empty separator, which splits that path into one level per character and
// produces a locator of nonsense segments — a shape a reader would follow. No
// axis produces no locator, which routes a consumer to file-level positioning
// instead.
func pillarNesting(pillar string) (scopeNesting, bool) {
	switch pillar {
	case authoredKeyValues:
		return subsystemNesting, true
	case authoredKeyInfra:
		return boundaryNesting, true
	default:
		return scopeNesting{}, false
	}
}

// RunOnNodeAttachmentPillarLeaves invokes check once per leaf of each pillar of
// each attachment's configuration, in node order, then attachment order, then
// pillar order, then leaf order.
//
// A pillar is itself one of the keys an attachment's block is written under, so
// it contributes that segment directly and no further key is spelled ahead of
// it. The pillar is a coordinate here and the slot is a coordinate in its own
// right elsewhere: the two are not the same axis.
//
// The file comes from the enumerated pillar and not from the node, because an
// attachment's two pillars are written on two different documents: the values
// the developer wrote sit in the Aggregate that declares the component, and the
// infrastructure configuration the operator wrote sits on the Release, whose
// attachment block is the only one that admits it. Taking the node's file would
// pair one of the two with a document that cannot hold the key the locator
// names.
//
// # The path comes from the leaf's own site, and not from the node
//
// A node's own segments name one shape — a component's block, in the scope the
// node was reached through — and an attachment's configuration is assembled
// from every scope that reaches that node, so the shape is wrong for most of
// what lands there. Three sites reach one leaf and only the first of them is
// the node's own: a component's own attachment block; a component's entry
// inside a scope, which the deployment keys by the identifier the composition
// gave the node rather than by the name its own document wrote; and a scope's
// own block, which names no component at all and whose leaves therefore have no
// component segment to sit under. Naming the node's shape for the second spells
// a component key under no scope, and for the third invents a component the
// entry never mentioned — both of them paths that read plausibly and resolve
// nowhere, which is worse than no path at all.
//
// So the site travels with the leaf, from the fold that settled which entry won
// it. It is settled there and nowhere else: the fold merges every scope's
// contribution into one entry, leaf by leaf, so a scope-wide entry and a
// component-scoped one on the same attachment each keep the leaves the other
// did not mention. Recovering that afterwards would mean re-running the
// precedence over the documents, which is a second answer to a question already
// answered.
//
// # A composed component key costs the locator every segment under it
//
// One of the shapes above spells the component with a key a deployment wrote,
// and a deployment addresses a component a composition contributed by the
// qualified identifier the composition gave it — which carries the separator the
// locator dialect divides its segments with. The dialect cannot address such a
// key, so the locator stops at the components block and gives up that segment
// and every segment after it: the attachment, the pillar and the leaf go with
// it, however ordinary each of those is on its own. The other two shapes lose
// nothing — a component's own block is keyed by the short name its own document
// wrote, and an entry written scope-wide names no component at all.
//
// So a rule over this kind names the attachment, the pillar and the leaf in its
// detail, captioned, because on that shape the locator carries none of them and
// the block alone cannot say which entry under it is at fault. It names them
// whether or not this particular component key carries the separator: the rule
// cannot see which case it is in, and a reader told the same thing twice is
// better served than one told it never. Which of a rule's values carries a
// caption is in [DetailValue].
//
// The component key itself is not among what a rule can name here. It is not a
// coordinate this kind carries — the coordinates are the node, the attachment,
// the pillar and the leaf — and a kind exposes one accessor per coordinate and
// no more, so the authored key the entry sits under is read as a fact keyed by
// those coordinates or not at all.
//
// What a rule does name is a requirement on the rule and not something the
// framework supplies. What a rule's registered wording already names is the
// rule's own, so which of its values still needs saying is too.
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
				nesting, named := pillarNesting(pillar.Value)
				for _, leaf := range leaves.AttachmentPillarLeaves(node.ID, attachment, pillar.Value) {
					var segments []string
					if named {
						tail := append(
							[]string{authoredKeyFeatures, attachment, pillar.Value},
							pillarLeafSegments(leaf.Value)...,
						)
						segments = authoredSiteSegments(nesting, leaf.Site, tail...)
					}
					out = append(out, newNodeAttachmentPillarLeafSubject(
						pillar.SourceFile, node.ID, attachment, pillar.Value, leaf.Value,
						segments...,
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
// The locator is .spec.artifacts.<name> in the Recipe's own file, where <name>
// is the output name the entry resolves to — the same value the subject carries
// as its artifact coordinate. An entry written inline and one supplied by an
// imported file each resolve to their own authored key, so for those forms the
// two are one value; one supplied by a library bundle resolves to a name per file
// the bundle holds, each nested under that key.
//
// An output name is a filename, so on this axis the name ordinarily carries the
// dialect's separator — and it carries it as ONE authored key, which the dialect
// cannot address. The locator then names the artifacts block and stops, which is
// what a rule over this kind has to make up for: it names the artifact in its
// detail, because the block says where to look and only the detail can say which
// artifact inside it, since the position is a path and a filename is a name. It
// names it CAPTIONED, because a filename standing bare beside another quoted
// value is not something a reader can tell apart from it — see [DetailValue] for
// which of a rule's values carries a caption.
//
// That is a requirement on the rule and not something the framework can supply.
// What a rule's registered wording already names is the rule's own, so which of
// its values still needs saying is too.
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

// RunOnNodeAttachmentArtifacts invokes check once per artifact the FeatureRecipe
// each attachment binds on a node projects, in node order, then attachment order,
// then artifact order.
//
// The locator is .spec.artifacts.<name> in the FeatureRecipe's own file, which is
// a different document from the Aggregate the attachment was written in — so the
// file comes from the enumerated entry and not from the node, exactly as it does
// for the coordinates a node's Recipe authors.
//
// The name is the output name the entry resolves to, so on this axis it ordinarily
// carries the locator dialect's separator, and it carries it as ONE authored key
// the dialect cannot address. The locator then names the artifacts block and stops
// — the same constraint [RunOnNodeArtifacts] carries and for the same reason, and
// this kind inherits it rather than reaching for a deeper path. So a rule over
// this kind owes the same thing a rule over the node's own artifacts owes: it
// names the artifact in its detail, captioned, because the block says where to
// look and only the detail can say which artifact inside it.
//
// The attachment reaches the subject as a coordinate without reaching the locator,
// and that is not an inconsistency: the locator names the document the author
// edits to change the entry, while the coordinate is what tells this artifacts
// block from the node's own and from another attachment's when a fact is keyed.
func RunOnNodeAttachmentArtifacts[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	artifacts AttachmentArtifactEnumerator,
	view V,
	check Check[V, NodeAttachmentArtifactSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentArtifactSubject {
		var out []NodeAttachmentArtifactSubject
		for _, attachment := range attachments.Attachments(node.ID) {
			for _, entry := range artifacts.AttachmentArtifacts(node.ID, attachment) {
				out = append(out, newNodeAttachmentArtifactSubject(
					entry.SourceFile, node.ID, attachment, entry.Value,
					documentSpecSegments(authoredKeyArtifacts, entry.Value)...,
				))
			}
		}
		return out
	})
}

// RunOnBoundaryAttachmentArtifacts invokes check once per artifact the
// FeatureRecipe each boundary-scope attachment binds projects, in boundary order,
// then attachment order, then artifact order.
//
// The locator is .spec.artifacts.<name> in the FeatureRecipe's own file, on the
// same reasoning as the node-carried attachment's artifacts: the entry is authored
// in the FeatureRecipe, which is the document an author edits to change it, and
// that document is neither the Release the attachment was declared in nor any
// component's Recipe.
//
// So the boundary contributes no locator segment here, and that is the point
// rather than an omission — the reading [RunOnBoundaryAttachmentPipelineSteps]
// states for the other boundary-rooted kind whose locator names a FeatureRecipe: a
// boundary key spliced onto a FeatureRecipe-rooted locator would name a path that
// document does not have. The boundary remains a coordinate, because that is what
// tells this artifacts block from another boundary's when a fact is keyed.
//
// The name is the output name the entry resolves to, so the locator names the
// artifacts block and stops, exactly as it does on the two node-rooted artifact
// kinds. A rule over this kind therefore owes the artifact in its detail,
// captioned, for the reason set out at [RunOnNodeArtifacts].
func RunOnBoundaryAttachmentArtifacts[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	attachments BoundaryAttachmentEnumerator,
	artifacts BoundaryAttachmentArtifactEnumerator,
	view V,
	check Check[V, BoundaryAttachmentArtifactSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryAttachmentArtifactSubject {
			var out []BoundaryAttachmentArtifactSubject
			for _, attachment := range attachments.BoundaryAttachments(boundary.Value) {
				for _, entry := range artifacts.BoundaryAttachmentArtifacts(boundary.Value, attachment) {
					out = append(out, newBoundaryAttachmentArtifactSubject(
						entry.SourceFile, boundary.Value, attachment, entry.Value,
						documentSpecSegments(authoredKeyArtifacts, entry.Value)...,
					))
				}
			}
			return out
		})
}

// RunOnBoundaryProviderPaths invokes check once per output path written for one
// provider within one boundary, in boundary order, then provider order, then
// path order.
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

// RunOnModules invokes check once per sub-module the composition brought in, in
// the order modules returns them.
//
// The locator is .spec.aggregates.<name> in the document that DECLARES the
// module, under the LOCAL name written there — the last segment of the dotted
// prefix, because a nested module is an entry in its importing module's own
// document and that document spells it with the one name it has inside that
// scope. Spelling it with the dotted prefix would name a key no file contains.
//
// It is not the nested-scope shape the two authoring documents' own scopes take,
// and the difference is the reason it does not go through that helper: a
// subsystem and a boundary nest INSIDE one document, so every level of the path
// is a key in that one file, while a module's levels are spread across one
// document per level. Repeating the container key here would name
// .spec.aggregates.data.aggregates.inner in a file that contains only the inner
// half.
func RunOnModules[V View](
	cancelled Cancellation,
	modules ModuleEnumerator,
	view V,
	check Check[V, ModuleSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, modules.Modules(), view, check.Run,
		func(module AuthoredCoordinate) []ModuleSubject {
			return []ModuleSubject{newModuleSubject(
				module.SourceFile, module.Value,
				moduleAuthoredSegments(module.Value)...,
			)}
		})
}

// moduleAuthoredSegments returns the segments naming where a sub-module was
// declared: the `aggregates` entry in its importing document, under the local
// name that document wrote.
//
// An empty path yields no segments, the mechanical form of not inventing a
// locator: a module with no path is one this rule cannot name, and an empty
// locator routes a consumer to file-level positioning rather than to a key that
// is not there.
func moduleAuthoredSegments(modulePath string) []string {
	if modulePath == "" {
		return nil
	}
	levels := strings.Split(modulePath, moduleNesting.separator)
	return documentSpecSegments(moduleNesting.containerKey, levels[len(levels)-1])
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
