// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

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
	"plugin/cli/moo",
	"component/core/api-server",
	"feature/core/tls",
	"plugin/provider/helm",
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
	wantPlugin := []string{"plugin/cli/moo", "plugin/provider/helm"}
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
	if len(moo) != 1 || moo[0] != "plugin/cli/moo" {
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
	if !strings.Contains(pluginOut.String(), "plugin/cli/moo") ||
		strings.Contains(pluginOut.String(), "component/core/api-server") {
		t.Fatalf("plugin search scope leaked non-plugin repos:\n%s", pluginOut.String())
	}

	var catalogOut bytes.Buffer
	if err := runRegistrySearch(ctx, cfg, "", catalogSearchScope, &catalogOut); err != nil {
		t.Fatalf("catalog search: %v", err)
	}
	if !strings.Contains(catalogOut.String(), "component/core/api-server") ||
		!strings.Contains(catalogOut.String(), "plugin/cli/moo") {
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

// newCatalogRegistry builds a real oras remote registry pointed at a local
// httptest server, mirroring the plain-HTTP catalog wiring oras's own registry
// tests use. It proves the search primitive against the real enumeration path,
// not just a fake lister.
func newCatalogRegistry(t *testing.T, ts *httptest.Server) *remote.Registry {
	t.Helper()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	reg, err := remote.NewRegistry(uri.Host)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	reg.PlainHTTP = true
	return reg
}

// TestSearchRegistry_MalformedLinkHeaderReturnsFirstPage proves that when the
// registry serves a bracket-less (non-compliant) pagination header, search
// returns the first page's repositories it already collected rather than
// hard-failing with nothing. It exercises the real oras enumeration path.
func TestSearchRegistry_MalformedLinkHeaderReturnsFirstPage(t *testing.T) {
	firstPage := []string{"component/core/api-server", "plugin/cli/moo"}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/_catalog" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// A bracket-less Link header: RFC-8288 requires the URI-Reference be
		// wrapped in angle brackets, so oras rejects this after delivering the
		// page. This reproduces the non-compliant real-world registry header.
		w.Header().Set("Link", fmt.Sprintf("%s/v2/_catalog?n=100&last=plugin%%2Fcli%%2Fmoo; rel=next", ts.URL))
		if err := json.NewEncoder(w).Encode(struct {
			Repositories []string `json:"repositories"`
		}{Repositories: firstPage}); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()

	reg := newCatalogRegistry(t, ts)
	got, err := searchRegistry(context.Background(), reg, "", catalogSearchScope)
	if err != nil {
		t.Fatalf("malformed Link header should degrade to first page, got error: %v", err)
	}
	want := []string{"component/core/api-server", "plugin/cli/moo"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("first-page fallback = %v; want %v", got, want)
	}
}

// TestSearchRegistry_CompliantMultiPagePagesFully proves the tolerance does not
// short-circuit compliant pagination: a well-formed multi-page Link chain still
// pages through every page and returns the union, in sorted order.
func TestSearchRegistry_CompliantMultiPagePagesFully(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/_catalog" {
			t.Errorf("unexpected access: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var repos []string
		switch r.URL.Query().Get("page") {
		case "two":
			repos = []string{"plugin/cli/moo", "recipe/helm/api-server"}
		default:
			repos = []string{"component/core/api-server", "feature/core/tls"}
			w.Header().Set("Link", fmt.Sprintf(`<%s/v2/_catalog?page=two>; rel="next"`, ts.URL))
		}
		if err := json.NewEncoder(w).Encode(struct {
			Repositories []string `json:"repositories"`
		}{Repositories: repos}); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	}))
	defer ts.Close()

	reg := newCatalogRegistry(t, ts)
	got, err := searchRegistry(context.Background(), reg, "", catalogSearchScope)
	if err != nil {
		t.Fatalf("compliant multi-page search: %v", err)
	}
	want := []string{
		"component/core/api-server",
		"feature/core/tls",
		"plugin/cli/moo",
		"recipe/helm/api-server",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("multi-page union = %v; want %v", got, want)
	}
}

// TestSearchRegistry_FirstRequestFailurePropagates proves a failure before any
// page is delivered (here, a connection failure — the callback is never
// invoked) still surfaces as an error rather than a silent empty result — the
// fallback covers only post-first-page failures.
func TestSearchRegistry_FirstRequestFailurePropagates(t *testing.T) {
	// Stand a server up only to reserve a reachable URL, then close it so the
	// first catalog request fails to connect before any page is delivered.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	reg := newCatalogRegistry(t, ts)
	ts.Close()

	got, err := searchRegistry(context.Background(), reg, "", catalogSearchScope)
	if err == nil {
		t.Fatalf("a first-request failure should propagate; got matches %v, nil error", got)
	}
	if got != nil {
		t.Fatalf("a first-request failure should return no matches; got %v", got)
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
