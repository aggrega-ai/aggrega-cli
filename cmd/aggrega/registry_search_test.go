// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fakeRepoLister is an in-memory registryRepoLister: it hands the search
// primitive a fixed repository set in one page, so search runs without a
// network. It mirrors the memory-target wrappers the tag-listing tests use.
type fakeRepoLister struct {
	repos []string
	err   error
}

func (f fakeRepoLister) Repositories(_ context.Context, _ string, fn func(repos []string) error) error {
	if f.err != nil {
		return f.err
	}
	return fn(f.repos)
}

// searchFixtureRepos spans every catalog kind plus two plugin repositories, so a
// scope filter's effect is observable: the plugin scope keeps only the wasm/
// paths, the catalog scope keeps them all.
var searchFixtureRepos = []string{
	"recipe/helm/api-server",
	"wasm/cli/moo",
	"component/core/api-server",
	"feature/core/tls",
	"wasm/provider/helm",
}

// withSearchTarget points the registry-search target seam at a fake lister for
// the duration of the test, so runRegistrySearch runs without a network.
func withSearchTarget(t *testing.T, reg registryRepoLister, err error) {
	t.Helper()
	prev := registrySearchTarget
	registrySearchTarget = func(workspaceconfig.Config) (registryRepoLister, string, error) {
		if err != nil {
			return nil, "hub.aggrega.ai", err
		}
		return reg, "hub.aggrega.ai", nil
	}
	t.Cleanup(func() { registrySearchTarget = prev })
}

// TestSearchRegistry_ScopesAndQuery proves the one search primitive filters by
// scope and by case-insensitive substring query, and returns matches in
// ascending order.
func TestSearchRegistry_ScopesAndQuery(t *testing.T) {
	reg := fakeRepoLister{repos: searchFixtureRepos}
	ctx := context.Background()

	pluginAll, err := searchRegistry(ctx, reg, "", pluginSearchScope)
	if err != nil {
		t.Fatalf("plugin scope search: %v", err)
	}
	wantPlugin := []string{"wasm/cli/moo", "wasm/provider/helm"}
	if strings.Join(pluginAll, ",") != strings.Join(wantPlugin, ",") {
		t.Fatalf("plugin scope = %v; want %v", pluginAll, wantPlugin)
	}

	catalogAll, err := searchRegistry(ctx, reg, "", catalogSearchScope)
	if err != nil {
		t.Fatalf("catalog scope search: %v", err)
	}
	if len(catalogAll) != len(searchFixtureRepos) {
		t.Fatalf("catalog scope kept %d repos; want all %d: %v", len(catalogAll), len(searchFixtureRepos), catalogAll)
	}
	// Distinct scopes over the same registry: the catalog scope is a strict
	// superset of the plugin scope, proving the two searches differ only in scope.
	if len(catalogAll) <= len(pluginAll) {
		t.Fatalf("catalog scope (%d) should be broader than plugin scope (%d)", len(catalogAll), len(pluginAll))
	}

	moo, err := searchRegistry(ctx, reg, "MOO", pluginSearchScope)
	if err != nil {
		t.Fatalf("query search: %v", err)
	}
	if len(moo) != 1 || moo[0] != "wasm/cli/moo" {
		t.Fatalf("case-insensitive query = %v; want [wasm/cli/moo]", moo)
	}

	core, err := searchRegistry(ctx, reg, "core", catalogSearchScope)
	if err != nil {
		t.Fatalf("catalog query search: %v", err)
	}
	wantCore := []string{"component/core/api-server", "feature/core/tls"}
	if strings.Join(core, ",") != strings.Join(wantCore, ",") {
		t.Fatalf("catalog query = %v; want %v", core, wantCore)
	}
}

// TestRunRegistrySearch_SharedPrimitiveTwoScopes proves both search commands'
// shared entry point routes through the one primitive with distinct scopes: the
// same fake registry yields the plugin subset under the plugin scope and the
// full set under the catalog scope.
func TestRunRegistrySearch_SharedPrimitiveTwoScopes(t *testing.T) {
	withSearchTarget(t, fakeRepoLister{repos: searchFixtureRepos}, nil)
	ctx := context.Background()
	cfg := workspaceconfig.Config{DefaultRegistry: "hub.aggrega.ai"}

	var pluginOut bytes.Buffer
	if err := runRegistrySearch(ctx, cfg, "", pluginSearchScope, &pluginOut); err != nil {
		t.Fatalf("plugin search: %v", err)
	}
	if !strings.Contains(pluginOut.String(), "wasm/cli/moo") ||
		strings.Contains(pluginOut.String(), "component/core/api-server") {
		t.Fatalf("plugin search scope leaked non-plugin repos:\n%s", pluginOut.String())
	}

	var catalogOut bytes.Buffer
	if err := runRegistrySearch(ctx, cfg, "", catalogSearchScope, &catalogOut); err != nil {
		t.Fatalf("catalog search: %v", err)
	}
	if !strings.Contains(catalogOut.String(), "component/core/api-server") ||
		!strings.Contains(catalogOut.String(), "wasm/cli/moo") {
		t.Fatalf("catalog search scope missing catalog repos:\n%s", catalogOut.String())
	}
}

// TestFormatSearchResults_Empty proves a zero-match search reports the empty
// result explicitly rather than printing a bare blank.
func TestFormatSearchResults_Empty(t *testing.T) {
	var out bytes.Buffer
	formatSearchResults(&out, "hub.aggrega.ai", "nope", nil)
	if !strings.Contains(out.String(), "(none)") {
		t.Fatalf("empty search should report (none):\n%s", out.String())
	}
}

// TestDiscoverableViaSearch proves the unknown-command hint matches a plugin
// repository whose final segment equals the name, and degrades to false on a
// search error rather than propagating it.
func TestDiscoverableViaSearch(t *testing.T) {
	ctx := context.Background()
	reg := fakeRepoLister{repos: searchFixtureRepos}
	if !discoverableViaSearch(ctx, reg, "moo") {
		t.Fatal("moo should be discoverable (wasm/cli/moo)")
	}
	if discoverableViaSearch(ctx, reg, "not-a-plugin") {
		t.Fatal("an absent name should not be discoverable")
	}
	// A registry failure degrades to "not discoverable", never an error.
	if discoverableViaSearch(ctx, fakeRepoLister{err: context.Canceled}, "moo") {
		t.Fatal("a search error should yield false, not a match")
	}
}
