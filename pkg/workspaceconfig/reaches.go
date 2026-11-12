// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// Reaches reports whether one of named reaches the place path leads to: the one
// comparison every surface makes between what somebody named — a directory or a
// Release file the workspace configuration lists, or a directory typed on the
// command line — and a path the product meets, such as the conventional `catalog`
// directory a stack would append, a link a walk of the workspace meets, or a
// Release file a request names.
//
// Places are compared, never spellings. Each entry of named is a root, and path is
// judged against it by [pathscope.ResolvesInside], the one definition of inside,
// which resolves the links of both sides: so an entry naming a link and an entry
// naming the place it leads to name the same thing, and a path spelled through a
// link is judged by where it leads, whichever spelling either side was written in.
//
// A directory is reached where it leads to the place an entry leads to, or to a
// place beneath it, because a named directory is read whole: every walk of a
// catalog directory reads the tree beneath it, so a directory there is read by
// that walk already. A file is reached only where it is the place an entry leads
// to, because a setting naming a file names that file and nothing beside it. A
// path that is not there leads to no place, and is reached by nothing.
//
// It reads nothing at the path but where it leads and what kind of thing lies
// there, and decides nothing else: which entries count as named is the caller's —
// the configuration's lists, or the named part of a catalog search stack
// ([ResolvedWorkspace.NamedCatalogDirs]) — and so is what follows from a path being
// reached.
func Reaches(named []string, path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	dir := info.IsDir()
	for _, entry := range named {
		if _, inside := pathscope.ResolvesInside(entry, path); !inside {
			continue
		}
		if dir {
			return true
		}
		// A file lies inside an entry that names it or a directory above it; it is
		// the entry's own place only where the entry, judged against the file as a
		// root, lies inside it too — nothing lies beneath a file.
		if _, same := pathscope.ResolvesInside(path, entry); same {
			return true
		}
	}
	return false
}
