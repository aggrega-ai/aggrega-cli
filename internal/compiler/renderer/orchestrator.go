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

		// Phase B preamble: evaluate Recipe .locals (self-contained; sees values/infra/provides).
		resolvedLocals, err := EvaluateRecipeLocals(
			recipe, nodeID, tmplCtx.Values, tmplCtx.Infra, allProvides[nodeID],
		)
		if err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}
		tmplCtx.Locals = resolvedLocals

		// Evaluate Recipe.Source as a template (strings or structured) so authors
		// can parameterise chart version / path / registry per boundary. Sees the
		// same self-contained context as locals (values/infra/provides only).
		// Copy the Recipe locally — never mutate the catalog's version.
		if recipe != nil && recipe.Spec.Source != nil {
			resolvedSource, err := EvaluateRecipeSource(
				recipe.Spec.Source, nodeID, tmplCtx.Values, tmplCtx.Infra, allProvides[nodeID],
			)
			if err != nil {
				return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
			}
			localRecipe := *recipe
			localRecipe.Spec.Source = resolvedSource
			recipe = &localRecipe
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

		// Per-node engine render
		nodeCtx := buildNodeRenderContext(nodeID, node, graph, input)
		if recipe != nil {
			nodeCtx.Source = recipe.Spec.Source
		}
		files, err := engine.Render(ctx, values, nodeCtx)
		if err != nil {
			return nil, fmt.Errorf("Step 5 node %q render: %w", nodeID, err)
		}

		// Group per boundary
		bp := node.BoundaryPath
		if _, ok := boundaries[bp]; !ok {
			boundaries[bp] = &pendingBoundary{files: map[string]map[string][]byte{}}
			orderedBoundaries = append(orderedBoundaries, bp)
		}
		nodeFileMap := make(map[string][]byte, len(files))
		for p, c := range files {
			nodeFileMap[p] = c
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

		// Phase B.5: FeatureRecipe Resource Generation.
		// For each attached FR that carries source or valuesMapping (resource-generating,
		// i.e. not inject-only), in Onion Model order: evaluate FR locals as templates,
		// build feature-scoped context, evaluate FR valuesMapping, dispatch to the engine
		// named by the FR's own provider, and append output files under features/<instance>/.
		for _, ref := range featureRefs {
			fr := ref.FeatureRecipe
			if !isResourceGeneratingFR(fr) {
				continue
			}

			feat, hasFeat := tmplCtx.Features.Get(ref.InstanceName)
			frCtx := *tmplCtx // shallow copy; we'll overwrite Values/Infra/Locals
			if hasFeat {
				frCtx.Values = feat.Values
				frCtx.Infra = feat.Infra
			}

			frEvaluatedLocals, err := EvaluateFeatureRecipeLocals(
				fr, ref.InstanceName, frCtx.Values, frCtx.Infra, allProvides[nodeID],
			)
			if err != nil {
				return nil, fmt.Errorf("Step 5 node %q feature %q: %w", nodeID, ref.InstanceName, err)
			}
			frCtx.Locals = mergeFeatureLocals(tmplCtx.Locals, frEvaluatedLocals)

			frValues, err := ExecutePhaseBForFeature(&frCtx, fr)
			if err != nil {
				return nil, fmt.Errorf("Step 5 node %q feature %q: %w", nodeID, ref.InstanceName, err)
			}

			frEngine, err := engineFactory(fr.Spec.Provider)
			if err != nil {
				return nil, fmt.Errorf("Step 5 node %q feature %q engine factory: %w", nodeID, ref.InstanceName, err)
			}

			frNodeCtx := nodeCtx
			if src, ok := fr.Spec.Source.(string); ok && src != "" {
				frNodeCtx.Source = src
			} else if fr.Spec.Source != nil {
				frNodeCtx.Source = fr.Spec.Source
			}

			frFiles, err := frEngine.Render(ctx, frValues, frNodeCtx)
			if err != nil {
				return nil, fmt.Errorf("Step 5 node %q feature %q render: %w", nodeID, ref.InstanceName, err)
			}

			prefix := "features/" + ref.InstanceName + "/"
			for p, c := range frFiles {
				boundaries[bp].files[nodeID][prefix+p] = c
			}
		}

		// Phase B.6: Credential FeatureRecipe rendering.
		// Secrets live in graph.Spec.Secrets[nodeID] (IR partitions credential features
		// here via guardCredentialPartition). For each secret, resolve the FR, build a
		// feature-scoped context with .secret_name + top-level .inject populated, then
		// render via the FR's own provider engine. Output files go under secrets/<name>/.
		if secretsForNode, ok := graph.Spec.Secrets.Get(nodeID); ok && secretsForNode.Len() > 0 && recipeLookup.FeatureRecipe != nil {
			secretNames := secretsForNode.Keys()
			sort.Strings(secretNames)
			for _, secretName := range secretNames {
				entry, _ := secretsForNode.Get(secretName)
				fr, found := recipeLookup.FeatureRecipe(entry.Type)
				if !found {
					continue
				}

				frCtx := *tmplCtx // shallow copy
				frCtx.Values = entry.Values
				frCtx.Infra = types.OrderedMap[interface{}]{}

				frEvaluatedLocals, err := EvaluateFeatureRecipeLocals(
					fr, secretName, frCtx.Values, frCtx.Infra, allProvides[nodeID],
				)
				if err != nil {
					return nil, fmt.Errorf("Step 5 node %q secret %q: %w", nodeID, secretName, err)
				}
				frCtx.Locals = mergeFeatureLocals(tmplCtx.Locals, frEvaluatedLocals)
				frCtx.SecretName = secretName
				frCtx.Inject = buildSecretInject(entry)

				frValues, err := ExecutePhaseBForFeature(&frCtx, fr)
				if err != nil {
					return nil, fmt.Errorf("Step 5 node %q secret %q: %w", nodeID, secretName, err)
				}

				frEngine, err := engineFactory(fr.Spec.Provider)
				if err != nil {
					return nil, fmt.Errorf("Step 5 node %q secret %q engine factory: %w", nodeID, secretName, err)
				}

				frNodeCtx := nodeCtx
				if src, ok := fr.Spec.Source.(string); ok && src != "" {
					frNodeCtx.Source = src
				} else if fr.Spec.Source != nil {
					frNodeCtx.Source = fr.Spec.Source
				}

				frFiles, err := frEngine.Render(ctx, frValues, frNodeCtx)
				if err != nil {
					return nil, fmt.Errorf("Step 5 node %q secret %q render: %w", nodeID, secretName, err)
				}

				prefix := "secrets/" + secretName + "/"
				for p, c := range frFiles {
					boundaries[bp].files[nodeID][prefix+p] = c
				}
			}
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
	_ = json.Unmarshal(raw, &out)
	return out
}

func tmplCtxInfra(graph *types.IRGraph, nodeID string) types.OrderedMap[interface{}] {
	out := types.OrderedMap[interface{}]{}
	raw, ok := graph.Spec.InfraMapping.Get(nodeID)
	if !ok {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}
