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
}

// Options carries per-invocation flags. IncludeTrace, when true, causes
// RunPipeline to construct an in-memory trace writer and populate
// Result.Trace with the collected per-pass entries; when false,
// Result.Trace stays nil and the trace writer is a no-op.
type Options struct {
	IncludeTrace bool
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
func RunPipeline(
	inputs Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
	subAggregateLoader func(path string) (*types.Aggregate, error),
	engineFactory renderer.EngineFactory,
	opts Options,
) (*Result, error) {
	// Step 1: Resolve (annotation binding validation + binding lint +
	// expand annotations + guards happen below).
	resolveResult, err := resolver.ResolveFromInputs(resolver.ResolveInputs{
		Release:           inputs.Release,
		Manifest:          inputs.Manifest,
		AggregateRawBytes: inputs.AggregateRawBytes,
		AggregatePath:     inputs.AggregatePath,
	})
	if err != nil {
		return nil, fmt.Errorf("Step 1 (resolve): %w", err)
	}
	// Purge the merged parameter map as RunPipeline exits so the lifetime
	// matches the rendering pass exactly. Any return path triggers purge.
	defer resolver.PurgeParameters(&resolveResult.Parameters)

	// Validate annotation binding target paths against each target
	// Feature's spec.schema before expansion runs. A binding that
	// routes a value to a path the Feature schema does not declare
	// halts here with a clear OPS_ERROR, ahead of any synthesis that
	// would carry the unrecognized field into downstream validation.
	if err := resolver.ValidateAnnotationBindingTargets(resolveResult.Manifest, catalogLookup); err != nil {
		return nil, fmt.Errorf("Step 1 (annotation binding validation): %w", err)
	}

	// Surface non-fatal Manifest binding-map lint warnings (same-path
	// overlap across Maps.Infra and Maps.Values).
	for _, w := range resolver.LintAnnotationBindingMaps(resolveResult.Manifest) {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}

	// Annotation-driven Feature expansion runs between the mechanical
	// resolver and the guards pass so synthetic Feature attachments are
	// visible to RunGuards' requiredFeatures completeness check.
	if err := resolver.ExpandAnnotations(resolveResult, catalogLookup); err != nil {
		return nil, fmt.Errorf("Step 1 (annotation expansion): %w", err)
	}

	// Run guards
	guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra)
	if len(guardErrs) > 0 {
		for _, e := range guardErrs {
			fmt.Fprintf(os.Stderr, "  guard: %s\n", e)
		}
		return nil, fmt.Errorf("Step 1: %d guard violation(s)", len(guardErrs))
	}

	// Step 2: Topology
	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: subAggregateLoader,
	})
	if err != nil {
		return nil, fmt.Errorf("Step 2 (topology): %w", err)
	}

	// Step 3: IR
	graph, featureOrder, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		return nil, fmt.Errorf("Step 3 (IR): %w", err)
	}

	// Step 4: Analyze
	analysis := analyzer.Analyze(graph, catalogLookup)
	for _, w := range analysis.Warnings {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}
	if len(analysis.Errors) > 0 {
		errs := make([]error, len(analysis.Errors))
		for i := range analysis.Errors {
			errs[i] = analysis.Errors[i].ToCompilerError()
		}
		return nil, stderrors.Join(errs...)
	}

	// Step 4b: Component / Feature assertions.
	if err := renderer.EvaluateComponentAndFeatureAssertions(graph, catalogLookup); err != nil {
		return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, "Step 4b (Component/Feature assertions)")
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
		Graph:         graph,
		FeatureOrder:  featureOrder,
		ResolveResult: resolveResult,
		CatalogLookup: catalogLookup,
		Trace:         traceWriter,
	}

	renderResult, err := renderer.RenderAll(context.Background(), rendererInput, recipeLookup, engineFactory)
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
