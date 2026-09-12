// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// writeEmptySourceWorkspace lays down the minimal buildable workspace of
// writeValidWorkspace and then rewrites its Recipe so the Recipe's source field
// carries the literal text in sourceLine — the whole point being that the three
// cases the empty-source halt has to separate differ in exactly that one line and
// in nothing else.
//
// The Recipe keeps its workload alongside whatever source the case declares,
// because a Recipe carrying an empty source and NOTHING else is refused by the
// input-kinds rule when the document is read and so never reaches rendering at
// all. Pairing the empty source with a workload is what makes the ambiguity
// reachable: the document is well-shaped, one input kind is declared, and the
// only open question is which of the two modes the empty source selected.
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
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
}

// renderInMemory drives the render entry that renders a Release to in-memory
// buffers with no lockfile freshness gate — the one the editor build tool and the
// daemon both call. That is what lets this test hand an empty-string source
// straight to rendering: a command-line build reads a lockfile and refuses one
// that no longer matches the catalog, and filling that lockfile means a pull,
// whose lock-time source descent refuses an empty source URL for its own
// unrelated reason (it cannot infer a source kind from one). Both are constraints
// on the build command rather than on the halt itself, which the validate command
// also reaches: over a Recipe file it renders that Recipe in isolation and refuses
// the same ambiguity.
func renderInMemory(t *testing.T, root, releasePath, catalogRoot string) error {
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
		t.Fatalf("resolve closure: %v", cerr)
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
	return err
}

// TestBuild_EmptySourceRecipeHalts is the halt's end-to-end proof: a Recipe that
// authors an empty-string source is refused by a real build over a real
// workspace, and the refusal names the condition rather than surfacing as a
// filesystem error from an attempt to read the empty path.
//
// The emitted text is logged rather than only matched, so the message a reader
// meets is readable from the test output and can be checked against the reference
// page that documents it.
func TestBuild_EmptySourceRecipeHalts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeEmptySourceWorkspace(t, root, "  source: \"\"\n")

	err := renderInMemory(t, root, releasePath, catalogRoot)
	if err == nil {
		t.Fatal("a Recipe authoring an empty-string source must halt the build; it built instead")
	}
	t.Logf("emitted diagnostic:\n%v", err)
	if !errors.Is(err, renderer.ErrEmptySource()) {
		t.Fatalf("the halt must carry the empty-source condition; got: %v", err)
	}
}

// TestBuild_SourcelessRecipeStillBuilds holds the halt to the ambiguity it exists
// for. The mode an empty source is ambiguous WITH is sourceless mode, so a Recipe
// declaring no source at all has to keep building; a halt that widened to it
// would have removed the very mode it exists to disambiguate from.
func TestBuild_SourcelessRecipeStillBuilds(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogRoot := writeEmptySourceWorkspace(t, root, "")
	primeLockfile(t, root, releasePath, catalogRoot)

	if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout); err != nil {
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
	root := t.TempDir()
	t.Chdir(root)
	chartDir := filepath.Join(root, "chart")
	writeMinimalChart(t, chartDir)
	releasePath, catalogRoot := writeEmptySourceWorkspace(t, root, "  source: "+chartDir+"\n")
	primeLockfile(t, root, releasePath, catalogRoot)

	if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogRoot}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("a Recipe naming a real source must still build: %v", err)
	}
	if len(readTree(t, filepath.Join(root, "dist"))) == 0 {
		t.Fatal("the sourced build emitted no artifacts, so it proves nothing about the halt not widening")
	}
}
