// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Phase B sub-pass functions per chapter 16 §5.1. The orchestrator threads a
// single PhaseBState accumulator through B.0 → B.6 in order; each function
// reads from and writes to the state, mutates the TemplateContext where
// the per-pass availability matrix specifies, and propagates errors.
//
// The orchestrator calls these sub-passes directly during production
// render runs. ExecutePhaseB at phase_b.go is a legacy entry point
// retained for the dedicated phase B unit tests; production code paths
// no longer use it.

package renderer

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// phaseB0 evaluates Recipe.Spec.Locals and Recipe.Spec.Source as Go templates
// against the self-contained context (values / infra / providesForNode only;
// no inject / uses access). Populates ctx.Locals and ctx.Source on success.
//
// On entry: ctx.Source is nil (sub-pass availability matrix invariant).
// On exit:  ctx.Source carries the resolved source value (string URI or
// structured object); ctx.Locals carries the resolved locals; state.Recipe
// is replaced with a local copy whose Spec.Source carries the resolved
// value (engines downstream consume the resolved form via state.Recipe).
func phaseB0(ctx *types.TemplateContext, state *PhaseBState, providesForNode types.OrderedMap[string]) error {
	if state.Recipe == nil {
		state.PhaseReached = 0
		return nil
	}

	resolvedLocals, err := EvaluateRecipeLocals(
		state.Recipe, state.NodeID, ctx.Values, ctx.Infra, providesForNode,
	)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("Phase B.0 node %q locals", state.NodeID))
	}
	ctx.Locals = resolvedLocals

	if state.Recipe.Spec.Source != nil {
		resolvedSource, err := EvaluateRecipeSource(
			state.Recipe.Spec.Source, state.NodeID, ctx.Values, ctx.Infra, providesForNode,
		)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("Phase B.0 node %q source", state.NodeID))
		}
		// Replace state.Recipe with a local copy carrying the resolved
		// source; engines later read state.Recipe.Spec.Source for the
		// final value.
		local := *state.Recipe
		local.Spec.Source = resolvedSource
		state.Recipe = &local
		ctx.Source = resolvedSource
	}

	// Trace points: B.0 source eval (slot 05) and B.0 locals eval (slot 06).
	// Slot 05 fires only when Recipe.Spec.Source is non-nil (a sourceless
	// Recipe has no source-eval to capture); slot 06 fires unconditionally —
	// phaseB0 always evaluates locals, even into an empty map.
	if state.Trace != nil {
		if state.Recipe.Spec.Source != nil {
			_ = state.Trace.Write(trace.PassPhaseB0, state.NodeID, state.BoundaryPath, "", trace.PillarSnapshot(ctx, trace.PassPhaseB0))
		}
		_ = state.Trace.Write(trace.PassPhaseB0Locals, state.NodeID, state.BoundaryPath, "", trace.PillarSnapshot(ctx, trace.PassPhaseB0Locals))
	}

	state.PhaseReached = 0
	return nil
}

// phaseB1 evaluates Recipe.Spec.ValuesMapping (or ValuesMappingFile) as a
// Go template against the post–Phase-A inject pipe. Populates
// state.ResolvedValues. The values dictionary is what the provider engine
// consumes via engine.Render in the sourced and sourceless rendering paths.
//
// Per chapter 16's strict reading, valuesMapping is meaningful only when
// source is also declared (rule 1). The legacy sourceless-helm path
// remains supported until the synthesised-mode replacement lands: rule 1
// is gated, so a Recipe with valuesMapping but no source is accepted at
// load time and reaches B.1 with a non-empty ValuesMapping. B.1 evaluates
// it identically in both modes; the engine selects sourced vs sourceless
// rendering based on whether state.Recipe.Spec.Source is set.
func phaseB1(ctx *types.TemplateContext, state *PhaseBState) error {
	if state.Recipe == nil {
		state.PhaseReached = 1
		return nil
	}
	tmplText, vmErr := resolveValuesMappingText(state.Recipe.Spec.ValuesMapping, state.Recipe.Metadata.Name, "Recipe", state.MappingLibraries)
	if vmErr != nil {
		return vmErr
	}

	if tmplText == "" {
		// No valuesMapping declared — flatten the values pillar as the
		// engine input. Matches today's ExecutePhaseB behaviour.
		state.ResolvedValues = flattenOrderedMap(ctx.Values)
		state.PhaseReached = 1
		return nil
	}

	output, err := EvalTemplate(
		fmt.Sprintf("valuesMapping[%s]", state.Recipe.Metadata.Name),
		tmplText,
		ctx,
	)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("Phase B.1: recipe %q valuesMapping", state.Recipe.Metadata.Name))
	}

	var values map[string]any
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("Phase B.1: recipe %q valuesMapping output is not valid YAML", state.Recipe.Metadata.Name))
	}
	state.ResolvedValues = values
	// Trace point: B.1 valuesMapping output (slot 07).
	if state.Trace != nil {
		_ = state.Trace.Write(trace.PassPhaseB1, state.NodeID, state.BoundaryPath, "", trace.PillarSnapshot(ctx, trace.PassPhaseB1))
	}
	state.PhaseReached = 1
	return nil
}

// phaseB2 evaluates Recipe.Spec.Workload — inline content or the file
// referenced by its `$import:` directive — parses the rendered output via
// yaml.Unmarshal into a *types.Workload, then runs the two validators in
// order:
//
//  1. validator.ValidateWorkload — schema validation against the embedded
//     Workload JSON Schema.
//  2. validator.ValidateWorkloadSecrets — cross-wave check against the
//     Component's secretsSchema slot allowlist.
//
// On both pass: state.ParsedWorkload and ctx.Workload populate. Any failure
// stops the build before B.3 runs; failures carry CATALOG_ERROR-prefixed
// messages with field paths.
//
// slots is the Component's SecretsSchema (passed by the orchestrator from
// the catalog lookup). nil/empty slots is acceptable when the Recipe's
// Workload declares no secrets entries.
func phaseB2(ctx *types.TemplateContext, state *PhaseBState, slots types.OrderedMap[types.SecretSlot]) error {
	if state.Recipe == nil {
		state.PhaseReached = 2
		return nil
	}

	workloadText, wlErr := resolveWorkloadText(state.Recipe.Spec.Workload, state.Recipe.Metadata.Name)
	if wlErr != nil {
		return wlErr
	}
	if workloadText == "" {
		state.PhaseReached = 2
		return nil
	}

	output, err := EvalTemplate(
		fmt.Sprintf("workload[%s]", state.Recipe.Metadata.Name),
		workloadText,
		ctx,
	)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("Phase B.2: recipe %q workload template", state.Recipe.Metadata.Name))
	}

	var w types.Workload
	if err := parse.TemplateOutput([]byte(output), &w); err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("Phase B.2: recipe %q workload yaml.Unmarshal", state.Recipe.Metadata.Name))
	}

	if err := validator.ValidateWorkload(&w, state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}
	if err := validator.ValidateWorkloadSecrets(&w, slots, state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}

	state.ParsedWorkload = &w
	ctx.Workload = &w
	// Trace point: B.2 parsed workload (slot 08).
	if state.Trace != nil {
		_ = state.Trace.Write(trace.PassPhaseB2, state.NodeID, state.BoundaryPath, "", trace.PillarSnapshot(ctx, trace.PassPhaseB2))
	}
	state.PhaseReached = 2
	return nil
}

// phaseB3 evaluates Recipe.Spec.Artifacts — each entry is either inline
// content or a file referenced by its `$import:` directive — the third
// input kind per chapter 16. Each emitted file carries its own EmitOrigin
// tag (artifacts-inline / artifacts-import) so the Phase B.5
// cross-origin collision detector can name every contributor on a clash.
//
// The provider's engine governs file routing: BoundaryAssembler engines
// (compose, helm-chart) receive intermediates that AssembleBoundary merges
// per-(path, provider); flat engines (helm, terraform, cloudformation)
// receive standalone files at the dispatch path. helm-chart inline-content
// artifacts: entries are rejected with a CATALOG_ERROR; $import:-loaded
// entries are a B.3 no-op for helm-chart (provider-specific pre-template
// injection lives outside Phase B).
func phaseB3(ctx context.Context, tmplCtx *types.TemplateContext, state *PhaseBState, engine Engine, nodeCtx NodeRenderContext) error {
	if state.Recipe == nil {
		state.PhaseReached = 3
		return nil
	}
	if state.Recipe.Spec.Artifacts.Len() == 0 {
		state.PhaseReached = 3
		return nil
	}
	files, err := evaluateArtifacts(ctx, tmplCtx, state, engine, nodeCtx)
	if err != nil {
		return err
	}
	state.EmittedFiles = append(state.EmittedFiles, files...)
	// Trace point: per-emitted-artifact (slot 09). One trace event per file
	// emitted by phaseB3, named by the original filename. Only files tagged
	// with an artifacts origin are captured here; phaseB4 (synthesis) is the
	// site of slot 10's per-file trace.
	if state.Trace != nil {
		for _, f := range files {
			if f.Origin != EmitOriginArtifactsInline && f.Origin != EmitOriginArtifactsImport {
				continue
			}
			_ = state.Trace.Write(trace.PassPhaseB3, state.NodeID, state.BoundaryPath, f.Name, trace.PillarSnapshot(tmplCtx, trace.PassPhaseB3))
		}
	}
	state.PhaseReached = 3
	return nil
}

// phaseB4 dispatches to the provider's WorkloadSynthesizer when
// state.ParsedWorkload != nil. Engines that don't implement the interface
// produce a CATALOG_ERROR via dispatchWorkloadSynthesis. Sourced-mode
// Recipes (no parsed Workload) are a no-op at B.4 — their files are
// produced by engine.Render at the orchestrator level.
func phaseB4(ctx context.Context, tmplCtx *types.TemplateContext, state *PhaseBState, engine Engine, nodeCtx NodeRenderContext) error {
	if state.ParsedWorkload == nil {
		state.PhaseReached = 4
		return nil
	}
	providerName := ""
	if state.Recipe != nil {
		providerName = state.Recipe.Spec.Provider
	}
	files, err := dispatchWorkloadSynthesis(ctx, engine, tmplCtx, state.ParsedWorkload, nodeCtx, providerName)
	if err != nil {
		return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("Phase B.4 node %q", state.NodeID))
	}
	state.EmittedFiles = append(state.EmittedFiles, files...)
	// Trace point: per-synthesised-file (slot 10). One trace event per file
	// emitted by phaseB4, named by the original filename. Filter by origin:
	// EmitOriginWorkload (workload-mode synthesis) and EmitOriginSource
	// (rare; sourced-and-workload coexistence) are the slot's contributors.
	if state.Trace != nil {
		for _, f := range files {
			if f.Origin != EmitOriginWorkload && f.Origin != EmitOriginSource {
				continue
			}
			_ = state.Trace.Write(trace.PassPhaseB4, state.NodeID, state.BoundaryPath, f.Name, trace.PillarSnapshot(tmplCtx, trace.PassPhaseB4))
		}
	}
	state.PhaseReached = 4
	return nil
}

// phaseB5 detects filename collisions across input-kind origins on
// state.EmittedFiles. Cross-origin collisions produce a
// RecipeValidationError{Rule: "7"} (the reserved rule-7 sentinel format).
// Within-origin collisions are caught by other rules (rule 6 at load time
// for duplicate artifacts: entry keys; engine-internal logic for duplicate
// sourced output).
//
// Implementation lives in phase_b_collision.go's detectFilenameCollisions.
func phaseB5(state *PhaseBState) error {
	if err := detectFilenameCollisions(state); err != nil {
		return err
	}
	state.PhaseReached = 5
	return nil
}

// phaseB6 runs FeatureRecipe Resource Generation per the existing
// mechanism (chapter 16; the prior semantics are preserved verbatim —
// only re-positioned in the sub-pass sequence). Two inner loops:
//
//  1. Non-credential FRs: walks featureRefs filtered by
//     isResourceGeneratingFR; per FR, evaluates locals + valuesMapping in
//     the FR-scoped context, dispatches to the FR's own provider engine,
//     emits files under "features/<instance>/" prefix.
//
//  2. Credential FRs: walks secretsForNode (graph.Spec.Secrets[nodeID]);
//     per secret, populates the FR-scoped context with .secret_name +
//     secret-specific .inject, dispatches similarly, emits files under
//     "secrets/<name>/" prefix.
//
// Returns a map of prefixed paths (e.g. "features/mon/deployment.yaml")
// to file contents. The orchestrator merges these into the per-node files
// map alongside the source/workload outputs.
func phaseB6(
	ctx context.Context,
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	featureRefs []FeatureRecipeRef,
	engineFactory EngineFactory,
	allProvides NodeProvides,
	secretsForNode types.OrderedMap[types.IRSecretEntry],
	recipeLookup RecipeLookup,
	nodeCtx NodeRenderContext,
	boundaryFRState *BoundaryFRState,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	input RendererInput,
) (map[string][]byte, error) {
	out := map[string][]byte{}
	nodeID := state.NodeID

	// Non-credential FR Resource Generation. For each attached FR that
	// carries source or valuesMapping (resource-generating, i.e. not
	// inject-only), in Onion Model order: evaluate FR locals as templates,
	// build feature-scoped context, evaluate FR valuesMapping, dispatch to
	// the engine named by the FR's own provider, emit under
	// features/<instance>/.
	//
	// Boundary-scope FRs (spec.executionScope: boundary) take a different
	// dispatch path: dedup by (boundary_path, fr_instance), evaluate against
	// the boundary-scope TemplateContext (.boundary.* pillars instead of
	// per-component .context.*), and route output via boundaryFRState for
	// post-loop assembly. See boundary_fr.go for the full lifecycle.
	for _, ref := range featureRefs {
		fr := ref.FeatureRecipe
		if !isResourceGeneratingFR(fr) {
			continue
		}

		if fr.Spec.ExecutionScope == "boundary" && boundaryFRState != nil {
			// Walk up from the component's boundary path to find the
			// ANCESTOR boundary that holds this FR-instance attachment
			// (the IR builder's Tier-4 propagation may have inherited
			// the feature from a parent boundary; the dedup key is the
			// owning boundary, not the component's containing boundary).
			owningBP := findFRAttachmentBoundary(graph.Spec.BoundaryFeatures, state.BoundaryPath, ref.InstanceName)
			key := boundaryFRKey{BoundaryPath: owningBP, FRInstance: ref.InstanceName}
			if boundaryFRState.Dedup[key] {
				continue // FR already ran for this boundary
			}
			boundaryFRState.Dedup[key] = true
			if err := dispatchBoundaryScopeFR(
				ctx, fr, ref.InstanceName, key, state, boundaryFRState,
				nodeCtx, engineFactory, graph, release, boundaryAnnotations, allProvides, input,
			); err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q boundary-scope feature %q", nodeID, ref.InstanceName))
			}
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
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q feature %q locals", nodeID, ref.InstanceName))
		}
		frCtx.Locals = mergeFeatureLocals(tmplCtx.Locals, frEvaluatedLocals)

		frValues, err := ExecutePhaseBForFeature(&frCtx, fr, input.MappingLibraries)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q feature %q", nodeID, ref.InstanceName))
		}

		frEngine, err := engineFactory(fr.Spec.Provider)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q feature %q engine factory", nodeID, ref.InstanceName))
		}

		frNodeCtx := nodeCtx
		frNodeCtx.OriginalSource = fr.Spec.Source
		frInfra := map[string]any{}
		frSrcValues := map[string]any{}
		if hasFeat {
			frInfra = feat.Infra.TemplateAlphabeticalMapDeep()
			frSrcValues = feat.Values.TemplateAlphabeticalMapDeep()
		}
		resolvedSrc, err := resolveEntitySource(input, fr.Spec.Source, fr.Spec.Locals, frSourceURI(fr), frSrcValues, frInfra)
		if err != nil {
			return nil, wrapSourceResolutionError(err, nodeID, fmt.Sprintf("Step 5 node %q feature %q source resolution", nodeID, ref.InstanceName))
		}
		frNodeCtx.Source = resolvedSrc

		frFiles, err := frEngine.Render(ctx, frValues, frNodeCtx)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q feature %q render", nodeID, ref.InstanceName))
		}

		prefix := "features/" + ref.InstanceName + "/"
		for p, c := range frFiles {
			out[prefix+p] = c
		}
	}

	// Credential FR Resource Generation. Secrets live in graph.Spec.Secrets[nodeID]
	// (IR partitions credential features here via guardCredentialPartition).
	// This sub-pass renders ONLY resource-generating credential FRs (those with a
	// source or valuesMapping — e.g. a Kubernetes Secret manifest), mirroring the
	// non-credential path's isResourceGeneratingFR guard above. Inject-only
	// credential FRs (injectMapping but no source/workload — e.g. a dotenv
	// polyfill) contribute through Phase A and are skipped here; rendering them
	// would have nothing to emit. For each resource-generating secret, build a
	// feature-scoped context with .secret_name + top-level .inject populated and
	// render via the FR's own provider engine. Output files under secrets/<name>/.
	if secretsForNode.Len() > 0 && recipeLookup.FeatureRecipe != nil {
		secretNames := secretsForNode.Keys()
		sort.Strings(secretNames)
		for _, secretName := range secretNames {
			entry, _ := secretsForNode.Get(secretName)
			fr, found := recipeLookup.FeatureRecipe(secretName, entry.Type)
			if !found {
				continue
			}
			if !isResourceGeneratingFR(fr) {
				// Inject-only credential FR: Phase A owns its inject merge; B.6
				// renders nothing for it.
				continue
			}

			frCtx := *tmplCtx // shallow copy
			frCtx.Values = entry.Values
			frCtx.Infra = entry.Infra

			frEvaluatedLocals, err := EvaluateFeatureRecipeLocals(
				fr, secretName, frCtx.Values, frCtx.Infra, allProvides[nodeID],
			)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q secret %q locals", nodeID, secretName))
			}
			frCtx.Locals = mergeFeatureLocals(tmplCtx.Locals, frEvaluatedLocals)
			frCtx.SecretName = secretName
			frCtx.Inject = buildSecretInject(entry)

			frValues, err := ExecutePhaseBForFeature(&frCtx, fr, input.MappingLibraries)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q secret %q", nodeID, secretName))
			}

			frEngine, err := engineFactory(fr.Spec.Provider)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q secret %q engine factory", nodeID, secretName))
			}

			frNodeCtx := nodeCtx
			frNodeCtx.OriginalSource = fr.Spec.Source
			resolvedSrc, err := resolveEntitySource(input, fr.Spec.Source, fr.Spec.Locals, frSourceURI(fr), entry.Values.TemplateAlphabeticalMapDeep(), entry.Infra.TemplateAlphabeticalMapDeep())
			if err != nil {
				return nil, wrapSourceResolutionError(err, nodeID, fmt.Sprintf("Step 5 node %q secret %q source resolution", nodeID, secretName))
			}
			frNodeCtx.Source = resolvedSrc

			frFiles, err := frEngine.Render(ctx, frValues, frNodeCtx)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("Step 5 node %q secret %q render", nodeID, secretName))
			}

			prefix := "secrets/" + secretName + "/"
			for p, c := range frFiles {
				out[prefix+p] = c
			}
		}
	}

	state.PhaseReached = 6
	return out, nil
}
