// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog/catalogtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIntegration_RegistryPipelinePlugin_PullAndReconstructionAgree compares the
// two sides of the drift verify directly: what the pull pinned, and what the
// build's reconstruction re-derives, on the entity axis and on the plugin axis —
// both non-empty, since the plugin rows are the subject. Both sides descend those
// axes through the one computation (closure.DescendPinnedAxes), each reached
// through its own surface — the pull through pullops, the build through the
// preview — so the comparison is between the two sets of arguments that
// computation is handed. The workspace is the one
// registry_plugin_frozen_build_integration_test.go lays down.
//
// The negative control reconstructs the same closure with its plugin passes
// handed a lookup that carries no pins, which cannot address a pinned .wasm. The
// comparison must report the artifact missing there; if it did not, agreement
// below would say nothing about whether the reconstruction can see the rows.
func TestIntegration_RegistryPipelinePlugin_PullAndReconstructionAgree(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	f := writeRegistryPluginFixture(t, startRegistry(t, nil), pluginPath, std, true)
	t.Chdir(f.root)
	lf := f.pull(t)
	f.assertPluginPinned(t, lf)

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root})
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	releasePath := filepath.Join(f.root, "release.yaml")
	dirs := stackFor(t, rw, releasePath, nil)

	// The build side, through the build's own computation: the lazy view pinned to
	// the lockfile, a nil puller, the lockfile held as one that was on disk.
	view := catalog.NewLazyLookup(f.root, cacheDir, dirs, nil)
	view.SetPins(catalogtest.MustPinsFromLockfile(t, lf))
	preview, err := compilerops.PreviewForArtifact(context.Background(),
		compilerops.NewPreviewInputs(compilerops.NoExternalInputs(), view, releasePath, f.root, wsCfg, "test",
			compilerops.WithLockfile(lf, true)),
		pipeline.Options{SkipRender: true})
	if err != nil {
		t.Fatalf("the build's computation over the pulled lockfile: %v", err)
	}
	rebuilt := preview.Resolved.Closure

	lockEntities, lockPlugins := lockfileURIs(lf)
	if missing, extra := setDiff(lockEntities, refURIs(rebuilt.Entities)); len(missing) > 0 || len(extra) > 0 {
		t.Errorf("entity axis: pinned but not reconstructed %v; reconstructed but not pinned %v", missing, extra)
	}
	if missing, extra := setDiff(lockPlugins, refURIs(rebuilt.Plugins)); len(missing) > 0 || len(extra) > 0 {
		t.Errorf("plugin axis: pinned but not reconstructed %v; reconstructed but not pinned %v", missing, extra)
	}
	if !containsString(refURIs(rebuilt.Plugins), f.artifactURI) {
		t.Errorf("the reconstructed plugin axis lacks %q; plugins=%+v", f.artifactURI, rebuilt.Plugins)
	}

	// Negative control: the same reconstruction with no pins on its plugin passes.
	controlView := catalog.NewLazyLookup(f.root, cacheDir, dirs, nil)
	controlView.SetPins(catalogtest.MustPinsFromLockfile(t, lf))
	resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View: controlView, ReleasePath: releasePath, WorkspaceRoot: f.root, WorkspaceConfig: wsCfg, Lockfile: lf, Augment: true,
	})
	if cerr != nil {
		t.Fatalf("ResolveClosure for the control: %v", cerr)
	}
	aggLoader, aerr := closure.RequireAggregateLoader(resolved.Lookup)
	if aerr != nil {
		t.Fatalf("RequireAggregateLoader: %v", aerr)
	}
	bindings := lockfile.ProviderBindings(lf)
	control := resolved.Closure
	if rerr := closure.DescendPinnedAxes(context.Background(), &control, aggLoader, controlView.ClosureRecipeLoader(), resolved.Lookup,
		nil, // no pinned lookup: the plugin passes fall back to the unpinned one
		func(ref string) (types.Plugin, bool, error) {
			return controlView.ProviderPluginForTarget(ref, bindings)
		},
		func() (closure.RecipeLoader, error) {
			controlView.SetPins(catalogtest.MustPinsFromClosure(t, control))
			return controlView.ClosureRecipeLoader(), nil
		},
		nil, // a build asks the entity pass nothing
		nil, // the plugin under test is registry-published, so no catalog-local artifact is asked about
	); rerr != nil {
		t.Fatalf("control reconstruction: %v", rerr)
	}
	if missing, _ := setDiff(lockPlugins, refURIs(control.Plugins)); !containsString(missing, f.artifactURI) {
		t.Fatalf("control: a reconstruction whose plugin passes carry no pins must fail to re-derive %q, "+
			"or the comparison above cannot tell a reconstruction that sees the plugin rows from one that does not; "+
			"control plugins=%+v", f.artifactURI, control.Plugins)
	}
}

func lockfileURIs(lf *lockfile.Lockfile) (entities, plugins []string) {
	for _, e := range lf.Entities {
		entities = append(entities, e.URI)
	}
	for _, p := range lf.Plugins {
		plugins = append(plugins, p.URI)
	}
	sort.Strings(entities)
	sort.Strings(plugins)
	return entities, plugins
}

func refURIs(refs []closure.ResolvedRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.URI)
	}
	sort.Strings(out)
	return out
}
