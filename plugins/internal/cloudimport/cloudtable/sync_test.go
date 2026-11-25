// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"cmp"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
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

// shallow reports whether the checkout at dir holds only part of its history.
func shallow(t *testing.T, dir string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-shallow-repository").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
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

// revisionLine matches the line a generated table names its revision on,
// capturing the revision.
var revisionLine = regexp.MustCompile(`(?m)^const TableRevision = "([^"]*)"$`)

// tableDrift returns what is wrong with committed, the text of a generated
// table of f, against rows and images regenerated from the checkout at hub: the
// first line at which it differs from the regeneration rendered with the
// revision committed itself names — so a row changed by hand, or a change to
// what the table reads, is named, and the hub moving on is not — and that
// revision where it is no commit the checkout holds. A checkout without its
// history cannot say which commits it holds, so there the revision is held to
// nothing.
func tableDrift(t *testing.T, committed []byte, rows []cloudimport.Row, images []cloudimport.ImageRow, hub string,
	f format) []string {
	t.Helper()
	m := revisionLine.FindSubmatch(committed)
	if m == nil {
		return []string{"the committed table names no TableRevision"}
	}
	revision := string(m[1])
	var out []string
	want, err := render(rows, images, revision, f)
	if err != nil {
		t.Fatalf("render the %s table: %v", f.capability, err)
	}
	if d := drift(committed, want); d != "" {
		out = append(out, "its rows are not what the hub's "+f.capability+" Recipes generate ("+d+")")
	}
	if !shallow(t, hub) {
		if err := exec.Command("git", "-C", hub, "cat-file", "-e", revision+"^{commit}").Run(); err != nil {
			out = append(out, fmt.Sprintf("it names the revision %q, which the hub checkout holds no commit of",
				revision))
		}
	}
	return out
}

// TestTheArmTableIsWhatTheHubGenerates regenerates the arm importer's table
// from the hub's arm Recipes and what they read, with the hand file as it is
// committed, renders it naming the revision the committed table names, and
// requires the committed table to be exactly what that writes — so a Recipe
// added, removed or changed in a way a row reads, a Component's labels changed,
// or a library a Recipe imports changed fails here until the table is
// regenerated with `go generate ./plugins/internal/armimport`, and the hub
// committing anything a row does not read never does. The revision committed
// must be a commit the hub checkout holds.
//
// 🔴 PLANTED DEFECTS: edit one generated row of armimport/table_gen.go by hand,
// or name a revision the hub holds no commit of — the cases below do each to a
// copy — and the check names it; a copy naming an older commit that changed
// what the table reads, its rows the same, passes.
func TestTheArmTableIsWhatTheHubGenerates(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	rows, notes, err := generateFromHub(hub(t), f, armimport.Table())
	if err != nil {
		t.Fatalf("generate the arm table from %s: %v\n%s", hub(t), err, strings.Join(notes, "\n"))
	}
	images, err := generateImagesFromHub(hub(t), f)
	if err != nil {
		t.Fatalf("generate the arm image rows from %s: %v", hub(t), err)
	}
	got, err := os.ReadFile(committedArmTable)
	if err != nil {
		t.Fatalf("read the committed arm table: %v", err)
	}
	for _, d := range tableDrift(t, got, rows, images, hub(t), f) {
		t.Errorf("the committed arm table: %s; regenerate it with `go generate ./plugins/internal/armimport` from "+
			"the aggrega-cli module root", d)
	}

	const field = "\t\tComponent: \""
	at := bytes.Index(got, []byte(field))
	if at < 0 {
		t.Fatalf("the committed arm table holds no row naming a Component, so the planted edit would change nothing")
	}
	planted := slices.Concat(got[:at+len(field)], []byte("core/planted/needle-"), got[at+len(field):])
	if found := tableDrift(t, planted, rows, images, hub(t), f); !slices.ContainsFunc(found, func(d string) bool {
		return strings.Contains(d, "core/planted/needle-")
	}) {
		t.Errorf("a generated row edited by hand passes the drift check: %v", found)
	}

	if shallow(t, hub(t)) {
		t.Logf("%s holds no history, so a revision is held to nothing and the revision cases plant nothing", hub(t))
		return
	}
	madeUp := revisionLine.ReplaceAll(slices.Clone(got),
		[]byte(`const TableRevision = "0123456789abcdef0123456789abcdef01234567"`))
	if found := tableDrift(t, madeUp, rows, images, hub(t), f); !slices.ContainsFunc(found, func(d string) bool {
		return strings.Contains(d, "0123456789abcdef0123456789abcdef01234567")
	}) {
		t.Errorf("a revision the hub holds no commit of passes the drift check: %v", found)
	}
	args := append([]string{"-C", hub(t), "log", "-2", "--format=%H", "--"}, readPaths(f)...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		t.Fatalf("read the commits of %s that changed what the arm table reads: %v", hub(t), err)
	}
	commits := strings.Fields(string(out))
	if len(commits) < 2 {
		t.Fatalf("%s holds one commit that changed what the arm table reads, so no older one can be named", hub(t))
	}
	older, err := render(rows, images, commits[1], f)
	if err != nil {
		t.Fatalf("render the arm table naming %s: %v", commits[1], err)
	}
	if found := tableDrift(t, older, rows, images, hub(t), f); len(found) > 0 {
		t.Errorf("a table naming the older commit %s, its rows the same, fails the drift check: %v", commits[1],
			found)
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

// missingEntities returns a line for each hand row of table that names a
// Component or a Recipe h does not hold, gives a category other than its
// Component's category label, or a trait its Component's labels do not carry
// with that value, and for each notImported entry naming an arm Recipe or
// FeatureRecipe h does not hold. A hand row leaving its Component, category or
// Recipe unset takes the generated row's of its identity, as merging it does.
func missingEntities(h *hubCatalog, table cloudimport.Table) []string {
	generated := map[string]cloudimport.Row{}
	for _, g := range table.Generated {
		generated[g.Identity()] = g
	}
	recipes := map[string]bool{}
	for _, name := range h.recipeNames() {
		recipes[name] = true
	}
	var out []string
	for _, r := range table.Hand {
		g := generated[r.Identity()]
		component, category, recipe := cmp.Or(r.Component, g.Component), cmp.Or(r.Category, g.Category),
			cmp.Or(r.Recipe, g.Recipe)
		row := "the hand row of " + r.Key + cloudimport.WhenText(r.When)
		if labels, ok := h.labels[component]; !ok {
			out = append(out, fmt.Sprintf("%s names the Component %q, which the hub holds no version of", row,
				component))
		} else {
			if category != labels[schemadraft.LabelCategory] {
				out = append(out, fmt.Sprintf("%s gives the category %q, and the hub's %s carries %q", row,
					category, component, labels[schemadraft.LabelCategory]))
			}
			for _, label := range slices.Sorted(maps.Keys(r.Traits)) {
				want, carried := labels[label]
				if label == schemadraft.LabelCategory || !carried || want != r.Traits[label] {
					out = append(out, fmt.Sprintf("%s gives the trait %s=%q, and the hub's %s carries %q under "+
						"it, the category being no trait", row, label, r.Traits[label], component, want))
				}
			}
		}
		if !recipes[recipe] {
			out = append(out, fmt.Sprintf("%s names the Recipe arm/%s, which the hub holds no version of", row,
				recipe))
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
	return out
}

// TestEveryHandRowNamesAHubEntity requires every Component and Recipe a hand
// row of the arm table names, and every Recipe its notImported lists, to be
// one the hub holds, and every hand row's category and traits to be its
// Component's labels, which the importer scores a candidate by.
//
// 🔴 PLANTED DEFECTS: name a Component the hub does not hold in a hand row,
// give one a category other than its Component's, or a trait its Component
// does not carry — the cases below add each to a copy of the table — and the
// check names it.
func TestEveryHandRowNamesAHubEntity(t *testing.T) {
	t.Parallel()
	h := loadHubOrFail(t)
	table := armimport.Table()
	for _, f := range missingEntities(h, table) {
		t.Errorf("%s: correct plugins/internal/armimport/table.go", f)
	}

	recipes := h.recipeNames()
	const component = "core/datastore/postgres"
	labels, ok := h.labels[component]
	if len(recipes) == 0 || !ok || labels[schemadraft.LabelCategory] != "datastore" {
		t.Fatalf("the hub holds no arm Recipe, or no %s in the datastore category, so the planted rows would "+
			"plant nothing", component)
	}
	for _, c := range []struct {
		name string
		row  cloudimport.Row
		want string
	}{
		{"a Component the hub does not hold", cloudimport.Row{Component: "core/planted/needle",
			Category: "datastore"}, `the Component "core/planted/needle"`},
		{"a category not its Component's", cloudimport.Row{Component: component, Category: "planted-needle"},
			`the category "planted-needle"`},
		{"a trait its Component does not carry", cloudimport.Row{Component: component, Category: "datastore",
			Traits: map[string]string{"component.aggrega.ai/type": "planted-needle"}},
			`the trait component.aggrega.ai/type="planted-needle"`},
	} {
		planted := table
		c.row.Key, c.row.Recipe = "Microsoft.Planted/needles", recipes[0]
		planted.Hand = append(slices.Clone(table.Hand), c.row)
		findings := missingEntities(h, planted)
		if !slices.ContainsFunc(findings, func(f string) bool { return strings.Contains(f, c.want) }) {
			t.Errorf("a hand row with %s passes the existence check: %v", c.name, findings)
		}
	}
}
