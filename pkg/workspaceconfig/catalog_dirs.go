// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ConventionalCatalogDir is the directory name, relative to the workspace root,
// that every catalog search stack reads without being told to. It is exported
// because the surfaces that tell a reader where to put an entity have to name
// the directory the resolver actually looks in; a second spelling inside a
// message is a spelling that goes on reading as correct after this one moves.
const ConventionalCatalogDir = "catalog"

// ReleaseCatalogsSetting is the settings path a reader writes to give one
// Release its own catalog directories, spelled as a diagnostic names it. It is
// exported for the reason CatalogDirsSetting is.
const ReleaseCatalogsSetting = "workspace.releases"

// ResolveCatalogDirs computes the effective catalog search stack for release,
// shared by every command, the daemon and the assistant-facing tools, from the
// resolved workspace it is called on: the repeatable `--catalog` flag
// directories PREPENDED to the configured directories, with the conventional
// `catalog` directory at the workspace root APPENDED last when that directory
// exists.
//
// The configured directories are the Release's own when the workspace
// configuration names release under `workspace.releases`, and the workspace's
// `catalog_dirs` otherwise. An entry that writes `catalog_dirs` replaces the
// workspace's list for that Release; one that does not keeps it; and the entry's
// `catalog_dirs_append` follows whichever applies. release is the Release file
// the stack is for, as the caller holds it — relative to the working directory
// or absolute — and is matched against the configuration's entries by the file
// it names, not by its spelling. An empty release asks for the workspace's own
// stack, which is what an operation that concerns no one Release reads: a
// whole-catalog lint or validation, and a by-name lookup, one entity's check or
// the listing of the workspace's entities that names no Release. A lookup, a
// check or a listing that names one reads that Release's stack, and a lookup or a
// listing holds the workspace documents [ResolvedWorkspace.ReleaseReach] says it
// holds.
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
// is the resolved workspace's own, so every surface that loads a catalog for one
// Release in one workspace searches the same directories.
//
// The workspace-level `catalog_dirs_append` is already folded into the config's
// `catalog_dirs` at config-load time, so it needs no handling here; a Release
// entry's is folded in by configuredCatalogDirs, because whether it follows the
// entry's list or the workspace's is only known once both are resolved. The
// configured directories are already absolute (the cascade resolved them against
// their declaring file at load); the `--catalog` flag dirs carry no declaring
// file, so they resolve against the working directory here — the one place flag
// paths are anchored, which keeps the resolved set identical across
// build/pull/inspect for the same flags and CWD.
func (r *ResolvedWorkspace) ResolveCatalogDirs(release string, flagDirs []string) []string {
	dirs, configured := r.namedCatalogDirs(release, flagDirs)
	if authoredEmpty(configured) {
		return dirs
	}
	conventional := filepath.Join(r.root, ConventionalCatalogDir)
	if !alreadyCovered(dirs, conventional) && isExistingDir(conventional) {
		dirs = append(dirs, conventional)
	}
	return dirs
}

// NamedCatalogDirs returns the part of the catalog search stack
// [ResolvedWorkspace.ResolveCatalogDirs] returns for release that somebody named:
// the `--catalog` flag directories and the directories the configuration lists,
// in the stack's own order and spelling, without the conventional `catalog`
// directory the stack appends unasked.
//
// It is what tells a directory named for the workspace to reach — one typed on
// the command line, or listed in the configuration, and possibly outside the
// workspace — from the one the workspace carries by convention, which is part of
// the workspace's own tree. A file the product composes beneath a named directory
// is held to the root that directory picks; one composed beneath the conventional
// directory is the workspace's. The stack itself is a single list, so the
// distinction is drawn here, where the list is built.
func (r *ResolvedWorkspace) NamedCatalogDirs(release string, flagDirs []string) []string {
	dirs, _ := r.namedCatalogDirs(release, flagDirs)
	return dirs
}

// namedCatalogDirs builds the named part of the stack, a fresh list, and returns
// it with the configured directories it was built from, whose nil-ness
// [authoredEmpty] reads.
func (r *ResolvedWorkspace) namedCatalogDirs(release string, flagDirs []string) (dirs, configured []string) {
	configured = r.configuredCatalogDirs(release)
	dirs = make([]string, 0, len(flagDirs)+len(configured)+1)
	for _, d := range flagDirs {
		if abs, err := filepath.Abs(d); err == nil {
			dirs = append(dirs, abs)
		} else {
			dirs = append(dirs, d)
		}
	}
	return append(dirs, configured...), configured
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

// configuredCatalogDirs returns the directories the configuration lists for
// release, before the flag directories and the conventional directory are
// added: the workspace's own list, or the Release's where the configuration
// names it.
//
// The nil-ness of the workspace list is preserved when an entry leaves it in
// place and adds nothing, because that nil-ness is what authoredEmpty reads. An
// entry that adds directories yields a fresh list, so neither the entry nor the
// workspace list is ever written through.
func (r *ResolvedWorkspace) configuredCatalogDirs(release string) []string {
	workspace := r.config.Workspace.CatalogDirs
	entry, ok := r.releaseCatalog(release)
	if !ok {
		return workspace
	}
	base := workspace
	if entry.CatalogDirs != nil {
		base = entry.CatalogDirs
	}
	if len(entry.CatalogDirsAppend) == 0 {
		return base
	}
	return concat(base, entry.CatalogDirsAppend)
}

// releaseCatalog returns the configuration's entry for release and whether it
// has one. release is matched by the file it names: made absolute against the
// working directory when it is relative, then brought to the same canonical
// spelling the entries were stored under when the configuration loaded, so a
// Release named through a symbolic link, or by two spellings of one path, finds
// the one entry that file has.
func (r *ResolvedWorkspace) releaseCatalog(release string) (ReleaseCatalogConfig, bool) {
	if r == nil || release == "" || r.config.Workspace.Releases.Len() == 0 {
		return ReleaseCatalogConfig{}, false
	}
	abs, err := filepath.Abs(release)
	if err != nil {
		return ReleaseCatalogConfig{}, false
	}
	return r.config.Workspace.Releases.Get(canonicalReleasePath(abs))
}

// canonicalReleasePath is the one spelling a Release file is keyed by: its
// absolute path with symbolic links resolved, or the cleaned absolute path when
// the links cannot be resolved because the file is not there. abs must be
// absolute.
func canonicalReleasePath(abs string) string {
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return filepath.Clean(abs)
}

// anchorReleaseCatalogs resolves one configuration file's `workspace.releases`
// against the file that declares them, refusing an entry that names no file.
//
// Each key becomes the canonical path of the Release file it names and each
// listed directory becomes absolute, both relative to the declaring file's
// directory — the same anchoring every other path in a configuration file takes,
// so where a Release's catalog is read from does not depend on where a command
// was run. An entry's nil list stays nil, because an entry that omits
// `catalog_dirs` and one that writes it empty say different things.
//
// An entry naming a file that does not exist, or that names a directory, is
// refused rather than kept. Nothing would ever match it, so the Release it was
// written for — typically one since renamed — would build against the
// workspace's list with nothing saying so, and which catalog a Release builds
// against is exactly what a reader must not have to discover from its output.
// Two entries naming one file are refused for the same reason: which of the two
// applied would otherwise depend on the order they were written in. Both
// refusals wrap ErrConfigInvalid, as every value check on a configuration file
// does, and name the file that carries the entry.
func anchorReleaseCatalogs(entries types.OrderedMap[ReleaseCatalogConfig], path string) (types.OrderedMap[ReleaseCatalogConfig], error) {
	if entries.Len() == 0 {
		return types.OrderedMap[ReleaseCatalogConfig]{}, nil
	}
	dir := filepath.Dir(path)
	anchored := types.NewOrderedMap[ReleaseCatalogConfig]()
	writtenAs := make(map[string]string, entries.Len())
	for key, entry := range entries.All() {
		file := resolveFilesystemPath(dir, key)
		fi, err := os.Stat(file)
		switch {
		case err != nil:
			return types.OrderedMap[ReleaseCatalogConfig]{}, fmt.Errorf(
				"%s: the %s entry %q names %s, which does not exist; write the path of the Release "+
					"file this entry is for, relative to this file, or remove the entry: %w",
				path, ReleaseCatalogsSetting, key, file, ErrConfigInvalid())
		case fi.IsDir():
			return types.OrderedMap[ReleaseCatalogConfig]{}, fmt.Errorf(
				"%s: the %s entry %q names %s, which is a directory; each entry is keyed by one "+
					"Release file: %w",
				path, ReleaseCatalogsSetting, key, file, ErrConfigInvalid())
		}
		canonical := canonicalReleasePath(file)
		if prior, dup := writtenAs[canonical]; dup {
			return types.OrderedMap[ReleaseCatalogConfig]{}, fmt.Errorf(
				"%s: the %s entries %q and %q both name %s; keep one entry for each Release file: %w",
				path, ReleaseCatalogsSetting, prior, key, canonical, ErrConfigInvalid())
		}
		writtenAs[canonical] = key
		anchored.Set(canonical, ReleaseCatalogConfig{
			CatalogDirs:       anchorDirs(dir, entry.CatalogDirs),
			CatalogDirsAppend: anchorDirs(dir, entry.CatalogDirsAppend),
		})
	}
	return *anchored, nil
}

// anchorDirs resolves each directory against dir into a fresh list, keeping a
// nil list nil and an empty one empty.
func anchorDirs(dir string, dirs []string) []string {
	if dirs == nil {
		return nil
	}
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = resolveFilesystemPath(dir, d)
	}
	return out
}

// foldReleaseCatalogs composes one cascade layer's Release entries into the
// entries composed so far, one Release at a time, the way the cascade composes
// the workspace's own two keys: a layer that writes an entry's `catalog_dirs`
// replaces it, and every layer's `catalog_dirs_append` for that Release is
// concatenated in cascade order. A Release first named by a later layer takes
// its place after the ones already named, so the composed entries keep the order
// the cascade met them in.
func foldReleaseCatalogs(into *types.OrderedMap[ReleaseCatalogConfig], layer types.OrderedMap[ReleaseCatalogConfig]) {
	for file, entry := range layer.All() {
		composed, _ := into.Get(file)
		if entry.CatalogDirs != nil {
			composed.CatalogDirs = append([]string{}, entry.CatalogDirs...)
		}
		composed.CatalogDirsAppend = append(composed.CatalogDirsAppend, entry.CatalogDirsAppend...)
		into.Set(file, composed)
	}
}
