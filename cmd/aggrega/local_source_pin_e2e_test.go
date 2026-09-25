// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// localSourcePinWorkspace authors a workspace whose own catalog directory holds one
// never-published Recipe sourcing a LOCAL Helm chart, with a note beside the Recipe
// that nothing declares. It returns the Release, the catalog directory and the
// Recipe document.
func localSourcePinWorkspace(t *testing.T, root string) (string, string, string) {
	t.Helper()
	catalogDir := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: server
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1alpha1
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
// local chart that nobody pinned pulls and builds, because the refusal of an
// unpinned local source is switched off in the lock package; the pin records one
// row per file its source contributes and none for the note beside it; the build
// is intact; a file edited inside the source halts the build and a verifying pull
// under the one condition, naming the file and both digests; and a file edited
// beside the source, outside what the entity declares, halts nothing.
//
// The first step is the one that moves when that switch is turned on: the pull
// over the never-pinned Recipe then halts under the unpinned-entity condition,
// naming each file of the source, and this test changes with it.
func TestALocalSourceIsPinnedAndABuildRefusesItsDrift(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
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
		return dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			releasePath, []string{catalogDir}, false, io.Discard, io.Discard)
	}

	// Nobody has pinned the Recipe, and a lock that records no pin for a local
	// source is not refused: the pull and the build both pass, and the pull writes
	// nothing beside the entity.
	if err := pull(false); err != nil {
		t.Fatalf("a pull over a local source nobody pinned must not be refused; got %v", err)
	}
	if err := build(); err != nil {
		t.Fatalf("a build over a local source nobody pinned must not be refused; got %v", err)
	}
	if _, err := os.Stat(recipepin.LockPath(recipePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a pull that does not pin must write no lock beside the entity; stat: %v", err)
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
	recordedTemplate, _ := recipepin.MatchLocalSourcePin(lock.LocalSourceDigests, "chart/templates/cm.yaml")

	if err := pull(false); err != nil {
		t.Fatalf("a pull over the pinned Recipe must pass: %v", err)
	}
	if err := build(); err != nil {
		t.Fatalf("the build over the pinned Recipe must be intact: %v", err)
	}

	// A file inside the declared source, edited with no re-pin.
	template := filepath.Join(filepath.Dir(recipePath), "chart", "templates", "cm.yaml")
	original := mustReadFile(t, template)
	edited := append(append([]byte(nil), original...), []byte("  edited: after-the-pin\n")...)
	if err := os.WriteFile(template, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	wantNamed := []string{"chart/templates/cm.yaml", digest.ContentDigest(edited), recordedTemplate.Digest}
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"the build", build},
		{"a verifying pull", func() error { return pull(true) }},
	} {
		err := step.run()
		if !errors.Is(err, recipepin.ErrLocalSourceContentMismatch()) {
			t.Fatalf("%s over an edited source must halt under the local-source mismatch condition; got %v", step.name, err)
		}
		// The build reports the condition as the cause of a per-node source
		// resolution frame, and an error's own text renders one frame only; the
		// file and the digests are in the frame that carries the condition, so
		// they are read from the whole chain.
		chain := cerrors.CauseChain(err)
		for _, want := range wantNamed {
			if !strings.Contains(chain, want) {
				t.Errorf("%s's halt does not name %q:\n%s", step.name, want, chain)
			}
		}
	}
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
