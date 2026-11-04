// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// writeEmptySourceWorkspace lays down the minimal buildable workspace of
// writeValidWorkspace and then rewrites its Recipe so the Recipe's source field
// carries the literal text in sourceLine — the whole point being that the three
// cases the empty-source halt has to separate differ in exactly that one line and
// in nothing else.
//
// The Recipe keeps its workload alongside whatever source the case declares, so
// every case declares an input kind whatever its source line says: the sourceless
// and real-source cases build, and the empty-source case is refused for the empty
// source alone rather than for anything else the document lacks.
func writeEmptySourceWorkspace(t *testing.T, root, sourceLine string) (releasePath, catalogRoot string) {
	t.Helper()
	releasePath, catalogRoot = writeValidWorkspace(t, root)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
`+sourceLine+`  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
	return releasePath, catalogRoot
}

// primeLockfile fills the release lockfile the way a build expects to find it.
// A build reads the lockfile and never writes one, so a workspace that has never
// been pulled fails on the missing lockfile before any rendering happens — which
// would leave every case below proving only that.
func primeLockfile(t *testing.T, root, releasePath, catalogRoot string) {
	t.Helper()
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
}

// renderInMemoryOver drives the render entry that renders a Release to in-memory
// buffers with no lockfile freshness gate — the one the editor build tool and the
// daemon both call — and returns the catalog view it read, the closure's failure
// and the render's. That is what lets a test hand a workspace straight to
// rendering: a command-line build reads a lockfile and refuses one that no longer
// matches the catalog, which is a constraint on the build command rather than on
// whatever the render itself decides.
//
// A closure failure leaves the render undriven, so at most one of the two
// failures is set. The view is returned because the surfaces calling this entry
// report a failure with the catalog files that view's load refused.
func renderInMemoryOver(
	t *testing.T, root, releasePath, catalogRoot string,
) (*catalog.LazyLookup, *compilerops.ClosureError, error) {
	t.Helper()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	view := catalog.NewLazyLookup(root, filepath.Join(root, ".aggrega", "cache"), []string{catalogRoot}, nil)
	resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   root,
		WorkspaceConfig: rw.Config(),
		Augment:         true,
	})
	if cerr != nil {
		return view, cerr, nil
	}
	_, err := compilerops.BuildToBuffer(context.Background(), compilerops.BuildInputs{
		View:                             view,
		Resolved:                         resolved,
		ReleasePath:                      releasePath,
		WorkspaceConfig:                  rw.Config(),
		WorkspaceRoot:                    root,
		CompilerVersion:                  "test",
		TolerateCatalogViewInconsistency: true,
	}, pipeline.Options{})
	return view, nil, err
}

// renderInMemory renders through the in-memory entry and returns the render's
// failure, stopping the test on a closure that does not resolve: every caller
// hands it a workspace whose closure is sound, so such a failure is a broken
// harness rather than the result under test.
func renderInMemory(t *testing.T, root, releasePath, catalogRoot string) error {
	t.Helper()
	_, cerr, err := renderInMemoryOver(t, root, releasePath, catalogRoot)
	if cerr != nil {
		t.Fatalf("resolve closure: %v", cerr)
	}
	return err
}

// leadingFailure is the failure a reader of err meets first: the refused catalog
// file a skip account promoted ahead of the failure it explains, where one was
// promoted, and err itself otherwise.
//
// A Recipe refused while it is loaded never reaches the dependency resolution
// that asked for it, so what that resolution reports is the reference it could
// not bind, with the refusal promoted ahead of it. The refusal is what names the
// author's fault, so it is the failure the condition is matched on.
func leadingFailure(err error) error {
	var skipped *compilerops.SkipContextError
	if errors.As(err, &skipped) {
		return skipped.Leading()
	}
	return err
}

// renderInMemoryReportingRefusals renders through the in-memory entry and reports
// its failure the way the surfaces that call that entry report one: annotated
// with the catalog files the load refused, so a refusal that explains the failure
// leads it.
//
// It returns the closure's failure rather than stopping the test on it, because a
// Recipe the load refused is one the closure can no longer find, and that miss is
// part of the failure being asserted rather than a broken harness.
func renderInMemoryReportingRefusals(t *testing.T, root, releasePath, catalogRoot string) error {
	t.Helper()
	view, cerr, err := renderInMemoryOver(t, root, releasePath, catalogRoot)
	if cerr != nil {
		return compilerops.AnnotateSkipContext(cerr, view.SkippedEntities(), "")
	}
	return compilerops.AnnotateSkipContext(err, view.SkippedEntities(), "")
}

// TestRenderInMemory_EmptySourceRecipeIsRefusedAtRecipeLoad holds the premise
// the empty-source halt rests on for the render entry that reads no lockfile —
// the one the editor build tool and the long-running service call: the render
// reaches its Recipes only through the Recipe load, so a Recipe whose source entry
// is empty is refused there and never rendered.
//
// The render itself carries no refusal of the entry and reads an empty one as
// declaring no source, so an entry that reached a Recipe without loading it would
// render this workspace successfully. That is the failure this test exists to
// make loud: it fails on a build that succeeds, and on any refusal other than the
// empty-source condition leading the report.
func TestRenderInMemory_EmptySourceRecipeIsRefusedAtRecipeLoad(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeEmptySourceWorkspace(t, root, "  source: \"\"\n")

	err := renderInMemoryReportingRefusals(t, root, releasePath, catalogRoot)
	if err == nil {
		t.Fatal("the in-memory render built a Recipe whose source entry is empty, so it reached that Recipe " +
			"without the Recipe load refusing it")
	}
	t.Logf("emitted diagnostic:\n%v", err)
	if !errors.Is(leadingFailure(err), validator.ErrEmptySource()) {
		t.Fatalf("the in-memory render must lead with the empty-source condition; got: %v", err)
	}
}

// TestPull_EmptySourceRecipeHaltsAtRecipeLoad is the halt's end-to-end proof: a
// Recipe that authors an empty-string source is refused by a real pull over a real
// workspace, and the refusal names the empty-source condition rather than the
// lock-time refusal of a source whose kind cannot be inferred, which a pull would
// meet if the Recipe reached the source descent.
//
// A pull is the command a build depends on — a build reads the lockfile a pull
// writes — so a Recipe refused here never reaches a build, and the condition a
// pull names is the one the author meets on the command line.
//
// The emitted text is logged rather than only matched, so the message a reader
// meets is readable from the test output and can be checked against the reference
// page that documents it.
func TestPull_EmptySourceRecipeHaltsAtRecipeLoad(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeEmptySourceWorkspace(t, root, "  source: \"\"\n")

	err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogRoot},
	})
	if err == nil {
		t.Fatal("a Recipe authoring an empty-string source must halt the pull; it pulled instead")
	}
	t.Logf("emitted diagnostic:\n%v", err)
	if !errors.Is(leadingFailure(err), validator.ErrEmptySource()) {
		t.Fatalf("the halt must lead with the empty-source condition; got: %v", err)
	}
}

// TestBuild_SourcelessRecipeStillBuilds holds the halt to the ambiguity it exists
// for. The mode an empty source is ambiguous WITH is sourceless mode, so a Recipe
// declaring no source at all has to keep building; a halt that widened to it
// would have removed the very mode it exists to disambiguate from.
func TestBuild_SourcelessRecipeStillBuilds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogRoot := writeEmptySourceWorkspace(t, root, "")
	primeLockfile(t, root, releasePath, catalogRoot)

	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("a Recipe declaring no source must still build: %v", err)
	}
	if len(readTree(t, filepath.Join(root, "dist"))) == 0 {
		t.Fatal("the sourceless build emitted no artifacts, so it proves nothing about the halt not widening")
	}
}

// TestBuild_RealSourceRecipeStillBuilds is the halt's other boundary: a Recipe
// naming a real source renders from it, so the refusal reads the value rather
// than the presence of the field.
func TestBuild_RealSourceRecipeStillBuilds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogRoot := writeEmptySourceWorkspace(t, root, "  source: ./chart\n")
	// The chart sits in the Recipe's own directory, which is where content an
	// entity reads has to live.
	writeMinimalChart(t, filepath.Join(catalogRoot, "recipes", "helm", "chart"))
	// A Recipe reading a local chart is pinned by its author before a pull reads it.
	if err := pinRun(context.Background(), io.Discard, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), false); err != nil {
		t.Fatalf("pin the Recipe: %v", err)
	}
	primeLockfile(t, root, releasePath, catalogRoot)

	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("a Recipe naming a real source must still build: %v", err)
	}
	if len(readTree(t, filepath.Join(root, "dist"))) == 0 {
		t.Fatal("the sourced build emitted no artifacts, so it proves nothing about the halt not widening")
	}
}
