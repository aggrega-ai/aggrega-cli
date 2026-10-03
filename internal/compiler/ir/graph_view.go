// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// graphView presents the finalised graph to a check as named facts: the label
// set a node ends up with, what each attachment resolved to, where each leaf of
// its configuration came from and which component key it was written under, and
// what each edge resolved to and delivers.
//
// It satisfies [constraint.GraphView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before
// the graph was finalised is readable here. Those facts are answered by the
// presentation the flattening already built, embedded and delegated to, rather
// than re-derived from the graph: one fact has one producer, and two producers
// of a fact are free to disagree about it with nothing comparing them. The
// graph carries a node's component type and a boundary path of its own, so
// re-reading them here would be easy and would be exactly that second producer;
// no accessor below does.
//
// What this stage adds is what the cascade and the annotation-driven expansion
// produce, and nothing that was complete before them. An attachment's resolved
// type is here rather than earlier because a scope above the node can
// contribute it; a leaf's provenance and the component key it was written under
// are here for one reason between them, that both record which contribution
// won.
//
// One fact here is a RETENTION rather than a production, and saying so is part
// of placing it. A connection's delivery as the author wrote it is complete when
// the composition finishes, one stage earlier: every surface that draws a
// connection has contributed by then and this stage draws none. What is settled
// HERE is the surface each connection's origin was declared on, which the same
// reading of the authoring tiers that folds the cascade settles — and the fact
// is keyed by that origin, so it is complete only once both halves are. So the
// content travels from the earlier stage unchanged and the coordinate it is
// keyed by is this stage's; a declaration one stage earlier could not be keyed
// at all.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract: what an accessor returns aliases what the
// compilation already holds.
type graphView struct {
	constraint.TopologyView

	// nodes is the finalised node set, by the identity every keyed fact takes.
	nodes types.OrderedMap[types.IRNode]

	// attachments is each node's merged attachment set, by that node's identity
	// and then by the attachment's own name, as the cascade folded them.
	attachments types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]

	// edges is every edge the graph holds, in the order it holds them. The edge
	// facts are keyed rather than enumerated, so this is walked per read instead
	// of folded once: an edge set is small beside the node set, and folding it
	// would hold a second index of what the graph already orders.
	edges []types.IREdge

	// sites names, per attachment leaf, where the contribution that won it was
	// written, and per attachment, every place that contributed it. It is held
	// rather than derived because the graph cannot answer either: the merged
	// entry keeps the winning value and its origin and never the contest or the
	// tiers, so which scope supplied any one leaf, and which scopes wrote the
	// attachment at all, is settled while the cascade folds and is gone once it
	// finishes.
	sites AttachmentSites

	// connections holds, per origin-keyed connection, the delivery that
	// connection dictates as the author wrote it. It is held for the same class
	// of reason the sites are: the conversion into the graph stores a delivery
	// as one string per delivery key, so the authored distinction between a
	// literal and a reference to a published key is gone by the time anything
	// here could read it back.
	connections *OriginatedConnections
}

// NewGraphView presents the graph a build has just finalised, with the facts
// complete before it answered by the presentation the flattening produced.
//
// The products are taken rather than reached for, because a view projects facts
// already produced and never produces one of its own.
//
// It returns nil when any product is absent. A presentation missing the graph
// would answer this stage's facts from nothing, one missing the earlier
// presentation would answer every earlier fact from nothing, one missing the
// originated connections would report every connection as dictating no delivery —
// which is the answer a connection dictating none legitimately has, so a rule
// could not tell the two apart — and one missing the
// per-leaf sites would report every leaf as written where no component is named
// — and a presentation that answers emptily is worse than an absent one, because
// a rule reading it cannot tell an empty answer from a real one. The sites are
// the sharpest case of that: naming no component is itself a real answer, the
// one a scope-wide entry has, so an absent projection would not read as absent.
// A projection holding no leaf is a different thing and is accepted, because a
// build whose attachments configure nothing has no leaf to record.
func NewGraphView(
	graph *types.IRGraph,
	earlier constraint.TopologyView,
	sites AttachmentSites,
	connections *OriginatedConnections,
) constraint.GraphView {
	if graph == nil || earlier == nil || !sites.present() || connections == nil {
		return nil
	}
	return &graphView{
		TopologyView: earlier,
		nodes:        graph.Spec.Nodes,
		attachments:  graph.Spec.FeatureValuesMapping,
		edges:        graph.Spec.Edges,
		sites:        sites,
		connections:  connections,
	}
}

// NodeLabels returns the label set a node carries once every contributing
// surface has been folded, in author order.
func (v *graphView) NodeLabels(node string) types.OrderedMap[string] {
	n, ok := v.nodes.Get(node)
	if !ok {
		return types.OrderedMap[string]{}
	}
	return n.Labels
}

// MergedAttachmentNames returns the attachment names a node carries once the
// whole attachment cascade has folded, in the order the merged set holds them.
func (v *graphView) MergedAttachmentNames(node string) []string {
	merged, ok := v.attachments.Get(node)
	if !ok {
		return nil
	}
	return merged.Keys()
}

// AttachmentContributions returns the places one attachment a node carries was
// contributed from, in the order the cascade folded them, and none for an
// attachment the node does not carry.
//
// It is answered from the product the cascade wrote while it folded rather than
// from the graph, because the merged entry keeps no record of which tiers
// contributed it.
func (v *graphView) AttachmentContributions(node string, attachment string) []constraint.AttachmentContribution {
	return v.sites.contributionsOf(node, attachment)
}

// AttachmentConnection returns the connection one attachment a node carries was
// written on — its socket, its alias and the slot the attachment fills there —
// and whether it was written on one at all.
//
// It reads the record the lowering carried onto the merged entry and nothing
// else; the attachment's composed name is never split to recover any part of it.
func (v *graphView) AttachmentConnection(node string, attachment string) (constraint.AttachmentConnection, bool) {
	entry, ok := v.attachment(node, attachment)
	if !ok || entry.Connection == nil {
		return constraint.AttachmentConnection{}, false
	}
	return constraint.AttachmentConnection{
		Socket: entry.Connection.Socket,
		Alias:  entry.Connection.Alias,
		Slot:   entry.Connection.Attachment,
	}, true
}

// AttachmentResolvedType returns the Feature type an attachment resolves to,
// empty when it resolved to no catalog entity.
func (v *graphView) AttachmentResolvedType(node string, attachment string) string {
	entry, ok := v.attachment(node, attachment)
	if !ok {
		return ""
	}
	return entry.Type
}

// PerLeafAttachmentProvenance returns where one leaf of an attachment's
// configuration came from.
//
// A pillar this stage carries no provenance map for, and a leaf that pillar's
// map does not hold, both return the zero record. That is the same answer as a
// leaf the author wrote explicitly with no annotation behind it, and
// deliberately so: the record's own fields are what a rule reads, and every one
// of them is absent in both cases.
func (v *graphView) PerLeafAttachmentProvenance(
	node string,
	attachment string,
	pillar string,
	leaf string,
) constraint.LeafProvenance {
	entry, ok := v.attachment(node, attachment)
	if !ok {
		return constraint.LeafProvenance{}
	}
	var provenance types.OrderedMap[types.FieldProvenance]
	switch pillar {
	case pillarValues:
		provenance = entry.ValuesProvenance
	case pillarInfra:
		provenance = entry.InfraProvenance
	default:
		return constraint.LeafProvenance{}
	}
	prov, held := provenance.Get(leaf)
	if !held {
		return constraint.LeafProvenance{}
	}
	out := constraint.LeafProvenance{
		Origin:           string(prov.Origin),
		AnnotationKey:    prov.AnnotationKey,
		AnnotationSource: prov.AnnotationSource,
	}
	// The displacement is carried as a reference to the provenance that lost,
	// so its presence IS the answer to whether anything was displaced.
	if prov.OverriddenAnnotation != nil {
		out.DisplacesAnnotation = true
		out.DisplacedAnnotationKey = prov.OverriddenAnnotation.AnnotationKey
		out.DisplacedAnnotationSource = prov.OverriddenAnnotation.AnnotationSource
	}
	return out
}

// PerLeafAttachmentAuthoredComponent returns the component key the entry that
// supplied one leaf of an attachment's configuration was written under, empty
// when that entry was written scope-wide and names no component at all.
//
// A leaf the projection holds no site for reads as naming no component, which is
// the same answer a scope-wide entry gives. The two collapse deliberately: what
// a reader does with this value is name a component or name none, and there is
// nothing it could do with the difference.
func (v *graphView) PerLeafAttachmentAuthoredComponent(
	node string,
	attachment string,
	pillar string,
	leaf string,
) string {
	return v.sites.siteOf(node, attachment, pillar, leaf).Component
}

// PerEdgeOriginFeatureType returns the Feature type an edge originated from,
// empty when the edge was drawn from a component's own dependency block.
func (v *graphView) PerEdgeOriginFeatureType(node string, socket string, alias string) string {
	edge, ok := v.edge(node, socket, alias)
	if !ok {
		return ""
	}
	return edge.OriginFeature
}

// PerEdgeResolvedTarget returns the node an edge resolves to.
func (v *graphView) PerEdgeResolvedTarget(node string, socket string, alias string) string {
	edge, ok := v.edge(node, socket, alias)
	if !ok {
		return ""
	}
	return edge.Target
}

// PerEdgeInject returns what one dependency edge delivers: per delivery
// mechanism, the key it delivers under and the provides key it reads, in author
// order.
func (v *graphView) PerEdgeInject(
	node string,
	socket string,
	alias string,
) types.OrderedMap[types.OrderedMap[string]] {
	edge, ok := v.edge(node, socket, alias)
	if !ok {
		return types.OrderedMap[types.OrderedMap[string]]{}
	}
	return edge.Inject
}

// OriginatedConnectionAuthoredDelivery returns the delivery every connection one
// origin key names dictates, as the author wrote it, one entry per connection
// in the order the composition drew them.
//
// It is answered from the product that retained the authored payloads rather
// than from the graph's own edges: the conversion into the graph stores one
// string per delivery key, so a literal and a reference to a published key are
// the same shape there.
func (v *graphView) OriginatedConnectionAuthoredDelivery(
	node string,
	origin string,
	attachment string,
	socket string,
	alias string,
	target string,
) []types.OrderedMap[any] {
	return v.connections.deliveryOf(node, origin, attachment, socket, alias, target)
}

// UsesInjectDeliverySockets returns the dependency sockets through which a
// node's own dependency edges deliver one injection key under one mechanism, in
// the order the graph holds those edges.
//
// One entry is appended per edge that delivers the key, so a socket appears as
// often as an edge named for it delivers it. Two edges on one socket each
// delivering the key is two findings for a rule that reports per delivery, and
// collapsing them here would report one.
func (v *graphView) UsesInjectDeliverySockets(node string, mechanism string, key string) []string {
	var out []string
	for _, edge := range v.edges {
		if edge.Source != node {
			continue
		}
		delivered, carries := edge.Inject.Get(mechanism)
		if !carries {
			continue
		}
		if _, delivers := delivered.Get(key); !delivers {
			continue
		}
		out = append(out, edge.Socket)
	}
	return out
}

// attachment returns one attachment on one node from the merged set.
func (v *graphView) attachment(node string, attachment string) (types.IRFeatureEntry, bool) {
	merged, ok := v.attachments.Get(node)
	if !ok {
		return types.IRFeatureEntry{}, false
	}
	return merged.Get(attachment)
}

// edge returns the edge one node draws on one socket under one alias.
//
// The three coordinates together are the edge's identity: a node draws more
// than one edge on a socket, and the alias is what tells them apart.
func (v *graphView) edge(node string, socket string, alias string) (types.IREdge, bool) {
	for _, e := range v.edges {
		if e.Source == node && e.Socket == socket && e.Alias == alias {
			return e, true
		}
	}
	return types.IREdge{}, false
}

// The compiler holds this type to the contract rather than a comment claiming
// it: a signature drifting from the declared facts stops compiling here.
var _ constraint.GraphView = (*graphView)(nil)
