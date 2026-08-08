// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
			Provides:        allProvides[edge.Target],
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

		// Key by alias (developer's local name for this edge)
		alias := edge.Alias
		if alias == "" {
			alias = edge.Target
		}
		result.Set(alias, ee)
	}

	return result
}
