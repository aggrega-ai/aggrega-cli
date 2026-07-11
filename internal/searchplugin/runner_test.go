// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package searchplugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/searchplugin"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// bytesLookup returns a resolver seam that hands the runner the supplied guest
// bytes for any URI, with an empty ResolvedPlugin.URI so the bridge's
// best-effort provenance recording is skipped (no filesystem side effect). The
// bytes are in memory, so the whole resolve-load-query path runs offline.
func bytesLookup(b []byte) pluginpath.Lookup {
	return func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{Bytes: b}, nil
	}
}

// TestRunner_Describe loads the fixture search guest directly through the shared
// load gate and asserts its describe() self-report: it is a search plugin, it
// declares the search export, it stamps the index format/version it reads, and
// the search export declares no host functions — so the guest reaches no host
// data surface and answers every query from its embedded index alone, offline.
func TestRunner_Describe(t *testing.T) {
	ctx := context.Background()
	guest := wasmtest.EnsureFixtureBytes(t, "search-go.wasm")

	// nil FetchService: the guest is granted no external-input service, and the
	// build profile denies filesystem and network access — the capability-denied
	// sandbox every non-CLI socket loads under.
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{Name: "search-fixture", Bytes: guest})
	if err != nil {
		t.Fatalf("LoadAndDescribe under capability-denied sandbox: %v", err)
	}
	defer func() { _ = plugin.Close(ctx) }()

	if desc.PluginType != wasm.PluginTypeSearch {
		t.Fatalf("plugin_type = %q; want %q", desc.PluginType, wasm.PluginTypeSearch)
	}

	var searchExport *wasm.PluginExport
	for i := range desc.Exports {
		if desc.Exports[i].Name == wasm.SearchExportName {
			searchExport = &desc.Exports[i]
		}
	}
	if searchExport == nil {
		t.Fatalf("describe() declares no %q export; exports=%+v", wasm.SearchExportName, desc.Exports)
	}
	if len(searchExport.HostFunctions) != 0 {
		t.Fatalf("search export declares host functions %v; want none (offline: no host data surface)", searchExport.HostFunctions)
	}
	if desc.Description == "" {
		t.Fatalf("describe() reports an empty plugin description; want the index format/version stamp")
	}
	if want := "aggrega.search.index/v1"; !strings.Contains(desc.Description, want) {
		t.Fatalf("describe() description %q does not stamp the index format %q", desc.Description, want)
	}
}

// TestRunner_SearchDeterministic drives the fixture guest through the reusable
// Runner entry point twice with an identical query and asserts the ranked hits
// are byte-identical and in the expected order — the determinism a diff-based
// consumer relies on — with no network access (the resolver hands in-memory
// bytes and the guest loads under the capability-denied build profile).
func TestRunner_SearchDeterministic(t *testing.T) {
	ctx := context.Background()
	guest := wasmtest.EnsureFixtureBytes(t, "search-go.wasm")

	r := searchplugin.Runner{Resolve: bytesLookup(guest)}

	out1, err := r.Search(ctx, "test://search-fixture", abi.SearchInput{Query: "compiler"})
	if err != nil {
		t.Fatalf("first Search: %v", err)
	}
	out2, err := r.Search(ctx, "test://search-fixture", abi.SearchInput{Query: "compiler"})
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}

	// The query "compiler" matches two documents, both scoring one; the
	// tie-break is ascending document id, so the order is fixed.
	wantDocs := []string{"architecture/invariants", "architecture/pipeline"}
	if got := docIDs(out1); !equal(got, wantDocs) {
		t.Fatalf("ranked doc ids = %v; want %v", got, wantDocs)
	}
	for _, res := range out1.Results {
		if res.Score != 1 {
			t.Fatalf("result %q score = %v; want 1", res.DocID, res.Score)
		}
		if res.Title == "" || res.Anchor == "" || res.Snippet == "" {
			t.Fatalf("result %q missing display fields: %+v", res.DocID, res)
		}
	}

	// Byte-identical output across two runs of identical input.
	j1, _ := json.Marshal(out1)
	j2, _ := json.Marshal(out2)
	if string(j1) != string(j2) {
		t.Fatalf("non-deterministic output:\n run1: %s\n run2: %s", j1, j2)
	}
}

// TestRunner_SearchRanking asserts a multi-term query ranks by descending
// score with a document-id tie-break: "compiler render" scores the pipeline
// doc highest (both terms), then the two single-term matches ordered by id.
func TestRunner_SearchRanking(t *testing.T) {
	ctx := context.Background()
	guest := wasmtest.EnsureFixtureBytes(t, "search-go.wasm")

	r := searchplugin.Runner{Resolve: bytesLookup(guest)}
	out, err := r.Search(ctx, "test://search-fixture", abi.SearchInput{Query: "compiler render"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []string{"architecture/pipeline", "architecture/invariants", "language-reference/recipe"}
	if got := docIDs(out); !equal(got, want) {
		t.Fatalf("ranked doc ids = %v; want %v", got, want)
	}
	if out.Results[0].Score != 2 {
		t.Fatalf("top result score = %v; want 2", out.Results[0].Score)
	}
}

// TestRunner_NotSearchPlugin proves the generic type gate: a non-search guest
// loads and describes fine, but the runner refuses to answer a query with it.
func TestRunner_NotSearchPlugin(t *testing.T) {
	ctx := context.Background()
	guest := wasmtest.EnsureFixtureBytes(t, "mutator-go.wasm")

	r := searchplugin.Runner{Resolve: bytesLookup(guest)}
	_, err := r.Search(ctx, "test://mutator", abi.SearchInput{Query: "compiler"})
	if !errors.Is(err, searchplugin.ErrPluginNotSearch) {
		t.Fatalf("Search with a mutator guest: err = %v; want ErrPluginNotSearch", err)
	}
}

func docIDs(out abi.SearchOutput) []string {
	ids := make([]string, 0, len(out.Results))
	for _, r := range out.Results {
		ids = append(ids, r.DocID)
	}
	return ids
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
