// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/analyzer"
	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/cognitio/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/cognitio/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/cognitio/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/internal/compiler/trace"
	"github.com/cognitio/aggrega-cli/pkg/catalog"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/resolver"

	"github.com/spf13/cobra"
)

var (
	releasePath  string
	catalogDir   string
	traceEnabled bool
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Compile Aggregate + Release into Helm artifacts via the 5-step pipeline",
	RunE:  runBuild,
}

func init() {
	buildCmd.Flags().StringVar(&releasePath, "release", "", "Path to Release YAML file (required)")
	buildCmd.Flags().StringVar(&catalogDir, "catalog", ".", "Directory containing catalog entities")
	buildCmd.Flags().BoolVar(&traceEnabled, "trace", false, "write per-node intermediate files to .trace/ (sibling to dist/) for post-mortem inspection")
	if err := buildCmd.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(buildCmd)
}

// runBuild orchestrates the 5-step compiler pipeline against the catalog
// at --catalog and the Release at --release, emitting rendered artifacts
// under dist/. Each step's output is the next step's input; the
// orchestration ordering is load-bearing for the pipeline's correctness:
//
//  1. Catalog load + extends resolution + deprecation surface — the
//     catalog must be fully resolved (inherited labels, secretNames,
//     usesSchema, composed schema) before any pipeline step consumes
//     catalog entities, because guards and renderer assume the
//     fully-merged shape.
//  2. Step 1 (Resolve) — loads the Release, derives Aggregate and
//     Manifest paths, runs the resolver to produce a ResolveResult,
//     then runs the resolver guards. Guard violations halt before
//     any topology work.
//  3. Step 2 (Topology) — flattens the Aggregate's component graph
//     and computes the topology result.
//  4. Step 3 (IR) — assembles the IR graph from the topology result
//     plus the resolved Aggregate / Release / catalog.
//  5. Step 4 (Analyze) — runs static-analysis checks against the IR
//     graph (selector validation, appliesTo validation). Non-empty
//     analysis errors halt before rendering.
//  6. Step 5 (Render) — invokes the renderer with the IR graph, the
//     feature execution order from Step 3, the resolve result, and
//     the catalog lookup. The engineFactory closure (declared inline
//     below) dispatches per-Recipe to the appropriate renderer.Engine.
//
// On error at any step, the function returns a wrapped error and cobra
// displays it via the Execute error path. Per-step diagnostic output
// (extends errors, deprecation warnings, guard violations, selector
// violations, render warnings) writes to stderr inline; the returned
// error is a count summary suitable for cobra display.
//
// On success, dist/boundaries/ is cleaned (stale files from prior
// builds don't leak into this one, preserving GitOps byte-identity)
// and the rendered files are written to dist/. A summary line writes
// to stdout.
func runBuild(cmd *cobra.Command, args []string) error {
	// Load catalog from directory
	cat, err := catalog.Load(catalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	// Resolve extends inheritance for every Component and Feature so downstream
	// guards/renderer see the fully-merged entity (inherited labels, secretNames,
	// usesSchema, composed schema).
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(os.Stderr, "  extends: %s\n", e)
		}
		return fmt.Errorf("catalog extends: %d resolution error(s)", len(extErrs))
	}

	// Surface non-fatal deprecation notices (e.g. legacy secretNames usage).
	for _, w := range cat.DeprecationWarnings() {
		fmt.Fprintf(os.Stderr, "  deprecation: %s\n", w)
	}

	// Step 1: Resolve
	relData, err := os.ReadFile(releasePath)
	if err != nil {
		return fmt.Errorf("read release: %w", err)
	}

	// Determine aggregate and manifest paths from release directory
	releaseDir := filepath.Dir(releasePath)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	_ = relData

	aggPath := filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml")
	manifestPath := filepath.Join(releaseDir, rel.Spec.Manifest+".yaml")

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: aggPath,
		ReleasePath:   releasePath,
		ManifestPath:  manifestPath,
	})
	if err != nil {
		return fmt.Errorf("Step 1 (resolve): %w", err)
	}
	// Parameter map flows through Steps 2-5 so orchestrator-time path-template
	// evaluation can consume .parameters.<key> references in directory: and
	// subDirectory: templates. Active-purge runs as runBuild exits (any return
	// path), bounding the in-memory parameter window to one build invocation.
	defer resolver.PurgeParameters(&resolveResult.Parameters)

	// Run guards
	catalogLookup := cat.CatalogLookup()
	guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra)
	if len(guardErrs) > 0 {
		for _, e := range guardErrs {
			fmt.Fprintf(os.Stderr, "  guard: %s\n", e)
		}
		return fmt.Errorf("Step 1: %d guard violation(s)", len(guardErrs))
	}

	// Step 2: Topology
	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: cat.SubAggregateLoader(),
	})
	if err != nil {
		return fmt.Errorf("Step 2 (topology): %w", err)
	}

	// Step 3: IR
	graph, featureOrder, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		return fmt.Errorf("Step 3 (IR): %w", err)
	}

	// Step 4: Analyze
	analysis := analyzer.Analyze(graph, catalogLookup)
	if len(analysis.Errors) > 0 {
		// Bubble each violation as a CompilerError via errors.Join. The
		// formatter walks the join'd chain and renders each violation
		// rich-formatted; the stderr side-effect printing the previous
		// shape used has been removed in favor of the structured chain.
		// errors.Join'd nil entries are dropped, but every analyzer error
		// here is non-nil by construction (len > 0 gate).
		errs := make([]error, len(analysis.Errors))
		for i := range analysis.Errors {
			errs[i] = analysis.Errors[i].ToCompilerError()
		}
		return stderrors.Join(errs...)
	}

	// Step 4b: Component / Feature assertions.
	if err := renderer.EvaluateComponentAndFeatureAssertions(graph, catalogLookup); err != nil {
		return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, "Step 4b (Component/Feature assertions)")
	}

	// Step 5: Render
	recipeLookup := cat.RecipeLookup()

	// Construct the trace writer. When --trace is off, New("") returns a
	// no-op writer; on, it returns an async filesystem writer rooted at
	// `.trace/` (sibling to `dist/`, never under). The sibling layout
	// keeps `dist/` purely deployable: no flag-conditional contents, no
	// apply-tool walking surprises, no operator-authored boundary
	// `directory:` collision risk against an Aggrega-managed dot-dir.
	// Close drains in-flight async writes; deferred so any return path
	// (including error) flushes the trace surface.
	traceRoot := ""
	if traceEnabled {
		traceRoot = ".trace"
		if err := os.RemoveAll(traceRoot); err != nil {
			return fmt.Errorf("clean %s: %w", traceRoot, err)
		}
	}
	traceWriter := trace.New(traceRoot)
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

	// engineFactory dispatches the providerName each Recipe carries
	// (recipe.spec.provider) to the renderer.Engine that will render it.
	// All provider engines construct fresh-per-call: cross-call shared
	// state belongs at the per-(path, provider) BoundaryAssembler level,
	// not at the engineFactory closure level. Three provider names ship:
	//
	//   - "helm" — fresh HelmEngine per call. The empty-string branch
	//     also routes here, preserved as a defensive default; current
	//     Recipe schema requires spec.provider, so empty values do
	//     not reach this dispatch in normal flow.
	//   - "helm-chart" — fresh helmchart.HelmEngine per call.
	//   - "compose" — fresh ComposeEngine per call. Per-(path, provider)
	//     dispatch grouping in the orchestrator (BoundaryAssembler tier)
	//     handles cross-call aggregation; the engine itself is stateless.
	//
	// Unknown providerName wraps renderer.ErrUnknownProvider with the
	// offending name so the upstream RenderAll error surfaces with
	// dispatch failure context intact.
	engineFactory := func(providerName string) (renderer.Engine, error) {
		switch providerName {
		case "helm", "":
			return &renderHelm.HelmEngine{}, nil
		case "helm-chart":
			return &renderHelmChart.HelmEngine{}, nil
		case "compose":
			return &renderCompose.ComposeEngine{}, nil
		default:
			return nil, fmt.Errorf("%w: %q", renderer.ErrUnknownProvider, providerName)
		}
	}

	renderResult, err := renderer.RenderAll(context.Background(), rendererInput, recipeLookup, engineFactory)
	if err != nil {
		return fmt.Errorf("Step 5 (render): %w", err)
	}

	for _, w := range renderResult.Warnings {
		fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
	}

	// Write output. Clean dist/boundaries/ first so stale files from a prior
	// build of a different variant don't leak into this one (GitOps consumers
	// must see exactly what the compiler just produced).
	outDir := filepath.Join("dist")
	if err := os.RemoveAll(filepath.Join(outDir, "boundaries")); err != nil {
		return fmt.Errorf("clean %s/boundaries: %w", outDir, err)
	}
	if err := writeArtifacts(outDir, renderResult.Files); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	fmt.Printf("Build complete: %d files written to %s/\n", len(renderResult.Files), outDir)
	return nil
}

// writeArtifacts writes the rendered file map to disk under outRoot,
// preserving relative path structure and creating intermediate
// directories as needed. Each entry's relative path is cleaned and
// validated:
//
//   - Empty paths are rejected (defensive guard against renderer
//     producing a zero-key entry).
//   - Path-traversal attempts ("..", or paths beginning with "../")
//     are rejected. The check defends against a malicious or buggy
//     renderer engine emitting a path that escapes outRoot.
//
// On any per-file error (mkdir, write), the function returns the
// wrapped error and stops — partial writes to disk persist; the
// caller is responsible for any cleanup in the partial-write case.
func writeArtifacts(outRoot string, files map[string][]byte) error {
	outRoot = filepath.Clean(outRoot)
	for relPath, content := range files {
		if relPath == "" {
			return fmt.Errorf("empty artifact path")
		}
		relPath = filepath.Clean(relPath)
		if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid artifact path %q", relPath)
		}
		full := filepath.Clean(filepath.Join(outRoot, relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return fmt.Errorf("write %q: %w", full, err)
		}
	}
	return nil
}
