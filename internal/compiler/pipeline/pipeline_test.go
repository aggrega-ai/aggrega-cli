// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	fullstackAggregatePath = "../../../internal/compiler/testdata/fullstack-app/aggregate.yaml"
	fullstackReleasePath   = "../../../internal/compiler/testdata/fullstack-app/release.yaml"
	fullstackManifestPath  = "../../../internal/compiler/testdata/fullstack-app/manifest.yaml"
	fullstackCatalogPath   = "../../../internal/compiler/testdata/fullstack-app/catalog"
	goldenFullstackDir     = "testdata/golden-fullstack-app"
)

// productionEngineFactory mirrors the closure constructed inside
// cmd/aggrega/build.go's dispatchBuild. Keeping the dispatch identical
// guarantees the test exercises the same provider-engine path the CLI
// does.
func productionEngineFactory(providerName string) (renderer.Engine, error) {
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

// TestRunPipelineProducesExpectedArtifactsFromInMemoryInputs invokes
// RunPipeline with pre-loaded Release / Manifest / raw Aggregate bytes
// for the fullstack-app fixture and asserts Result.Artifacts matches
// the committed golden tree byte-for-byte. The golden tree carries
// pre-refactor dispatchBuild dist/ output verbatim; the assertion
// proves the refactor preserved byte-identity for the CLI build path.
func TestRunPipelineProducesExpectedArtifactsFromInMemoryInputs(t *testing.T) {
	t.Parallel()
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.Load("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d resolution error(s); first: %v", len(extErrs), extErrs[0])
	}
	result, err := pipeline.RunPipeline(
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     fullstackAggregatePath,
			WorkspaceConfig:   workspaceconfig.Default(),
		},
		cat.CatalogLookup(),
		cat.RecipeLookup(),
		cat.SubAggregateLoader(),
		productionEngineFactory,
		pipeline.Options{IncludeTrace: false},
	)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	wantPaths := map[string][]byte{}
	walkErr := filepath.Walk(goldenFullstackDir, func(p string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		key, _ := filepath.Rel(goldenFullstackDir, p)
		wantPaths[filepath.ToSlash(key)] = body
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk golden: %v", walkErr)
	}
	if len(wantPaths) == 0 {
		t.Fatalf("golden tree at %s is empty; the test cannot proceed", goldenFullstackDir)
	}

	// The renderer's artifact map already keys outputs under dist/<...>;
	// the CLI wrapper passes "dist" as outDir to writeArtifacts, which
	// concatenates the prefix in filepath.Join — producing dist/dist/<...>
	// on disk. The golden tree was captured from the on-disk physical
	// dist/ directory, so its filepath.Rel-derived keys land at
	// dist/<...> (matching the renderer's keys verbatim). Compare directly.
	gotKeys := keysOf(result.Artifacts)
	wantKeys := keysOf(wantPaths)
	if !equalStringSlices(gotKeys, wantKeys) {
		t.Errorf("artifact key set mismatch:\n  got:  %v\n  want: %v", gotKeys, wantKeys)
	}
	for k, want := range wantPaths {
		got, ok := result.Artifacts[k]
		if !ok {
			t.Errorf("missing artifact key %q", k)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("byte mismatch on %q (got %d bytes, want %d bytes)", k, len(got), len(want))
		}
	}
}

// TestRunPipelineReturnsEmptyTraceWhenIncludeTraceFalse asserts that
// passing Options.IncludeTrace=false leaves Result.Trace empty (nil or
// empty map both acceptable per the documented contract).
func TestRunPipelineReturnsEmptyTraceWhenIncludeTraceFalse(t *testing.T) {
	t.Parallel()
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.Load("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d resolution error(s); first: %v", len(extErrs), extErrs[0])
	}
	result, err := pipeline.RunPipeline(
		pipeline.Inputs{
			Release: rel, Manifest: manifest,
			AggregateRawBytes: aggBytes, AggregatePath: fullstackAggregatePath,
			WorkspaceConfig: workspaceconfig.Default(),
		},
		cat.CatalogLookup(), cat.RecipeLookup(), cat.SubAggregateLoader(),
		productionEngineFactory,
		pipeline.Options{IncludeTrace: false},
	)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(result.Trace) != 0 {
		t.Errorf("Result.Trace not empty when IncludeTrace=false; got %d entries", len(result.Trace))
	}
}

// TestRunPipelinePopulatesTraceWhenIncludeTraceTrue asserts that passing
// Options.IncludeTrace=true causes Result.Trace to be populated with
// per-pass entries. The exact entry count depends on the fixture; the
// test asserts non-empty and well-formed paths.
func TestRunPipelinePopulatesTraceWhenIncludeTraceTrue(t *testing.T) {
	t.Parallel()
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.Load("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d resolution error(s); first: %v", len(extErrs), extErrs[0])
	}
	result, err := pipeline.RunPipeline(
		pipeline.Inputs{
			Release: rel, Manifest: manifest,
			AggregateRawBytes: aggBytes, AggregatePath: fullstackAggregatePath,
			WorkspaceConfig: workspaceconfig.Default(),
		},
		cat.CatalogLookup(), cat.RecipeLookup(), cat.SubAggregateLoader(),
		productionEngineFactory,
		pipeline.Options{IncludeTrace: true},
	)
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if len(result.Trace) == 0 {
		t.Errorf("Result.Trace empty when IncludeTrace=true; expected per-pass entries")
	}
	for k := range result.Trace {
		if k == "" {
			t.Errorf("empty key in Result.Trace")
		}
	}
}

// TestRunPipelineWritesNoFilesystemArtifacts pins the no-side-effects
// contract: RunPipeline itself must not touch the filesystem under the
// test's CWD. The wrapper at cmd/aggrega/build.go owns dist/ and .trace/
// writing; RunPipeline returns maps.
//
// The test changes CWD to t.TempDir() before invoking RunPipeline,
// asserts no dist/ or .trace/ directory exists after the call.
func TestRunPipelineWritesNoFilesystemArtifacts(t *testing.T) {
	// NOTE: not t.Parallel — os.Chdir is process-global.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	rel, err := resolver.LoadRelease(fullstackReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(fullstackManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggBytes, err := os.ReadFile(fullstackAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.Load("", fullstackCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d resolution error(s); first: %v", len(extErrs), extErrs[0])
	}

	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if _, err := pipeline.RunPipeline(
		pipeline.Inputs{
			Release: rel, Manifest: manifest,
			AggregateRawBytes: aggBytes, AggregatePath: fullstackAggregatePath,
			WorkspaceConfig: workspaceconfig.Default(),
		},
		cat.CatalogLookup(), cat.RecipeLookup(), cat.SubAggregateLoader(),
		productionEngineFactory,
		pipeline.Options{IncludeTrace: true},
	); err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	for _, name := range []string{"dist", ".trace"} {
		if _, err := os.Stat(filepath.Join(tmp, name)); err == nil {
			t.Errorf("RunPipeline created %q under test CWD; expected zero filesystem side effects", name)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
