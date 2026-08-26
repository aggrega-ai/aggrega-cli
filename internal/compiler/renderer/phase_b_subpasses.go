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
	"errors"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// phaseB0 publishes the node's resolved locals onto the render context and
// evaluates Recipe.Spec.Source as a Go template against the self-contained context
// (instance_id / values / infra / locals; no inject / uses access and no provides).
// Populates ctx.Locals and ctx.Source on success.
//
// localsForNode is the node's locals as Pass 1 resolved them, ahead of that node's
// provides. They arrive resolved rather than being evaluated here so that the
// values a provides template read, the values an assertion sees, and the values a
// rendered artifact carries all come from one evaluation.
//
// The ORDER of the two steps is load-bearing: the locals are published onto the
// context before the source template is evaluated, so a source template reading
// .locals receives resolved values. Reversing the two would deliver an empty
// pillar, and because a locals reference is checked against what the pillar
// carries, it would report every such reference as naming a key the block does not
// declare.
//
// On entry: ctx.Source is nil (sub-pass availability matrix invariant).
// On exit:  ctx.Source carries the resolved source value (string URI or
// structured object); ctx.Locals carries the resolved locals; state.Recipe
// is replaced with a local copy whose Spec.Source carries the resolved
// value (engines downstream consume the resolved form via state.Recipe).
func phaseB0(ctx *types.TemplateContext, state *PhaseBState, localsForNode types.OrderedMap[string]) error {
	if state.Recipe == nil {
		state.PhaseReached = 0
		return nil
	}

	ctx.Locals = localsForNode

	// A source that is PRESENT and yet declares nothing is the authoring
	// ambiguity, and it is refused here because this is the first point in the
	// render sequence that holds the value as the author wrote it: everything
	// downstream reads the source after its own template has been evaluated, and
	// everything downstream that treats an empty source as no source at all would
	// take the reading the author never asked for. The condition is derived from
	// the same predicate every reader uses, so the refusal and the readings cannot
	// come apart.
	if state.Recipe.Spec.Source != nil && !sourceValueDeclared(state.Recipe.Spec.Source) {
		return cerrors.Wrap(ErrEmptySource(), cerrors.CatalogError,
			cerrors.SourceLocation{File: state.RecipePath},
			cerrors.ComposeMessage(ErrEmptySource(), []string{
				fmt.Sprintf("Recipe %q", state.Recipe.Metadata.Name),
				fmt.Sprintf("node %q", state.NodeID),
			}, "omit the source field for sourceless mode, or name a non-empty path or URI for sourced mode"))
	}

	// Read once, ahead of the evaluation that replaces the Recipe with a local
	// copy carrying the resolved source. Both the evaluation below and the trace
	// capture beneath it ask the same question of the same document, and reading
	// it twice around a step that rewrites the value asks it of two different
	// values — the second reading would then answer about the RESULT of the
	// evaluation rather than about whether there was one to run.
	sourceDeclared := recipeSourceDeclared(state.Recipe)

	if sourceDeclared {
		// The locals were published onto the context immediately above, so the source
		// template reads the same resolved values every other reader of this node's
		// locals reads.
		resolvedSource, err := EvaluateRecipeSource(
			state.Recipe.Spec.Source, state.NodeID, ctx.Values, ctx.Infra, ctx.Locals,
		)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("node %q source", state.NodeID))
		}
		// Replace state.Recipe with a local copy carrying the resolved
		// source; engines later read state.Recipe.Spec.Source for the
		// final value.
		local := *state.Recipe
		local.Spec.Source = resolvedSource
		state.Recipe = &local
		ctx.Source = resolvedSource
	}

	// Trace points: the source evaluation and the locals publication. The source
	// point fires only where there was a source to evaluate (a sourceless Recipe
	// has no source-eval to capture); the locals point fires unconditionally —
	// every node reaching here publishes its locals, even an empty map.
	if sourceDeclared {
		state.capture(trace.PassPhaseB0, "", ctx)
	}
	state.capture(trace.PassPhaseB0Locals, "", ctx)

	state.PhaseReached = 0
	return nil
}

// phaseB1 evaluates Recipe.Spec.ValuesMapping as a Go template against the
// post–Phase-A inject pipe, populating state.ResolvedValues — the values
// dictionary the provider engine consumes via engine.Render in the sourced
// render path. valuesMapping requires a source (the values-mapping rule,
// enforced at load), so a Recipe reaching B.1 with a non-empty ValuesMapping
// always also declares a source.
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
		// This branch is the ONLY one whose resolved keys are the authored leaf
		// paths, so it is the only one that can record the fact. Recorded here
		// rather than re-answered downstream: reading the Recipe's values mapping
		// a second time to reach the same conclusion would be two readings of one
		// declaration, free to disagree once either moves.
		state.ValuesAreDeclaredLeaves = true
		state.PhaseReached = 1
		return nil
	}

	output, err := EvalTemplate(
		fmt.Sprintf("valuesMapping[%s]", state.Recipe.Metadata.Name),
		tmplText,
		ctx,
	)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q valuesMapping", state.Recipe.Metadata.Name))
	}

	var values map[string]any
	if err := parse.TemplateOutput([]byte(output), &values); err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q valuesMapping output is not valid YAML", state.Recipe.Metadata.Name))
	}
	state.ResolvedValues = values
	// Trace point: B.1 valuesMapping output (slot 07).
	state.capture(trace.PassPhaseB1, "", ctx)
	state.PhaseReached = 1
	return nil
}

// phaseB2 evaluates Recipe.Spec.Workload — inline content or the file
// referenced by its `$import:` directive — parses the rendered output via
// yaml.Unmarshal into a *types.Workload, then runs the validators in order:
//
//  1. validator.ValidateWorkload — schema validation against the embedded
//     Workload JSON Schema.
//  2. validator.ValidateWorkloadAttachments — cross-entity check against the
//     Component's featuresSchema slot allowlist.
//  3. validator.ValidateWorkloadResourceReferences — every attachments entry's
//     slot must expose the provides keys (name, key) the synthesizer reads to
//     resolve the reference; checked against the slot's resolved provides on
//     the template context (ctx.Features) — the same bag the synthesizer reads,
//     so the base and FeatureRecipe paths check uniformly.
//
// On all pass: state.ParsedWorkload and ctx.Workload populate. Any failure
// stops the build before B.3 runs; failures carry CATALOG_ERROR-prefixed
// messages with field paths.
//
// slots is the Component's FeaturesSchema (passed by the orchestrator from
// the catalog lookup). nil/empty slots is acceptable when the Recipe's
// Workload declares no attachments entries.
func phaseB2(ctx *types.TemplateContext, state *PhaseBState, slots types.OrderedMap[types.AttachmentSlot]) error {
	if state.Recipe == nil {
		state.PhaseReached = 2
		return nil
	}

	workloadText, wlErr := resolveWorkloadText(state.Recipe.Spec.Workload, state.Recipe.Metadata.Name, state.WorkloadLibraries)
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
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q workload template", state.Recipe.Metadata.Name))
	}

	var w types.Workload
	if err := parse.TemplateOutput([]byte(output), &w); err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q workload template output is not valid YAML", state.Recipe.Metadata.Name))
	}

	// Both checks run, and the near-duplicate is deliberate: they validate two
	// different instances against one schema and reject disjoint classes. The
	// rendered bytes carry keys the typed model does not name, which the decode
	// discards, so only the bytes check can see a stray or misspelled key. The
	// decoded value elides a field whose value is the zero value, so only the
	// typed check can see a schema-required field the document supplied empty.
	// Deleting either loses a rejection class the other cannot observe.
	if err := validator.ValidateWorkloadBytes([]byte(output), state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}
	if err := validator.ValidateWorkload(&w, state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}
	if err := validator.ValidateWorkloadAttachments(&w, slots, state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}
	// Referenceability reads each slot's resolved provides from the template
	// context — the same bag the synthesizer reads to render the reference — so
	// the base and FeatureRecipe workload paths (the latter renders against a
	// shallow copy of this context) are checked uniformly.
	if err := validator.ValidateWorkloadResourceReferences(&w, AttachmentProvidesFromContext(ctx), state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
		return err
	}

	// What a display SHOWS in place of this document is decided HERE, where the
	// template's text is already resolved. An imported workload's text is not on
	// the Recipe, so resolving it anywhere upstream to answer the same question
	// would be two resolutions of one text and two chances to disagree. This
	// sub-pass runs before the first pass that projects .workload, so the display
	// is settled before anything could show it.
	//
	// The re-render is built against the context as the real evaluation saw it,
	// which is why it runs before ctx.Workload is set: a template reading
	// .workload would otherwise see a document the real render did not have.
	if trace.Captures(state.Trace) && workloadReadsWithheld(workloadText, state.Redact) {
		if shown, produced := renderWorkloadForDisplay(state.Recipe.Metadata.Name, workloadText, ctx, state.Redact); produced {
			state.DisplayWorkload = shown
			state.Redact.Workload = redact.WorkloadSubstituted
		} else {
			state.Redact.Workload = redact.WorkloadUnavailable
		}
	}

	state.ParsedWorkload = &w
	ctx.Workload = &w
	// Trace point: B.2 parsed workload (slot 08).
	state.capture(trace.PassPhaseB2, "", ctx)
	state.PhaseReached = 2
	return nil
}

// phaseB3 projects Recipe.Spec.Artifacts — each entry is either inline content
// or a file referenced by its `$import:` directive — the third input kind per
// chapter 16. The host builds an ArtifactsProjection (the $import read, the
// template parse pre-check, the Go-template evaluation, and the build-provenance key
// computation — the generic, invariant-forced work) and dispatches it to the
// provider's ArtifactsEmitter, which decides the output format, the provenance
// placement, the output path, and the final bytes. Each returned file carries
// its own EmitOrigin tag (artifacts-inline / artifacts-import) so the Phase B.5
// cross-origin collision detector can name every contributor on a clash.
//
// An engine that does not implement ArtifactsEmitter, while the Recipe declares
// artifacts:, produces a CATALOG_ERROR — mirroring the WorkloadSynthesizer gate
// at B.4.
func phaseB3(ctx context.Context, tmplCtx *types.TemplateContext, state *PhaseBState, engine Provider, nodeCtx NodeRenderContext) error {
	if state.Recipe == nil {
		state.PhaseReached = 3
		return nil
	}
	if state.Recipe.Spec.Artifacts.Len() == 0 {
		state.PhaseReached = 3
		return nil
	}
	emitter, ok := engine.(ArtifactsEmitter)
	if !ok {
		return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath},
			fmt.Sprintf("provider %q does not implement artifacts emission; the Recipe declares artifacts: but the bound provider has no artifacts-emission implementation", state.Recipe.Spec.Capability),
			nil, nil)
	}
	leftDelim, rightDelim := emitter.ArtifactTemplateDelimiters()
	proj, err := buildArtifactsProjection(ctx.Err, tmplCtx, state, nodeCtx, leftDelim, rightDelim)
	if err != nil {
		return err
	}
	files, err := emitter.EmitArtifacts(ctx, proj, nodeCtx)
	if err != nil {
		return err
	}
	state.EmittedFiles = append(state.EmittedFiles, files...)
	// Trace point: per-emitted-artifact (slot 09). One trace event per file
	// emitted by phaseB3, named by the original filename. Only files tagged
	// with an artifacts origin are captured here; phaseB4 (synthesis) is the
	// site of slot 10's per-file trace.
	for _, f := range files {
		if f.Origin != EmitOriginArtifactsInline && f.Origin != EmitOriginArtifactsImport {
			continue
		}
		state.capture(trace.PassPhaseB3, f.Name, tmplCtx)
	}
	state.PhaseReached = 3
	return nil
}

// phaseB4 dispatches to the provider's WorkloadSynthesizer when
// state.ParsedWorkload != nil. Engines that don't implement the interface
// produce a CATALOG_ERROR via dispatchWorkloadSynthesis. Sourced-mode
// Recipes (no parsed Workload) are a no-op at B.4 — their files are
// produced by engine.Render at the orchestrator level.
func phaseB4(ctx context.Context, tmplCtx *types.TemplateContext, state *PhaseBState, engine Provider, nodeCtx NodeRenderContext) error {
	if state.ParsedWorkload == nil {
		state.PhaseReached = 4
		return nil
	}
	providerName := ""
	if state.Recipe != nil {
		providerName = state.Recipe.Spec.Capability
	}
	files, err := dispatchWorkloadSynthesis(ctx, engine, tmplCtx, state.ParsedWorkload, nodeCtx, providerName)
	if err != nil {
		return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("node %q workload synthesis", state.NodeID))
	}
	state.EmittedFiles = append(state.EmittedFiles, files...)
	// Trace point: per-synthesised-file (slot 10). One trace event per file
	// emitted by phaseB4, named by the original filename. Filter by origin:
	// EmitOriginWorkload (workload-mode synthesis) and EmitOriginSource
	// (rare; sourced-and-workload coexistence) are the slot's contributors.
	for _, f := range files {
		if f.Origin != EmitOriginWorkload && f.Origin != EmitOriginSource {
			continue
		}
		state.capture(trace.PassPhaseB4, f.Name, tmplCtx)
	}
	state.PhaseReached = 4
	return nil
}

// phaseB5 detects filename collisions across input-kind origins on
// state.EmittedFiles. Cross-origin collisions produce a filename-collision
// validation failure (the reserved sentinel format). Within-origin collisions
// are caught elsewhere: duplicate artifacts: entry keys are refused when the
// document is decoded, and engine-internal logic covers duplicate sourced
// output.
//
// Implementation lives in phase_b_collision.go's detectFilenameCollisions.
func phaseB5(state *PhaseBState) error {
	if err := detectFilenameCollisions(state); err != nil {
		return err
	}
	state.PhaseReached = 5
	return nil
}

// phaseB6 runs FeatureRecipe Resource Generation (chapter 16). It walks ONE
// sequence of resource-generating attachments and renders every one of them
// through the same path: derive the attachment-scoped context, evaluate the
// FeatureRecipe's locals and valuesMapping against it, dispatch to the engine
// named by the FR's own provider, run the FR's pipeline when it declares one,
// and emit the FR's files under the attachment's output prefix.
//
// attachmentRenderSequence assembles that sequence from the node's two IR
// attachment buckets — features in FeatureOrder, then credentials in author
// order — applying the isResourceGeneratingFR guard uniformly: an inject-only
// FeatureRecipe contributes through Phase A and emits nothing here, whichever
// bucket it came from. What varies between attachments is DATA on the queued
// item (its context pillars, the delivery pipe a credential populates, its
// output prefix), never a second render path.
//
// A FeatureRecipe declaring spec.executionScope: boundary takes the
// boundary-scope dispatch instead: dedup by (boundary_path, fr_instance),
// evaluate against the boundary-scope TemplateContext (.boundary.* pillars in
// place of the per-component .context.*), and route output via boundaryFRState
// for post-loop assembly. See boundary_fr.go for the full lifecycle. The scope
// is read from the FeatureRecipe, so it governs every attachment identically.
//
// Returns a map of prefixed paths (e.g. "features/mon/deployment.yaml")
// to file contents (the FR's own generated files), plus a second map of
// host-document modifications keyed by their original emitted path. A
// component-scope FeatureRecipe's pipeline may /host PATCH the host
// component's existing documents or /host EMIT new ones; both surface in the
// hostPatched map. The orchestrator merges out and hostPatched into the host
// node's dispatched file map via the per-path merge conduit — the host docs
// were snapshotted before this sub-pass ran, so write-back must flow through
// the return value, not by mutating state.EmittedFiles (inert post-snapshot).
//
// hostOwn is the host component's own emitted documents (the cross-entity
// surface a component-scope FR pipeline reads as /host). hostIsAssembler
// reports whether the host's provider renders pre-assembly fragments; a /host
// PATCH against an assembler host halts (post-assembly phase territory).
// hostProvider is the host component's recipe provider — the provider rendering
// the /host documents, against which a host-scope pipeline step's
// targets is enforced. renderCtx is the render-time context every
// pipeline step receives.
func phaseB6(
	ctx context.Context,
	tmplCtx *types.TemplateContext,
	state *PhaseBState,
	featureRefs []FeatureRecipeRef,
	engineFactory EngineFactory,
	allProvides NodeProvides,
	recipeLookup RecipeLookup,
	nodeCtx NodeRenderContext,
	boundaryFRState *BoundaryFRState,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	input RendererInput,
	hostOwn []EmittedFile,
	hostIsAssembler bool,
	hostProvider string,
	renderCtx renderctx.RenderContext,
) (map[string][]byte, map[string][]byte, []DeferredPatch, error) {
	out := map[string][]byte{}
	hostPatched := map[string][]byte{}
	var deferred []DeferredPatch
	nodeID := state.NodeID

	// Running host document set threaded across the attachment sequence: each
	// component-scope FR's pipeline sees the host documents as modified by the
	// FRs that ran before it (the Onion-order cascade), so a later FR patches on
	// top of an earlier FR's host changes. Non-intermediate host docs only —
	// intermediate fragments are not discrete /host targets.
	runningHost := nonIntermediateFiles(hostOwn)

	// The node's attachment entries supply the pillar the render context does
	// not carry — the authored delivery pipe. Order comes from featureRefs (the
	// cascade order the IR recorded); this map supplies the per-attachment data.
	var nodeAttachments types.OrderedMap[types.IRFeatureEntry]
	if graph != nil {
		nodeAttachments, _ = graph.Spec.FeatureValuesMapping.Get(nodeID)
	}

	// ONE attachment render loop over ONE sequence. The sequence carries every
	// resource-generating attachment the node holds; the per-attachment
	// differences travel as data on the item, so the render path below exists
	// exactly once.
	for seqIndex, item := range attachmentRenderSequence(tmplCtx, featureRefs, nodeAttachments, state.AttachmentProvides) {
		fr := item.FR

		if fr.Spec.ExecutionScope == executionScopeBoundary && boundaryFRState != nil {
			// Walk up from the component's boundary path to find the
			// ANCESTOR boundary that holds this FR-instance attachment
			// (the IR builder's Tier-4 propagation may have inherited
			// the attachment from a parent boundary; the dedup key is the
			// owning boundary, not the component's containing boundary).
			owningBP := findFRAttachmentBoundary(graph.Spec.BoundaryFeatures, state.BoundaryPath, item.Instance)
			key := boundaryFRKey{BoundaryPath: owningBP, FRInstance: item.Instance}
			if boundaryFRState.Dedup[key] {
				continue // FR already ran for this boundary
			}
			boundaryFRState.Dedup[key] = true
			if err := dispatchBoundaryScopeFR(
				ctx, fr, item.Instance, key, state, boundaryFRState,
				nodeCtx, engineFactory, graph, release, boundaryAnnotations, allProvides, input,
			); err != nil {
				return nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q boundary-scope attachment %q", nodeID, item.Instance))
			}
			continue
		}

		frCtx := attachmentFRContext(tmplCtx, item.Instance, item.CtxValues, item.CtxInfra)

		frEvaluatedLocals, err := EvaluateFeatureRecipeLocals(
			fr, item.Instance, nodeID, frCtx.Values, frCtx.Infra,
		)
		if err != nil {
			return nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment %q locals", nodeID, item.Instance))
		}
		frCtx.Locals = mergeFeatureLocals(tmplCtx.Locals, frEvaluatedLocals)
		// The delivery pipe and the resolved-fill provides are populated only
		// for an attachment whose family carries them; both land after the
		// locals evaluation, which reads neither.
		if item.Inject != nil {
			frCtx.Inject = item.Inject
		}
		if item.Provides != nil {
			frCtx.Provides = *item.Provides
		}

		frValues, err := ExecutePhaseBForFeature(&frCtx, fr, input.MappingLibraries)
		if err != nil {
			return nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment %q", nodeID, item.Instance))
		}

		frEngine, err := engineFactory(fr.Spec.Capability)
		if err != nil {
			return nil, nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment %q engine factory", nodeID, item.Instance))
		}

		frNodeCtx := nodeCtx
		frNodeCtx.OriginalSource = fr.Spec.Source
		resolvedSrc, err := resolveEntitySource(input, fr.Spec.Source, fr.Spec.Locals, frSourceURI(fr), item.OwnValues, item.OwnInfra, fr.Spec.SourceDigests)
		if err != nil {
			return nil, nil, nil, wrapSourceResolutionError(err, nodeID, fmt.Sprintf("node %q attachment %q source resolution", nodeID, item.Instance))
		}
		frNodeCtx.Source = resolvedSrc

		// Return the helper error directly: its internal errors (the central
		// gate's uniform capability diagnostic, engine.Render failures, and the
		// Phase B sub-pass CATALOG_ERRORs) already carry their own context, and
		// re-wrapping would bury the gate diagnostic the user needs to see.
		// The carrier is (this node, this attachment). Both coordinates are needed:
		// two attachments on one node carry independently authored documents, and
		// nothing makes one's output names distinct from the other's or from the
		// node's own. It carries this render's presentation too, so the rule declared
		// over the carrier's facts can be asked inside the walk that records them.
		frFiles, frComponentID, err := renderFeatureRecipeFiles(ctx, frEngine, fr, &frCtx, frNodeCtx, frValues, resolvedSrc, item.Instance,
			state.hydrated.attachmentOnNode(state.hydratedFacts, nodeID, item.Instance),
			featureAttachmentSlots(fr, input), input.WorkloadLibraries, input.ArtifactsLibraries)
		if err != nil {
			return nil, nil, nil, err
		}
		// The label value that render derived joins this node's own declaration
		// set, which is where its dispatch group reads every declaration this node
		// is responsible for. Declared as each attachment's render returns, one
		// declaration per render, which is the unit the check's within-one-
		// declaration half is defined over.
		//
		// The coverage that buys, stated because an unstated bound reads as a full
		// one: every contributor's declarations reach the group under that
		// contributor's own name, so two attachments on DIFFERENT nodes reaching
		// one label value are two declarers and halt, as do an attachment and a
		// boundary-scope feature. Two attachments on the SAME node reaching one
		// label value are one declarer restating a name across two of its own
		// renders, which is the shape this check does not refuse.
		if frComponentID != "" {
			state.DerivedKeyRepeats = append(state.DerivedKeyRepeats,
				declareDerivedKey(&state.DerivedKeys, componentIDKeySpace, frComponentID)...)
		}

		// Component-scope FeatureRecipe pipeline. Every attachment's
		// FeatureRecipe runs its pipeline here — the pipeline is a property of
		// the FeatureRecipe, so a credential can transform the host component's
		// rendered documents on invitation exactly as an operational attachment
		// can.
		//
		// Flat host (the common case): own + host run together per-node. own is
		// the FR's own generated files; host is the running host-document set
		// (the host component's own docs, modified by FRs that ran earlier in
		// the sequence), so a /host PATCH rewrites a host doc and threads forward
		// for a later FR to see.
		//
		// Assembler host (compose): the host's per-node output is pre-assembly
		// fragments — assembly merges them into one document only later, so there
		// is no discrete host document for the guest to content-select per-node.
		// The host-scope pipeline is therefore DEFERRED whole to the
		// post-assembly phase, which replays it against the merged document. The
		// FR's own output (its artifacts / synthesized resources) is already
		// rendered above; only its pipeline's host work defers.
		//
		// Own-scope-on-assembler via a component-scope FeatureRecipe: a
		// FeatureRecipe's own output is emitted under the attachment's own output
		// prefix as discrete files — it is NOT routed to a BoundaryAssembler (only a
		// component's own render output is), so there is no merged document for a
		// post-assembly /own replay to target. An /own PATCH against an
		// FR-rendered assembler fragment therefore stays a per-node concern and
		// trips the executor's own-fragment guard loudly (correct: there is
		// genuinely no discrete document and nothing to defer to). The replayable
		// own-on-assembler case is a COMPONENT Recipe whose own provider is an
		// assembler — handled at the orchestrator, whose own render output IS
		// assembled. So this branch defers only the host scope.
		if len(fr.Spec.Pipeline) > 0 {
			if hostIsAssembler {
				// Record the FR's pipeline for post-assembly replay. The host
				// node and provider are stamped here; the orchestrator adds the
				// host-node author-order key. HostProvider drives the host-scope
				// codec on replay; OwnProvider is empty — this record carries no
				// replayable own scope (see above). An own-scope step in such a
				// record has no merged document to address, and the rule over an
				// attachment's own-scope steps against a merging component reports
				// it before this render reaches here.
				deferred = append(deferred, DeferredPatch{
					Steps:             fr.Spec.Pipeline,
					WithCtx:           &frCtx,
					RenderCtx:         renderCtx,
					HostProvider:      hostProvider,
					NodeID:            nodeID,
					FRInstance:        item.Instance,
					FeatureOrderIndex: seqIndex,
				})
			} else {
				// own provider is the FR's own engine kind (version pin stripped —
				// the codec keys on the kind); host provider is the host component's
				// recipe engine kind (threaded in from the orchestrator).
				pres, perr := runNodePipeline(ctx, fr.Spec.Pipeline, frFiles, runningHost, hostIsAssembler, ProviderEngineKind(fr.Spec.Capability), hostProvider, renderCtx, &frCtx, input.YAMLIndent, input.PluginPathLookup, engineFactory, nodeID)
				if perr != nil {
					return nil, nil, nil, perr
				}
				frFiles = pres.Own
				state.Warnings = append(state.Warnings, pres.Warnings...)
				for path, content := range pres.HostPatched {
					hostPatched[path] = content
					runningHost = applyHostPatchToRunning(runningHost, path, content)
				}
			}
		}

		for _, f := range frFiles {
			out[item.Prefix+f.Name] = f.Body
		}
	}

	state.PhaseReached = 6
	return out, hostPatched, deferred, nil
}

// attachmentRenderItem is one resource-generating attachment queued for the
// Phase B.6 render loop. Everything that differs between attachment families
// is a field here, so the loop that consumes the sequence has one body:
//
//   - CtxValues / CtxInfra are the .values / .infra pillars the attachment's
//     render context carries.
//   - OwnValues / OwnInfra are the attachment's own pillars flattened for the
//     source resolver's template substitution. They are empty when the
//     attachment contributes no pillars of its own, in which case the context
//     pillars above fall back to the host component's.
//   - Inject is the delivery pipe the attachment publishes on its own .inject
//     pillar; nil when the attachment authored none.
//   - Provides, when non-nil, replaces the inherited .provides pillar with the
//     attachment's own resolved provides.
//   - Prefix is the attachment's output prefix, applied to every file its
//     FeatureRecipe emits.
type attachmentRenderItem struct {
	Instance  string
	FR        *types.FeatureRecipe
	CtxValues types.OrderedMap[any]
	CtxInfra  types.OrderedMap[any]
	OwnValues map[string]any
	OwnInfra  map[string]any
	Inject    *types.InjectPipe
	Provides  *types.OrderedMap[string]
	Prefix    string
}

// attachmentRenderSequence assembles the one ordered sequence Phase B.6
// renders: every attachment on the node whose bound FeatureRecipe is
// resource-generating (it declares a source, a workload, artifacts or a
// valuesMapping). An inject-only FeatureRecipe is excluded — Phase A owns its
// inject merge and it has nothing to emit here.
//
// The order is the refs' order, which is the cascade order the IR recorded —
// the same order Phase A walks — so both phases see one ordering rule and there
// is no second sequence for an attachment family to be appended to.
//
// Every item carries its authored delivery pipe on .inject and its own resolved
// provides (computed once before B.4, the same bag the consumer reads through
// the attachment pillar) as its FeatureRecipe's .provides — so a valuesMapping
// deriving an emitted resource's name from .provides renders the exact name the
// consumer read, from one evaluation with no drift.
func attachmentRenderSequence(
	tmplCtx *types.TemplateContext,
	featureRefs []FeatureRecipeRef,
	attachments types.OrderedMap[types.IRFeatureEntry],
	attachmentProvides map[string]types.OrderedMap[string],
) []attachmentRenderItem {
	seq := make([]attachmentRenderItem, 0, len(featureRefs))

	for _, ref := range featureRefs {
		if !isResourceGeneratingFR(ref.FeatureRecipe) {
			continue
		}
		item := attachmentRenderItem{
			Instance:  ref.InstanceName,
			FR:        ref.FeatureRecipe,
			CtxValues: tmplCtx.Values,
			CtxInfra:  tmplCtx.Infra,
			OwnValues: map[string]any{},
			OwnInfra:  map[string]any{},
			Prefix:    "features/" + ref.InstanceName + "/",
		}
		if attach, ok := tmplCtx.Features.Get(ref.InstanceName); ok {
			item.CtxValues, item.CtxInfra = attach.Values, attach.Infra
			item.OwnValues = attach.Values.TemplateAlphabeticalMapDeep()
			item.OwnInfra = attach.Infra.TemplateAlphabeticalMapDeep()
		}
		if entry, ok := attachments.Get(ref.InstanceName); ok && entry.Inject.Len() > 0 {
			item.Inject = buildAttachmentInject(entry)
		}
		if provides, ok := attachmentProvides[ref.InstanceName]; ok {
			item.Provides = &provides
		}
		seq = append(seq, item)
	}
	return seq
}

// attachmentFRContext derives an attachment FeatureRecipe's render context from
// the host node's context: a shallow copy whose values and infra pillars are the
// attachment's own, and which names the attachment instance being rendered.
//
// It is the only site that names an attachment instance on the render context,
// so every attachment family reads that name from one place and the spelling
// cannot diverge between families. Callers overwrite the remaining per-family
// pillars (locals, and the delivery pipe where a family populates one) after
// this returns.
func attachmentFRContext(tmplCtx *types.TemplateContext, instanceName string, values, infra types.OrderedMap[any]) types.TemplateContext {
	frCtx := *tmplCtx // shallow copy; callers overwrite the per-family pillars
	frCtx.Values = values
	frCtx.Infra = infra
	frCtx.AttachmentName = instanceName
	return frCtx
}

// nonIntermediateFiles returns the emitted files that are not pre-assembly
// fragments — the discrete documents a /host pipeline operation may target.
func nonIntermediateFiles(files []EmittedFile) []EmittedFile {
	out := make([]EmittedFile, 0, len(files))
	for _, f := range files {
		if f.Intermediate {
			continue
		}
		out = append(out, f)
	}
	return out
}

// applyHostPatchToRunning rewrites the running host set with a host-document
// modification so a later FeatureRecipe in FeatureOrder sees the change. An
// existing file's bytes are replaced in place (position preserved); a /host
// EMIT of a new file is appended.
func applyHostPatchToRunning(running []EmittedFile, path string, content []byte) []EmittedFile {
	for i := range running {
		if running[i].Name == path {
			running[i].Body = content
			return running
		}
	}
	return append(running, EmittedFile{Name: path, Origin: EmitOriginPipeline, Body: content})
}

// featureAttachmentSlots returns the featuresSchema slots of the Feature the
// given FeatureRecipe implements — the owning entity whose slot allowlist
// governs an FR-synthesized Workload's containers.attachments cross-check
// (parallel to a Component governing its own Recipe). Returns an empty map when
// the Feature declares no slots or cannot be resolved.
func featureAttachmentSlots(fr *types.FeatureRecipe, input RendererInput) types.OrderedMap[types.AttachmentSlot] {
	if fr != nil && input.CatalogLookup.Feature != nil {
		if feat, found := input.CatalogLookup.Feature(fr.Spec.Implements); found {
			return feat.Spec.FeaturesSchema
		}
	}
	return types.OrderedMap[types.AttachmentSlot]{}
}

// renderFeatureRecipeFiles runs the full input-kind render for a resource-
// generating FeatureRecipe at a per-component site (feature or credential
// attachment). It mirrors the component render path exactly: the central
// input-kind gate, then sourced engine.Render / synthesised workload synthesis
// / artifacts emission via the same Phase B leaf passes (phaseB2/B3/B4) and the
// same Phase B.5 cross-input-kind filename-collision check. A render-only
// synthetic Recipe carries the FR's input-kind fields so those passes — which
// read state.Recipe.Spec.* — operate unchanged; this is reuse of the component
// machinery, not a parallel FeatureRecipe track. Returns the accumulated
// EmittedFiles (each carrying its Origin and Intermediate flag); the per-
// component callers flatten them under features/<instance>/, while the
// boundary-scope caller routes them by the Intermediate flag.
//
// The second return value is the component-identity label value this render
// derived from instanceLabel, for the caller to declare into its own
// derived-name accumulator — the caller holds that accumulator and this function
// does not, and the value is reported rather than re-derived at the caller so
// the derivation itself stays at one site. It is empty for a render that derived
// none: a caller supplying no instance label, and a render whose only crossing
// is the sourced one, which reads the unmodified context and so never puts the
// derived value on anything emitted.
//
// hydrated is the carrier this render records its mid-render state through, and
// the carrier whose declared rule is asked about each artifact entry it projects.
// It is supplied by the caller because the carrier is the caller's own knowledge
// and cannot be recovered here: this function is reached from a per-component site
// and from a boundary-scope one, and the node identity it is handed is the host
// component's on the first and a composite dispatch marker on the second. A nil
// carrier records nothing and asks nothing.
func renderFeatureRecipeFiles(
	ctx context.Context,
	frEngine Provider,
	fr *types.FeatureRecipe,
	frCtx *types.TemplateContext,
	frNodeCtx NodeRenderContext,
	frValues map[string]any,
	resolvedSrc any,
	instanceLabel string,
	hydrated attachmentCarrier,
	slots types.OrderedMap[types.AttachmentSlot],
	workloadLibs map[string]types.WorkloadLibrary,
	artifactsLibs map[string]types.ArtifactsLibrary,
) ([]EmittedFile, string, error) {
	hasSource := sourceValueDeclared(resolvedSrc)
	hasWorkload := !fr.Spec.Workload.IsEmpty()
	hasArtifacts := fr.Spec.Artifacts.Len() > 0

	if err := gateInputKind(frNodeCtx.NodeID, fr.Spec.Capability, frEngine, hasSource, hasWorkload, hasArtifacts); err != nil {
		return nil, "", err
	}

	// Advertised-only per-node input validation for the FeatureRecipe render —
	// the same gate the component render path applies, on the same seat (after the
	// capability gate, before any Phase B pass). frNodeCtx carries the node's
	// warning sink (inherited from the host component context, or wired by the
	// boundary-scope caller), so an FR-render warning surfaces through the same
	// channel a component-render warning does.
	if validator, ok := frEngine.(NodeInputValidator); ok {
		diags, verr := validator.ValidateNodeInput(ctx, frValues, frNodeCtx)
		if verr != nil {
			return nil, "", cerrors.Wrap(verr, cerrors.TranslationError, cerrors.SourceLocation{File: frNodeCtx.NodeID}, "feature recipe input validation")
		}
		if err := routeNodeInputDiagnostics(frNodeCtx.NodeID, fr.Spec.Capability, diags, frNodeCtx.WarningSink); err != nil {
			return nil, "", err
		}
	}

	// The synthesised and artifacts paths name their resources after the
	// feature instance, not the host component whose NodeID the FR node carries
	// — otherwise an FR-synthesised Deployment would be named for the host and
	// collide with the host's own workload. We set LabelComponentID (consumed by
	// ProvenanceComponentID inside the synthesizer and the artifacts provenance
	// builder) on a copy used only for B.3/B.4; the sourced engine.Render runs
	// against the unmodified frNodeCtx so existing sourced-FR provenance is
	// unchanged.
	//
	// That label value is a name the compiler derived, so it is reported to the
	// caller for the injectivity check the names a guest declares go through: two
	// attachments whose instances reach one label value would otherwise hand the
	// same provenance to two different sets of resources, with nothing in the
	// emitted output recording that two were meant. It is reported only where a
	// crossing that READS this copy runs — the artifacts placement and the
	// workload synthesis below. A render whose only crossing is the sourced one
	// reads the unmodified context above, so the value derived here reaches
	// nothing emitted, and offering it would invite a refusal over a name in no
	// output.
	synthNodeCtx := frNodeCtx
	derivedComponentID := ""
	if instanceLabel != "" {
		synthNodeCtx.LabelComponentID = labels.SanitizeForK8sLabelValue(instanceLabel)
		if hasWorkload || hasArtifacts {
			derivedComponentID = synthNodeCtx.LabelComponentID
		}
	}

	synth := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: fr.Metadata.Name, Version: fr.Metadata.Version},
		Spec: types.RecipeSpec{
			Capability:      fr.Spec.Capability,
			Implements:      fr.Spec.Implements,
			Source:          resolvedSrc,
			ValuesMapping:   fr.Spec.ValuesMapping,
			Workload:        fr.Spec.Workload,
			Artifacts:       fr.Spec.Artifacts,
			ArtifactDigests: fr.Spec.ArtifactDigests,
		},
	}
	state := NewPhaseBState(synth, frNodeCtx.NodeID, frNodeCtx.BoundaryPath, "")
	// The carrier this render records against and is checked under. It is set
	// instead of the per-node retention a component render carries, never beside it:
	// the passes below walk the same producers under the node identity above, so
	// recording through the node retention would file this attachment's values under
	// the host component — and the rule reading them would answer from the host's
	// pillars while reading this FeatureRecipe's template.
	state.attachmentHydrated = hydrated
	// The FR workload flows through the same phaseB2 resolveWorkloadText as a
	// base Recipe; a workload library `$import` resolves against this set. The
	// base path sets the same field on its node state, sourced identically from
	// the render input — single source, no second lib map.
	state.WorkloadLibraries = workloadLibs
	// The FR artifacts flow through the same phaseB3 buildArtifactsProjection as a
	// base Recipe; an artifacts library `$import` resolves against this set,
	// sourced identically from the render input — single source, no second map.
	state.ArtifactsLibraries = artifactsLibs

	// Sourced mode hits engine.Render; synthesised renders at B.4, artifacts at
	// B.3. EmitOriginSource files accumulate by sorted filename for
	// determinism. The Intermediate flag follows the same rule the component
	// path uses: a BoundaryAssembler that does not place its own artifacts
	// consumes Render output as fragment input.
	if hasSource {
		files, err := frEngine.Render(ctx, frValues, frNodeCtx)
		if err != nil {
			return nil, "", cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: frNodeCtx.NodeID}, "feature recipe render")
		}
		renderIsIntermediate := RenderIsIntermediate(frEngine)
		// Accumulate by sorted path for deterministic EmittedFile order, carrying
		// each file's producer-declared format onto the EmittedFile.
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		for _, f := range files {
			state.EmittedFiles = append(state.EmittedFiles, EmittedFile{Name: f.Path, Origin: EmitOriginSource, Body: f.Content, Intermediate: renderIsIntermediate, Format: f.Format})
		}
	}

	if err := phaseB2(frCtx, state, slots); err != nil {
		return nil, "", err
	}
	// Reduce this render's mid-render state to the projection that outlives it, at
	// the position the component walk records its own: the synthetic Recipe above
	// carries the resolved source, the workload has now been parsed, and no
	// artifact template has been evaluated — which is the state each recorded value
	// names.
	state.recordAttachmentPillars()
	if err := phaseB3(ctx, frCtx, state, frEngine, synthNodeCtx); err != nil {
		// The artifacts pass is the one crossing that hands the provider's own
		// error back untouched, so the category is assigned here or nowhere: the
		// callers of this function return its error unchanged by design. An
		// artifacts fault is normally a catalog fault — the entry, its import or
		// its template is what went wrong. A provider that rendered
		// non-deterministically is the exception, and it carries the same
		// translation category here that it carries on this function's source-
		// render and workload-synthesis crossings. The condition fixes the
		// category; the crossing it surfaced through does not. The component
		// render path reads its own artifacts crossing the same way, and the two
		// are deliberately separate expressions of that reading: each path names
		// its own crossing in the message it adds, so neither depends on the
		// other's wrapping.
		category := cerrors.CatalogError
		if errors.Is(err, ErrRenderNondeterministic()) {
			category = cerrors.TranslationError
		}
		return nil, "", cerrors.Wrap(err, category, cerrors.SourceLocation{File: frNodeCtx.NodeID}, "feature recipe artifacts")
	}
	if err := phaseB4(ctx, frCtx, state, frEngine, synthNodeCtx); err != nil {
		return nil, "", err
	}
	if err := phaseB5(state); err != nil {
		return nil, "", err
	}

	return state.EmittedFiles, derivedComponentID, nil
}
