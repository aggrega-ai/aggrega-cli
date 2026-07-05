// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Boundary-scope FeatureRecipe dispatch: the executionScope: boundary
// branch of Phase B.6. Per-component FR dispatch (the default,
// executionScope: component or unset) lives in phase_b_subpasses.go and is
// untouched by this file.
//
// Lifecycle.
//
//   1. RenderAll instantiates one *BoundaryFRState at start. The state
//      threads through every per-node phaseB6 invocation so dedup tracking
//      and boundary-scope provides accumulate across nodes that share a
//      boundary attachment.
//   2. Per-node phaseB6 detects boundary-scope FRs by checking
//      fr.Spec.ExecutionScope == "boundary". For each such FR encountered:
//      - Compute the dedup key (boundary_path, fr_instance).
//      - If state.Dedup[key] is set, skip — the FR already ran for this
//        boundary.
//      - Else: validate the FR's templates against the boundary-scope
//        allowlist (see validateBoundaryScopeFRTemplate), build the
//        boundary-scope TemplateContext (see buildBoundaryContext),
//        evaluate FR locals + values + provides templates, dispatch to the
//        FR's engine, classify emitted files by Intermediate flag, and
//        record state.
//   3. After the per-node loop completes, RenderAll routes boundary-scope
//      FR Intermediate=true files into the per-(path, provider) dispatch
//      group at the FR's evaluated path (using fr.Spec.Provider for the
//      provider component of the dispatch key). Intermediate=false files
//      land under <boundary_directory>/<provider>/features/<instance>/.
//   4. Consumer resolution: when a per-component consumer references
//      .context.uses.<feat>.<provided> for a feat whose FR has
//      executionScope: boundary, the inject-rehydration path consults
//      state.Provides via cascade-aware lookup (see
//      resolveBoundaryScopeFRProvides). The cascade walks from the
//      consumer's BoundaryPath up to the boundary holding the FR
//      attachment.

package renderer

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// boundaryFRKey identifies a (boundary_path, FR-instance) tuple for
// boundary-scope FR deduplication and provides storage. The boundary path
// is the slash-separated traversal path to the boundary that holds the FR
// attachment (matching IRNode.BoundaryPath's shape); the FR instance name
// is the map key in the boundary's Features block.
type boundaryFRKey struct {
	BoundaryPath string
	FRInstance   string
}

// BoundaryFRState holds cross-node accumulator state for boundary-scope
// FeatureRecipe dispatch. One instance lives per RenderAll invocation;
// the orchestrator threads it through every per-node phaseB6 call.
//
// Dedup tracks (boundary_path, FR-instance) tuples already processed, so
// subsequent encounters of the same key skip the FR — the FR has already
// run once for that boundary.
//
// Provides stores the resolved provides for each (boundary, FR-instance)
// tuple; consumers reference these via cascade walk during inject pre-
// hydration.
//
// Files captures the boundary-scope FR's emitted output for routing at
// finalization (Intermediate=true → per-(path, provider) BoundaryAssembler;
// Intermediate=false → per-boundary features/<instance>/ disk write).
//
// ValidatedFRs caches FR-template-allowlist validation results so a given
// FR is validated once per RenderAll invocation rather than once per
// encountered (boundary, FR-instance) tuple.
type BoundaryFRState struct {
	Dedup        map[boundaryFRKey]bool
	Provides     map[boundaryFRKey]map[string]any
	Files        map[boundaryFRKey][]EmittedFile
	ValidatedFRs map[string]error

	// ConsumerEnvOverrides carries per-consumer environment overrides a
	// boundary-scope FeatureRecipe's resource-gen emits, keyed
	// consumer-nodeID -> {envVar: value}. The orchestrator applies each named
	// consumer's entries to that consumer's injected env after Phase A,
	// superseding the uniform injectMapping for those keys. The channel is
	// generic: it carries opaque string values and does not interpret them.
	ConsumerEnvOverrides map[string]map[string]string
}

// NewBoundaryFRState returns a fresh state instance ready for population.
func NewBoundaryFRState() *BoundaryFRState {
	return &BoundaryFRState{
		Dedup:                map[boundaryFRKey]bool{},
		Provides:             map[boundaryFRKey]map[string]any{},
		Files:                map[boundaryFRKey][]EmittedFile{},
		ValidatedFRs:         map[string]error{},
		ConsumerEnvOverrides: map[string]map[string]string{},
	}
}

// boundaryScopeFRRejectedFields enumerates the per-component-only field
// references that a boundary-scope FR template MUST NOT reference. Each
// entry is a top-level dotted-field key returned by walkNodeForFields
// (the same AST walker used by EvalPathTemplate's allowlist validation).
//
// Per the boundary-scope TemplateContext shape, these pillars are absent
// or replaced by per-boundary equivalents:
//
//   - context  — replaced by .boundary.{path,boundary_name,components,...}.
//     .context.instance_id, .context.inject, .context.uses are
//     meaningless at boundary scope.
//   - workload — per-component Workload; absent at boundary scope.
//   - secret_name, inject, inject_raw — secret-specific; absent at
//     boundary scope.
//
// boundary-scope FRs reference .boundary.* pillars instead. The allowlist-
// rejection at parse time prevents shape-mismatch errors at runtime.
var boundaryScopeFRRejectedFields = map[string]bool{
	"context":     true,
	"workload":    true,
	"secret_name": true,
	"inject":      true,
	"inject_raw":  true,
}

// validateBoundaryScopeFRTemplate parses every inline template field on a FR
// with spec.executionScope: boundary and verifies that no template references
// any per-component-only field (.context.*, .workload.*, .secret_name,
// .inject, .inject_raw). Returns nil on pass; ErrFRTemplateInvalidField
// wrapped with the FR name + offending field on the first violation, or a
// CATALOG_ERROR if any inline template fails to parse.
//
// Templates checked (inline only): valuesMapping, injectMapping, each provides
// entry, each locals entry, and each pipeline step `with` template. Templates
// loaded from external files (paths rather than inline strings) are not parsed
// here — file content load + parse happens at engine-render time. A parse
// failure propagates as a CATALOG_ERROR rather than being skipped: this
// validator is the only surface that parses injectMapping and pipeline `with`
// for a boundary-scope FR (the dispatch evaluates only locals, valuesMapping,
// and provides), so a skipped parse error would drop silently for those fields.
func validateBoundaryScopeFRTemplate(fr *types.FeatureRecipe) error {
	if fr == nil {
		return nil
	}
	frName := fr.Metadata.Name
	type tmplField struct {
		label string
		text  string
	}
	var tmpls []tmplField
	// Inline-mode valuesMapping text is available here; file / library
	// imports defer to render-time validation (the template text is
	// disk-resident or OCI-resolved at that point).
	if fr.Spec.ValuesMapping.Inline != "" {
		tmpls = append(tmpls, tmplField{label: "valuesMapping", text: fr.Spec.ValuesMapping.Inline})
	}
	if fr.Spec.InjectMapping != "" {
		tmpls = append(tmpls, tmplField{label: "injectMapping", text: fr.Spec.InjectMapping})
	}
	// Inline workload / artifacts templates are subject to the same boundary-
	// scope allowlist (file / library imports defer to render-time validation,
	// as with valuesMapping above). A boundary-scope FR's synthesised workload
	// or emitted artifacts reference .boundary.* pillars, never .context.* /
	// .workload / .secret_name / .inject.
	if fr.Spec.Workload.Inline != "" {
		tmpls = append(tmpls, tmplField{label: "workload", text: fr.Spec.Workload.Inline})
	}
	for k, v := range fr.Spec.Artifacts.All() {
		if v.Inline != "" {
			tmpls = append(tmpls, tmplField{label: "artifacts[" + k + "]", text: v.Inline})
		}
	}
	for k, v := range fr.Spec.Provides.All() {
		tmpls = append(tmpls, tmplField{label: "provides[" + k + "]", text: v})
	}
	for k, v := range fr.Spec.Locals.All() {
		tmpls = append(tmpls, tmplField{label: "locals[" + k + "]", text: v})
	}
	for _, step := range fr.Spec.Pipeline {
		if step.With != "" {
			tmpls = append(tmpls, tmplField{label: "pipeline[" + step.ID + "].with", text: step.With})
		}
	}
	for _, t := range tmpls {
		t2, err := template.New("fr-validate").Funcs(aggregaFuncMap()).Parse(t.text)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: frName},
				fmt.Sprintf("FeatureRecipe %q %s template failed to parse", frName, t.label))
		}
		fields := collectTopLevelFields(t2)
		for _, f := range fields {
			if boundaryScopeFRRejectedFields[f] {
				inner := fmt.Errorf("%w: FeatureRecipe %q with executionScope: boundary references per-component-only field .%s in %s; boundary-scope FRs use .boundary.* pillars in place of .context.* / .workload / .secret_name / .inject — see aggrega-docs/language-reference/entities/featurerecipe.md",
					ErrFRTemplateInvalidField, frName, f, t.label)
				return cerrors.Wrap(inner, cerrors.CatalogError, cerrors.SourceLocation{}, inner.Error())
			}
		}
	}
	return nil
}

// dispatchBoundaryScopeFR runs a boundary-scope FeatureRecipe once for a
// given (boundary_path, fr_instance) tuple. The caller (phaseB6) gates on
// state.Dedup[key]; this function assumes the dedup check passed.
//
// Steps:
//
//  1. Validate FR templates against the boundary-scope allowlist (cached
//     per FR via state.ValidatedFRs).
//  2. Build the boundary-scope TemplateContext via buildBoundaryContext.
//  3. Read the boundary's BoundaryFeature attachment for the FR instance
//     to populate .infra and .values.
//  4. Evaluate FR locals + valuesMapping + provides against the boundary-
//     scope context.
//  5. Dispatch to the FR's engine.Render with the resolved values.
//  6. Classify emitted files by Intermediate flag; record into state.Files
//     for routing at orchestrator finalization.
//  7. Record provides into state.Provides for downstream consumer
//     resolution.
func dispatchBoundaryScopeFR(
	ctx context.Context,
	fr *types.FeatureRecipe,
	frInstance string,
	key boundaryFRKey,
	state *PhaseBState,
	boundaryFRState *BoundaryFRState,
	nodeCtx NodeRenderContext,
	engineFactory EngineFactory,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	allProvides NodeProvides,
	input RendererInput,
) error {
	// 1. Validate FR templates (cached).
	cacheKey := fr.Metadata.Name + ":" + fr.Metadata.Version
	if cached, seen := boundaryFRState.ValidatedFRs[cacheKey]; seen {
		if cached != nil {
			return cached
		}
	} else {
		validationErr := validateBoundaryScopeFRTemplate(fr)
		boundaryFRState.ValidatedFRs[cacheKey] = validationErr
		if validationErr != nil {
			return validationErr
		}
	}

	// 2-4. Build the FR-scoped TemplateContext (.boundary + .infra, .values
	//       empty per the Release pillar-split). Shared with the pre-loop
	//       provides pre-pass so both evaluate against an identical context.
	feat, _ := lookupBoundaryFeatureAttachment(graph.Spec.BoundaryFeatures, key.BoundaryPath, frInstance)
	frCtx, err := buildBoundaryFRTemplateContext(allProvides, fr, frInstance, key, graph, release, boundaryAnnotations)
	if err != nil {
		return err
	}

	// 5. Evaluate FR locals + valuesMapping + provides.
	if err := evaluateBoundaryFRTemplates(fr, frInstance, frCtx, boundaryFRState, key); err != nil {
		return err
	}

	// 6. Dispatch to FR's engine.
	frEngine, err := engineFactory(fr.Spec.Provider)
	if err != nil {
		return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: key.BoundaryPath}, fmt.Sprintf("boundary-scope FR %q engine factory", frInstance))
	}

	// Build the per-FR NodeRenderContext. Use the boundary's path as the
	// pseudo-NodeID for the engine — the engine's per-node identity is
	// the boundary-FR tuple, not a per-component instance. The synthetic
	// NodeID marker syntax stays for dispatch + trace + error-message
	// uniqueness; LabelComponentID carries the K8s-safe sanitized form
	// for label values. RecipeName/Version carry the FR's identity so the
	// aggrega.ai/recipe-uri label resolves to the FR (not the consumer
	// node's Recipe whose nodeCtx this struct was copied from).
	frNodeCtx := nodeCtx
	frNodeCtx.NodeID = "<boundary-fr>:" + frInstance + "@" + key.BoundaryPath
	frNodeCtx.BoundaryPath = key.BoundaryPath
	frNodeCtx.RecipeName = fr.Metadata.Name
	frNodeCtx.RecipeVersion = fr.Metadata.Version
	frNodeCtx.LabelComponentID = labels.SanitizeForK8sLabelValue(frNodeCtx.NodeID)
	frNodeCtx.OriginalSource = fr.Spec.Source
	// The boundary-scope FR's source resolves through the shared
	// resolve+extract helper against the boundary's own infra — the SAME
	// infra (graph.Spec.BoundaryFeatures) the lock-time descent pins from —
	// so a remote source is cache-extracted at build and the build-time
	// identity matches the lockfile pin. A local source forwards verbatim.
	resolvedSrc, err := resolveEntitySource(input, fr.Spec.Source, fr.Spec.Locals, frSourceURI(fr), nil, feat.Infra.TemplateAlphabeticalMapDeep())
	if err != nil {
		return wrapSourceResolutionError(err, frNodeCtx.NodeID, fmt.Sprintf("boundary-scope FR %q source resolution", frInstance))
	}
	frNodeCtx.Source = resolvedSrc

	// Evaluate the FR's valuesMapping against the boundary-scope context — the same
	// evaluator the per-component FR path uses (phase_b_subpasses.go), not a parallel
	// path. A valuesMapping-less FR yields the flattened .values pillar unchanged, so a
	// boundary FR without a valuesMapping renders byte-identically.
	frValues, err := ExecutePhaseBForFeature(frCtx, fr, input.MappingLibraries)
	if err != nil {
		return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("boundary-scope FR %q valuesMapping", frInstance))
	}

	// 6-7. Render via the SAME input-kind machinery the per-component FR sites
	// use — the central gate, then sourced engine.Render / synthesised workload
	// synthesis / artifacts emission — against the boundary-scope context (frCtx
	// carries .boundary.* pillars, never .context.*; the workload/artifacts
	// templates were allowlist-checked at validateBoundaryScopeFRTemplate). The
	// returned EmittedFiles already carry their Intermediate flag for boundary-
	// assembler routing. instanceLabel is "" so the boundary-disambiguated
	// LabelComponentID set above stays the resource-naming basis (a per-instance
	// label would collide across boundaries sharing an FR-instance name).
	emitted, err := renderFeatureRecipeFiles(ctx, frEngine, fr, frCtx, frNodeCtx, frValues, resolvedSrc, "", featureSecretSlots(fr, input))
	if err != nil {
		return err
	}

	// Boundary-scope FeatureRecipe pipeline (own only — a boundary-scope FR has
	// no single host component, so host is nil and any /host operation halts
	// with ErrPipelineHostScopeUnavailable).
	if len(fr.Spec.Pipeline) > 0 {
		var parameters map[string]any
		if input.ResolveResult != nil {
			parameters = input.ResolveResult.Parameters
		}
		renderCtx := buildBoundaryFRRenderCtx(key.BoundaryPath, parameters, boundaryAnnotations)
		// own provider is the boundary-scope FR's own engine kind (version pin
		// stripped — codec + compatibility key on the kind); a boundary-scope FR
		// has no single host component (host nil), so hostProvider is empty.
		pres, perr := runNodePipeline(ctx, fr.Spec.Pipeline, emitted, nil, false, ProviderEngineKind(fr.Spec.Provider), "", renderCtx, frCtx, input.YAMLIndent, input.PluginPathLookup, engineFactory, frNodeCtx.NodeID)
		if perr != nil {
			return perr
		}
		emitted = pres.Own
		state.Warnings = append(state.Warnings, pres.Warnings...)
	}
	boundaryFRState.Files[key] = emitted

	return nil
}

// buildBoundaryFRRenderCtx assembles the render-time context a boundary-scope
// FeatureRecipe pipeline step receives. A boundary-scope FR has no single host
// component, so the context carries the boundary's metadata and the fulfilled
// parameters; the boundary's cascaded annotations land on the boundary root.
func buildBoundaryFRRenderCtx(boundaryPath string, parameters map[string]any, boundaryAnnotations map[string]types.OrderedMap[string]) renderctx.RenderContext {
	rc := renderctx.RenderContext{
		Boundary: renderctx.BoundaryContext{
			Name: boundaryLeafName(boundaryPath),
			Path: splitBoundaryPath(boundaryPath),
		},
	}
	rc.Parameters = orderedParameters(parameters)
	if ann, ok := boundaryAnnotations[boundaryPath]; ok {
		rc.Boundary.Annotations = ann
	}
	return rc
}

// buildBoundaryFRTemplateContext assembles the FR-scoped TemplateContext for a
// boundary-scope FeatureRecipe instance: the boundary pillars (.boundary) plus
// the boundary attachment's .infra, with .values empty per the Release
// pillar-split (Aggregate authors values; Release authors infra). Shared by the
// pre-loop provides pre-pass and the Phase B.6 dispatch so both evaluate the
// FR's templates against an identical context.
func buildBoundaryFRTemplateContext(
	allProvides NodeProvides,
	fr *types.FeatureRecipe,
	frInstance string,
	key boundaryFRKey,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
) (*types.TemplateContext, error) {
	boundaryCtx, err := buildBoundaryContext(allProvides, key.BoundaryPath, graph, release, boundaryAnnotations)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.OpsError, cerrors.SourceLocation{File: key.BoundaryPath}, fmt.Sprintf("boundary-scope FR %q: build context for boundary %q", frInstance, key.BoundaryPath))
	}
	feat, found := lookupBoundaryFeatureAttachment(graph.Spec.BoundaryFeatures, key.BoundaryPath, frInstance)
	if !found {
		// The boundary doesn't carry the attachment directly. Defensive: the
		// cascade resolution that reaches this FR-instance is upstream.
		return nil, cerrors.New(cerrors.OpsError, cerrors.SourceLocation{File: key.BoundaryPath},
			fmt.Sprintf("boundary-scope FR %q: no BoundaryFeature attachment found at boundary %q", frInstance, key.BoundaryPath),
			nil, nil)
	}
	frCtx := &types.TemplateContext{
		Values:   types.OrderedMap[any]{},
		Infra:    feat.Infra,
		Locals:   types.OrderedMap[string]{},
		Boundary: boundaryCtx,
		Metadata: nil,
	}
	if release != nil {
		frCtx.Metadata = &types.TemplateMetadata{
			ReleaseName:    release.Metadata.Name,
			ReleaseVersion: release.Metadata.Version,
			Environment:    release.Metadata.Environment,
		}
		if graph != nil {
			frCtx.Metadata.AggregateName = graph.Metadata.AggregateName
		}
	}
	return frCtx, nil
}

// PrepopulateBoundaryScopeProvides evaluates every boundary-scope FeatureRecipe's
// provides ahead of the per-node render loop, storing them in
// state.Provides keyed by (owning-boundary-path, instance). A per-component
// consumer's uses edge resolves a boundary-scope FR's provided values from this
// state during Phase A inject rehydration; without the pre-pass those provides
// are written only at Phase B.6 — after the consumer's Phase A — and resolve
// silently empty. The pre-pass is idempotent with the Phase B.6 dispatch: both
// share buildBoundaryFRTemplateContext + evaluateBoundaryFRTemplates and the
// validation cache, so a boundary-scope FR's provides evaluate identically
// whether read here or overwritten at dispatch. Walks graph.Spec.BoundaryFeatures
// (the un-folded projection), so no Release re-walk.
func PrepopulateBoundaryScopeProvides(
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
	recipeLookup RecipeLookup,
	allProvides NodeProvides,
	engineFactory EngineFactory,
	state *BoundaryFRState,
) error {
	if graph == nil || state == nil {
		return nil
	}
	for boundaryPath, feats := range graph.Spec.BoundaryFeatures.All() {
		for instName, bf := range feats.All() {
			fr, ok := recipeLookup.FeatureRecipe(instName, bf.Type)
			if !ok || fr.Spec.ExecutionScope != "boundary" {
				continue
			}
			cacheKey := fr.Metadata.Name + ":" + fr.Metadata.Version
			if cached, seen := state.ValidatedFRs[cacheKey]; seen {
				if cached != nil {
					return cached
				}
			} else {
				validationErr := validateBoundaryScopeFRTemplate(fr)
				state.ValidatedFRs[cacheKey] = validationErr
				if validationErr != nil {
					return validationErr
				}
			}
			key := boundaryFRKey{BoundaryPath: boundaryPath, FRInstance: instName}
			frCtx, err := buildBoundaryFRTemplateContext(allProvides, fr, instName, key, graph, release, boundaryAnnotations)
			if err != nil {
				return err
			}
			if err := evaluateBoundaryFRTemplates(fr, instName, frCtx, state, key); err != nil {
				return err
			}
			// Generic boundary->component env channel: if the FR's provider
			// engine implements the optional override capability, let it
			// produce per-consumer env overrides from the (now edge-enriched)
			// boundary context. The channel is provider-agnostic — it stores
			// whatever opaque string values the engine returns; it does not
			// interpret them. Production lives in the provider engine, not here.
			// This is a host-side capability, resolved from the provider's host
			// capability holder: a provider routed to a Wasm guest exposes the
			// wrapped in-tree engine's channel through the wrapper's holder.
			if engineFactory != nil {
				eng, engErr := engineFactory(fr.Spec.Provider)
				if engErr != nil {
					return cerrors.Wrap(engErr, cerrors.TranslationError, cerrors.SourceLocation{File: key.BoundaryPath}, fmt.Sprintf("boundary-scope FR %q engine factory", instName))
				}
				if envOverrides := ResolveHostCaps(eng).BoundaryConsumerEnvOverrides; envOverrides != nil {
					overrides, ovErr := envOverrides(frCtx)
					if ovErr != nil {
						return cerrors.Wrap(ovErr, cerrors.TranslationError, cerrors.SourceLocation{File: key.BoundaryPath}, fmt.Sprintf("boundary-scope FR %q consumer env overrides", instName))
					}
					mergeConsumerEnvOverrides(state, overrides)
				}
			}
		}
	}
	return nil
}

// BoundaryConsumerEnvOverrider is the optional capability a provider engine may
// implement to drive the boundary->component env channel. Given a boundary-scope
// FeatureRecipe's edge-enriched template context, it returns per-consumer
// environment overrides keyed consumer-nodeID -> {envVar: value}. The channel
// applies each named consumer's entries to that consumer's injected env after
// Phase A. The contract is provider-agnostic: values are opaque strings the
// orchestrator carries without interpretation.
type BoundaryConsumerEnvOverrider interface {
	BoundaryConsumerEnvOverrides(frCtx *types.TemplateContext) (map[string]map[string]string, error)
}

// applyConsumerEnvOverrides sets each override key into inject.Env and rebuilds
// inject.All. The overrides arrive as a plain map from the provider-agnostic
// BoundaryConsumerEnvOverrider boundary, which carries no author order — so the
// sorted-key application here is the determinism mechanism for any genuinely-new
// Env key (Invariant 8), NOT a substitution for an OrderedMap order that exists
// upstream (there is none to drive from). An existing key is updated in place
// (OrderedMap.Set preserves position). It is a targeted last-wins on exactly
// the override keys — other Env keys, and inject.File / inject.Arg, are left
// byte-identical. A nil/empty override map is a no-op (non-named consumers are
// untouched). Rebuilding inject.All matches the inject-rehydrate tail.
func applyConsumerEnvOverrides(inject *types.InjectPipe, overrides map[string]string) {
	if inject == nil || len(overrides) == 0 {
		return
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		inject.Env.Set(k, overrides[k])
	}
	inject.All = buildInjectAll(*inject)
}

// mergeConsumerEnvOverrides merges a producer's per-consumer env-override map
// into state.ConsumerEnvOverrides. Per (nodeID, envVar) the later writer wins;
// callers iterate boundary FRs in graph.Spec.BoundaryFeatures order, which is
// deterministic, so the merge result is deterministic.
func mergeConsumerEnvOverrides(state *BoundaryFRState, overrides map[string]map[string]string) {
	if state == nil || len(overrides) == 0 {
		return
	}
	for nodeID, envs := range overrides {
		dest, ok := state.ConsumerEnvOverrides[nodeID]
		if !ok {
			dest = map[string]string{}
			state.ConsumerEnvOverrides[nodeID] = dest
		}
		for k, v := range envs {
			dest[k] = v
		}
	}
}

// evaluateBoundaryFRTemplates evaluates a boundary-scope FR's locals and
// provides templates against the FR-scoped context, storing the resolved
// provides into state.Provides under key (provides feed the cross-boundary
// consumer cascade, a boundary-FR-specific concern).
//
// The FR's valuesMapping is evaluated separately at the dispatch site
// (dispatchBoundaryScopeFR) through the shared ExecutePhaseBForFeature — the
// same evaluator the per-component FR path uses — and its output drives
// engine.Render. A valuesMapping-less FR resolves to the flattened .values
// pillar unchanged, so it renders identically either way.
func evaluateBoundaryFRTemplates(
	fr *types.FeatureRecipe,
	frInstance string,
	frCtx *types.TemplateContext,
	state *BoundaryFRState,
	key boundaryFRKey,
) error {
	// Evaluate locals.
	resolvedLocals := types.OrderedMap[string]{}
	for k, tmplText := range fr.Spec.Locals.All() {
		out, err := EvalTemplate("fr-local["+frInstance+"."+k+"]", tmplText, frCtx)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("boundary-scope FR %q locals[%q]", frInstance, k))
		}
		resolvedLocals.Set(k, out)
	}
	frCtx.Locals = resolvedLocals

	// Evaluate provides — store into state.Provides for cascade-aware
	// consumer resolution. Uses the same EvalTemplate (Sprig-enabled,
	// missingkey=zero) as per-component FR provides today.
	provided := map[string]any{}
	for k, tmplText := range fr.Spec.Provides.All() {
		out, err := EvalTemplate("fr-provides["+frInstance+"."+k+"]", tmplText, frCtx)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("boundary-scope FR %q provides[%q]", frInstance, k))
		}
		provided[k] = out
	}
	state.Provides[key] = provided
	return nil
}

// buildBoundaryContext walks the Release's boundary tree to find the
// boundary at boundaryPath, then assembles the BoundaryContext that the
// boundary-scope FR template evaluation reads.
//
// The function populates the documented BoundaryContext pillars:
//
//   - Path: the boundary's evaluated directory (read from any subtree
//     node's BoundaryInfra; falls back to the boundaryPath verbatim).
//   - BoundaryName: the last segment of the boundary path (the map key
//     in release.spec.boundaries).
//   - Components: every node in the boundary subtree (recursive),
//     captured as instance_id → opaque ComponentInfo (resolved type URI,
//     instance ID, provided values).
//   - InternalUsesEdges, OutgoingCrossBoundaryUsesEdges,
//     IncomingCrossBoundaryUsesEdges, CrossBoundaryUsesEdges — derived by
//     intersecting graph.Spec.Edges with subtree node membership.
//   - Labels: union of subtree nodes' Labels with author-order preservation
//     (alphabetical-by-key key order; OrderedMap iteration preserves
//     insertion order).
//   - Annotations: read from boundaryAnnotations[boundaryPath] (the
//     resolver-side cascade-resolved set).
//   - FeatureAttachments: features directly attached to the boundary,
//     parsed from boundary.Features via parseBoundaryFeature equivalent.
func buildBoundaryContext(
	allProvides NodeProvides,
	boundaryPath string,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
) (*types.BoundaryContext, error) {
	if release == nil {
		return nil, cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{}, "buildBoundaryContext: release is nil", nil, nil)
	}
	parts := strings.Split(boundaryPath, "/")
	if _, ok := findBoundaryAtPath(release.Spec.Boundaries, parts); !ok {
		return nil, cerrors.New(cerrors.OpsError, cerrors.SourceLocation{File: boundaryPath}, fmt.Sprintf("buildBoundaryContext: boundary %q not found in release", boundaryPath), nil, nil)
	}

	bc := &types.BoundaryContext{
		Path:         boundaryPath,
		BoundaryName: parts[len(parts)-1],
	}
	if bc.BoundaryName == "" && len(parts) > 1 {
		bc.BoundaryName = parts[len(parts)-2]
	}

	// Subtree node membership: every node whose BoundaryPath is the
	// boundary's path or descends from it.
	subtreeMembers := map[string]bool{}
	componentsMap := types.OrderedMap[types.ComponentInfo]{}
	if graph != nil {
		// Populate in alphabetical-by-nodeID order for output determinism;
		// graph.Spec.Nodes.All() preserves author order, but the subtree
		// filter collapses across cross-boundary nodes, so a deterministic
		// alphabetical order at the boundary view is the safer choice.
		nodeIDs := graph.Spec.Nodes.Keys()
		sort.Strings(nodeIDs)
		for _, nodeID := range nodeIDs {
			node, _ := graph.Spec.Nodes.Get(nodeID)
			if isInSubtree(node.BoundaryPath, boundaryPath) {
				subtreeMembers[nodeID] = true
				componentsMap.Set(nodeID, types.ComponentInfo{
					InstanceID:      nodeID,
					Component:       node.Component,
					ResolvedVersion: node.ResolvedVersion,
					BoundaryPath:    node.BoundaryPath,
				})
			}
		}
	}
	bc.Components = componentsMap

	// Edge enumeration.
	if graph != nil {
		for _, edge := range graph.Spec.Edges {
			srcIn := subtreeMembers[edge.Source]
			tgtIn := subtreeMembers[edge.Target]
			edgeView := EnrichedEdgeViewFromIREdge(edge, allProvides)
			// Per-endpoint network inputs (pure data): the endpoints' boundary
			// paths and raw aggrega.ai/namespace annotation values, so a provider
			// engine can resolve each endpoint's network and decide whether the
			// edge crosses a seam. No interpretation here (Invariant 1/6).
			edgeView.SourceBoundaryPath, edgeView.SourceNamespace = edgeEndpointNetworkInputs(edge.Source, graph, boundaryAnnotations)
			edgeView.TargetBoundaryPath, edgeView.TargetNamespace = edgeEndpointNetworkInputs(edge.Target, graph, boundaryAnnotations)
			switch {
			case srcIn && tgtIn:
				bc.InternalUsesEdges = append(bc.InternalUsesEdges, edgeView)
			case srcIn && !tgtIn:
				bc.OutgoingCrossBoundaryUsesEdges = append(bc.OutgoingCrossBoundaryUsesEdges, edgeView)
				bc.CrossBoundaryUsesEdges = append(bc.CrossBoundaryUsesEdges, edgeView)
			case !srcIn && tgtIn:
				bc.IncomingCrossBoundaryUsesEdges = append(bc.IncomingCrossBoundaryUsesEdges, edgeView)
				bc.CrossBoundaryUsesEdges = append(bc.CrossBoundaryUsesEdges, edgeView)
			}
		}
	}

	// Labels: union across subtree nodes (alphabetical-by-key for
	// determinism since multiple nodes contribute).
	labels := types.OrderedMap[string]{}
	if graph != nil {
		labelKeys := map[string]string{}
		for _, node := range graph.Spec.Nodes.All() {
			if !subtreeMembers[graph.Metadata.AggregateName+"-"+node.Component] {
				// Subtree filter applies to nodeID, not component name;
				// re-iterate via Keys() and Get().
				_ = node
			}
		}
		// Re-iterate alphabetically over subtree node IDs to populate
		// labels in a deterministic order.
		nodeIDs := graph.Spec.Nodes.Keys()
		sort.Strings(nodeIDs)
		for _, nid := range nodeIDs {
			if !subtreeMembers[nid] {
				continue
			}
			node, _ := graph.Spec.Nodes.Get(nid)
			for k, v := range node.Labels.All() {
				if _, exists := labelKeys[k]; !exists {
					labelKeys[k] = v
					labels.Set(k, v)
				}
			}
		}
	}
	bc.Labels = labels

	// Annotations from the resolver-side cascade.
	if ann, ok := boundaryAnnotations[boundaryPath]; ok {
		bc.Annotations = ann
	}

	// Feature attachments: the boundary's Feature attachments read from the
	// IR projection (the IR builder parsed them once at build; no Release
	// re-walk here).
	if graph != nil {
		if inner, ok := graph.Spec.BoundaryFeatures.Get(boundaryPath); ok {
			for fname, bf := range inner.All() {
				bc.FeatureAttachments = append(bc.FeatureAttachments, types.TemplateFeature{
					Type:   bf.Type,
					Values: types.OrderedMap[any]{},
					Infra:  bf.Infra,
				})
				_ = fname
			}
		}
	}

	return bc, nil
}

// EnrichedEdgeViewFromIREdge converts an IREdge to a renderer-side
// EnrichedEdge view. The boundary-scope FR templates consume edges via
// the same EnrichedEdge shape used elsewhere in the renderer; this
// converter projects the IR edge's Source/Target/Socket/Alias onto the
// renderer view so templates can reference `.boundary.*_uses_edges[].
// target` etc.
func EnrichedEdgeViewFromIREdge(edge types.IREdge, allProvides NodeProvides) types.EnrichedEdge {
	view := types.EnrichedEdge{
		Source:        edge.Source,
		Target:        edge.Target,
		Socket:        edge.Socket,
		OriginFeature: edge.OriginFeature,
		Inject:        edge.Inject,
	}
	if p, ok := allProvides[edge.Target]; ok {
		view.Provides = p
	}
	return view
}

// edgeEndpointNetworkInputs returns the boundary path and raw
// aggrega.ai/namespace annotation value for an edge endpoint node, as pure
// data for a provider engine's network-identity resolution. Empty strings when
// the node or annotation is absent. The core does not resolve these into a
// network name — that (sanitize + fallback + compare) is provider-domain.
func edgeEndpointNetworkInputs(nodeID string, graph *types.IRGraph, boundaryAnnotations map[string]types.OrderedMap[string]) (boundaryPath, namespace string) {
	if graph == nil {
		return "", ""
	}
	n, ok := graph.Spec.Nodes.Get(nodeID)
	if !ok {
		return "", ""
	}
	boundaryPath = n.BoundaryPath
	if anns, ok := boundaryAnnotations[boundaryPath]; ok {
		namespace, _ = anns.Get(AnnotationNamespace)
	}
	return boundaryPath, namespace
}

// findBoundaryAtPath traverses release.Spec.Boundaries by the path
// segments. Returns (boundary, true) on hit; (zero, false) on miss.
func findBoundaryAtPath(boundaries types.OrderedMap[types.Boundary], parts []string) (types.Boundary, bool) {
	if len(parts) == 0 {
		return types.Boundary{}, false
	}
	current := boundaries
	var found types.Boundary
	for _, part := range parts {
		b, ok := current.Get(part)
		if !ok {
			return types.Boundary{}, false
		}
		found = b
		current = b.Boundaries
	}
	return found, true
}

// isInSubtree reports whether candidate is the boundaryPath itself or
// descends from it. Subtree membership is exact-match-or-prefix-with-slash.
func isInSubtree(candidate, boundaryPath string) bool {
	if candidate == boundaryPath {
		return true
	}
	return strings.HasPrefix(candidate, boundaryPath+"/")
}

// findFRAttachmentBoundary walks from componentBoundaryPath up the boundary
// tree (toward the root) and returns the path of the FIRST ancestor (or
// componentBoundaryPath itself) whose `Features` map includes frInstance.
// Used by Phase B.6 to compute the dedup key for boundary-scope FRs whose
// attachment lives on an ancestor of the component's containing boundary
// (Tier-4 propagation in the IR builder cascades the feature down to every
// component in the subtree, but the orchestrator-side dedup key is the
// owning boundary, not the component's containing boundary).
//
// Falls back to componentBoundaryPath when no ancestor carries the
// attachment — the boundary-scope dispatch then surfaces a precise
// "no BoundaryFeature attachment" error rather than silently misattributing.
func findFRAttachmentBoundary(boundaryFeatures types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]], componentBoundaryPath, frInstance string) string {
	if componentBoundaryPath == "" {
		return componentBoundaryPath
	}
	parts := strings.Split(componentBoundaryPath, "/")
	for i := len(parts); i >= 1; i-- {
		candidatePath := strings.Join(parts[:i], "/")
		if inner, ok := boundaryFeatures.Get(candidatePath); ok {
			if _, hasFeat := inner.Get(frInstance); hasFeat {
				return candidatePath
			}
		}
	}
	return componentBoundaryPath
}

// lookupBoundaryFeatureAttachment finds the BoundaryFeature with the given
// instance name on the boundary at boundaryPath. Returns (zero, false)
// when the boundary or feature instance is absent. The boundary's
// Features map is OrderedMap[any]; this function parses the raw
// payload into a typed BoundaryFeature.
func lookupBoundaryFeatureAttachment(boundaryFeatures types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]], boundaryPath, frInstance string) (types.IRBoundaryFeature, bool) {
	inner, ok := boundaryFeatures.Get(boundaryPath)
	if !ok {
		return types.IRBoundaryFeature{}, false
	}
	return inner.Get(frInstance)
}

// resolveBoundaryScopeFRProvides walks the consumer's BoundaryPath up the
// boundary tree to find the boundary holding the FR-instance attachment;
// returns the resolved provides values + true on first match.
//
// Cascade walk: split consumer's boundary path on "/"; iterate from full
// path back to root; at each level, check state.Provides for
// (level, frInstance). Return on first hit.
//
// Wiring status: this function is declared and ready, but
// RehydrateInjectFromProvides at inject.go does NOT yet call it. The
// wiring requires a (Feature-type-URI → FR-instance-name) resolution
// path that today's IREdge.OriginFeature (carries the catalog Feature
// TYPE URI, not the per-attachment instance name) doesn't supply
// directly. The instance-name derivation depends on walking the
// per-component feature cascade to identify which attachment fired
// the edge — a substantive change to the topology/IR pipeline that
// belongs in a follow-up driven by a real consumer-side fixture.
//
// Practical consequence today: a per-component consumer that references
// .context.uses.<feat>.<provided> for a feat whose FR has ExecutionScope
// == "boundary" will NOT find the provides via the existing per-component
// allProvides lookup. The provides become silently empty (missingkey=zero
// renders as empty string in value templates).
//
// The compose-bridge proof case does not actively
// consume boundary-scope FR provides via per-component edges — the
// bridge service's behavior is driven by Phase A injectMapping (which
// fans per-consumer regardless of FR scope) plus the assembled compose
// fragment's own service definitions. The latent gap surfaces only if a
// future Release authors a per-component `uses:` consuming a boundary-
// scope FR's `provides:` value via cross-edge resolution.
//
// When that fixture surfaces, the wire-up is: extend
// RehydrateInjectFromProvides to accept a *BoundaryFRState +
// RecipeLookup; per-edge with OriginFeature set, look up FR scope; when
// boundary-scope, derive the FR-instance-name via the per-component
// cascade attachment record (requires threading or recomputing the
// attachment view); call resolveBoundaryScopeFRProvides; merge results
// into the targetProvides view feeding resolveInjectKeys.
func resolveBoundaryScopeFRProvides(
	state *BoundaryFRState,
	consumerBoundaryPath, frInstance string,
) (map[string]any, bool) {
	if state == nil {
		return nil, false
	}
	parts := strings.Split(consumerBoundaryPath, "/")
	for i := len(parts); i > 0; i-- {
		candidate := strings.Join(parts[:i], "/")
		key := boundaryFRKey{BoundaryPath: candidate, FRInstance: frInstance}
		if provides, ok := state.Provides[key]; ok {
			return provides, true
		}
	}
	return nil, false
}
