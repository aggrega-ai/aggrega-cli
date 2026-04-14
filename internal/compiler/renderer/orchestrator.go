// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// RenderResult is the output of the full Step 5 rendering pipeline.
type RenderResult struct {
	Files    map[string][]byte // boundary-relative path → content
	Warnings []string
}

// RecipeLookup resolves a component type to its Recipe and FeatureRecipes.
type RecipeLookup struct {
	Recipe        func(componentType string) (*types.Recipe, bool)
	FeatureRecipe func(featureType string) (*types.FeatureRecipe, bool)
}

// RenderAll executes the full Step 5 pipeline:
//  1. Pass 1: Evaluate all provides (any order, self-contained)
//  2. For each node (sorted):
//     a. Pass 2: Pre-hydrate inject from provides
//     b. Build template context
//     c. Phase A: Execute injectMapping (Onion Model order)
//     d. Phase B: Execute valuesMapping → values dictionary
//     e. Helm render → K8s manifests
//  3. Collect all files
func RenderAll(ctx context.Context, input RendererInput, recipeLookup RecipeLookup, engine Engine) (*RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	graph := input.Graph
	result := &RenderResult{
		Files: make(map[string][]byte),
	}

	// Resolve recipes per node
	nodeRecipes := make(map[string]*types.Recipe)
	nodeIDs := sortedNodeIDs(graph.Spec.Nodes)
	for _, nodeID := range nodeIDs {
		node := graph.Spec.Nodes[nodeID]
		if recipeLookup.Recipe != nil {
			if r, found := recipeLookup.Recipe(node.Component); found {
				nodeRecipes[nodeID] = r
			}
		}
	}

	// Pass 1: Evaluate all provides
	allProvides, err := EvaluateAllProvides(graph, nodeRecipes)
	if err != nil {
		return nil, fmt.Errorf("Step 5 Pass 1: %w", err)
	}

	// Per-node rendering
	for _, nodeID := range nodeIDs {
		node := graph.Spec.Nodes[nodeID]
		recipe := nodeRecipes[nodeID]

		// Pass 2a: Self-inject resolution (component-level inject)
		inject, injectRaw := BuildInjectPipe(node.SelfInject)
		if node.SelfInject != nil {
			siCtx := SelfInjectContext{
				Values: tmplCtxValues(graph, nodeID),
				Infra:  tmplCtxInfra(graph, nodeID),
				Context: SelfInjectContextBlock{
					InstanceID: nodeID,
					Labels:     node.Labels,
					Annotations: &types.AnnotationBuckets{
						Catalog:   node.CatalogAnnotations,
						Release:   node.ReleaseAnnotations,
						Aggregate: node.AggregateAnnotations,
					},
				},
			}
			if _, err := ExecutePass2a(&inject, node.SelfInject, nodeID, allProvides[nodeID], siCtx); err != nil {
				return nil, fmt.Errorf("Step 5 node %q Pass 2a: %w", nodeID, err)
			}
		}

		// Pass 2b: Rehydrate inject from uses-edge provides
		RehydrateInjectFromProvides(&inject, graph.Spec.Edges, nodeID, allProvides)

		// Build template context
		tmplCtx := AssembleNodeContext(nodeID, graph, allProvides, inject, injectRaw)
		if input.ResolveResult != nil && input.ResolveResult.Release != nil {
			AssembleNodeContextWithRelease(tmplCtx, input.ResolveResult.Release)
		}

		// Phase A: injectMapping from FeatureRecipes in Onion Model order
		featureRefs := resolveFeatureRecipes(nodeID, input.FeatureOrder, graph, recipeLookup)
		if err := ExecutePhaseA(tmplCtx, featureRefs); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B: valuesMapping → values dictionary
		values, err := ExecutePhaseB(tmplCtx, recipe)
		if err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase C: validate pipeline (no execution today)
		if recipe != nil && len(recipe.Spec.Pipeline) > 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("node %q: Phase C pipeline (%d steps) validated but not executed", nodeID, len(recipe.Spec.Pipeline)),
			)
		}

		// Helm render
		nodeCtx := buildNodeRenderContext(nodeID, node, graph, input)
		files, err := engine.Render(ctx, values, nodeCtx)
		if err != nil {
			return nil, fmt.Errorf("Step 5 node %q render: %w", nodeID, err)
		}

		// Collect files with boundary prefix
		boundaryPrefix := ""
		if node.BoundaryPath != "" {
			boundaryPrefix = "boundaries/" + node.BoundaryPath + "/"
		}
		for path, content := range files {
			result.Files[boundaryPrefix+path] = content
		}
	}

	return result, nil
}

func resolveFeatureRecipes(
	nodeID string,
	featureOrder map[string][]string,
	graph *types.IRGraph,
	recipeLookup RecipeLookup,
) []FeatureRecipeRef {
	order, ok := featureOrder[nodeID]
	if !ok || recipeLookup.FeatureRecipe == nil {
		return nil
	}

	fvm := graph.Spec.FeatureValuesMapping[nodeID]
	if fvm == nil {
		return nil
	}

	var refs []FeatureRecipeRef
	for _, instanceName := range order {
		entry, ok := fvm[instanceName]
		if !ok {
			continue
		}
		fr, found := recipeLookup.FeatureRecipe(entry.Type)
		if !found {
			continue
		}
		refs = append(refs, FeatureRecipeRef{
			InstanceName:  instanceName,
			FeatureRecipe: fr,
		})
	}
	return refs
}

func buildNodeRenderContext(
	nodeID string,
	node types.IRNode,
	graph *types.IRGraph,
	input RendererInput,
) NodeRenderContext {
	nrc := NodeRenderContext{
		NodeID:          nodeID,
		Labels:          node.Labels,
		Annotations:     node.ReleaseAnnotations,
		BoundaryPath:    node.BoundaryPath,
		SyncWave:        node.SyncWave,
		ResolvedVersion: node.ResolvedVersion,
		Component:       node.Component,
		Provenance: ProvenanceContext{
			CompilerVersion:  graph.Spec.Provenance.CompilerVersion,
			AggregateVersion: graph.Spec.Provenance.AggregateVersion,
		},
	}
	if graph.Metadata.AppName != "" {
		nrc.Provenance.AggregateName = graph.Metadata.AppName
	}
	if input.ResolveResult != nil && input.ResolveResult.Release != nil {
		nrc.Provenance.ReleaseName = input.ResolveResult.Release.Metadata.Name
		nrc.Provenance.Environment = input.ResolveResult.Release.Metadata.Environment
	}
	return nrc
}

func sortedNodeIDs(nodes map[string]types.IRNode) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func tmplCtxValues(graph *types.IRGraph, nodeID string) map[string]interface{} {
	raw, ok := graph.Spec.ValuesMapping[nodeID]
	if !ok {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

func tmplCtxInfra(graph *types.IRGraph, nodeID string) map[string]interface{} {
	raw, ok := graph.Spec.InfraMapping[nodeID]
	if !ok {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}
