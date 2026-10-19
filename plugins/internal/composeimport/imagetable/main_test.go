// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/composeimport"
)

// hubRecipes are the Compose Recipes of the aggrega-hub repository checked out
// beside this one. The check below reads them, and without that checkout it fails
// rather than skipping, so check out aggrega-hub beside this repository first.
var hubRecipes = filepath.Join("..", "..", "..", "..", "..", "aggrega-hub", "aggrega-catalog", "recipe", "compose")

// committedTable is the generated table the importer embeds.
const committedTable = "../imagetable_gen.go"

// TestTheImageTableIsWhatTheHubsComposeRecipesName regenerates the image table
// from the hub's Compose Recipes, naming the revision the committed table names,
// and requires the committed table to be exactly what the regeneration writes —
// so a change to a Recipe's image or implements, a Recipe added or one removed
// fails here until the table is regenerated with
// `go generate ./plugins/internal/composeimport`. It also requires the rows the
// importer's PostgreSQL and Redis cases rest on.
//
// 🔴 PLANTED DEFECT: edit one row of imagetable_gen.go by hand — say redis to
// core/datastore/valkey — and the case fails naming the regeneration.
func TestTheImageTableIsWhatTheHubsComposeRecipesName(t *testing.T) {
	t.Parallel()
	rows, _, err := generate(hubRecipes)
	if err != nil {
		t.Fatalf("generate the table from %s: %v", hubRecipes, err)
	}
	want := render(rows, composeimport.TableRevision)
	got, err := os.ReadFile(committedTable)
	if err != nil {
		t.Fatalf("read the committed table: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the committed image table is not what the hub's Compose Recipes generate; regenerate it with "+
			"`go generate ./plugins/internal/composeimport` from the aggrega-cli module root\n--- committed ---\n%s\n"+
			"--- generated ---\n%s", got, want)
	}
	for _, r := range []row{{"postgres", "core/datastore/postgres"}, {"redis", "core/datastore/redis"}} {
		found := false
		for _, g := range rows {
			found = found || g == r
		}
		if !found {
			t.Errorf("the table holds no row %s -> %s", r.repository, r.component)
		}
	}
}

// writeRecipes lays each Recipe body down at dir/<name>/1.0.0/recipe.yaml, as a
// hub keeps its Compose Recipes, and returns dir.
func writeRecipes(t *testing.T, recipes map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range recipes {
		path := filepath.Join(dir, name, "1.0.0", "recipe.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// recipeDoc writes a Compose Recipe implementing component whose workload is
// workload, indented as a Recipe's literal block holds it, with locals.
func recipeDoc(component, locals, workload string) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: r\n  version: 1.0.0\nspec:\n")
	b.WriteString("  capability: compose\n  implements: " + component + ":1.0.0\n")
	if locals != "" {
		b.WriteString("  locals:\n" + locals)
	}
	b.WriteString("  workload: |\n")
	for _, line := range strings.Split(strings.TrimRight(workload, "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}

// TestTheMainContainersImageIsReadLiterally reads one Recipe of each shape and
// requires the rows its main container's image yields: a literal reference; a
// repository whose tag the template writes; a repository each branch of the
// template writes; a literal locals entry the image is whole; and none for the
// whole image the Release names, an image composed from locals, an image another
// action's output precedes, and the images of init containers, of a container
// beside main and of artifacts.
func TestTheMainContainersImageIsReadLiterally(t *testing.T) {
	t.Parallel()
	containers := func(image string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ninitContainers:\n  - name: pull\n" +
			"    image: \"python:3.12-slim\"\ncontainers:\n  main:\n    image: " + image + "\n  sidecar:\n" +
			"    image: \"nginx:{{ .locals.tag }}\"\n"
	}
	for _, row := range []struct {
		name, locals, image string
		want                []string
	}{
		{"a literal reference", "", `"adminer:4"`, []string{"adminer"}},
		{"a templated tag", "", `"ghcr.io/ferretdb/ferretdb:{{ .infra.engine_version }}"`, []string{"ghcr.io/ferretdb/ferretdb"}},
		{"each branch", "", `"{{ if .infra.overrides }}{{ .infra.overrides.image }}{{ else }}redis:{{ .infra.engine_version }}{{ end }}"`,
			[]string{"redis"}},
		{"a literal locals entry", "    image: \"docker.io/grafana/loki:3.6.7\"\n", `"{{ .locals.image }}"`, []string{"grafana/loki"}},
		{"the Release's whole image", "", `"{{ .infra.image }}"`, nil},
		{"locals in both branches", "", `"{{ if eq .infra.r \"p\" }}{{ .locals.a }}{{ else }}{{ .locals.b }}{{ end }}"`, nil},
		{"a templated registry", "", `"{{ .locals.registry }}/postgres:{{ .infra.v }}"`, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			dir := writeRecipes(t, map[string]string{"x": recipeDoc("core/x/x", row.locals, containers(row.image))})
			rows, notes, err := generate(dir)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			var got []string
			for _, r := range rows {
				got = append(got, r.repository)
				if r.component != "core/x/x" {
					t.Errorf("the row maps %s to %s, want core/x/x", r.repository, r.component)
				}
			}
			if !reflect.DeepEqual(got, row.want) {
				t.Errorf("the image %s yields rows %v, want %v (%v)", row.image, got, row.want, notes)
			}
		})
	}
}

// TestTheTableMovesWithARecipesImage generates the table from a copy of the hub's
// PostgreSQL and Redis Recipes in which the PostgreSQL Recipe's literal
// repository is changed: the row moves with it, so the table is read from the
// Recipes rather than written by hand; and two runs write the same bytes.
func TestTheTableMovesWithARecipesImage(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(hubRecipes, name, "1.0.0", "recipe.yaml"))
		if err != nil {
			t.Fatalf("read the hub's %s Recipe: %v", name, err)
		}
		return string(body)
	}
	postgres := read("postgres")
	const literal = `image: "postgres:{{ .infra.engine_version }}"`
	if strings.Count(postgres, literal) != 1 {
		t.Fatalf("the hub's PostgreSQL Recipe no longer writes %s once, so the plant would change nothing", literal)
	}
	planted := strings.Replace(postgres, literal, `image: "planted/postgres-needle:{{ .infra.engine_version }}"`, 1)
	dir := writeRecipes(t, map[string]string{"postgres": planted, "redis": read("redis")})
	rows, _, err := generate(dir)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := []row{{"planted/postgres-needle", "core/datastore/postgres"}, {"redis", "core/datastore/redis"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("the planted table holds %v, want %v", rows, want)
	}
	again, _, _ := generate(dir)
	if !bytes.Equal(render(rows, "r"), render(again, "r")) {
		t.Errorf("two generations from one set of Recipes differ")
	}
}

// TestTwoRecipesNamingOneRepositoryForDifferentComponentsAreRefused generates the
// table from two Recipes naming one repository for two Components, and requires
// a refusal naming the repository and both Recipes, with no rows; and from two
// naming one repository for one Component, one row.
func TestTwoRecipesNamingOneRepositoryForDifferentComponentsAreRefused(t *testing.T) {
	t.Parallel()
	workload := "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: \"acme/db:{{ .infra.v }}\"\n"
	dir := writeRecipes(t, map[string]string{"a": recipeDoc("core/x/a", "", workload), "b": recipeDoc("core/x/b", "", workload)})
	rows, _, err := generate(dir)
	if err == nil || rows != nil {
		t.Fatalf("two Recipes naming acme/db for two Components generated %v, %v; want a refusal", rows, err)
	}
	for _, want := range []string{"acme/db", "a/1.0.0/recipe.yaml implements core/x/a", "b/1.0.0/recipe.yaml implements core/x/b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	dir = writeRecipes(t, map[string]string{"a": recipeDoc("core/x/a", "", workload), "b": recipeDoc("core/x/a", "", workload)})
	rows, _, err = generate(dir)
	if err != nil || !reflect.DeepEqual(rows, []row{{"acme/db", "core/x/a"}}) {
		t.Errorf("two Recipes naming acme/db for one Component generated %v, %v; want one row", rows, err)
	}
}
