// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The delivery mechanisms an injection block names as its own fields, as against
// the further mechanisms an author may add. The distinction is the authored
// shape's and is why a payload lookup needs both halves: these three are read
// from the fields that carry them and any other from the block's remaining
// entries.
//
// The name set itself is not restated by the axis that enumerates it — that
// delegates to the one reading of which mechanisms a block declares — so a
// mechanism added to the authored shape appears on the axis whether or not the
// lookup below is extended. What would then be missing is its payload, which the
// parity assertion over the earlier stage's own key reading is what catches.
const (
	injectMechanismEnv  = "env"
	injectMechanismFile = "file"
	injectMechanismArg  = "arg"
)

// GraphStageCoordinates supplies the instance sets the framework's traversals
// walk for the rules declared over the finalised graph.
//
// A rule needs two things to be invoked: a presentation answering the facts it
// reads, and an enumeration of what it is invoked once per. [NewGraphView]
// supplies the first; this supplies the second, over the same compilation
// products, so the coordinate a rule is invoked for and the content it then
// reads come from one reading of one graph.
//
// # Everything here is keyed by the identity the node carries in the graph
//
// The flattened identifier is the key every fact at this stage takes, and it is
// the key used throughout. It is not the name the author typed: a component a
// sub-aggregate contributes carries the contributing module's dotted prefix
// ahead of its own name, and a set keyed by authored names holds no entry for
// it at all. An enumeration keyed the other way would present the rules a set
// of coordinates the facts cannot answer for — silently, because a rule asked
// about a coordinate no fact knows raises nothing and the build stays green.
//
// # An axis a fact already names is delegated to that fact
//
// The attachment axis is named by a fact as well as by an enumerator, so it
// delegates rather than re-reading the graph beside it. Two readings of one set
// are free to disagree with nothing comparing them; one reading cannot. The
// axes no fact names — the node set, the edge set, the originated-connection set,
// an attachment's pillars and their leaves — are read from the products this
// value holds.
//
// # Order
//
// Every axis returns a deterministic order and each states which, because a
// rule raises in the order it is invoked and that order reaches the build's
// output. The node set, the attachment set and each pillar's leaves come from
// ordered carriers and keep the order those carriers hold; the edge set keeps
// the order the graph holds it in; the originated-connection set keeps the order
// the composition drew the connections in; the slot set keeps the order the
// component type declares its slots in; the pillar axis returns a fixed
// declared order. No axis here ranges a plain Go map.
type GraphStageCoordinates struct {
	graph         *types.IRGraph
	view          constraint.GraphView
	catalogLookup resolver.CatalogLookup

	// releaseFile is the file the Release was authored in. It is held
	// because one axis is authored there rather than in the document its node
	// came from: an attachment's infrastructure configuration is the operator's
	// and the Release's attachment block is the only surface that admits it.
	releaseFile string

	// sites names, per attachment leaf, where the contribution that won it was
	// written. It is held rather than derived for the reason the attachment axis
	// is delegated to a fact: the answer was settled once, while the cascade
	// folded, and a second derivation of it here would be free to disagree with
	// the one that produced the bytes.
	sites AttachmentSites

	// nodes is the enumerated node set, folded once when this value is built
	// because it is the outermost order of every node-keyed traversal.
	nodes []constraint.NodeInstance

	// nodesByID indexes that same set by the identity the facts take, so an
	// axis needing one node's provenance reads the instance the node axis
	// already hands out rather than reading the graph a second time.
	nodesByID map[string]constraint.NodeInstance

	// edges is the enumerated edge set, folded once alongside the nodes: an
	// edge's subject carries the provenance of the component it is authored
	// in, so building one needs the node the edge is sourced at.
	edges []constraint.EdgeInstance

	// connections is the enumerated originated-connection set, folded once
	// alongside the edges. It is a separate axis rather than a projection of
	// the edge set: an originated connection is keyed by the origin as well as
	// by the edge triple, so two connections one attachment apart are two
	// instances here and one there.
	connections []constraint.OriginatedConnectionInstance
}

// NodeInstanceOf returns the framework node instance one node of a compiled
// graph stands for.
//
// The authored name is derived from the identifier rather than read off the
// node, because the graph carries no such field: the identifier records how the
// node was reached — a node a sub-aggregate contributes carries the
// contributing module's prefix — while a locator is built from what the author
// typed, and the framework is what defines the relation between the two.
//
// It is exported because more than one surface needs this mapping: the
// coordinate enumeration a stage's rules run over, and an answer about where
// the node's own block was written. A second mapping would agree the day it was
// written and be free to diverge the moment the graph carries another
// provenance fact, with nothing comparing them.
func NodeInstanceOf(id string, node types.IRNode) constraint.NodeInstance {
	return constraint.NodeInstance{
		ID:                   id,
		SourceFile:           node.SourceFile,
		AuthoredName:         constraint.AuthoredComponentName(id),
		SubsystemPath:        node.SubsystemPath,
		SynthesizedSubsystem: node.SynthesizedSubsystem,
	}
}

// NewGraphStageCoordinates derives the coordinates the finalised graph carries.
//
// The presentation is taken as well as the graph, because the axes a fact
// already names are answered by that fact rather than read a second time. The
// catalog is taken because one axis — the attachment slots a node's component
// type declares — is a property of the catalog entity rather than of the graph:
// a slot exists whether or not anything binds it, which is the case the rules
// over it exist to catch, so it cannot be read off what the node ended up with.
//
// The Release's file is taken for the same class of reason as the catalog:
// it is not in the graph. One axis is authored on the Release rather than in
// the document its node came from — an attachment's infrastructure
// configuration, which no other authoring surface admits — so an enumeration
// naming only the graph's own files could not tell a rule where that axis was
// written. It is a source-location label and nothing is read from disk with it;
// an empty one leaves that axis unpositioned rather than mispositioned.
//
// It returns nil when either the graph or the presentation is absent. An
// enumeration built from nothing would present every rule an empty subject set,
// and a rule invoked zero times reports exactly what a rule invoked and
// satisfied reports. A caller checks for nil rather than being handed a value
// that answers emptily.
func NewGraphStageCoordinates(
	graph *types.IRGraph,
	view constraint.GraphView,
	catalogLookup resolver.CatalogLookup,
	releaseFile string,
	sites AttachmentSites,
	connections *OriginatedConnections,
) *GraphStageCoordinates {
	if graph == nil || view == nil {
		return nil
	}
	c := &GraphStageCoordinates{
		graph:         graph,
		view:          view,
		catalogLookup: catalogLookup,
		releaseFile:   releaseFile,
		sites:         sites,
		nodesByID:     make(map[string]constraint.NodeInstance, graph.Spec.Nodes.Len()),
	}

	for id, node := range graph.Spec.Nodes.All() {
		instance := NodeInstanceOf(id, node)
		c.nodes = append(c.nodes, instance)
		c.nodesByID[id] = instance
	}

	for _, edge := range graph.Spec.Edges {
		node, held := c.nodesByID[edge.Source]
		if !held {
			// An edge whose consumer is not in the node set has no component
			// block for its locator to be named relative to, so there is
			// nothing to present a rule with. The graph does not produce that
			// shape; the guard keeps a rule from being handed a coordinate
			// whose provenance would have to be invented.
			continue
		}
		kind := originKindOf(edge)
		c.edges = append(c.edges, constraint.EdgeInstance{
			Node:             node,
			Socket:           edge.Socket,
			Alias:            edge.Alias,
			SynthesizedAlias: aliasIsDerived(graph, edge.Source, edge.Socket, kind),
			// The origin positions the finding and keys nothing. It is read
			// from the product that recorded where each origin's own block was
			// written, addressed by the coordinate the edge carries — so an
			// edge an attachment drew names that attachment's block rather than
			// a dependency block the consuming component does not have.
			Origin: connections.originOfEdge(edge.Source, kind, edge.OriginFeatureInstance),
		})
	}
	for _, entry := range connections.Entries() {
		node, held := c.nodesByID[entry.Node]
		if !held {
			continue
		}
		c.connections = append(c.connections, constraint.OriginatedConnectionInstance{
			Node:             node,
			Origin:           entry.Origin,
			Socket:           entry.Socket,
			Alias:            entry.Alias,
			Target:           entry.Target,
			SynthesizedAlias: entry.SynthesizedAlias,
		})
	}
	return c
}

// Nodes returns every component instance in the flattened set, a sub-aggregate's
// nodes included, in the order the graph holds them.
//
// That order is the order the composition produced, taken from the graph's own
// ordered node map rather than imposed here. It is the outermost order of every
// node-keyed traversal, so it is what fixes the order the findings of those
// rules appear in.
func (c *GraphStageCoordinates) Nodes() []constraint.NodeInstance { return c.nodes }

// Edges returns every connection the graph holds, one instance per connection,
// in the order the graph holds them.
//
// A connection is enumerated once whichever surface drew it — the component's
// own dependency block, an attachment's, or the Release's — because the graph
// holds one edge per connection however it arrived. The origin is a fact a rule
// reads, not a reason to enumerate the same connection twice.
func (c *GraphStageCoordinates) Edges() []constraint.EdgeInstance { return c.edges }

// OriginatedConnections returns every connection the composition drew, each
// paired with the surface that drew it, in the order the composition drew them.
//
// It is a different axis from [GraphStageCoordinates.Edges] rather than a
// re-ordering of it, and the difference is the key. The edge axis enumerates one
// instance per node, socket and alias, which is the key the facts about a
// connection's resolved shape take; this axis adds the origin, which is what a
// rule reading a connection's AUTHORED content needs — two attachments on one
// node may each write one socket and one alias, and each wrote its own payload.
//
// A connection is enumerated once per origin key. Where one origin key names
// more than one connection — one attachment instance name declared at two scopes
// draws one from each block, and the composition records the two identically —
// the key appears once and the fact keyed by it answers for every payload under
// it, so no authored block goes unexamined.
func (c *GraphStageCoordinates) OriginatedConnections() []constraint.OriginatedConnectionInstance {
	return c.connections
}

// Attachments returns a node's attachment names once the whole attachment
// cascade has folded, in the order the merged set holds them.
//
// It delegates to the fact naming that set rather than reading the graph beside
// it, so a rule is invoked for exactly the attachments it can then read.
func (c *GraphStageCoordinates) Attachments(node string) []string {
	return c.view.MergedAttachmentNames(node)
}

// Slots returns the attachment slot names a node's component type declares, in
// the order the catalog entity declares them.
//
// It is the declared set and never the bound one. A rule over a slot that must
// be bound has to be asked about a slot nothing binds — that is the case it
// exists to catch — so an axis enumerating what the node ended up with would
// never present that rule its own subject.
//
// A node whose component type the catalog does not hold contributes no slots:
// there is no declaration to enumerate, and an earlier stage already refuses a
// component reference that resolves to nothing.
func (c *GraphStageCoordinates) Slots(node string) []string {
	componentType, present := c.view.FlattenedNodeComponentType(node)
	if !present || c.catalogLookup.Component == nil {
		return nil
	}
	component, found := c.catalogLookup.Component(componentType)
	if !found {
		return nil
	}
	return component.Spec.FeaturesSchema.Keys()
}

// AttachmentPillars returns the configuration pillars one attachment carries
// per-leaf provenance for, each with the document it was authored in, in the
// fixed order the pillars are declared in.
//
// The two pillars are a fixed authoring surface rather than an open set, so the
// order is declared here rather than derived from a carrier. A pillar the
// attachment carries no provenance for is absent, because the leaf axis beneath
// it would be empty and enumerating it would claim the attachment wrote a
// pillar it did not.
//
// # Each pillar names its own document, and the two differ
//
// The values pillar is the developer's and is written in the document that
// declares the component, so it takes that node's own file. The infrastructure
// pillar is the operator's and is written on the Release: it is the only
// authoring surface whose attachment block admits one, on either route a leaf
// can arrive by — an authored block, where no Aggregate-side attachment
// schema declares the key, or an annotation, where the routing admits an
// infrastructure path from the Release side alone. So the answer is a
// reading of where such a leaf can come from rather than a guess about where
// this one did.
func (c *GraphStageCoordinates) AttachmentPillars(
	node string,
	attachment string,
) []constraint.AuthoredCoordinate {
	entry, held := c.attachment(node, attachment)
	if !held {
		return nil
	}
	var out []constraint.AuthoredCoordinate
	if entry.ValuesProvenance.Len() > 0 {
		out = append(out, constraint.AuthoredCoordinate{
			Value:      pillarValues,
			SourceFile: c.nodesByID[node].SourceFile,
		})
	}
	if entry.InfraProvenance.Len() > 0 {
		out = append(out, constraint.AuthoredCoordinate{
			Value:      pillarInfra,
			SourceFile: c.releaseFile,
		})
	}
	return out
}

// AttachmentPillarLeaves returns the leaves of one pillar of one attachment's
// configuration, each with the site it was written at, in the order the
// provenance record holds them.
//
// The leaves come from the provenance record rather than from the merged
// payload, and that is what keys the axis where the facts are keyed: a leaf
// coordinate is a dotted path into the pillar, which is the shape the provenance
// record is keyed by, while the payload is a nested map whose top-level keys are
// a different set. Enumerating the payload would hand a rule coordinates no fact
// at this stage answers for.
//
// Each leaf's site is read from what the fold retained, under the same
// coordinate the leaf is enumerated at. A leaf with no retained site carries an
// empty one, which names no authored place and routes a rule's finding to
// file-level positioning — the mechanical form of not inventing a locator, and
// the same answer every other axis gives when it cannot name a site.
func (c *GraphStageCoordinates) AttachmentPillarLeaves(
	node string,
	attachment string,
	pillar string,
) []constraint.AttachmentLeaf {
	entry, held := c.attachment(node, attachment)
	if !held {
		return nil
	}
	var names []string
	switch pillar {
	case pillarValues:
		names = entry.ValuesProvenance.Keys()
	case pillarInfra:
		names = entry.InfraProvenance.Keys()
	default:
		return nil
	}
	out := make([]constraint.AttachmentLeaf, 0, len(names))
	for _, name := range names {
		out = append(out, constraint.AttachmentLeaf{
			Value: name,
			Site:  c.sites.siteOf(node, attachment, pillar, name),
		})
	}
	return out
}

// Mechanisms returns the delivery mechanisms a node's own injection block
// declares, in the order that block enumerates them: the mechanisms it names as
// fields first, then any further mechanism in author order.
//
// It reads the block the finalised graph carries for the node, which is what
// keys this axis where the facts a rule over it reads are keyed. The equivalent
// fact belongs to an earlier stage and is answered from the documents that stage
// holds — the application's own instances — so it holds no entry for a component
// a sub-aggregate contributed, whose identity here carries the contributing
// module's prefix. An axis delegating to it would present a rule no subject at
// all for such a node, and a rule invoked zero times reports exactly what a rule
// invoked and satisfied reports.
//
// A node the graph does not hold, and one whose author wrote no injection block,
// both contribute nothing: there is no block to enumerate.
func (c *GraphStageCoordinates) Mechanisms(node string) []string {
	return resolver.InjectMechanismNames(c.selfInject(node))
}

// DeliveryKeys returns the keys a node's own injection block delivers under one
// mechanism, in author order where the authored shape carries one and
// alphabetically where it does not.
//
// The order is the authored shape's rather than a chosen one, because a rule
// raises in the order it is invoked and that order reaches the build's output.
// Every mechanism is authored as an array of entries, each naming the identifier
// it delivers into, so the authored order is the array's; the mapping shapes are
// answered in sorted order because a plain mapping carries none.
//
// # Why this reads the block rather than the fact that names the same keys
//
// This is a second reader of one authored shape, left deliberately and held to
// the first by a parity assertion rather than by eye. The reason is the one the
// mechanism axis above records: the fact naming these keys belongs to an earlier
// stage, is answered from that stage's own documents, and therefore cannot answer
// for a node the composition contributed. Reading the block the graph carries is
// what makes the axis complete over the flattened node set. A reader tempted to
// collapse the two should export the earlier stage's own key reading and call it
// with this block, which removes the duplication without reintroducing the
// keying gap; re-deriving the keys a third time would not.
func (c *GraphStageCoordinates) DeliveryKeys(node string, mechanism string) []string {
	return injectDeliveryKeys(c.selfInject(node), mechanism)
}

// selfInject returns the injection block a node's author wrote, nil when the
// graph holds no such node or the node carries no block.
func (c *GraphStageCoordinates) selfInject(node string) *types.InjectBlock {
	n, held := c.graph.Spec.Nodes.Get(node)
	if !held {
		return nil
	}
	return n.SelfInject
}

// injectMechanismPayload returns what an injection block carries under one
// mechanism name, nil when it carries nothing there.
//
// The three mechanisms the block names as fields are read from those fields and
// every other from the block's remaining entries, which is the same division the
// authored shape makes.
func injectMechanismPayload(inject *types.InjectBlock, mechanism string) any {
	if inject == nil {
		return nil
	}
	switch mechanism {
	case injectMechanismEnv:
		return inject.Env
	case injectMechanismFile:
		return inject.File
	case injectMechanismArg:
		return inject.Arg
	}
	payload, held := inject.Additional.Get(mechanism)
	if !held {
		return nil
	}
	return payload
}

// injectDeliveryKeys returns the identifiers one mechanism of an injection block
// delivers into.
//
// The array shape is the one an authored document reaches: every mechanism is
// authored as an array of entries, and each entry is decoded through the one
// decoder that reads such an entry rather than by reaching into it here. The
// mapping shapes are read defensively and sorted, since a mapping carries no
// order of its own; an entry that decodes to no identifier is skipped rather
// than contributing an empty coordinate no fact could answer for.
func injectDeliveryKeys(inject *types.InjectBlock, mechanism string) []string {
	switch payload := injectMechanismPayload(inject, mechanism).(type) {
	case []any:
		out := make([]string, 0, len(payload))
		for _, entry := range payload {
			if in, _, _, _, _, ok := types.DecodeVerboseInjectEntry(entry); ok {
				out = append(out, in)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case *types.OrderedMap[any]:
		if payload == nil {
			return nil
		}
		return payload.Keys()
	case map[string]any:
		return sortedKeys(payload)
	case map[string]string:
		return sortedKeys(payload)
	default:
		return nil
	}
}

// sortedKeys returns a mapping's keys in sorted order, which is the only
// deterministic order a plain mapping admits.
func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// attachment returns one attachment on one node from the merged set.
func (c *GraphStageCoordinates) attachment(node, attachment string) (types.IRFeatureEntry, bool) {
	merged, held := c.graph.Spec.FeatureValuesMapping.Get(node)
	if !held {
		return types.IRFeatureEntry{}, false
	}
	return merged.Get(attachment)
}

// aliasIsDerived reports whether a connection's alias was composed by the
// compiler rather than written by the author as a key.
//
// It matters to the locator and not to the coordinate: an alias no file spells
// contributes no segment to the authored path a diagnostic points at, while the
// alias itself still reaches the rule either way.
//
// Only a connection drawn from the component's own dependency block can carry a
// derived alias here, and the node's authored block is what says so — the short
// authoring form lists targets as array elements and gives each connection the
// target's own name, so no key in the file spells it. A connection an attachment
// projected or a Release wired is treated as author-written: the Release's
// edge surface names an alias per entry as a key, and an attachment's own block
// is authored in a document this stage's node does not carry, so claiming a
// derived alias for one would be a guess rather than a reading.
//
// It takes the origin kind rather than a connection, because the two
// enumerations that need the answer hold the connection in two different shapes
// — the finalised graph's edge and the composition's — and a second copy keyed
// on the other shape would be free to answer differently.
func aliasIsDerived(graph *types.IRGraph, nodeID, socketName, originKind string) bool {
	if originKind != "" && originKind != constraint.OriginComponent {
		return false
	}
	node, held := graph.Spec.Nodes.Get(nodeID)
	if !held {
		return false
	}
	socket, populated := node.Uses.Get(socketName)
	if !populated {
		return false
	}
	return len(socket.Compact) > 0
}

// originKindOf is the surface a finalised edge was drawn from, read off the
// marks the composition stamped on it. It names the kind alone; the site that
// surface's block was written at is carried on the compilation product that
// settled it.
func originKindOf(edge types.IREdge) string {
	switch {
	case edge.OriginRelease:
		return constraint.OriginRelease
	case edge.OriginFeatureInstance != "" || edge.OriginFeature != "":
		return constraint.OriginAttachment
	default:
		return constraint.OriginComponent
	}
}

// The compiler holds this type to every axis the rules over this stage are
// invoked once per, rather than a comment claiming it: a traversal those rules
// reach for that this value cannot supply stops compiling here.
var (
	_ constraint.NodeEnumerator                 = (*GraphStageCoordinates)(nil)
	_ constraint.EdgeEnumerator                 = (*GraphStageCoordinates)(nil)
	_ constraint.OriginatedConnectionEnumerator = (*GraphStageCoordinates)(nil)
	_ constraint.AttachmentEnumerator           = (*GraphStageCoordinates)(nil)
	_ constraint.SlotEnumerator                 = (*GraphStageCoordinates)(nil)
	_ constraint.MechanismEnumerator            = (*GraphStageCoordinates)(nil)
	_ constraint.DeliveryKeyEnumerator          = (*GraphStageCoordinates)(nil)
	_ constraint.AttachmentPillarEnumerator     = (*GraphStageCoordinates)(nil)
	_ constraint.AttachmentPillarLeafEnumerator = (*GraphStageCoordinates)(nil)
)
