// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
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
	"github.com/cognitio/aggrega-cli/pkg/catalog"
	"github.com/cognitio/aggrega-cli/pkg/resolver"

	"github.com/spf13/cobra"
)

var (
	releasePath string
	catalogDir  string
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Compile Aggregate + Release into Helm artifacts via the 5-step pipeline",
	RunE:  runBuild,
}

func init() {
	buildCmd.Flags().StringVar(&releasePath, "release", "", "Path to Release YAML file (required)")
	buildCmd.Flags().StringVar(&catalogDir, "catalog", ".", "Directory containing catalog entities")
	if err := buildCmd.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	// Load catalog from directory
	cat, err := catalog.Load(catalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	// Resolve extends inheritance for every Component and Feature so downstream
	// guards/renderer see the fully-merged entity (inherited labels, secretNames,
	// usesSchema, unified cueSchema).
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
		for _, e := range analysis.Errors {
			fmt.Fprintf(os.Stderr, "  selector: %s -> %s (%s): %s\n", e.Source, e.Target, e.Socket, e.Message)
		}
		return fmt.Errorf("Step 4: %d selector violation(s)", len(analysis.Errors))
	}

	// Step 5: Render
	recipeLookup := cat.RecipeLookup()
	rendererInput := renderer.RendererInput{
		Graph:         graph,
		FeatureOrder:  featureOrder,
		ResolveResult: resolveResult,
		CatalogLookup: catalogLookup,
	}

	// composeEngine is memoised so every "compose" dispatch in this Release
	// returns the SAME instance — the orchestrator's Release-end pass groups
	// nodes by ReleaseComposer instance identity, so a fresh instance per
	// node would split fragments across multiple ComposeRelease calls and
	// produce one docker-compose.yaml per node instead of one per Release.
	composeEngine := &renderCompose.ComposeEngine{}

	// engineFactory dispatches the providerName each Recipe carries
	// (recipe.spec.provider) to the renderer.Engine that will render
	// it. Three provider names ship:
	//
	//   - "helm" — fresh HelmEngine per call. The empty-string branch
	//     also routes here, preserved as a defensive default; current
	//     Recipe schema requires spec.provider, so empty values do
	//     not reach this dispatch in normal flow.
	//   - "helm-chart" — fresh helmchart.HelmEngine per call.
	//   - "compose" — the memoised composeEngine declared above (see
	//     that comment for why instance identity matters here).
	//
	// The fresh-vs-memoised asymmetry tracks engine statefulness:
	// helm and helm-chart hold no per-Release state, so per-dispatch
	// construction is correct and avoids cross-Release leakage;
	// compose holds Release-level fragment state that must be shared
	// across every node-level dispatch in the same Release.
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
			return composeEngine, nil
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
