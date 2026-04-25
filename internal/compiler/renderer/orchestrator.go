// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
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
func RenderAll(ctx context.Context, input RendererInput, recipeLookup RecipeLookup, engineFactory EngineFactory) (*RenderResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	graph := input.Graph
	result := &RenderResult{
		Files: make(map[string][]byte),
	}

	// Resolve recipes per node
	nodeRecipes := make(map[string]*types.Recipe)
	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
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

	// Track per-boundary sub-chart info for umbrella composition
	type pendingBoundary struct {
		files     map[string]map[string][]byte // nodeID → per-node file map
		subCharts []SubChartInfo
		composer  BoundaryComposer // first composer engine seen in this boundary (if any)
	}
	boundaries := map[string]*pendingBoundary{}
	orderedBoundaries := []string{}

	// Per-node rendering
	for _, nodeID := range nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
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

		// Phase B sub-pass pipeline per chapter 16 §5. State carries cross-
		// sub-pass accumulators (resolved values, parsed workload, emitted
		// files). Recipe path is empty here today — the catalog loader does
		// not retain the on-disk path on the parsed Recipe; validator errors
		// surfacing through the sub-passes show "" for the path. Future
		// improvement: thread the path through the catalog resolver.
		state := NewPhaseBState(recipe, nodeID, node.BoundaryPath, "")

		// Phase B.0 — locals + source-as-template eval; populates
		// ctx.Locals + ctx.Source. May replace state.Recipe with a local
		// copy whose Spec.Source carries the resolved value.
		if err := phaseB0(tmplCtx, state, allProvides[nodeID]); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}
		recipe = state.Recipe // pick up the local copy with resolved source

		// Phase A: injectMapping from FeatureRecipes in Onion Model order.
		// Sits between B.0 and B.1 — chapter 16 sub-pass numbering scopes
		// to Phase B; Phase A is a separate phase and stays at the
		// orchestrator level.
		featureRefs := resolveFeatureRecipes(nodeID, input.FeatureOrder, graph, recipeLookup)
		if err := ExecutePhaseA(tmplCtx, featureRefs); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B.1 — valuesMapping → state.ResolvedValues.
		if err := phaseB1(tmplCtx, state); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}
		values := state.ResolvedValues

		// Ghost Infrastructure: compare valuesMapping template references against
		// the keys actually in .values / .infra — unused keys emit a warning so
		// catalog authors notice when a Release infra knob is never consumed.
		// Skip file-based templates (too large; defer) and nil recipes.
		if recipe != nil && recipe.Spec.ValuesMapping != "" {
			if refs, parseErr := CollectTemplateFieldAccesses(recipe.Spec.ValuesMapping); parseErr == nil {
				for _, k := range UnreadKeys(refs, ".values", tmplCtx.Values) {
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("node %q: unused .values key %q (not referenced by valuesMapping of recipe %q)",
							nodeID, k, recipe.Metadata.Name))
				}
				for _, k := range UnreadKeys(refs, ".infra", tmplCtx.Infra) {
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("node %q: unused .infra key %q (not referenced by valuesMapping of recipe %q)",
							nodeID, k, recipe.Metadata.Name))
				}
			}
		}

		// Sourceless baseSchema validation: if the Recipe has no source and a ProviderLookup
		// is available, validate the valuesMapping output against the provider's baseSchema.
		if recipe != nil && input.ProviderLookup != nil && isSourceless(recipe) {
			if err := ValidateValuesAgainstBaseSchema(
				recipe.Spec.Provider,
				input.ProviderLookup,
				values,
				fmt.Sprintf("node %q recipe %q", nodeID, recipe.Metadata.Name),
			); err != nil {
				return nil, fmt.Errorf("Step 5 node %q baseSchema: %w", nodeID, err)
			}
		}

		// Phase C: validate pipeline (no execution today)
		if recipe != nil && len(recipe.Spec.Pipeline) > 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("node %q: Phase C pipeline (%d steps) validated but not executed", nodeID, len(recipe.Spec.Pipeline)),
			)
		}

		// Resolve engine for this node's provider via factory
		providerName := ""
		if recipe != nil {
			providerName = recipe.Spec.Provider
		}
		engine, err := engineFactory(providerName)
		if err != nil {
			return nil, fmt.Errorf("Step 5 node %q engine factory: %w", nodeID, err)
		}

		// Per-node engine render — sourced/sourceless rendering. Output
		// files are tagged EmitOriginSource and accumulated on the state
		// for B.5's collision detector.
		nodeCtx := buildNodeRenderContext(nodeID, node, graph, input)
		if recipe != nil {
			nodeCtx.Source = recipe.Spec.Source
		}
		files, err := engine.Render(ctx, values, nodeCtx)
		if err != nil {
			return nil, fmt.Errorf("Step 5 node %q render: %w", nodeID, err)
		}
		// Tag with EmitOriginSource and accumulate (deterministic per LDR
		// 0026: iterate the engine.Render output by sorted filename so
		// state.EmittedFiles order is stable across runs).
		sourceNames := make([]string, 0, len(files))
		for p := range files {
			sourceNames = append(sourceNames, p)
		}
		sort.Strings(sourceNames)
		for _, p := range sourceNames {
			state.EmittedFiles = append(state.EmittedFiles, EmittedFile{
				Name:   p,
				Origin: EmitOriginSource,
				Body:   files[p],
			})
		}

		// Phase B.2 — workload eval + ValidateWorkload + ValidateWorkloadSecrets.
		// No-op for Recipes that don't declare workload: / workloadFrom:.
		// Slots lookup deferred to inside the function body (passes empty
		// when no workload is declared); future enhancement: thread the
		// Component's secretsSchema from input.CatalogLookup when Workload
		// recipes ship in WI05.
		emptySlots := types.OrderedMap[types.SecretSlot]{}
		if err := phaseB2(tmplCtx, state, emptySlots); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B.3 — artifacts STUB. Errors loud if Recipe declares
		// artifacts: / artifactsFrom:; no-op otherwise.
		if err := phaseB3(state); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B.4 — workload synthesizer dispatch. No-op for sourced/
		// sourceless paths (state.ParsedWorkload is nil); active when WI05
		// helm Workload synthesizer + Workload-bearing Recipes ship.
		if err := phaseB4(ctx, tmplCtx, state, engine, nodeCtx); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B.5 — filename collision detection across input-kind
		// origins (rule 7). With current fixtures only EmitOriginSource
		// contributes; collisions surface when WI05 lands Workload
		// synthesis or when artifacts evaluation lands.
		if err := phaseB5(state); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Group per boundary. Per-node file map is built from
		// state.EmittedFiles (carries every input-kind contribution).
		bp := node.BoundaryPath
		if _, ok := boundaries[bp]; !ok {
			boundaries[bp] = &pendingBoundary{files: map[string]map[string][]byte{}}
			orderedBoundaries = append(orderedBoundaries, bp)
		}
		nodeFileMap := make(map[string][]byte, len(state.EmittedFiles))
		for _, f := range state.EmittedFiles {
			nodeFileMap[f.Name] = f.Body
		}
		boundaries[bp].files[nodeID] = nodeFileMap
		boundaries[bp].subCharts = append(boundaries[bp].subCharts, SubChartInfo{
			InstanceID: nodeID,
			Version:    node.ResolvedVersion,
			Values:     values,
			Source:     nodeCtx.Source,
		})
		if boundaries[bp].composer == nil {
			if c, ok := engine.(BoundaryComposer); ok {
				boundaries[bp].composer = c
			}
		}

		// Phase B.6 — FeatureRecipe Resource Generation (both non-credential
		// and credential FRs). Returns prefixed paths
		// ("features/<instance>/<file>", "secrets/<name>/<file>") merged
		// into the per-node files map alongside source/workload outputs.
		secretsForNode, _ := graph.Spec.Secrets.Get(nodeID)
		frFiles, err := phaseB6(ctx, tmplCtx, state, featureRefs, engineFactory, allProvides, secretsForNode, recipeLookup, nodeCtx)
		if err != nil {
			return nil, err
		}
		for path, content := range frFiles {
			boundaries[bp].files[nodeID][path] = content
		}
	}

	// Finalize per-boundary output
	sort.Strings(orderedBoundaries)
	for _, bp := range orderedBoundaries {
		pb := boundaries[bp]
		boundaryPrefix := ""
		if bp != "" {
			boundaryPrefix = "boundaries/" + bp + "/"
		}

		// If engine is a BoundaryComposer (helm-chart) AND the boundary is non-empty:
		// nest per-node under charts/<nodeID>/ and emit boundary-level umbrella files.
		if pb.composer != nil && bp != "" {
			info := BoundaryInfo{
				BoundaryPath:  bp,
				AggregateName: graph.Metadata.AggregateName,
				SubCharts:     pb.subCharts,
			}
			boundaryFiles, nestPerNode, err := pb.composer.ComposeBoundary(ctx, info)
			if err != nil {
				return nil, fmt.Errorf("Step 5 boundary %q compose: %w", bp, err)
			}
			for p, c := range boundaryFiles {
				result.Files[boundaryPrefix+p] = c
			}
			// Node files go under charts/<nodeID>/ (or flat if composer says no nesting)
			for nodeID, nodeFiles := range pb.files {
				nodePrefix := boundaryPrefix
				if nestPerNode {
					nodePrefix += "charts/" + nodeID + "/"
				}
				for p, c := range nodeFiles {
					result.Files[nodePrefix+p] = c
				}
			}
		} else {
			// Flat: per-node files directly under boundary
			for _, nodeFiles := range pb.files {
				for p, c := range nodeFiles {
					result.Files[boundaryPrefix+p] = c
				}
			}
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

	fvm, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
	if !ok {
		return nil
	}

	var refs []FeatureRecipeRef
	for _, instanceName := range order {
		entry, ok := fvm.Get(instanceName)
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
		Labels:          flattenOrderedStringMap(node.Labels),
		Annotations:     flattenOrderedStringMap(node.ReleaseAnnotations),
		BoundaryPath:    node.BoundaryPath,
		SyncWave:        node.SyncWave,
		ResolvedVersion: node.ResolvedVersion,
		Component:       node.Component,
		Provenance: ProvenanceContext{
			CompilerVersion:  graph.Spec.Provenance.CompilerVersion,
			AggregateVersion: graph.Spec.Provenance.AggregateVersion,
		},
	}
	if graph.Metadata.AggregateName != "" {
		nrc.Provenance.AggregateName = graph.Metadata.AggregateName
	}
	if input.ResolveResult != nil && input.ResolveResult.Release != nil {
		nrc.Provenance.ReleaseName = input.ResolveResult.Release.Metadata.Name
		nrc.Provenance.Environment = input.ResolveResult.Release.Metadata.Environment
	}
	return nrc
}

func tmplCtxValues(graph *types.IRGraph, nodeID string) types.OrderedMap[interface{}] {
	out := types.OrderedMap[interface{}]{}
	raw, ok := graph.Spec.ValuesMapping.Get(nodeID)
	if !ok {
		return out
	}
	_ = parse.IRPayload(raw, &out)
	return out
}

func tmplCtxInfra(graph *types.IRGraph, nodeID string) types.OrderedMap[interface{}] {
	out := types.OrderedMap[interface{}]{}
	raw, ok := graph.Spec.InfraMapping.Get(nodeID)
	if !ok {
		return out
	}
	_ = parse.IRPayload(raw, &out)
	return out
}
