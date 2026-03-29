package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/builder"
	"github.com/cognitio/aggrega-cli/internal/compiler/provider"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/spf13/cobra"
)

const (
	aggregateFile = "aggregate.yaml"
	manifestFile  = "manifest.yaml"
	releaseFile   = "release.yaml"
)

var buildScope string

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Compile Aggregate + Manifest into IR and render artifacts for a release scope",
	Run:   runBuild,
}

func init() {
	buildCmd.Flags().StringVar(&buildScope, "scope", "", "Release scope name to build (must match spec.scopes[].name, e.g. local or prod)")
	if err := buildCmd.MarkFlagRequired("scope"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) {
	_ = cmd
	_ = args

	if err := validateScopeSegment(buildScope); err != nil {
		log.Fatalf("aggrega build: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("aggrega build: get working directory: %v", err)
	}

	aggPath := filepath.Join(wd, aggregateFile)
	manPath := filepath.Join(wd, manifestFile)
	relPath := filepath.Join(wd, releaseFile)

	agg, err := builder.ParseAggregateFile(aggPath)
	if err != nil {
		log.Fatalf("aggrega build: %v", err)
	}
	man, err := builder.ParseManifestFile(manPath)
	if err != nil {
		log.Fatalf("aggrega build: %v", err)
	}
	if err := builder.ResolveManifestAgainstAggregate(agg, man); err != nil {
		log.Fatalf("aggrega build: manifest resolution failed: %v", err)
	}

	gb := &builder.GraphBuilder{CompilerVersion: "aggrega-cli"}
	graph, err := gb.BuildIRFromParsed(agg, man, aggPath, manPath)
	if err != nil {
		log.Fatalf("aggrega build: build IR: %v", err)
	}

	release, err := builder.ParseReleaseFile(relPath)
	if err != nil {
		log.Fatalf("aggrega build: %v", err)
	}

	scope, ok := findScope(release, buildScope)
	if !ok {
		log.Fatalf("aggrega build: no scope named %q in %s (spec.scopes[].name)", buildScope, releaseFile)
	}

	engine, err := provider.GetEngine(scope.Provider)
	if err != nil {
		log.Fatalf("aggrega build: %v", err)
	}

	artifacts, err := engine.Render(context.Background(), graph, release, scope)
	if err != nil {
		log.Fatalf("aggrega build: render: %v", err)
	}

	outRoot := filepath.Join(wd, ".aggrega", "dist", buildScope)
	if err := writeArtifacts(outRoot, artifacts); err != nil {
		log.Fatalf("aggrega build: write output: %v", err)
	}

	fmt.Printf("wrote %d file(s) under %s\n", len(artifacts), outRoot)
}

func validateScopeSegment(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("--scope must not be empty")
	}
	if strings.Contains(s, "..") || strings.ContainsAny(s, `/\`) {
		return fmt.Errorf("--scope %q must not contain path separators or '..'", s)
	}
	return nil
}

func findScope(rel *types.Release, name string) (types.Scope, bool) {
	for _, s := range rel.Spec.Scopes {
		if s.Name == name {
			return s, true
		}
	}
	return types.Scope{}, false
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
		relToOut, err := filepath.Rel(outRoot, full)
		if err != nil {
			return fmt.Errorf("artifact path %q: %w", relPath, err)
		}
		if relToOut == ".." || strings.HasPrefix(relToOut, ".."+string(filepath.Separator)) {
			return fmt.Errorf("artifact path %q escapes output directory", relPath)
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return fmt.Errorf("write %q: %w", full, err)
		}
	}
	return nil
}
