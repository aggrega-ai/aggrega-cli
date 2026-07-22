// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// registryRepoLister enumerates the repository names a registry serves. It is
// the one registry capability the search primitive needs — the OCI catalog
// (_catalog) API surfaced by oras as Repositories. The production value is an
// authenticated oras remote registry; a test substitutes an in-memory lister so
// search runs without a network.
type registryRepoLister interface {
	Repositories(ctx context.Context, last string, fn func(repos []string) error) error
}

// searchScope decides whether a repository path is within a search's scope. It
// is what makes one search primitive serve two commands: plugin search and
// catalog search pass different scopes over the same enumeration.
type searchScope func(repoPath string) bool

// pluginSearchScope admits the repository paths Wasm plugins publish under — the
// plugin/ path prefix the plugin URI convention uses (KindPathSegment("Plugin")).
// It scopes plugin search to installable plugins rather than the whole catalog.
func pluginSearchScope(repoPath string) bool {
	return strings.HasPrefix(repoPath, catalog.KindPathSegment("Plugin")+"/")
}

// catalogSearchScope admits every repository — catalog search spans the full
// entity surface, not one kind.
func catalogSearchScope(string) bool { return true }

// searchRegistry is the single registry-search primitive behind both
// `aggrega plugin search` and `aggrega catalog search`. It enumerates the
// registry's repositories, keeps those the scope admits whose path contains the
// case-insensitive query substring (an empty query matches every in-scope
// repository), and returns the matches in ascending order. The two commands
// differ only in the scope they pass; the enumeration, filtering, and ordering
// live here once so the two never drift into two searches.
//
// Enumeration is progress-aware. The registry delivers each catalog page to the
// callback before it parses that page's pagination header, so a registry that
// serves a malformed pagination header fails only after the first page's
// repositories were already collected. In that case the matches gathered so far
// are returned rather than discarded — a partial result beats no result. An
// error raised before any page was delivered (a connection or first-request
// failure) still propagates, since there is nothing to fall back to.
func searchRegistry(ctx context.Context, reg registryRepoLister, query string, scope searchScope) ([]string, error) {
	q := strings.ToLower(query)
	var matches []string
	pageDelivered := false
	err := reg.Repositories(ctx, "", func(repos []string) error {
		pageDelivered = true
		for _, repo := range repos {
			if !scope(repo) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(repo), q) {
				continue
			}
			matches = append(matches, repo)
		}
		return nil
	})
	if err != nil && !pageDelivered {
		return nil, fmt.Errorf("search registry: %w", err)
	}
	sort.Strings(matches)
	return matches, nil
}

// registrySearchTarget resolves the registry to search from the resolved
// workspace's default registry. Production returns an authenticated oras remote
// registry; a test substitutes an in-memory lister so search runs without a
// network. It is a package-level seam because the live path needs registry
// credentials the sandbox does not carry: the search logic is proven against a
// fake target, and the live registry resolution is operator-gated.
var registrySearchTarget = func(cfg workspaceconfig.Config) (registryRepoLister, string, error) {
	host := cfg.DefaultRegistry
	if host == "" {
		return nil, "", errors.New("no default registry is configured to search")
	}
	httpClient, err := workspaceEgressClient(cfg)
	if err != nil {
		// The network-egress gate refused (the workspace is offline); the caller
		// decides whether that is loud (registry search / plugin add) or silent
		// (the best-effort install hint).
		return nil, host, err
	}
	// Construction routes through the one registry home so the loopback
	// plain-HTTP handling and the Docker-credential authentication match the
	// repository client exactly.
	reg, err := oci.NewRemoteRegistry(host, httpClient)
	if err != nil {
		return nil, host, err
	}
	return reg, host, nil
}

// productionPluginDiscoverable is the live-registry backing for the
// unknown-command install hint: it resolves the workspace registry and reports
// whether it serves an installable CLI plugin named name. It is wired into the
// pluginDiscoverable seam only in the process entry point, so the test binary
// keeps the non-network stub. Every step is best-effort — an unresolvable
// workspace, an offline workspace, an unconfigured registry, or a search failure
// yields false — so an unknown command never blocks or crashes on a registry
// probe. The workspace resolves lazily from the working directory since the
// unknown-command boundary carries no cobra context.
func productionPluginDiscoverable(name string) bool {
	rw, err := workspaceconfig.Resolve(".", "")
	if err != nil {
		return false
	}
	cfg := rw.Config()
	// The offline decision lives at the egress gate: registrySearchTarget derives
	// its client there and returns an error for an offline workspace, which this
	// best-effort hint treats as "not discoverable" (silent).
	reg, _, err := registrySearchTarget(cfg)
	if err != nil {
		return false
	}
	return discoverableViaSearch(context.Background(), reg, name)
}

// discoverableViaSearch reports whether the registry serves an installable CLI
// plugin whose command name matches name, so the unknown-command path can
// suggest installing it. It matches a plugin repository whose final path segment
// equals name — a best-effort hint over the plugin scope, not an install-time
// guarantee. Any search error yields false: a registry failure degrades the
// hint to "not discoverable" and never propagates to the unknown-command path.
func discoverableViaSearch(ctx context.Context, reg registryRepoLister, name string) bool {
	matches, err := searchRegistry(ctx, reg, name, pluginSearchScope)
	if err != nil {
		return false
	}
	for _, m := range matches {
		if path.Base(m) == name {
			return true
		}
	}
	return false
}
