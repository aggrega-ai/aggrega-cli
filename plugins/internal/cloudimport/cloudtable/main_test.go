// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/libraryimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// miniatureHub is the hub this directory's testdata holds: Components, an arm
// Recipe writing its resource in a fragment, one sourcing a module by
// bicep-module-oci, one taking its fragment from an ArtifactLibrary, one taking
// its valuesMapping from a MappingLibrary in two versions, and a reference
// realization writing no resource.
const miniatureHub = "testdata/hub"

// copyTree copies the directory tree at from beneath to.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", from, err)
	}
}

// hubWith returns a copy of the miniature hub with each file of extra written
// at its path beneath the hub's root.
func hubWith(t *testing.T, extra map[string]string) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, miniatureHub, root)
	for rel, body := range extra {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// moduleRecipe writes an arm Recipe named name implementing component that
// sources the module avm/res/<module> by bicep-module-oci, its valuesMapping
// writing kind as the literal kind.
func moduleRecipe(name, component, module, kind string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: " + name + "\n  version: 1.0.0\n" +
		"spec:\n  capability: arm\n  implements: " + component + ":1.0.0\n  source:\n    kind: bicep-module-oci\n" +
		"    url: oci://mcr.microsoft.com/bicep/avm/res/" + module + "\n    version: 0.1.0\n" +
		"  valuesMapping: |\n    name: \"{{ .context.instance_id }}\"\n    kind: " + kind + "\n"
}

// armFormatOrFail returns the arm format, its index of Azure Verified Modules
// extended with the modules under avm/res/acme/ the fixtures source, each of
// the type Microsoft.Acme/<its name>.
func armFormatOrFail(t *testing.T) format {
	t.Helper()
	f, ok := formatNamed("arm")
	if !ok {
		t.Fatal("no format is named arm")
	}
	index := f.moduleType
	f.moduleType = func(module string) (string, bool) {
		if name, ok := strings.CutPrefix(module, "avm/res/acme/"); ok {
			return "Microsoft.Acme/" + name, true
		}
		return index(module)
	}
	return f
}

// TestTheMiniatureHubGeneratesItsRows generates the arm rows of the miniature
// hub and requires each as its Recipe writes it: the resource its fragment keys
// by the instance's symbol, with the kind it writes as its When, its whole
// values and infra, its overrides header and the identifier it publishes; the
// module a Recipe sources, with its parameters and the module output it
// publishes; the key and kind alone of a resource a library's fragment builds
// with dict; and the module the version of a Recipe an unversioned reference
// resolves to sources, read through a MappingLibrary entry that imports
// another. The reference realization writes no resource and gives no row; and
// two runs write the same bytes.
func TestTheMiniatureHubGeneratesItsRows(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	rows, notes, err := generateFromHub(miniatureHub, f, cloudimport.Table{})
	if err != nil {
		t.Fatalf("generate the miniature hub's table: %v (%v)", err, notes)
	}
	pred := func(op cloudimport.PredOp, value string, path ...string) []cloudimport.Pred {
		return []cloudimport.Pred{{Path: path, Op: op, Value: value}}
	}
	place := func(key string, from ...string) cloudimport.Placement {
		return cloudimport.Placement{From: from, Key: key}
	}
	want := []cloudimport.Row{
		{Key: "Microsoft.Acme/app", Component: "core/compute/acme-app", Category: "compute", Recipe: "app-module",
			Traits: map[string]string{"component.aggrega.ai/type": "server"}, Form: cloudimport.FormAVM,
			Values: []cloudimport.Placement{place("display_name", "displayName")},
			Infra:  []cloudimport.Placement{place("image", "image"), place("port", "port")},
			Origin: "generated aggrega-catalog/recipe/arm/app-module/1.1.0/recipe.yaml"},
		{Key: "Microsoft.Acme/cache", Component: "core/datastore/acme-cache", Category: "datastore", Recipe: "cache-module",
			When: pred(cloudimport.OpEquals, "Standard", "kind"),
			Traits: map[string]string{"component.aggrega.ai/type": "stateful-server",
				"acme.aggrega.ai/engine": "memory"}, Form: cloudimport.FormAVM,
			Values: []cloudimport.Placement{place("eviction_policy", "evictionPolicy")},
			Infra:  []cloudimport.Placement{place("memory_gb", "memoryGB"), place("ha.replicas", "replicas")},
			Overrides: []cloudimport.Placement{place("sku_name", "skuName"), place("location", "location"),
				place("network.subnet_id", "networkConfig", "subnetResourceId"),
				place("default_access_policy_enabled", "accessPolicy", "enabled")},
			Provides: []cloudimport.Placement{place("host", "outputs", "hostName", "value")},
			Origin:   "generated aggrega-catalog/recipe/arm/cache-module/1.0.0/recipe.yaml"},
		{Key: "Microsoft.Acme/namespaces", Component: "core/messaging/acme-queue", Category: "messaging", Recipe: "queue-library",
			When:   pred(cloudimport.OpEquals, "Messaging", "kind"),
			Traits: map[string]string{"component.aggrega.ai/type": "resource"}, Form: cloudimport.FormRaw,
			Origin: "generated aggrega-catalog/recipe/arm/queue-library/1.0.0/recipe.yaml"},
		{Key: "Microsoft.Acme/stores", Component: "core/datastore/acme-store", Category: "datastore", Recipe: "store-inline",
			When: pred(cloudimport.OpEquals, "Premium", "kind"),
			Traits: map[string]string{"component.aggrega.ai/type": "stateful-server",
				"acme.aggrega.ai/engine": "kv"}, Form: cloudimport.FormRaw,
			Values: []cloudimport.Placement{place("tier", "properties", "tier"),
				place("label", "properties", "label")},
			Infra: []cloudimport.Placement{place("storage_size", "properties", "capacityGB"),
				place("zone", "properties", "zones", "0")},
			Overrides: []cloudimport.Placement{place("subnet_id", "properties", "network", "subnetId"),
				place("replicas", "properties", "replicaCount"), place("zones", "zones", "0")},
			Provides: []cloudimport.Placement{{Key: "id"}},
			Origin:   "generated aggrega-catalog/recipe/arm/store-inline/1.0.0/recipe.yaml"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("the miniature hub generated\n%#v\nwant\n%#v", rows, want)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "endpoint-reference") {
		t.Errorf("no note says why the reference realization gives no row: %v", notes)
	}
	first, err := render(rows, nil, "r", f)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	again, _, err := generateFromHub(miniatureHub, f, cloudimport.Table{})
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	second, err := render(again, nil, "r", f)
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two generations from one hub differ")
	}
}

// TestTheRenderedTableIsGoOfTheFormatsPackage renders rows and requires Go
// source of the format's package naming the revision, which parses.
func TestTheRenderedTableIsGoOfTheFormatsPackage(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	content, err := render([]cloudimport.Row{{Key: "Microsoft.Acme/stores", Component: "core/datastore/acme-store",
		Recipe: "store-inline", When: []cloudimport.Pred{{Path: []string{"kind"}, Op: cloudimport.OpEquals,
			Value: "Premium"}}, Form: cloudimport.FormRaw, Provides: []cloudimport.Placement{{Key: "id"}},
		Traits: map[string]string{"b": "2", "a": "1"}, Origin: "generated x"}},
		[]cloudimport.ImageRow{{Repository: "ghcr.io/acme/product", Component: "core/compute/acme-app",
			Recipe: "acme-product", TagKey: "image_tag"}}, "abc123", f)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "table_gen.go", content, parser.ParseComments)
	if err != nil {
		t.Fatalf("the rendered table does not parse: %v\n%s", err, content)
	}
	if parsed.Name.Name != f.pkg {
		t.Errorf("the rendered table is of package %s, want %s", parsed.Name.Name, f.pkg)
	}
	for _, want := range []string{`const TableRevision = "abc123"`, "DO NOT EDIT",
		`"a": "1",` + "\n\t\t\t\"b\": \"2\",", "var generatedImages = []cloudimport.ImageRow{",
		`{Repository: "ghcr.io/acme/product", Component: "core/compute/acme-app", Recipe: "acme-product", TagKey: "image_tag"},`} {
		if !bytes.Contains(content, []byte(want)) {
			t.Errorf("the rendered table does not hold %q:\n%s", want, content)
		}
	}
}

// TestAnUnresolvableImportIsRefused generates from the miniature hub with a
// Recipe whose valuesMapping imports a MappingLibrary the hub does not hold,
// and requires the refusal to name the Recipe and the reference and to carry
// the library import's own condition, with no rows — whether or not the hand
// table lists the Recipe in notImported, since a build reaching it would halt
// there too.
func TestAnUnresolvableImportIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	copyTree(t, miniatureHub, root)
	copyTree(t, "testdata/unresolvable-import", root)
	for _, hand := range []cloudimport.Table{{}, {NotImported: []cloudimport.NotImported{{Recipe: "broken-import",
		Reason: "r"}}}} {
		rows, _, err := generateFromHub(root, armFormatOrFail(t), hand)
		if err == nil || rows != nil {
			t.Fatalf("a Recipe importing an absent library, notImported %v, generated %v, %v; want a refusal",
				hand.NotImported, rows, err)
		}
		if !errors.Is(err, libraryimport.ErrMappingLibraryNotFound()) {
			t.Errorf("the refusal does not carry the absent library's condition: %v", err)
		}
		for _, want := range []string{"arm/broken-import:1.0.0", "AcmeAbsentParameters#/mappings/App"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
	}
}

// TestAFileImportIsReadAsTheBuildReadsIt generates from the miniature hub with
// a module Recipe whose valuesMapping, and a Recipe whose fragment, are files
// beside it, and requires the rows each file's text gives: the catalog's load
// anchors the path to the Recipe's directory, as a build reads it. A file
// import naming no file is refused, naming the Recipe.
func TestAFileImportIsReadAsTheBuildReadsIt(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	recipe := func(name, component, body string) string {
		return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: " + name + "\n  version: 1.0.0\n" +
			"spec:\n  capability: arm\n  implements: " + component + ":1.0.0\n" + body
	}
	dir := "aggrega-catalog/recipe/arm/"
	root := hubWith(t, map[string]string{
		dir + "file-module/1.0.0/recipe.yaml": recipe("file-module", "core/compute/acme-legacy-app",
			"  source:\n    kind: bicep-module-oci\n    url: oci://mcr.microsoft.com/bicep/avm/res/acme/file-app\n"+
				"    version: 0.1.0\n  valuesMapping:\n    $import: ./mapping.tmpl\n"),
		dir + "file-module/1.0.0/mapping.tmpl": "name: \"{{ .context.instance_id }}\"\nlogin: \"{{ .values.user }}\"\n",
		dir + "file-fragment/1.0.0/recipe.yaml": recipe("file-fragment", "core/ai-model/acme-endpoint",
			"  artifacts:\n    vault.yaml:\n      $import: ./vault.yaml\n"),
		dir + "file-fragment/1.0.0/vault.yaml": "resources:\n  {{ capitalizedWords .context.instance_id }}:\n" +
			"    type: Microsoft.Acme/vaults\n    kind: Standard\n",
	})
	rows, notes, err := generateFromHub(root, f, cloudimport.Table{})
	if err != nil {
		t.Fatalf("generate: %v (%v)", err, notes)
	}
	got := map[string]cloudimport.Row{}
	for _, r := range rows {
		got[r.Recipe] = r
	}
	if r := got["file-module"]; r.Key != "Microsoft.Acme/file-app" || !reflect.DeepEqual(r.Values,
		[]cloudimport.Placement{{From: []string{"login"}, Key: "user"}}) {
		t.Errorf("the module Recipe whose valuesMapping is a file gave %#v", r)
	}
	if r := got["file-fragment"]; r.Key != "Microsoft.Acme/vaults" || len(r.When) != 1 || r.When[0].Value != "Standard" {
		t.Errorf("the Recipe whose fragment is a file gave %#v", r)
	}

	if err := os.Remove(filepath.Join(root, filepath.FromSlash(dir+"file-fragment/1.0.0/vault.yaml"))); err != nil {
		t.Fatal(err)
	}
	rows, _, err = generateFromHub(root, f, cloudimport.Table{})
	var refused *importError
	if !errors.As(err, &refused) || rows != nil || !strings.Contains(err.Error(), "arm/file-fragment:1.0.0") {
		t.Errorf("a fragment file naming no file gave %v, %v; want a refusal naming the Recipe", rows, err)
	}
}

// TestTwoRecipesClaimingOneKeyAndWhenAreRefused generates from the miniature
// hub with a second Recipe sourcing the cache module with the same kind for
// another Component, and requires a refusal naming the key, both Recipes and
// both Components, with no rows. Listing one in the hand file's notImported
// generates the other's row alone; a hand row naming one generates the
// other's alone; hand rows naming both generate neither.
func TestTwoRecipesClaimingOneKeyAndWhenAreRefused(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	root := hubWith(t, map[string]string{"aggrega-catalog/recipe/arm/cache-legacy/1.0.0/recipe.yaml": moduleRecipe(
		"cache-legacy", "core/compute/acme-legacy-app", "acme/cache", "Standard")})
	rows, _, err := generateFromHub(root, f, cloudimport.Table{})
	var conflict *claimConflict
	if !errors.As(err, &conflict) || rows != nil {
		t.Fatalf("two Recipes claiming one key and When generated %v, %v; want a claim conflict", rows, err)
	}
	for _, want := range []string{"Microsoft.Acme/cache", `kind equals "Standard"`, "cache-module", "core/datastore/acme-cache",
		"cache-legacy", "core/compute/acme-legacy-app", "notImported"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	recipesOf := func(rows []cloudimport.Row) []string {
		var out []string
		for _, r := range rows {
			if r.Key == "Microsoft.Acme/cache" {
				out = append(out, r.Recipe)
			}
		}
		return out
	}
	for _, c := range []struct {
		name string
		hand cloudimport.Table
		want []string
	}{
		{"one not imported", cloudimport.Table{NotImported: []cloudimport.NotImported{{Recipe: "cache-legacy",
			Reason: "r"}}}, []string{"cache-module"}},
		{"one by a hand row", cloudimport.Table{Hand: []cloudimport.Row{{Key: "Microsoft.Acme/caches",
			Recipe: "cache-module"}}}, []string{"cache-legacy"}},
		{"both by hand rows", cloudimport.Table{Hand: []cloudimport.Row{{Key: "Microsoft.Acme/caches",
			Recipe: "cache-module"}, {Key: "Microsoft.Acme/caches", Recipe: "cache-legacy",
			When: []cloudimport.Pred{{Path: []string{"sku"}, Op: cloudimport.OpPresent}}}}}, nil},
	} {
		rows, notes, err := generateFromHub(root, f, c.hand)
		if err != nil {
			t.Errorf("%s: generate: %v (%v)", c.name, err, notes)
			continue
		}
		if got := recipesOf(rows); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: the cache module's rows name %v, want %v", c.name, got, c.want)
		}
	}
}

// TestARecipeTheHandFileListsAsNotImportedGeneratesNothing generates from the
// miniature hub with the store Recipe listed in the hand file's notImported,
// and requires no row naming it, the rest generated as before.
func TestARecipeTheHandFileListsAsNotImportedGeneratesNothing(t *testing.T) {
	t.Parallel()
	rows, _, err := generateFromHub(miniatureHub, armFormatOrFail(t), cloudimport.Table{
		NotImported: []cloudimport.NotImported{{Recipe: "store-inline", Reason: "r"}}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var recipes []string
	for _, r := range rows {
		recipes = append(recipes, r.Recipe)
	}
	if want := []string{"app-module", "cache-module", "queue-library"}; !reflect.DeepEqual(recipes, want) {
		t.Errorf("the rows name the Recipes %v, want %v", recipes, want)
	}
}

// TestARowWhoseComponentTheHubDoesNotHoldIsRefused generates from the miniature
// hub with a Recipe implementing a Component the hub holds no version of, and
// requires a refusal naming the Recipe and the Component, with no rows.
func TestARowWhoseComponentTheHubDoesNotHoldIsRefused(t *testing.T) {
	t.Parallel()
	root := hubWith(t, map[string]string{"aggrega-catalog/recipe/arm/orphan/1.0.0/recipe.yaml": moduleRecipe(
		"orphan", "core/datastore/acme-absent", "acme/orphan", "Standard")})
	rows, _, err := generateFromHub(root, armFormatOrFail(t), cloudimport.Table{})
	var missing *missingComponent
	if !errors.As(err, &missing) || rows != nil {
		t.Fatalf("a Recipe implementing an absent Component generated %v, %v; want a refusal", rows, err)
	}
	for _, want := range []string{"orphan", "core/datastore/acme-absent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// git runs git in dir, failing the test where it fails.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestTheRevisionIsACommitHoldingWhatTheTableReads commits the miniature hub to
// a repository of its own and requires the revision to be its HEAD; then a
// change under a path the generator reads, committed or not, and an untracked
// file there, each refuse the revision, and a change elsewhere does not.
func TestTheRevisionIsACommitHoldingWhatTheTableReads(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	root := t.TempDir()
	copyTree(t, miniatureHub, root)
	git(t, root, "init", "-q")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "hub")
	head := git(t, root, "rev-parse", "HEAD")
	if got, err := hubRevision(root, f); err != nil || got != head {
		t.Fatalf("the clean checkout's revision is %q, %v; want %s", got, err, head)
	}

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := hubRevision(root, f); err != nil || got != head {
		t.Errorf("a change outside what the table reads gave %q, %v; want %s", got, err, head)
	}

	recipe := filepath.Join(root, "aggrega-catalog", "recipe", "arm", "store-inline", "1.0.0", "recipe.yaml")
	body, err := os.ReadFile(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipe, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := hubRevision(root, f); err == nil || !strings.Contains(err.Error(), "-revision") {
		t.Errorf("an uncommitted change to an arm Recipe gave %q, %v; want a refusal naming -revision", got, err)
	}
	if err := os.WriteFile(recipe, body, 0o644); err != nil {
		t.Fatal(err)
	}

	untracked := filepath.Join(root, "aggrega-catalog", "mappinglibrary", "new", "1.0.0", "library.yaml")
	if err := os.MkdirAll(filepath.Dir(untracked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untracked, []byte("kind: MappingLibrary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := hubRevision(root, f); err == nil {
		t.Errorf("an untracked library gave %q; want a refusal", got)
	}
}

// TestTheRevisionIsTheLastCommitThatChangedWhatTheTableReads commits the
// miniature hub, then a file the generator does not read, then a change to a
// Recipe it reads, and requires the revision to stay the first commit across
// the second and to become the third, though the checkout's HEAD is the
// second's or the third's.
func TestTheRevisionIsTheLastCommitThatChangedWhatTheTableReads(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	root := t.TempDir()
	copyTree(t, miniatureHub, root)
	git(t, root, "init", "-q")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "hub")
	first := git(t, root, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "-q", "-m", "elsewhere")
	if head := git(t, root, "rev-parse", "HEAD"); head == first {
		t.Fatal("the second commit did not move HEAD")
	}
	if got, err := hubRevision(root, f); err != nil || got != first {
		t.Errorf("after a commit outside what the table reads the revision is %q, %v; want %s", got, err, first)
	}

	recipe := filepath.Join(root, "aggrega-catalog", "recipe", "arm", "store-inline", "1.0.0", "recipe.yaml")
	body, err := os.ReadFile(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipe, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", recipe)
	git(t, root, "commit", "-q", "-m", "recipe")
	third := git(t, root, "rev-parse", "HEAD")
	if got, err := hubRevision(root, f); err != nil || got != third {
		t.Errorf("after a commit to an arm Recipe the revision is %q, %v; want %s", got, err, third)
	}
	if got, err := lastReadCommit(root, f); err != nil || got != third {
		t.Errorf("the last commit that changed what the table reads is %q, %v; want %s", got, err, third)
	}
}

// TestAHandRowWrittenWholeStandsInForItsRecipesClaim generates no row for a
// Recipe a hand row of another key and When names, the hand row standing in
// its place, and keeps the generated row a hand row of its own key and When
// merges into.
func TestAHandRowWrittenWholeStandsInForItsRecipesClaim(t *testing.T) {
	t.Parallel()
	f := armFormatOrFail(t)
	root := hubWith(t, nil)
	rows, notes, err := generateFromHub(root, f, cloudimport.Table{})
	if err != nil {
		t.Fatalf("generate: %v (%v)", err, notes)
	}
	var claim cloudimport.Row
	for _, r := range rows {
		if r.Recipe == "cache-module" {
			claim = r
		}
	}
	if claim.Recipe == "" {
		t.Fatalf("the miniature hub generates no row of cache-module: %v", rows)
	}
	has := func(rows []cloudimport.Row) bool {
		return slices.ContainsFunc(rows, func(r cloudimport.Row) bool { return r.Recipe == "cache-module" })
	}

	whole := cloudimport.Row{Key: claim.Key, Recipe: "cache-module", Component: claim.Component,
		When: []cloudimport.Pred{{Path: []string{"sku", "name"}, Op: cloudimport.OpContains, Value: "Premium"}}}
	rows, notes, err = generateFromHub(root, f, cloudimport.Table{Hand: []cloudimport.Row{whole}})
	if err != nil {
		t.Fatalf("generate with a whole hand row: %v (%v)", err, notes)
	}
	if has(rows) {
		t.Errorf("a hand row of another When naming cache-module leaves its generated row: %v", rows)
	}

	merged := cloudimport.Row{Key: claim.Key, When: claim.When, Recipe: "cache-module"}
	rows, notes, err = generateFromHub(root, f, cloudimport.Table{Hand: []cloudimport.Row{merged, whole}})
	if err != nil {
		t.Fatalf("generate with a merging hand row: %v (%v)", err, notes)
	}
	if !has(rows) {
		t.Errorf("a hand row of the claim's own key and When drops the generated row it merges into: %v", rows)
	}
}
