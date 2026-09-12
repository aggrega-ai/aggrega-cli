// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fixtureWorkspace resolves the self-contained fullstack-app fixture — a
// Release with its sibling Manifest and Aggregate plus a local catalog — and
// builds a request-scoped catalog lookup over it. The three shared operations
// resolve entirely against the local catalog (nil puller: registry pull never
// fires), so the fixture exercises the layer hermetically.
func fixtureWorkspace(t *testing.T) (view *catalog.LazyLookup, releasePath, workspaceRoot string, wsCfg workspaceconfig.Config) {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	// This test runs from internal/compilerops/; the module root is two dirs up.
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	workspaceRoot = filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app")
	catalogDir := filepath.Join(workspaceRoot, "catalog")
	releasePath = filepath.Join(workspaceRoot, "release.yaml")

	wsCfg = workspaceconfig.Default()
	lazy := catalog.NewLazyLookup(workspaceRoot, wsCfg.ResolvedArtifactCacheDir(), []string{catalogDir}, nil)
	return lazy.ForRequest(nil), releasePath, workspaceRoot, wsCfg
}

// TestSharedOperationsAgainstFixture drives the three shared compiler
// operations end-to-end against explicit inputs: ResolveClosure produces the
// closure, Validate runs the semantic core clean, and BuildToBuffer builds the
// IR graph in memory. It is the standalone-callability pin — the layer runs
// with no front-end state, only the explicit inputs.
func TestSharedOperationsAgainstFixture(t *testing.T) {
	view, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)
	ctx := context.Background()

	resolved, cerr := ResolveClosure(context.Background(), ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   workspaceRoot,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure: stage=%d %v", cerr.Stage, cerr)
	}
	if resolved.Release == nil || resolved.Manifest == nil || resolved.Aggregate == nil {
		t.Fatalf("ResolveClosure returned incomplete entities: %+v", resolved)
	}
	if len(resolved.AggBytes) == 0 {
		t.Fatalf("ResolveClosure returned empty aggregate bytes")
	}
	if resolved.AggregatePath == "" {
		t.Fatalf("ResolveClosure returned empty aggregate path")
	}

	vr := Validate(ctx, ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   resolved.AggregatePath,
		WorkspaceConfig: wsCfg,
		CompilerVersion: "test",
	})
	if len(vr.ClosureExtendsErrors) > 0 {
		t.Fatalf("Validate closure-extends gate: %v", vr.ClosureExtendsErrors)
	}
	if vr.PillarError != nil {
		t.Fatalf("Validate pillar gate: %v", vr.PillarError)
	}
	if vr.RunErr != nil {
		t.Fatalf("Validate pipeline error: %v", vr.RunErr)
	}
	if vr.Tier4 == nil || vr.Tier4.IRGraph == nil {
		t.Fatalf("Validate produced no IR graph: %+v", vr.Tier4)
	}

	res, err := BuildToBuffer(ctx, BuildInputs{
		View:                             view,
		Resolved:                         resolved,
		ReleasePath:                      releasePath,
		WorkspaceConfig:                  wsCfg,
		WorkspaceRoot:                    workspaceRoot,
		CompilerVersion:                  "test",
		TolerateCatalogViewInconsistency: true,
	}, pipeline.Options{SkipRender: true})
	if err != nil {
		t.Fatalf("BuildToBuffer: %v", err)
	}
	if completed := res.Completed(); completed == nil || completed.IRGraph == nil {
		t.Fatalf("BuildToBuffer produced no IR graph: %+v", res)
	}
}

// TestResolveClosureReportsLoadStage confirms a failure at the first stage
// (loading the Release) is reported with the load-release stage tag rather than
// as a closure-compute failure, so a caller attributes the failure to the
// Release path.
func TestResolveClosureReportsLoadStage(t *testing.T) {
	view, _, workspaceRoot, wsCfg := fixtureWorkspace(t)
	_, cerr := ResolveClosure(context.Background(), ClosureInputs{
		View:            view,
		ReleasePath:     filepath.Join(workspaceRoot, "does-not-exist.yaml"),
		WorkspaceRoot:   workspaceRoot,
		WorkspaceConfig: wsCfg,
	})
	if cerr == nil {
		t.Fatalf("expected a ClosureError for a missing Release")
	}
	if cerr.Stage != ClosureStageLoadRelease {
		t.Fatalf("Stage = %d; want ClosureStageLoadRelease (%d)", cerr.Stage, ClosureStageLoadRelease)
	}
}
