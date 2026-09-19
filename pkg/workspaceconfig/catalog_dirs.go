// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"strings"
)

// ConventionalCatalogDir is the directory name, relative to the workspace root,
// that every catalog search stack reads without being told to. It is exported
// because the surfaces that tell a reader where to put an entity have to name
// the directory the resolver actually looks in; a second spelling inside a
// message is a spelling that goes on reading as correct after this one moves.
const ConventionalCatalogDir = "catalog"

// ResolveCatalogDirs computes the effective catalog search stack shared by every
// command that loads a catalog, from the resolved workspace it is called on: the
// repeatable `--catalog` flag directories PREPENDED to the workspace config's
// `catalog_dirs`, with the conventional `catalog` directory at the workspace
// root APPENDED last when that directory exists.
//
// The list is an ordered first-win precedence stack (the loader resolves a
// duplicate identity to the earliest-listed directory and records the rest as
// shadows). Prepending the flag dirs makes `--catalog` additive and
// override-capable in one move: an explicit `--catalog ./x` is searched before
// the configured directories, shadowing them on collision, while the configured
// directories still resolve everything `./x` does not carry. Appending the
// conventional directory last means anything the reader lists wins over it on
// collision, and appending rather than defaulting means declaring one unrelated
// directory never silently stops the conventional one being read. It is not
// appended when an entry already in the stack reaches it — the catalog walk is
// recursive, so a stack naming the workspace root already reads it, and reading
// one directory twice makes the loader report every identity in it as shadowed
// by itself.
//
// A `catalog_dirs` list the reader authored as empty is the explicit off-switch
// and suppresses the conventional directory too, so "this workspace has no local
// catalog" is expressible; an ABSENT `catalog_dirs` is a different statement and
// still reads the conventional directory. The two are carried by the merged
// slice being nil (absent at every layer) or non-nil and empty (a layer authored
// `catalog_dirs: []`), which is what the cascade's seeded-overlay merge
// preserves.
//
// There is no per-caller default: the root the conventional directory hangs off
// is the resolved workspace's own, so every command that loads a catalog in one
// workspace searches the same directories.
//
// `catalog_dirs_append` is already folded into the config's `catalog_dirs` at
// config-load time, so it needs no handling here. The configured directories
// are already absolute (the cascade resolved them against their declaring file
// at load); the `--catalog` flag dirs carry no declaring file, so they resolve
// against the working directory here — the one place flag paths are anchored,
// which keeps the resolved set identical across build/pull/inspect for the same
// flags and CWD.
func (r *ResolvedWorkspace) ResolveCatalogDirs(flagDirs []string) []string {
	configured := r.config.Workspace.CatalogDirs
	dirs := make([]string, 0, len(flagDirs)+len(configured)+1)
	for _, d := range flagDirs {
		if abs, err := filepath.Abs(d); err == nil {
			dirs = append(dirs, abs)
		} else {
			dirs = append(dirs, d)
		}
	}
	dirs = append(dirs, configured...)
	if authoredEmpty(configured) {
		return dirs
	}
	conventional := filepath.Join(r.root, ConventionalCatalogDir)
	if !alreadyCovered(dirs, conventional) && isExistingDir(conventional) {
		dirs = append(dirs, conventional)
	}
	return dirs
}

// authoredEmpty reports whether the merged catalog-directory list is the
// reader's explicit empty list rather than an absent key. The cascade merges
// each layer over the one beneath it, so a key no layer wrote leaves the field
// nil while a layer that wrote `catalog_dirs: []` leaves it non-nil and empty —
// the one place that distinction is read, named here so a caller never has to
// re-derive it from a nil check.
func authoredEmpty(configured []string) bool {
	return configured != nil && len(configured) == 0
}

// alreadyCovered reports whether an entry already in the stack reaches dir —
// either by naming it, or by being an ancestor of it, since the catalog walk of
// a directory is recursive.
//
// It is what keeps the appended conventional directory from being read twice,
// and the cost of getting this wrong is not merely a repeated walk: the stack is
// first-win per entry, so a directory reached twice makes the loader report
// every identity in it as shadowed by itself. The ancestor case is the one that
// bites, because listing the workspace root — the documented way to ask for a
// whole-tree walk deliberately — reaches the conventional directory already.
//
// Paths are compared cleaned, because a configured entry may be an absolute
// path the cascade passed through verbatim, trailing separator and all. The
// containment test requires a separator boundary, so a sibling directory whose
// name merely starts with the same characters is not mistaken for an ancestor.
func alreadyCovered(dirs []string, dir string) bool {
	want := filepath.Clean(dir)
	for _, d := range dirs {
		clean := filepath.Clean(d)
		if clean == want {
			return true
		}
		if strings.HasPrefix(want, clean+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// isExistingDir reports whether path names a directory that is there. A path
// that does not exist, or that exists as a file, contributes nothing to the
// search stack — handing the loader a directory that is not there would make the
// walk fail on a workspace that simply does not use the convention.
func isExistingDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
