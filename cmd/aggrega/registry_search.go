// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"path"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The registry enumeration primitive, its scope predicate and the two standing
// scopes live in the shared operations layer, which is where every surface that
// enumerates a registry reaches them. They are named here so this package's
// existing spellings resolve to that one implementation rather than to a second
// copy of it.
type (
	registryRepoLister = registryops.RepoLister
	searchScope        = registryops.RepoScope
)

// pluginSearchScope admits the repository paths Wasm plugins publish under, so
// an enumeration is scoped to installable plugins rather than the whole catalog.
var pluginSearchScope = registryops.PluginScope

// catalogSearchScope admits every repository — a catalog enumeration spans the
// full entity surface, not one kind.
var catalogSearchScope = registryops.CatalogScope

// searchRegistry enumerates the repositories a registry serves that the scope
// admits and whose path contains the query substring, in ascending order. It
// delegates to the shared layer's one enumeration primitive, whose walk,
// filtering, ordering and partial-on-error posture are documented there.
func searchRegistry(ctx context.Context, reg registryRepoLister, query string, scope searchScope) ([]string, error) {
	return registryops.EnumerateRepositories(ctx, reg, query, scope)
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
