// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the authored-local-path resolution base every import-bearing
// Recipe and FeatureRecipe field shares: a relative path an author writes
// resolves against the directory of the entity file that declared it, never
// against the directory the build happens to run from.
//
// Each test chdirs into an unrelated directory carrying a same-named decoy
// file, so a resolution that falls back to the process working directory is
// observable as a wrong body rather than only as a missing one. Reading the
// resolved path and comparing the sentinel is what separates the two
// candidate bases; asserting absoluteness alone cannot.

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	beside = "BESIDE_THE_ENTITY"
	decoy  = "IN_THE_WORKING_DIRECTORY"
)

// importBaseFixture writes an entity file at <root>/entity/<file> with the
// given body, drops one sibling copy of each name in names carrying the
// beside sentinel, chdirs the test into a separate directory holding a decoy
// copy of the same relative names, and returns the entity path.
func importBaseFixture(t *testing.T, file, body string, names ...string) string {
	t.Helper()
	root := t.TempDir()
	entityDir := filepath.Join(root, "entity")
	runDir := filepath.Join(root, "elsewhere")
	for _, dir := range []string{entityDir, runDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			target := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			content := beside
			if dir == runDir {
				content = decoy
			}
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	entityPath := filepath.Join(entityDir, file)
	if err := os.WriteFile(entityPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// The decoy sits in the process working directory, so a resolution that
	// keeps the authored relative path reads the decoy instead of failing.
	t.Chdir(runDir)
	return entityPath
}

// assertResolvesBesideEntity reads the resolved path and fails unless it is
// absolute and carries the sibling copy's sentinel.
func assertResolvesBesideEntity(t *testing.T, field, resolved string) {
	t.Helper()
	if !filepath.IsAbs(resolved) {
		t.Errorf("%s: path not resolved to absolute: %q", field, resolved)
		return
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		t.Errorf("%s: resolved path unreadable: %v", field, err)
		return
	}
	if got := strings.TrimSpace(string(body)); got != beside {
		t.Errorf("%s: resolved against the wrong base: read %q from %q, want %q",
			field, got, resolved, beside)
	}
}

// TestRecipeImportPathsResolveAgainstRecipeDirectory pins the resolution base
// for every import-bearing field a Recipe carries. valuesMapping and workload
// are the fields this test was added for; artifacts and source are included so
// a later change cannot narrow the shared base to a subset of them.
func TestRecipeImportPathsResolveAgainstRecipeDirectory(t *testing.T) {
	path := importBaseFixture(t, "recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: import-base
  version: 1.0.0
spec:
  capability: helm
  implements: core/compute/server:1.0.0
  source: ./charts/app
  valuesMapping:
    $import: ./mappings/values.tmpl
  workload:
    $import: ./workload/body.yaml.tmpl
  artifacts:
    extra.yaml:
      $import: ./artifacts/extra.tmpl
`, "mappings/values.tmpl", "workload/body.yaml.tmpl", "artifacts/extra.tmpl", "charts/app/Chart.yaml")

	r, err := parseRecipeEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Recipe: %v", err)
	}

	assertResolvesBesideEntity(t, "valuesMapping", r.Spec.ValuesMapping.Import)
	assertResolvesBesideEntity(t, "workload", r.Spec.Workload.Import)
	entry, ok := r.Spec.Artifacts.Get("extra.yaml")
	if !ok {
		t.Fatal("artifacts entry not loaded")
	}
	assertResolvesBesideEntity(t, "artifacts", entry.Import)

	src, ok := r.Spec.Source.(string)
	if !ok {
		t.Fatalf("source not a string: %T", r.Spec.Source)
	}
	if !filepath.IsAbs(src) || !strings.HasSuffix(filepath.ToSlash(src), "/entity/charts/app") {
		t.Errorf("source not resolved against the Recipe directory: %q", src)
	}
}

// TestFeatureRecipeImportPathsResolveAgainstRecipeDirectory is the
// FeatureRecipe half. Both kinds route through the same resolution, so a
// change that reaches one and not the other is what this pins.
func TestFeatureRecipeImportPathsResolveAgainstRecipeDirectory(t *testing.T) {
	path := importBaseFixture(t, "featurerecipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: import-base-fr
  version: 1.0.0
spec:
  capability: helm
  implements: example/sidecar:1.0.0
  source: ./charts/sidecar
  valuesMapping:
    $import: ./mappings/values.tmpl
  workload:
    $import: ./workload/body.yaml.tmpl
  artifacts:
    extra.yaml:
      $import: ./artifacts/extra.tmpl
`, "mappings/values.tmpl", "workload/body.yaml.tmpl", "artifacts/extra.tmpl", "charts/sidecar/Chart.yaml")

	fr, err := parseFeatureRecipeEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse FeatureRecipe: %v", err)
	}

	assertResolvesBesideEntity(t, "valuesMapping", fr.Spec.ValuesMapping.Import)
	assertResolvesBesideEntity(t, "workload", fr.Spec.Workload.Import)
	entry, ok := fr.Spec.Artifacts.Get("extra.yaml")
	if !ok {
		t.Fatal("artifacts entry not loaded")
	}
	assertResolvesBesideEntity(t, "artifacts", entry.Import)
}

// TestImportPathsThatAreNotLocalPathsSurviveResolution pins the three values
// the resolution must hand back untouched. A bare-name library reference and
// a registry-host reference are identities rather than paths, and the
// consumption site reads their exact prefix to resolve or to reject them — so
// rebasing either would defeat the read. An absolute path is already anchored.
func TestImportPathsThatAreNotLocalPathsSurviveResolution(t *testing.T) {
	absTarget := filepath.Join(t.TempDir(), "absolute.tmpl")
	if err := os.WriteFile(absTarget, []byte(beside), 0o644); err != nil {
		t.Fatal(err)
	}

	path := importBaseFixture(t, "recipe.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: import-untouched
  version: 1.0.0
spec:
  capability: helm
  implements: core/compute/server:1.0.0
  source: /already/absolute/chart
  valuesMapping:
    $import: oci://hub.example.com/mappinglibrary/example/helpers:1.0.0
  workload:
    $import: BaseWorkloads#/workloads/StatelessService
  artifacts:
    extra.yaml:
      $import: `+absTarget+`
`)

	r, err := parseRecipeEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Recipe: %v", err)
	}

	// A registry host must still be readable at the consumption site, which
	// is what lets the build reject it by name instead of reporting a
	// missing file.
	if got := r.Spec.ValuesMapping.Import; got != "oci://hub.example.com/mappinglibrary/example/helpers:1.0.0" {
		t.Errorf("registry-host reference was rewritten: %q", got)
	}
	if !r.Spec.ValuesMapping.HasOCIPrefix() {
		t.Error("registry-host reference no longer reports its host after load")
	}
	if got := r.Spec.Workload.Import; got != "BaseWorkloads#/workloads/StatelessService" {
		t.Errorf("library reference was rewritten: %q", got)
	}
	if !r.Spec.Workload.IsLibraryImport() {
		t.Error("library reference no longer reports as a library import after load")
	}
	entry, ok := r.Spec.Artifacts.Get("extra.yaml")
	if !ok {
		t.Fatal("artifacts entry not loaded")
	}
	if entry.Import != absTarget {
		t.Errorf("absolute path was rewritten: got %q want %q", entry.Import, absTarget)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
