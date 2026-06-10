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
