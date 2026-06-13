// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pipeline orchestrates the 5-step compiler pipeline from already-
// loaded inputs to in-memory artifacts. It carries no CLI dependency: no
// flag parsing, no dist/ writing, no .trace/ writing. The CLI wrapper at
// cmd/aggrega/build.go composes catalog loading and dist/ writing around
// RunPipeline; a long-running daemon invokes RunPipeline directly with its
// own in-memory inputs (parse-cached Release and Manifest, cached raw
// Aggregate bytes, pre-constructed catalog lookup callbacks).
//
// RunPipeline performs no filesystem reads beyond the operations the
// caller-supplied lookup callbacks perform. The Aggregate path on Inputs
// is a source-location label only; it is not re-read.
package pipeline

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Inputs carry the caller-supplied state RunPipeline needs to execute the
// 5-step pipeline. Release and Manifest are pre-parsed typed values;
// AggregateRawBytes is the raw YAML the resolver interpolates with the
// merged parameter map. AggregatePath is a source-location label used in
// resolver errors and annotation-provenance entries; RunPipeline performs
// no filesystem operations against it.
type Inputs struct {
	Release           *types.Release
	Manifest          *types.Manifest
	AggregateRawBytes []byte
	AggregatePath     string
	WorkspaceConfig   workspaceconfig.Config

	// Lockfile and WorkspaceRoot, together with WorkspaceConfig's resolved
	// cache directory, enable build-time source-artifact resolution: when
	// Lockfile is non-nil and WorkspaceRoot is set, RunPipeline populates the
	// renderer's source-resolution inputs so a remote Recipe (or FeatureRecipe)
	// source resolves to its lockfile-pinned, content-addressed cache entry and
	// renders. When Lockfile is nil or WorkspaceRoot is empty, the renderer
	// forwards each source verbatim — the sourceless and local-path paths.
	Lockfile      *lockfile.Lockfile
	WorkspaceRoot string

	// MappingLibraries is the resolved MappingLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It
	// flows to the renderer so a Recipe / FeatureRecipe valuesMapping's
	// bare-name `$import: <Library>#/mappings/<entry>` resolves at render. nil
	// when no MappingLibraries are present.
	MappingLibraries map[string]types.MappingLibrary
}

// Options carries per-invocation flags. IncludeTrace, when true, causes
// RunPipeline to construct an in-memory trace writer and populate
// Result.Trace with the collected per-pass entries; when false,
// Result.Trace stays nil and the trace writer is a no-op.
type Options struct {
	IncludeTrace bool
	// RecipeResolutionVerifier, when set, runs after IR-build and before render
	// to assert every component/feature instance resolves to exactly one recipe
	// (halting loudly on a no-recipe instance or an unmapped ambiguously-
	// implemented type). Left nil — e.g. by callers that build the IR for
	// inspection rather than render — the check is skipped. Catalog supplies
	// VerifyRecipeResolution, whose signature this matches.
	RecipeResolutionVerifier func(*types.Manifest, *types.IRGraph) error
}

// Result carries the pipeline's outputs. Artifacts is the rendered file
// map the CLI wrapper writes to dist/. IRGraph is the step-3 output the
// daemon's irGraph operation returns directly. Trace is the per-pass
// intermediate state, empty unless Options.IncludeTrace is true.
type Result struct {
	Artifacts map[string][]byte
	IRGraph   *types.IRGraph
	Trace     map[string][]byte
}

// Tier4Result carries the outputs of Steps 1-4 of the compiler pipeline,
// produced by RunTier4Pipeline. The daemon's runTier4Validation consumes
// this shape to surface structured per-violation diagnostics through
// aggrega/validate. IRGraph is populated when Step 3 ran; Analysis is
// populated when Step 4 ran. Earlier phases that produced fatal errors
// leave later phases skipped (the returned error is non-nil), but the
// partial results from the phases that ran are still surfaced so the
// caller can construct diagnostics from whichever phases completed.
//
// ResolveDiagnostics carries the per-violation slice extracted from the
// resolver's error return. Populated for both single-violation and
// multi-violation cases: single-violation wraps the embedded
// *ResolverError as a one-element slice; multi-violation pulls the
// preserved Violations slice from MultiResolverError. Empty only when
// Step 1 succeeded (the function-level error is nil) or when the error
// chain carries no *ResolverError at all. Tier-4 position-recovery
// walks the slice with a uniform code path regardless of cardinality.
type Tier4Result struct {
	IRGraph            *types.IRGraph
	Analysis           *analyzer.AnalysisResult
	ResolveDiagnostics []*resolver.ResolverError
}

// stepsOneToFourOutput bundles the un-purged outputs that runStepsOneToFour
// produces. Both RunTier4Pipeline and RunPipeline call the helper and own
// the resolveResult.Parameters purge themselves so each exported function's
// purge-on-exit discipline stays self-contained.
type stepsOneToFourOutput struct {
	ResolveResult *resolver.ResolveResult
	Topology      *topology.TopologyResult
	IRGraph       *types.IRGraph
	FeatureOrder  ir.FeatureOrder
	Analysis      *analyzer.AnalysisResult
}

// runStepsOneToFour executes Steps 1-4 of the compiler pipeline (resolve,
// topology, IR finalisation, analyse) against caller-supplied inputs and
// returns the un-purged ResolveResult alongside the phase outputs. The
// caller owns parameter-map purge via its own defer. Used by both
// RunTier4Pipeline (which discards Step 5) and RunPipeline (which feeds
// the rendering step from the same outputs).
//
// Behavior on phase failure:
//   - Step 1 sub-pass failures return (out, error) with whichever sub-pass
//     outputs were captured before the failure. resolveResult may be nil
//     when the very first sub-pass (ResolveFromInputs) failed.
//   - Step 2/3 failures return populated resolveResult but nil downstream
//     phases.
//   - Step 4 (analyzer) errors do NOT surface as a function-level error.
//     The structured AnalysisResult.Errors slice is returned in out.Analysis;
//     callers decide whether to treat analyzer errors as fatal or
//     structured diagnostics. This preserves the daemon's Tier-4 contract.
func runStepsOneToFour(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	subAggregateLoader topology.SubAggregateLoader,
) (*stepsOneToFourOutput, error) {
	out := &stepsOneToFourOutput{}

	if err := ctx.Err(); err != nil {
		return out, err
	}

	// Steps 1-2: resolve + topology flatten. Shared with `aggrega pull`'s
	// per-component source resolution via ResolveAndFlatten, so lock-time
	// source-descent and build-time rendering run one resolve + topology +
	// recipe-selection path (no drift).
	resolveResult, topoResult, err := ResolveAndFlatten(ctx, inputs, catalogLookup, subAggregateLoader)
	out.ResolveResult = resolveResult // non-nil once resolve succeeded, even on later failure (caller purges)
	out.Topology = topoResult
	if err != nil {
		return out, err
	}

	// Step 3: IR.
	graph, featureOrder, err := ir.BuildIRContext(ctx, ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		return out, fmt.Errorf("Step 3 (IR): %w", err)
	}
	out.IRGraph = graph
	out.FeatureOrder = featureOrder

	if err := ctx.Err(); err != nil {
		return out, err
	}

	// Step 4: Analyze. Analyser errors surface as structured
	// AnalysisResult.Errors; the function-level error stays nil for
	// analyzer-only failures so Tier-4 callers can consume them as
	// diagnostics. Build / irgraph callers convert AnalysisResult.Errors
	// to the joined-error shape via RunPipeline's wrapper logic.
	analysis, err := analyzer.AnalyzeContext(ctx, graph, catalogLookup)
	if err != nil {
		return out, err
	}
	out.Analysis = analysis
	for _, w := range analysis.Warnings {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}
	return out, nil
}

// ResolveAndFlatten runs Steps 1-2 of the compiler pipeline — resolve
// (parameter interpolation, annotation binding validation + expansion,
// guards) then topology flatten — and returns the resolved per-component
// view: ResolveResult (carrying per-component BoundaryInfra) and the
// flattened TopologyResult (nodes with InstanceID / ComponentType / Values).
//
// It is the single source of the resolve+flatten wiring. The build pipeline
// (runStepsOneToFour) and `aggrega pull`'s per-component source resolution
// both call it, so lock-time source-descent cannot drift from build-time
// component / recipe / infra resolution — recipe selection then runs through
// the same catalog RecipeLookup at both sites.
//
// resolveResult is returned non-nil once ResolveFromInputs succeeds, even when
// a later Step-1 sub-pass or the topology step fails, so the caller can purge
// the parameter map. The caller owns resolver.PurgeParameters.
func ResolveAndFlatten(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	subAggregateLoader topology.SubAggregateLoader,
) (*resolver.ResolveResult, *topology.TopologyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	resolveResult, err := resolver.ResolveFromInputs(resolver.ResolveInputs{
		Release:           inputs.Release,
		Manifest:          inputs.Manifest,
		AggregateRawBytes: inputs.AggregateRawBytes,
		AggregatePath:     inputs.AggregatePath,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Step 1 (resolve): %w", err)
	}

	if err := resolver.ValidateAnnotationBindingTargets(resolveResult.Manifest, catalogLookup); err != nil {
		return resolveResult, nil, fmt.Errorf("Step 1 (annotation binding validation): %w", err)
	}
	for _, w := range resolver.LintAnnotationBindingMaps(resolveResult.Manifest) {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}
	if err := resolver.ExpandAnnotations(resolveResult, catalogLookup); err != nil {
		return resolveResult, nil, fmt.Errorf("Step 1 (annotation expansion): %w", err)
	}

	guardErrs := resolver.RunGuards(types.Normalize(resolveResult.Aggregate), resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra, resolveResult.Parameters)
	if len(guardErrs) > 0 {
		for _, e := range guardErrs {
			fmt.Fprintf(os.Stderr, "  guard: %s\n", e)
		}
		// Carry the per-violation slice on the returned error so a caller can
		// errors.Is the specific guard sentinel at the pipeline boundary
		// (parity with the analyzer path, which joins). The summary text is
		// unchanged: MultiResolverError.Error() renders "Step 1: N guard
		// violation(s)".
		return resolveResult, nil, &resolver.MultiResolverError{
			Entity:     "Step 1",
			Summary:    fmt.Sprintf("%d guard violation(s)", len(guardErrs)),
			Violations: guardErrs,
		}
	}

	if err := ctx.Err(); err != nil {
		return resolveResult, nil, err
	}

	topoResult, err := topology.BuildContext(ctx, topology.BuildConfig{
		ResolveResult:     resolveResult,
		CatalogLookup:     catalogLookup,
		LoadSubAggregate:  subAggregateLoader,
		RootAggregateFile: inputs.AggregatePath,
	})
	if err != nil {
		return resolveResult, nil, fmt.Errorf("Step 2 (topology): %w", err)
	}

	return resolveResult, topoResult, nil
}

// RunTier4Pipeline executes Steps 1-4 of the compiler pipeline (resolve,
// topology, IR finalisation, analyse) against caller-supplied inputs.
// The daemon's runTier4Validation consumes this to surface structured
// per-violation diagnostics through aggrega/validate without running
// Step 5 (render), which is expensive and not needed for semantic
// validation.
//
// Unlike RunPipeline's first-error-fatal semantic for terminal phases,
// the returned Tier4Result carries whichever phases completed even when
// a later phase produced an error. Callers consume the analyser's
// structured errors (Analysis.Errors) directly rather than the joined
// error return. The returned error is non-nil iff a phase produced a
// fatal error that blocked downstream phases; analyzer errors are NOT
// returned as the function-level error because the caller wants them
// as structured diagnostics, not opaque error text.
func RunTier4Pipeline(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	subAggregateLoader topology.SubAggregateLoader,
) (*Tier4Result, error) {
	stepsOut, err := runStepsOneToFour(ctx, inputs, catalogLookup, subAggregateLoader)
	if stepsOut != nil && stepsOut.ResolveResult != nil {
		// Purge the merged parameter map as RunTier4Pipeline exits so the
		// lifetime matches the rendering pass exactly. Owned per-exported-
		// function to keep the discipline at each API boundary.
		defer resolver.PurgeParameters(&stepsOut.ResolveResult.Parameters)
	}

	out := &Tier4Result{
		IRGraph:  stepsOut.IRGraph,
		Analysis: stepsOut.Analysis,
	}

	// Extract the structured per-violation slice into a uniform shape so
	// downstream consumers iterate ResolveDiagnostics with one code path
	// regardless of single-vs-multi-violation cardinality.
	//
	// Single-violation case: extract the embedded *ResolverError directly
	// and wrap as a one-element slice. Multi-violation case: pull the
	// preserved Violations slice from MultiResolverError. The two
	// extractions are mutually exclusive — MultiResolverError contains
	// ResolverErrors and the multi check runs first; the single check
	// never matches inside a multi.
	if err != nil {
		var multi *resolver.MultiResolverError
		var single *resolver.ResolverError
		switch {
		case stderrors.As(err, &multi):
			out.ResolveDiagnostics = multi.Violations
		case stderrors.As(err, &single):
			out.ResolveDiagnostics = []*resolver.ResolverError{single}
		}
	}

	return out, err
}

// RunPipeline executes the 5-step compiler pipeline against caller-supplied
// inputs and returns the rendered artifact map plus the IR graph. The
// function performs no filesystem reads or writes beyond the operations
// the supplied lookup callbacks perform; the caller is responsible for
// any filesystem-side effects (writing artifacts to dist/, writing the
// trace map to .trace/).
//
// Mid-pipeline warnings (binding-lint advisories, analyzer warnings,
// renderer warnings) are written to os.Stderr inline by this function;
// daemons that need to capture or silence them redirect os.Stderr at the
// process boundary.
//
// The parameter map merged from Release.spec.parameters flows through to
// the renderer (for orchestrator-time path-template evaluation). Active-
// purge runs as RunPipeline exits via a deferred PurgeParameters call —
// the wrapper does not need to remember to purge.
//
// RunPipeline shares Steps 1-4 with RunTier4Pipeline through the
// unexported runStepsOneToFour helper, then runs Step 4b + Step 5.
// First-error-fatal semantic is preserved for terminal phases:
// analyser errors land as a joined error return so existing irgraph +
// CLI build callers see the flattened error shape they already consume.
func RunPipeline(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
	subAggregateLoader topology.SubAggregateLoader,
	engineFactory renderer.EngineFactory,
	opts Options,
) (*Result, error) {
	stepsOut, err := runStepsOneToFour(ctx, inputs, catalogLookup, subAggregateLoader)
	if stepsOut != nil && stepsOut.ResolveResult != nil {
		defer resolver.PurgeParameters(&stepsOut.ResolveResult.Parameters)
	}
	if err != nil {
		return nil, err
	}

	// Step 4: convert analyzer errors to the joined-error shape that
	// pre-extraction RunPipeline returned. Preserves the first-error-fatal
	// semantic existing callers (irgraph, CLI build) consume.
	if len(stepsOut.Analysis.Errors) > 0 {
		errs := make([]error, len(stepsOut.Analysis.Errors))
		for i := range stepsOut.Analysis.Errors {
			errs[i] = stepsOut.Analysis.Errors[i].ToCompilerError()
		}
		return nil, stderrors.Join(errs...)
	}

	resolveResult := stepsOut.ResolveResult
	graph := stepsOut.IRGraph
	featureOrder := stepsOut.FeatureOrder

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Step 4b: Component / Feature assertions.
	if err := renderer.EvaluateComponentAndFeatureAssertions(graph, catalogLookup); err != nil {
		return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, "Step 4b (Component/Feature assertions)")
	}

	// Step 4c: resolution-completeness — every component/feature instance must
	// resolve to exactly one recipe before render. Skipped when the caller does
	// not supply a verifier (IR-inspection paths that do not render).
	if opts.RecipeResolutionVerifier != nil {
		if err := opts.RecipeResolutionVerifier(inputs.Manifest, graph); err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "Step 4c (recipe resolution)")
		}
	}

	// Step 5: Render. Trace writer selection: in-memory collection when
	// IncludeTrace is true (caller surfaces or writes the map); no-op
	// otherwise.
	var memWriter trace.MemoryWriter
	var traceWriter trace.Writer
	if opts.IncludeTrace {
		memWriter = trace.NewMemory()
		traceWriter = memWriter
	} else {
		traceWriter = trace.New("")
	}
	defer func() {
		if cerr := traceWriter.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "  trace: close: %s\n", cerr)
		}
	}()

	rendererInput := renderer.RendererInput{
		Graph:            graph,
		FeatureOrder:     featureOrder,
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		MappingLibraries: inputs.MappingLibraries,
		Trace:            traceWriter,
		YAMLIndent:       inputs.WorkspaceConfig.Build.YAMLIndent,
	}
	// Build-time source-artifact resolution: when the caller supplies a
	// lockfile and workspace root, the orchestrator resolves each remote
	// source to its pinned cache entry. Left unset, the renderer forwards
	// sources verbatim (sourceless / local-path paths).
	if inputs.Lockfile != nil && inputs.WorkspaceRoot != "" {
		rendererInput.Lockfile = inputs.Lockfile
		rendererInput.WorkspaceRoot = inputs.WorkspaceRoot
		rendererInput.CacheDir = inputs.WorkspaceConfig.Workspace.ResolvedCacheDir()
	}

	renderResult, err := renderer.RenderAll(ctx, rendererInput, recipeLookup, engineFactory)
	if err != nil {
		return nil, fmt.Errorf("Step 5 (render): %w", err)
	}

	for _, w := range renderResult.Warnings {
		fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
	}

	out := &Result{
		Artifacts: renderResult.Files,
		IRGraph:   graph,
	}
	if memWriter != nil {
		out.Trace = memWriter.Entries()
	}
	return out, nil
}
