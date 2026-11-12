// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AssembleEnrichedUses builds the enriched topological edge map for a node.
// Each entry is a JOIN projection of the target node's resolved provides,
// labels, annotations, component type, version, and boundary_path, plus the
// edge's connection-scoped per-edge config (values / infra) and its origin
// markers. Keyed by the developer's alias (from the edge), so templates read
// it at .context.uses.<alias> — per-edge config at .context.uses.<alias>.values
// and .context.uses.<alias>.infra.
//
// The projection is what carries a dependency's deterministic pointer strings
// across the graph — a service name, an interpolation string, a formatted
// connection string — so a Recipe template reads a neighbour's published values
// as ordinary struct fields instead of walking the topology itself.
//
// A target's locals are not in the projection and never cross an edge. Locals
// are the producing Recipe's own private working state; provides is the entire
// surface a consumer can read. Nothing here reads the target's locals and the
// projected entry carries no field for them, so a template that reaches for one
// finds nothing rather than finding a value it was never meant to see.
//
// The projected provides bag is narrowed to the keys the target's Component
// DECLARES, so a consumer reads the target's published contract rather than
// whatever its Recipe happens to emit. That is what makes Recipes
// interchangeable: two Recipes realizing one Component present the same surface
// to a consumer, and swapping them cannot silently withdraw a key a consumer had
// come to depend on. A Recipe publishing more than its Component declares stays
// legal — the extra key is simply not part of the contract, and it is reported
// where it is caused rather than where it is read.
//
// Narrowing is skipped for a node the declared set does not cover, per
// NodeDeclaredProvides: no declaration read is not the same as a declaration of
// nothing.
//
// The projection is consumer-scoped by construction: the loop only includes
// edges whose Source is this node (the node's outgoing edges), so a node never
// sees per-edge config authored on an edge pointing at it. This is what keeps a
// target's render context invariant with respect to its consumers.
//
// The returned OrderedMap preserves edge emission order from the IR (which in
// turn reflects Aggregate author order through the topology builder).
func AssembleEnrichedUses(
	nodeID string,
	graph *types.IRGraph,
	allProvides NodeProvides,
	declaredProvides NodeDeclaredProvides,
) types.OrderedMap[types.EnrichedEdge] {
	result := types.OrderedMap[types.EnrichedEdge]{}

	for _, edge := range graph.Spec.Edges {
		if edge.Source != nodeID {
			continue
		}

		targetNode, ok := graph.Spec.Nodes.Get(edge.Target)
		if !ok {
			continue
		}

		ee := types.EnrichedEdge{
			Target:          edge.Target,
			Socket:          edge.Socket,
			Provides:        narrowToDeclared(allProvides[edge.Target], declaredProvides, edge.Target),
			Inject:          edge.Inject,
			Labels:          targetNode.Labels,
			Component:       targetNode.Component,
			ResolvedVersion: targetNode.ResolvedVersion,
			BoundaryPath:    targetNode.BoundaryPath,
			OriginFeature:   edge.OriginFeature,
			OriginRelease:   edge.OriginRelease,
			Values:          edge.Values,
			Infra:           edge.Infra,
		}

		// Target annotations as buckets
		if targetNode.CatalogAnnotations.Len() > 0 || targetNode.ReleaseAnnotations.Len() > 0 || targetNode.AggregateAnnotations.Len() > 0 {
			ee.Annotations = &types.AnnotationBuckets{
				Catalog:   targetNode.CatalogAnnotations,
				Release:   targetNode.ReleaseAnnotations,
				Aggregate: targetNode.AggregateAnnotations,
			}
		}

		// Keyed by alias alone — the developer's local name for the connection —
		// whatever socket it is wired into. No two connections of a built node
		// share an alias, on one socket or on two: the build refuses such a pair
		// once the graph is final, before anything renders, so no entry here
		// replaces another.
		result.Set(connectionAlias(edge), ee)
	}

	return result
}

// connectionAlias is the key a connection is held under in its consumer's uses
// map: the alias the author gave it, or its target where it carries none.
func connectionAlias(edge types.IREdge) string {
	if edge.Alias == "" {
		return edge.Target
	}
	return edge.Alias
}

// AssembleInboundEdges builds the connections other nodes make TO nodeID: one
// entry per edge whose Target is nodeID, carrying the connecting node's identity,
// the socket it connects through, its published provides, and where it is placed —
// its namespace and boundary path.
//
// The placement is read by edgeEndpointNetworkInputs, the one derivation the
// boundary-scope edge views read each endpoint's placement through, so a caller's
// namespace is the same value on every view that shows it. It is the caller's own
// namespace — the one its own render is handed, a Release entry's own setting
// included — and never the namespace of the node it connects to.
//
// It is the inverse of AssembleEnrichedUses and deliberately the narrower of the
// two. An entry carries no per-connection values or infra, no inject mapping and no
// attachments written on the connection: those belong to the consuming node's
// context and are kept there, so a per-connection setting still reaches only the
// node that authored it.
//
// The provides an entry carries are the connecting node's own bag narrowed to the
// keys its Component declares — the same narrowToDeclared reading a consumer of that
// node meets at .context.uses.<alias>.provides — so one node's view of another is
// one projection whichever end of the edge it is read from. Every node's provides
// are evaluated in one pass ahead of any node's render, which is what lets a
// connecting node's bag be read while the node it connects to is still rendering.
//
// 🔴 The result belongs on the render context of a Feature attached to nodeID and
// nowhere else. The node's own context must not carry it: that context is what the
// node's own Recipe renders against, and a node whose rendered bytes depended on who
// connects to it would change every time a consumer was wired to it.
//
// The order is the order the graph records the edges in, which is author order.
// Nothing is sorted: a node connecting twice appears twice, once per connection, in
// the order its connections were written. An edge whose source the graph does not
// hold as a node is skipped, on the same terms AssembleEnrichedUses skips an edge
// whose target it does not hold.
func AssembleInboundEdges(
	nodeID string,
	graph *types.IRGraph,
	allProvides NodeProvides,
	declaredProvides NodeDeclaredProvides,
) []types.InboundEdge {
	if graph == nil {
		return nil
	}
	var result []types.InboundEdge
	for _, edge := range graph.Spec.Edges {
		if edge.Target != nodeID {
			continue
		}
		if _, ok := graph.Spec.Nodes.Get(edge.Source); !ok {
			continue
		}
		boundaryPath, namespace := edgeEndpointNetworkInputs(edge.Source, graph)
		result = append(result, types.InboundEdge{
			ComponentID:        edge.Source,
			Socket:             edge.Socket,
			Provides:           narrowToDeclared(allProvides[edge.Source], declaredProvides, edge.Source),
			SourceNamespace:    namespace,
			SourceBoundaryPath: boundaryPath,
		})
	}
	return result
}

// narrowToDeclared returns the target's produced provides bag restricted to the
// keys its Component declares, in the producing Recipe's author order.
//
// It returns produced unchanged in two cases, and the first of them is the
// load-bearing one: when the target has no entry in declared at all, because an
// absent entry means no declaration was read rather than a declaration of
// nothing (see NodeDeclaredProvides). The second is when every produced key is
// already declared — the ordinary case for a well-authored pair — which keeps
// the common path allocation-free and the projected value bit-for-bit what it
// was before narrowing existed.
func narrowToDeclared(produced types.OrderedMap[string], declared NodeDeclaredProvides, target string) types.OrderedMap[string] {
	contract, covered := declared[target]
	if !covered {
		return produced
	}

	for k := range produced.All() {
		if contract[k] {
			continue
		}
		narrowed := types.OrderedMap[string]{}
		for k, v := range produced.All() {
			if contract[k] {
				narrowed.Set(k, v)
			}
		}
		return narrowed
	}
	return produced
}
