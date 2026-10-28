// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nodeDispatchKey evaluates one Recipe-bound node's dispatch key ahead of the
// per-node loop, from the inputs that loop reads for the same node: the bare
// engine kind of the Recipe's capability, the Recipe's name, the node's boundary
// path, its boundary placement and the Release's parameters. It is the one place
// those inputs are gathered for an evaluation ahead of the loop, so every early
// reader — the cross-boundary link classification and the assembly paths an edge
// view carries — asks the same question in the same words.
func nodeDispatchKey(
	nodeID string,
	node types.IRNode,
	recipe *types.Recipe,
	input RendererInput,
	parameters map[string]any,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (dispatchKey, error) {
	return onDemandDispatchKey(nodeID, ProviderEngineKind(recipe.Spec.Capability), recipe.Metadata.Name,
		node.BoundaryPath, boundaryInfraFor(input, nodeID), parameters, boundaryDirEvals, perBoundaryAnnotations)
}

// assemblyPathsByNode answers, for every node of the graph a Recipe binds, the
// directory its provider assembles its output into: the dispatch path the
// per-node render groups the node by and hands that group's assembler. A
// boundary-scope edge view carries each endpoint's answer as its assembly path,
// which is what lets a provider place an endpoint on the network the assembler
// places it on.
//
// It is answered by [nodeDispatchKey], over [onDemandDispatchKey], the evaluation
// the per-node loop runs, so every answer is the loop's own value rather than a
// second derivation that agrees with it today. It is answered once, before any
// template is evaluated, because the boundary-scope provides pre-pass builds edge
// views ahead of the loop.
//
// A node no Recipe binds, and a node whose path evaluation fails, has no answer
// here: the per-node loop halts on that same evaluation where it always has, so
// nothing reads an empty answer as a placement.
func assemblyPathsByNode(
	graph *types.IRGraph,
	nodeRecipes map[string]*types.Recipe,
	input RendererInput,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) map[string]string {
	if graph == nil {
		return nil
	}
	var parameters map[string]any
	if input.ResolveResult != nil {
		parameters = input.ResolveResult.Parameters
	}
	out := make(map[string]string, graph.Spec.Nodes.Len())
	for nodeID, node := range graph.Spec.Nodes.All() {
		recipe := nodeRecipes[nodeID]
		if recipe == nil {
			continue
		}
		key, err := nodeDispatchKey(nodeID, node, recipe, input, parameters, boundaryDirEvals, perBoundaryAnnotations)
		if err != nil {
			continue
		}
		out[nodeID] = key.Path
	}
	return out
}
