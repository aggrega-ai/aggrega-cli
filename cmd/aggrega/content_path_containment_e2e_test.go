// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// What this file proves.
//
// A path an entity declares for content it carries is bounded by the directory
// holding the entity's document, because every file a publication carries is
// taken from there. A path that leaves it — by climbing out, or by naming an absolute
// location — is refused at every moment the entity is read: a whole-catalog
// validate, a single-entity validate, a pull, a build, the pin step, and a
// publication, with and without the pin step. A path that climbs and comes back
// inside is untouched at all of them. A source written as a template has no value
// until the render runs, so the render is where its value is judged.
//
// Every refused case names a file that EXISTS outside the directory, so a refusal
// is attributable to where the path points rather than to a missing file.

// containmentRecipeDir is where the fixture's Recipe sits, relative to the
// workspace root: a per-version directory of its own, five levels below the root.
var containmentRecipeDir = filepath.Join("catalog", "recipe", "helm", "server", "1.0.0")

// containmentWorkspace lays down a buildable workspace whose Recipe imports its
// workload from a file beside it and imports one artifact through artifactImport,
// with that artifact's file present both inside the Recipe's directory and at
// <root>/outside. It returns the Recipe's path.
//
// The configuration names a registry the test owns, one that answers every request
// as not found. A pull over a Recipe the catalog refuses for its content path finds
// no local copy of it and asks the default registry, so without one of the test's
// own that question would go to the built-in default instead.
func containmentWorkspace(t *testing.T, root, artifactImport string) string {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\nauto_pull: false\ndefault_registry: \""+newRegistryReach(t).host()+"\"\n")
	writeFixture(t, filepath.Join(root, "catalog", "component", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeHelmRendererPlugin(t, filepath.Join(root, "catalog", "providers"))
	recipeDir := filepath.Join(root, containmentRecipeDir)
	recipePath := filepath.Join(recipeDir, "recipe.yaml")
	writeContainmentRecipe(t, recipePath, artifactImport)
	writeFixture(t, filepath.Join(recipeDir, "workload.tmpl"), `apiVersion: aggrega.ai/v1alpha1
kind: Workload
replicas: 1
containers:
  server:
    image: test-server:latest
    ports:
      web: {{ .values.port }}
`)
	extra := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: contained-extra\ndata:\n  origin: beside-the-recipe\n"
	writeFixture(t, filepath.Join(recipeDir, "extras", "extra.yaml"), extra)
	writeFixture(t, filepath.Join(root, "outside", "extras", "extra.yaml"), strings.Replace(extra, "beside-the-recipe", "outside", 1))
	if err := os.MkdirAll(filepath.Join(recipeDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
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
        server: helm/server:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: contained-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	return recipePath
}

// writeContainmentRecipe writes the fixture's Recipe with the given artifact
// import, so a case can rewrite that one line and nothing else.
func writeContainmentRecipe(t *testing.T, recipePath, artifactImport string) {
	t.Helper()
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  workload:
    $import: ./workload.tmpl
  artifacts:
    extra.yaml:
      $import: `+artifactImport+`
`)
}

// containmentPull runs a pull that pins, the form a fresh workspace needs.
func containmentPull(t *testing.T, root string) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: filepath.Join(root, "release.yaml"),
		CatalogDirs: []string{filepath.Join(root, "catalog")},
		PinSources:  boolPtr(true),
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	})
}

// containmentBuild runs a build against the lockfile a pull wrote, writing the
// rendered tree under root.
func containmentBuild(t *testing.T, root string) error {
	t.Helper()
	return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		filepath.Join(root, "release.yaml"), []string{filepath.Join(root, "catalog")}, false, io.Discard, io.Discard)
}

// containmentPush runs a publication dry run, holding the lock or running the pin
// step as pinSources says.
func containmentPush(t *testing.T, root, recipePath string, pinSources *bool) error {
	t.Helper()
	return pushRun(context.Background(), pushConfig{
		Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), EntityPath: recipePath,
		Registry: "example.com", PushTarget: memory.New(), DryRun: true, PinSources: pinSources, Out: io.Discard,
	})
}

// assertContentRefused asserts a moment refused under the content-path condition,
// reported under its short code. A pull and a build report a refused entity the
// Release reaches ahead of the resolution failure it caused, so the condition is
// read from what the reader meets first where the failure carries that account.
func assertContentRefused(t *testing.T, moment string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted a content path outside the entity's directory", moment)
	}
	leading := err
	var annotated *compilerops.SkipContextError
	if errors.As(err, &annotated) {
		leading = annotated.Leading()
	}
	if !errors.Is(leading, sourceref.ErrContentOutsideEntityDirectory()) {
		t.Fatalf("%s refused for another reason; want the content-outside-the-entity-directory condition: %v", moment, err)
	}
	if code, _ := cerrors.ShortCodeOf(leading); code != "E0304" {
		t.Errorf("%s: the refusal carries short code %q; want E0304", moment, code)
	}
	if chain := cerrors.CauseChain(err); !strings.Contains(chain, "would not travel with the entity") {
		t.Errorf("%s: the refusal does not tell the author why the path matters:\n%s", moment, chain)
	}
}

// TestAContentPathOutsideTheEntityDirectoryIsRefusedAtEveryMoment drives the
// refusal through every command that reads the entity, for a climb out of the
// directory and for an absolute path. The build is driven against a lockfile a
// clean pull wrote, so it refuses the edited entity as it reads it rather than on
// a missing lockfile.
func TestAContentPathOutsideTheEntityDirectoryIsRefusedAtEveryMoment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		authored func(root string) string
	}{
		{"a climb out of the directory", func(string) string { return "../../../../../outside/extras/extra.yaml" }},
		{"an absolute path", func(root string) string { return filepath.Join(root, "outside", "extras", "extra.yaml") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			recipePath := containmentWorkspace(t, root, "./extras/extra.yaml")
			if err := containmentPull(t, root); err != nil {
				t.Fatalf("the clean workspace must pull: %v", err)
			}
			writeContainmentRecipe(t, recipePath, tc.authored(root))
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
			catalogDir := filepath.Join(root, "catalog")

			for _, m := range []struct {
				name string
				run  func() error
			}{
				{"aggrega validate", func() error { return validateCatalogWideFromWorkspace(ws, []string{catalogDir}) }},
				{"aggrega validate <recipe>", func() error {
					return dispatchValidate(context.Background(), ws, recipePath, "", []string{catalogDir}, false, io.Discard)
				}},
				{"aggrega pull --pin-sources", func() error { return containmentPull(t, root) }},
				{"aggrega build", func() error { return containmentBuild(t, root) }},
				{"aggrega pin --dry-run", func() error { return pinRun(context.Background(), io.Discard, ws, recipePath, true) }},
				{"aggrega push --dry-run", func() error { return containmentPush(t, root, recipePath, nil) }},
				{"aggrega push --dry-run --pin-sources", func() error { return containmentPush(t, root, recipePath, boolPtr(true)) }},
			} {
				assertContentRefused(t, m.name, m.run())
			}
		})
	}
}

// TestAContentPathThatStaysInsideTheEntityDirectoryIsUntouched is the other
// direction over the same moments: a path that climbs into a directory of the
// entity's own and back is an ordinary path inside it, and the build renders the
// file beside the Recipe rather than the copy outside.
func TestAContentPathThatStaysInsideTheEntityDirectoryIsUntouched(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	recipePath := containmentWorkspace(t, root, "./sub/../extras/extra.yaml")
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	catalogDir := filepath.Join(root, "catalog")

	if err := validateCatalogWideFromWorkspace(ws, []string{catalogDir}); err != nil {
		t.Fatalf("aggrega validate: %v", err)
	}
	if err := dispatchValidate(context.Background(), ws, recipePath, "", []string{catalogDir}, false, io.Discard); err != nil {
		t.Fatalf("aggrega validate <recipe>: %v", err)
	}
	if err := containmentPull(t, root); err != nil {
		t.Fatalf("aggrega pull --pin-sources: %v", err)
	}
	if err := containmentBuild(t, root); err != nil {
		t.Fatalf("aggrega build: %v", err)
	}
	if err := containmentPush(t, root, recipePath, nil); err != nil {
		t.Fatalf("aggrega push --dry-run: %v", err)
	}
	rendered := strings.Join(mapValues(readTree(t, filepath.Join(root, "dist"))), "\n")
	if !strings.Contains(rendered, "origin: beside-the-recipe") {
		t.Errorf("the build did not render the imported file beside the Recipe:\n%s", rendered)
	}
	if strings.Contains(rendered, "origin: outside") {
		t.Errorf("the build rendered the copy outside the Recipe's directory:\n%s", rendered)
	}
}

// schemaImportWorkspace lays down a workspace holding one Component and one
// Feature, each in a per-version directory of its own five levels below the root,
// whose schema is imported through the paths given. Each imported file exists both
// beside its entity and at <root>/outside, so a refusal is attributable to where
// the path points rather than to a missing file. It returns the two entities'
// paths.
func schemaImportWorkspace(t *testing.T, root, componentImport, featureImport string) (string, string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
	componentDir := filepath.Join(root, "catalog", "component", "demo", "web", "1.0.0")
	featureDir := filepath.Join(root, "catalog", "feature", "demo", "mon", "1.0.0")
	values := "greeting:\n  type: string\n  default: hello\n"
	whole := "values:\n  level:\n    type: string\n    default: info\n"
	for _, dir := range []string{filepath.Join(componentDir, "schema"), filepath.Join(root, "outside", "schema")} {
		writeFixture(t, filepath.Join(dir, "values.yaml"), values)
	}
	for _, dir := range []string{filepath.Join(featureDir, "schema"), filepath.Join(root, "outside", "schema")} {
		writeFixture(t, filepath.Join(dir, "whole.yaml"), whole)
	}
	for _, dir := range []string{componentDir, featureDir} {
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	componentPath := filepath.Join(componentDir, "component.yaml")
	writeFixture(t, componentPath, `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      $import: `+componentImport+`
`)
	featurePath := filepath.Join(featureDir, "feature.yaml")
	writeFixture(t, featurePath, `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: demo/mon
  version: "1.0.0"
spec:
  schema:
    $import: `+featureImport+`
`)
	return componentPath, featurePath
}

// TestASchemaImportOutsideTheEntityDirectoryIsRefusedAtPublication drives the
// publication of a Component and a Feature whose schema is imported from a file:
// a climb out of the entity's directory and an absolute path are refused under
// the content-path condition, with and without the pin step, and a path that
// climbs and comes back inside publishes. A publication of either kind reads the
// entity the way a build does, so it meets the same refusal a build meets.
func TestASchemaImportOutsideTheEntityDirectoryIsRefusedAtPublication(t *testing.T) {
	for _, tc := range []struct {
		name     string
		authored func(root, file string) string
		refused  bool
	}{
		{"a climb out of the directory", func(_, file string) string { return "../../../../../outside/schema/" + file }, true},
		{"an absolute path", func(root, file string) string { return filepath.Join(root, "outside", "schema", file) }, true},
		{"a climb that comes back inside", func(_, file string) string { return "./sub/../schema/" + file }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			componentPath, featurePath := schemaImportWorkspace(t, root, tc.authored(root, "values.yaml"), tc.authored(root, "whole.yaml"))
			for _, entity := range []string{componentPath, featurePath} {
				for _, pin := range []*bool{nil, boolPtr(true)} {
					moment := fmt.Sprintf("aggrega push --dry-run %s (pin step requested: %t)", filepath.Base(entity), pin != nil)
					err := containmentPush(t, root, entity, pin)
					if !tc.refused {
						if err != nil {
							t.Fatalf("%s: a path inside the entity's directory must publish: %v", moment, err)
						}
						continue
					}
					assertContentRefused(t, moment, err)
				}
			}
		})
	}
}

// TestPublicationRefusesAComponentOrFeatureTheCatalogCannotRead holds a
// publication of a Component or a Feature to the catalog's own reading of it:
// each row carries one fault a catalog load refuses, and the publication refuses
// it under that same condition, so a registry never receives an entity whose
// consumer's first load would refuse it. The readable row is the control.
func TestPublicationRefusesAComponentOrFeatureTheCatalogCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  func(t *testing.T, componentPath, featurePath string) string
		wantIs error
	}{
		{"a readable Component", func(_ *testing.T, componentPath, _ string) string { return componentPath }, nil},
		{"a Component without its required type label", func(t *testing.T, componentPath, _ string) string {
			rewriteFixture(t, componentPath, "  labels:\n    component.aggrega.ai/type: server\n", "")
			return componentPath
		}, envelope.ErrEntityEnvelope()},
		{"a Component whose imported schema file is missing", func(t *testing.T, componentPath, _ string) string {
			if err := os.Remove(filepath.Join(filepath.Dir(componentPath), "schema", "values.yaml")); err != nil {
				t.Fatal(err)
			}
			return componentPath
		}, resolver.ErrYAMLLoad()},
		{"a Feature whose import shares its mapping with a second key", func(t *testing.T, _, featurePath string) string {
			rewriteFixture(t, featurePath, "    $import: ./schema/whole.yaml\n", "    $import: ./schema/whole.yaml\n    extra: 1\n")
			return featurePath
		}, catalog.ErrUnusableSchemaImport()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			componentPath, featurePath := schemaImportWorkspace(t, root, "./schema/values.yaml", "./schema/whole.yaml")
			entity := tc.fault(t, componentPath, featurePath)
			err := containmentPush(t, root, entity, nil)
			if tc.wantIs == nil {
				if err != nil {
					t.Fatalf("aggrega push --dry-run: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("aggrega push --dry-run published an entity the catalog refuses to read")
			}
			if !errors.Is(err, tc.wantIs) {
				t.Fatalf("aggrega push --dry-run refused for another reason; want %v: %v", tc.wantIs, err)
			}
		})
	}
}

// rewriteFixture replaces the one occurrence of old in the file at path, failing
// the test when there is not exactly one, so a fixture edit cannot silently miss.
func rewriteFixture(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), old); n != 1 {
		t.Fatalf("%s: want exactly one %q to rewrite, found %d", path, old, n)
	}
	writeFixture(t, path, strings.Replace(string(data), old, replacement, 1))
}

// TestSingleEntityValidateReadsARecipesFilesFromItsOwnDirectory runs the
// single-entity check from a directory unrelated to the Recipe. Every path the
// Recipe names is relative to its own directory, so where the command runs from
// must not change the verdict.
func TestSingleEntityValidateReadsARecipesFilesFromItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	recipePath := containmentWorkspace(t, root, "./extras/extra.yaml")
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	var out bytes.Buffer
	if err := dispatchValidate(context.Background(), ws, recipePath, "", []string{filepath.Join(root, "catalog")}, false, &out); err != nil {
		t.Fatalf("validating a Recipe from another directory must read its files from its own: %v\n%s", err, out.String())
	}
	if got := out.String(); !strings.Contains(got, "Render: ") || !strings.Contains(got, "origin: beside-the-recipe") {
		t.Errorf("the render did not read the files beside the Recipe:\n%s", got)
	}
}

// templatedSourceRecipe is a Recipe whose source is a template over a local it
// declares, so the path it names exists only once the render has evaluated it.
func templatedSourceRecipe(chartDir string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  locals:
    chart_dir: ` + chartDir + `
  source: "{{ .locals.chart_dir }}"
`
}

// TestATemplatedSourceOutsideTheEntityDirectoryIsRefusedAtRender covers the one
// surface no read of the document can judge: a source written as a template. The
// pull reads the document and cannot know what the template will produce, so it
// is the build's render that refuses, once the value exists — and the same
// Recipe producing a path inside its directory builds.
func TestATemplatedSourceOutsideTheEntityDirectoryIsRefusedAtRender(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		chart   string
		refused bool
	}{
		{"a produced path inside the directory", "./charts/../charts/app", false},
		{"a produced path climbing out", "../../../../../outside/charts/app", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			recipePath := containmentWorkspace(t, root, "./extras/extra.yaml")
			for _, dir := range []string{filepath.Join(filepath.Dir(recipePath), "charts", "app"), filepath.Join(root, "outside", "charts", "app")} {
				writeFixture(t, filepath.Join(dir, "Chart.yaml"), "apiVersion: v2\nname: app\nversion: 0.1.0\n")
				writeFixture(t, filepath.Join(dir, "templates", "cm.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app-cm\ndata:\n  k: v\n")
			}
			writeFixture(t, recipePath, templatedSourceRecipe(tc.chart))

			if err := containmentPull(t, root); err != nil {
				t.Fatalf("a templated source has no value to judge when the document is read, so the pull proceeds: %v", err)
			}
			err := containmentBuild(t, root)
			if !tc.refused {
				if err != nil {
					t.Fatalf("a templated source producing a path inside the directory must build: %v", err)
				}
				return
			}
			assertContentRefused(t, "aggrega build (templated source)", err)
		})
	}
}
