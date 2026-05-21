// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// maxPathsPerNode caps the number of acyclic paths emitted per
// starting node. Defends against combinatorial explosion in dense
// graphs where unbounded enumeration would grow exponentially in the
// node count even after cycle detection prunes back-edges.
const maxPathsPerNode = 1000

// calculatePaths enumerates the precalculated_paths array Step 4
// emits to the policy validator. From each node in the IR graph the
// function performs a cycle-safe depth-first search, emitting one
// [PrecalculatedPath] per acyclic path it discovers.
//
// Contract for callers:
//
//   - Cycle-safe. Cycles in the input graph terminate at the
//     back-edge; a node never appears twice within a single emitted
//     path.
//   - Terminates. A per-start-node cap (maxPathsPerNode) bounds
//     enumeration in dense graphs even when no cycle is present.
//   - Deterministic across runs of identical input. PathIDs assign
//     in DFS-emission order (path_01, path_02, … gap-free across the
//     full output, not per-start-node-numbered); AttachedFeatures
//     sort alphabetically; adjacency lists sort alphabetically before
//     traversal.
//   - Suffix paths included by design. Each start node enumerates
//     paths beginning at itself, so a logical sub-path may appear
//     multiple times with different PathIDs — policy authors often
//     need start-from-X enumeration ("is there a path from X to Y?")
//     rather than maximal-path-only.
//   - Length filter. Only paths of length > 1 (at least one edge)
//     emit; isolated nodes and dead-end starts produce no output.
//   - Cap behaviour. When maxPathsPerNode trips, enumeration for the
//     current start node stops without emitting the current partial
//     path; previously-emitted paths from that node remain. Total
//     output is bounded by maxPathsPerNode * |start nodes|.
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

// dfs is calculatePaths's recursive worker. The visited map is
// mutated on descent and restored on backtrack so it tracks only the
// nodes on the current path; emission happens at terminal nodes (no
// outgoing neighbors) and at fully-blocked nodes (all neighbors
// already visited on the current path).
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

// emitPath builds one [PrecalculatedPath] from the current DFS state.
// The path slice is copied because dfs continues to mutate the
// underlying array on backtrack via append/delete; an uncopied
// reference would corrupt already-emitted paths. AttachedFeatures
// sort alphabetically as the union of features attached to every
// node along the path.
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
