// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// RenderResult is the output of the full Step 5 rendering pipeline.
type RenderResult struct {
	Files    map[string][]byte // boundary-relative path → content
	Warnings []string
}

// dispatchKey identifies a per-(evaluated_path, provider) dispatch group.
// Two boundaries with the same evaluated path and provider produce ONE
// dispatch group receiving contributors from both; mixed-provider boundaries
// produce one dispatch group per provider at the boundary's evaluated path.
//
// Replaces the previous boundary-keyed dispatch model. Per-(path, provider)
// dispatch grouping enables operator-controlled cross-boundary aggregation
// via shared `directory:` declarations and naturally accommodates mixed-
// provider boundaries.
type dispatchKey struct {
	Path     string // evaluated final_path (post-template-evaluation)
	Provider string // resolved per-component recipe.Spec.Provider
}

// pendingDispatch holds per-(path, provider) state across the per-node loop
// and the per-(path, provider) BoundaryAssembler assembly pass.
//
// intermediates carries Intermediate=true EmittedFiles separately from files
// so the orchestrator never writes them at the dispatch path directly; they
// flow only to the BoundaryAssembler at finalization. nodeAnnotations and
// nodeComponent are the per-node metadata the assembler needs to assemble
// its input (annotations for per-boundary network derivation, component for
// per-node info). engineByNode lets the assembly pass map node-IDs to their
// engine instances for any provider-specific lookups.
//
// contributingBoundaries records every boundary path whose components fed
// into this dispatch group — used for diagnostic messages on collision /
// chart-name-mismatch errors and for chart-name cascade lookups against
// the resolver-side BoundaryAnnotations map.
type pendingDispatch struct {
	files                  map[string]map[string][]byte
	intermediates          map[string][]EmittedFile
	nodeAnnotations        map[string]map[string]string
	nodeComponent          map[string]string
	subCharts              []SubChartInfo
	assembler              BoundaryAssembler
	engineByNode           map[string]Engine
	contributingBoundaries []string
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

	dispatch := map[dispatchKey]*pendingDispatch{}
	orderedDispatch := []dispatchKey{}

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

		// Sourceless baseSchema validation: if the Recipe has no source AND no
		// workload AND a ProviderLookup is available, validate the
		// valuesMapping output against the provider's baseSchema. The baseSchema
		// check is the legacy validation against the values-dict-driven
		// sourceless path; it doesn't apply to the workload-driven path
		// (where the rendering input lives inside the parsed Workload, not in
		// the values dict). Phase B.2 + ValidateWorkload + ValidateWorkloadSecrets
		// cover the workload-mode validation surface.
		hasWorkload := recipe != nil && (recipe.Spec.Workload != "" || recipe.Spec.WorkloadFrom != "")
		if recipe != nil && input.ProviderLookup != nil && isSourceless(recipe) && !hasWorkload {
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
		//
		// Skipped when the Recipe declares workload: / workloadFrom: but no
		// source: (workload-only path). The Workload synthesizer at Phase
		// B.4 is the source of truth for that path; engine.Render's
		// sourceless mode would produce a duplicate Deployment that rule
		// 7 (filename collision) correctly rejects. Pure sourceless (no
		// source, no workload) and sourced (source declared) paths still
		// run engine.Render normally; both coexist with workload synthesis
		// when a Recipe declares both source and workload.
		nodeCtx := buildNodeRenderContext(nodeID, node, graph, input)
		if recipe != nil {
			nodeCtx.Source = recipe.Spec.Source
		}
		sourceEmpty := nodeCtx.Source == nil
		if s, ok := nodeCtx.Source.(string); ok && s == "" {
			sourceEmpty = true
		}
		runEngineRender := !(hasWorkload && sourceEmpty)
		var files map[string][]byte
		if runEngineRender {
			var err error
			files, err = engine.Render(ctx, values, nodeCtx)
			if err != nil {
				return nil, fmt.Errorf("Step 5 node %q render: %w", nodeID, err)
			}
		}
		// Tag with EmitOriginSource and accumulate (deterministic across
		// runs of identical input by iterating the engine.Render output
		// by sorted filename; state.EmittedFiles order stays stable).
		//
		// Intermediate marking. When the engine implements BoundaryAssembler
		// (compose, helm-chart), Render's output is intermediate input to the
		// per-(path, provider) BoundaryAssembler at the contributor's
		// evaluated path and must NOT be written to disk directly alongside
		// per-node files. The orchestrator marks every Render output as
		// Intermediate=true for BoundaryAssembler engines; the assembler
		// reads the fragments and emits the canonical per-dispatch-path
		// file. Engines that are not BoundaryAssemblers keep
		// Intermediate=false — their Render outputs ship as-is.
		_, isAssembler := engine.(BoundaryAssembler)
		sourceNames := make([]string, 0, len(files))
		for p := range files {
			sourceNames = append(sourceNames, p)
		}
		sort.Strings(sourceNames)
		for _, p := range sourceNames {
			state.EmittedFiles = append(state.EmittedFiles, EmittedFile{
				Name:         p,
				Origin:       EmitOriginSource,
				Body:         files[p],
				Intermediate: isAssembler,
			})
		}

		// Phase B.2 — workload eval + ValidateWorkload + ValidateWorkloadSecrets.
		// No-op for Recipes that don't declare workload: / workloadFrom:.
		// Slots lookup deferred to inside the function body (passes empty
		// when no workload is declared); future enhancement: thread the
		// Component's secretsSchema from input.CatalogLookup when Workload
		// recipes ship.
		emptySlots := types.OrderedMap[types.SecretSlot]{}
		if err := phaseB2(tmplCtx, state, emptySlots); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B.3 — artifacts evaluation. Inline artifacts: and
		// artifactsFrom: render through Go templates against the full
		// TemplateContext, get provenance labels merged into K8s metadata
		// when present, and accumulate on state.EmittedFiles for Phase
		// B.5 collision detection.
		if err := phaseB3(ctx, tmplCtx, state, engine, nodeCtx); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B.4 — workload synthesizer dispatch. No-op for sourced/
		// sourceless paths (state.ParsedWorkload is nil); active when
		// helm Workload synthesizer + Workload-bearing Recipes ship.
		if err := phaseB4(ctx, tmplCtx, state, engine, nodeCtx); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Phase B.5 — filename collision detection across input-kind
		// origins (rule 7). With current fixtures only EmitOriginSource
		// contributes; collisions surface when code lands Workload
		// synthesis or when artifacts evaluation lands.
		if err := phaseB5(state); err != nil {
			return nil, fmt.Errorf("Step 5 node %q: %w", nodeID, err)
		}

		// Group per (evaluated_path, provider). Per-node file map is built
		// from state.EmittedFiles (carries every input-kind contribution).
		// Intermediate=true entries are split off into intermediates so
		// the orchestrator never writes them at the dispatch path directly;
		// they flow only to the BoundaryAssembler at finalization.
		//
		// Path-template evaluation runs per-component at orchestrator-time
		// using BoundaryInfra.{Directory, SubDirectory} (raw template
		// strings populated by the resolver) plus the live parameter map
		// from ResolveResult.Parameters. The evaluated final_path becomes
		// the dispatch Path; the per-(path, provider) tuple is the dispatch
		// key. Two boundaries with same evaluated path + same provider
		// merge into one dispatch group.
		bp := node.BoundaryPath
		recipeName := ""
		if recipe != nil {
			recipeName = recipe.Metadata.Name
		}
		var bi *struct {
			Directory    string
			SubDirectory string
		}
		if input.ResolveResult != nil {
			if rbi, ok := input.ResolveResult.BoundaryInfra[nodeID]; ok && rbi != nil {
				bi = &struct {
					Directory    string
					SubDirectory string
				}{Directory: rbi.Directory, SubDirectory: rbi.SubDirectory}
			}
		}
		var parameters map[string]interface{}
		if input.ResolveResult != nil {
			parameters = input.ResolveResult.Parameters
		}
		finalPath, err := computeFinalPath(nodeID, bp, providerName, recipeName, bi, parameters)
		if err != nil {
			return nil, fmt.Errorf("Step 5 path eval node %q: %w", nodeID, err)
		}
		key := dispatchKey{Path: finalPath, Provider: providerName}
		if _, ok := dispatch[key]; !ok {
			dispatch[key] = &pendingDispatch{
				files:           map[string]map[string][]byte{},
				intermediates:   map[string][]EmittedFile{},
				nodeAnnotations: map[string]map[string]string{},
				nodeComponent:   map[string]string{},
				engineByNode:    map[string]Engine{},
			}
			orderedDispatch = append(orderedDispatch, key)
		}
		// INSTANCE_ID_COLLISION check: when two contributors land in the
		// same dispatch group with the same node-ID, the assembler input
		// would be ambiguous. Defensive guardrail — topology flattening's
		// prefix discipline should make collisions impossible — but the
		// check surfaces structural violations immediately rather than
		// letting them propagate to broken assembled output.
		if _, alreadyHere := dispatch[key].files[nodeID]; alreadyHere {
			return nil, fmt.Errorf("%w: dispatch path %q provider %q already contains node %q (contributing boundaries: %v); component instance IDs must be unique within an assembly group",
				ErrInstanceIDCollision, key.Path, key.Provider, nodeID, dispatch[key].contributingBoundaries)
		}
		// Track contributing boundaries (deduplicated). Used for diagnostic
		// messages on collision/chart-name-mismatch errors and for chart-name
		// cascade lookups against ResolveResult.BoundaryAnnotations.
		alreadyContributing := false
		for _, existing := range dispatch[key].contributingBoundaries {
			if existing == bp {
				alreadyContributing = true
				break
			}
		}
		if !alreadyContributing {
			dispatch[key].contributingBoundaries = append(dispatch[key].contributingBoundaries, bp)
		}
		nodeFileMap := make(map[string][]byte, len(state.EmittedFiles))
		for _, f := range state.EmittedFiles {
			if f.Intermediate {
				dispatch[key].intermediates[nodeID] = append(dispatch[key].intermediates[nodeID], f)
				continue
			}
			nodeFileMap[f.Name] = f.Body
		}
		dispatch[key].files[nodeID] = nodeFileMap
		dispatch[key].nodeAnnotations[nodeID] = nodeCtx.Annotations
		dispatch[key].nodeComponent[nodeID] = node.Component
		dispatch[key].engineByNode[nodeID] = engine
		dispatch[key].subCharts = append(dispatch[key].subCharts, SubChartInfo{
			InstanceID: nodeID,
			Version:    node.ResolvedVersion,
			Values:     values,
			Source:     nodeCtx.Source,
		})
		if dispatch[key].assembler == nil {
			if a, ok := engine.(BoundaryAssembler); ok {
				dispatch[key].assembler = a
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
			dispatch[key].files[nodeID][path] = content
		}

		// Drain Phase B's per-node warnings into the build-level result.
		// Drain position is the end of the per-node Phase B sequence
		// (after phaseB6 — the LAST sub-pass) so any sub-pass appending
		// to state.Warnings reaches result.Warnings, not just B.3's
		// helm-chart provenance-skip notice today.
		result.Warnings = append(result.Warnings, state.Warnings...)
	}

	// Finalize per-(path, provider) output. Sort dispatch keys for
	// deterministic output ordering: by Path, then by Provider.
	sort.Slice(orderedDispatch, func(i, j int) bool {
		if orderedDispatch[i].Path != orderedDispatch[j].Path {
			return orderedDispatch[i].Path < orderedDispatch[j].Path
		}
		return orderedDispatch[i].Provider < orderedDispatch[j].Provider
	})

	// PATH_COLLISION check: scan dispatch keys for the same Path across
	// distinct Provider values. This case arises only when the operator
	// overrode subDirectory: in a way that defeats the per-provider
	// segregation default ({{ .provider }}); under default rules each
	// provider gets its own sub-path so paths never collide across
	// providers naturally. Per CLAUDE.md Loud Guardrails: surface the
	// collision rather than silently overwriting one provider's output
	// with another's.
	pathToProviders := map[string][]dispatchKey{}
	for _, key := range orderedDispatch {
		pathToProviders[key.Path] = append(pathToProviders[key.Path], key)
	}
	for path, keys := range pathToProviders {
		if len(keys) <= 1 {
			continue
		}
		providers := make([]string, 0, len(keys))
		var contributingBoundaries []string
		for _, k := range keys {
			providers = append(providers, k.Provider)
			contributingBoundaries = append(contributingBoundaries, dispatch[k].contributingBoundaries...)
		}
		return nil, fmt.Errorf("%w: dispatch path %q produced multiple provider outputs (%v); contributing boundaries %v resolve to the same path under their current directory: / subDirectory: settings; set distinct subDirectory values per provider or change one boundary's directory",
			ErrPathCollision, path, providers, contributingBoundaries)
	}

	// CHART_NAME_MISMATCH check: when a helm-chart dispatch group has more
	// than one contributing boundary, every contributing boundary's
	// effective aggrega.ai/chart-name annotation (computed via the
	// resolver's per-boundary annotation cascade) must resolve to the
	// same value. Mismatches indicate the operator has merged paths
	// that conceptually belong to different umbrella charts.
	for _, key := range orderedDispatch {
		if key.Provider != "helm-chart" {
			continue
		}
		pd := dispatch[key]
		if len(pd.contributingBoundaries) <= 1 {
			continue
		}
		var firstName string
		var firstBoundary string
		mismatch := false
		mismatchValues := map[string]string{}
		for _, bp := range pd.contributingBoundaries {
			name := ""
			if input.ResolveResult != nil {
				if ann, ok := input.ResolveResult.BoundaryAnnotations[bp]; ok {
					name, _ = ann.Get("aggrega.ai/chart-name")
				}
			}
			mismatchValues[bp] = name
			if firstBoundary == "" {
				firstName = name
				firstBoundary = bp
			} else if name != firstName {
				mismatch = true
			}
		}
		if mismatch {
			return nil, fmt.Errorf("%w: boundaries %v merge to assembly path %q under helm-chart provider but their effective aggrega.ai/chart-name annotations resolve to different values: %v; the merged umbrella chart has exactly one name; cascade or declare matching values explicitly",
				ErrChartNameMismatch, pd.contributingBoundaries, key.Path, mismatchValues)
		}
	}

	for _, key := range orderedDispatch {
		pd := dispatch[key]
		// Disk-write prefix is the evaluated final_path verbatim. Default
		// boundaries/<bp>/<provider>/ shape produced by computeFinalPath
		// when boundaries omit directory: / subDirectory: preserves
		// today's per-boundary, per-provider segregation.
		dispatchPrefix := ""
		if key.Path != "" {
			dispatchPrefix = key.Path + "/"
		}

		// If engine is a BoundaryAssembler (helm-chart, compose) AND the dispatch path is non-empty:
		// nest per-node under charts/<nodeID>/ and emit assembled files at the dispatch path.
		if pd.assembler != nil && key.Path != "" {
			info := BoundaryInfo{
				BoundaryPath:  key.Path,
				AggregateName: graph.Metadata.AggregateName,
				SubCharts:     pd.subCharts,
			}
			boundaryFiles, nestPerNode, err := pd.assembler.AssembleBoundary(ctx, info)
			if err != nil {
				return nil, fmt.Errorf("Step 5 dispatch %q assemble: %w", key.Path, err)
			}
			for p, c := range boundaryFiles {
				result.Files[dispatchPrefix+p] = c
			}
			// Node files go under charts/<nodeID>/ (or flat if assembler says no nesting)
			for nodeID, nodeFiles := range pd.files {
				nodePrefix := dispatchPrefix
				if nestPerNode {
					nodePrefix += "charts/" + nodeID + "/"
				}
				for p, c := range nodeFiles {
					result.Files[nodePrefix+p] = c
				}
			}
		} else {
			// Flat: per-node files directly under the dispatch path
			for _, nodeFiles := range pd.files {
				for p, c := range nodeFiles {
					result.Files[dispatchPrefix+p] = c
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

// computeFinalPath evaluates the per-component path templates against the
// path-template variable allowlist and returns the dispatch path used for
// per-(path, provider) grouping.
//
// Defaults:
//   - bi.Directory empty → "boundaries/<bp>" (today's per-boundary segregation).
//   - bi.SubDirectory empty → render `{{ .provider }}` against the per-component
//     provider name.
//
// Available template variables: .provider, .boundary_name, .parameters.<key>,
// .component_id (subDirectory: only), .manifest.recipe_name. .parent_boundary.*
// is intentionally not populated in this revision; references will trigger
// missingkey=error per the path-template strictness policy.
func computeFinalPath(
	nodeID, bp, providerName, recipeName string,
	bi *struct {
		Directory    string
		SubDirectory string
	},
	parameters map[string]interface{},
) (string, error) {
	data := map[string]interface{}{
		"provider":      providerName,
		"boundary_name": bp,
		"parameters":    parameters,
		"component_id":  nodeID,
		"manifest":      map[string]interface{}{"recipe_name": recipeName},
	}

	// Evaluate directory:
	var evalDir string
	if bi == nil || bi.Directory == "" {
		// Default: today's per-boundary segregation. No template eval needed.
		evalDir = "boundaries/" + bp
	} else {
		var err error
		evalDir, err = EvalPathTemplate("directory:"+bp, "directory", bp, bi.Directory, data)
		if err != nil {
			return "", err
		}
	}

	// Evaluate subDirectory:
	subTmpl := ""
	if bi != nil {
		subTmpl = bi.SubDirectory
	}
	if subTmpl == "" {
		subTmpl = "{{ .provider }}"
	}
	evalSub, err := EvalPathTemplate("subDirectory:"+bp+":"+nodeID, "subDirectory", bp, subTmpl, data)
	if err != nil {
		return "", err
	}

	if evalSub == "" || evalSub == "." {
		return evalDir, nil
	}
	return filepath.Join(evalDir, evalSub), nil
}
