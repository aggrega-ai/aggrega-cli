// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// localSourcePinWorkspace authors a workspace whose own catalog directory holds one
// never-published Recipe sourcing a LOCAL Helm chart, with a note beside the Recipe
// that nothing declares. It returns the Release, the catalog directory and the
// Recipe document.
func localSourcePinWorkspace(t *testing.T, root string) (string, string, string) {
	t.Helper()
	catalogDir := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: server
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: local-source-pin
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  components:
    app: {}
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
`)
	writeFixture(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogDir, "providers"))
	recipeDir := filepath.Join(catalogDir, "recipes", "server")
	writeFixture(t, filepath.Join(recipeDir, "chart", "Chart.yaml"), "apiVersion: v2\nname: server\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(recipeDir, "chart", "templates", "cm.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: pinned-config
data:
  key: value
`)
	writeFixture(t, filepath.Join(recipeDir, "NOTES.md"), "a note beside the Recipe that nothing declares\n")
	recipePath := filepath.Join(recipeDir, "recipe.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source: ./chart
`)
	return filepath.Join(root, "release.yaml"), catalogDir, recipePath
}

// TestALocalSourceIsPinnedAndABuildRefusesItsDrift drives a local source's pins end
// to end, through the command paths a user runs: a never-published Recipe with a
// local chart that nobody pinned is refused by a pull and by a build that
// resolves its own dependencies, under the unpinned-entity condition, naming each
// file of the source and not the note beside it, and neither writes a lock; the
// pin records one row per file its source contributes and none for the note; the
// build is intact; a file edited inside the source moves the Recipe's lockfile row,
// so the build and a verifying pull halt on that row, naming the Recipe and both
// row digests; and a file edited beside the source, outside what the entity
// declares, halts nothing.
func TestALocalSourceIsPinnedAndABuildRefusesItsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir, recipePath := localSourcePinWorkspace(t, root)
	pull := func(verify bool) error {
		return pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath,
			CatalogDirs: []string{catalogDir},
			Verify:      verify,
			DiagOut:     io.Discard,
			Out:         io.Discard,
			diag:        buildDiagOptions{quiet: true},
		})
	}
	build := func() error {
		return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			releasePath, []string{catalogDir}, false, io.Discard, io.Discard)
	}

	// Nobody has pinned the Recipe, so a pull that does not pin refuses it, and so
	// does a build asked to resolve its own dependencies, which runs the same hold.
	// Each names every file of the source and not the note beside it, and neither
	// writes anything beside the entity.
	buildFillingItsLockfile := func() error {
		return dispatchBuildWithDiag(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			releasePath, []string{catalogDir}, false, buildDiagOptions{quiet: true}, rootedBuildOptions(t, root, buildops.RunOptions{AutoPull: true}),
			io.Discard, io.Discard)
	}
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"a pull", func() error { return pull(false) }},
		{"a build resolving its own dependencies", buildFillingItsLockfile},
	} {
		err := step.run()
		if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
			t.Fatalf("%s over a local source nobody pinned must halt under the unpinned-entity condition; got %v", step.name, err)
		}
		chain := cerrors.CauseChain(err)
		for _, want := range []string{
			`Recipe "server"`,
			"local source file chart/Chart.yaml: no pin recorded",
			"local source file chart/templates/cm.yaml: no pin recorded",
		} {
			if !strings.Contains(chain, want) {
				t.Errorf("%s's halt does not name %q:\n%s", step.name, want, chain)
			}
		}
		if strings.Contains(chain, "NOTES.md") {
			t.Errorf("%s's halt names the note beside the Recipe, which nothing declares:\n%s", step.name, chain)
		}
		if _, err := os.Stat(recipepin.LockPath(recipePath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s that does not pin must write no lock beside the entity; stat: %v", step.name, err)
		}
	}

	var out bytes.Buffer
	if err := pinRun(context.Background(), &out, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), recipePath, false); err != nil {
		t.Fatalf("aggrega pin: %v", err)
	}
	lock := readLockForTest(t, recipePath)
	var pinned []string
	for _, p := range lock.LocalSourceDigests {
		pinned = append(pinned, p.Path)
	}
	if strings.Join(pinned, ",") != "chart/Chart.yaml,chart/templates/cm.yaml" {
		t.Fatalf("the pin must record the source's two files and nothing else; it recorded %v", pinned)
	}
	if err := pull(false); err != nil {
		t.Fatalf("a pull over the pinned Recipe must pass: %v", err)
	}
	if err := build(); err != nil {
		t.Fatalf("the build over the pinned Recipe must be intact: %v", err)
	}
	recorded := entityRow(t, releasePath, "server")

	// A file inside the declared source, edited with no re-pin. The Recipe's own
	// lockfile row covers every file its source contributes, so the edit moves that
	// row, and the build and a verifying pull halt on it — naming the Recipe and both
	// row digests — before any single file is compared, as for a Recipe of any name.
	template := filepath.Join(filepath.Dir(recipePath), "chart", "templates", "cm.yaml")
	original := mustReadFile(t, template)
	edited := append(append([]byte(nil), original...), []byte("  edited: after-the-pin\n")...)
	if err := os.WriteFile(template, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	assertRowRefusal(t, pull(true), build(), "server", recorded, rowDigestAt(t, recipePath),
		recipepin.ErrLocalSourceContentMismatch())
	if err := os.WriteFile(template, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build(); err != nil {
		t.Fatalf("the build must be intact again once the file is restored: %v", err)
	}

	// A file beside the source, which nothing the Recipe declares names.
	note := filepath.Join(filepath.Dir(recipePath), "NOTES.md")
	if err := os.WriteFile(note, []byte("an edit to a file nothing declares\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build(); err != nil {
		t.Fatalf("an edit outside the declared source must not halt the build: %v", err)
	}
}

// sourcelessServerRecipe is the Recipe localSourcePinWorkspace writes with its
// local source taken out: the workload is synthesised, so the Recipe renders and
// declares no local source for the lock's local source pins to pin.
const sourcelessServerRecipe = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      server:
        image: test-server:latest
`

// TestALockOutlivingItsLocalSourceIsHeldOnlyWhileTheSourceIsDeclared drives a
// lock's local source pins through the three readers that hold an entity to them
// — a pull that does not pin, a verifying pull and a build — over the two ways a
// Recipe can stop matching what its lock records for a local source.
//
// A declared source whose files were all removed is a change to the source, so
// all three refuse it: a pull that does not pin naming every file that is gone,
// and a verifying pull and a build naming the Recipe's lockfile row those files
// moved. A Recipe that no
// longer declares a local source reads nothing those pins pin, so all three admit
// it, exactly as a pin recorded for an imported file the Recipe no longer declares
// is admitted; the pin step then drops them. The discriminator is what the Recipe
// declares, never whether any file is left, which is why the two halves are one
// test.
func TestALockOutlivingItsLocalSourceIsHeldOnlyWhileTheSourceIsDeclared(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir, recipePath := localSourcePinWorkspace(t, root)
	pull := func(verify bool) error {
		return pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath,
			CatalogDirs: []string{catalogDir},
			Verify:      verify,
			DiagOut:     io.Discard,
			Out:         io.Discard,
			diag:        buildDiagOptions{quiet: true},
		})
	}
	build := func() error {
		return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			releasePath, []string{catalogDir}, false, io.Discard, io.Discard)
	}
	pin := func() error {
		return pinRun(context.Background(), io.Discard, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), recipePath, false)
	}

	if err := pin(); err != nil {
		t.Fatalf("aggrega pin: %v", err)
	}
	if err := pull(false); err != nil {
		t.Fatalf("a pull over the pinned Recipe must pass, or nothing below is about the lock: %v", err)
	}
	lockPath := recipepin.LockPath(recipePath)
	pinned := mustReadFile(t, lockPath)

	// The declared source's files, every one removed; its directories stay.
	recipeDir := filepath.Dir(recipePath)
	sourceFiles := []string{"chart/Chart.yaml", "chart/templates/cm.yaml"}
	saved := map[string][]byte{}
	for _, f := range sourceFiles {
		p := filepath.Join(recipeDir, filepath.FromSlash(f))
		saved[f] = mustReadFile(t, p)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, reader := range []struct {
		name      string
		run       func() error
		condition error
		names     func(file string) string
	}{
		{"a pull that does not pin", func() error { return pull(false) }, recipepin.ErrEntityNotPinned(),
			func(f string) string { return "local source file " + f + ": the recorded pin no longer matches it" }},
		// The verifying pull and the build hold the Release to its lockfile first, and
		// the Recipe's own row covers every file its source contributes, so the
		// removal moves that row: both halt on the row, before any single file is
		// compared — as they do for a Recipe of any name.
		{"a verifying pull", func() error { return pull(true) }, lockfile.ErrLockfileDrift(),
			func(string) string { return `entity "server" (recipe) changed` }},
		{"a build", build, lockfile.ErrLockfileDrift(),
			func(string) string { return `entity "server" (recipe) changed` }},
	} {
		err := reader.run()
		if !errors.Is(err, reader.condition) {
			t.Fatalf("%s over a declared source whose files were all removed must refuse it; got %v", reader.name, err)
		}
		chain := cerrors.CauseChain(err)
		for _, f := range sourceFiles {
			if want := reader.names(f); !strings.Contains(chain, want) {
				t.Errorf("%s's refusal does not name %q:\n%s", reader.name, want, chain)
			}
		}
	}
	for f, body := range saved {
		if err := os.WriteFile(filepath.Join(recipeDir, filepath.FromSlash(f)), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := build(); err != nil {
		t.Fatalf("the build must be intact again once the source's files are restored: %v", err)
	}

	// The Recipe stops declaring a local source; its lock still records the chart's
	// files, and the chart still sits beside it.
	writeFixture(t, recipePath, sourcelessServerRecipe)
	for _, reader := range []struct {
		name string
		run  func() error
	}{
		// A pull that does not pin goes first: the Recipe document moved, and it is
		// what records the document's new digest in the lockfile the other two read.
		{"a pull that does not pin", func() error { return pull(false) }},
		{"a verifying pull", func() error { return pull(true) }},
		{"a build", build},
	} {
		if err := reader.run(); err != nil {
			t.Fatalf("%s over a Recipe that declares no local source must not be held to the lock's local source pins; got %v",
				reader.name, err)
		}
	}
	if got := mustReadFile(t, lockPath); !bytes.Equal(got, pinned) {
		t.Fatalf("no reader may rewrite the lock; it moved from\n%s\nto\n%s", pinned, got)
	}
	if err := pin(); err != nil {
		t.Fatalf("aggrega pin over the sourceless Recipe: %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the pin step must drop the pins of a source the Recipe no longer declares, leaving no lock; stat: %v", err)
	}
}
