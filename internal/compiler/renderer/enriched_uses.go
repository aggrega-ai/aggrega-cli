// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// AssembleEnrichedUses builds the enriched topological edge map for a node.
// Each entry is a JOIN projection of the target node's resolved provides,
// labels, annotations, component type, version, and boundary_path.
// Keyed by the developer's alias (from the edge).
func AssembleEnrichedUses(
	nodeID string,
	graph *types.IRGraph,
	allProvides NodeProvides,
) map[string]types.EnrichedEdge {
	result := make(map[string]types.EnrichedEdge)

	for _, edge := range graph.Spec.Edges {
		if edge.Source != nodeID {
			continue
		}

		targetNode, ok := graph.Spec.Nodes[edge.Target]
		if !ok {
			continue
		}

		ee := types.EnrichedEdge{
			Target:          edge.Target,
			Socket:          edge.Socket,
			Provides:        ensureStringMap(allProvides[edge.Target]),
			Inject:          edge.Inject,
			Labels:          targetNode.Labels,
			Component:       targetNode.Component,
			ResolvedVersion: targetNode.ResolvedVersion,
			BoundaryPath:    targetNode.BoundaryPath,
			OriginFeature:   edge.OriginFeature,
		}

		// Target annotations as buckets
		if targetNode.CatalogAnnotations != nil || targetNode.ReleaseAnnotations != nil || targetNode.AggregateAnnotations != nil {
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
		result[alias] = ee
	}

	return result
}
