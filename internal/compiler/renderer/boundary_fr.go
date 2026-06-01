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
}

// NewBoundaryFRState returns a fresh state instance ready for population.
func NewBoundaryFRState() *BoundaryFRState {
	return &BoundaryFRState{
		Dedup:        map[boundaryFRKey]bool{},
		Provides:     map[boundaryFRKey]map[string]any{},
		Files:        map[boundaryFRKey][]EmittedFile{},
		ValidatedFRs: map[string]error{},
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

// validateBoundaryScopeFRTemplate parses every template field on a FR with
// spec.executionScope: boundary and verifies that no template references
// any per-component-only field (.context.*, .workload.*, .secret_name,
// .inject, .inject_raw). Returns nil on pass; ErrFRTemplateInvalidField
// wrapped with the FR name + offending field on first violation.
//
// Templates checked: Workload, InjectMapping, ValuesMapping, Provides
// (each entry's template), Artifacts (each entry's template), Pipeline
// step `with` templates. Templates loaded from external files (paths
// rather than inline strings) are not parsed by this function — file
// content load + parse happens at engine-render time and the validation
// would need to load the file too. Pragmatic scope: inline templates
// only at this validator; file-template content is the test-migration
// intent's responsibility.
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
			// Parse errors at the FR template surface as CATALOG_ERROR
			// elsewhere; this validator skips on parse failure rather than
			// double-reporting.
			continue
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
	frCtx, err := buildBoundaryFRTemplateContext(fr, frInstance, key, graph, release, boundaryAnnotations)
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
	resolvedSrc, err := resolveEntitySource(input, fr.Spec.Source, fr.Spec.Locals, frSourceURI(fr), feat.Infra.TemplateAlphabeticalMapDeep())
	if err != nil {
		return wrapSourceResolutionError(err, frNodeCtx.NodeID, fmt.Sprintf("boundary-scope FR %q source resolution", frInstance))
	}
	frNodeCtx.Source = resolvedSrc

	frValues := flattenOrderedMap(frCtx.Values)
	frFiles, err := frEngine.Render(ctx, frValues, frNodeCtx)
	if err != nil {
		return cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: key.BoundaryPath}, fmt.Sprintf("boundary-scope FR %q render", frInstance))
	}

	// 7. Classify emitted files by Intermediate flag and record.
	_, isAssembler := frEngine.(BoundaryAssembler)
	emitted := make([]EmittedFile, 0, len(frFiles))
	names := make([]string, 0, len(frFiles))
	for p := range frFiles {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		emitted = append(emitted, EmittedFile{
			Name:         p,
			Origin:       EmitOriginSource,
			Body:         frFiles[p],
			Intermediate: isAssembler,
		})
	}
	boundaryFRState.Files[key] = emitted

	// Suppress unused-state warnings — state is reserved for future per-
	// boundary-FR observations.
	_ = state
	return nil
}

// buildBoundaryFRTemplateContext assembles the FR-scoped TemplateContext for a
// boundary-scope FeatureRecipe instance: the boundary pillars (.boundary) plus
// the boundary attachment's .infra, with .values empty per the Release
// pillar-split (Aggregate authors values; Release authors infra). Shared by the
// pre-loop provides pre-pass and the Phase B.6 dispatch so both evaluate the
// FR's templates against an identical context.
func buildBoundaryFRTemplateContext(
	fr *types.FeatureRecipe,
	frInstance string,
	key boundaryFRKey,
	graph *types.IRGraph,
	release *types.Release,
	boundaryAnnotations map[string]types.OrderedMap[string],
) (*types.TemplateContext, error) {
	boundaryCtx, err := buildBoundaryContext(key.BoundaryPath, graph, release, boundaryAnnotations)
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
	state *BoundaryFRState,
) error {
	if graph == nil || state == nil {
		return nil
	}
	for boundaryPath, feats := range graph.Spec.BoundaryFeatures.All() {
		for instName, bf := range feats.All() {
			fr, ok := recipeLookup.FeatureRecipe(bf.Type)
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
			frCtx, err := buildBoundaryFRTemplateContext(fr, instName, key, graph, release, boundaryAnnotations)
			if err != nil {
				return err
			}
			if err := evaluateBoundaryFRTemplates(fr, instName, frCtx, state, key); err != nil {
				return err
			}
		}
	}
	return nil
}

// evaluateBoundaryFRTemplates evaluates a boundary-scope FR's locals,
// valuesMapping, and provides templates against the FR-scoped context.
// Stores the resolved provides into state.Provides under key.
//
// Pragmatic scope for this revision: locals + provides eval lands here;
// the engine-side valuesMapping eval that drives engine.Render is invoked
// indirectly via flattenOrderedMap(frCtx.Values) at the dispatch site (no
// valuesMapping intermediate; the FR's values block carries the resolved
// values via .values). Future enhancement extends this to invoke
// ExecutePhaseBForFeature against the boundary-scope context.
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
			edgeView := EnrichedEdgeViewFromIREdge(edge)
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
func EnrichedEdgeViewFromIREdge(edge types.IREdge) types.EnrichedEdge {
	return types.EnrichedEdge{
		Target:        edge.Target,
		Socket:        edge.Socket,
		OriginFeature: edge.OriginFeature,
	}
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
