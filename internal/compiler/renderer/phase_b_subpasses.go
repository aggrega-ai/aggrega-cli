// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Phase B sub-pass functions per chapter 16 §5.1. The orchestrator threads a
// single PhaseBState accumulator through B.0 → B.6 in order; each function
// reads from and writes to the state, mutates the TemplateContext where
// the per-pass availability matrix specifies, and propagates errors.
//
// Phase 1 of WI04 authors these sub-pass functions but does NOT wire them
// into the orchestrator yet. ExecutePhaseB at phase_b.go remains the
// orchestrator's call site until Phase 2's orchestrator-rewire moves to
// RunPhaseBPipeline.

package renderer

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
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
		return fmt.Errorf("Phase B.0 node %q locals: %w", state.NodeID, err)
	}
	ctx.Locals = resolvedLocals

	if state.Recipe.Spec.Source != nil {
		resolvedSource, err := EvaluateRecipeSource(
			state.Recipe.Spec.Source, state.NodeID, ctx.Values, ctx.Infra, providesForNode,
		)
		if err != nil {
			return fmt.Errorf("Phase B.0 node %q source: %w", state.NodeID, err)
		}
		// Replace state.Recipe with a local copy carrying the resolved
		// source; engines later read state.Recipe.Spec.Source for the
		// final value.
		local := *state.Recipe
		local.Spec.Source = resolvedSource
		state.Recipe = &local
		ctx.Source = resolvedSource
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
	if state.Recipe.Spec.ValuesMappingJsonnet != "" {
		return fmt.Errorf("%w: recipe %q uses valuesMappingJsonnet", ErrJsonnetNotSupported, state.Recipe.Metadata.Name)
	}

	tmplText := state.Recipe.Spec.ValuesMapping
	if tmplText == "" && state.Recipe.Spec.ValuesMappingFile != "" {
		data, err := os.ReadFile(state.Recipe.Spec.ValuesMappingFile)
		if err != nil {
			return fmt.Errorf("Phase B.1: recipe %q valuesMappingFile %q: %w",
				state.Recipe.Metadata.Name, state.Recipe.Spec.ValuesMappingFile, err)
		}
		tmplText = string(data)
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
		return fmt.Errorf("Phase B.1: recipe %q valuesMapping: %w", state.Recipe.Metadata.Name, err)
	}

	var values map[string]interface{}
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return fmt.Errorf("Phase B.1: recipe %q valuesMapping output is not valid YAML: %w",
			state.Recipe.Metadata.Name, err)
	}
	state.ResolvedValues = values
	state.PhaseReached = 1
	return nil
}

// phaseB2 evaluates Recipe.Spec.Workload (inline) or reads + evaluates
// Recipe.Spec.WorkloadFrom (file), parses the rendered output via
// yaml.Unmarshal into a *types.Workload, then runs the two validators
// per Path B (operator-approved per WI03 closure):
//
//  1. validator.ValidateWorkload — schema unification against the embedded
//     #Workload CUE.
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

	workloadText := state.Recipe.Spec.Workload
	if workloadText == "" && state.Recipe.Spec.WorkloadFrom != "" {
		data, err := os.ReadFile(state.Recipe.Spec.WorkloadFrom)
		if err != nil {
			return fmt.Errorf("Phase B.2: recipe %q workloadFrom %q: %w",
				state.Recipe.Metadata.Name, state.Recipe.Spec.WorkloadFrom, err)
		}
		workloadText = string(data)
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
		return fmt.Errorf("Phase B.2: recipe %q workload template: %w", state.Recipe.Metadata.Name, err)
	}

	var w types.Workload
	if err := parse.TemplateOutput([]byte(output), &w); err != nil {
		return fmt.Errorf("Phase B.2: recipe %q workload yaml.Unmarshal: %w", state.Recipe.Metadata.Name, err)
	}

	if err := validator.ValidateWorkload(&w, state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}
	if err := validator.ValidateWorkloadSecrets(&w, slots, state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}

	state.ParsedWorkload = &w
	ctx.Workload = &w
	state.PhaseReached = 2
	return nil
}

// phaseB3 evaluates Recipe.Spec.Artifacts (inline) and
// Recipe.Spec.ArtifactsFrom (file-loaded), the third input kind per
// chapter 16. Each emitted file carries its own EmitOrigin tag
// (artifacts-inline / artifacts-from-file) so the Phase B.5 cross-origin
// collision detector can name every contributor on a clash.
//
// The provider's engine governs file routing: ReleaseComposer engines
// (compose) receive intermediates that ComposeRelease merges at Release
// end; flat engines (helm, terraform, cloudformation) receive standalone
// files under boundaries/<bp>/. helm-chart inline artifacts: is rejected
// with a CATALOG_ERROR; helm-chart artifactsFrom: is a B.3 no-op
// (provider-specific pre-template injection lives outside Phase B).
func phaseB3(ctx context.Context, tmplCtx *types.TemplateContext, state *PhaseBState, engine Engine, nodeCtx NodeRenderContext) error {
	if state.Recipe == nil {
		state.PhaseReached = 3
		return nil
	}
	if state.Recipe.Spec.Artifacts.Len() == 0 && len(state.Recipe.Spec.ArtifactsFrom) == 0 {
		state.PhaseReached = 3
		return nil
	}
	files, err := evaluateArtifacts(ctx, tmplCtx, state, engine, nodeCtx)
	if err != nil {
		return err
	}
	state.EmittedFiles = append(state.EmittedFiles, files...)
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
		return fmt.Errorf("Phase B.4 node %q: %w", state.NodeID, err)
	}
	state.EmittedFiles = append(state.EmittedFiles, files...)
	state.PhaseReached = 4
	return nil
}

// phaseB5 detects filename collisions across input-kind origins on
// state.EmittedFiles. Cross-origin collisions produce a
// RecipeValidationError{Rule: "7"} per WI02's reserved sentinel format.
// Within-origin collisions are caught by other rules (rule 6 at load time
// for artifacts vs artifactsFrom; engine-internal logic for duplicate
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
) (map[string][]byte, error) {
	out := map[string][]byte{}
	nodeID := state.NodeID

	// Non-credential FR Resource Generation. For each attached FR that
	// carries source or valuesMapping (resource-generating, i.e. not
	// inject-only), in Onion Model order: evaluate FR locals as templates,
	// build feature-scoped context, evaluate FR valuesMapping, dispatch to
	// the engine named by the FR's own provider, emit under
	// features/<instance>/.
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
			out[prefix+p] = c
		}
	}

	// Credential FR rendering. Secrets live in graph.Spec.Secrets[nodeID]
	// (IR partitions credential features here via guardCredentialPartition).
	// For each secret, resolve the FR, build a feature-scoped context with
	// .secret_name + top-level .inject populated, render via the FR's own
	// provider engine. Output files under secrets/<name>/.
	if secretsForNode.Len() > 0 && recipeLookup.FeatureRecipe != nil {
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
				out[prefix+p] = c
			}
		}
	}

	state.PhaseReached = 6
	return out, nil
}
