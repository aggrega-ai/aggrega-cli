// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"path"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
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

// The registry-target seam is deliberately NOT re-declared here. It lives in the
// plugin operations layer, which is where every surface that resolves the default
// registry to enumerate reaches it, and the sites below name it there.
//
// A local spelling of it would have to be either a copy of the resolution or a
// variable initialized from the seam, and both are the same defect: a test points
// at one of the two while production reads the other, so the fake silently stops
// applying and the enumeration goes to the live registry. Naming the one seam at
// the call site cannot come apart that way.

// productionPluginDiscoverable is the live-registry backing for the
// unknown-command install hint: it resolves the workspace registry and reports
// whether it serves an installable CLI plugin named name. It is wired into the
// pluginDiscoverable seam only in the process entry point, so the test binary
// keeps the non-network stub. Every step is best-effort — an unresolvable
// workspace, an offline workspace, an unconfigured registry, or a search failure
// yields false — so an unknown command never blocks or crashes on a registry
// probe. The workspace resolves lazily from the working directory, because the
// unknown-command boundary is reached after dispatch has already failed and
// carries no resolved workspace of its own.
//
// ctx is the run's own, so the enumeration this makes ends when the reader asks
// the run to stop rather than holding the process open for a hint.
func productionPluginDiscoverable(ctx context.Context, name string) bool {
	rw, err := workspaceconfig.Resolve(".", "")
	if err != nil {
		return false
	}
	cfg := rw.Config()
	// The offline decision lives at the egress gate: the registry target derives
	// its client there and returns an error for an offline workspace, which this
	// best-effort hint treats as "not discoverable" (silent).
	reg, _, err := pluginops.RegistryTarget(cfg)
	if err != nil {
		return false
	}
	return discoverableViaSearch(ctx, reg, cfg.DefaultRegistryLocation().Path, name)
}

// discoverableViaSearch reports whether the registry serves an installable CLI
// plugin whose command name matches name, so the unknown-command path can
// suggest installing it. It matches a plugin repository whose final path segment
// equals name — a best-effort hint over the plugin scope beneath under, the
// repository path the default registry carries (empty for none), not an
// install-time guarantee. Any search error yields false: a registry failure
// degrades the hint to "not discoverable" and never propagates to the
// unknown-command path.
func discoverableViaSearch(ctx context.Context, reg registryRepoLister, under, name string) bool {
	scope := pluginSearchScope
	if under != "" {
		beneath := registryhost.Location{Path: under}.Repository("")
		scope = func(repoPath string) bool {
			rest, ok := strings.CutPrefix(repoPath, beneath)
			return ok && pluginSearchScope(rest)
		}
	}
	matches, err := searchRegistry(ctx, reg, name, scope)
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
