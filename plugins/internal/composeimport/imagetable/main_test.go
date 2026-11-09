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

// hub is the aggrega-hub repository checked out beside this one, and hubRecipes
// its Compose Recipes. The checks below read them, and without that checkout they
// fail rather than skipping, so check out aggrega-hub beside this repository
// first.
var (
	hub        = filepath.Join("..", "..", "..", "..", "..", "aggrega-hub")
	hubRecipes = filepath.Join(hub, "aggrega-catalog", "recipe", "compose")
)

// committedTable is the generated table the importer embeds.
const committedTable = "../imagetable_gen.go"

// renderOrFail renders rows naming revision, failing the test where they do not
// render.
func renderOrFail(t *testing.T, rows []row, revision string) []byte {
	t.Helper()
	content, err := render(rows, revision)
	if err != nil {
		t.Fatalf("render the table: %v", err)
	}
	return content
}

// TestTheImageTableIsWhatTheHubsComposeRecipesName regenerates the image table
// from the hub's Compose Recipes and Components, naming the revision the
// committed table names, and requires the committed table to be exactly what the
// regeneration writes — so a change to a Recipe's image or implements, a Recipe
// added or one removed, or a change to the labels of a Component a row names,
// fails here until the table is regenerated with
// `go generate ./plugins/internal/composeimport`. It also requires the rows the
// importer's PostgreSQL and Redis cases rest on, each carrying its Component's
// labels as traits and not its category.
//
// 🔴 PLANTED DEFECT: edit one row of imagetable_gen.go by hand — say redis to
// core/datastore/valkey, or drop one of its traits — and the case fails naming
// the regeneration.
func TestTheImageTableIsWhatTheHubsComposeRecipesName(t *testing.T) {
	t.Parallel()
	rows, _, err := generateFromHub(hub)
	if err != nil {
		t.Fatalf("generate the table from %s: %v", hub, err)
	}
	want := renderOrFail(t, rows, composeimport.TableRevision)
	got, err := os.ReadFile(committedTable)
	if err != nil {
		t.Fatalf("read the committed table: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the committed image table is not what the hub's Compose Recipes and Components generate; "+
			"regenerate it with `go generate ./plugins/internal/composeimport` from the aggrega-cli module root\n"+
			"--- committed ---\n%s\n--- generated ---\n%s", got, want)
	}
	for _, r := range []row{{repository: "postgres", component: "core/datastore/postgres"},
		{repository: "redis", component: "core/datastore/redis"}} {
		found := false
		for _, g := range rows {
			if g.repository != r.repository || g.component != r.component {
				continue
			}
			found = true
			if _, category := g.traits[categoryLabel]; len(g.traits) == 0 || category {
				t.Errorf("the row %s -> %s carries the traits %v, want its Component's labels but its category",
					g.repository, g.component, g.traits)
			}
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
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: r\n  version: 1.0.0\nspec:\n")
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
		return "apiVersion: aggrega.ai/v1beta1\nkind: Workload\ninitContainers:\n  - name: pull\n" +
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
	want := []row{{repository: "planted/postgres-needle", component: "core/datastore/postgres"},
		{repository: "redis", component: "core/datastore/redis"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("the planted table holds %v, want %v", rows, want)
	}
	again, _, _ := generate(dir)
	if !bytes.Equal(renderOrFail(t, rows, "r"), renderOrFail(t, again, "r")) {
		t.Errorf("two generations from one set of Recipes differ")
	}
}

// copyHubFile copies the hub's file at rel, beneath its root, to the same place
// beneath root, passing its content through edit.
func copyHubFile(t *testing.T, root, rel string, edit func(string) string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(hub, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read the hub's %s: %v", rel, err)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(body))), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTheTraitsMoveWithAComponentsLabels generates the table from a copy of the
// hub holding its PostgreSQL and Redis Compose Recipes and Components, in which
// one label of the Redis Component is changed: the Redis row's traits move with
// it, so the traits are read from the Component rather than written by hand;
// neither row carries the category label; and two runs write the same bytes.
//
// 🔴 PLANTED DEFECT: in withTraits, stop skipping the category label: both rows
// carry it and the case fails.
func TestTheTraitsMoveWithAComponentsLabels(t *testing.T) {
	t.Parallel()
	const label = "    datastore.aggrega.ai/engine: kv\n"
	const planted = "    datastore.aggrega.ai/engine: planted-engine-needle\n"
	root := t.TempDir()
	same := func(s string) string { return s }
	for _, name := range []string{"postgres", "redis"} {
		copyHubFile(t, root, "aggrega-catalog/recipe/compose/"+name+"/1.0.0/recipe.yaml", same)
	}
	copyHubFile(t, root, "aggrega-catalog/component/core/datastore/postgres/1.0.0/component.yaml", same)
	copyHubFile(t, root, "aggrega-catalog/component/core/datastore/redis/1.0.0/component.yaml", func(s string) string {
		if strings.Count(s, label) != 1 {
			t.Fatalf("the hub's Redis Component no longer carries %q once, so the plant would change nothing", label)
		}
		return strings.Replace(s, label, planted, 1)
	})
	rows, _, err := generateFromHub(root)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(rows) != 2 || rows[1].repository != "redis" {
		t.Fatalf("the planted hub generated %v, want the postgres and redis rows", rows)
	}
	if got := rows[1].traits["datastore.aggrega.ai/engine"]; got != "planted-engine-needle" {
		t.Errorf("the redis row's engine trait is %q, want the planted label's value; traits %v", got, rows[1].traits)
	}
	for _, r := range rows {
		if _, category := r.traits[categoryLabel]; category || len(r.traits) == 0 {
			t.Errorf("the row %s carries the traits %v, want its Component's labels but its category", r.repository, r.traits)
		}
	}
	again, _, err := generateFromHub(root)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if !bytes.Equal(renderOrFail(t, rows, "r"), renderOrFail(t, again, "r")) {
		t.Errorf("two generations from one hub differ")
	}
}

// TestARowWhoseComponentTheHubDoesNotHoldIsRefused gives rows their traits from
// labels holding one row's Component and not the other's, and requires a refusal
// naming the missing Component and its repository, with no rows; and from labels
// holding both, the rows with every label but the category as traits, a
// Component carrying the category alone giving a row no traits.
func TestARowWhoseComponentTheHubDoesNotHoldIsRefused(t *testing.T) {
	t.Parallel()
	rows := []row{{repository: "acme/db", component: "core/x/db"}, {repository: "acme/web", component: "core/x/web"}}
	labels := map[string]map[string]string{
		"core/x/db": {categoryLabel: "x", "component.aggrega.ai/type": "stateful-server", "x.aggrega.ai/engine": "sql"},
	}
	got, err := withTraits(rows, labels)
	if err == nil || got != nil || !strings.Contains(err.Error(), "acme/web maps to core/x/web") {
		t.Errorf("a row naming a Component the hub does not hold gave %v, %v; want a refusal naming it", got, err)
	}
	labels["core/x/web"] = map[string]string{categoryLabel: "x"}
	got, err = withTraits(rows, labels)
	want := []row{
		{repository: "acme/db", component: "core/x/db",
			traits: map[string]string{"component.aggrega.ai/type": "stateful-server", "x.aggrega.ai/engine": "sql"}},
		{repository: "acme/web", component: "core/x/web"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("rows whose Components the hub holds gave %v, %v; want %v", got, err, want)
	}
}

// TestTwoRecipesNamingOneRepositoryForDifferentComponentsAreRefused generates the
// table from two Recipes naming one repository for two Components, and requires
// a refusal naming the repository and both Recipes, with no rows; and from two
// naming one repository for one Component, one row.
func TestTwoRecipesNamingOneRepositoryForDifferentComponentsAreRefused(t *testing.T) {
	t.Parallel()
	workload := "apiVersion: aggrega.ai/v1beta1\nkind: Workload\ncontainers:\n  main:\n    image: \"acme/db:{{ .infra.v }}\"\n"
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
	if err != nil || !reflect.DeepEqual(rows, []row{{repository: "acme/db", component: "core/x/a"}}) {
		t.Errorf("two Recipes naming acme/db for one Component generated %v, %v; want one row", rows, err)
	}
}
