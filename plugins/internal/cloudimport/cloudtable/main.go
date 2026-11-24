// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Command cloudtable generates a cloud importer's mapping table from the
// Recipes of an aggrega-hub checkout: one row per Recipe of the format's
// capability, mapping the vendor type, or the module, the Recipe writes to the
// catalog Component it implements and to the Recipe itself, written as the Go
// source the importer's package embeds beside its hand-written rows.
//
// It loads the checkout through the compiler's own catalog loader — the
// capability's Recipes and FeatureRecipes, the Components and the schema
// libraries they read, and the mapping and artifact libraries a Recipe imports
// — and reads, of each Recipe at the version a reference naming it without one
// resolves to, its text literally, every library import resolved as a build
// resolves it; it renders nothing and evaluates no template. Each format reads
// its Recipes by rules of its own (arm.go for arm). A row carries its
// Component's category label as its category, and every other label of the
// Component, as an import is handed them, as its traits.
//
// The generator reads the format's hand-written table, in the importer's
// package, for what it accounts for: a Recipe its notImported lists generates
// no row. Two Recipes claiming one key and When are refused, naming both, and
// no table is written, since a table naming one of them would decide which
// Component a resource of that kind is — unless the hand table writes the
// rows of all of them but one itself, each Recipe it names then generating no
// row. A Recipe whose import the checkout does not resolve is refused too,
// naming it and the reference, and so is a Recipe implementing a Component the
// checkout holds no version of. The rows are ordered by key, When and Recipe,
// so one checkout always writes the same bytes.
//
// The table names the aggrega-hub commit it was generated from: the checkout's
// HEAD, read through git, which is refused where the directories the generator
// reads hold changes no commit records, unless -revision names what the table
// is read from.
//
// Usage:
//
//	go run ./plugins/internal/cloudimport/cloudtable -format arm -hub ../aggrega-hub -out plugins/internal/armimport/table_gen.go
//	go run ./plugins/internal/cloudimport/cloudtable -format arm -hub DIR -revision NAME -report
package main

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// format is one importer whose table the generator writes: its name, the
// capability its Recipes bind to, the package its table is written into, how
// one Recipe's row is read, and the importer's table, whose hand rows and
// notImported the generator reads.
type format struct {
	name, capability, pkg string
	extract               func(recipeSource) (cloudimport.Row, string)
	table                 func() cloudimport.Table
}

// formats are the formats the generator writes a table for.
var formats = []format{
	{name: "arm", capability: "arm", pkg: "armimport", extract: extractARM, table: armimport.Table},
}

// formatNamed returns the format named name.
func formatNamed(name string) (format, bool) {
	for _, f := range formats {
		if f.name == name {
			return f, true
		}
	}
	return format{}, false
}

func main() {
	name := flag.String("format", "", "the format whose table is generated: arm (required)")
	hub := flag.String("hub", "", "the aggrega-hub checkout whose Recipes the table is generated from (required)")
	out := flag.String("out", "", "the file the table is written to; standard output where empty")
	revision := flag.String("revision", "", "what the table names as the revision it was read from; the checkout's HEAD where empty")
	report := flag.Bool("report", false, "print, on standard error, what each Recipe contributed and why")
	flag.Parse()
	f, ok := formatNamed(*name)
	if !ok {
		fmt.Fprintf(os.Stderr, "cloudtable: -format names %q, which is no format a table is generated for; name arm\n",
			*name)
		os.Exit(2)
	}
	if *hub == "" {
		fmt.Fprintln(os.Stderr, "cloudtable: -hub names no checkout")
		os.Exit(2)
	}
	if info, err := os.Stat(*hub); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "cloudtable: no aggrega-hub checkout at %s; check out aggrega-hub there, or name the "+
			"checkout with -hub\n", *hub)
		os.Exit(1)
	}
	if err := run(f, *hub, *out, *revision, *report); err != nil {
		fmt.Fprintln(os.Stderr, "cloudtable: "+err.Error())
		os.Exit(1)
	}
}

// run generates f's table from the checkout at hub and writes it to out.
func run(f format, hub, out, revision string, report bool) error {
	rows, notes, err := generateFromHub(hub, f, f.table())
	if report {
		for _, n := range notes {
			fmt.Fprintln(os.Stderr, n)
		}
	}
	if err != nil {
		return err
	}
	if revision == "" {
		if revision, err = hubRevision(hub, f); err != nil {
			return err
		}
	}
	content, err := render(rows, revision, f)
	if err != nil {
		return err
	}
	if out == "" {
		_, err = os.Stdout.Write(content)
		return err
	}
	return os.WriteFile(out, content, 0o644)
}

// generateFromHub generates f's rows from the checkout at hub, with the hand
// table hand, and one note per Recipe saying what it contributed.
func generateFromHub(hub string, f format, hand cloudimport.Table) ([]cloudimport.Row, []string, error) {
	h, err := loadHub(hub, f.capability)
	if err != nil {
		return nil, nil, err
	}
	return generate(h, f, hand)
}

// claim is one Recipe's generated row, with the Recipe's identity.
type claim struct {
	row      cloudimport.Row
	identity string
}

// claimConflict is two or more Recipes claiming one key and When, each group
// of claims one key and When.
type claimConflict struct {
	groups [][]claim
}

// Error names each key and When with the Recipes claiming it and the
// Components they implement, and what to write.
func (c *claimConflict) Error() string {
	var lines []string
	for _, g := range c.groups {
		first := g[0].row
		dup := (&cloudimport.DuplicateRow{Key: first.Key, When: first.When, Origin: "generated"}).Error()
		var by []string
		for _, cl := range g {
			by = append(by, cl.identity+" implementing "+cl.row.Component)
		}
		lines = append(lines, dup+" — the Recipes "+strings.Join(by, " and ")+" claim it")
	}
	return strings.Join(lines, "; ") + "; the table maps a key and When to one Recipe, and the hub's text does not " +
		"say which, so no table is written: write the row of each but one by hand, with a When telling their " +
		"resources apart, in the importer's table.go, or list it in notImported there with why"
}

// missingComponent is a Recipe implementing a Component the hub holds no
// version of.
type missingComponent struct {
	recipes, components []string
}

// Error names each Recipe and the Component it implements.
func (m *missingComponent) Error() string {
	var lines []string
	for i := range m.recipes {
		lines = append(lines, "the Recipe "+m.recipes[i]+" implements "+m.components[i])
	}
	return strings.Join(lines, "; ") + ", which the hub's catalog holds no version of, so no row can carry its " +
		"labels and no table is written"
}

// generate reads each Recipe of h's capability, by name, at the version a
// reference naming it without one resolves to, and returns f's rows and one
// note per Recipe.
func generate(h *hubCatalog, f format, hand cloudimport.Table) ([]cloudimport.Row, []string, error) {
	notImported := map[string]bool{}
	for _, n := range hand.NotImported {
		notImported[n.Recipe] = true
	}
	handNamed := map[string]bool{}
	for _, r := range hand.Hand {
		handNamed[r.Recipe] = true
	}
	var (
		claims  []claim
		notes   []string
		missing missingComponent
	)
	for _, name := range h.recipeNames() {
		r, ok := h.recipe(name)
		if !ok {
			continue
		}
		identity := f.capability + "/" + name + ":" + r.Metadata.Version
		if notImported[name] {
			notes = append(notes, identity+": no row, since the hand table lists it in notImported")
			continue
		}
		src, err := h.read(r)
		if err != nil {
			return nil, notes, err
		}
		row, why := f.extract(src)
		if row.Key == "" {
			notes = append(notes, identity+": no row, since "+why)
			continue
		}
		labels, ok := h.labels[src.component]
		if !ok {
			missing.recipes = append(missing.recipes, identity)
			missing.components = append(missing.components, src.component)
			continue
		}
		row.Component, row.Recipe, row.Origin = src.component, name, "generated "+src.path
		row.Category, row.Traits = labelsOf(labels)
		claims = append(claims, claim{row: row, identity: identity})
		notes = append(notes, identity+": "+row.Key+whenNote(row)+" -> "+row.Component)
	}
	if len(missing.recipes) > 0 {
		return nil, notes, &missing
	}
	rows, conflict := settle(claims, handNamed)
	if conflict != nil {
		return nil, notes, conflict
	}
	slices.SortFunc(rows, func(a, b cloudimport.Row) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Key), strings.ToLower(b.Key)),
			cmp.Compare(claimText(a), claimText(b)), cmp.Compare(a.Recipe, b.Recipe))
	})
	if _, err := (cloudimport.Table{Generated: rows}).Rows(); err != nil {
		return nil, notes, err
	}
	return rows, notes, nil
}

// settle returns the claims' rows, two or more claims of one key and When
// settled: a claim whose Recipe a hand row names gives no row, and where two
// or more remain they are a conflict.
func settle(claims []claim, handNamed map[string]bool) ([]cloudimport.Row, *claimConflict) {
	var order []string
	groups := map[string][]claim{}
	for _, c := range claims {
		id := strings.ToLower(c.row.Key) + claimText(c.row)
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = append(groups[id], c)
	}
	var rows []cloudimport.Row
	var conflict claimConflict
	for _, id := range order {
		g := groups[id]
		if len(g) > 1 {
			g = slices.DeleteFunc(slices.Clone(g), func(c claim) bool { return handNamed[c.row.Recipe] })
		}
		switch len(g) {
		case 0:
		case 1:
			rows = append(rows, g[0].row)
		default:
			conflict.groups = append(conflict.groups, g)
		}
	}
	if len(conflict.groups) > 0 {
		return nil, &conflict
	}
	return rows, nil
}

// claimText writes a row's When as one text whatever the order of its
// predicates and the case of their paths and values, empty for none, as the
// table matches two Whens.
func claimText(r cloudimport.Row) string {
	return strings.ToLower(whenNote(r))
}

// whenNote writes a row's When as report text, its predicates sorted, empty
// for none.
func whenNote(r cloudimport.Row) string {
	preds := make([]string, len(r.When))
	for i, p := range r.When {
		preds[i] = strings.Join(p.Path, ".") + " " + string(p.Op)
		if p.Op != cloudimport.OpPresent && p.Op != cloudimport.OpAbsent {
			preds[i] += fmt.Sprintf(" %q", p.Value)
		}
	}
	slices.SortFunc(preds, func(a, b string) int { return cmp.Compare(strings.ToLower(a), strings.ToLower(b)) })
	if len(preds) == 0 {
		return ""
	}
	return " when " + strings.Join(preds, " and ")
}

// labelsOf returns a Component's category label and every other label, nil
// where it carries none.
func labelsOf(labels map[string]string) (string, map[string]string) {
	traits := maps.Clone(labels)
	delete(traits, schemadraft.LabelCategory)
	if len(traits) == 0 {
		traits = nil
	}
	return labels[schemadraft.LabelCategory], traits
}

// hubRevision returns the commit the checkout at hub has checked out, refusing
// a checkout whose directories the generator reads for f hold changes no
// commit records.
func hubRevision(hub string, f format) (string, error) {
	head, err := exec.Command("git", "-C", hub, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read the commit %s has checked out (pass -revision to name it): %w", hub, err)
	}
	args := []string{"-C", hub, "status", "--porcelain", "--untracked-files=all", "--"}
	for _, d := range readDirs(f.capability) {
		args = append(args, filepath.ToSlash(d))
	}
	status, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read whether the %s Recipes and what they read in %s hold changes: %w", f.capability,
			hub, err)
	}
	if len(bytes.TrimSpace(status)) > 0 {
		return "", errors.New("the " + f.capability + " Recipes, FeatureRecipes, Components or libraries of " + hub +
			" hold changes no commit records, so no commit names what the table is read from (pass -revision to " +
			"name it):\n" + strings.TrimRight(string(status), "\n"))
	}
	return strings.TrimSpace(string(head)), nil
}
