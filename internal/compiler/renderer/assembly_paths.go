// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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

// boundaryFRDispatch evaluates where one boundary-scope FeatureRecipe
// attachment's output is routed: the dispatch key its output joins, and the
// evaluation that key's path was composed from. It is the one evaluation of that
// placement, read by the routing that places the attachment's output after the
// per-node loop and by the attachment's own boundary view before any of its
// templates is evaluated, so the directory a template reads as
// .boundary.assembly_path and the directory the output lands in are one value.
//
// The attachment is attached at the boundary rather than placed per component,
// so its placement is the boundary's own authored directory: and subDirectory:,
// read from the Release tree, with the FeatureRecipe's bare engine kind as the
// capability segment. The identity handed to the path evaluation is the
// instance-only marker, which is what an operator's subDirectory: template reads
// as .component_id, and what decides which dispatch group this contribution
// joins. Two boundaries whose declared directories evaluate alike are meant to
// feed one group, so the value stays instance-only rather than carrying the
// boundary path.
func boundaryFRDispatch(
	key boundaryFRKey,
	fr *types.FeatureRecipe,
	input RendererInput,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (dispatchKey, dispatchPathEvaluation, error) {
	var parameters map[string]any
	var bi *resolver.BoundaryInfra
	if input.ResolveResult != nil {
		parameters = input.ResolveResult.Parameters
		if input.ResolveResult.Release != nil {
			if b, ok := findBoundaryAtPath(input.ResolveResult.Release.Spec.Boundaries, strings.Split(key.BoundaryPath, "/")); ok {
				bi = &resolver.BoundaryInfra{
					BoundaryPath: key.BoundaryPath,
					Directory:    b.Directory,
					SubDirectory: b.SubDirectory,
					SyncWave:     b.SyncWave,
				}
			}
		}
	}
	provider := ProviderEngineKind(fr.Spec.Capability)
	eval, err := computeFinalPath(boundaryFRNodePrefix+key.FRInstance, key.BoundaryPath, provider, fr.Metadata.Name, bi, parameters, boundaryDirEvals, perBoundaryAnnotations)
	if err != nil {
		return dispatchKey{}, dispatchPathEvaluation{}, err
	}
	return dispatchKey{Path: eval.path, Provider: provider}, eval, nil
}

// assemblyPathsByBoundaryFR answers, for every boundary-scope FeatureRecipe
// attachment of the graph, the directory its own output is assembled into: the
// path of the dispatch key [boundaryFRDispatch] routes it under. The attachment's
// boundary view carries the answer as its assembly path, which is what lets the
// FeatureRecipe tell a connection whose endpoints are assembled into its own file
// from one whose endpoints are assembled elsewhere.
//
// It is answered once, before any template is evaluated, because the
// boundary-scope provides pre-pass builds the boundary view ahead of the per-node
// loop. An attachment no FeatureRecipe binds, one whose FeatureRecipe is not
// boundary-scope, and one whose path evaluation fails have no answer here, and
// their view carries an empty assembly path: the routing evaluates the same
// placement for every attachment that rendered and halts on that same failure
// before any output is placed.
func assemblyPathsByBoundaryFR(
	graph *types.IRGraph,
	recipeLookup RecipeLookup,
	input RendererInput,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) map[boundaryFRKey]string {
	if graph == nil || recipeLookup.FeatureRecipe == nil {
		return nil
	}
	out := map[boundaryFRKey]string{}
	for boundaryPath, feats := range graph.Spec.BoundaryFeatures.All() {
		for instName, bf := range feats.All() {
			fr, ok := recipeLookup.FeatureRecipe(instName, bf.Type)
			if !ok || fr.Spec.ExecutionScope != executionScopeBoundary {
				continue
			}
			key := boundaryFRKey{BoundaryPath: boundaryPath, FRInstance: instName}
			dKey, _, err := boundaryFRDispatch(key, fr, input, boundaryDirEvals, perBoundaryAnnotations)
			if err != nil {
				continue
			}
			out[key] = dKey.Path
		}
	}
	return out
}
