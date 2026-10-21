// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stampCrossBoundaryLinkSentinels overwrites the current node's cross-boundary
// injected values with deterministic link placeholders before the node's
// artifacts render, so the placeholder is what the render pipes into the
// consuming output. The post-assembly link pass then locates each placeholder
// in the assembled output and replaces it with the provider-native
// cross-reference — a two-stage handshake because the consuming render runs per
// node while the reference can only be shaped once every dispatch group has
// assembled and the peer's published identity is known.
//
// The stamp fires only for a consumer whose own provider advertises
// cross-boundary linking, and only on an edge the shared classifier rules a
// firing cross-boundary link. It derives each end's dispatch key on demand from
// the same pure path evaluation the per-node loop runs, so the stamp's view of
// which edges cross a boundary matches the link pass's view exactly. A same-
// provider crossing reports one linking claim for both ends, so the consumer's
// claim stands in for the target's; the cross-provider case the classifier
// decides before it reads the second claim.
//
// Only a value already delivered on the edge (present in the inject pipe after
// the feature-order overlays) is overwritten: the stamp replaces a delivered
// scalar, it never introduces an injection the edge did not carry.
func stampCrossBoundaryLinkSentinels(
	tmplCtx *types.TemplateContext,
	nodeID, providerName, recipeName string,
	engine Provider,
	node types.IRNode,
	graph *types.IRGraph,
	nodeRecipes map[string]*types.Recipe,
	input RendererInput,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) error {
	if !linksCrossBoundary(engine) {
		// Only a linking provider can consume a cross-boundary reference; a
		// non-linking consumer keeps its scalar value-pipe, so nothing is stamped.
		return nil
	}

	var parameters map[string]any
	if input.ResolveResult != nil {
		parameters = input.ResolveResult.Parameters
	}

	srcKey, err := onDemandDispatchKey(releaseNameOf(input), nodeID, providerName, recipeName, node.BoundaryPath,
		boundaryInfraFor(input, nodeID), parameters, boundaryDirEvals, perBoundaryAnnotations)
	if err != nil {
		return err
	}
	// A shared provider reports one linking claim for both ends; the consumer's
	// claim (true here, since the guard above passed) stands in for the target's.
	const consumerLinks = true

	stamped := false
	for _, edge := range graph.Spec.Edges {
		if edge.Source != nodeID {
			continue
		}
		tgtNode, ok := graph.Spec.Nodes.Get(edge.Target)
		if !ok {
			continue
		}
		tgtRecipe := nodeRecipes[edge.Target]
		if tgtRecipe == nil {
			continue
		}
		tgtProvider := ProviderEngineKind(tgtRecipe.Spec.Capability)
		tgtKey, err := onDemandDispatchKey(releaseNameOf(input), edge.Target, tgtProvider, tgtRecipe.Metadata.Name, tgtNode.BoundaryPath,
			boundaryInfraFor(input, edge.Target), parameters, boundaryDirEvals, perBoundaryAnnotations)
		if err != nil {
			return err
		}
		if classifyCrossBoundaryLink(srcKey, tgtKey, consumerLinks, consumerLinks) != crossBoundaryLinkFires {
			continue
		}
		if stampEdgeSentinels(&tmplCtx.Context.Inject, edge) {
			stamped = true
		}
	}
	if stamped {
		tmplCtx.Context.Inject.All = buildInjectAll(tmplCtx.Context.Inject)
	}
	return nil
}

// onDemandDispatchKey derives a node's dispatch key by evaluating the pure path
// template the per-node loop evaluates, so a target's key can be computed before
// the target's own loop iteration reaches it. It shares [computeFinalPath] with
// the loop, so the key it returns is byte-identical to the one the loop records.
func onDemandDispatchKey(
	release, nodeID, providerName, recipeName, boundaryPath string,
	bi *resolver.BoundaryInfra,
	parameters map[string]any,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (dispatchKey, error) {
	finalPath, err := computeFinalPath(release, nodeID, boundaryPath, providerName, recipeName, bi, parameters, boundaryDirEvals, perBoundaryAnnotations)
	if err != nil {
		return dispatchKey{}, err
	}
	return dispatchKey{Path: finalPath, Provider: providerName}, nil
}

// boundaryInfraFor returns the resolver-side per-component BoundaryInfra for a
// node, or nil when the resolve carries none (path evaluation then uses its
// documented defaults).
func boundaryInfraFor(input RendererInput, nodeID string) *resolver.BoundaryInfra {
	if input.ResolveResult == nil {
		return nil
	}
	if bi, ok := input.ResolveResult.BoundaryInfra[nodeID]; ok {
		return bi
	}
	return nil
}

// stampEdgeSentinels overwrites the delivered value of each of an edge's
// injected keys with the deterministic placeholder for that (edge, output-key)
// pair, across the env, file, and arg mechanisms the uses-edge inject path
// delivers. Only a key already present in the pipe is overwritten — the edge's
// delivered value is replaced, never a new key introduced — so a value the
// producer's provides did not carry stays absent. It reports whether it changed
// any value.
func stampEdgeSentinels(inject *types.InjectPipe, edge types.IREdge) bool {
	stamped := false
	for _, mech := range []string{"env", "file", "arg"} {
		keymap, ok := edge.Inject.Get(mech)
		if !ok {
			continue
		}
		dest := injectMechanism(inject, mech)
		if dest == nil {
			continue
		}
		for localKey, providesKey := range keymap.All() {
			if _, delivered := dest.Get(localKey); !delivered {
				continue
			}
			dest.Set(localKey, rendercore.CrossBoundaryLinkSentinel(edge.Source, edge.Target, edge.Socket, providesKey))
			stamped = true
		}
	}
	return stamped
}

// injectMechanism returns the addressable delivery map for one inject mechanism,
// or nil for a mechanism the uses-edge inject path does not deliver.
func injectMechanism(inject *types.InjectPipe, mechanism string) *types.OrderedMap[string] {
	switch mechanism {
	case "env":
		return &inject.Env
	case "file":
		return &inject.File
	case "arg":
		return &inject.Arg
	default:
		return nil
	}
}
