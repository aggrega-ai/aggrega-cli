// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the authored-local-path resolution base every import-bearing
// Recipe and FeatureRecipe field shares: a relative path an author writes
// resolves against the directory of the entity file that declared it, never
// against the directory the build happens to run from.
//
// Every test that reads a resolved path chdirs into an unrelated directory
// carrying a same-named decoy file, so a resolution that falls back to the
// process working directory is observable as a wrong body rather than only as a
// missing one. Reading the resolved path and comparing the sentinel is what
// separates the two candidate bases; asserting absoluteness alone cannot.
//
// The file also covers the two edges of that base: the values the resolution
// must hand back rather than rebase, and the gate in front of it that refuses a
// registry host in a reference an author wrote.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

	r, _, err := parseRecipeEntity(mustRead(t, path), path)
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

	fr, _, err := parseFeatureRecipeEntity(mustRead(t, path), path)
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

// TestImportPathsThatAreNotLocalPathsSurviveResolution pins the values the
// resolution must hand back untouched. A bare-name library reference is an
// identity rather than a path, and the consumption site reads its exact text to
// find the library and the entry — so rebasing one would turn a resolvable
// identity into a path under the entity's directory. Both fragment surfaces an
// author can reference by identity are covered, so a change reaching one and
// not the other is visible. An absolute path is already anchored.
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
    $import: BaseMappings#/mappings/SimpleValues
  workload:
    $import: BaseWorkloads#/workloads/StatelessService
  artifacts:
    extra.yaml:
      $import: `+absTarget+`
`)

	r, _, err := parseRecipeEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Recipe: %v", err)
	}

	if got := r.Spec.ValuesMapping.Import; got != "BaseMappings#/mappings/SimpleValues" {
		t.Errorf("mapping library reference was rewritten: %q", got)
	}
	if !r.Spec.ValuesMapping.IsLibraryImport() {
		t.Error("mapping library reference no longer reports as a library import after load")
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

// TestAuthoredRegistryHostImportHaltsAtLoad pins the gate that sits in front of
// that resolution on the mapping surface. A reference an author writes names a
// library by identity, so a registry host in one is refused while the document
// is being read, naming the field that carried it — rather than travelling on
// to be reported a layer away from the text that produced it.
//
// The same document with a host-less identity is loaded first, so the refusal
// below is attributable to the host alone rather than to anything else the
// fixture happens to say.
func TestAuthoredRegistryHostImportHaltsAtLoad(t *testing.T) {
	const body = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: import-host-refused
  version: 1.0.0
spec:
  capability: helm
  implements: core/compute/server:1.0.0
  source: /already/absolute/chart
  valuesMapping:
    $import: %s
`

	admitted := importBaseFixture(t, "recipe.yaml", fmt.Sprintf(body, "BaseMappings#/mappings/SimpleValues"))
	if _, _, err := parseRecipeEntity(mustRead(t, admitted), admitted); err != nil {
		t.Fatalf("the host-less identity form should load: %v", err)
	}

	refused := importBaseFixture(t, "recipe.yaml", fmt.Sprintf(body, "oci://hub.example.com/mappinglibrary/example/helpers:1.0.0"))
	_, _, err := parseRecipeEntity(mustRead(t, refused), refused)
	if err == nil {
		t.Fatal("a registry host on the valuesMapping $import should be refused as the document is read")
	}
	if !strings.Contains(err.Error(), "spec.valuesMapping") {
		t.Errorf("the refusal does not name the field that carried the host: %v", err)
	}
}

// TestRegistryHostImportSurvivesResolutionDirectly proves the resolution's
// host-bearing arm against its specification rather than through a caller. No
// document can reach it: every `$import:` position on both kinds that carry one
// refuses a registry host as the document is read, so the value never arrives
// at a rebase. The arm stays because it is what keeps the resolution total over
// the values its own types can hold — a host-bearing reference joined to an
// entity's directory would become a path, and the refusal that names it would
// become a missing-file report.
//
// A reference read out of a library entry's body carries no such gate, because
// that body is declared as a plain string; it is refused where it is consumed,
// which is a different resolution in a different package.
func TestRegistryHostImportSurvivesResolutionDirectly(t *testing.T) {
	const ref = "oci://hub.example.com/mappinglibrary/example/helpers:1.0.0"

	got, err := resolveImportPath(filepath.Join("entity", "recipe.yaml"), ref, types.ValuesMapping{Import: ref})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != ref {
		t.Errorf("registry-host reference was rewritten: got %q want %q", got, ref)
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
