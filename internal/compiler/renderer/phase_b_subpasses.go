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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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

	// Read once, ahead of the evaluation that replaces the Recipe with a local
	// copy carrying the resolved source. Both the evaluation below and the trace
	// capture beneath it ask the same question of the same document, and reading
	// it twice around a step that rewrites the value asks it of two different
	// values — the second reading would then answer about the RESULT of the
	// evaluation rather than about whether there was one to run.
	sourceDeclared := recipeSourceDeclared(state.Recipe)

	if sourceDeclared {
		// Read the authored value before the evaluation below replaces it: the
		// anchoring beneath asks what the AUTHOR wrote, and the two are the same
		// value read either side of one step that rewrites it.
		authored := state.Recipe.Spec.Source

		// The locals were published onto the context immediately above, so the source
		// template reads the same resolved values every other reader of this node's
		// locals reads.
		resolvedSource, err := EvaluateRecipeSource(
			state.Recipe.Spec.Source, state.NodeID, ctx.Values, ctx.Infra, ctx.Locals, state.Withheld,
		)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("node %q source", state.NodeID))
		}

		// What a relative source produced by a template is resolved against is
		// anchorSourceToDocument's answer, which every template-evaluated source
		// surface shares — see there for why the produced value decides its own
		// treatment.
		resolvedSource, err = anchorSourceToDocument(authored, resolvedSource, state.RecipePath)
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

	var values map[string]any
	_, err, decodeErr := EvalTemplateDecoded(
		fmt.Sprintf("valuesMapping[%s]", state.Recipe.Metadata.Name),
		tmplText,
		ctx,
		state.Withheld,
		&values,
	)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q valuesMapping", state.Recipe.Metadata.Name))
	}
	if decodeErr != nil {
		return cerrors.Wrap(decodeErr, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q valuesMapping output is not valid YAML", state.Recipe.Metadata.Name))
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
//  2. validator.FoldWorkloadConnections — every entry naming a connection is
//     folded onto the slot name the compiler composes for that connection's
//     attachment, refusing a connection the component does not draw and a slot
//     its socket does not declare. Everything below reads the folded Workload.
//  3. validator.ValidateWorkloadAttachments — cross-entity check against the
//     slot allowlist: the Component's featuresSchema, and each connection's
//     socket slots under their composed names.
//  4. validator.ValidateWorkloadResourceReferences — every attachments entry's
//     slot must expose the provides keys (name, key) the synthesizer reads to
//     resolve the reference; checked against the slot's resolved provides on
//     the template context — the same bag the synthesizer reads, so the base
//     and FeatureRecipe paths check uniformly.
//
// On all pass: state.ParsedWorkload and ctx.Workload populate with the folded
// Workload. Any failure stops the build before B.3 runs; failures carry
// CATALOG_ERROR-prefixed messages with field paths.
//
// slots carries the owner's own slots (the Component's FeaturesSchema, or the
// implemented Feature's for an attachment's FeatureRecipe) and the connections
// the component draws with their sockets' slots. An empty value is acceptable
// when the Recipe's Workload names no slot.
func phaseB2(ctx *types.TemplateContext, state *PhaseBState, slots validator.WorkloadSlots) error {
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

	var w types.Workload
	output, err, decodeErr := EvalTemplateDecoded(
		fmt.Sprintf("workload[%s]", state.Recipe.Metadata.Name),
		workloadText,
		ctx,
		state.Withheld,
		&w,
	)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q workload template", state.Recipe.Metadata.Name))
	}
	if decodeErr != nil {
		return cerrors.Wrap(decodeErr, cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath}, fmt.Sprintf("recipe %q workload template output is not valid YAML", state.Recipe.Metadata.Name))
	}

	// Whether the document the checks below judge was produced from a value an
	// author declared sensitive, asked of the template's references before any
	// check runs and over the declarations as read — never over the capture's set,
	// whose escape hatch must not change what a failure says. A rendered document's
	// fields carry no link back to the data the template read, so which of its
	// values came from a declared datum cannot be told field by field; where the
	// template read any, every finding a check reports is worded without the value
	// it found. That is the fail-safe direction: a finding at a position no
	// declared datum reached loses its value text too.
	var validation []validator.WorkloadValidationOption
	if workloadReadsWithheld(workloadText, state.Withheld) {
		validation = append(validation, validator.WithholdRenderedValues())
	}

	// Both checks run, and the near-duplicate is deliberate: they validate two
	// different instances against one schema and reject disjoint classes. The
	// rendered bytes carry keys the typed model does not name, which the decode
	// discards, so only the bytes check can see a stray or misspelled key. The
	// decoded value elides a field whose value is the zero value, so only the
	// typed check can see a schema-required field the document supplied empty.
	// Deleting either loses a rejection class the other cannot observe.
	if err := validator.ValidateWorkloadBytes([]byte(output), state.RecipePath, state.RecipeName, state.RecipeVersion, validation...); err != nil {
		return err
	}
	if err := validator.ValidateWorkload(&w, state.RecipePath, state.RecipeName, state.RecipeVersion, validation...); err != nil {
		return err
	}
	// The fold runs before every check that reads a slot, so those checks — and
	// the provider that resolves the folded Workload — read an entry naming a
	// connection's slot exactly as they read one naming the Component's own.
	checked, err := validator.FoldWorkloadConnections(&w, slots, state.RecipePath, state.RecipeName, state.RecipeVersion)
	if err != nil {
		return err
	}
	if err := validator.ValidateWorkloadAttachments(&w, checked, state.RecipePath, state.RecipeName, state.RecipeVersion); err != nil {
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
		if shown, produced := renderWorkloadForDisplay(state.Recipe.Metadata.Name, workloadText, ctx, state.Redact, slots); produced {
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
// its own EmitOrigin tag (artifacts-inline / artifacts-import) so the
// filename-collision rule can name every contributor on a clash.
//
// An engine that does not implement ArtifactsEmitter, while the Recipe declares
// artifacts:, produces a CATALOG_ERROR. That is not a capability check: which
// input kinds a provider may be handed is settled once per node, before any render
// dispatch, by the central input-kind gate reading the provider's declared
// contract, and no provider this host ships can fail the assertion anyway — the
// single provider adapter the host renders through implements ArtifactsEmitter by
// compile-time assertion whatever its guest declares. The branch is kept loud
// because the alternative is emitting nothing for a declared artifacts: block and
// reporting success.
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
		return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: state.RecipePath},
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
	// Trace point: per-synthesized-file (slot 10). One trace event per file
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

// phaseB6 runs FeatureRecipe Resource Generation (chapter 16). It walks ONE
// sequence of attachments and renders every one of them through the same path:
// derive the attachment-scoped context, evaluate the FeatureRecipe's locals and
// valuesMapping against it, dispatch to the engine named by the FR's own
// provider, run the FR's pipeline when it declares one, and emit the FR's files
// under the attachment's output prefix.
//
// attachmentRenderSequence assembles that sequence from the node's single IR
// attachment bucket, in the cascade order the IR recorded — every attachment the
// node holds, credential-handling ones included — applying the
// participatesInRenderSequence guard uniformly: an
// attachment is admitted when its FeatureRecipe generates a resource of its own
// or declares a pipeline, whichever bucket it came from. An attachment doing
// neither emits nothing here — an inject-only FeatureRecipe contributes through
// Phase A, a pure-wiring one through the uses-edge its Feature carries. What
// varies between attachments is DATA on the queued item (its context pillars,
// the delivery pipe a credential populates, its output prefix), never a second
// render path.
//
// An attachment admitted for its pipeline alone renders no files of its own: it
// carries no render input, so the render passes below emit nothing for it and
// its pipeline's whole effect is what it patches or emits into the host.
//
// The attachment-scoped context is also where the connections made TO the host
// appear, at .context.used_by, read from state.InboundEdges. They are carried on
// the templates that produce the attachment's own resources — its valuesMapping,
// its workload and its artifacts — and on none whose output can land in the host's
// own: Phase A's injectMapping writes onto the host's delivery pipe, and a pipeline
// step may rewrite the host's documents, so both read a context without them. That
// is what keeps the host's rendered bytes independent of who connects to it while
// an attached Feature that restricts or describes those connections can read them.
//
// A FeatureRecipe declaring spec.executionScope: boundary takes the
// boundary-scope dispatch instead: dedup by (boundary_path, fr_instance),
// evaluate against the boundary-scope TemplateContext (.boundary.* pillars in
// place of the per-component .context.*), and route output via boundaryFRState
// for post-loop assembly. See boundary_fr.go for the full lifecycle. The scope
// is read from the FeatureRecipe, so it governs every attachment identically.
//
// Returns a map of prefixed paths (e.g. "features/api/mon/deployment.yaml")
// to file contents (the FR's own generated files), plus a second map of
// host-document modifications keyed by their original emitted path. A
// component-scope FeatureRecipe's pipeline may /host PATCH the host
// component's existing documents or /host EMIT new ones; both surface in the
// hostPatched map. The orchestrator merges out and hostPatched into the host
// node's dispatched file map via the per-path merge conduit — the host docs
// were snapshotted before this sub-pass ran, so write-back must flow through
// the return value, not by mutating state.EmittedFiles (inert post-snapshot).
//
// The third return carries every attachment the orchestrator has to route as a
// contributor of the host's dispatch group, one entry per attachment. An entry
// carries either the output handed to that group's assembler or the position the
// output was written at instead, never both — and the output handed over is
// deliberately ABSENT from the path map above, because content handed to an
// assembler and also written beside the assembled document would be delivered
// twice. Which of the two an attachment's output takes needs both providers: the
// host's decides whether its group folds anything, and the attachment's decides
// whether what it rendered is in the shape that group's assembler reads. One
// answer covers the whole of an attachment's output, so the two cannot overlap.
//
// hostNestsContributors reports whether the host's dispatch group places each of
// its contributors under a position of its own. It selects which of the two
// placements an attachment's own output takes: where the group does, the
// attachment is a contributor with a position of its own and its files are
// carried out under their bare names to be keyed at it; where it does not, there
// is no position to be given, so the coordinates go into the filename and the
// files are written into the host's map here. It is a property of the dispatch
// group this node's output joins rather than of this loop, which the orchestrator
// settles around it.
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
	hostNestsContributors bool,
	renderCtx renderctx.RenderContext,
) (map[string][]byte, map[string][]byte, []attachmentContribution, []DeferredPatch, error) {
	out := map[string][]byte{}
	hostPatched := map[string][]byte{}
	var contributions []attachmentContribution
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

	// The platform annotations authored for the scope every component-scope
	// attachment below renders into. An attachment's output lands in the host
	// node's boundary directory, so the set that reaches it is the host node's own
	// merged set — read here from the ordered IR node rather than from the render
	// context's flattened copy, so the advisory stream each render raises is
	// byte-stable across runs of identical input.
	var nodeAnnotations types.OrderedMap[string]
	if graph != nil {
		if hostNode, ok := graph.Spec.Nodes.Get(nodeID); ok {
			nodeAnnotations = hostNode.ReleaseAnnotations
		}
	}

	// ONE attachment render loop over ONE sequence. The sequence carries every
	// attachment the node holds that has work here — a resource of its own to
	// generate, a pipeline to run, or both; the per-attachment differences travel
	// as data on the item, so the render path below exists exactly once.
	for seqIndex, item := range attachmentRenderSequence(tmplCtx, nodeID, featureRefs, nodeAttachments, state.AttachmentProvides) {
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
				nodeCtx, engineFactory, recipeLookup, graph, release, boundaryAnnotations, allProvides, input,
			); err != nil {
				return nil, nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q boundary-scope attachment %q", nodeID, item.Instance))
			}
			continue
		}

		frCtx := attachmentFRContext(tmplCtx, item.Instance, item.CtxValues, item.CtxInfra)
		// The connections made to the host, on this attachment's copy only. The copy
		// holds its own context block by value, so setting the field here leaves the
		// host's context — the one its own Recipe rendered against — without it.
		frCtx.Context.UsedBy = state.InboundEdges

		// What of this attachment's copy an author declared sensitive: the host's
		// set for every pillar carried over from the host, the attachment's own
		// Feature for the values and infra the copy carries in their place. Every
		// template this attachment evaluates below is withheld by it.
		_, pillarsFound := tmplCtx.AttachmentPillars(item.Instance)
		frWithheld := hostWithheld{
			host: state.Withheld,
			attachment: func(string) resolver.SensitiveLeaves {
				return declaredLeavesOf(input.declaredFeatureLeaves, item.FeatureType)
			},
		}
		frKeys := frWithheld.forAttachment(item.Instance, pillarsFound, fr.Spec.Locals)

		frEvaluatedLocals, err := EvaluateFeatureRecipeLocals(
			fr, item.Instance, nodeID, frCtx.Values, frCtx.Infra,
			frWithheld.leaves(item.Instance, pillarsFound),
		)
		if err != nil {
			return nil, nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment %q locals", nodeID, item.Instance))
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

		frValues, err := ExecutePhaseBForFeature(&frCtx, fr, input.MappingLibraries, frKeys)
		if err != nil {
			return nil, nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment %q", nodeID, item.Instance))
		}

		frEngine, err := engineFactory(fr.Spec.Capability)
		if err != nil {
			return nil, nil, nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q attachment %q: selecting its rendering plugin", nodeID, item.Instance))
		}

		frNodeCtx := nodeCtx
		// The copy above carries the HOST Component's sensitivity declarations,
		// and those govern the host's own rendered keys. They name nothing this
		// attachment renders, so a key of this render that merely shares a name
		// with a leaf the host declared would be marked from a declaration no
		// author wrote about it — and a consumer that begins withholding a value
		// has no way to discover that it should not have. Overwritten rather than
		// left inherited because a whole-struct copy names no field: nothing else
		// on this line says which declarations travelled with it.
		//
		// What replaces them is what this attachment's OWN Feature declares, so a
		// leaf its author declared sensitive is marked on its own render. The
		// assembly is not written out here: it is the same rule the boundary scope
		// applies, and two statements of it would agree on the day they were
		// written and be free to diverge afterwards.
		frNodeCtx.DeclaredSensitivity = featureRecipeDeclaredSensitivity(fr, item.FeatureType, input, frValues.KeysAreDeclaredLeaves)
		// This attachment is a contributor of its own, so the names its render
		// derives are declared under its own label rather than the host node's.
		// The sink is rewired for the same reason the boundary-scope render
		// rewires its own: the copied context's sink records against the node,
		// so a name this attachment's guest derived would be uncomparable
		// against a sibling attachment's — the two would arrive as one declarer,
		// and a collision between them would read as one contributor restating
		// itself, which the group check rules legitimate.
		//
		// Whether this attachment's output is handed to its host's dispatch group
		// takes BOTH coordinates, and each one alone answers a different question
		// wrongly.
		//
		// The HOST's provider decides whether the group folds its contributors into one
		// document at all — the group is keyed by the host's evaluated path and the
		// host's provider, so nothing the attachment declares changes what that group
		// does with what it holds. Reading the attachment's engine alone therefore
		// hands fragments to a group that folds nothing, and they are lost in silence,
		// because such a group never reads the map they would travel in.
		//
		// The ATTACHMENT's provider decides whether what it rendered is something that
		// group's assembler can read. An attachment names its own capability and need
		// not name the host's; an assembler consumes fragments in its own target's
		// shape, so a document rendered for a different target is not a contribution to
		// it but an unrelated file. Handing one over is lost just as silently — the
		// assembler parses it, finds none of the sections it models, and folds nothing.
		//
		// So the output is handed over only where the host's group folds AND this
		// attachment rendered through the same target that group assembles for.
		// Everything else writes under the attachment's own output prefix, which is
		// where it has always been written.
		//
		// It is settled before the render because the sink below closes over the
		// answer: a disposition settled after a crossing had already declared into the
		// sink would label that crossing's names with the wrong answer.
		//
		// 🔴 The disposition is stated, never inferred from the label. A
		// boundary-scope attachment carries a label of the same shape and reaches its
		// assembler by a different route — its own dispatch group, under its own
		// provider — so a reader keying on the shape would answer the same for both and
		// would be wrong for one of them.
		attachmentJoinsHostAssembly := hostIsAssembler &&
			ProviderEngineKind(fr.Spec.Capability) == hostProvider
		attachmentPlacedStandalone := !attachmentJoinsHostAssembly
		attachmentProducer := derivedKeyProducerFor(producerKindComponent, nodeID, item.Instance)
		attachmentContributor := derivedKeyContributorFor(attachmentProducer, attachmentPlacedStandalone)
		frNodeCtx.DerivedKeySink = func(declared types.OrderedMap[[]string]) {
			state.DerivedKeys.declare(attachmentContributor, declared)
		}
		// The document this attachment's FeatureRecipe was declared in: what a
		// relative source its template produces is resolved against, and what a
		// diagnostic raised inside its render names as its location. An entity
		// carrying no document on disk reports none, and both readers treat that as
		// the absence it is.
		frDocument := recipeLookup.featureRecipeDocument(fr)
		// The attachment's source template is evaluated before it is resolved, and
		// the order is the component path's own: what the render resolves is the
		// value the template produced, anchored to the document that declared it.
		// The locals were merged onto frCtx immediately above, so the template reads
		// the same resolved values every other template of this attachment reads.
		//
		// The identity and the instance name are the ones this attachment's locals
		// were evaluated with, passed through so the two surfaces publish one
		// denotation of .context.instance_id and .attachment_name.
		evaluatedSource, err := EvaluateFeatureRecipeSource(
			fr.Spec.Source, item.Instance, nodeID, frCtx.Values, frCtx.Infra, frCtx.Locals, frKeys,
		)
		if err != nil {
			return nil, nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: frDocument}, fmt.Sprintf("node %q attachment %q source", nodeID, item.Instance))
		}
		evaluatedSource, err = anchorSourceToDocument(fr.Spec.Source, evaluatedSource, frDocument)
		if err != nil {
			return nil, nil, nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: frDocument}, fmt.Sprintf("node %q attachment %q source", nodeID, item.Instance))
		}
		frNodeCtx.OriginalSource = evaluatedSource
		resolvedSrc, err := resolveEntitySource(input, evaluatedSource, fr.Spec.Locals, frSourceURI(fr), item.OwnValues, item.OwnInfra, frDocument)
		if err != nil {
			return nil, nil, nil, nil, wrapSourceResolutionError(err, nodeID, fmt.Sprintf("node %q attachment %q source resolution", nodeID, item.Instance))
		}
		frNodeCtx.Source = resolvedSrc
		frNodeCtx.SourceIgnore = localSourceIgnore(input, evaluatedSource, frDocument)

		// Return the helper error directly: its internal errors (the central
		// gate's uniform capability diagnostic, engine.Render failures, and the
		// Phase B sub-pass CATALOG_ERRORs) already carry their own context, and
		// re-wrapping would bury the gate diagnostic the user needs to see.
		// The owner is (this node, this attachment). Both coordinates are needed:
		// two attachments on one node carry independently authored documents, and
		// nothing makes one's output names distinct from the other's or from the
		// node's own. It carries this render's presentation too, so the rule declared
		// over the owner's facts can be asked inside the walk that records them.
		frFiles, frComponentID, err := renderFeatureRecipeFiles(ctx, frEngine, fr, &frCtx, frNodeCtx, frValues.Resolved, resolvedSrc, frDocument,
			input.RecordedPins.For(frDocument), item.Identity,
			ownerProductsOf(state).attachmentOnNode(nodeID, item.Instance),
			featureAttachmentSlots(fr, input), input.WorkloadLibraries, input.ArtifactLibraries,
			nodeAnnotations, input.CatalogLookup.ProviderPlugin, frKeys)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		// The label value that render derived joins this node's own declaration
		// set, which is where its dispatch group reads every declaration this node
		// is responsible for. Declared as each attachment's render returns, one
		// declaration per render, which is the unit the check's within-one-
		// declaration half is defined over.
		//
		// Declared under THIS ATTACHMENT's label rather than the host node's, which
		// is what the coverage rests on: every contributor's declarations reach the
		// group under its own label, so two attachments reaching one label value are
		// two declarers rather than one contributor restating itself. The attachment
		// key is a coordinate of the value being declared, since the value is derived
		// from it, so it is a coordinate of the label too.
		//
		// 🔴 The bound, because an unstated one reads as full coverage. This value
		// travels with the disposition above, and the disposition says the output it
		// labels is placed standalone — so on a provider that folds its group's
		// output into one document, two attachments reaching one label value are
		// compared only within themselves and do not halt. The requirement this value
		// carries is wider than one document: it is unique across contributors,
		// because two of them reaching one value hand one provenance to two different
		// sets of resources. It is reached only where a composed identity had to be
		// shortened, the composition being injective wherever it fits its target's
		// bound. An attachment and a boundary-scope feature are excluded by
		// construction rather than by this: their identities compose under different
		// scopes and can never meet.
		if frComponentID != "" {
			state.DerivedKeys.declareDerived(attachmentContributor, componentIDKeySpace, frComponentID)
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
			// The steps read this attachment's context without the connections made
			// to the host. A step may rewrite the host's own documents, and one that
			// could read who connects would make the host's rendered bytes depend on
			// it — the one dependency the host's render is kept free of. The copy is
			// taken after the render above, so the steps still see every pillar the
			// render resolved.
			pipelineCtx := frCtx
			pipelineCtx.Context.UsedBy = nil
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
					WithCtx:           &pipelineCtx,
					Withheld:          frKeys,
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
				pres, perr := runNodePipeline(ctx, fr.Spec.Pipeline, frFiles, runningHost, hostIsAssembler, ProviderEngineKind(fr.Spec.Capability), hostProvider, renderCtx, &pipelineCtx, frKeys, input.YAMLIndent, input.PluginPathLookup, engineFactory, nodeID)
				if perr != nil {
					return nil, nil, nil, nil, perr
				}
				frFiles = pres.Own
				state.Warnings = append(state.Warnings, pres.Warnings...)
				for path, content := range pres.HostPatched {
					hostPatched[path] = content
					runningHost = applyHostPatchToRunning(runningHost, path, content)
				}
			}
		}

		// Route this attachment's output by the disposition settled above, which is
		// the host's. Handed to the group, it is NOT written here; written here, it
		// lands under the attachment's own output prefix exactly where it has always
		// landed. The two arms are complements, so nothing is delivered twice and no
		// path moves.
		//
		// The whole of the attachment's output takes one arm rather than each file
		// taking its own, because the question is what the group does with what it
		// holds and the group answers once for this attachment. Splitting per file
		// would ask each rendered file a question only the pair of providers answers.
		var fragments []EmittedFile
		var ownFiles []EmittedFile
		if attachmentJoinsHostAssembly {
			fragments = frFiles
		} else if hostNestsContributors {
			// A host whose group places each contributor at a position of its own
			// places this attachment at one too — BESIDE the host rather than
			// inside it. So the files are carried out under their own names and
			// keyed under the attachment's own key, and the group composes the
			// position from the coordinates that identify it.
			//
			// Beside rather than within, because within is not a position the
			// host's target reads: a group's target discovers its contributors at
			// the positions the group gives them, and output sitting below one of
			// those positions belongs to that contributor rather than being a
			// contributor. Keying these files inside the host's map is what left
			// them unread — present in the output, addressed by nothing, with the
			// build reporting success.
			ownFiles = frFiles
		} else {
			// A group giving no contributor a position of its own has nowhere to
			// put this attachment, so its coordinates go into the FILENAME and its
			// files land beside the host's own, in the host's map, under the host's
			// key. The host's own entry is what names that position.
			outputPrefix := item.Identity.outputPrefix()
			for _, f := range frFiles {
				out[outputPrefix+f.Name] = f.Body
			}
		}
		if len(fragments) > 0 || len(ownFiles) > 0 {
			// The coordinates travel with the fragments rather than being recovered
			// downstream: the dispatch identity joins its parts with characters either
			// part may contain, so cutting it apart cannot return what went in. The
			// basis is the identity this attachment's render derived its own names
			// from, read through the one home of that fallback — so the name declared
			// while rendering and the name the assembled artifact addresses that
			// content by come from one identity rather than two that happen to agree.
			contributions = append(contributions, attachmentContribution{
				nodeID:    item.Identity.dispatchNodeID(),
				producer:  attachmentProducer,
				nameBasis: TargetNameBasis(frNodeCtx),
				fragments: fragments,
				files:     ownFiles,
				values:    frValues.Resolved,
			})
		}
	}

	state.PhaseReached = 6
	return out, hostPatched, contributions, deferred, nil
}

// attachmentContribution is one component-scope attachment's output where that
// attachment rendered for the target its host's group assembles into one
// document, carried out of the attachment render loop with every coordinate the
// group needs to hold it as a contributor of its own.
//
// It travels as a value rather than being routed inside the render loop because
// the loop holds no dispatch group: the group is keyed by the host component's
// evaluated path and provider, which the orchestrator settles around the loop.
// Routing here would need that key composed a second time, and a second
// composition of a routing key is the drift the key's own home exists to remove.
//
// nameBasis is the identity the assembled artifact addresses this contribution's
// content by. It is present rather than optional because nodeID is a marker no
// target's name alphabet accepts, so a contributor arriving without it would have
// its names derived from the marker.
//
// A contribution carries EITHER fragments or files, never both, and which one is
// the disposition the render settled. fragments are the output handed to the
// host's group to fold into one document. files are the output the attachment
// emitted under its own names, for a group that places each contributor at a
// position of its own — the group keys them under this contribution's own key and
// composes the position, so the attachment sits beside its host rather than
// inside it. A contribution is made in both cases because the group's contributor
// list is a property of the group: an attachment left out of it is one no
// assembled artifact can name, whichever way its files travelled.
//
// The files are carried out under their BARE names rather than under a prefix,
// and that is the placement rather than a detail of the carrying. A prefix would
// put them below the position the group gives this contributor, and a group's
// target discovers its contributors AT those positions — so output below one is
// not a contributor and is read by nothing.
//
// values is what this attachment's own valuesMapping resolved to, carried out for
// a group whose assembler supplies each contributor's values from the assembled
// artifact rather than from the contributor's own files. It travels because the
// files this attachment placed are the ones its author shipped, vendored in
// verbatim: nothing wrote the resolved values into them, so a group that does not
// carry them here has no other way to reach them and the placed content renders
// its own defaults instead.
type attachmentContribution struct {
	nodeID    string
	producer  derivedKeyProducer
	nameBasis string
	fragments []EmittedFile
	files     []EmittedFile
	values    map[string]any
}

// attachmentRenderItem is one attachment queued for the Phase B.6 render loop —
// one that generates a resource of its own, or one that declares a pipeline, or
// both. Everything that differs between attachment families is a field here, so
// the loop that consumes the sequence has one body:
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
//   - Identity is the pair of authored parts this attachment is identified by —
//     its host component and its instance name. Both the component identity its
//     resources are stamped with and the directory its files are written under
//     are derived from it, so the two cannot come to name different attachments.
type attachmentRenderItem struct {
	Instance string
	// FeatureType is the Feature this attachment NAMES — the entity whose schema
	// the attachment's authored values were checked against, and therefore the
	// entity whose declarations govern those values wherever one is read. It is
	// deliberately not the Feature the bound FeatureRecipe implements: which
	// FeatureRecipe binds to an attachment is decided by the Manifest's own
	// mapping and nothing holds the two spellings equal, so a reader that took
	// the FeatureRecipe's word for it could read a different entity's
	// declarations over this attachment's data.
	FeatureType string
	FR          *types.FeatureRecipe
	CtxValues   types.OrderedMap[any]
	CtxInfra    types.OrderedMap[any]
	OwnValues   map[string]any
	OwnInfra    map[string]any
	Inject      *types.InjectPipe
	Provides    *types.OrderedMap[string]
	Identity    attachmentIdentity
}

// attachmentRenderSequence assembles the one ordered sequence Phase B.6
// renders: every attachment on the node whose bound FeatureRecipe has work here.
// Two disjoint reasons admit one — it declares a render input, so it generates a
// resource of its own; or it declares a pipeline, so it rewrites documents
// already emitted — and participatesInRenderSequence holds that rule. A
// FeatureRecipe declaring neither is excluded: an inject-only one because Phase A
// owns its inject merge, a pure-wiring one because its effect is the uses-edge
// its Feature carries. Neither has anything to do here.
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
//
// The host component is a parameter because an attachment is identified by its
// host as well as by the name it is attached under, and both parts are needed
// here: the item carries them as one value, and everything downstream that names
// this attachment — the identity its resources are stamped with, the directory
// its files are written under — derives from that one value rather than
// assembling the pair again.
func attachmentRenderSequence(
	tmplCtx *types.TemplateContext,
	hostComponentID string,
	featureRefs []FeatureRecipeRef,
	attachments types.OrderedMap[types.IRFeatureEntry],
	attachmentProvides map[string]types.OrderedMap[string],
) []attachmentRenderItem {
	seq := make([]attachmentRenderItem, 0, len(featureRefs))

	for _, ref := range featureRefs {
		if !participatesInRenderSequence(ref.FeatureRecipe) {
			continue
		}
		item := attachmentRenderItem{
			Instance:  ref.InstanceName,
			FR:        ref.FeatureRecipe,
			CtxValues: tmplCtx.Values,
			CtxInfra:  tmplCtx.Infra,
			OwnValues: map[string]any{},
			OwnInfra:  map[string]any{},
			Identity:  attachmentIdentity{host: hostComponentID, instance: ref.InstanceName},
		}
		if attach, ok := tmplCtx.AttachmentPillars(ref.InstanceName); ok {
			item.CtxValues, item.CtxInfra = attach.Values, attach.Infra
			item.OwnValues = attach.Values.TemplateAlphabeticalMapDeep()
			item.OwnInfra = attach.Infra.TemplateAlphabeticalMapDeep()
		}
		if entry, ok := attachments.Get(ref.InstanceName); ok {
			item.FeatureType = entry.Type
			if entry.Inject.Len() > 0 {
				item.Inject = buildAttachmentInject(entry)
			}
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
//
// It leaves .context.used_by as the host's context holds it, which is empty.
// Whether a surface sees the connections made to the host is decided by what that
// surface's output can reach, not by the attachment family, so the caller sets
// it: Phase B.6 does for the templates producing the attachment's own resources,
// and Phase A's injectMapping, whose output lands on the host's delivery pipe,
// never does.
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
// input-kind gate, then sourced engine.Render / synthesized workload synthesis
// / artifacts emission via the same Phase B leaf passes (phaseB2/B3/B4). Its
// emission is then recorded and checked for two of its blocks meeting on one
// output name, under the owner this render belongs to rather than under the
// host component's node identity the passes run beneath. A render-only
// synthetic Recipe carries the FR's input-kind fields so those passes — which
// read state.Recipe.Spec.* — operate unchanged; this is reuse of the component
// machinery, not a parallel FeatureRecipe track. Returns the accumulated
// EmittedFiles (each carrying its Origin and Intermediate flag); the per-
// component callers place them under features/<host>/<instance>/, while the
// boundary-scope caller routes them by the Intermediate flag.
//
// identity names the per-component attachment being rendered — the component the
// feature is attached to, and the name it is attached under. Both parts are
// needed, and the host is the one that separates a single instance name attached
// to several components: those attachments emit different resources, so they need
// different identities, and an author who names them alike has written nothing
// wrong. A boundary-scope caller names no attachment here, because the identity
// its render carries is composed from the boundary path instead and is set on the
// context before this is reached.
//
// The same value is what the per-component caller derives this render's output
// directory from, so the identity composed below and the directory the emission
// lands in are two readings of one statement of the parts rather than two
// assemblies that could disagree.
//
// The second return value is the component-identity label value this render
// composed from those parts, for the caller to declare into its own
// derived-name accumulator — the caller holds that accumulator and this function
// does not, and the value is reported rather than re-derived at the caller so
// the derivation itself stays at one site. It is empty only for a render that
// derived none, which is a caller supplying no instance label. Every input kind
// stamps the composed value on what it emits, so every one of them reports it.
//
// hydrated is the owner this render records its mid-render state through, and
// the owner whose declared rule is asked about each artifact entry it projects.
// It is supplied by the caller because the owner is the caller's own knowledge
// and cannot be recovered here: this function is reached from a per-component site
// and from a boundary-scope one, and the node identity it is handed is the host
// component's on the first and a composite dispatch marker on the second. A nil
// owner records nothing and asks nothing.
//
// annotations are the platform annotations authored for the scope this render's
// output lands in, in the order the Release authored them. They are supplied by
// the caller for the same reason the owner is — the set is the caller's own
// knowledge and differs by scope: the per-component caller passes the host node's
// merged annotations, the boundary-scope caller its boundary's. They arrive as the
// ordered source rather than as the render context's flattened copy, which is what
// keeps the advisory stream below byte-stable across runs of identical input.
// providerPlugin resolves this FeatureRecipe's provider binding to the entity
// carrying the support claim that advisory reads. Either one absent leaves nothing
// to compare and the advisory emits nothing.
//
// documentPath is the file this FeatureRecipe was declared in, and it is what every
// diagnostic the sub-passes below raise names as its location. It is supplied by
// the caller rather than looked up here for the reason the owner above is: this
// function is reached from both attachment scopes, and the lookup that answers it
// is the caller's own. An entity resolved from a registry has no such document and
// passes the empty string, which positions those diagnostics at no file — the same
// answer the component render path gives for a Recipe in the same position.
//
// recordedPins are the content pins the load recorded for that same document, and
// they arrive already resolved for the same reason documentPath does: the lookup
// that answers them travels on the render input the caller holds. They reach the
// artifacts walk through the state built below, where an imported file whose
// current bytes have moved off its recorded pin halts the render.
//
// withheld names what of frCtx an author declared sensitive, so a failure of any
// template this render evaluates — its workload, its artifacts — or of a check of
// what one produced reports without such a value.
func renderFeatureRecipeFiles(
	ctx context.Context,
	frEngine Provider,
	fr *types.FeatureRecipe,
	frCtx *types.TemplateContext,
	frNodeCtx NodeRenderContext,
	frValues map[string]any,
	resolvedSrc any,
	documentPath string,
	recordedPins recipepin.Pins,
	identity attachmentIdentity,
	hydrated attachmentOwner,
	slots types.OrderedMap[types.AttachmentSlot],
	workloadLibs map[string]types.WorkloadLibrary,
	artifactsLibs map[string]types.ArtifactLibrary,
	annotations types.OrderedMap[string],
	providerPlugin func(ref string) (types.Plugin, bool, error),
	withheld redact.Keys,
) ([]EmittedFile, string, error) {
	// Every document this render emits names THIS FeatureRecipe as the Recipe that
	// produced it. The context arrives as a copy of the host component's and so
	// carries the host Recipe's identity, while the recipe-uri provenance value is
	// assembled from these two fields at whichever surface stamps it — so leaving
	// them as copied hands a reader tracing a document back to its producer the
	// identity of a Recipe that did not produce it. That is worse than stamping
	// nothing: an absent value is visibly absent, whereas a wrong one is
	// indistinguishable from a right one and is read precisely when someone is
	// working out where a document came from.
	//
	// This is the one home for that identity because this function is the single
	// funnel every FeatureRecipe render passes through — both attachment scopes,
	// and each of the three input kinds. The artifacts kind reaches the same value
	// by its own route: its recipe-uri is computed host-side from the synthetic
	// Recipe below, which carries this same metadata. So setting it here is what
	// makes all three kinds answer alike, not what makes one of them work.
	frNodeCtx.RecipeName = fr.Metadata.Name
	frNodeCtx.RecipeVersion = fr.Metadata.Version

	hasSource := sourceValueDeclared(resolvedSrc)
	hasWorkload := !fr.Spec.Workload.IsEmpty()
	hasArtifacts := fr.Spec.Artifacts.Len() > 0

	if err := gateInputKind(frNodeCtx.NodeID, fr.Spec.Capability, frEngine, hasSource, hasWorkload, hasArtifacts); err != nil {
		return nil, "", err
	}

	// The same advisory the component render path raises, on the same seat: after
	// the capability gate, before any render pass. It reaches the one site that
	// emits it rather than restating the rule, and it reads the SAME declared-kind
	// triple the gate above consults, so the two cannot come to describe this
	// render's input kinds differently.
	//
	// This render is judged on its OWN provider and its OWN declared kinds, which
	// is the whole reason the host node's advisory does not cover it: a
	// FeatureRecipe names its own capability and declares its own inputs, so an
	// annotation the host's target acts on may reach nothing at all here — and the
	// documents this render emits are a different set of bytes from the host's.
	// Judging it by the host's answer would report the host's outcome about output
	// the host did not produce.
	//
	// An attachment admitted for its pipeline alone declares no input kind, so the
	// triple is empty and nothing is emitted for it: it renders no documents of its
	// own, so there is no output for an annotation to have been dropped from.
	warnUnhonorableAnnotations(frNodeCtx.NodeID, fr.Spec.Capability, ProviderEngineKind(fr.Spec.Capability),
		declaredNodeInputKinds(hasSource, hasWorkload, hasArtifacts),
		annotations, providerPlugin, frNodeCtx.UnsupportedFieldSink)

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

	// Every resource this attachment emits carries the attachment's own identity,
	// not the host component's whose NodeID the FR node carries — for all three
	// input kinds alike, so a reader or a tool selecting an attachment's resources
	// by identity finds every one of them. We set LabelComponentID (consumed by
	// ProvenanceComponentID inside the synthesizer, the artifacts provenance
	// builder and the sourced render's label injection) on a copy every crossing
	// below is handed.
	//
	// What the copy deliberately does NOT set is the target-name basis. The
	// synthesized and artifacts paths name their resources after the identity,
	// which is what keeps an FR-synthesized Deployment from being named for the
	// host and colliding with the host's own workload. But a sourced attachment
	// delivers a chart or a fragment its AUTHOR wrote, and that author's own
	// templates render against the release name and name resources after it — so
	// the basis stays empty here, falls back to the host component's node id, and
	// the author's resources keep the names they are already deployed under. The
	// identity and the basis are two carries for exactly this reason; a
	// boundary-scope attachment, which has no host component to fall back to, sets
	// both.
	//
	// That label value is a name the compiler derived, so it is reported to the
	// caller for the injectivity check the names a guest declares go through: two
	// attachments whose instances reach one label value would otherwise hand the
	// same provenance to two different sets of resources, with nothing in the
	// emitted output recording that two were meant. It is reported for every
	// crossing, because every crossing now reads this copy and stamps the value on
	// what it emits.
	//
	// The identity is COMPOSED from the host component and the instance rather
	// than written out of the instance alone, and the host is what makes it an
	// identity of the attachment rather than of the name it was attached under.
	// One name attached to several components is a shape an author may write —
	// and a cascade that mints one instance name across many components by design
	// gives an author no way to write anything else — so the host has to be part
	// of what separates them. It also separates an attachment named exactly after
	// the component hosting it, which composes under a scope no authored identity
	// may open with and so cannot meet the host's own.
	// How long that identity may be is the target's answer rather than a number
	// the compiler holds, so the composition is settled against the engine this
	// attachment renders through: composed, put to the guest, shortened to whatever
	// bound the guest returned, and put again until the guest accepts it. A guest
	// that answers no such question accepts the composition as composed.
	synthNodeCtx := frNodeCtx
	derivedComponentID := ""
	if identity.named() {
		settled, err := SettleComposedComponentIdentity(ctx, frEngine, labels.AttachmentScope,
			fr.Spec.Capability, identity.parts()...)
		if err != nil {
			return nil, "", err
		}
		synthNodeCtx.LabelComponentID = settled
		derivedComponentID = settled
	}

	synth := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: fr.Metadata.Name, Version: fr.Metadata.Version},
		Spec: types.RecipeSpec{
			Capability:    fr.Spec.Capability,
			Implements:    fr.Spec.Implements,
			Source:        resolvedSrc,
			ValuesMapping: fr.Spec.ValuesMapping,
			Workload:      fr.Spec.Workload,
			Artifacts:     fr.Spec.Artifacts,
		},
	}
	state := NewPhaseBState(synth, frNodeCtx.NodeID, frNodeCtx.BoundaryPath, documentPath)
	// What of frCtx an author declared sensitive, supplied by the caller because
	// which entity governs each pillar is the caller's own knowledge: this
	// attachment's Feature for the values and infra, the host's declarations for
	// the rest, or the boundary attachment's Feature for a boundary-scope render.
	state.Withheld = withheld
	// This attachment's own recorded pins, so the artifacts walk below compares
	// against what was recorded for the FeatureRecipe's document rather than
	// against the host component's. They travel on the state and not on the
	// synthesized Recipe above: what that value carries is the authored shape the
	// sub-passes render from, and a derived record copied into it would be read as
	// something the FeatureRecipe's author had written.
	state.RecordedPins = recordedPins
	// The owner this render records against and is checked under. It is set
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
	// base Recipe; an artifact library `$import` resolves against this set,
	// sourced identically from the render input — single source, no second map.
	state.ArtifactLibraries = artifactsLibs

	// Sourced mode hits engine.Render; synthesized renders at B.4, artifacts at
	// B.3. EmitOriginSource files accumulate by sorted filename for
	// determinism. The Intermediate flag follows the same rule the component
	// path uses: a BoundaryAssembler that does not place its own artifacts
	// consumes Render output as fragment input.
	if hasSource {
		// The settled copy, so the identity this attachment's resources are
		// labelled with is the attachment's own on this crossing too. Only the
		// label moves: the copy leaves the target-name basis empty, so the release
		// name and the output-file prefix the author's chart renders under stay the
		// host component's.
		files, err := frEngine.Render(ctx, frValues, synthNodeCtx)
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

	// The Feature's own slots and no connection: a FeatureRecipe's Workload is
	// held to the Feature it implements, which draws none of the host
	// component's connections, so an entry there naming a connection is refused
	// as naming one the owner does not draw.
	if err := phaseB2(frCtx, state, validator.WorkloadSlots{Own: slots}); err != nil {
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
	// Reduce this render's completed emission to the projection that outlives it,
	// then ask the value declared over this owner's emission whether two of the
	// FeatureRecipe's blocks met on one output name.
	//
	// This is the position: every pass of this render that can contribute a file has
	// run, and the caller has not yet touched the set — the per-component caller
	// composes the attachment's output prefix onto every name, and the
	// boundary-scope caller routes them by the intermediate flag. Either would put
	// the set beyond the point at which it is this attachment's own emission under
	// the names its own blocks chose.
	//
	// The recording and the reading are adjacent deliberately: the rule reads the
	// retention, so recording anywhere but immediately before it would leave the
	// rule asked about an emission that is not this one.
	state.recordAttachmentEmission()
	if err := runAttachmentEmissionCollisionRule(ctx.Err, state); err != nil {
		return nil, "", err
	}

	return state.EmittedFiles, derivedComponentID, nil
}
