// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
// axes no fact names — the node set, the edge set, an attachment's pillars and
// their leaves — are read from the products this value holds.
//
// # Order
//
// Every axis returns a deterministic order and each states which, because a
// rule raises in the order it is invoked and that order reaches the build's
// output. The node set, the attachment set and each pillar's leaves come from
// ordered carriers and keep the order those carriers hold; the edge set keeps
// the order the graph holds it in; the slot set keeps the order the component
// type declares its slots in; the pillar axis returns a fixed declared order.
// No axis here ranges a plain Go map.
type GraphStageCoordinates struct {
	graph         *types.IRGraph
	view          constraint.GraphView
	catalogLookup resolver.CatalogLookup

	// nodes is the enumerated node set, folded once when this value is built
	// because it is the outermost order of every node-keyed traversal.
	nodes []constraint.NodeInstance

	// edges is the enumerated edge set, folded once alongside the nodes: an
	// edge's subject carries the provenance of the component it is authored
	// in, so building one needs the node the edge is sourced at.
	edges []constraint.EdgeInstance
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
// It returns nil when either the graph or the presentation is absent. An
// enumeration built from nothing would present every rule an empty subject set,
// and a rule invoked zero times reports exactly what a rule invoked and
// satisfied reports. A caller checks for nil rather than being handed a value
// that answers emptily.
func NewGraphStageCoordinates(
	graph *types.IRGraph,
	view constraint.GraphView,
	catalogLookup resolver.CatalogLookup,
) *GraphStageCoordinates {
	if graph == nil || view == nil {
		return nil
	}
	c := &GraphStageCoordinates{graph: graph, view: view, catalogLookup: catalogLookup}

	byID := make(map[string]constraint.NodeInstance, graph.Spec.Nodes.Len())
	for id, node := range graph.Spec.Nodes.All() {
		instance := constraint.NodeInstance{
			ID:                   id,
			SourceFile:           node.SourceFile,
			AuthoredName:         authoredComponentName(id),
			SubsystemPath:        node.SubsystemPath,
			SynthesizedSubsystem: node.SynthesizedSubsystem,
		}
		c.nodes = append(c.nodes, instance)
		byID[id] = instance
	}

	for _, edge := range graph.Spec.Edges {
		node, held := byID[edge.Source]
		if !held {
			// An edge whose consumer is not in the node set has no component
			// block for its locator to be named relative to, so there is
			// nothing to present a rule with. The graph does not produce that
			// shape; the guard keeps a rule from being handed a coordinate
			// whose provenance would have to be invented.
			continue
		}
		c.edges = append(c.edges, constraint.EdgeInstance{
			Node:             node,
			Socket:           edge.Socket,
			Alias:            edge.Alias,
			SynthesizedAlias: c.aliasIsDerived(edge),
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
// own dependency block, an attachment's, or the deployment's — because the graph
// holds one edge per connection however it arrived. The origin is a fact a rule
// reads, not a reason to enumerate the same connection twice.
func (c *GraphStageCoordinates) Edges() []constraint.EdgeInstance { return c.edges }

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
// per-leaf provenance for, in the fixed order the pillars are declared in.
//
// The two pillars are a fixed authoring surface rather than an open set, so the
// order is declared here rather than derived from a carrier. A pillar the
// attachment carries no provenance for is absent, because the leaf axis beneath
// it would be empty and enumerating it would claim the attachment wrote a
// pillar it did not.
func (c *GraphStageCoordinates) AttachmentPillars(node string, attachment string) []string {
	entry, held := c.attachment(node, attachment)
	if !held {
		return nil
	}
	var out []string
	if entry.ValuesProvenance.Len() > 0 {
		out = append(out, pillarValues)
	}
	if entry.InfraProvenance.Len() > 0 {
		out = append(out, pillarInfra)
	}
	return out
}

// AttachmentPillarLeaves returns the leaves of one pillar of one attachment's
// configuration, in the order the provenance record holds them.
//
// The leaves come from the provenance record rather than from the merged
// payload, and that is what keys the axis where the facts are keyed: a leaf
// coordinate is a dotted path into the pillar, which is the shape the provenance
// record is keyed by, while the payload is a nested map whose top-level keys are
// a different set. Enumerating the payload would hand a rule coordinates no fact
// at this stage answers for.
func (c *GraphStageCoordinates) AttachmentPillarLeaves(
	node string,
	attachment string,
	pillar string,
) []string {
	entry, held := c.attachment(node, attachment)
	if !held {
		return nil
	}
	switch pillar {
	case pillarValues:
		return entry.ValuesProvenance.Keys()
	case pillarInfra:
		return entry.InfraProvenance.Keys()
	default:
		return nil
	}
}

// attachment returns one attachment on one node from the merged set.
func (c *GraphStageCoordinates) attachment(node, attachment string) (types.IRFeatureEntry, bool) {
	merged, held := c.graph.Spec.FeatureValuesMapping.Get(node)
	if !held {
		return types.IRFeatureEntry{}, false
	}
	return merged.Get(attachment)
}

// aliasIsDerived reports whether an edge's alias was composed by the compiler
// rather than written by the author as a key.
//
// It matters to the locator and not to the coordinate: an alias no file spells
// contributes no segment to the authored path a diagnostic points at, while the
// alias itself still reaches the rule either way.
//
// Only a connection drawn from the component's own dependency block can carry a
// derived alias here, and the node's authored block is what says so — the short
// authoring form lists targets as array elements and gives each connection the
// target's own name, so no key in the file spells it. A connection an attachment
// projected or a deployment wired is treated as author-written: the deployment's
// edge surface names an alias per entry as a key, and an attachment's own block
// is authored in a document this stage's node does not carry, so claiming a
// derived alias for one would be a guess rather than a reading.
func (c *GraphStageCoordinates) aliasIsDerived(edge types.IREdge) bool {
	if edge.OriginFeature != "" || edge.OriginRelease {
		return false
	}
	node, held := c.graph.Spec.Nodes.Get(edge.Source)
	if !held {
		return false
	}
	socket, populated := node.Uses.Get(edge.Socket)
	if !populated {
		return false
	}
	return len(socket.Compact) > 0
}

// authoredComponentName returns the name the author typed for a node, taken
// from the identifier the flattened set keys it by.
//
// A node a sub-aggregate contributes carries the contributing module's dotted
// prefix ahead of its own name, and the separator is reserved to the compiler
// rather than available to an author — so the segment after the last separator
// is the name, and a node no module contributed is its own name entire.
//
// The analyzer's hand-written readings derive the same name the same way for
// their own locators. That near-duplicate is deliberate for now rather than an
// oversight: those readings are removed once the rules beside them are invoked
// through the framework, and the derivation retires with them onto this one.
func authoredComponentName(nodeID string) string {
	if i := strings.LastIndex(nodeID, "."); i >= 0 {
		return nodeID[i+1:]
	}
	return nodeID
}

// The compiler holds this type to every axis the rules over this stage are
// invoked once per, rather than a comment claiming it: a traversal those rules
// reach for that this value cannot supply stops compiling here.
var (
	_ constraint.NodeEnumerator                 = (*GraphStageCoordinates)(nil)
	_ constraint.EdgeEnumerator                 = (*GraphStageCoordinates)(nil)
	_ constraint.AttachmentEnumerator           = (*GraphStageCoordinates)(nil)
	_ constraint.SlotEnumerator                 = (*GraphStageCoordinates)(nil)
	_ constraint.AttachmentPillarEnumerator     = (*GraphStageCoordinates)(nil)
	_ constraint.AttachmentPillarLeafEnumerator = (*GraphStageCoordinates)(nil)
)
