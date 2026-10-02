// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
	dirs := s.catalogDirsFor(releasePath)
	return s.releaseCatalogs.lookupFor(dirs, func() *catalog.LazyLookup {
		return catalog.NewLazyLookup(s.workspaceRoot, s.wsConfig.ArtifactCacheDir, dirs, nil)
	})
}

// catalogDirsFor returns the catalog search stack a request naming releasePath
// reads: the directories the workspace configuration resolves for that Release,
// with no command-line catalog directories, for the reason catalogFor gives.
// releasePath is absolute, and the empty string asks for the workspace's own
// stack.
//
// It is the one place the daemon asks the resolver for a Release's directories.
// catalogFor builds its lookup over what this returns, and a check that reads
// directories rather than a lookup — one entity's check in a Release's terms —
// is handed the same list, so the two cannot come to read different stacks for
// one Release.
//
// A Server holding no resolved workspace — the in-memory test harness — has no
// resolver to ask, and answers the list its configuration carries.
func (s *Server) catalogDirsFor(releasePath string) []string {
	if s.resolvedWorkspace == nil {
		return s.wsConfig.Workspace.CatalogDirs
	}
	return s.resolvedWorkspace.ResolveCatalogDirs(releasePath, nil)
}

// releaseTerms is what a request that may name a Release is answered within: the
// catalog lookup it resolves names through, and which workspace documents its
// answer holds.
//
// A request naming no Release is answered within the workspace's own terms — the
// session lookup, holding every workspace document — which are the terms every
// request concerning no one Release reads. A request naming one is answered from the
// lookup over that Release's stack, and holds only the workspace documents that
// Release's reach holds, so variants a workspace keeps side by side for different
// Releases are never conflated in one answer.
type releaseTerms struct {
	// release is the absolute path of the Release the request named, and empty
	// where it named none.
	release string
	lookup  *catalog.LazyLookup
	reach   workspaceconfig.ReleaseReach
}

// workspaceTerms returns the terms of a request that names no Release.
func (s *Server) workspaceTerms() releaseTerms {
	return releaseTerms{lookup: s.lazyCatalog}
}

// termsFor returns the terms a request naming release is answered within, or the
// refusal for a value that cannot name one. release is the request's own value: a
// path relative to the workspace root, refused in the shapes every Release
// parameter is refused in, or empty for a request that names none.
//
// The lookup and the reach are both read for the Release with no command-line
// catalog directories, as catalogFor reads the lookup, so the documents an answer
// holds and the stack its names resolve through are read from one stack.
//
// Every request that answers about workspace documents in a Release's terms asks
// this, rather than composing its own pair, so no two of them can come to read a
// different stack or a different set of documents for the same Release.
func (s *Server) termsFor(release string) (releaseTerms, *jsonRPCError) {
	if release == "" {
		return s.workspaceTerms(), nil
	}
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, release)
	if paramErr != nil {
		return releaseTerms{}, paramErr
	}
	reach, err := s.resolvedWorkspace.ReleaseReach(releasePath, nil)
	if err != nil {
		return releaseTerms{}, &jsonRPCError{Code: jsonRPCInvalidParams, Message: "invalid params: " + err.Error(), cause: err}
	}
	return releaseTerms{release: releasePath, lookup: s.catalogFor(releasePath), reach: reach}, nil
}
