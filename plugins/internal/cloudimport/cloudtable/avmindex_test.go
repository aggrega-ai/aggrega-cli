// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"errors"
	gofmt "go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// indexCommit is a commit name the tests write into a generated index.
const indexCommit = "0123456789abcdef0123456789abcdef01234567"

// indexHeader is the columns of a module index file, in the order Microsoft's
// file writes the ones the generator reads, with columns it does not between.
const indexHeader = "ProviderNamespace,ResourceType,ModuleDisplayName,AlternativeNames,ModuleName,ParentModule," +
	"ModuleStatus,TelemetryIdPrefix\n"

// indexCSV is a module index file: rows out of prefix order, a module with a
// type of two segments, a module with no telemetry prefix, a pattern module
// and a quoted display name holding a comma.
const indexCSV = indexHeader +
	"Microsoft.Web,sites,\"Web, App\",,avm/res/web/site,n/a,Available,46d3xbcp.res.web-site-bbbb\n" +
	"Microsoft.ApiManagement,service/apis,API,,avm/res/api-management/service/api,n/a,Available,46d3xbcp.res.apimanagement-api-aaaa\n" +
	"Microsoft.App,containerApps,Container App,,avm/res/app/container-app,n/a,Available,\n" +
	",,Landing zone,,avm/ptn/lz/sub-vending,n/a,Available,46d3xbcp.ptn.lz-subvending-cccc\n" +
	"Microsoft.Cache,redisEnterprise,Redis,,avm/res/cache/redis-enterprise,n/a,Available,46d3xbcp.res.cache-redisenterprise-dddd\n"

// readIndexOrFail reads text as a module index file.
func readIndexOrFail(t *testing.T, text string) []armimport.AVMModule {
	t.Helper()
	mods, err := readAVMIndex(strings.NewReader(text))
	if err != nil {
		t.Fatalf("read the module index: %v", err)
	}
	return mods
}

// TestAVMIndexHoldsOneRowPerResourcePrefixOrderedByPrefix reads a module index
// file and requires one row for each module whose telemetry prefix is a
// resource module's, in prefix order, with the namespace and the type as the
// file writes them; a module with no prefix and a pattern module give none.
func TestAVMIndexHoldsOneRowPerResourcePrefixOrderedByPrefix(t *testing.T) {
	t.Parallel()
	got := readIndexOrFail(t, indexCSV)
	want := []armimport.AVMModule{
		{Prefix: "46d3xbcp.res.apimanagement-api-aaaa", Module: "avm/res/api-management/service/api",
			Namespace: "Microsoft.ApiManagement", Type: "service/apis"},
		{Prefix: "46d3xbcp.res.cache-redisenterprise-dddd", Module: "avm/res/cache/redis-enterprise",
			Namespace: "Microsoft.Cache", Type: "redisEnterprise"},
		{Prefix: "46d3xbcp.res.web-site-bbbb", Module: "avm/res/web/site", Namespace: "Microsoft.Web", Type: "sites"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the index reads as\n%#v\nwant\n%#v", got, want)
	}
}

// TestAVMIndexIsRefusedWhereItsFileIsFaulty gives the generator a file with
// one fault each and requires the refusal to name that fault.
func TestAVMIndexIsRefusedWhereItsFileIsFaulty(t *testing.T) {
	t.Parallel()
	const row = "Microsoft.Web,sites,Web,,avm/res/web/site,n/a,Available,46d3xbcp.res.web-site-bbbb\n"
	for _, c := range []struct{ name, text, want string }{
		{"a missing column", strings.Replace(indexHeader, "ModuleName", "Name", 1) + row, "ModuleName"},
		{"a repeated prefix", indexHeader + row + strings.Replace(row, "avm/res/web/site", "avm/res/web/other", 1),
			"46d3xbcp.res.web-site-bbbb"},
		{"a repeated module", indexHeader + row + strings.Replace(row, "web-site-bbbb", "web-site-cccc", 1),
			"avm/res/web/site"},
		{"no module", indexHeader + strings.Replace(row, "avm/res/web/site", "", 1), "46d3xbcp.res.web-site-bbbb"},
		{"no type", indexHeader + strings.Replace(row, "sites", "", 1), "46d3xbcp.res.web-site-bbbb"},
		{"a short row", indexHeader + "Microsoft.Web,sites\n", "line 2"},
		{"an empty file", "", "ProviderNamespace"},
	} {
		_, err := readAVMIndex(strings.NewReader(c.text))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: read as %v; want a refusal naming %q", c.name, err, c.want)
		}
	}
}

// TestAVMIndexIsWrittenAsGoWithItsNotice renders an index and requires Go
// source that parses, is formatted, names the repository, the file, the commit
// and the command that regenerates it, carries the MIT notice with its
// copyright holder, and is the same bytes each time.
func TestAVMIndexIsWrittenAsGoWithItsNotice(t *testing.T) {
	t.Parallel()
	mods := readIndexOrFail(t, indexCSV)
	src, err := renderAVMIndex(mods, indexCommit)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	again, err := renderAVMIndex(mods, indexCommit)
	if err != nil || string(again) != string(src) {
		t.Errorf("two renderings differ (%v)", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "avmindex_gen.go", src, parser.ParseComments); err != nil {
		t.Fatalf("the rendering is not Go: %v\n%s", err, src)
	}
	if formatted, err := gofmt.Source(src); err != nil || string(formatted) != string(src) {
		t.Errorf("the rendering is not formatted as gofmt formats it (%v)", err)
	}
	for _, want := range []string{"https://github.com/Azure/Azure-Verified-Modules", avmIndexFile, indexCommit,
		"Copyright (c) Microsoft Corporation", "Permission is hereby granted, free of charge", "MIT License",
		"go run ./plugins/internal/cloudimport/cloudtable -avm-index", "-avm-commit", "package armimport",
		`{Prefix: "46d3xbcp.res.web-site-bbbb", Module: "avm/res/web/site", Namespace: "Microsoft.Web", Type: "sites"}`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("the rendering does not hold %q:\n%s", want, src)
		}
	}
}

// TestAVMIndexIsWrittenFromAFileAndACommit runs the generator over a file on
// disk and requires the file it writes to be the rendering, and a commit that
// is not a full commit name, or a file that is absent, to be refused with
// nothing written.
func TestAVMIndexIsWrittenFromAFileAndACommit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	csv, out := filepath.Join(dir, "modules.csv"), filepath.Join(dir, "avmindex_gen.go")
	if err := os.WriteFile(csv, []byte(indexCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runAVMIndex(csv, indexCommit, out); err != nil {
		t.Fatalf("generate: %v", err)
	}
	want, err := renderAVMIndex(readIndexOrFail(t, indexCSV), indexCommit)
	got, readErr := os.ReadFile(out)
	if err != nil || readErr != nil || string(got) != string(want) {
		t.Errorf("the generator wrote %q (%v), want the rendering (%v)", got, readErr, err)
	}
	for _, c := range []struct{ name, csv, commit, want string }{
		{"a branch name", csv, "main", "-avm-commit"},
		{"a short commit", csv, "0123456", "-avm-commit"},
		{"an absent file", filepath.Join(dir, "absent.csv"), indexCommit, "absent.csv"},
	} {
		out := filepath.Join(dir, "refused.go")
		err := runAVMIndex(c.csv, c.commit, out)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: generated with %v; want a refusal naming %q", c.name, err, c.want)
		}
		if _, statErr := os.Stat(out); statErr == nil {
			t.Errorf("%s: a file was written despite the refusal", c.name)
		}
	}
}

// unindexedModules returns, for each module an arm Recipe or FeatureRecipe of
// h sources that index does not hold, a line naming the Recipe and the module.
func unindexedModules(t *testing.T, h *hubCatalog, index func(string) (armimport.AVMModule, bool)) []string {
	t.Helper()
	var out []string
	for _, name := range h.recipeNames() {
		r, ok := h.recipe(name)
		if !ok {
			continue
		}
		src, err := h.read(r)
		if err != nil {
			t.Fatalf("read the Recipe %s: %v", name, err)
		}
		if src.source == nil {
			continue
		}
		if module, _ := avmModule(src.source); module != "" {
			if _, ok := index(module); !ok {
				out = append(out, "the Recipe arm/"+name+" sources "+module)
			}
		}
	}
	return out
}

// TestEveryArmRecipeModuleIsInTheAVMIndex requires every Azure Verified Modules
// resource module an arm Recipe of the hub sources to be in the index, the
// Recipes listed in notImported included, so a Recipe sourcing a module the
// index lacks fails here until the index is regenerated from a newer file.
//
// PLANTED DEFECT: a Recipe sourcing a module the index lacks — the case below
// reads the miniature hub, whose modules are the index's none — is named.
func TestEveryArmRecipeModuleIsInTheAVMIndex(t *testing.T) {
	t.Parallel()
	for _, line := range unindexedModules(t, loadHubOrFail(t, armFormatOrFail(t)), armimport.AVMModuleByPath) {
		t.Errorf("%s, which the index holds no row of: regenerate the index from a newer module index file with "+
			"`go run ./plugins/internal/cloudimport/cloudtable -avm-index FILE -avm-commit COMMIT -out "+
			"plugins/internal/armimport/avmindex_gen.go`", line)
	}
	mini, err := loadHub(miniatureHub, "arm")
	if err != nil {
		t.Fatalf("load the miniature hub: %v", err)
	}
	if planted := unindexedModules(t, mini, armimport.AVMModuleByPath); !strings.Contains(strings.Join(planted, "\n"),
		"the Recipe arm/cache-module sources avm/res/acme/cache") {
		t.Errorf("a Recipe sourcing a module the index lacks passes the check: %v", planted)
	}
}

// TestAModuleTheAVMIndexLacksIsRefused generates from the miniature hub, whose
// modules the index does not hold, and requires a refusal naming each Recipe
// and its module, with no rows.
func TestAModuleTheAVMIndexLacksIsRefused(t *testing.T) {
	t.Parallel()
	f, ok := formatNamed("arm")
	if !ok {
		t.Fatal("no format is named arm")
	}
	rows, _, err := generateFromHub(miniatureHub, f, cloudimport.Table{})
	var lacking *unindexedModule
	if !errors.As(err, &lacking) || rows != nil {
		t.Fatalf("generating from modules the index lacks gave %v, %v; want a refusal", rows, err)
	}
	for _, want := range []string{"app-module", "avm/res/acme/app", "cache-module", "avm/res/acme/cache"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}
