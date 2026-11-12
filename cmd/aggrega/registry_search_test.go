// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fakeRepoLister is an in-memory registryops.RepoLister: it hands the enumeration
// primitive a fixed repository set in one page, so an enumeration runs without a
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
// scope's or a query's effect on an enumeration is observable.
var searchFixtureRepos = []string{
	"recipe/helm/api-server",
	"plugin/cli/moo",
	"component/core/api-server",
	"feature/core/tls",
	"plugin/provider/helm",
}

// withSearchTarget points the registry-enumeration target seam at a fake lister
// for the duration of the test, so a plugin addition runs without a network.
//
// It points the tag-listing seam at a registry that lists no tag for any
// repository as well, because an addition looks a plugin up by its name with a tag
// listing before it walks the catalog: left live, that listing would reach a real
// registry. A test that needs published versions sets them with withTagLister,
// after this call.
func withSearchTarget(t *testing.T, reg registryops.RepoLister, err error) {
	t.Helper()
	prev := pluginops.RegistryTarget
	pluginops.RegistryTarget = func(workspaceconfig.Config) (registryops.RepoLister, string, error) {
		if err != nil {
			return nil, "hub.aggrega.ai", err
		}
		return reg, "hub.aggrega.ai", nil
	}
	prevTags := pluginops.RepoTagLister
	pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		return nil, nil
	}
	t.Cleanup(func() { pluginops.RepoTagLister = prevTags })
	// Disable the live deprecation probe so a search stays network-free: the fake
	// target's host would otherwise trigger a per-result registry probe.
	prevLookup := newRepoDeprecationLookup
	newRepoDeprecationLookup = func(*workspaceconfig.ResolvedWorkspace, string) repoDeprecationLookup { return nil }
	t.Cleanup(func() {
		pluginops.RegistryTarget = prev
		newRepoDeprecationLookup = prevLookup
	})
}

// TestEnumerateRepositories_ScopeAndQuery proves the one enumeration primitive
// keeps every repository the catalog scope admits, filters by case-insensitive
// substring query, and returns matches in ascending order.
func TestEnumerateRepositories_ScopeAndQuery(t *testing.T) {
	reg := fakeRepoLister{repos: searchFixtureRepos}
	ctx := context.Background()

	catalogAll, err := registryops.EnumerateRepositories(ctx, reg, "", registryops.CatalogScope)
	if err != nil {
		t.Fatalf("catalog scope enumeration: %v", err)
	}
	wantAll := []string{
		"component/core/api-server",
		"feature/core/tls",
		"plugin/cli/moo",
		"plugin/provider/helm",
		"recipe/helm/api-server",
	}
	if strings.Join(catalogAll, ",") != strings.Join(wantAll, ",") {
		t.Fatalf("catalog scope = %v; want %v", catalogAll, wantAll)
	}

	moo, err := registryops.EnumerateRepositories(ctx, reg, "MOO", registryops.CatalogScope)
	if err != nil {
		t.Fatalf("query enumeration: %v", err)
	}
	if len(moo) != 1 || moo[0] != "plugin/cli/moo" {
		t.Fatalf("case-insensitive query = %v; want [plugin/cli/moo]", moo)
	}

	core, err := registryops.EnumerateRepositories(ctx, reg, "core", registryops.CatalogScope)
	if err != nil {
		t.Fatalf("catalog query enumeration: %v", err)
	}
	wantCore := []string{"component/core/api-server", "feature/core/tls"}
	if strings.Join(core, ",") != strings.Join(wantCore, ",") {
		t.Fatalf("catalog query = %v; want %v", core, wantCore)
	}
}

// newCatalogRegistry builds a real oras remote registry pointed at a local
// httptest server, mirroring the plain-HTTP catalog wiring oras's own registry
// tests use. It proves the enumeration primitive against the real enumeration
// path, not just a fake lister.
func newCatalogRegistry(t *testing.T, ts *httptest.Server) *remote.Registry {
	t.Helper()
	uri, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("invalid test http server: %v", err)
	}
	return newCatalogRegistryAt(t, uri.Host)
}

// newCatalogRegistryAt wires the same plain-HTTP registry against an arbitrary
// authority, for the case where no server is standing at it.
func newCatalogRegistryAt(t *testing.T, authority string) *remote.Registry {
	t.Helper()
	reg, err := remote.NewRegistry(authority)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	reg.PlainHTTP = true
	return reg
}

// TestEnumerateRepositories_MalformedLinkHeaderReturnsFirstPage proves that when
// the registry serves a bracket-less (non-compliant) pagination header, the
// enumeration returns the first page's repositories it already collected rather
// than hard-failing with nothing. It exercises the real oras enumeration path.
func TestEnumerateRepositories_MalformedLinkHeaderReturnsFirstPage(t *testing.T) {
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
	got, err := registryops.EnumerateRepositories(context.Background(), reg, "", registryops.CatalogScope)
	if err != nil {
		t.Fatalf("malformed Link header should degrade to first page, got error: %v", err)
	}
	want := []string{"component/core/api-server", "plugin/cli/moo"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("first-page fallback = %v; want %v", got, want)
	}
}

// TestEnumerateRepositories_CompliantMultiPagePagesFully proves the tolerance
// does not short-circuit compliant pagination: a well-formed multi-page Link
// chain still pages through every page and returns the union, in sorted order.
func TestEnumerateRepositories_CompliantMultiPagePagesFully(t *testing.T) {
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
	got, err := registryops.EnumerateRepositories(context.Background(), reg, "", registryops.CatalogScope)
	if err != nil {
		t.Fatalf("compliant multi-page enumeration: %v", err)
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

// TestEnumerateRepositories_FirstRequestFailurePropagates proves a failure before
// any page is delivered (here, a connection failure — the callback is never
// invoked) still surfaces as an error rather than a silent empty result — the
// fallback covers only post-first-page failures.
func TestEnumerateRepositories_FirstRequestFailurePropagates(t *testing.T) {
	// A loopback authority that refuses connections, so the first catalog request
	// fails to connect before any page is delivered.
	reg := newCatalogRegistryAt(t, refusingLoopbackAddr(t))

	got, err := registryops.EnumerateRepositories(context.Background(), reg, "", registryops.CatalogScope)
	if err == nil {
		t.Fatalf("a first-request failure should propagate; got matches %v, nil error", got)
	}
	if got != nil {
		t.Fatalf("a first-request failure should return no matches; got %v", got)
	}
}
