// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// graphView presents the finalised graph to a check as named facts: the label
// set a node ends up with, what each attachment resolved to and where each leaf
// of its configuration came from, and what each edge resolved to and delivers.
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
// contribute it; a leaf's provenance is here because it records which
// contribution won.
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
}

// NewGraphView presents the graph a build has just finalised, with the facts
// complete before it answered by the presentation the flattening produced.
//
// The products are taken rather than reached for, because a view projects facts
// already produced and never produces one of its own.
//
// It returns nil when either product is absent. A presentation missing the
// graph would answer this stage's facts from nothing, and one missing the
// earlier presentation would answer every earlier fact from nothing — and a
// presentation that answers emptily is worse than an absent one, because a rule
// reading it cannot tell an empty answer from a real one.
func NewGraphView(graph *types.IRGraph, earlier constraint.TopologyView) constraint.GraphView {
	if graph == nil || earlier == nil {
		return nil
	}
	return &graphView{
		TopologyView: earlier,
		nodes:        graph.Spec.Nodes,
		attachments:  graph.Spec.FeatureValuesMapping,
		edges:        graph.Spec.Edges,
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
