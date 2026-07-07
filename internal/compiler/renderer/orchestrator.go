// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
	assemblerCandidates    []assemblerCandidate
	engineByNode           map[string]Engine
	contributingBoundaries []string
}

// assemblerCandidate is one node's contribution to its dispatch group's
// boundary-assembler election: the engine that rendered the node and the
// resolved provider version behind it. The group's assembler is the engine of
// the highest-version candidate (see selectBoundaryAssembler); candidates are
// appended in node-processing order, so iterating them in order keeps the
// earliest node's engine on a version tie.
type assemblerCandidate struct {
	engine  Engine
	version string
}

// RecipeLookup resolves a component/feature instance to its Recipe and
// FeatureRecipes. Both closures take the instance NAME plus the catalog TYPE so
// the Manifest name tier (a per-instance recipe override) can take precedence
// over the type mapping; an empty instance name resolves by type alone.
type RecipeLookup struct {
	Recipe        func(instanceName, componentType string) (*types.Recipe, bool)
	FeatureRecipe func(instanceName, featureType string) (*types.FeatureRecipe, bool)
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
			if r, found := recipeLookup.Recipe(nodeID, node.Component); found {
				nodeRecipes[nodeID] = r
			}
		}
	}

	// Pass 1: Evaluate all provides
	allProvides, err := EvaluateAllProvides(graph, nodeRecipes)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "Step 5 Pass 1: provides evaluation")
	}

	// Step 5b: Recipe / FeatureRecipe assertions.
	if err := EvaluateRecipeAndFeatureRecipeAssertions(graph, nodeRecipes, recipeLookup, allProvides); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "Step 5b (Recipe/FeatureRecipe assertions)")
	}

	dispatch := map[dispatchKey]*pendingDispatch{}
	orderedDispatch := []dispatchKey{}

	// Deferred-PATCH accumulator, keyed by the dispatch group of the assembler
	// node. A pipeline against an assembler node cannot PATCH per-node — the
	// merged documents are not discrete until assembly. This covers both scopes:
	// a component-scope FeatureRecipe whose host's provider is an assembler (its
	// /host pipeline) and a Recipe (or component-scope FeatureRecipe) whose own
	// provider is an assembler (its /own pipeline). Each such pipeline is recorded
	// here and replayed in the post-assembly phase against the assembled
	// documents. The records carry the A1 ordering keys (node author order,
	// FeatureOrder index) so the replay layers deterministically.
	deferredByDispatch := map[dispatchKey][]DeferredPatch{}

	// Pre-compute the per-boundary evaluated `directory:` map for the
	// .parent_boundary.directory cascade. Path-template eval at any
	// boundary depth can read its IMMEDIATE parent's evaluated directory
	// from this map; root-level boundaries have no parent and surface
	// missingkey=error if their templates reference .parent_boundary.*.
	var boundaryDirEvals map[string]string
	var perBoundaryAnnotations map[string]types.OrderedMap[string]
	var releaseParameters map[string]any
	if input.ResolveResult != nil {
		releaseParameters = input.ResolveResult.Parameters
		_, perBoundaryAnnotations = resolver.WireBoundaryInfra(types.NormalizeRelease(input.ResolveResult.Release))
		var err error
		boundaryDirEvals, err = evaluateAllBoundaryDirectories(input.ResolveResult.Release, releaseParameters, perBoundaryAnnotations)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{}, "Step 5 boundary directory pre-eval")
		}
	}

	// Boundary-scope FR cross-node accumulator (dedup, provides, files,
	// validator cache). One instance lives per RenderAll invocation;
	// phaseB6 threads it into the executionScope: boundary dispatch path.
	// See boundary_fr.go.
	boundaryFRState := NewBoundaryFRState()

	// Provides pre-pass: evaluate every boundary-scope FeatureRecipe's
	// provides before the per-node loop so a per-component consumer's uses
	// edge can resolve them during Phase A inject rehydration. The per-node
	// Phase B.6 dispatch (which renders the FR's files) re-evaluates the same
	// provides idempotently. See PrepopulateBoundaryScopeProvides.
	if input.ResolveResult != nil {
		if err := PrepopulateBoundaryScopeProvides(graph, input.ResolveResult.Release, input.ResolveResult.BoundaryAnnotations, recipeLookup, allProvides, engineFactory, boundaryFRState); err != nil {
			return nil, err
		}
	}

	// Trace writer (no-op when --trace is off; async filesystem writer
	// when on). Per-pass capture surfaces happen at four representative
	// sites in this revision: Phase A injectMapping output, Phase B.4
	// Workload synthesis output, Phase B.6 boundary-scope FR dispatch,
	// and per-emission provenance-label routing. Other sub-passes wire
	// in a follow-up.
	traceWriter := input.Trace
	if traceWriter == nil {
		traceWriter = trace.New("")
	}

	// Per-node rendering. nodeIDs is sorted (topology author order for the
	// deterministic walk); nodeOrder is the host node's position in that order,
	// the outer A1 replay-ordering key for deferred host PATCHes.
	for nodeOrder, nodeID := range nodeIDs {
		node, _ := graph.Spec.Nodes.Get(nodeID)
		recipe := nodeRecipes[nodeID]

		// Pass 2a: Self-inject resolution (component-level inject)
		inject, injectRaw := BuildInjectPipe(node.SelfInject)
		var selfKeys map[string]map[string]string
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
			var err error
			selfKeys, err = ExecutePass2a(&inject, node.SelfInject, nodeID, allProvides[nodeID], siCtx)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q Pass 2a", nodeID))
			}
		}

		// Pass 2b: Rehydrate inject from uses-edge provides
		RehydrateInjectFromProvides(&inject, graph.Spec.Edges, nodeID, allProvides, graph, recipeLookup, boundaryFRState)

		// Pass 2 collision check: self-inject vs uses-inject (post-2b, pre-Phase-A).
		// Phase A's overlay merge runs downstream and is structurally invisible
		// here per the FeatureRecipe-interception exemption documented in the
		// inject-key-collision-guard rationale.
		if err := CheckSelfInjectCollisions(selfKeys, graph.Spec.Edges, nodeID); err != nil {
			return nil, err
		}

		// Attachment-level inject (feature.inject / secret.inject): resolve each
		// authored delivery key's provides-key reference against the attachment's
		// own resolved provides and deliver into .context.inject, before Phase A's
		// FeatureRecipe injectMapping last-wins overlay.
		if err := ResolveAttachmentInject(&inject, graph, nodeID, recipeLookup); err != nil {
			return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q attachment inject", nodeID))
		}

		// Build template context
		tmplCtx := AssembleNodeContext(nodeID, graph, allProvides, inject, injectRaw)
		if input.ResolveResult != nil && input.ResolveResult.Release != nil {
			AssembleNodeContextWithRelease(tmplCtx, input.ResolveResult.Release)
		}

		// Resolve each credential attachment's fill provides ONCE (single bag) and
		// expose it to the consumer as .secrets.<slot>.provides. The same bag is
		// retained on state below and reused at Phase B.6 as the credential
		// FeatureRecipe's .provides, so the consumer's read and the generated
		// Secret derive from one evaluation (no drift). Also halts a bound
		// FeatureRecipe that does not fulfil its Feature's provides contract.
		secretProvides, err := ResolveSecretProvides(graph, nodeID, recipeLookup, input.CatalogLookup)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q secret provides", nodeID))
		}
		for slot, provided := range secretProvides {
			if sec, ok := tmplCtx.Secrets.Get(slot); ok {
				sec.Provides = provided
				tmplCtx.Secrets.Set(slot, sec)
			}
		}

		// Trace points: per-node pre-loop captures.
		_ = traceWriter.Write(trace.PassPillarsInitial, nodeID, node.BoundaryPath, "", trace.PillarSnapshot(tmplCtx, trace.PassPillarsInitial))
		_ = traceWriter.Write(trace.PassProvides, nodeID, node.BoundaryPath, "", trace.PillarSnapshot(tmplCtx, trace.PassProvides))
		_ = traceWriter.Write(trace.PassInject, nodeID, node.BoundaryPath, "", trace.PillarSnapshot(tmplCtx, trace.PassInject))

		// Phase B sub-pass pipeline per chapter 16 §5. State carries cross-
		// sub-pass accumulators (resolved values, parsed workload, emitted
		// files). Recipe path is empty here today — the catalog loader does
		// not retain the on-disk path on the parsed Recipe; validator errors
		// surfacing through the sub-passes show "" for the path. Future
		// improvement: thread the path through the catalog resolver.
		state := NewPhaseBState(recipe, nodeID, node.BoundaryPath, "")
		state.Trace = traceWriter
		state.MappingLibraries = input.MappingLibraries
		state.SecretProvides = secretProvides

		// Phase B.0 — locals + source-as-template eval; populates
		// ctx.Locals + ctx.Source. May replace state.Recipe with a local
		// copy whose Spec.Source carries the resolved value.
		if err := phaseB0(tmplCtx, state, allProvides[nodeID]); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q", nodeID))
		}
		recipe = state.Recipe // pick up the local copy with resolved source

		// Phase A: injectMapping from FeatureRecipes in Onion Model order.
		// Sits between B.0 and B.1 — chapter 16 sub-pass numbering scopes
		// to Phase B; Phase A is a separate phase and stays at the
		// orchestrator level.
		featureRefs := resolveFeatureRecipes(nodeID, input.FeatureOrder, graph, recipeLookup)
		// Credential-partitioned secrets participate in Phase A too: an
		// inject-only credential FeatureRecipe (no source/workload) rewrites the
		// component's inject pipe exactly as a Feature FR does. Feature refs
		// (Onion order) first, then secret refs (author order). The Phase A
		// deep-merge is last-writer-wins per delivery key, so on a collision a
		// secret overrides a feature (and a later secret overrides an earlier
		// one) — deterministic by this ordering. featureRefs is reused by phaseB6
		// below, so build a separate combined slice rather than appending in
		// place.
		secretRefs := resolveSecretRecipes(nodeID, graph, recipeLookup)
		phaseARefs := make([]FeatureRecipeRef, 0, len(featureRefs)+len(secretRefs))
		phaseARefs = append(phaseARefs, featureRefs...)
		phaseARefs = append(phaseARefs, secretRefs...)
		if err := ExecutePhaseA(tmplCtx, phaseARefs); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q", nodeID))
		}
		// Boundary->component env channel: apply any per-consumer env overrides
		// a boundary-scope FR emitted (computed in the pre-loop) over this
		// consumer's injected env. Gated on map membership, so non-named
		// consumers are untouched; targeted last-wins on exactly the named keys.
		applyConsumerEnvOverrides(&tmplCtx.Context.Inject, boundaryFRState.ConsumerEnvOverrides[nodeID])
		// Trace point: Phase A — feature-scoped per-consumer inject pipe
		// after FR injectMapping rewrites.
		_ = traceWriter.Write(trace.PassPhaseA, nodeID, node.BoundaryPath, "", trace.PillarSnapshot(tmplCtx, trace.PassPhaseA))

		// Phase B.1 — valuesMapping → state.ResolvedValues.
		if err := phaseB1(tmplCtx, state); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q", nodeID))
		}
		values := state.ResolvedValues

		// Ghost Infrastructure: compare valuesMapping template references against
		// the keys actually in .values / .infra — unused keys emit a warning so
		// catalog authors notice when a Release infra knob is never consumed.
		// Inline-mode templates only; file / library imports defer (the
		// template text is disk-resident or OCI-resolved at render time).
		if recipe != nil && recipe.Spec.ValuesMapping.Inline != "" {
			if refs, parseErr := CollectTemplateFieldAccesses(recipe.Spec.ValuesMapping.Inline); parseErr == nil {
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

		hasWorkload := recipe != nil && !recipe.Spec.Workload.IsEmpty()

		// Resolve engine for this node's provider via factory. The factory
		// receives the full binding (version pin included) so it can resolve the
		// pinned provider-rendering plugin; every other consumer below keys on the
		// bare engine kind (dispatch grouping, path segment, codec, compatibility).
		providerRef := ""
		providerName := ""
		if recipe != nil {
			providerRef = recipe.Spec.Provider
			providerName = ProviderEngineKind(recipe.Spec.Provider)
		}
		// Resolved provider-rendering plugin version behind this binding, shared
		// with the engine the factory returns for it. Empty for a binding that
		// renders through the in-tree engine (no resolved plugin version). Stamped
		// on the aggrega.ai/provider-version label and used to pick the dispatch
		// group's boundary assembler when the group mixes provider versions.
		providerVersion := ""
		if input.ProviderVersion != nil {
			providerVersion = input.ProviderVersion(providerRef)
		}
		engine, err := engineFactory(providerRef)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q engine factory", nodeID))
		}

		// Per-node engine render — the source-based render. Output files
		// are tagged EmitOriginSource and accumulated on the state for
		// B.5's collision detector. A node with no source: emits no
		// engine.Render output: no compute is invented for it (Invariant 4).
		// The Workload synthesizer at Phase B.4 is the source of truth for a
		// workload-only Recipe, and coexists with a source-based render when
		// a Recipe declares both source and workload.
		nodeCtx := buildNodeRenderContext(nodeID, node, graph, input)
		nodeCtx.ProviderName = providerName
		nodeCtx.ProviderVersion = providerVersion
		if recipe != nil {
			nodeCtx.RecipeName = recipe.Metadata.Name
			nodeCtx.RecipeVersion = recipe.Metadata.Version
		}
		// Route the non-fatal advisories a provider guest raises while rendering
		// this node into the per-node warnings accumulator, which the orchestrator
		// drains into result.Warnings at the end of the per-node sequence. Set once
		// here so every downstream crossing that carries this node context —
		// engine.Render, artifacts emission, workload synthesis, and the FeatureRecipe
		// sub-renders that copy it — surfaces its guest warnings through one channel.
		nodeCtx.WarningSink = func(w string) { state.Warnings = append(state.Warnings, w) }
		if err := resolveSourceForNode(input, recipe, &nodeCtx); err != nil {
			return nil, wrapSourceResolutionError(err, nodeID,
				fmt.Sprintf("Step 5 node %q source resolution", nodeID))
		}
		hasSource := nodeCtx.Source != nil
		if s, ok := nodeCtx.Source.(string); ok && s == "" {
			hasSource = false
		}
		hasArtifacts := recipe != nil && recipe.Spec.Artifacts.Len() > 0

		// Central input-kind gate (the describe()-fed seat). Classify the node's
		// mode from its declared input kinds and confirm the resolved provider's
		// engine supports it — once per node, BEFORE any Phase B dispatch
		// (engine.Render, B.3 artifacts, B.4 synthesis), so an unsupported
		// (mode, provider) halts loudly before emission. Source > workload >
		// artifacts precedence: a node with a source: or workload: is
		// sourced/synthesised even if it ALSO declares artifacts: (those ride
		// through as composed artifacts and are NOT checked against
		// ArtifactsStandalone); only a no-source/no-workload node is
		// artifacts-only.
		if err := gateInputKind(nodeID, providerName, engine, hasSource, hasWorkload, hasArtifacts); err != nil {
			return nil, err
		}

		// Advertised-only per-node input validation. When the resolved provider's
		// guest declares the optional input-validation export, run it once here —
		// after the capability gate, before any Phase B render pass — so a provider
		// that can detect a malformed input halts loudly (Invariant 4) before
		// emission rather than producing broken output. Error-severity diagnostics
		// halt; warnings surface to the build result.
		if validator, ok := engine.(NodeInputValidator); ok {
			diags, verr := validator.ValidateNodeInput(ctx, values, nodeCtx)
			if verr != nil {
				return nil, cerrors.Wrap(verr, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q input validation", nodeID))
			}
			if err := routeNodeInputDiagnostics(nodeID, providerName, diags, nodeCtx.WarningSink); err != nil {
				return nil, err
			}
		}

		// engine.Render (the source-based render) runs only for sourced mode;
		// synthesised renders in Phase B.4, artifacts-only emits in Phase B.3.
		runEngineRender := hasSource
		var files map[string][]byte
		if runEngineRender {
			var err error
			files, err = engine.Render(ctx, values, nodeCtx)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q render", nodeID))
			}
		}
		// Tag with EmitOriginSource and accumulate (deterministic across
		// runs of identical input by iterating the engine.Render output
		// by sorted filename; state.EmittedFiles order stays stable).
		//
		// Intermediate marking. When the engine implements BoundaryAssembler
		// AND consumes per-node Render output as fragment input (today:
		// compose), Render's output is intermediate to the per-(path,
		// provider) BoundaryAssembler at the contributor's evaluated path.
		// Engines that implement BoundaryAssembler but place per-node
		// content at engine-controlled positions (today: helm-chart,
		// signalled by ExternalArtifactsHandler — its sub-charts land at
		// charts/<nodeID>/ via the umbrella's nestPerNode contract) keep
		// Render output Intermediate=false so the orchestrator nests their
		// files under the per-node prefix rather than routing them to
		// AssembleBoundary's IntermediatesByNode. Engines that are not
		// BoundaryAssemblers also keep Intermediate=false.
		renderIsIntermediate := RenderIsIntermediate(engine)
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
				Intermediate: renderIsIntermediate,
			})
		}

		// Phase B.2 — workload eval + ValidateWorkload + ValidateWorkloadSecrets.
		// No-op for Recipes that don't declare workload:. The workload-secret
		// cross-check validates containers.<n>.secrets against the bound
		// Component's secretsSchema slot allowlist.
		var slots types.OrderedMap[types.SecretSlot]
		if input.CatalogLookup.Component != nil {
			if comp, found := input.CatalogLookup.Component(node.Component); found {
				slots = comp.Spec.SecretsSchema
			}
		}
		if err := phaseB2(tmplCtx, state, slots); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q", nodeID))
		}

		// Phase B.3 — artifacts evaluation. Each artifacts: entry (inline
		// content or $import:-loaded) renders through Go templates against
		// the full TemplateContext, gets provenance labels merged into K8s metadata
		// when present, and accumulate on state.EmittedFiles for Phase
		// B.5 collision detection.
		if err := phaseB3(ctx, tmplCtx, state, engine, nodeCtx); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q", nodeID))
		}

		// Phase B.4 — workload synthesizer dispatch. No-op for a node with no
		// parsed workload (state.ParsedWorkload is nil); dispatches to the
		// engine's WorkloadSynthesizer otherwise (engines that don't
		// implement the interface produce a CATALOG_ERROR).
		if err := phaseB4(ctx, tmplCtx, state, engine, nodeCtx); err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q", nodeID))
		}
		// Slot 10 (PassPhaseB4) is now wired per-emitted-file inside phaseB4
		// itself; the orchestrator-level single-event-per-node wire that
		// previously lived here was superseded by the per-file granularity.

		// Phase B.5 — filename collision detection across input-kind
		// origins (rule 7). Collisions can surface across EmitOriginSource
		// and EmitOriginWorkload contributions (when a Recipe declares both
		// source: and workload:); EmitOriginArtifacts* contributions land
		// when the artifacts render path ships.
		if err := phaseB5(state); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q", nodeID))
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
		var bi *resolver.BoundaryInfra
		var parameters map[string]any
		if input.ResolveResult != nil {
			if rbi, ok := input.ResolveResult.BoundaryInfra[nodeID]; ok && rbi != nil {
				bi = rbi
			}
			parameters = input.ResolveResult.Parameters
		}
		finalPath, err := computeFinalPath(nodeID, bp, providerName, recipeName, bi, parameters, boundaryDirEvals, perBoundaryAnnotations)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 path eval node %q", nodeID))
		}

		// Recipe pipeline (own scope). Runs per-node AFTER the primary render
		// (state.EmittedFiles carries source/workload/artifacts — the node's
		// own discrete documents) and BEFORE the dispatch snapshot below reads
		// them. FeatureRecipe files are not yet in state.EmittedFiles (phaseB6
		// returns them separately and merges them into the dispatch map), so
		// the own set here is exactly the node's own output. The pipeline
		// applies /own PATCHes and appends /own EMITs as EmitOriginPipeline
		// files; re-running the rule-7 collision detector catches a pipeline
		// EMIT clashing with a rendered filename.
		//
		// Own-on-assembler: when this node's own provider is an assembler
		// (renderIsIntermediate — its per-node output is pre-assembly fragments,
		// not discrete documents), a /own PATCH cannot run per-node. The pipeline
		// is deferred whole to the post-assembly phase (recorded below, after the
		// dispatch key exists) and replayed against the merged documents, exactly
		// as an assembler host's /host pipeline defers. EMIT is additive and could
		// run per-node, but the whole pipeline defers as one record so a mixed
		// PATCH+EMIT pipeline replays in its declared step order on the merged
		// document.
		var recipeOwnDeferred *DeferredPatch
		if recipe != nil && len(recipe.Spec.Pipeline) > 0 {
			renderCtx := buildPipelineRenderCtx(node, bi, parameters)
			if renderIsIntermediate {
				recipeOwnDeferred = &DeferredPatch{
					Steps:             recipe.Spec.Pipeline,
					WithCtx:           tmplCtx,
					RenderCtx:         renderCtx,
					OwnProvider:       providerName,
					NodeID:            nodeID,
					FeatureOrderIndex: -1, // a Recipe own pipeline precedes every FeatureRecipe in the cascade
				}
			} else {
				// own provider is this node's recipe provider; a Recipe step has no
				// host scope (host nil), so hostProvider is empty.
				pres, perr := runNodePipeline(ctx, recipe.Spec.Pipeline, state.EmittedFiles, nil, false, providerName, "", renderCtx, tmplCtx, input.YAMLIndent, input.PluginPathLookup, engineFactory, nodeID)
				if perr != nil {
					return nil, perr
				}
				state.EmittedFiles = pres.Own
				result.Warnings = append(result.Warnings, pres.Warnings...)
				if err := phaseB5(state); err != nil {
					return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q pipeline EMIT collision", nodeID))
				}
			}
		}

		key := dispatchKey{Path: finalPath, Provider: providerName}
		// Stash a deferred own-scope Recipe pipeline under this node's dispatch
		// group now that the key exists. The merged documents the /own PATCH
		// targets are the ones this dispatch group's assembler produces, the same
		// group an attached FeatureRecipe's deferred /host pipeline lands in.
		if recipeOwnDeferred != nil {
			recipeOwnDeferred.HostNodeOrder = nodeOrder
			deferredByDispatch[key] = append(deferredByDispatch[key], *recipeOwnDeferred)
		}
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
			inner := fmt.Errorf("%w: dispatch path %q provider %q already contains node %q (contributing boundaries: %v); component instance IDs must be unique within an assembly group",
				ErrInstanceIDCollision, key.Path, key.Provider, nodeID, dispatch[key].contributingBoundaries)
			return nil, cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
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
		// Record this node as a boundary-assembler candidate for its dispatch
		// group. The group's assembler is elected after the per-node loop as the
		// engine of the highest resolved provider version (selectBoundaryAssembler)
		// — not the first engine seen — so a mixed-version group assembles through
		// its newest provider. Candidates append in node-processing order, which
		// the election's earliest-wins tie-break relies on.
		dispatch[key].assemblerCandidates = append(dispatch[key].assemblerCandidates, assemblerCandidate{
			engine:  engine,
			version: providerVersion,
		})

		// Phase B.6 — FeatureRecipe Resource Generation (both non-credential
		// and credential FRs). Returns prefixed paths
		// ("features/<instance>/<file>", "secrets/<name>/<file>") merged
		// into the per-node files map alongside source/workload outputs.
		secretsForNode, _ := graph.Spec.Secrets.Get(nodeID)
		var releaseForFR *types.Release
		var boundaryAnnotationsForFR map[string]types.OrderedMap[string]
		if input.ResolveResult != nil {
			releaseForFR = input.ResolveResult.Release
			boundaryAnnotationsForFR = input.ResolveResult.BoundaryAnnotations
		}
		// Host docs a component-scope FR pipeline may /host PATCH or EMIT: the
		// host component's own discrete documents (state.EmittedFiles).
		// hostIsAssembler mirrors the per-node renderIsIntermediate predicate —
		// an assembler that is not an ExternalArtifactsHandler renders
		// pre-assembly fragments, so a /host PATCH against it halts (post-
		// assembly territory). providerName is the host component's recipe
		// provider, threaded in so a host-scope pipeline step's
		// compatibleProviders is enforced against the provider rendering the
		// /host documents. renderCtx is the render-time context every pipeline
		// step receives.
		pipelineRenderCtx := buildPipelineRenderCtx(node, bi, parameters)
		frFiles, hostPatched, deferred, err := phaseB6(ctx, tmplCtx, state, featureRefs, engineFactory, allProvides, secretsForNode, recipeLookup, nodeCtx, boundaryFRState, graph, releaseForFR, boundaryAnnotationsForFR, input, state.EmittedFiles, renderIsIntermediate, providerName, pipelineRenderCtx)
		if err != nil {
			return nil, err
		}
		// Stash deferred host PATCHes under the host component's dispatch group.
		// The host node IS this node (a component-scope FR patches the component
		// it is attached to), so the merged documents the PATCH targets are the
		// ones this dispatch group's assembler produces. Stamp the host-node
		// author-order key for the A1 replay ordering.
		for i := range deferred {
			deferred[i].HostNodeOrder = nodeOrder
			deferredByDispatch[key] = append(deferredByDispatch[key], deferred[i])
		}
		for path, content := range frFiles {
			dispatch[key].files[nodeID][path] = content
		}
		// Host-document write-back conduit: a component-scope FR pipeline's
		// /host PATCH (a rewritten host doc) or /host EMIT (a new host doc)
		// arrives keyed by the host doc's original emitted path. The host docs
		// were snapshotted into dispatch[key].files[nodeID] before phaseB6 ran,
		// so this merge — keyed by path — is the only live write-back path;
		// mutating state.EmittedFiles would be inert. A PATCH overwrites the
		// snapshotted host doc; an EMIT adds a new entry.
		for path, content := range hostPatched {
			dispatch[key].files[nodeID][path] = content
		}
		// Trace point: Phase B.6 — FeatureRecipe Resource Generation. The
		// snapshot captures the post-B.6 TemplateContext for this node;
		// boundary-scope FR contributions deduplicated by Phase B.6 surface
		// in boundaryFRState.Files (post-loop, not per-node here).
		_ = traceWriter.Write(trace.PassPhaseB6, nodeID, node.BoundaryPath, "", trace.PillarSnapshot(tmplCtx, trace.PassPhaseB6))

		// Drain Phase B's per-node warnings into the build-level result.
		// Drain position is the end of the per-node Phase B sequence
		// (after phaseB6 — the LAST sub-pass) so any sub-pass appending
		// to state.Warnings reaches result.Warnings, not just B.3's
		// helm-chart provenance-skip notice today.
		result.Warnings = append(result.Warnings, state.Warnings...)

		// Trace point: per-node emission log (slot 11). Captures the
		// post-Phase-B node summary — the warnings accumulated during the
		// per-node sub-pass run plus the final emitted-files inventory.
		emittedNames := make([]string, 0, len(state.EmittedFiles))
		for _, f := range state.EmittedFiles {
			emittedNames = append(emittedNames, f.Name)
		}
		emissionLog := map[string]any{
			"node_id":       nodeID,
			"boundary_path": node.BoundaryPath,
			"warnings":      state.Warnings,
			"emitted_files": emittedNames,
		}
		_ = traceWriter.Write(trace.PassEmission, nodeID, node.BoundaryPath, "", emissionLog)

		// Trace point: per-emission provenance routing summary. Captures
		// the per-node label and annotation set applied to emitted files
		// at the post-render handoff. Surfaces the canonical aggrega.ai/*
		// labels and the argocd.argoproj.io/sync-wave annotation each
		// node's emission carries.
		labelsSummary := map[string]any{
			"node_id":             nodeID,
			"boundary_path":       node.BoundaryPath,
			"provenance_labels":   buildProvenanceLabels(state, nodeCtx),
			"provenance_annots":   buildProvenanceAnnotations(state, nodeCtx),
			"emitted_files_count": len(state.EmittedFiles),
		}
		_ = traceWriter.Write(trace.PassLabels, nodeID, node.BoundaryPath, "", labelsSummary)

		// Per-node progress hook (nil = no-op). Fires once as this node's render
		// completes, with the 1-based completed count and the total node count.
		// A diagnostic observation only; it must not touch the file map, so
		// rendered output is invariant with respect to whether it is set.
		if input.NodeProgress != nil {
			input.NodeProgress(nodeOrder+1, len(nodeIDs), nodeID)
		}
	}

	// Boundary-scope FR output routing. After the per-node loop, any
	// boundary-scope FR that ran (deduped to one execution per
	// (boundary_path, fr_instance)) has produced files captured in
	// boundaryFRState.Files. Classify each by Intermediate flag:
	//
	//   - Intermediate=true: route to the per-(path, provider) dispatch
	//     group at the FR's evaluated path, using the FR's spec.provider
	//     for the {{ .provider }} substitution. The compose-bridge case
	//     drives this — the bridge service fragment lands alongside per-
	//     component compose contributors at the same dispatch path so the
	//     compose engine's AssembleBoundary merges them.
	//   - Intermediate=false: write directly under
	//     <boundary_directory>/<provider>/features/<fr_instance>/<file>.
	//
	// Synthetic node-IDs ("<boundary-fr>:<instance>") distinguish boundary-
	// scope contributions from per-component contributions in the dispatch
	// group's per-node maps.
	for key, files := range boundaryFRState.Files {
		// Look up the FR by walking the release; the FR's provider is
		// what determines the dispatch path's provider component.
		feat, found := lookupBoundaryFeatureAttachment(graph.Spec.BoundaryFeatures, key.BoundaryPath, key.FRInstance)
		if !found {
			continue
		}
		// Resolve the FR for this boundary-feature instance (name tier wins,
		// else by type) to get its provider.
		fr, ok := recipeLookup.FeatureRecipe(key.FRInstance, feat.Type)
		if !ok {
			continue
		}
		// Bare engine kind for the {{ .provider }} path segment and the dispatch
		// grouping; the FR's files were already rendered through its version-pinned
		// engine upstream, so no engine resolution happens here.
		frProvider := ProviderEngineKind(fr.Spec.Provider)
		// Compute the dispatch path for this boundary-scope FR. The FR is
		// attached at the boundary level (no per-component BoundaryInfra
		// entry), so look up the boundary's authored directory/subDirectory
		// directly from the Release tree and synthesise a BoundaryInfra
		// for computeFinalPath. Operator-authored templates on the boundary
		// thus drive the FR's dispatch path the same way they drive
		// per-component dispatch paths.
		var parameters map[string]any
		if input.ResolveResult != nil {
			parameters = input.ResolveResult.Parameters
		}
		var bi *resolver.BoundaryInfra
		if input.ResolveResult != nil && input.ResolveResult.Release != nil {
			parts := strings.Split(key.BoundaryPath, "/")
			if b, ok := findBoundaryAtPath(input.ResolveResult.Release.Spec.Boundaries, parts); ok {
				bi = &resolver.BoundaryInfra{
					BoundaryPath: key.BoundaryPath,
					Directory:    b.Directory,
					SubDirectory: b.SubDirectory,
					SyncWave:     b.SyncWave,
				}
			}
		}
		// Use the FR's provider for the {{ .provider }} substitution
		// instead of any per-component provider.
		finalPath, err := computeFinalPath("<boundary-fr>:"+key.FRInstance, key.BoundaryPath, frProvider, fr.Metadata.Name, bi, parameters, boundaryDirEvals, perBoundaryAnnotations)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{}, fmt.Sprintf("Step 5 boundary-scope FR %q path eval", key.FRInstance))
		}
		dKey := dispatchKey{Path: finalPath, Provider: frProvider}
		syntheticNodeID := "<boundary-fr>:" + key.FRInstance
		if _, ok := dispatch[dKey]; !ok {
			dispatch[dKey] = &pendingDispatch{
				files:           map[string]map[string][]byte{},
				intermediates:   map[string][]EmittedFile{},
				nodeAnnotations: map[string]map[string]string{},
				nodeComponent:   map[string]string{},
				engineByNode:    map[string]Engine{},
			}
			orderedDispatch = append(orderedDispatch, dKey)
		}
		intermediates := []EmittedFile{}
		nodeFiles := map[string][]byte{}
		for _, f := range files {
			if f.Intermediate {
				intermediates = append(intermediates, f)
				continue
			}
			// Intermediate=false: emit directly under features/<instance>/.
			nodeFiles["features/"+key.FRInstance+"/"+f.Name] = f.Body
		}
		if len(intermediates) > 0 {
			dispatch[dKey].intermediates[syntheticNodeID] = append(dispatch[dKey].intermediates[syntheticNodeID], intermediates...)
		}
		if len(nodeFiles) > 0 {
			dispatch[dKey].files[syntheticNodeID] = nodeFiles
		}
		// Record the boundary as a contributor for the dispatch group's
		// diagnostic context.
		alreadyContributing := false
		for _, existing := range dispatch[dKey].contributingBoundaries {
			if existing == key.BoundaryPath {
				alreadyContributing = true
				break
			}
		}
		if !alreadyContributing {
			dispatch[dKey].contributingBoundaries = append(dispatch[dKey].contributingBoundaries, key.BoundaryPath)
		}
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
		inner := fmt.Errorf("%w: dispatch path %q produced multiple provider outputs (%v); contributing boundaries %v resolve to the same path under their current directory: / subDirectory: settings; set distinct subDirectory values per provider or change one boundary's directory",
			ErrPathCollision, path, providers, contributingBoundaries)
		return nil, cerrors.Wrap(inner, cerrors.OpsError, cerrors.SourceLocation{}, inner.Error())
	}

	// Assembly-name uniqueness check: a provider whose assembler declares an
	// assembly-name annotation with require-unique folds a dispatch group's
	// contributing boundaries into one named assembly, so every contributing
	// boundary's effective value for that annotation (computed via the resolver's
	// per-boundary annotation cascade) must resolve to the same value. Mismatches
	// indicate the operator has merged paths that conceptually belong to
	// different assemblies. A provider that declares no such annotation gets no
	// check.
	for _, key := range orderedDispatch {
		pd := dispatch[key]
		annotationKey, requireUnique := assemblyNameAnnotationOf(groupAssemblerEngine(pd))
		if annotationKey == "" || !requireUnique {
			continue
		}
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
					name, _ = ann.Get(annotationKey)
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
			inner := fmt.Errorf("%w: boundaries %v merge to assembly path %q under provider %q but their effective %s annotations resolve to different values: %v; the merged assembly has exactly one name; cascade or declare matching values explicitly",
				ErrChartNameMismatch, pd.contributingBoundaries, key.Path, key.Provider, annotationKey, mismatchValues)
			return nil, cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
		}
	}

	// Elect each dispatch group's boundary assembler now that every contributing
	// node has been grouped: the engine of the highest resolved provider version
	// in the group, ties broken by node-processing order. Halts loudly if the
	// elected engine cannot assemble a group its provider is expected to assemble
	// — it never falls back to a lower-version engine that happens to assemble.
	for _, key := range orderedDispatch {
		if err := selectBoundaryAssembler(dispatch[key], key); err != nil {
			return nil, err
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
			edges := make([]IREdgeInfo, 0, len(graph.Spec.Edges))
			for _, e := range graph.Spec.Edges {
				edges = append(edges, IREdgeInfo{Source: e.Source, Target: e.Target})
			}
			// Cascaded assembly-name lookup: a provider whose assembler declares
			// an assembly-name annotation cascades the first resolved value from
			// its contributing boundaries into the assembler as
			// BoundaryInfo.ChartName. Empty when no cascade value resolves or the
			// provider declares no such annotation (assembler falls back to
			// path-derived naming).
			chartName := ""
			if annotationKey, _ := assemblyNameAnnotationOf(groupAssemblerEngine(pd)); annotationKey != "" && input.ResolveResult != nil {
				for _, bp := range pd.contributingBoundaries {
					if ann, ok := input.ResolveResult.BoundaryAnnotations[bp]; ok {
						if v, ok := ann.Get(annotationKey); ok && v != "" {
							chartName = v
							break
						}
					}
				}
			}
			info := BoundaryInfo{
				BoundaryPath:           key.Path,
				AggregateName:          graph.Metadata.AggregateName,
				YAMLIndent:             input.YAMLIndent,
				SubCharts:              pd.subCharts,
				IntermediatesByNode:    pd.intermediates,
				NodeAnnotations:        pd.nodeAnnotations,
				NodeComponent:          pd.nodeComponent,
				ContributingBoundaries: pd.contributingBoundaries,
				Edges:                  edges,
				ChartName:              chartName,
				WarningSink:            func(w string) { result.Warnings = append(result.Warnings, w) },
			}
			boundaryFiles, nestPerNode, err := pd.assembler.AssembleBoundary(ctx, info)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: key.Path}, fmt.Sprintf("Step 5 dispatch %q assemble", key.Path))
			}
			// Post-assembly PATCH replay (scope-symmetric). A pipeline against
			// this assembler node deferred per-node because the merged documents
			// were not discrete yet: a component-scope FeatureRecipe's /host
			// pipeline, or a Recipe (or component-scope FeatureRecipe) whose own
			// provider is this assembler (its /own pipeline). Now that the
			// assembler has produced the merged documents, replay each deferred
			// pipeline against them — own-scope routed to the own root, host-scope
			// to the host root, own-before-host— in the deterministic A1
			// cascade, rewriting boundaryFiles in place before they are written.
			if recs := deferredByDispatch[key]; len(recs) > 0 {
				warnings, perr := replayDeferredPatches(ctx, boundaryFiles, key.Provider, recs, engineFactory, input)
				if perr != nil {
					return nil, perr
				}
				result.Warnings = append(result.Warnings, warnings...)
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
		fr, found := recipeLookup.FeatureRecipe(instanceName, entry.Type)
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

// resolveSecretRecipes builds Phase A refs for the node's credential-partitioned
// secrets (graph.Spec.Secrets[nodeID]). Each secret resolves its FeatureRecipe
// by the secret's Type and carries a scope set to the secret entry's own values
// with empty infra — so an inject-only credential FR's injectMapping rewrites the
// component inject pipe with the secret-scoped context. Secrets are walked in
// author order (the IR secrets bucket is author-ordered). A secret whose FR is
// not found, or carries no injectMapping, is skipped by Phase A downstream.
func resolveSecretRecipes(nodeID string, graph *types.IRGraph, recipeLookup RecipeLookup) []FeatureRecipeRef {
	if recipeLookup.FeatureRecipe == nil {
		return nil
	}
	secretsForNode, ok := graph.Spec.Secrets.Get(nodeID)
	if !ok {
		return nil
	}
	var refs []FeatureRecipeRef
	for secretName, entry := range secretsForNode.All() {
		fr, found := recipeLookup.FeatureRecipe(secretName, entry.Type)
		if !found {
			continue
		}
		refs = append(refs, FeatureRecipeRef{
			InstanceName:  secretName,
			FeatureRecipe: fr,
			scope: &phaseAScope{
				Values: entry.Values,
				Infra:  types.OrderedMap[any]{},
			},
		})
	}
	return refs
}

// gateInputKind is the central input-kind capability gate. It classifies a
// node's mode from its declared input kinds and confirms the resolved
// provider's engine supports that mode — once per node, BEFORE any Phase B
// dispatch (engine.Render, B.3 artifacts, B.4 synthesis), so an unsupported
// (mode, provider) halts loudly before emission. Source > workload > artifacts
// precedence: a node with source: or workload: is sourced/synthesised even if
// it ALSO declares artifacts: (those ride through as composed artifacts and are
// NOT checked against ArtifactsStandalone); only a no-source/no-workload node
// is artifacts-only.
//
// Both the component render path (RenderAll) and the FeatureRecipe render
// sites call this, so a Recipe and a FeatureRecipe bound to the same provider
// gate identically — there is one gate, not a parallel FR track.
func gateInputKind(nodeID, providerName string, engine Engine, hasSource, hasWorkload, hasArtifacts bool) error {
	caps := engine.Capabilities()
	switch {
	case hasSource:
		if !caps.Sourced {
			return unsupportedInputKindError(nodeID, providerName, "sourced", caps)
		}
	case hasWorkload:
		if !caps.Synthesized {
			return unsupportedInputKindError(nodeID, providerName, "synthesised", caps)
		}
	case hasArtifacts:
		if !caps.ArtifactsStandalone {
			return unsupportedInputKindError(nodeID, providerName, "artifacts-only", caps)
		}
	}
	return nil
}

// routeNodeInputDiagnostics splits a provider's per-node input-validation
// diagnostics by severity: warning- and info-severity entries go to the node's
// warning sink (nil-safe; the orchestrator wires it to result.Warnings), and
// error-severity entries halt the build with the ErrProviderNodeInputInvalid
// Loud Guardrail, naming the node, the provider, and the guest's messages.
// Warnings never affect emitted bytes. The component render path and the
// FeatureRecipe render path share this routing, so a provider's input check
// reads identically on both surfaces.
func routeNodeInputDiagnostics(nodeID, providerName string, diags []Diagnostic, sink func(string)) error {
	errs, advisories := SplitDiagnostics(diags)
	for _, a := range advisories {
		if sink != nil {
			sink(fmt.Sprintf("node %q (provider %q): %s", nodeID, providerName, a))
		}
	}
	if len(errs) > 0 {
		inner := fmt.Errorf("%w: node %q, provider %q: %s",
			ErrProviderNodeInputInvalid, nodeID, providerName, strings.Join(errs, "; "))
		return cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, inner.Error())
	}
	return nil
}

// unsupportedInputKindError is the single uniform diagnostic the central
// input-kind gate emits when a provider's engine does not support the node's
// mode. It names the provider, the unsupported mode, the kinds the provider
// does support, and the remedy — framed as a provider capability, not a
// "compute mandatory" semantic opinion (Invariant 1).
func unsupportedInputKindError(nodeID, providerName, mode string, caps ProviderCapabilities) error {
	return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q: %s", nodeID, UnsupportedInputKindMessage(providerName, mode, caps)),
		nil, nil)
}

// UnsupportedInputKindMessage is the provider-capability clause shared by the
// render-time input-kind gate and the early lint detector, so an unsupported
// (mode, provider) reads identically whether it surfaces at lint or at render.
// It names the provider, the unsupported mode, the kinds the provider does
// support, and the remedy — framed as a provider capability, not a "compute
// mandatory" semantic opinion (Invariant 1). Callers prefix it with the
// offending entity: the gate with the flattened node id, the lint detector with
// the Recipe/FeatureRecipe identity.
func UnsupportedInputKindMessage(providerName, mode string, caps ProviderCapabilities) string {
	return fmt.Sprintf(
		"provider %q does not support %s recipes (this provider supports: %s); "+
			"declare one of the supported input kinds, or target a provider that supports %s",
		providerName, mode, supportedKindsList(caps), mode)
}

// supportedKindsList renders a provider's supported input kinds for the
// diagnostic, in source → synthesised → artifacts-only order. Returns "none"
// when the provider supports no input kind (not expected for a rendering
// provider).
func supportedKindsList(caps ProviderCapabilities) string {
	var kinds []string
	if caps.Sourced {
		kinds = append(kinds, "sourced")
	}
	if caps.Synthesized {
		kinds = append(kinds, "synthesised")
	}
	if caps.ArtifactsStandalone {
		kinds = append(kinds, "artifacts-only")
	}
	if len(kinds) == 0 {
		return "none"
	}
	return strings.Join(kinds, ", ")
}

func buildNodeRenderContext(
	nodeID string,
	node types.IRNode,
	graph *types.IRGraph,
	input RendererInput,
) NodeRenderContext {
	nrc := NodeRenderContext{
		NodeID:       nodeID,
		Annotations:  flattenOrderedStringMap(node.ReleaseAnnotations),
		BoundaryPath: node.BoundaryPath,
		SyncWave:     node.SyncWave,
		YAMLIndent:   input.YAMLIndent,
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

// selectBoundaryAssembler elects a dispatch group's boundary assembler and, on a
// mismatch, halts with the No-Ghost-Infrastructure loud guardrail. The assembler
// is the engine of the highest resolved provider version among the group's nodes
// (providerVersionGreater); a version-less node — the in-tree fallback route,
// carrying no resolved plugin version — ranks below any versioned node. Ties
// break on node-processing order: candidates append in that order and the scan
// replaces the leader only on a strict version win, so the earliest node keeps
// the assembler on a tie.
//
// The guardrail applies to the ELECTED engine, never a lower-version fallback:
// when that engine cannot assemble but the in-tree engine it stands in for can (a
// rendering plugin advertising per-node rendering without the matching
// boundary-assembly export), the build halts rather than silently emitting
// unmerged per-node fragments. A group with no candidates (e.g. only boundary-
// scope FeatureRecipe contributions) leaves the assembler unset — the group
// finalizes flat.
func selectBoundaryAssembler(pd *pendingDispatch, key dispatchKey) error {
	selected := groupAssemblerEngine(pd)
	if selected == nil {
		return nil
	}
	if a, ok := selected.(BoundaryAssembler); ok {
		pd.assembler = a
		return nil
	}
	if AssemblesBoundary(selected) && key.Path != "" {
		inner := fmt.Errorf("%w: provider %q renders per node but its rendering plugin does not provide boundary assembly (assemble_boundary); dispatch path %q would emit unassembled fragments",
			ErrBoundaryAssemblerMissing, key.Provider, key.Path)
		return cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{File: key.Path}, inner.Error())
	}
	return nil
}

// groupAssemblerEngine returns the engine that assembles a dispatch group — the
// highest resolved-provider-version candidate, ties broken by node-processing
// order (candidates append in that order and the scan replaces the leader only on
// a strict version win). It is the single election both selectBoundaryAssembler
// (which sets pd.assembler) and the capability reads (the assembly-name guard and
// cascade) consult, so a group reads its provider-level claim from the same engine
// that assembles it. Returns nil when the group has no candidates — e.g. only
// boundary-scope FeatureRecipe contributions — in which case the group finalizes
// flat.
func groupAssemblerEngine(pd *pendingDispatch) Engine {
	best := -1
	for i := range pd.assemblerCandidates {
		if best == -1 || providerVersionGreater(pd.assemblerCandidates[i].version, pd.assemblerCandidates[best].version) {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return pd.assemblerCandidates[best].engine
}

func tmplCtxValues(graph *types.IRGraph, nodeID string) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	raw, ok := graph.Spec.ValuesMapping.Get(nodeID)
	if !ok {
		return out
	}
	_ = parse.IRPayload(raw, &out)
	return out
}

func tmplCtxInfra(graph *types.IRGraph, nodeID string) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
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
// bi is the resolver-side per-component BoundaryInfra (the same struct
// already threaded through the orchestrator from input.ResolveResult.
// BoundaryInfra). Pass nil to use defaults across the board.
//
// Defaults:
//   - bi nil OR bi.Directory empty → "boundaries/<bp>" (today's per-boundary segregation).
//   - bi nil OR bi.SubDirectory empty → render `{{ .provider }}` against the per-component
//     provider name.
//
// Available template variables: .provider, .boundary_name, .parameters.<key>,
// .component_id (subDirectory: only), .manifest.recipe_name, .parent_boundary.*.
//
// boundaryDirEvals is the precomputed per-boundary evaluated `directory:`
// map produced by evaluateAllBoundaryDirectories; it sources
// .parent_boundary.directory for the IMMEDIATE parent boundary of bp.
// perBoundaryAnnotations sources .parent_boundary.annotations from the
// resolver's cascaded annotation map. Both default to no .parent_boundary.*
// when the parent path is empty (root-level boundary) or absent from the
// maps. .parent_boundary.parameters mirrors .parameters since parameters
// are Release-global.
func computeFinalPath(
	nodeID, bp, providerName, recipeName string,
	bi *resolver.BoundaryInfra,
	parameters map[string]any,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (string, error) {
	data := map[string]any{
		"provider":        providerName,
		"boundary_name":   bp,
		"parameters":      parameters,
		"component_id":    nodeID,
		"manifest":        map[string]any{"recipe_name": recipeName},
		"parent_boundary": parentBoundaryData(bp, parameters, boundaryDirEvals, perBoundaryAnnotations),
	}

	// Evaluate directory:
	var evalDir string
	if bi != nil && bi.Synthesized {
		// The compiler-synthesized `default` boundary emits flat to dist/ —
		// no "boundaries/<bp>" segregation segment. Keyed on the provenance
		// flag, NOT the name: a boundary a user explicitly named "default"
		// has Synthesized == false and still segregates to boundaries/default.
		evalDir = ""
	} else if bi == nil || bi.Directory == "" {
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

// parentBoundaryData returns the .parent_boundary.* data map for path-template
// evaluation at boundary path bp. The parent path is the prefix of bp before
// the last `/`; root-level boundaries return an empty map (no parent). Fields:
//
//   - directory: parent's evaluated directory string from boundaryDirEvals,
//     or the default "boundaries/<parentPath>" when the parent was not
//     evaluated explicitly (parent has no `directory:` declared and the
//     pre-compute walk skipped it).
//   - boundary_name: the parent's authored name (last segment of parent path).
//   - parameters: the same Release-global parameters map.
//   - annotations: parent's cascaded annotations from perBoundaryAnnotations.
//
// Returns an empty map for root-level boundaries; references like
// `.parent_boundary.directory` then resolve to nil and trigger missingkey=error
// per the path-template strictness policy.
func parentBoundaryData(
	bp string,
	parameters map[string]any,
	boundaryDirEvals map[string]string,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) map[string]any {
	parentPath, parentName, ok := splitParentPath(bp)
	if !ok {
		return map[string]any{}
	}
	parentDir, hasEval := boundaryDirEvals[parentPath]
	if !hasEval {
		parentDir = "boundaries/" + parentPath
	}
	annotations := map[string]string{}
	if pa, ok := perBoundaryAnnotations[parentPath]; ok {
		for k, v := range pa.All() {
			annotations[k] = v
		}
	}
	return map[string]any{
		"directory":     parentDir,
		"boundary_name": parentName,
		"parameters":    parameters,
		"annotations":   annotations,
	}
}

// splitParentPath returns the parent boundary path and parent boundary name
// for a slash-separated boundary path bp. For "cluster/services-net":
// ("cluster", "cluster", true). For "a/b/c": ("a/b", "b", true). For a
// root-level boundary "cluster" (no slash): ("", "", false).
func splitParentPath(bp string) (parentPath, parentName string, ok bool) {
	idx := strings.LastIndex(bp, "/")
	if idx < 0 {
		return "", "", false
	}
	parentPath = bp[:idx]
	if pIdx := strings.LastIndex(parentPath, "/"); pIdx >= 0 {
		parentName = parentPath[pIdx+1:]
	} else {
		parentName = parentPath
	}
	return parentPath, parentName, true
}

// evaluateAllBoundaryDirectories walks the Release boundary tree top-down and
// returns a map keyed by boundary path of evaluated `directory:` strings.
// The eval at each boundary uses the parent's already-evaluated directory as
// `.parent_boundary.directory` (cascade). `.provider` is set to empty during
// the walk because boundary-level `directory:` cannot meaningfully reference
// per-component provider — per-provider segregation is the `subDirectory:`
// concern (default `{{ .provider }}`). Boundaries without an explicit
// `directory:` declaration are skipped (consumers fall back to the default
// "boundaries/<bp>" string in parentBoundaryData).
//
// perBoundaryAnnotations sources `.parent_boundary.annotations` for nested
// boundaries — read by the parent_boundary cascade walk before the parent's
// own eval finalises.
func evaluateAllBoundaryDirectories(
	release *types.Release,
	parameters map[string]any,
	perBoundaryAnnotations map[string]types.OrderedMap[string],
) (map[string]string, error) {
	out := map[string]string{}
	if release == nil {
		return out, nil
	}
	var walk func(name, parentPath string, b types.Boundary) error
	walk = func(name, parentPath string, b types.Boundary) error {
		currentPath := name
		if parentPath != "" {
			currentPath = parentPath + "/" + name
		}
		if b.Directory != "" {
			data := map[string]any{
				"provider":        "",
				"boundary_name":   currentPath,
				"parameters":      parameters,
				"manifest":        map[string]any{"recipe_name": ""},
				"parent_boundary": parentBoundaryData(currentPath, parameters, out, perBoundaryAnnotations),
			}
			eval, err := EvalPathTemplate("directory:"+currentPath, "directory", currentPath, b.Directory, data)
			if err != nil {
				return cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: currentPath}, fmt.Sprintf("boundary %q directory eval", currentPath))
			}
			out[currentPath] = eval
		}
		for childName, childBoundary := range b.Boundaries.All() {
			if err := walk(childName, currentPath, childBoundary); err != nil {
				return err
			}
		}
		return nil
	}
	for bName, boundary := range release.Spec.Boundaries.All() {
		if err := walk(bName, "", boundary); err != nil {
			return nil, err
		}
	}
	return out, nil
}
