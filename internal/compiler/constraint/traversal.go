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
	authoredKeyAssertions = "assertions"
	authoredKeySchema     = "schema"

	// authoredKeyOutputLabels and authoredKeyOutputComponentLabels are the two
	// keys a Recipe writes its output-label declarations under. They sit here
	// with the rest, and they are the second place in this block whose values
	// are also carried as a coordinate: the exported block vocabulary below is
	// declared from them rather than beside them, so the word a rule reports and
	// the segment a finding is positioned at cannot come to disagree.
	authoredKeyOutputLabels          = "outputLabels"
	authoredKeyOutputComponentLabels = "outputComponentLabels"

	// authoredKeyMappings and authoredKeyAnnotations are the two keys a
	// Manifest's annotation registry is written under. They sit here with the
	// rest rather than beside the one traversal that spells them, because the
	// block is where an authoring key is declared once and the alternative is a
	// second spelling of the same key appearing the first time a second rule
	// names it.
	authoredKeyMappings    = "mappings"
	authoredKeyAnnotations = "annotations"

	// authoredKeyType and authoredKeyName are the two keys a Manifest's
	// mapping tier writes its entries under. They sit here with the rest for
	// the reason the pair above does, and they are the one place in this block
	// whose values are also read back OUT of a locator: the exported tier and
	// axis vocabulary below is declared from these constants rather than
	// beside them, so the words a rule reads off a subject and the segments a
	// locator is assembled from cannot come to disagree.
	authoredKeyType = "type"
	authoredKeyName = "name"
)

// The mapping tiers a Manifest's mapping table is written under, and the axes
// each tier keys on. A rule reading [DocumentMappingEntrySubject.MappingTier] or
// [DocumentMappingEntrySubject.MappingAxis] compares against these rather than
// against a literal of its own.
//
// The sets are closed, and closed because the authoring surface closes them: a
// Manifest's mappings block accepts the two tier keys and no other, and a tier
// accepts the two axis keys and no other. A third of either would be a change to
// what a Manifest may say.
//
// Each is declared from the authoring key the locator is assembled from rather
// than spelled again, so the word a rule branches on and the segment a finding is
// positioned at are one value. Two spellings would agree the day they were
// written and be free to drift afterwards, with nothing comparing them.
const (
	// MappingTierComponents is the tier whose entries map component types and
	// component instance names onto Recipes.
	MappingTierComponents = authoredKeyComponents

	// MappingTierFeatures is the tier whose entries map feature types and
	// feature instance names onto FeatureRecipes.
	MappingTierFeatures = authoredKeyFeatures

	// MappingAxisType is the axis whose keys are types, which routes every
	// instance of the type it names.
	MappingAxisType = authoredKeyType

	// MappingAxisName is the axis whose keys are instance names, which routes
	// the one instance it names.
	MappingAxisName = authoredKeyName
)

// The two blocks a Recipe declares output-label keys in. A rule reading
// [DocumentOutputLabelSubject.OutputLabelBlock] compares against these rather
// than against a literal of its own, and a caller presenting a block names one
// of them rather than spelling it.
//
// The set is closed, and closed because the authoring surface closes it: a
// Recipe declares label keys destined for its rendered output at these two keys
// and no other. A third would be a change to what a Recipe may say.
//
// Each is declared from the authoring key the locator is assembled from rather
// than spelled again, so the word a diagnostic names the block by and the
// segment a finding is positioned at are one value. Two spellings would agree
// the day they were written and be free to drift afterwards, with nothing
// comparing them.
const (
	// OutputLabelBlockLiteral is the block whose entries are label keys with
	// the values written in place beside them.
	OutputLabelBlockLiteral = authoredKeyOutputLabels

	// OutputLabelBlockPromoted is the block whose entries name label keys whose
	// values are taken from the component instance being rendered.
	OutputLabelBlockPromoted = authoredKeyOutputComponentLabels
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

// JoinBoundaryScope composes the scope path of one boundary from its parent's
// path and its own name, in the spelling [AuthoredSite.ScopePath] takes on the
// Release's nesting axis.
//
// It is exported because an enumerator supplying a site on that axis has to
// compose the path the locator assembly will split back apart, and the separator
// belongs to the axis rather than to any one producer: a caller spelling it
// itself would be free to pick a different character from the one this package
// splits on, and nothing would compare the two.
//
// A boundary the compiler synthesized contributes no level and collapses the path
// to empty, which is the rule [AuthoredSite.ScopePath] declares: such a boundary
// is not a scope any file spells, so an entry inside one is named at the
// top-level shape the author did write. That collapse is what makes this a
// different question from the boundary path a Release's OUTPUT is routed by,
// which names the synthesized boundary like any other because it is routing
// artifacts rather than naming a key. The two must be free to answer differently,
// and they do.
func JoinBoundaryScope(parentPath, name string, synthesized bool) string {
	if synthesized {
		return ""
	}
	if parentPath == "" {
		return name
	}
	return parentPath + boundaryNesting.separator + name
}

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
//
// Which BLOCK the locator is rooted at comes from the edge's origin, through the
// rule [connectionSite] states. An enumeration whose edges were all drawn by the
// consuming component's own dependency block records no origin and gets the
// shape above; an enumeration that yields edges an attachment or the Release
// drew records one, and those name the block their author wrote rather than a
// `uses` block the consuming component does not have.
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
		file, segments := connectionSite(
			edge.Node, edge.Origin, edge.Socket, edge.Alias, edge.SynthesizedAlias)
		subject := newEdgeSubject(
			file, edge.Node.ID, edge.Socket, edge.Alias, segments...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out, nil
}

// originNesting is the scope nesting an origin's site path is spelled with,
// and whether the document is one this rule can name at all.
//
// It is the sibling of [pillarNesting] and exists for the same reason: the axis
// a scope path belongs to is settled by what is being named, and for an origin
// that is which document its block sits in. A document this rule does not know
// yields no nesting, which routes the finding to file-level positioning rather
// than to a path spelled with the wrong container key.
func originNesting(document string) (scopeNesting, bool) {
	switch document {
	case AuthoringDocumentAggregate:
		return subsystemNesting, true
	case AuthoringDocumentRelease:
		return boundaryNesting, true
	default:
		return scopeNesting{}, false
	}
}

// connectionSite is the file and the locator segments naming where a connection
// was authored, given the origin that drew it.
//
// It is one rule shared by the two traversals that walk connections, because
// both name the same authored site and a second copy would be free to spell it
// differently. Which of the two a rule declares turns on what the rule reads,
// not on where its finding points.
//
// Three shapes, and the first is the one every connection had before origins
// were tracked. An origin nothing recorded, and an origin that is the consuming
// component's own dependency block, both name that block — which is the site the
// author wrote in both cases, since a component's own connection is written
// there and an untracked one is not known to be written anywhere else. They take
// the node's own segments rather than the recorded origin's site, so the two
// share one path instead of two that agree: a recorded component origin's site
// is [ComponentSite] over the same three values the node instance carries, and
// reading it here would be a second route to one answer for no gain. An
// enumeration that records no origin has no other route in any case.
//
// A
// connection an attachment drew names that attachment's own block, at the site
// the attachment was declared: a component's own attachment block, a scope's
// block in either document, or a component's entry inside the Release. A
// connection the Release's edge surface drew names that surface.
//
// The alias contributes a segment only when the author wrote it as a key, which
// is the rule [RunOnEdges] states in full: the short authoring form lists targets
// as array elements and gives each connection the target's own name, so no key in
// the file spells it.
//
// An origin whose site the enumeration could not record yields no segments,
// which is the mechanical form of not inventing a locator.
func connectionSite(
	node NodeInstance,
	origin ConnectionOrigin,
	socket, alias string,
	synthesizedAlias bool,
) (string, []string) {
	aliasSegment := alias
	if synthesizedAlias {
		aliasSegment = ""
	}
	if origin.Kind == "" || origin.Kind == OriginComponent {
		return node.SourceFile,
			componentInnerSegments(node, authoredKeyUses, socket, aliasSegment)
	}
	nesting, named := originNesting(origin.Document)
	if !named {
		return origin.SourceFile, nil
	}
	var tail []string
	if origin.Kind == OriginAttachment {
		tail = append(tail, authoredKeyFeatures, origin.Attachment)
	}
	tail = append(tail, authoredKeyUses, socket, aliasSegment)
	return origin.SourceFile, authoredSiteSegments(nesting, origin.Site, tail...)
}

// RunOnOriginatedConnections invokes check once per connection together with the
// origin that drew it, in the order the enumerator returns them.
//
// The set is walked flat rather than per node or per origin, because the derived
// kind carries one coordinate whose key is the whole of the node, the origin and
// the connection: one order over the whole set, promised by one enumerator.
//
// The locator names the origin's own block and not the consuming component's,
// through the rule [connectionSite] states — which is why this traversal exists
// alongside [RunOnEdges] rather than instead of it. A rule reading what an author
// WROTE on a connection has to be invoked once per authored connection, and two
// connections sharing a node, a socket and an alias are two authored blocks.
func RunOnOriginatedConnections[V View](
	cancelled Cancellation,
	connections OriginatedConnectionEnumerator,
	view V,
	check Check[V, OriginatedConnectionSubject],
) ([]Objection, error) {
	var out []Objection
	for _, connection := range connections.OriginatedConnections() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		file, segments := connectionSite(
			connection.Node, connection.Origin,
			connection.Socket, connection.Alias, connection.SynthesizedAlias,
		)
		subject := newOriginatedConnectionSubject(
			file, connection.Node.ID,
			connection.Origin.Kind, connection.Origin.Attachment,
			connection.Socket, connection.Alias, connection.Target,
			segments...,
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

// RunOnNodeAttachmentSockets invokes check once per socket an attachment WIRES,
// in node order, then attachment order, then socket order.
//
// The axis is the wired set and never the set the attached Feature declares —
// the enumerator's own contract, stated there and not restated here. What the
// Feature declares is a fact keyed by its type, which a rule over one of these
// sockets reads and compares the socket it was invoked for against.
//
// The locator is .spec.components.<name>.features.<attachment>.uses.<socket>,
// the entry the author wrote inside the attachment's own dependency block. The
// socket names come from the attached Feature, which is a separate authoring
// surface from the component's own dependency block — that is what makes the axis
// keyed per attachment as well as per node, since the same socket name means
// different things under two attachments of different Features.
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
//
// No production path invokes it, and the reason is granularity rather than an
// omission. The kind exists because a fact is keyed by it — one configuration
// pillar as the author wrote it — and the rule reading that fact declares the
// node kind and asks for a whole named pillar at once, so it decides per node
// and never per leaf. This entry is what keeps the kind reachable: a kind with
// no traversal is a kind a check can declare and nothing will ever invoke.
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
//
// No production path invokes it, and the kind's fact is nonetheless read: the
// corpus the view-narrowing read is proven against declares a check over this
// kind, reading a parameter a node resolved through the narrowest view that
// suffices for it. That check is the control which keeps the narrowing read from
// reporting every check it inspects, so the fact and the kind both have a reader
// and this entry is what keeps the kind reachable.
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
// container key: the values the developer wrote sit in the Aggregate, which
// nests subsystems, and the infrastructure configuration the operator wrote
// sits on the Release, which nests boundaries. The file each pillar carries
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
// inside a scope, which the Release keys by the identifier the composition
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
// One of the shapes above spells the component with a key a Release wrote,
// and a Release addresses a component a composition contributed by the
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
//
// # The order is what the two faults over this axis are decided from
//
// A locals key can carry two faults — a reference to a name declared later in the
// block, and a reference to a name the block does not declare at all — and both are
// decided from the declared order plus the entry's own authored text. The prefix of
// this enumeration above a given entry IS the set that entry may read, because the
// evaluation refuses before it evaluates and any refusal aborts the block. So the
// question is answerable from declarations, which is what makes it answerable at
// all: a template either fault refuses is exactly one whose value is never
// produced.
//
// Two facts carry those inputs at the stage the Recipe binding is known — the keys
// in author order, and the text standing at each — and the rules reading them are
// invoked over this entry from the render's own described-stage reading.
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

// RunOnNodeAttachmentLocalsKeys invokes check once per local the FeatureRecipe
// each attachment binds on a node declares, in node order, then attachment order,
// then the order that FeatureRecipe declares its locals.
//
// The locator is .spec.locals.<key> in the FeatureRecipe's own file, on the same
// reading the node-carried attachment's artifacts axis states: the entry is
// authored in the FeatureRecipe, which is the document an author edits to change
// it, and that document is neither the Aggregate the attachment was written in nor
// the node's own Recipe. So the attachment reaches the subject as a coordinate
// without reaching the locator — the coordinate is what tells this locals block
// from the node's own and from another attachment's when a fact is keyed.
//
// # The order is what the two faults over this axis are decided from
//
// The prefix of this enumeration above a given entry IS the set that entry may
// read, exactly as it is on the node's own block: the evaluation refuses before it
// evaluates and any refusal aborts the block. The prefix is the attachment's OWN
// block and stops there — the node's resolved locals are merged over it after it
// has resolved, so an attachment local naming a base-Recipe key is reading a key
// that does not exist at the point it runs.
func RunOnNodeAttachmentLocalsKeys[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	locals AttachmentLocalsKeyEnumerator,
	view V,
	check Check[V, NodeAttachmentLocalsKeySubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentLocalsKeySubject {
		var out []NodeAttachmentLocalsKeySubject
		for _, attachment := range attachments.Attachments(node.ID) {
			for _, entry := range locals.AttachmentLocalsKeys(node.ID, attachment) {
				out = append(out, newNodeAttachmentLocalsKeySubject(
					entry.SourceFile, node.ID, attachment, entry.Value,
					documentSpecSegments(authoredKeyLocals, entry.Value)...,
				))
			}
		}
		return out
	})
}

// RunOnBoundaryAttachmentLocalsKeys invokes check once per local the FeatureRecipe
// each boundary-scope attachment binds declares, in boundary order, then
// attachment order, then the order that FeatureRecipe declares its locals.
//
// The locator is .spec.locals.<key> in the FeatureRecipe's own file, so the
// boundary contributes no locator segment — the reading the other boundary-rooted
// kinds whose locator names a FeatureRecipe state: a boundary key spliced onto a
// FeatureRecipe-rooted locator would name a path that document does not have. The
// boundary remains a coordinate, because that is what tells this locals block from
// another boundary's when a fact is keyed.
//
// The boundary set is walked rather than the node set: such an attachment is
// declared once and reaches every component the boundary holds, so a node-rooted
// walk would ask about one authored block once per component.
func RunOnBoundaryAttachmentLocalsKeys[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	attachments BoundaryAttachmentEnumerator,
	locals BoundaryAttachmentLocalsKeyEnumerator,
	view V,
	check Check[V, BoundaryAttachmentLocalsKeySubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryAttachmentLocalsKeySubject {
			var out []BoundaryAttachmentLocalsKeySubject
			for _, attachment := range attachments.BoundaryAttachments(boundary.Value) {
				for _, entry := range locals.BoundaryAttachmentLocalsKeys(boundary.Value, attachment) {
					out = append(out, newBoundaryAttachmentLocalsKeySubject(
						entry.SourceFile, boundary.Value, attachment, entry.Value,
						documentSpecSegments(authoredKeyLocals, entry.Value)...,
					))
				}
			}
			return out
		})
}

// authoredFieldSegments returns the segments naming a template field inside its
// own document: the spec, then the authored keys the enumerator supplied.
//
// The keys arrive from the enumerator rather than being derived from the field's
// label, because the blocks a carrier's template fields sit in differ from one
// another and no single reading of a label spells all of them. An enumerator
// supplying none leaves the locator at the spec, which routes a consumer to the
// document rather than to a plausible-looking path the file does not contain.
func authoredFieldSegments(authoredKeys []string) []string {
	if len(authoredKeys) == 0 {
		return []string{authoredKeySpec}
	}
	return documentSpecSegments(authoredKeys[0], authoredKeys[1:]...)
}

// RunOnNodeTemplateFields invokes check once per inline template field each
// node's own Recipe writes, in node order and then in the order that Recipe's
// fields are enumerated in.
//
// The locator is the authored field inside the Recipe's own file, spelled from the
// keys the enumerator supplied — .spec.valuesMapping for the values mapping,
// .spec.artifacts.<key> for an artifacts entry, and so on. The node contributes no
// segment, on the reading every node-rooted kind whose locator names a Recipe
// carries: the fault is in the Recipe's text rather than in any one node that binds
// it, so a node key spliced onto a Recipe-rooted locator would point an author at an
// instance when the edit is in the entry every instance shares. The node remains a
// coordinate, because that is what selects which Recipe's text is being asked about
// and what the facts a rule over this kind reads are keyed by.
//
// # The field is enumerated, which is what makes coverage assertable
//
// The innermost coordinate is the authored field label, so a Recipe gaining a
// template field gains a subject with no kind, fact, traversal entry or declaration
// moving. That is the property the family is for: what a rule over this kind covers
// is read off the enumeration rather than counted from how many declarations
// somebody remembered to write.
func RunOnNodeTemplateFields[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	fields NodeTemplateFieldEnumerator,
	view V,
	check Check[V, NodeTemplateFieldSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeTemplateFieldSubject {
		entries := fields.NodeTemplateFields(node.ID)
		out := make([]NodeTemplateFieldSubject, 0, len(entries))
		for _, entry := range entries {
			out = append(out, newNodeTemplateFieldSubject(
				entry.SourceFile, node.ID, entry.Value,
				authoredFieldSegments(entry.AuthoredKeys)...,
			))
		}
		return out
	})
}

// RunOnNodeAttachmentTemplateFields invokes check once per inline template field
// the FeatureRecipe each attachment binds on a node writes, in node order, then
// attachment order, then the order that FeatureRecipe's fields are enumerated in.
//
// The locator is the authored field inside the FeatureRecipe's own file, spelled
// from the keys the enumerator supplied. Neither the node nor the attachment
// contributes a locator segment, on the reading every kind whose locator names a
// FeatureRecipe carries: the fault is in that document's text rather than in any
// one node or attachment that binds it, so a node or attachment key spliced onto a
// FeatureRecipe-rooted locator would spell a path the file does not contain and
// would point an author at an instance when the edit is in the entry every instance
// shares. Both remain coordinates — the attachment selects the document, the node
// selects the render context its text is judged against, and together they are what
// the facts a rule over this kind reads are keyed by.
//
// # The field is enumerated, which is what makes coverage assertable
//
// The innermost coordinate is the authored field label, so a FeatureRecipe gaining
// a template field gains a subject with no kind, fact, traversal entry or
// declaration moving. That is the property the family is for: what a rule over this
// kind covers is read off the enumeration rather than counted from how many
// declarations somebody remembered to write.
func RunOnNodeAttachmentTemplateFields[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	fields AttachmentTemplateFieldEnumerator,
	view V,
	check Check[V, NodeAttachmentTemplateFieldSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentTemplateFieldSubject {
		var out []NodeAttachmentTemplateFieldSubject
		for _, attachment := range attachments.Attachments(node.ID) {
			for _, entry := range fields.AttachmentTemplateFields(node.ID, attachment) {
				out = append(out, newNodeAttachmentTemplateFieldSubject(
					entry.SourceFile, node.ID, attachment, entry.Value,
					authoredFieldSegments(entry.AuthoredKeys)...,
				))
			}
		}
		return out
	})
}

// RunOnBoundaryAttachmentTemplateFields invokes check once per inline template
// field the FeatureRecipe each attachment declared on a boundary writes, in
// boundary order, then attachment order, then the order that FeatureRecipe's
// fields are enumerated in.
//
// The locator is the authored field inside the FeatureRecipe's own file, spelled
// from the keys the enumerator supplied — .spec.provides.<key> for a provides
// entry, .spec.valuesMapping for the values mapping, and so on. The boundary
// contributes no segment, on the reading every boundary-rooted kind whose locator
// names a FeatureRecipe carries: a boundary key spliced onto a
// FeatureRecipe-rooted locator would name a path that document does not have. The
// boundary remains a coordinate, because that is what tells one attachment's
// fields from another boundary's when a fact is keyed.
//
// The boundary set is walked rather than the node set: such an attachment is
// declared once and reaches every component the boundary holds, so a node-rooted
// walk would ask about one authored document once per component.
//
// # The field is enumerated, which is what makes coverage assertable
//
// The innermost coordinate is the authored field label, so a carrier gaining a
// template field gains a subject with no kind, fact, traversal entry or
// declaration moving. That is the property the family is for: what a rule over
// this kind covers is read off the enumeration rather than counted from how many
// declarations somebody remembered to write.
func RunOnBoundaryAttachmentTemplateFields[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	attachments BoundaryAttachmentEnumerator,
	fields BoundaryAttachmentTemplateFieldEnumerator,
	view V,
	check Check[V, BoundaryAttachmentTemplateFieldSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryAttachmentTemplateFieldSubject {
			var out []BoundaryAttachmentTemplateFieldSubject
			for _, attachment := range attachments.BoundaryAttachments(boundary.Value) {
				for _, entry := range fields.BoundaryAttachmentTemplateFields(boundary.Value, attachment) {
					out = append(out, newBoundaryAttachmentTemplateFieldSubject(
						entry.SourceFile, boundary.Value, attachment, entry.Value,
						authoredFieldSegments(entry.AuthoredKeys)...,
					))
				}
			}
			return out
		})
}

// RunOnBoundaryPathTemplateFields invokes check once per path template each
// boundary of a Release declares, in boundary order and then in the order that
// boundary's fields are enumerated in.
//
// The locator is .spec.boundaries.<boundary>.<field>, with the container's key
// repeated for each level of a nested boundary, which is the site the operator
// wrote. The boundary DOES contribute segments here, where a boundary-rooted kind
// whose locator names a FeatureRecipe contributes none: the difference is which
// document the finding is positioned at. A path template is authored in the
// Release document itself, under the scope's own key, so the scope path is
// exactly what walks the reader to it — and the file is the scope's own for the
// same reason, rather than something the field enumerator restates.
//
// # The field is enumerated, which is what makes coverage assertable
//
// The innermost coordinate is the authored field, so a boundary gaining a path
// template gains a subject with no kind, fact, traversal entry or declaration
// moving. That is the property the family is for: what a rule over this kind
// covers is read off the enumeration rather than counted from how many
// declarations somebody remembered to write.
//
// # What this axis does NOT reach, stated because an unstated bound reads as coverage
//
// The scope set is the boundaries the Release declares. A per-component
// override of the same field — written under a component inside a scope rather
// than on the scope — is authored at a coordinate no boundary key enumerates, so
// a rule over this kind is not invoked over it. That carrier keeps the refusal
// the render makes when it evaluates the template.
func RunOnBoundaryPathTemplateFields[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	fields BoundaryPathTemplateFieldEnumerator,
	view V,
	check Check[V, BoundaryPathTemplateFieldSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryPathTemplateFieldSubject {
			names := fields.BoundaryPathTemplateFields(boundary.Value)
			out := make([]BoundaryPathTemplateFieldSubject, 0, len(names))
			for _, field := range names {
				out = append(out, newBoundaryPathTemplateFieldSubject(
					boundary.SourceFile, boundary.Value, field,
					nestedScopeSegments(boundaryNesting, boundary.Value, field)...,
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

// RunOnNodeArtifactsEntries invokes check once per entry each node's own Recipe
// writes in its artifacts block, in node order and then the order that Recipe
// declares its entries.
//
// The locator is .spec.artifacts.<key> in the Recipe's own file, where <key> is
// the key the author typed — which is the coordinate the subject carries, before
// anything resolves what the entry stands for.
//
// An authored artifacts key is the output's filename, so on this axis the key
// ordinarily carries the locator dialect's separator, and it carries it as ONE
// authored key the dialect cannot address. The locator then names the artifacts
// block and stops. That bound is [RunOnNodeArtifacts]'s and is inherited here
// unchanged, because it follows from what an artifacts key IS and not from when
// the key is read: an inline entry's key is already the output filename, so the
// authored key carries the separator exactly as the composed output name does.
// A rule over this kind therefore owes exactly what a rule over that one owes —
// it names the entry in its detail, CAPTIONED, because the block says where to
// look and only the detail can say which entry inside it, and a filename standing
// bare beside another quoted value is not something a reader can tell apart from
// it. See [DetailValue] for which of a rule's values carries a caption.
//
// That is a requirement on the rule and not something the framework can supply.
//
// The axis is the authored block and not the projected outputs, and that is the
// whole reason this entry stands beside [RunOnNodeArtifacts] rather than being
// reached through it. The outputs of an entry importing a library bundle exist
// only once the import has succeeded, so a rule about the import would be invoked
// no times for an entry whose import failed — which is the one outcome
// indistinguishable from a rule that ran and approved.
func RunOnNodeArtifactsEntries[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	entries ArtifactsEntryEnumerator,
	view V,
	check Check[V, NodeArtifactsEntrySubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeArtifactsEntrySubject {
		declared := entries.ArtifactsEntries(node.ID)
		out := make([]NodeArtifactsEntrySubject, 0, len(declared))
		for _, entry := range declared {
			out = append(out, newNodeArtifactsEntrySubject(
				entry.SourceFile, node.ID, entry.Value,
				documentSpecSegments(authoredKeyArtifacts, entry.Value)...,
			))
		}
		return out
	})
}

// RunOnNodeRecipeImportFields invokes check once per spec-root field each node's
// own Recipe supplies by `$import:`, in node order and then the fixed order the
// enumeration declares its fields in.
//
// The locator is .spec.<field> in the Recipe's own file, where <field> is the key
// the author wrote the `$import:` under — which is the coordinate the subject
// carries, before anything resolves what the reference stands for. The field names
// are the Recipe's own spec-root keys, fixed by the authoring surface and free of
// the locator dialect's separator, so the locator is exact and a rule over this
// kind owes no captioned stand-in for a position it could not reach.
//
// The node contributes no segment, on the reading every node-rooted kind whose
// locator names a Recipe carries: the fault is in the Recipe's text rather than in
// any one node that binds it, so a node key spliced onto a Recipe-rooted locator
// would point an author at an instance when the edit is in the entry every
// instance shares.
//
// The axis is what the author WROTE and not what the reference resolved to, which
// is the whole reason it stands beside [RunOnNodeTemplateFields] rather than being
// reached through it. An imported field's content exists only once the import has
// succeeded, so a rule about the import would be invoked no times for a field whose
// import failed — which is the one outcome indistinguishable from a rule that ran
// and approved.
func RunOnNodeRecipeImportFields[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	fields RecipeImportFieldEnumerator,
	view V,
	check Check[V, NodeRecipeImportFieldSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeRecipeImportFieldSubject {
		declared := fields.RecipeImportFields(node.ID)
		out := make([]NodeRecipeImportFieldSubject, 0, len(declared))
		for _, field := range declared {
			out = append(out, newNodeRecipeImportFieldSubject(
				field.SourceFile, node.ID, field.Value,
				documentSpecSegments(field.Value)...,
			))
		}
		return out
	})
}

// RunOnNodeRecipeSourceFields invokes check once per source entry each node's own
// Recipe declares a position for, in node order.
//
// The locator is .spec.source in the Recipe's own file, where the key is the
// coordinate the subject carries. It is the authoring surface's own spec-root key
// and free of the locator dialect's separator, so the locator is exact and a rule
// over this kind owes no captioned stand-in for a position it could not reach.
//
// The node contributes no segment, on the reading every node-rooted kind whose
// locator names a Recipe carries: the fault is in the Recipe's text rather than in
// any one node that binds it, so a node key spliced onto a Recipe-rooted locator
// would point an author at an instance when the edit is in the entry every instance
// shares.
//
// The axis is the POSITION and not what the author wrote at it, which is the whole
// reason it stands beside [RunOnNodeRecipeImportFields] and [RunOnNodeTemplateFields]
// rather than being reached through either. Both of those present a field only once
// it carries something — an import or template text — and a rule about an entry left
// behind with nothing in it would be invoked no times for exactly the documents it
// exists to refuse, which is the one outcome indistinguishable from a rule that ran
// and approved.
func RunOnNodeRecipeSourceFields[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	fields RecipeSourceFieldEnumerator,
	view V,
	check Check[V, NodeRecipeSourceFieldSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeRecipeSourceFieldSubject {
		declared := fields.RecipeSourceFields(node.ID)
		out := make([]NodeRecipeSourceFieldSubject, 0, len(declared))
		for _, field := range declared {
			out = append(out, newNodeRecipeSourceFieldSubject(
				field.SourceFile, node.ID, field.Value,
				documentSpecSegments(field.Value)...,
			))
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

// RunOnNodeEmittedPaths invokes check once per output name one node's own render
// emitted, in node order and then name order.
//
// The locator is .spec.components.<name>, the component instance itself, spelled
// level by level for a component reached through a subsystem — the same site the
// node kind's own subjects carry, and for the same reason: a component instance
// is what an author wrote and what a reader is sent to.
//
// The emitted name contributes no segment, and that is the point rather than an
// omission. A name is output: it is produced by whichever of the node's blocks
// emitted it, and no document holds it as a key — so a segment naming it would
// spell a path the file does not contain. It remains a coordinate, because that
// is what tells one of a node's emissions from another when a fact is keyed.
//
// The locator therefore stops one level short of the block that emitted the file,
// and a rule over this kind makes that up in its detail: it names the emitted
// name, CAPTIONED, because a filename standing bare beside another quoted value
// is not something a reader can tell apart from it — see [DetailValue] for which
// of a rule's values carries a caption. Naming the block is a rule's own job too,
// and it reads that from the fact rather than from the subject: the subject knows
// which file, and only the origin set knows which blocks put it there.
func RunOnNodeEmittedPaths[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	paths NodeEmittedPathEnumerator,
	view V,
	check Check[V, NodePathSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodePathSubject {
		segments := componentAuthoredSegments(
			node.AuthoredName, node.SubsystemPath, node.SynthesizedSubsystem,
		)
		names := paths.NodeEmittedPaths(node.ID)
		out := make([]NodePathSubject, 0, len(names))
		for _, name := range names {
			out = append(out, newNodePathSubject(node.SourceFile, node.ID, name, segments...))
		}
		return out
	})
}

// RunOnNodeAttachmentEmittedPaths invokes check once per output name the render each
// attachment carries on a node emitted, in node order, then attachment order, then
// name order.
//
// The locator names the FeatureRecipe's own file and stops at its spec, which is the
// document an author edits to rename one of the colliding blocks. It is neither the
// Aggregate the attachment was written in nor the node's own Recipe, on the reading
// [RunOnNodeAttachmentArtifacts] states: the file comes from the enumerated entry
// rather than from the node.
//
// The emitted name contributes no segment, for the reason [RunOnNodeEmittedPaths]
// gives: a name is output, produced by whichever block emitted it, and no document
// holds it as a key — so a segment naming it would spell a path the file does not
// contain. It remains a coordinate, because that is what tells one of a render's
// emissions from another when a fact is keyed.
//
// The attachment likewise reaches the subject as a coordinate without reaching the
// locator, which is not an inconsistency: the locator names the document to edit,
// while the coordinate is what tells this render's emission from the host node's own
// and from another attachment's on the same node.
//
// So a rule over this kind owes the emitted name in its detail, CAPTIONED, exactly
// as the node-rooted form does — see [DetailValue] for which of a rule's values
// carries a caption — and it names the blocks from the fact rather than from the
// subject, because the subject knows which file and only the origin set knows which
// blocks put it there.
func RunOnNodeAttachmentEmittedPaths[V View](
	cancelled Cancellation,
	nodes NodeEnumerator,
	attachments AttachmentEnumerator,
	paths NodeAttachmentEmittedPathEnumerator,
	view V,
	check Check[V, NodeAttachmentPathSubject],
) ([]Objection, error) {
	return runOverNodes(cancelled, nodes, view, check.Run, func(node NodeInstance) []NodeAttachmentPathSubject {
		var out []NodeAttachmentPathSubject
		for _, attachment := range attachments.Attachments(node.ID) {
			for _, entry := range paths.NodeAttachmentEmittedPaths(node.ID, attachment) {
				out = append(out, newNodeAttachmentPathSubject(
					entry.SourceFile, node.ID, attachment, entry.Value,
				))
			}
		}
		return out
	})
}

// RunOnBoundaryAttachmentEmittedPaths invokes check once per output name the render
// each boundary-scope attachment carries emitted, in boundary order, then attachment
// order, then name order.
//
// The locator names the FeatureRecipe's own file, which is the document an author
// edits to rename one of the colliding blocks, and the boundary contributes no
// segment — the reading [RunOnBoundaryAttachmentArtifacts] states for the other
// boundary-rooted kind whose locator names a FeatureRecipe: a boundary key spliced
// onto a FeatureRecipe-rooted locator would name a path that document does not have.
// The boundary remains a coordinate, because that is what tells this render's
// emission from another boundary's when a fact is keyed.
//
// The emitted name contributes no segment either, for the reason every emitted axis
// gives: a name is output and no document holds it as a key. So a rule over this
// kind owes the emitted name in its detail, captioned, and reads the blocks from the
// fact rather than from the subject.
func RunOnBoundaryAttachmentEmittedPaths[V View](
	cancelled Cancellation,
	boundaries BoundaryEnumerator,
	attachments BoundaryAttachmentEnumerator,
	paths BoundaryAttachmentEmittedPathEnumerator,
	view V,
	check Check[V, BoundaryAttachmentPathSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, boundaries.Boundaries(), view, check.Run,
		func(boundary AuthoredCoordinate) []BoundaryAttachmentPathSubject {
			var out []BoundaryAttachmentPathSubject
			for _, attachment := range attachments.BoundaryAttachments(boundary.Value) {
				for _, entry := range paths.BoundaryAttachmentEmittedPaths(boundary.Value, attachment) {
					out = append(out, newBoundaryAttachmentPathSubject(
						entry.SourceFile, boundary.Value, attachment, entry.Value,
					))
				}
			}
			return out
		})
}

// RunOnAssemblyPathProviders invokes check once per assembly group — one
// provider's contribution at one evaluated assembly path — in assembly-path
// order and then provider order.
//
// The locator is empty and the source file is the Release document. An
// assembly path is evaluated output and a provider is selected rather than
// written, so neither is a key any document holds and neither contributes a
// segment; naming one would name a path the file does not contain. Both are
// still coordinates the subject carries, which is what lets a rule name the group
// it objects about in its detail. A rule that has to send an operator somewhere
// editable reads the boundaries that fed the group as a fact and names those.
func RunOnAssemblyPathProviders[V View](
	cancelled Cancellation,
	paths AssemblyPathEnumerator,
	providers AssemblyGroupProviderEnumerator,
	view V,
	check Check[V, AssemblyPathProviderSubject],
) ([]Objection, error) {
	return runOverAuthoredScopes(cancelled, paths.AssemblyPaths(), view, check.Run,
		func(path AuthoredCoordinate) []AssemblyPathProviderSubject {
			names := providers.AssemblyGroupProviders(path.Value)
			out := make([]AssemblyPathProviderSubject, 0, len(names))
			for _, provider := range names {
				out = append(out, newAssemblyPathProviderSubject(
					path.SourceFile, path.Value, provider,
				))
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

// RunOnBoundaryComponentReferences invokes check once per component entry the
// Release's boundaries declare, in the order the enumerator returns them.
//
// The axis is flat rather than nested under the boundary set, for the reason
// [BoundaryComponentReferenceEnumerator] states: the id is the entry's identity,
// so the boundary positions the finding and keys nothing.
//
// The locator is assembled through the shared authored-site rule rather than a
// spelling of its own, and the two shapes it covers are exactly the two axes a
// boundary names components on. An entry the boundary named directly carries its
// component key on the site, so the site alone names it — .spec.components.<name>
// for an entry in the boundary the compiler synthesized, and the boundary level by
// level under its own key then the component for every other. An entry a routing
// entry declared names no component of the boundary at all, so the site names the
// boundary and the entry's own routing keys follow it.
//
// A rule over this kind owes the id in its detail whichever shape it was invoked
// for. On the direct axis the id the author typed carries the locator dialect's
// separator whenever a sub-module contributed the component, so the locator names
// the component block and stops — the division [RunOnAssemblyPathProviders] draws
// for a value legitimate as a coordinate and refused as a segment. On the routed
// axis the locator reaches the local name, which is not the id the entry
// addresses, so the detail is the only place the id appears.
func RunOnBoundaryComponentReferences[V View](
	cancelled Cancellation,
	references BoundaryComponentReferenceEnumerator,
	view V,
	check Check[V, BoundaryComponentReferenceSubject],
) ([]Objection, error) {
	var out []Objection
	for _, reference := range references.BoundaryComponentReferences() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		out = append(out, check.Run(view, newBoundaryComponentReferenceSubject(
			reference.SourceFile, reference.ID,
			authoredSiteSegments(boundaryNesting, reference.Site, reference.RoutedKeys...)...,
		))...)
	}
	return out, nil
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
// an Aggregate's top-level components and gives it no attachment surface of its
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

// RunOnAuthoredAnnotations invokes check once per annotation entry an author
// wrote, in the order the enumerator returns them.
//
// The set is walked flat rather than per document or per scope, because the
// derived kind carries one coordinate whose identity is the whole of the
// document, the site and the entry's keys: one order over the whole set,
// promised by one enumerator. Walking it per scope would need one walk per
// document and would leave an entry written at a document's top level outside
// both, since that shape sits in a scope the compiler synthesized and the scope
// axes exclude.
//
// The locator is the site the author wrote, then the annotation block, then the
// key: .spec.boundaries.<a>.boundaries.<b>.components.<c>.annotations.<key> for
// an entry on a component of a nested Release scope, and
// .spec.subsystems.<s>.annotations.<key> for one written on an Aggregate scope
// itself. The container's key is repeated for each level of a nested scope,
// because that is how both documents spell one, and a scope-wide entry names the
// scope and stops — there is no component to name, and naming one would point at
// an edit that would not fix the finding. It is assembled through the shared
// authored-site rule rather than a spelling of its own, so an entry written
// inside a Release's routing entry names the keys that entry spells, exactly
// as a rule over the routed component itself does.
//
// The subkey contributes no segment. It is a dotted route into the Feature the
// binding targets rather than a key of the document the locator names, and it may
// be one authored key carrying the dialect's own separator — so a locator built
// from it would name a path no file contains. It reaches the check as the
// subject's own coordinate instead, which is the division [authoredLocator] draws
// for every value that is legitimate as a coordinate and refused as a segment.
//
// An entry whose document this rule cannot name gets no segments, which is the
// mechanical form of not inventing a locator: the scope nesting is selected by
// the document, and a document outside the closed pair selects none.
func RunOnAuthoredAnnotations[V View](
	cancelled Cancellation,
	annotations AuthoredAnnotationEnumerator,
	view V,
	check Check[V, AuthoredAnnotationSubject],
) ([]Objection, error) {
	var out []Objection
	for _, entry := range annotations.AuthoredAnnotations() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		var segments []string
		if nesting, named := originNesting(entry.Document); named {
			tail := make([]string, 0, len(entry.RoutedKeys)+2)
			tail = append(tail, entry.RoutedKeys...)
			tail = append(tail, authoredKeyAnnotations, entry.Key)
			segments = authoredSiteSegments(nesting, entry.Site, tail...)
		}
		subject := newAuthoredAnnotationSubject(
			entry.SourceFile, entry.Document,
			entry.Site.ScopePath, entry.Component,
			entry.Key, entry.Subkey,
			segments...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out, nil
}

// RunOnBoundaryPlacements invokes check once per thing a Release's boundaries
// place, in the order the enumerator returns them.
//
// The set is walked flat rather than per boundary, because the derived kind
// carries one coordinate whose identity is the whole of the document, the
// boundary, the reference and the axis: one order over the whole set, promised by
// one enumerator. Walking it per boundary would leave every placement of a
// Release written without named boundaries outside the walk, since that shape
// sits in a boundary the compiler synthesized and the boundary axis excludes.
//
// The locator is the site the entry was written at, then whatever routing keys
// reach it: .spec.boundaries.<a>.boundaries.<b>.components.<c> for a component a
// nested boundary names directly, .spec.components.<c> for one in the boundary
// normalization synthesized, .spec.boundaries.<a>.aggregates.<m>.components.<local>
// for a component named inside a routing entry, and .spec.boundaries.<a>.aggregates.<m>
// for the routing entry itself. The container's key is repeated for each level of
// a nested boundary, because that is how the document spells one — joining the
// boundary path in as a single segment would name a key no file contains. It is
// assembled through the shared authored-site rule rather than a spelling of its
// own, so a routed placement names the keys its routing entry spells, exactly as
// a rule over the routed component itself does.
//
// A component the boundary names directly carries the id it holds once the
// composition is flattened as its own authoring key, and that id carries the
// locator dialect's separator whenever a sub-module contributed the component —
// so the locator names the component block and stops, and the id reaches the
// author as the value the rule raises. That is the division [authoredLocator]
// draws for a value legitimate as a coordinate and refused as a segment, and it
// is the standing trade rather than this rule's loss.
//
// A placement whose document this rule cannot name gets no segments, which is the
// mechanical form of not inventing a locator: the scope nesting is selected by the
// document, and a document outside the closed pair selects none.
func RunOnBoundaryPlacements[V View](
	cancelled Cancellation,
	placements BoundaryPlacementEnumerator,
	view V,
	check Check[V, BoundaryPlacementSubject],
) ([]Objection, error) {
	var out []Objection
	for _, entry := range placements.BoundaryPlacements() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		var segments []string
		if nesting, named := originNesting(entry.Placement.Document); named {
			segments = authoredSiteSegments(nesting, entry.Site, entry.RoutedKeys...)
		}
		out = append(out, check.Run(view, newBoundaryPlacementSubject(
			entry.SourceFile,
			entry.Placement.Document,
			entry.Placement.BoundaryPath,
			entry.Placement.Reference,
			entry.Placement.Axis,
			segments...,
		))...)
	}
	return out, nil
}

// RunOnReleaseEdgeConfigurations invokes check once per entry the Release's
// per-edge `uses` surface writes, in the order the enumerator returns them.
//
// The set is walked flat rather than per node, because an entry may name a
// consuming component the composition does not hold: a walk rooted at the node
// set would drop exactly the entries a rule over this axis has to be invoked for,
// and its silence on them would read as a Release that wrote nothing there.
//
// The locator is the boundary the entry sits in, then the keys reaching the
// consuming component's own block, then the `uses` container, the socket and the
// alias: .spec.boundaries.<a>.components.<c>.uses.<socket>.<alias> for a
// component a boundary names directly,
// .spec.boundaries.<a>.aggregates.<m>.components.<local>.uses.<socket>.<alias>
// for one named inside a routing entry, and .spec.components.<c>.uses.<socket>.<alias>
// for an entry in the boundary normalization synthesized. The container's key is
// repeated for each level of a nested boundary, because that is how the document
// spells one. It is assembled through the shared authored-site rule rather than a
// spelling of its own, so an entry on a routed component names the keys its
// routing entry spells, exactly as a rule over that component itself does.
//
// A component the boundary names directly carries the id it holds once the
// composition is flattened as its own authoring key, and that id carries the
// locator dialect's separator whenever a sub-module contributed the component —
// so the locator names the component block and stops there, and the id reaches
// the author as the value the rule raises. That is the division [authoredLocator]
// draws for a value legitimate as a coordinate and refused as a segment, and it
// is the standing trade rather than this rule's loss.
//
// An entry whose document this rule cannot name gets no segments, which is the
// mechanical form of not inventing a locator: the scope nesting is selected by
// the document, and a document outside the closed pair selects none.
func RunOnReleaseEdgeConfigurations[V View](
	cancelled Cancellation,
	configurations ReleaseEdgeConfigurationEnumerator,
	view V,
	check Check[V, ReleaseEdgeConfigurationSubject],
) ([]Objection, error) {
	var out []Objection
	for _, entry := range configurations.ReleaseEdgeConfigurations() {
		if err := cancelled.err(); err != nil {
			return nil, err
		}
		out = append(out, check.Run(view, newReleaseEdgeConfigurationSubject(
			entry.SourceFile,
			entry.Configuration.Document,
			entry.Configuration.BoundaryPath,
			entry.Configuration.Node,
			entry.Configuration.Socket,
			entry.Configuration.Alias,
			releaseEdgeConfigurationSegments(entry)...,
		))...)
	}
	return out, nil
}

// releaseEdgeConfigurationSegments returns the segments naming where one entry of
// the Release's per-edge `uses` surface was written, with tail appended inside
// the entry: the boundary it sits in, the keys reaching the consuming component's
// own block, the `uses` container, the socket and the alias.
//
// It is the one spelling of that site, shared by the traversal invoking a rule
// once per entry and by the locator naming an element written inside an entry,
// so a finding about the entry and a finding about an attachment it carries
// cannot come to name two different entries.
//
// An entry whose document this rule cannot name yields no segments, which is the
// mechanical form of not inventing a locator.
func releaseEdgeConfigurationSegments(entry ReleaseEdgeConfigurationInstance, tail ...string) []string {
	nesting, named := originNesting(entry.Configuration.Document)
	if !named {
		return nil
	}
	keys := make([]string, 0, len(entry.ComponentKeys)+3+len(tail))
	keys = append(keys, entry.ComponentKeys...)
	keys = append(keys, authoredKeyUses, entry.Configuration.Socket, entry.Configuration.Alias)
	keys = append(keys, tail...)
	return authoredSiteSegments(nesting, entry.Site, keys...)
}

// ReleaseConnectionAttachmentLocator returns the locator naming one attachment a
// Release per-edge entry carries: the entry's own site, then the `features` block
// and the slot the attachment is written under.
//
// It is exported for the one finding about such an attachment that is not raised
// by a rule over the entry — the conformance of the attachment's merged
// configuration, measured once the attachment cascade has folded — so that
// finding names the entry the operator wrote through the same assembly every
// rule over the entry is positioned by, rather than a path spelled from the name
// the attachment carries on the consuming component.
func ReleaseConnectionAttachmentLocator(entry ReleaseEdgeConfigurationInstance, attachment string) string {
	return authoredLocator(releaseEdgeConfigurationSegments(entry, authoredKeyFeatures, attachment)...)
}

// ConnectionAttachmentLocator returns the locator naming one attachment a
// component's own connection entry carries, in the document the component was
// written in: the component's block, the `uses` container, the socket and the
// alias, then the `features` block and the slot the attachment is written under.
//
// It is exported for the reason [ReleaseConnectionAttachmentLocator] is, from the
// other authoring document: the conformance of a connection attachment's merged
// configuration is measured outside any rule's traversal, and the finding has to
// name the connection entry the developer wrote rather than an attachment block
// the consuming component does not have. A connection carrying attachments is
// always written in the long form, whose alias is a key the author typed, so the
// alias is always a segment here.
func ConnectionAttachmentLocator(node NodeInstance, socket, alias, attachment string) string {
	return authoredLocator(componentInnerSegments(
		node, authoredKeyUses, socket, alias, authoredKeyFeatures, attachment)...)
}
