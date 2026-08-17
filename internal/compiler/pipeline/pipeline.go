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
	"io"
	"os"
	"sort"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
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

	// WorkloadLibraries is the resolved WorkloadLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It
	// flows to the renderer so a Recipe workload's bare-name
	// `$import: <Library>#/workloads/<entry>` resolves at render. nil when no
	// WorkloadLibraries are present.
	WorkloadLibraries map[string]types.WorkloadLibrary

	// ArtifactsLibraries is the resolved ArtifactsLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It flows
	// to the renderer so a Recipe / FeatureRecipe artifacts entry's bare-name
	// `artifacts.<name>: { $import: <Library>#/artifacts/<entry> }` resolves at
	// render. nil when no ArtifactsLibraries are present.
	ArtifactsLibraries map[string]types.ArtifactsLibrary

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
	// cascade (embed → colocated → local → cache → registry) configured from the
	// build context — the lockfile digest pins, the workspace root + cache dir,
	// and the catalog's co-located artifact directories, with a nil puller (the
	// build is hermetic). A caller may override it to point a fixture's plugin URI at a
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

	// TraceUnredacted turns off the per-pass capture's withholding of
	// declared-sensitive data, and stamps every captured file with a header
	// announcing that. It is meaningful only alongside IncludeTrace: with the
	// capture off there is nothing to withhold from. Left false — the default —
	// declared-sensitive data is withheld.
	//
	// It reaches ONLY the capture surface. The key set it empties is consumed by
	// the per-pass projection and by nothing else, so the `inspect` layouts and
	// the diagnostic bodies — which reach the replacement text on their own,
	// without a key set — are unaffected in either state.
	TraceUnredacted bool

	// SkipRender, when true, stops the pipeline after Step 4b (once the IR
	// graph is finalised and Component/Feature assertions have run) and
	// returns the graph with no Artifacts. Step 5 render — the sole path that
	// loads rendering plugins — never runs, so an IR-inspection caller that
	// needs only the graph avoids the render cost entirely. The returned graph
	// is byte-identical to a full-render run's graph, because render never
	// mutates it. Left false — the common case — the pipeline renders as usual.
	SkipRender bool
	// RecipeResolutionVerifier, when set, runs after IR-build and before render
	// to assert every component/feature instance resolves to exactly one recipe
	// (halting loudly on a no-recipe instance or an unmapped ambiguously-
	// implemented type). Left nil — e.g. by callers that build the IR for
	// inspection rather than render — the check is skipped. Catalog supplies
	// VerifyRecipeResolution, whose signature this matches.
	RecipeResolutionVerifier func(*types.Manifest, *types.IRGraph) error

	// NodeProgress, when set, is invoked once per node during Step 5 render as
	// each node completes: done is the 1-based count of nodes rendered so far,
	// total is the node count of the graph, and nodeID names the node just
	// finished. It is a diagnostic hook for a progress surface; the callback must
	// write only to a diagnostic sink (never stdout) so rendered output stays
	// byte-deterministic. Left nil — the common case, and every non-CLI caller —
	// the render loop skips the call. Mirrors IncludeTrace: an opt-in per-node
	// observation point that is a no-op when unset.
	NodeProgress func(done, total int, nodeID string)

	// PluginCompileBegin, when set, turns on the eager provider-guest compilation
	// pass: before the render loop, RunPipeline force-compiles the distinct
	// provider guests the render needs, so a cold guest compilation is a discrete,
	// attributed phase rather than a freeze inside the first node's render. Begin
	// is invoked once with the number of guests that will be compiled, before the
	// first is compiled. Left nil — the common case, and every non-CLI caller —
	// the eager pass is skipped and guests compile lazily on first per-node load,
	// as before. It is a diagnostic hook for a progress surface; the callback must
	// write only to a diagnostic sink (never stdout) so rendered output stays
	// byte-deterministic, and it carries no authority over the compile.
	PluginCompileBegin func(guests int)

	// PluginCompileGuestDone, when set, is invoked after each provider guest
	// finishes compiling in the eager pass, with the running count of guests
	// compiled so far. Paired with PluginCompileBegin; nil when the eager pass is
	// off. Like PluginCompileBegin it is a diagnostic hook that must write only to
	// a diagnostic sink and cannot alter output.
	PluginCompileGuestDone func(done int)

	// PluginCompileRunning, when set, is invoked the first time a provider guest
	// in the eager pass is observed to be compiling rather than served from the
	// cache — a warm still running after a short cache-hit window. It lets a
	// progress surface reveal the compilation step only for a build that truly
	// recompiles a guest, including a build whose cache directory already existed
	// but lacked the exact guest this build needs. Paired with PluginCompileBegin;
	// nil leaves the step hidden. Like the other compile hooks it must write only
	// to a diagnostic sink and carries no authority over the compile. It may fire
	// from a timer goroutine, so the callback must be safe to call concurrently
	// with the pass.
	PluginCompileRunning func()

	// SuppressUnsupportedFieldWarnings, when true, silences the unsupported-field
	// warning class on the render-warning surface — the per-authored-field notices
	// a lower-fidelity target emits for fields it cannot honor. It matches on that
	// class's own code, so other render warnings and the other coded diagnostic
	// classes still surface. It is a pure display filter over structured
	// diagnostics carried out-of-band; it never re-renders and never changes the
	// emitted bytes. Left false — the default — the class surfaces.
	SuppressUnsupportedFieldWarnings bool
}

// emitRenderWarnings writes the render step's advisory warnings to w: first the
// free-form warning stream, then the structured coded diagnostics, each entry
// attributed to its node and provider from its location. Suppression is keyed on
// the diagnostic's own code and drops the unsupported-field class alone — the
// free-form warnings and every other coded class still surface, which is what
// keeps a target-fidelity preference from silencing an unrelated advisory. This
// is a pure display pass over out-of-band diagnostics and never touches the
// rendered bytes.
func emitRenderWarnings(w io.Writer, result *renderer.RenderResult, suppressUnsupportedFields bool) {
	for _, warning := range result.Warnings {
		fmt.Fprintf(w, "  warning: %s\n", warning)
	}
	for _, d := range result.Diagnostics {
		if suppressUnsupportedFields && d.Code == renderer.UnsupportedFieldCode {
			continue
		}
		fmt.Fprintf(w, "  warning: %s\n", renderer.RenderDiagnosticText(d))
	}
}

// guestWarmer is the compile-only capability the eager plugin-compilation pass
// drives on a provider engine that renders through a Wasm guest: it compiles the
// guest into the shared compilation cache without rendering a node, so the render
// loop's per-node loads are cache hits. A provider that renders through the
// in-tree Go engine does not satisfy it and is skipped by the pass.
type guestWarmer interface {
	Warm(ctx context.Context) error
}

// Result carries the pipeline's outputs. Artifacts is the rendered file
// map the CLI wrapper writes to dist/. IRGraph is the step-3 output the
// daemon's irGraph operation returns directly. Trace is the per-pass
// intermediate state, empty unless Options.IncludeTrace is true.
type Result struct {
	Artifacts map[string][]byte
	IRGraph   *types.IRGraph
	Trace     map[string][]byte
	// RenderDiagnostics carries the render's structured, coded advisories so a
	// caller that does not read the terminal can surface them itself — the daemon
	// turns them into editor problems. The terminal display already happened by
	// the time this returns; carrying them here is what stops that display from
	// being the only place they exist. Empty when the render was skipped, and
	// out-of-band in every case: no entry affects Artifacts.
	RenderDiagnostics []renderer.Diagnostic
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
//
// LintAdvisories carries the exit-code-neutral advisory findings the resolve
// scope produced — presently the attachment-slot near-miss lint. They are
// structurally identical to ResolveDiagnostics (entity, field locator, message)
// so a front end reuses one position-recovery path for both, but they never
// contribute to a verdict: a caller renders them as warnings and proceeds.
type Tier4Result struct {
	IRGraph            *types.IRGraph
	Analysis           *analyzer.AnalysisResult
	ResolveDiagnostics []*resolver.ResolverError
	LintAdvisories     []*resolver.ResolverError
}

// stepsOneToFourOutput bundles the outputs runStepsOneToFour produces inside
// the resolve scope. The scope is owned by resolver.ResolveAndUse (which
// runStepsOneToFour routes through) and closes only after the consumer's
// continuation — RunPipeline's render or RunTier4Pipeline's extraction —
// returns, so the outputs, the merged parameter map among them, are valid for
// the whole continuation.
type stepsOneToFourOutput struct {
	ResolveResult *resolver.ResolveResult
	Topology      *topology.TopologyResult
	IRGraph       *types.IRGraph
	FeatureOrder  ir.FeatureOrder
	Analysis      *analyzer.AnalysisResult

	// AttachmentSites names, per attachment leaf, where the contribution that
	// won that leaf was written. It is carried alongside the graph because the
	// graph cannot answer the question: the cascade folds every scope's
	// contribution into one entry and keeps the winning value, not the contest
	// that produced it. A rule invoked over such a leaf needs the site to point
	// its finding at an edit somebody can make.
	AttachmentSites ir.AttachmentSites

	// GraphView presents the finalised graph to a check as the named facts
	// complete once the whole attachment cascade and the annotation-driven
	// expansion have run, with every fact complete earlier answered by the
	// presentation the flattening produced.
	//
	// It is built here because this is the one place holding both products at
	// once: a presentation of a stage embeds the presentation of the stage below
	// it rather than re-reading that stage's own inputs, so it can only be built
	// where both are in hand. A rule placed at this stage reads it; a rule placed
	// later reads a presentation that embeds it in turn.
	//
	// Nil when either product is absent.
	GraphView constraint.GraphView

	// AssertionErr carries the first failing Component or Feature assertion, or
	// nil when every assertion passed. It is produced inside runStepsOneToFour
	// rather than by each consumer, because the rule's inputs — the finalised IR
	// graph and the catalog — are complete once the graph is built, so every
	// caller of Steps 1-4 is entitled to the verdict. What differs per caller is
	// only how the failure is surfaced: the render path wraps it as a fatal halt,
	// the validation path returns it as the run error alongside the graph it
	// already captured.
	AssertionErr error
}

// runStepsOneToFour executes the pre-render phases of the compiler pipeline
// (resolve, topology, IR finalisation, analyse, and Component / Feature
// assertion evaluation) and invokes use with the phase outputs while still
// inside the resolve scope. It routes through ResolveAndFlatten →
// resolver.ResolveAndUse, so use runs while the parameter map is live and the
// resolved view cannot outlive the scope. Used by both RunTier4Pipeline
// (which renders nothing) and RunPipeline (which renders from the same outputs).
//
// Every check whose inputs are complete before render lives here rather than on
// one consumer's path. That is what makes the two entry points agree: a rule
// placed on the render path alone would let a validation surface report a clean
// verdict on a document a build rejects.
//
// Behavior on phase failure: a resolve, topology or IR failure returns the error
// without invoking use. An analyzer error is non-fatal — the structured
// AnalysisResult.Errors slice rides on out.Analysis and use IS invoked, so
// callers decide whether to treat analyzer errors as fatal (RunPipeline joins
// them) or as diagnostics (RunTier4Pipeline). This preserves the daemon's
// Tier-4 contract. An assertion failure likewise rides on out.AssertionErr
// rather than aborting the callback, so a caller still receives the graph.
func runStepsOneToFour(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
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
	return ResolveAndFlatten(ctx, inputs, catalogLookup, recipeFor, subAggregateLoader, func(resolveResult *resolver.ResolveResult, topoResult *topology.TopologyResult) error {
		out := &stepsOneToFourOutput{
			ResolveResult: resolveResult,
			Topology:      topoResult,
		}

		// Step 2b: label-transform governance role. Config-driven off the
		// label-transform attachments in WorkspaceConfig.Plugins.Governance — a no-op
		// when unconfigured, so an unattached build is byte-identical to a build
		// without the role. When configured, each plugin auto-tags node labels via
		// JSON Patch (the narrow mutation exception); the tags flow into the IR
		// because Step 3 reads its labels from this same topoResult.
		if err := runMutatorSocket(ctx, inputs, catalogLookup, topoResult); err != nil {
			return err
		}

		// Step 3: IR.
		graph, featureOrder, attachmentSites, err := ir.BuildIRContext(ctx, ir.BuildIRConfig{
			Topology:        topoResult,
			ResolveResult:   resolveResult,
			CatalogLookup:   catalogLookup,
			CompilerVersion: inputs.CompilerVersion,
		})
		if err != nil {
			return fmt.Errorf("IR graph construction: %w", err)
		}
		out.IRGraph = graph
		out.FeatureOrder = featureOrder
		out.AttachmentSites = attachmentSites

		// The presentation of the graph just finalised, extending the one the
		// flattening produced. Built here because this is where both products are
		// in hand: a stage's presentation embeds the stage below it rather than
		// re-reading that stage's inputs, so building it anywhere else would mean
		// producing a second answer to facts this one already answers.
		out.GraphView = ir.NewGraphView(graph, topoResult.StageView)

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

		// The rules declared over the finalised graph are invoked here, against
		// the presentation built above and the coordinates of the same graph.
		// This is the site that holds both, which is why the invocation sits
		// beside the analysis rather than inside it: the analysis is handed the
		// graph and the catalog and would have to build a second presentation of
		// its own to ask a rule anything, and one fact with two producers is two
		// answers free to disagree.
		if err := runGraphStageRules(ctx, out, catalogLookup); err != nil {
			return err
		}

		// Step 4b: ir-verdict governance role. Config-driven off the ir-verdict
		// attachments in WorkspaceConfig.Plugins.Governance — a no-op when
		// unconfigured. When configured, each plugin evaluates the full IR graph and
		// its failed verdicts append to analysis.Errors, so a verdict rejection halts
		// the build through the same gate selector/appliesTo violations use
		// (RunPipeline joins Errors; the Tier-4 path surfaces them as diagnostics). A
		// verdict role never mutates the graph.
		if err := runPolicySocket(ctx, inputs, catalogLookup, analysis); err != nil {
			return err
		}

		// Component / Feature assertions. The rule reads the finalised IR graph and
		// the catalog and nothing else, so it belongs here — shared by every caller
		// of Steps 1-4 — rather than on one consumer's path, where a validation
		// surface would report a clean verdict on a document the render path
		// rejects. Skipped when an earlier violation already exists, so the
		// reported set is the earliest failing class rather than a mixture.
		if len(analysis.Errors) == 0 {
			out.AssertionErr = renderer.EvaluateComponentAndFeatureAssertions(graph, catalogLookup)
		}

		for _, w := range analysis.Warnings {
			fmt.Fprintf(os.Stderr, "  %s\n", w)
		}
		return use(out)
	})
}

// wrapAssertionErr gives a Component / Feature assertion failure its user-facing
// shape. Single-sourced so the render path and the validation path present one
// failure identically — a diagnostic that reads differently depending on which
// command surfaced it would suggest two different rules.
func wrapAssertionErr(err error) error {
	if err == nil {
		return nil
	}
	return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, "Component/Feature assertions")
}

// dropCatalogViewInconsistencies returns the guard-violation slice with every
// ErrGuardEntityViewInconsistency finding removed, preserving order. It backs
// Inputs.TolerateCatalogViewInconsistency: a resilient-inspection caller drops
// the resolved-set / view divergence from the halt set while every other guard
// violation still halts.
func dropCatalogViewInconsistencies(errs []*resolver.ResolverError) []*resolver.ResolverError {
	kept := errs[:0:0]
	for _, e := range errs {
		if stderrors.Is(e, resolver.ErrGuardEntityViewInconsistency()) {
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
// The continuation runs inside resolver.ResolveAndUse's scope, so the resolved
// view is live throughout it and reachable nowhere else. A resolve, annotation,
// or guard failure returns the error without invoking use.
//
// recipeFor is the component-instance-to-Recipe binding the caller will render
// with. It is read by the advisory drain below and by nothing else here, so a
// caller with no binding to offer passes nil and that one advisory is skipped
// while every other check is unaffected.
func ResolveAndValidate(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
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
		// The annotation-transform governance role runs first in the resolve
		// continuation: it injects annotations onto resolved components before
		// binding validation and expansion, so a bound injection is validated and
		// expanded into a Feature attachment with no re-run. Unconfigured, it is a
		// byte-identical no-op.
		if err := runAnnotationSocket(ctx, inputs, catalogLookup, resolveResult); err != nil {
			return err
		}
		if err := resolver.ValidateAnnotationBindingTargets(resolveResult.Manifest, catalogLookup); err != nil {
			return fmt.Errorf("annotation binding validation: %w", err)
		}
		for _, w := range resolver.LintAnnotationBindingMaps(resolveResult.Manifest) {
			fmt.Fprintf(os.Stderr, "  %s\n", w)
		}
		if err := resolver.ExpandAnnotations(resolveResult, catalogLookup); err != nil {
			return fmt.Errorf("annotation expansion: %w", err)
		}

		normalized := types.Normalize(resolveResult.Aggregate)

		// The catalog-aware advisory checks run in the same scope as the halting
		// guards and read the same normalized aggregate and catalog view. This is
		// the single DRAIN both front ends consume: the build renders every
		// advisory to stderr below, the daemon maps them to editor warnings off
		// Tier4Result. They are computed before the guard verdict so an advisory is
		// not lost when an unrelated guard halts.
		//
		// APPEND, never assign: the resolve arrives already carrying the advisories
		// from the checks that needed only the Aggregate, and assigning here would
		// silently drop them.
		resolveResult.LintAdvisories = append(resolveResult.LintAdvisories,
			resolver.LintAttachmentSlotNearMiss(normalized, catalogLookup)...)
		resolveResult.LintAdvisories = append(resolveResult.LintAdvisories,
			resolver.LintUsesSocketNearMiss(normalized, catalogLookup)...)
		resolveResult.LintAdvisories = append(resolveResult.LintAdvisories,
			resolver.LintProvidesNotDeclared(normalized, catalogLookup, recipeFor)...)
		for _, a := range resolveResult.LintAdvisories {
			fmt.Fprintf(os.Stderr, "  lint: %s\n", a)
		}

		// ctx.Err is the cancellation signal the rule walks read once per
		// component and once per declared scope: a caller that has given up on
		// this build stops paying for the rest of it, and a walk that stops hands
		// back nothing rather than a partial set that would read as a document
		// satisfying rules nobody asked.
		guardErrs, err := resolver.RunGuards(ctx.Err, normalized, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra, resolveResult.Parameters)
		if err != nil {
			return err
		}
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
			// unchanged: the rendered summary reads "resolution: N guard
			// violation(s)".
			return &resolver.MultiResolverError{
				Entity:     "resolution",
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
// ResolveAndValidate), so the resolved view is live throughout it; a resolve,
// guard, or topology failure returns the error without invoking use.
func ResolveAndFlatten(
	ctx context.Context,
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeFor resolver.RecipeBinding,
	subAggregateLoader topology.SubAggregateLoader,
	use func(*resolver.ResolveResult, *topology.TopologyResult) error,
) error {
	return ResolveAndValidate(ctx, inputs, catalogLookup, recipeFor, func(resolveResult *resolver.ResolveResult) error {
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
			return fmt.Errorf("topology construction: %w", err)
		}

		return use(resolveResult, topoResult)
	})
}

// RunTier4Pipeline executes the pre-render phases of the compiler pipeline
// (resolve, topology, IR finalisation, analyse, Component / Feature assertions)
// against caller-supplied inputs. The daemon's runTier4Validation consumes this
// to surface structured per-violation diagnostics through aggrega/validate
// without rendering, which is expensive and not needed for semantic validation.
//
// It runs every pre-render check the render path runs, because both entry points
// share one implementation of those phases. A rule reachable from one and not the
// other would make "this document validates" mean something different from "this
// document builds".
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
	recipeFor resolver.RecipeBinding,
	subAggregateLoader topology.SubAggregateLoader,
) (*Tier4Result, error) {
	out := &Tier4Result{}
	var assertionErr error
	err := runStepsOneToFour(ctx, inputs, catalogLookup, recipeFor, subAggregateLoader, func(stepsOut *stepsOneToFourOutput) error {
		// Capture the phase outputs inside the resolve scope, which
		// resolver.ResolveAndUse closes once this returns. Tier-4 runs no
		// further work in the scope — analyzer errors ride on
		// Analysis.Errors, consumed below.
		out.IRGraph = stepsOut.IRGraph
		out.Analysis = stepsOut.Analysis
		assertionErr = stepsOut.AssertionErr
		if stepsOut.ResolveResult != nil {
			out.LintAdvisories = stepsOut.ResolveResult.LintAdvisories
		}
		return nil
	})

	// A failing Component or Feature assertion is a fatal verdict on this
	// document, so it surfaces here as the run error — the same class the render
	// path halts on, reported by a surface that never renders. The captured
	// IRGraph and Analysis stay populated: this function's contract is that the
	// phases which completed are returned even when a later one failed, and the
	// graph is final before assertions evaluate.
	if err == nil {
		err = wrapAssertionErr(assertionErr)
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
// the renderer (for orchestrator-time path-template evaluation). RunPipeline
// runs inside resolver.ResolveAndUse's scope (via runStepsOneToFour →
// ResolveAndFlatten), which owns that scope: the render completes inside the
// callback, so the map is live for every render phase and reachable from no
// caller once RunPipeline returns.
//
// buildPluginResolver constructs the hermetic build-time plugin-resolution
// cascade from the pipeline inputs: the catalog's co-located artifact
// directories, the workspace root + resolved cache directory, and the lockfile
// digest pins. The
// puller is left nil — the build never fetches from a registry (AOT purity), so
// a cache miss is a loud halt rather than a network round-trip. Pins make every
// tier's resolved bytes verify against the lockfile digest, so a drifted or
// substituted artifact halts on the pin regardless of which tier produced it.
// For a plugin row that carries a verified signature fact, the recorded
// signature-bundle digest is threaded through too, so the resolver re-hashes the
// cached bundle blob against it before the guest loads — an offline integrity
// gate that re-runs no signature verification. colocatedDirs is the catalog's artifact-reference → catalog-
// directory map, feeding the co-located tier so a binary that embeds no core
// guests resolves them from beside their catalog entities; nil at a socket with
// no catalog access, which disables that tier.
func buildPluginResolver(inputs Inputs, colocatedDirs map[string]string) pluginpath.Resolver {
	var pins pluginpath.PinMaps
	if inputs.Lockfile != nil && len(inputs.Lockfile.Plugins) > 0 {
		pinDigests := make(map[string]string, len(inputs.Lockfile.Plugins))
		versions := make(map[string]string, len(inputs.Lockfile.Plugins))
		bundleDigests := make(map[string]string, len(inputs.Lockfile.Plugins))
		for _, wp := range inputs.Lockfile.Plugins {
			pinDigests[wp.URI] = wp.Digest
			versions[wp.URI] = wp.ResolvedVersion
			if wp.Signature != nil && wp.Signature.BundleDigest != "" {
				bundleDigests[wp.URI] = wp.Signature.BundleDigest
			}
		}
		pins.Pins = pinDigests
		pins.PinVersions = versions
		if len(bundleDigests) > 0 {
			pins.BundleDigests = bundleDigests
		}
	}
	return pluginpath.NewResolver(pluginpath.ResolutionContext{
		ColocatedDirs: colocatedDirs,
		WorkspaceRoot: inputs.WorkspaceRoot,
		CacheDir:      inputs.WorkspaceConfig.ResolvedArtifactCacheDir(),
	}, pluginpath.PinPostureLockfile, pins)
}

// buildEntityPluginLookup fronts the build-time artifact cascade with the
// Plugin entity translation: a pipeline step references a Plugin entity
// URI, and the front derives the entity's .wasm artifact reference (its bare
// identity, via resolver.EntityToArtifact) before the cascade resolves the
// bytes. The catalog's Plugin accessor supplies the entity; the cascade keys its
// pins by the artifact reference, so the front leaves the pin model unchanged.
// A reference that matches no Plugin entity halts with
// ErrPluginEntityNotFound. This is the same entity-front the linter's
// workspace-config path binds, so the entity → artifact linkage is single-sourced.
func buildEntityPluginLookup(inputs Inputs, catalogLookup resolver.CatalogLookup) pluginpath.Lookup {
	cascade := buildPluginResolver(inputs, catalogLookup.PluginColocatedDirs).Resolve
	return pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, *[]pluginpath.PluginCommandMirror, bool) {
		if catalogLookup.Plugin == nil {
			return "", nil, nil, nil, false
		}
		entity, ok := catalogLookup.Plugin(uri)
		if !ok {
			return "", nil, nil, nil, false
		}
		return entity.ArtifactReference(), resolver.PluginContractClaim(entity), resolver.PluginRuntimeLimits(entity), resolver.PluginCommandsMirror(entity), true
	}, cascade, pluginpath.SurfaceEntityOnly)
}

// pluginCompileRevealDelay is the wall-clock a provider-guest warm must exceed
// for the eager pass to treat it as a genuine compilation rather than a cache
// hit. A guest served from the populated compile cache loads in a few
// milliseconds; compiling one from source takes seconds, so a warm still running
// past this delay is compiling. The margin is wide enough that a cache hit never
// crosses it and a real compile always does, so the signal cleanly separates the
// two without inspecting the cache's internal keys.
const pluginCompileRevealDelay = 750 * time.Millisecond

// warmProviderGuest compiles one provider guest, reporting through onCompiling
// when the warm runs long enough to be a genuine compile rather than a cache
// hit. A warm still running after pluginCompileRevealDelay is compiling: the
// timer fires onCompiling from its own goroutine while the warm proceeds, so a
// progress surface can reveal the compilation step mid-compile. A warm that
// finishes within the delay (a cache hit) cancels the timer and reports nothing.
// A nil onCompiling skips the timing and warms directly.
func warmProviderGuest(ctx context.Context, w guestWarmer, onCompiling func()) error {
	if onCompiling == nil {
		return w.Warm(ctx)
	}
	timer := time.AfterFunc(pluginCompileRevealDelay, onCompiling)
	defer timer.Stop()
	return w.Warm(ctx)
}

// collectProviderGuestWarmers resolves the distinct provider guests the render
// will load, in the same way the render loop does: it walks the graph's nodes,
// resolves each node's recipe to its provider reference, and routes that provider
// through the same hybrid engine factory the render uses. A provider that routes
// to a Wasm guest yields a guestWarmer; a provider that renders through the
// in-tree Go engine yields none. Guests are deduplicated by the engine identity
// the factory memoizes, so two bindings that resolve to one guest warm once.
// Node order is stable so the pass is deterministic. A factory error (a version
// pin no plugin satisfies) is returned — the same error the render loop would
// raise for that provider, surfaced before the render loop instead.
func collectProviderGuestWarmers(graph *types.IRGraph, recipeLookup renderer.RecipeLookup, engineFactory renderer.EngineFactory) ([]guestWarmer, error) {
	if recipeLookup.Recipe == nil {
		return nil, nil
	}
	seenRef := map[string]struct{}{}
	var refs []string
	for _, nodeID := range graph.Spec.Nodes.Keys() {
		node, ok := graph.Spec.Nodes.Get(nodeID)
		if !ok {
			continue
		}
		r, found := recipeLookup.Recipe(nodeID, node.Component)
		if !found || r == nil || r.Spec.Capability == "" {
			continue
		}
		if _, dup := seenRef[r.Spec.Capability]; dup {
			continue
		}
		seenRef[r.Spec.Capability] = struct{}{}
		refs = append(refs, r.Spec.Capability)
	}
	sort.Strings(refs)

	var warmers []guestWarmer
	seenGuest := map[guestWarmer]struct{}{}
	for _, ref := range refs {
		engine, err := engineFactory(ref)
		if err != nil {
			return nil, err
		}
		w, ok := engine.(guestWarmer)
		if !ok {
			continue
		}
		if _, dup := seenGuest[w]; dup {
			continue
		}
		seenGuest[w] = struct{}{}
		warmers = append(warmers, w)
	}
	return warmers, nil
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
	opts Options,
) (*Result, error) {
	var result *Result
	err := runStepsOneToFour(ctx, inputs, catalogLookup, recipeLookup.Recipe, subAggregateLoader, func(stepsOut *stepsOneToFourOutput) error {
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

		// Component / Feature assertions, evaluated in the shared Steps 1-4 core.
		if err := wrapAssertionErr(stepsOut.AssertionErr); err != nil {
			return err
		}

		// Resolution-completeness — every component/feature instance must resolve
		// to exactly one recipe before render. Skipped when the caller does not
		// supply a verifier (IR-inspection paths that do not render).
		if opts.RecipeResolutionVerifier != nil {
			if err := opts.RecipeResolutionVerifier(inputs.Manifest, graph); err != nil {
				return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "recipe resolution")
			}
		}

		// Render-skip cutoff: an IR-inspection caller that needs only the graph
		// stops here, before any render machinery (trace writer, plugin
		// compilation cache, provider engines) is constructed — so no rendering
		// plugin loads. The graph is already final after Step 4b; render never
		// mutates it, so this returns the same graph a full run would.
		if opts.SkipRender {
			result = &Result{IRGraph: graph}
			return nil
		}

		// Step 5: Render. Trace writer selection: in-memory collection when
		// IncludeTrace is true (caller surfaces or writes the map); no-op
		// otherwise. The trace close runs as this continuation unwinds
		// (deferred here) and the resolve scope closes after it, so render
		// reads live parameters throughout.
		// The capture mode is constructed once and handed to both the writer that
		// stamps the header and the renderer that empties the withheld-key set, so
		// the announcement and the withholding cannot disagree.
		traceMode := trace.Mode{Unredacted: opts.TraceUnredacted}
		var memWriter trace.MemoryWriter
		var traceWriter trace.Writer
		if opts.IncludeTrace {
			memWriter = trace.NewMemory(traceMode)
			traceWriter = memWriter
		} else {
			traceWriter = trace.New("", traceMode)
		}
		defer func() {
			if cerr := traceWriter.Close(); cerr != nil {
				fmt.Fprintf(os.Stderr, "  trace: close: %s\n", cerr)
			}
		}()

		rendererInput := renderer.RendererInput{
			Graph: graph,
			// The presentation of the finalised graph, forwarded so the render
			// can present what every plugin reports about itself as that
			// presentation plus its own facts. Forwarding it is what keeps one
			// fact to one producer: rebuilding it inside the render would give
			// every earlier fact a second answer, free to disagree with this one.
			GraphView:          stepsOut.GraphView,
			FeatureOrder:       featureOrder,
			ResolveResult:      resolveResult,
			CatalogLookup:      catalogLookup,
			MappingLibraries:   inputs.MappingLibraries,
			WorkloadLibraries:  inputs.WorkloadLibraries,
			ArtifactsLibraries: inputs.ArtifactsLibraries,
			Trace:              traceWriter,
			TraceMode:          traceMode,
			YAMLIndent:         inputs.WorkspaceConfig.Build.YAMLIndent,
			// Bind the entity-fronted plugin-resolution path so a Recipe /
			// FeatureRecipe pipeline step's plugin URI — a Plugin entity URI —
			// resolves to its verified .wasm bytes: the entity-front translates the
			// entity URI to its spec.plugin artifact reference, then the shared
			// cascade (embed → colocated → local → cache → registry) resolves the
			// artifact. The build configures the cascade hermetic (nil puller) with
			// the lockfile digest pins keyed by the artifact reference, so a cache
			// miss is a loud halt and a drifted artifact halts on the pin. The
			// per-step plugin loads through the describe() gate at execution. A
			// caller-supplied override (e.g. a fixture pointing a plugin URI at a
			// built artifact) wins when present.
			PluginPathLookup: inputs.PluginPathLookup,
			// Per-node progress hook (nil = no-op). Forwarded verbatim; the
			// renderer invokes it once per node as the loop completes. It writes
			// only to a diagnostic sink, so rendered output is unaffected.
			NodeProgress: opts.NodeProgress,
		}
		if rendererInput.PluginPathLookup == nil {
			rendererInput.PluginPathLookup = buildEntityPluginLookup(inputs, catalogLookup)
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

		// Route provider rendering through the hybrid dispatch: a provider whose
		// catalog Provider binds to a render_node-advertising Wasm plugin renders
		// through the wasmengine adapter; a provider that resolves to no such plugin
		// is a loud halt. The loader is backed by the process-wide shared
		// compilation cache, so a guest compiles once and every node, every later
		// build, and every concurrent served request reuses the machine code. The
		// routing is uniform across every caller of RunPipeline, so build, serve,
		// and the test harness all route through it with no per-caller change.
		sharedLoader := wasm.NewCachingLoaderFromContext(ctx, wasm.ExternalInputsFromContext(ctx))
		hybrid, providerVersion, closeEngines := enginefactory.NewHybrid(catalogLookup, rendererInput.PluginPathLookup, sharedLoader)
		// Release each provider engine's reused plugin instance at build end. A
		// guest that declares reusable_instance is loaded once per build and its
		// single instance lives on the per-build engine; this closes it after the
		// render (and eager compile) completes. A guest with no reuse claim holds
		// no instance, so the close is a no-op.
		defer func() { _ = closeEngines(ctx) }()
		rendererInput.ProviderVersion = providerVersion

		// Eager provider-guest compilation. When the caller opts in (a progress
		// surface that attributes the compile), force-compile the distinct provider
		// guests the render needs into the shared cache before the render loop, so a
		// cold guest compilation is a discrete phase the surface can report rather
		// than a stall inside the first node's render. It warms the same cache the
		// render loads hit, so it moves compilation earlier without changing output;
		// a warm cache makes each warm a near-instant cache hit. A compile failure
		// here is the same failure the render load would raise, surfaced earlier.
		if opts.PluginCompileBegin != nil {
			warmers, werr := collectProviderGuestWarmers(graph, recipeLookup, hybrid)
			if werr != nil {
				return fmt.Errorf("plugin compilation: %w", werr)
			}
			opts.PluginCompileBegin(len(warmers))
			for i, w := range warmers {
				if err := warmProviderGuest(ctx, w, opts.PluginCompileRunning); err != nil {
					return fmt.Errorf("plugin compilation: %w", err)
				}
				if opts.PluginCompileGuestDone != nil {
					opts.PluginCompileGuestDone(i + 1)
				}
			}
		}

		renderResult, rerr := renderer.RenderAll(ctx, rendererInput, recipeLookup, hybrid)
		if rerr != nil {
			return fmt.Errorf("rendering: %w", rerr)
		}

		emitRenderWarnings(os.Stderr, renderResult, opts.SuppressUnsupportedFieldWarnings)

		// Render-verdict governance role. Config-driven off the render-verdict
		// attachments in WorkspaceConfig.Plugins.Governance — a no-op when
		// unconfigured, and it runs those attachments over the rendered artifacts
		// (path-sorted). A deny verdict returns a build-halting error carrying
		// ErrPolicyViolation; warn / audit verdicts are non-halting diagnostics. A
		// verdict role never mutates the rendered artifacts.
		if err := runRenderPolicySocket(ctx, inputs, catalogLookup, renderResult.Files); err != nil {
			return err
		}

		result = &Result{
			Artifacts:         renderResult.FileBytes(),
			IRGraph:           graph,
			RenderDiagnostics: renderResult.Diagnostics,
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
