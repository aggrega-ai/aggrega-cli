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
// its Recipes by rules of its own (arm.go for arm, cloudformation.go for
// cloudformation), and a format's reader may refuse a Recipe whose text it
// cannot read a row from, naming the Recipe and what to fix; no table is then
// written. A row carries its Component's category label as its category,
// every other label of the Component, as an import is handed them, as its
// traits, and each values key the Component's schema requires and declares no
// default for, with its type, as a requirement.
//
// The generator reads the format's hand-written table, in the importer's
// package, for what it accounts for: a Recipe its notImported lists generates
// no row, though its imports are resolved like every other's. A Recipe a hand
// row written whole names — a hand row of another key and When than the
// Recipe's text gives — generates no row either, the hand row standing in its
// place. Two Recipes claiming one key and When are refused, naming both, and
// no table is written, since a table naming one of them would decide which
// Component a resource of that kind is — unless the hand table writes the
// rows of all of them but one itself, each Recipe it names then generating no
// row. A format's hand table may also write the Whens of the Recipes whose
// text gives no When telling their resources apart: each Recipe it names gives
// one row for each When written, its text's When followed by that one, and a
// When written for a Recipe that gives no row is refused. A Recipe whose
// import the checkout does not resolve is refused too, naming it and the
// reference, and so is a Recipe implementing a Component the checkout holds no
// version of. The rows are ordered by key, When and Recipe, so one checkout
// always writes the same bytes.
//
// A Recipe sourcing a module of the Azure Verified Modules programme gives a
// row keyed by the type of the resource the module deploys, which the format's
// index of the programme's modules gives; a module the index lacks is refused,
// naming the Recipe and the module.
//
// Beside the rows it writes the image rows of the container products: each
// Recipe whose locals name a literal image default maps the image's
// repository, as the Compose image table reads one, to the Recipe and its
// Component, with the infra key the Recipe reads the image's tag from. For a
// format that asks for them it writes, too, the one feature type each Recipe's
// assertions admit at a credential slot, where they admit one.
//
// The table names the aggrega-hub commit it was generated from: the last
// commit that changed a path the generator reads, so a commit to anything else
// in the checkout leaves the table as it is. It is refused where the
// directories the generator reads hold changes no commit records, unless
// -revision names what the table is read from.
//
// A separate run writes the Azure Verified Modules index the format's package
// embeds, from a copy of Microsoft's module index file downloaded at a commit
// the run is given and the generated file names, with the licence that file's
// repository states. It is run deliberately and never by a test.
//
// Usage:
//
//	go run ./plugins/internal/cloudimport/cloudtable -format arm -hub ../aggrega-hub -out plugins/internal/armimport/table_gen.go
//	go run ./plugins/internal/cloudimport/cloudtable -format cloudformation -hub ../aggrega-hub -out plugins/internal/cfnimport/table_gen.go
//	go run ./plugins/internal/cloudimport/cloudtable -format arm -hub DIR -revision NAME -report
//	go run ./plugins/internal/cloudimport/cloudtable -avm-index BicepResourceModules.csv -avm-commit COMMIT -out plugins/internal/armimport/avmindex_gen.go
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
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfnimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// format is one importer whose table the generator writes: its name, the
// capability its Recipes bind to, the package its table is written into, how
// one Recipe's row is read, the type of the resource a sourced module of the
// Azure Verified Modules programme deploys, and the importer's table, whose
// hand rows and notImported the generator reads.
type format struct {
	name, capability, pkg string
	extract               func(recipeSource) (cloudimport.Row, string)
	moduleType            func(module string) (string, bool)
	table                 func() cloudimport.Table
	// read, where set, reads one Recipe's row in extract's place: the row,
	// with notes on what it leaves out; no row, with why; or an error saying
	// why the Recipe is refused and what to fix, which writes no table.
	read func(recipeSource) (cloudimport.Row, string, error)
	// whens, where set, returns the Whens the importer's table writes by hand
	// for the generated rows of the Recipes it names.
	whens func() map[string][][]cloudimport.Pred
	// slots makes the table list, beside the rows, the one feature type each
	// Recipe's assertions admit at a credential slot, and how each Recipe names
	// the resource that is its instance.
	slots bool
}

// formats are the formats the generator writes a table for.
var formats = []format{
	{name: "arm", capability: "arm", pkg: "armimport", extract: extractARM, moduleType: avmResourceType,
		table: armimport.Table},
	{name: "cloudformation", capability: "cloudformation", pkg: "cfnimport", read: extractCloudFormation,
		table: cfnimport.Table, whens: cfnimport.RecipeWhens, slots: true},
}

// avmResourceType returns the type of the resource the Azure Verified Modules
// module at path deploys, from the arm importer's index.
func avmResourceType(module string) (string, bool) {
	m, ok := armimport.AVMModuleByPath(module)
	return m.ResourceType(), ok
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
	name := flag.String("format", "", "the format whose table is generated: arm or cloudformation (required, unless "+
		"-avm-index is given)")
	hub := flag.String("hub", "", "the aggrega-hub checkout whose Recipes the table is generated from (required)")
	out := flag.String("out", "", "the file the table is written to; standard output where empty")
	revision := flag.String("revision", "", "what the table names as the revision it was read from; the checkout's HEAD where empty")
	report := flag.Bool("report", false, "print, on standard error, what each Recipe contributed and why")
	avmIndex := flag.String("avm-index", "", "write the Azure Verified Modules index instead, from this copy of the programme's BicepResourceModules.csv")
	avmCommit := flag.String("avm-commit", "", "the commit of the Azure Verified Modules repository the -avm-index file was downloaded at")
	flag.Parse()
	if *avmIndex != "" || *avmCommit != "" {
		if *avmIndex == "" || *avmCommit == "" || *name != "" || *hub != "" || *revision != "" || *report {
			fmt.Fprintln(os.Stderr, "cloudtable: -avm-index and -avm-commit are given together, with -out and none of "+
				"-format, -hub, -revision and -report")
			os.Exit(2)
		}
		if err := runAVMIndex(*avmIndex, *avmCommit, *out); err != nil {
			fmt.Fprintln(os.Stderr, "cloudtable: "+err.Error())
			os.Exit(1)
		}
		return
	}
	f, ok := formatNamed(*name)
	if !ok {
		fmt.Fprintf(os.Stderr, "cloudtable: -format names %q, which is no format a table is generated for; name arm "+
			"or cloudformation\n", *name)
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
	images, err := generateImagesFromHub(hub, f)
	if err != nil {
		return err
	}
	facts, err := generateFactsFromHub(hub, f, f.table())
	if err != nil {
		return err
	}
	if revision == "" {
		if revision, err = hubRevision(hub, f); err != nil {
			return err
		}
	}
	content, err := renderTable(rows, images, facts, revision, f)
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

// unindexedModule is a Recipe sourcing a module of the Azure Verified Modules
// programme that the format's index does not hold.
type unindexedModule struct {
	recipes, modules []string
}

// Error names each Recipe and its module, and what to regenerate.
func (u *unindexedModule) Error() string {
	var lines []string
	for i := range u.recipes {
		lines = append(lines, "the Recipe "+u.recipes[i]+" sources the module "+u.modules[i])
	}
	return strings.Join(lines, "; ") + ", which the index of Azure Verified Modules holds no row of, so the type of " +
		"the resource it deploys is not known and no table is written: regenerate the index from a newer module " +
		"index file, or source a module the index holds"
}

// refusedRecipes is each Recipe a format's reader refuses, with why and what
// to fix, and each Recipe a When is written by hand for that gives no row.
type refusedRecipes struct {
	lines []string
}

// Error names each Recipe and what to fix.
func (r *refusedRecipes) Error() string {
	return strings.Join(r.lines, "; ") + "; no table is written"
}

// generate reads each Recipe of h's capability, by name, at the version a
// reference naming it without one resolves to, and returns f's rows and one
// note per Recipe.
func generate(h *hubCatalog, f format, hand cloudimport.Table) ([]cloudimport.Row, []string, error) {
	notImported := map[string]bool{}
	for _, n := range hand.NotImported {
		notImported[n.Recipe] = true
	}
	var whens map[string][][]cloudimport.Pred
	if f.whens != nil {
		whens = f.whens()
	}
	applied := map[string]bool{}
	var (
		claims  []claim
		notes   []string
		missing missingComponent
		lacking unindexedModule
		refused refusedRecipes
	)
	for _, name := range h.recipeNames() {
		r, ok := h.recipe(name)
		if !ok {
			continue
		}
		identity := f.capability + "/" + name + ":" + r.Metadata.Version
		src, err := h.read(r)
		if err != nil {
			return nil, notes, err
		}
		if notImported[name] {
			notes = append(notes, identity+": no row, since the hand table lists it in notImported")
			continue
		}
		var (
			row cloudimport.Row
			why string
		)
		if f.read != nil {
			if row, why, err = f.read(src); err != nil {
				refused.lines = append(refused.lines, "the Recipe "+identity+" ("+src.path+") "+err.Error())
				continue
			}
		} else {
			row, why = f.extract(src)
		}
		if row.Key == "" {
			notes = append(notes, identity+": no row, since "+why)
			continue
		}
		if row.Form == cloudimport.FormAVM && f.moduleType != nil {
			typ, ok := f.moduleType(row.Key)
			if !ok {
				lacking.recipes = append(lacking.recipes, identity)
				lacking.modules = append(lacking.modules, row.Key)
				continue
			}
			row.Key = typ
		}
		labels, ok := h.labels[src.component]
		if !ok {
			missing.recipes = append(missing.recipes, identity)
			missing.components = append(missing.components, src.component)
			continue
		}
		row.Component, row.Recipe, row.Origin = src.component, name, "generated "+src.path
		row.Category, row.Traits = labelsOf(labels)
		row.Requires = h.requires[src.component]
		if why != "" {
			why = "; " + why
		}
		hands, ok := whens[name]
		if !ok {
			hands = [][]cloudimport.Pred{nil}
		}
		applied[name] = ok
		for _, w := range hands {
			told := row
			if len(w) > 0 {
				told.When = slices.Concat(row.When, w)
			}
			claims = append(claims, claim{row: told, identity: identity})
			notes = append(notes, identity+": "+told.Key+cloudimport.WhenText(told.When)+" -> "+told.Component+why)
		}
	}
	if len(refused.lines) > 0 {
		return nil, notes, &refused
	}
	if len(missing.recipes) > 0 {
		return nil, notes, &missing
	}
	if len(lacking.recipes) > 0 {
		return nil, notes, &lacking
	}
	for _, name := range slices.Sorted(maps.Keys(whens)) {
		if !applied[name] {
			refused.lines = append(refused.lines, "the importer's table.go writes Whens for the Recipe "+
				f.capability+"/"+name+", which gives no row: remove them, or let the Recipe give a row")
		}
	}
	if len(refused.lines) > 0 {
		return nil, notes, &refused
	}
	rows, conflict := settle(claims, hand.Hand)
	if conflict != nil {
		return nil, notes, conflict
	}
	slices.SortFunc(rows, func(a, b cloudimport.Row) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Key), strings.ToLower(b.Key)),
			cmp.Compare(a.Identity(), b.Identity()), cmp.Compare(a.Recipe, b.Recipe))
	})
	if _, err := (cloudimport.Table{Generated: rows}).Rows(); err != nil {
		return nil, notes, err
	}
	return rows, notes, nil
}

// settle returns the claims' rows, settled against the hand rows. A claim
// whose Recipe a hand row of another key and When names gives no row: that
// hand row is the Recipe's row, written whole, and stands in the claim's
// place. A claim whose key and When a hand row shares stays, the hand row
// merging into it. Where two or more claims share one key and When, each
// whose Recipe any hand row names gives no row, and where two or more remain
// they are a conflict.
func settle(claims []claim, hand []cloudimport.Row) ([]cloudimport.Row, *claimConflict) {
	named := map[string]bool{}
	merging := map[string]bool{}
	for _, r := range hand {
		if r.Recipe != "" {
			named[r.Recipe] = true
		}
		merging[r.Identity()] = true
	}
	var order []string
	groups := map[string][]claim{}
	for _, c := range claims {
		id := c.row.Identity()
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
			g = slices.DeleteFunc(slices.Clone(g), func(c claim) bool { return named[c.row.Recipe] })
		} else if named[g[0].row.Recipe] && !merging[id] {
			g = nil
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

// hubRevision returns the last commit of the checkout at hub that changed a
// path the generator reads for f, refusing a checkout whose directories the
// generator reads hold changes no commit records.
func hubRevision(hub string, f format) (string, error) {
	revision, err := lastReadCommit(hub, f)
	if err != nil {
		return "", err
	}
	args := []string{"-C", hub, "status", "--porcelain", "--untracked-files=all", "--"}
	args = append(args, readPaths(f)...)
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
	return revision, nil
}

// lastReadCommit returns the last commit of the checkout at hub that changed a
// path the generator reads for f, whatever the checkout holds uncommitted.
func lastReadCommit(hub string, f format) (string, error) {
	args := append([]string{"-C", hub, "log", "-1", "--format=%H", "--"}, readPaths(f)...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read the last commit of %s that changed what the %s table reads (pass -revision to "+
			"name it): %w", hub, f.capability, err)
	}
	revision := strings.TrimSpace(string(out))
	if revision == "" {
		return "", errors.New("no commit of " + hub + " holds anything the " + f.capability + " table reads (pass " +
			"-revision to name it)")
	}
	return revision, nil
}

// readPaths returns the directories the generator reads for f, as git
// pathspecs.
func readPaths(f format) []string {
	var paths []string
	for _, d := range readDirs(f.capability) {
		paths = append(paths, filepath.ToSlash(d))
	}
	return paths
}
