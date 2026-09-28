// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// releaseCatalogs holds the catalog lookups one workspace's Release-scoped
// requests resolve against, one lookup per catalog search stack.
//
// A workspace configuration may give a Release catalog directories of its own,
// so two Releases in one workspace can read different directories, and a
// request naming a Release has to be answered from that Release's. Which
// directories those are is never decided here: the stack is asked of the one
// resolver every command and the assistant-facing tools read a Release's
// directories through, and this only keeps the lookup built for each distinct
// answer, so a Release is answered from warm indexes after its first request and
// Releases sharing a stack share a lookup.
//
// The lookups are kept in the order they were first asked for and found by
// comparison rather than through a map, so nothing about which lookup serves a
// request depends on iteration order. The set is seeded with the workspace's
// session lookup under the workspace's own list, so a Release whose stack is that
// list — every Release in a workspace that gives none its own directories and has
// no conventional catalog directory the list omits — is answered from exactly the
// lookup it was answered from before any Release had directories of its own.
type releaseCatalogs struct {
	mu      sync.Mutex
	entries []releaseCatalogEntry
}

// releaseCatalogEntry is one stack and the lookup built over it.
type releaseCatalogEntry struct {
	stack  string
	lookup *catalog.LazyLookup
}

// newReleaseCatalogs returns a set seeded with session, the lookup already built
// over the workspace's own directory list.
func newReleaseCatalogs(sessionDirs []string, session *catalog.LazyLookup) *releaseCatalogs {
	return &releaseCatalogs{entries: []releaseCatalogEntry{{stack: catalogStackKey(sessionDirs), lookup: session}}}
}

// lookupFor returns the lookup over dirs, building it with build the first time
// that stack is asked for.
func (c *releaseCatalogs) lookupFor(dirs []string, build func() *catalog.LazyLookup) *catalog.LazyLookup {
	key := catalogStackKey(dirs)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.stack == key {
			return e.lookup
		}
	}
	lookup := build()
	c.entries = append(c.entries, releaseCatalogEntry{stack: key, lookup: lookup})
	return lookup
}

// reset drops every lookup's workspace-derived state, as an invalidation of the
// workspace asks. Each lookup re-walks its own directories on its next request.
func (c *releaseCatalogs) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		e.lookup.Reset()
	}
}

// catalogStackKey spells a stack as one comparable string. The separator is a
// byte no path carries, so two different stacks never spell the same key, and a
// nil and an empty stack — both of which search no directory — spell the same
// one.
func catalogStackKey(dirs []string) string {
	return strings.Join(dirs, "\x00")
}

// catalogFor returns the session catalog lookup a request naming releasePath is
// answered from: the lookup over the catalog search stack the workspace
// configuration resolves for that Release. releasePath is absolute, as every
// Release parameter is once resolved.
//
// The stack is resolved with no command-line catalog directories, because a
// request carries none: what an editor may choose between is expressed in the
// workspace configuration, which a reader edits, rather than in a flag nobody
// types here.
//
// A Server holding no resolved workspace — the in-memory test harness and a
// request whose subject is not a workspace — has nothing to resolve a stack
// from, and is answered from the one lookup it holds.
func (s *Server) catalogFor(releasePath string) *catalog.LazyLookup {
	if s.resolvedWorkspace == nil || s.releaseCatalogs == nil {
		return s.lazyCatalog
	}
	dirs := s.resolvedWorkspace.ResolveCatalogDirs(releasePath, nil)
	return s.releaseCatalogs.lookupFor(dirs, func() *catalog.LazyLookup {
		return catalog.NewLazyLookup(s.workspaceRoot, s.wsConfig.ArtifactCacheDir, dirs, nil)
	})
}
