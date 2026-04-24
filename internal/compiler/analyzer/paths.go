// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

const maxPathsPerNode = 1000

// calculatePaths performs a cycle-safe DFS from each node to enumerate all acyclic paths.
// Adjacency lists are sorted for deterministic traversal order.
func calculatePaths(graph *types.IRGraph) []PrecalculatedPath {
	adj := buildAdjacency(graph.Spec.Edges)
	featuresByNode := collectFeatureTypes(graph)

	var paths []PrecalculatedPath
	pathCounter := 0

	// Start DFS from each node in sorted order
	nodeIDs := sortedNodeIDs(graph.Spec.Nodes)
	for _, startNode := range nodeIDs {
		neighbors := adj[startNode]
		if len(neighbors) == 0 {
			continue
		}

		nodePaths := 0
		dfs(startNode, []string{startNode}, map[string]bool{startNode: true}, adj, featuresByNode, &paths, &pathCounter, &nodePaths)
	}

	return paths
}

func dfs(
	current string,
	path []string,
	visited map[string]bool,
	adj map[string][]string,
	featuresByNode map[string][]string,
	result *[]PrecalculatedPath,
	counter *int,
	nodePaths *int,
) {
	neighbors := adj[current]
	if len(neighbors) == 0 {
		// Terminal node — emit path if length > 1
		if len(path) > 1 {
			emitPath(path, featuresByNode, result, counter)
			*nodePaths++
		}
		return
	}

	extended := false
	for _, next := range neighbors {
		if visited[next] {
			continue // cycle detected on this path — skip
		}
		if *nodePaths >= maxPathsPerNode {
			return
		}

		extended = true
		visited[next] = true
		dfs(next, append(path, next), visited, adj, featuresByNode, result, counter, nodePaths)
		delete(visited, next)
	}

	if !extended && len(path) > 1 {
		// All neighbors were visited (cycles) — emit current path
		emitPath(path, featuresByNode, result, counter)
		*nodePaths++
	}
}

func emitPath(path []string, featuresByNode map[string][]string, result *[]PrecalculatedPath, counter *int) {
	*counter++

	// Collect attached features from all nodes in the path
	featureSet := map[string]bool{}
	for _, nodeID := range path {
		for _, feat := range featuresByNode[nodeID] {
			featureSet[feat] = true
		}
	}
	features := make([]string, 0, len(featureSet))
	for f := range featureSet {
		features = append(features, f)
	}
	sort.Strings(features)

	// Copy path to avoid slice aliasing
	pathCopy := make([]string, len(path))
	copy(pathCopy, path)

	*result = append(*result, PrecalculatedPath{
		PathID:           fmt.Sprintf("path_%02d", *counter),
		Nodes:            pathCopy,
		AttachedFeatures: features,
	})
}

// buildAdjacency builds a sorted adjacency list from IR edges.
func buildAdjacency(edges []types.IREdge) map[string][]string {
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
	}
	// Sort each adjacency list for determinism
	for k := range adj {
		sort.Strings(adj[k])
	}
	return adj
}

// collectFeatureTypes collects all feature types attached to each node (from feature_values_mapping).
func collectFeatureTypes(graph *types.IRGraph) map[string][]string {
	result := map[string][]string{}
	for nodeID, features := range graph.Spec.FeatureValuesMapping.All() {
		var featTypes []string
		for _, entry := range features.All() {
			if entry.Type != "" {
				featTypes = append(featTypes, entry.Type)
			}
		}
		sort.Strings(featTypes)
		result[nodeID] = featTypes
	}
	return result
}

func sortedNodeIDs(nodes types.OrderedMap[types.IRNode]) []string {
	ids := nodes.Keys()
	sort.Strings(ids)
	return ids
}
