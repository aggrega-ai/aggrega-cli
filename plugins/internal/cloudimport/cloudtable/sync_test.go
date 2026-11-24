// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// hub is the aggrega-hub repository checked out beside this one. The checks
// below read it, and skip, naming the repository, when that checkout is absent.
func hub(t testing.TB) string {
	t.Helper()
	return wasmtest.SiblingPath(t, "aggrega-hub")
}

// committedArmTable is the generated table the arm importer embeds.
const committedArmTable = "../../armimport/table_gen.go"

// loadHubOrFail loads the arm Recipes of [hub] with what they read.
func loadHubOrFail(t *testing.T) *hubCatalog {
	t.Helper()
	h, err := loadHub(hub(t), "arm")
	if err != nil {
		t.Fatalf("load %s: %v", hub(t), err)
	}
	return h
}

// drift returns where committed differs from regenerated, the first line that
// differs named with both its versions, and empty where the two are one.
func drift(committed, regenerated []byte) string {
	if bytes.Equal(committed, regenerated) {
		return ""
	}
	a, b := strings.Split(string(committed), "\n"), strings.Split(string(regenerated), "\n")
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y || i >= len(a) || i >= len(b) {
			return fmt.Sprintf("line %d is %q committed and %q regenerated", i+1, x, y)
		}
	}
	return "the two differ"
}

// TestTheArmTableIsWhatTheHubGenerates regenerates the arm importer's table
// from the hub's arm Recipes and what they read, with the hand file as it is
// committed, naming the revision the committed table names, and requires the
// committed table to be exactly what the regeneration writes — so a Recipe
// added, removed or changed, a Component's labels changed, or a library a
// Recipe imports changed fails here until the table is regenerated with
// `go generate ./plugins/internal/armimport`.
//
// 🔴 PLANTED DEFECT: edit one generated row of armimport/table_gen.go by hand
// — the case below does so to a copy, renaming the first row's Component —
// and the check names the line.
func TestTheArmTableIsWhatTheHubGenerates(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	rows, notes, err := generateFromHub(hub(t), f, armimport.Table())
	if err != nil {
		t.Fatalf("generate the arm table from %s: %v\n%s", hub(t), err, strings.Join(notes, "\n"))
	}
	want, err := render(rows, armimport.TableRevision, f)
	if err != nil {
		t.Fatalf("render the arm table: %v", err)
	}
	got, err := os.ReadFile(committedArmTable)
	if err != nil {
		t.Fatalf("read the committed arm table: %v", err)
	}
	if d := drift(got, want); d != "" {
		t.Errorf("the committed arm table is not what the hub's arm Recipes generate (%s); regenerate it with "+
			"`go generate ./plugins/internal/armimport` from the aggrega-cli module root", d)
	}

	const field = "\t\tComponent: \""
	at := bytes.Index(got, []byte(field))
	if at < 0 {
		t.Fatalf("the committed arm table holds no row naming a Component, so the planted edit would change nothing")
	}
	planted := slices.Concat(got[:at+len(field)], []byte("core/planted/needle-"), got[at+len(field):])
	if d := drift(planted, want); !strings.Contains(d, "core/planted/needle-") {
		t.Errorf("a generated row edited by hand passes the drift check: %q", d)
	}
}

// unaccounted returns, for each arm Recipe and FeatureRecipe of h that table
// neither reaches by a row nor lists in notImported, a line naming it; for
// each notImported entry naming a Recipe a row reaches, a line naming both;
// and for each notImported entry giving no reason, a line naming it.
func unaccounted(h *hubCatalog, table cloudimport.Table) ([]string, error) {
	rows, err := table.Rows()
	if err != nil {
		return nil, err
	}
	reached := map[string]string{}
	for _, r := range rows {
		if _, seen := reached[r.Recipe]; !seen {
			reached[r.Recipe] = r.Key
		}
	}
	listed := map[string]bool{}
	var out []string
	for _, n := range table.NotImported {
		listed[n.Recipe] = true
		if key, ok := reached[n.Recipe]; ok {
			out = append(out, fmt.Sprintf("notImported lists %s, which a row of %s reaches: remove one", n.Recipe, key))
		}
		if strings.TrimSpace(n.Reason) == "" {
			out = append(out, "notImported lists "+n.Recipe+" with no reason: write why no row reaches it")
		}
	}
	for _, name := range h.recipeNames() {
		if _, ok := reached[name]; !ok && !listed[name] {
			out = append(out, "the Recipe arm/"+name+" is reached by no row and listed in no notImported entry")
		}
	}
	for _, name := range h.featureRecipeNames() {
		if !listed[name] {
			out = append(out, "the FeatureRecipe arm/"+name+" is listed in no notImported entry")
		}
	}
	return out, nil
}

// TestEveryArmRecipeIsAccountedFor reads every arm Recipe and FeatureRecipe the
// hub holds as the test runs and requires each to be reached by a row of the
// arm table or listed in its notImported with a reason, and no Recipe both: a
// Recipe the hub gains fails here until the importer maps it or says why not.
//
// 🔴 PLANTED DEFECT: delete a row — the case below does so to a copy of the
// table, dropping a generated row no other row's Recipe shares — and the
// check names its Recipe.
func TestEveryArmRecipeIsAccountedFor(t *testing.T) {
	t.Parallel()
	h := loadHubOrFail(t)
	table := armimport.Table()
	findings, err := unaccounted(h, table)
	if err != nil {
		t.Fatalf("read the arm table's rows: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s: map it in plugins/internal/armimport/table.go, or list it in notImported there with why, "+
			"then run `go generate ./plugins/internal/armimport`", f)
	}

	recipes := map[string]int{}
	for _, r := range table.Generated {
		recipes[r.Recipe]++
	}
	for _, r := range table.Hand {
		recipes[r.Recipe]++
	}
	drop := -1
	for i, r := range table.Generated {
		if recipes[r.Recipe] == 1 {
			drop = i
			break
		}
	}
	if drop < 0 {
		t.Fatalf("every generated row shares its Recipe with another, so deleting one would change nothing")
	}
	planted := table
	planted.Generated = slices.Delete(slices.Clone(table.Generated), drop, drop+1)
	findings, err = unaccounted(h, planted)
	if err != nil {
		t.Fatalf("read the planted table's rows: %v", err)
	}
	name := table.Generated[drop].Recipe
	if !slices.ContainsFunc(findings, func(f string) bool { return strings.Contains(f, "arm/"+name+" ") }) {
		t.Errorf("a table without the row of %s passes the coverage check: %v", name, findings)
	}
}

// missingEntities returns a line for each row of table a hand row writes that
// names a Component or a Recipe h does not hold, or a category other than its
// Component's category label, and for each notImported entry naming an arm
// Recipe or FeatureRecipe h does not hold.
func missingEntities(h *hubCatalog, table cloudimport.Table) ([]string, error) {
	rows, err := table.Rows()
	if err != nil {
		return nil, err
	}
	recipes := map[string]bool{}
	for _, name := range h.recipeNames() {
		recipes[name] = true
	}
	var out []string
	for _, r := range rows {
		if strings.HasPrefix(r.Origin, "generated ") && !strings.HasSuffix(r.Origin, " and hand") {
			continue
		}
		row := "the hand row of " + r.Key + " realized by " + r.Recipe
		labels, ok := h.labels[r.Component]
		switch {
		case !ok:
			out = append(out, row+" names the Component "+r.Component+", which the hub holds no version of")
		case r.Category != labels[schemadraft.LabelCategory]:
			out = append(out, fmt.Sprintf("%s gives the category %q, and the hub's %s carries %q", row, r.Category,
				r.Component, labels[schemadraft.LabelCategory]))
		}
		if !recipes[r.Recipe] {
			out = append(out, row+" names the Recipe arm/"+r.Recipe+", which the hub holds no version of")
		}
	}
	featureRecipes := map[string]bool{}
	for _, name := range h.featureRecipeNames() {
		featureRecipes[name] = true
	}
	for _, n := range table.NotImported {
		if !recipes[n.Recipe] && !featureRecipes[n.Recipe] {
			out = append(out, "notImported lists "+n.Recipe+", which the hub holds no arm Recipe or FeatureRecipe of")
		}
	}
	return out, nil
}

// TestEveryHandRowNamesAHubEntity requires every Component and Recipe a hand
// row of the arm table names, and every Recipe its notImported lists, to be
// one the hub holds, and every hand row's category to be its Component's
// category label, which the importer scores a candidate by.
//
// 🔴 PLANTED DEFECT: name a Component the hub does not hold in a hand row — the
// case below adds one to a copy of the table — and the check names it.
func TestEveryHandRowNamesAHubEntity(t *testing.T) {
	t.Parallel()
	h := loadHubOrFail(t)
	table := armimport.Table()
	findings, err := missingEntities(h, table)
	if err != nil {
		t.Fatalf("read the arm table's rows: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s: correct plugins/internal/armimport/table.go", f)
	}

	recipes := h.recipeNames()
	if len(recipes) == 0 {
		t.Fatalf("the hub holds no arm Recipe, so the planted row would name none")
	}
	planted := table
	planted.Hand = append(slices.Clone(table.Hand), cloudimport.Row{Key: "Microsoft.Planted/needles",
		Component: "core/planted/needle", Category: "datastore", Recipe: recipes[0]})
	findings, err = missingEntities(h, planted)
	if err != nil {
		t.Fatalf("read the planted table's rows: %v", err)
	}
	if !slices.ContainsFunc(findings, func(f string) bool { return strings.Contains(f, "core/planted/needle") }) {
		t.Errorf("a hand row naming a Component the hub does not hold passes the existence check: %v", findings)
	}
}
