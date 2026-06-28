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
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
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
	// ReleasePath is a source-location label only (no I/O), forwarded into the
	// resolver so the annotation-expansion pass can build Release-authored
	// annotation source locations. Optional: callers that leave it empty get an
	// empty release path in those source-location strings, unchanged from before.
	ReleasePath string
	// RootAggregateFile is the source-location label stamped onto the flattened
	// root-tier topology node (and carried to IRNode.SourceFile) — a label only,
	// no I/O. It is distinct from AggregatePath: AggregatePath drives the
	// resolver's annotation source-location strings, while this drives the node
	// source stamp, and a caller may need different representations for the two
	// (inspect uses a sibling-resolved AggregatePath for byte-identical annotation
	// brackets but a workspace-relative RootAggregateFile for a machine-stable
	// node stamp). Optional: when empty, ResolveAndFlatten defaults it to
	// AggregatePath, so callers that supply a single workspace-relative path are
	// unchanged.
	RootAggregateFile string
	WorkspaceConfig   workspaceconfig.Config

	// CompilerVersion is the build identity stamped into IR provenance
	// (IRGraph.Spec.Provenance.CompilerVersion). The command layer supplies the
	// binary's version string — the pipeline pipes it through to buildProvenance
	// without deriving it, so a build-from-source and a released build stamp
	// distinct identities while the value stays injectable for deterministic
	// tests. Empty when a caller does not stamp provenance (the field carries no
	// default; an unset value renders as an empty compiler-version).
	CompilerVersion string

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

	// TolerateCatalogViewInconsistency opts a caller out of the guard-layer
	// halt for a component type that resolved AS A CATALOG REFERENCE yet is
	// absent from the guard's catalog view (the resolved-set / view divergence
	// that ErrGuardEntityViewInconsistency reports). Default false: the
	// divergence halts loudly — the build's safety net. A resilient-inspection
	// caller working against a lazily-populated view (where a cache slot may be
	// skipped as drifted, leaving an in-closure type momentarily absent) sets
	// it true: the divergence is dropped from the halt set so the IR still
	// builds with the unavailable entity skipped, and the condition surfaces as
	// a diagnostic rather than failing the inspection. Opt-out, not opt-in, so a
	// caller that forgets the flag halts (safe) rather than silently skips.
	TolerateCatalogViewInconsistency bool

	// PluginPathLookup resolves a pipeline step's plugin URI to its verified
	// .wasm bytes. When nil, RunPipeline binds the shared plugin-resolution
	// cascade (embed → sidecar → local → cache → registry) configured from the
	// build context — the lockfile digest pins, the workspace root + cache dir,
	// and the configured sidecar directory, with a nil puller (the build is
	// hermetic). A caller may override it to point a fixture's plugin URI at a
	// built artifact. The renderer's per-node pipeline executor uses it to load
	// each step's guest; a nil lookup reaching the renderer leaves pipelines
	// validated-but-not-executed.
	PluginPathLookup pluginpath.Lookup
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

// stepsOneToFourOutput bundles the outputs runStepsOneToFour produces inside
// the resolve scope. The parameter-map purge is owned by resolver.ResolveAndUse
// (which runStepsOneToFour routes through) and fires after the consumer's
// continuation — RunPipeline's render or RunTier4Pipeline's extraction —
// returns, so the outputs are valid for the whole continuation and no caller
// holds a purge defer.
type stepsOneToFourOutput struct {
	ResolveResult *resolver.ResolveResult
	Topology      *topology.TopologyResult
	IRGraph       *types.IRGraph
	FeatureOrder  ir.FeatureOrder
	Analysis      *analyzer.AnalysisResult
}

// runStepsOneToFour executes Steps 1-4 of the compiler pipeline (resolve,
// topology, IR finalisation, analyse) and invokes use with the phase outputs
// while still inside the resolve scope. It routes through ResolveAndFlatten →
// resolver.ResolveAndUse, so the parameter purge is guaranteed by construction
// and use runs while the parameter map is live. Used by both RunTier4Pipeline
// (which discards Step 5) and RunPipeline (which renders from the same outputs).
//
// Behavior on phase failure: a Step 1-3 failure returns the error without
// invoking use. A Step 4 (analyzer) error is non-fatal — the structured
// AnalysisResult.Errors slice rides on out.Analysis and use IS invoked, so
// callers decide whether to treat analyzer errors as fatal (RunPipeline joins
// them) or as diagnostics (RunTier4Pipeline). This preserves the daemon's
// Tier-4 contract.
func runStepsOneToFour(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	subAggregateLoader topology.SubAggregateLoader,
	use func(*stepsOneToFourOutput) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Steps 1-2: resolve + topology flatten. Shared with `aggrega pull`'s
	// per-component source resolution via ResolveAndFlatten, so lock-time
	// source-descent and build-time rendering run one resolve + topology +
	// recipe-selection path (no drift).
	return ResolveAndFlatten(ctx, inputs, catalogLookup, subAggregateLoader, func(resolveResult *resolver.ResolveResult, topoResult *topology.TopologyResult) error {
		out := &stepsOneToFourOutput{
			ResolveResult: resolveResult,
			Topology:      topoResult,
		}

		// Step 3: IR.
		graph, featureOrder, err := ir.BuildIRContext(ctx, ir.BuildIRConfig{
			Topology:        topoResult,
			ResolveResult:   resolveResult,
			CatalogLookup:   catalogLookup,
			CompilerVersion: inputs.CompilerVersion,
		})
		if err != nil {
			return fmt.Errorf("Step 3 (IR): %w", err)
		}
		out.IRGraph = graph
		out.FeatureOrder = featureOrder

		if err := ctx.Err(); err != nil {
			return err
		}

		// Step 4: Analyze. Analyser errors surface as structured
		// AnalysisResult.Errors; the function-level error stays nil for
		// analyzer-only failures so Tier-4 callers can consume them as
		// diagnostics. Build / irgraph callers convert AnalysisResult.Errors
		// to the joined-error shape via RunPipeline's wrapper logic.
		analysis, err := analyzer.AnalyzeContext(ctx, graph, catalogLookup)
		if err != nil {
			return err
		}
		out.Analysis = analysis
		for _, w := range analysis.Warnings {
			fmt.Fprintf(os.Stderr, "  %s\n", w)
		}
		return use(out)
	})
}

// dropCatalogViewInconsistencies returns the guard-violation slice with every
// ErrGuardEntityViewInconsistency finding removed, preserving order. It backs
// Inputs.TolerateCatalogViewInconsistency: a resilient-inspection caller drops
// the resolved-set / view divergence from the halt set while every other guard
// violation still halts.
func dropCatalogViewInconsistencies(errs []*resolver.ResolverError) []*resolver.ResolverError {
	kept := errs[:0:0]
	for _, e := range errs {
		if stderrors.Is(e, resolver.ErrGuardEntityViewInconsistency) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// ResolveAndValidate runs Step 1 of the compiler pipeline — resolve (parameter
// interpolation) + annotation binding validation + expansion + the full guard
// suite — against caller-supplied inputs, then invokes use with the resolved
// per-component view (ResolveResult carrying per-component BoundaryInfra)
// WITHOUT flattening topology. It is the single source of the build's
// pre-topology validation: ResolveAndFlatten calls it then flattens, and a
// validate-only caller (`aggrega lint --release`) calls it and stops, so the two
// cannot diverge on what a Release's resolved values are validated against. The
// guard suite operates on the resolved (post-parameter-interpolation) aggregate,
// so templated `{{ .parameters.* }}` values are validated as resolved.
//
// The continuation runs inside resolver.ResolveAndUse's scope, so the parameter
// purge is guaranteed by construction. A resolve, annotation, or guard failure
// returns the error without invoking use; the purge still fires.
func ResolveAndValidate(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	use func(*resolver.ResolveResult) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return resolver.ResolveAndUse(ctx, resolver.ResolveInputs{
		Release:           inputs.Release,
		Manifest:          inputs.Manifest,
		AggregateRawBytes: inputs.AggregateRawBytes,
		AggregatePath:     inputs.AggregatePath,
		ReleasePath:       inputs.ReleasePath,
	}, func(resolveResult *resolver.ResolveResult) error {
		if err := resolver.ValidateAnnotationBindingTargets(resolveResult.Manifest, catalogLookup); err != nil {
			return fmt.Errorf("Step 1 (annotation binding validation): %w", err)
		}
		for _, w := range resolver.LintAnnotationBindingMaps(resolveResult.Manifest) {
			fmt.Fprintf(os.Stderr, "  %s\n", w)
		}
		if err := resolver.ExpandAnnotations(resolveResult, catalogLookup); err != nil {
			return fmt.Errorf("Step 1 (annotation expansion): %w", err)
		}

		guardErrs := resolver.RunGuards(types.Normalize(resolveResult.Aggregate), resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra, resolveResult.Parameters)
		if inputs.TolerateCatalogViewInconsistency {
			// Resilient inspection against a lazily-populated view: a resolved
			// catalog reference momentarily absent from the view (a drifted cache
			// slot skipped on read) is a drift signal, not an authoring fault.
			// Drop it from the halt set so the IR still builds with the entity
			// skipped; the condition surfaces as a diagnostic. Every other guard
			// violation still halts.
			guardErrs = dropCatalogViewInconsistencies(guardErrs)
		}
		if len(guardErrs) > 0 {
			for _, e := range guardErrs {
				fmt.Fprintf(os.Stderr, "  guard: %s\n", e)
			}
			// Carry the per-violation slice on the returned error so a caller can
			// errors.Is the specific guard sentinel at the pipeline boundary
			// (parity with the analyzer path, which joins). The summary text is
			// unchanged: MultiResolverError.Error() renders "Step 1: N guard
			// violation(s)".
			return &resolver.MultiResolverError{
				Entity:     "Step 1",
				Summary:    fmt.Sprintf("%d guard violation(s)", len(guardErrs)),
				Violations: guardErrs,
			}
		}
		return use(resolveResult)
	})
}

// ResolveAndFlatten runs Steps 1-2 of the compiler pipeline — resolve
// (parameter interpolation, annotation binding validation + expansion, guards)
// then topology flatten — and invokes use with the resolved per-component view
// (ResolveResult carrying per-component BoundaryInfra) and the flattened
// TopologyResult (nodes with InstanceID / ComponentType / Values).
//
// It is the single source of the resolve+flatten wiring. The build pipeline
// (runStepsOneToFour) and `aggrega pull`'s per-component source resolution both
// call it, so lock-time source-descent cannot drift from build-time component /
// recipe / infra resolution — recipe selection then runs through the same
// catalog RecipeLookup at both sites.
//
// The continuation runs inside resolver.ResolveAndUse's scope (threaded through
// ResolveAndValidate), so the parameter purge is guaranteed by construction; a
// resolve, guard, or topology failure returns the error without invoking use.
func ResolveAndFlatten(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	subAggregateLoader topology.SubAggregateLoader,
	use func(*resolver.ResolveResult, *topology.TopologyResult) error,
) error {
	return ResolveAndValidate(ctx, inputs, catalogLookup, func(resolveResult *resolver.ResolveResult) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		// The root-tier node source stamp defaults to AggregatePath but may be
		// supplied separately (RootAggregateFile) when a caller's annotation-source
		// label and node-source label need different path representations.
		rootAggregateFile := inputs.RootAggregateFile
		if rootAggregateFile == "" {
			rootAggregateFile = inputs.AggregatePath
		}
		topoResult, err := topology.BuildContext(ctx, topology.BuildConfig{
			ResolveResult:     resolveResult,
			CatalogLookup:     catalogLookup,
			LoadSubAggregate:  subAggregateLoader,
			RootAggregateFile: rootAggregateFile,
		})
		if err != nil {
			if inputs.TolerateCatalogViewInconsistency {
				// The sub-aggregate guard reaches the same shared uses-socket
				// validator as Step 1, so the view-inconsistency class can surface
				// here too. Apply the same tolerance: if every Step-2 violation is
				// a catalog-view inconsistency, suppress the halt and proceed with
				// the graph BuildContext built up to the guard (uses-conformance for
				// the unavailable node skipped). A remaining real violation halts.
				var multi *resolver.MultiResolverError
				if stderrors.As(err, &multi) && len(dropCatalogViewInconsistencies(multi.Violations)) == 0 {
					return use(resolveResult, topoResult)
				}
			}
			return fmt.Errorf("Step 2 (topology): %w", err)
		}

		return use(resolveResult, topoResult)
	})
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
	out := &Tier4Result{}
	err := runStepsOneToFour(ctx, inputs, catalogLookup, subAggregateLoader, func(stepsOut *stepsOneToFourOutput) error {
		// Capture the phase outputs inside the resolve scope; the parameter
		// purge is owned by resolver.ResolveAndUse and fires after this returns.
		// Tier-4 runs no further work in the scope — analyzer errors ride on
		// Analysis.Errors, consumed below.
		out.IRGraph = stepsOut.IRGraph
		out.Analysis = stepsOut.Analysis
		return nil
	})

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
// the renderer (for orchestrator-time path-template evaluation). RunPipeline
// runs inside resolver.ResolveAndUse's scope (via runStepsOneToFour →
// ResolveAndFlatten), which owns the active-purge: the render completes inside
// the callback and the deferred PurgeParameters fires after it returns, so
// RunPipeline holds no purge defer of its own.
//
// buildPluginResolver constructs the hermetic build-time plugin-resolution
// cascade from the pipeline inputs: the configured sidecar directory, the
// workspace root + resolved cache directory, and the lockfile digest pins. The
// puller is left nil — the build never fetches from a registry (AOT purity), so
// a cache miss is a loud halt rather than a network round-trip. Pins make every
// tier's resolved bytes verify against the lockfile digest, so a drifted or
// substituted artifact halts on the pin regardless of which tier produced it.
// The sidecar-overrides-embed notice is surfaced to stderr alongside the
// pipeline's other mid-build diagnostics.
func buildPluginResolver(inputs Inputs) pluginpath.Resolver {
	r := pluginpath.Resolver{
		SidecarDir:    inputs.WorkspaceConfig.PluginSidecarDir,
		WorkspaceRoot: inputs.WorkspaceRoot,
		CacheDir:      inputs.WorkspaceConfig.ResolvedArtifactCacheDir(),
		OverrideLog:   func(msg string) { fmt.Fprintf(os.Stderr, "  plugin: %s\n", msg) },
	}
	if inputs.Lockfile != nil && len(inputs.Lockfile.WasmPlugins) > 0 {
		pins := make(map[string]string, len(inputs.Lockfile.WasmPlugins))
		versions := make(map[string]string, len(inputs.Lockfile.WasmPlugins))
		for _, wp := range inputs.Lockfile.WasmPlugins {
			pins[wp.URI] = wp.Digest
			versions[wp.URI] = wp.ResolvedVersion
		}
		r.Pins = pins
		r.PinVersions = versions
	}
	return r
}

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
	var result *Result
	err := runStepsOneToFour(ctx, inputs, catalogLookup, subAggregateLoader, func(stepsOut *stepsOneToFourOutput) error {
		// Step 4: convert analyzer errors to the joined-error shape that
		// pre-extraction RunPipeline returned. Preserves the first-error-fatal
		// semantic existing callers (irgraph, CLI build) consume.
		if len(stepsOut.Analysis.Errors) > 0 {
			errs := make([]error, len(stepsOut.Analysis.Errors))
			for i := range stepsOut.Analysis.Errors {
				errs[i] = stepsOut.Analysis.Errors[i].ToCompilerError()
			}
			return stderrors.Join(errs...)
		}

		resolveResult := stepsOut.ResolveResult
		graph := stepsOut.IRGraph
		featureOrder := stepsOut.FeatureOrder

		if err := ctx.Err(); err != nil {
			return err
		}

		// Step 4b: Component / Feature assertions.
		if err := renderer.EvaluateComponentAndFeatureAssertions(graph, catalogLookup); err != nil {
			return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, "Step 4b (Component/Feature assertions)")
		}

		// Step 4c: resolution-completeness — every component/feature instance must
		// resolve to exactly one recipe before render. Skipped when the caller does
		// not supply a verifier (IR-inspection paths that do not render).
		if opts.RecipeResolutionVerifier != nil {
			if err := opts.RecipeResolutionVerifier(inputs.Manifest, graph); err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "Step 4c (recipe resolution)")
			}
		}

		// Step 5: Render. Trace writer selection: in-memory collection when
		// IncludeTrace is true (caller surfaces or writes the map); no-op
		// otherwise. The trace close and the resolver's parameter purge both
		// run as this continuation unwinds — trace first (deferred here), purge
		// after (owned by resolver.ResolveAndUse), so render reads live
		// parameters exactly as before.
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
			// Bind the shared plugin-resolution cascade so a Recipe /
			// FeatureRecipe pipeline step's plugin URI resolves to its verified
			// .wasm bytes (embed → sidecar → local → cache → registry). The
			// build configures it hermetic (nil puller) with the lockfile digest
			// pins, so a cache miss is a loud halt and a drifted artifact halts
			// on the pin. The per-step plugin loads through the describe() gate
			// at execution. A caller-supplied override (e.g. a fixture pointing a
			// plugin URI at a built artifact) wins when present.
			PluginPathLookup: inputs.PluginPathLookup,
		}
		if rendererInput.PluginPathLookup == nil {
			rendererInput.PluginPathLookup = buildPluginResolver(inputs).Resolve
		}
		// Build-time source-artifact resolution: when the caller supplies a
		// lockfile and workspace root, the orchestrator resolves each remote
		// source to its pinned cache entry. Left unset, the renderer forwards
		// sources verbatim (sourceless / local-path paths).
		if inputs.Lockfile != nil && inputs.WorkspaceRoot != "" {
			rendererInput.Lockfile = inputs.Lockfile
			rendererInput.WorkspaceRoot = inputs.WorkspaceRoot
			rendererInput.CacheDir = inputs.WorkspaceConfig.ResolvedArtifactCacheDir()
		}

		renderResult, rerr := renderer.RenderAll(ctx, rendererInput, recipeLookup, engineFactory)
		if rerr != nil {
			return fmt.Errorf("Step 5 (render): %w", rerr)
		}

		for _, w := range renderResult.Warnings {
			fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
		}

		result = &Result{
			Artifacts: renderResult.Files,
			IRGraph:   graph,
		}
		if memWriter != nil {
			result.Trace = memWriter.Entries()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
