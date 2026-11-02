// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
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
// A configured directory that is not a directory — nothing exists there, or a
// file does, or a link leading nowhere — is refused, by every surface alike,
// because every surface reads the stack from here: the error names the entry, the
// file and line that list it, and what to do ([namedCatalogDirs]). Every caller
// able to report the refusal reports it and reads nothing. The stack is returned
// beside the refusal, every configured directory in it as listed, for a holder
// that keeps a lookup over the stack whatever it is told — one serving requests
// that have no way to carry a refusal, such as a listing: its lookup is then over
// the directories the configuration lists, and treats the refused one as it treats
// any directory it cannot walk, while a strict load of the stack still refuses it.
// A `--catalog` directory is kept whether or not it exists, so a load of the stack
// still reports one that is not there.
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
// directories still resolve everything `./x` does not carry. A directory named
// more than once — a flag naming a directory the configuration already lists, say
// — is one entry, at the position it was first named in ([namedCatalogDirs]), so
// it is read once and nothing in it is reported as shadowed by itself. Appending the
// conventional directory last means anything the reader lists wins over it on
// collision, and appending rather than defaulting means declaring one unrelated
// directory never silently stops the conventional one being read. It is not
// appended when an entry already in the stack reaches it ([Reaches]): the place it
// leads to is the place an entry leads to, or lies beneath it — the catalog walk is
// recursive, so a stack naming the workspace root already reads it, and a stack
// listing the directory a `catalog` link leads to already reads what lies behind
// the link. Reading one directory twice makes the loader report every identity in
// it as shadowed by itself; and a conventional directory appended beside a listed
// directory it leads into would be held to the workspace, so the link standing at
// it would halt a read of the very directory the configuration lists.
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
func (r *ResolvedWorkspace) ResolveCatalogDirs(release string, flagDirs []string) ([]string, error) {
	dirs, configured, refusal := r.namedCatalogDirs(release, flagDirs)
	if authoredEmpty(configured) {
		return dirs, refusal
	}
	conventional := filepath.Join(r.root, ConventionalCatalogDir)
	if presenceOf(conventional) == presentDirectory && !Reaches(dirs, conventional) {
		dirs = append(dirs, conventional)
	}
	return dirs, refusal
}

// NamedCatalogDirs returns the part of the catalog search stack
// [ResolvedWorkspace.ResolveCatalogDirs] returns for release that somebody named:
// the `--catalog` flag directories and the directories the configuration lists,
// in the stack's own order and spelling and each directory once, without the
// conventional `catalog` directory the stack appends unasked.
//
// It is what tells a directory named for the workspace to reach — one typed on
// the command line, or listed in the configuration, and possibly outside the
// workspace — from the one the workspace carries by convention, which is part of
// the workspace's own tree. The conventional directory is in this list exactly
// when somebody named it in its own spelling; where somebody named the place it
// leads to in another spelling, or a directory above that place, the stack does not
// carry it at all ([ResolvedWorkspace.ResolveCatalogDirs]). The stack itself is a
// single list, so the distinction is drawn here, where the list is built.
//
// What it returns is the only way that distinction travels: every reader of the
// stack is handed it beside the stack, and hands it on to [CatalogDirRoot]
// unjudged. A reader handed none treats the conventional directory as nobody's
// naming, so a reader that was not handed it refuses a link there rather than
// following it.
//
// It refuses a configured directory exactly as ResolveCatalogDirs does, with the
// same error, and returns the list as listed beside it, for the reason given
// there.
func (r *ResolvedWorkspace) NamedCatalogDirs(release string, flagDirs []string) ([]string, error) {
	dirs, _, refusal := r.namedCatalogDirs(release, flagDirs)
	return dirs, refusal
}

// CatalogDirRoot returns the root everything read beneath the catalog directory
// dir, and everything the product composes beneath it, is held to, given the
// workspace at workspaceRoot and named, the directories of dir's search stack
// somebody named ([ResolvedWorkspace.NamedCatalogDirs]).
//
// The conventional `catalog` directory at the workspace root, where nobody named
// it, is the workspace's own tree, so it is held to the workspace root: the
// directory being a link leading out of the workspace is a link inside the
// workspace, and is refused rather than made a root of its own by where it leads.
// Every directory somebody named — typed on the command line, or listed in the
// configuration, the conventional one included — has the root [writescope.RootFor]
// picks: the workspace root where the directory lies inside the workspace, and the
// directory itself, followed where it was named, where it lies outside.
//
// Whether somebody named the conventional directory is a question of the place it
// leads to, answered by [Reaches] from named: a directory named in another spelling
// of that place, or a directory above it, names it, exactly as the stack builder
// reads named when it decides whether to append the directory at all.
//
// named is read as it was handed over, never re-derived: which directories were
// named is decided where the stack is built, and nowhere else. Where named is nil
// the conventional directory is held to the workspace.
//
// It is the one answer for a catalog directory, asked by every walk of one and by
// the pin step placing an entity.lock beneath one, so what a lookup reads beneath
// a directory and what the pin step writes there are held to the same root.
func CatalogDirRoot(workspaceRoot, dir string, named []string) string {
	if workspaceRoot != "" && isConventionalCatalogDir(workspaceRoot, dir) && !Reaches(named, dir) {
		return workspaceRoot
	}
	return writescope.RootFor(workspaceRoot, dir)
}

// isConventionalCatalogDir reports whether dir is the entry a search stack carries
// for the workspace's conventional `catalog` directory. That entry is the one the
// stack builder appends spelled as the workspace root joined with the directory's
// name, so which entry it is reads from the spelling, made absolute and cleaned;
// whether anybody named the place it leads to is [Reaches]'s.
func isConventionalCatalogDir(workspaceRoot, dir string) bool {
	a, b := dir, filepath.Join(workspaceRoot, ConventionalCatalogDir)
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}

// namedCatalogDirs builds the named part of the stack, a fresh list, and returns
// it with the configured directories it was built from, whose nil-ness
// [authoredEmpty] reads, and with the refusal of the first configured directory
// that is not a directory, in list order, or nil.
//
// # A configured directory that is not a directory
//
// A directory the configuration lists — the workspace's `catalog_dirs`, or a
// Release's own entry under `workspace.releases`, an append included, at any
// layer of the cascade — where nothing exists, or where a file or a link leading
// nowhere stands ([presenceOf]), is refused ([ResolvedWorkspace.catalogDirRefusal]).
// A listed directory is a statement that entities are read from there, so reading
// on without it would build against fewer catalogs than the reader listed, with
// nothing to tell them which. This is the one place that decision is made: every
// surface that reads a catalog — a pull, a build, a check, an inspection, the
// daemon and the assistant-facing tools — reads the stack built here, so none of
// them can come to read the same configuration a second way.
//
// The refused directory stays in the stack, at the place it was listed, so a
// strict load of a stack whose refusal was not reported still refuses it. A directory
// that exists and cannot be read is not refused here — whether it is a directory
// is all this asks — and a load of the stack reports it as it always has. A
// `--catalog` directory is not judged here either, whether or not it exists: the
// flag is typed for one invocation, and the load's refusal of a directory that is
// not there is the answer to a mistyped one. A configured directory a flag also
// names is the flag's entry, so it is not judged for the same reason.
//
// A directory named more than once — by a `--catalog` flag and by the
// configuration, or twice by the configuration — is one entry, at the position it
// was first named in, spelled as it was named there. It is one directory however
// often it is named, so it is read once: a second read would meet every file the
// first read placed and report each as shadowed by itself, and a reader would be
// told of overrides nobody wrote. Keeping the first position leaves the first-win
// precedence of every other entry where it was, so no identity resolves to a
// different file for it. Two directories are one when they are one place on disk
// ([pathscope.SamePlace]), which is the file system's answer rather than a
// comparison of spellings: a directory named through a link, relative to another
// directory, with a trailing separator, or — on a file system that does not
// distinguish case — in another case, is the directory it leads to. A directory
// that is not there is compared by its spelling made absolute and cleaned, so
// naming it twice still names it once: a flag directory is reported missing once
// by the load, and a configured one is refused at its first entry.
//
// Only the same directory is folded. A directory that lies beneath another the
// stack names is a different directory and keeps its own entry, even though the
// walk of the one above it reads its files too.
func (r *ResolvedWorkspace) namedCatalogDirs(release string, flagDirs []string) (dirs, configured []string, refusal error) {
	configured, from := r.configuredCatalogDirEntries(release)
	if len(from) != len(configured) {
		from = nil
	}
	dirs = make([]string, 0, len(flagDirs)+len(configured)+1)
	for _, d := range flagDirs {
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		dirs = appendDistinctDir(dirs, d)
	}
	for i, d := range configured {
		if refusal == nil && !holdsDir(dirs, d) {
			if fault, refused := faultOf(presenceOf(d)); refused {
				var entry *catalogDirEntry
				if from != nil {
					entry = &from[i]
				}
				refusal = r.catalogDirRefusal(release, d, fault, entry)
			}
		}
		dirs = appendDistinctDir(dirs, d)
	}
	return dirs, configured, refusal
}

// appendDistinctDir appends dir to dirs unless an entry of dirs is the same
// directory, the comparison namedCatalogDirs states.
func appendDistinctDir(dirs []string, dir string) []string {
	if holdsDir(dirs, dir) {
		return dirs
	}
	return append(dirs, dir)
}

// holdsDir reports whether an entry of dirs is the same directory as dir, the
// comparison namedCatalogDirs states.
func holdsDir(dirs []string, dir string) bool {
	for _, have := range dirs {
		if pathscope.SamePlace(have, dir) || filepath.Clean(have) == filepath.Clean(dir) {
			return true
		}
	}
	return false
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

// presence is what the file system holds at a path a search stack names.
type presence int

const (
	// presentDirectory is a directory, reached through a link where the path is one.
	presentDirectory presence = iota
	// nothingThere is a path where nothing exists, not even a link.
	nothingThere
	// danglingLink is a link leading to a path where nothing exists.
	danglingLink
	// notADirectory is something that exists and is not a directory: a file, or
	// a link leading to one.
	notADirectory
	// unknownPresence is a path the file system will not say anything about —
	// one beneath a directory that cannot be read, say.
	unknownPresence
)

// presenceOf answers what is at path, and it is the one question the stack
// builder asks the file system about a directory it may carry. The two kinds of
// entry read it differently, and that difference is the whole of the rule. The
// conventional directory nobody named joins the stack only where it is a
// directory: a workspace that does not use the convention, or holds a file of
// that name, has none to read, and handing the loader one would make its walk
// fail on a workspace that simply does not use the convention. A configured
// directory is refused where the answer says it is not one ([faultOf]), and is
// kept where the file system will not say, so a directory that exists and cannot
// be read still reaches the load that reports it.
func presenceOf(path string) presence {
	fi, err := os.Stat(path)
	switch {
	case err == nil && fi.IsDir():
		return presentDirectory
	case err == nil:
		return notADirectory
	case !errors.Is(err, fs.ErrNotExist):
		return unknownPresence
	}
	if _, lerr := os.Lstat(path); lerr == nil {
		return danglingLink
	}
	return nothingThere
}

// catalogDirFault is what a refusal of a configured catalog directory says about
// it: what stands at the path instead of a directory, and what to do about it.
type catalogDirFault struct {
	what   string
	remedy string
}

// faultOf returns what a refusal says about a configured catalog directory whose
// path holds p, and whether p is refused at all: a directory is not, and nor is a
// path the file system will not say anything about, which the load's walk reports
// in its own words.
func faultOf(p presence) (catalogDirFault, bool) {
	switch p {
	case nothingThere:
		return catalogDirFault{what: "which does not exist", remedy: "create that directory"}, true
	case danglingLink:
		return catalogDirFault{
			what:   "which is a link leading to a path that does not exist",
			remedy: "point the link at a directory",
		}, true
	case notADirectory:
		return catalogDirFault{what: "which is not a directory", remedy: "list the directory you meant"}, true
	}
	return catalogDirFault{}, false
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
	dirs, _ := r.configuredCatalogDirEntries(release)
	return dirs
}

// configuredCatalogDirEntries returns the directories configuredCatalogDirs
// returns, with where each was written beside it, entry for entry, composed by
// the same choice of list. The record is nil where the resolution carries none,
// and a caller treats a record that does not line up with the directories as
// absent.
func (r *ResolvedWorkspace) configuredCatalogDirEntries(release string) ([]string, []catalogDirEntry) {
	workspace, workspaceFrom := r.config.Workspace.CatalogDirs, r.catalogDirsFrom
	entry, ok := r.releaseCatalog(release)
	if !ok {
		return workspace, workspaceFrom
	}
	base, baseFrom := workspace, workspaceFrom
	if entry.CatalogDirs != nil {
		base, baseFrom = entry.CatalogDirs, entry.dirsFrom
	}
	if len(entry.CatalogDirsAppend) == 0 {
		return base, baseFrom
	}
	return concat(base, entry.CatalogDirsAppend), concat(baseFrom, entry.appendFrom)
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
			dirsFrom:          releaseCatalogDirsWritten(path, key, "catalog_dirs", entry.CatalogDirs),
			appendFrom:        releaseCatalogDirsWritten(path, key, "catalog_dirs_append", entry.CatalogDirsAppend),
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
// the cascade met them in. Where each directory was written is composed in the
// same steps, so an entry's record stays entry for entry with its lists.
func foldReleaseCatalogs(into *types.OrderedMap[ReleaseCatalogConfig], layer types.OrderedMap[ReleaseCatalogConfig]) {
	for file, entry := range layer.All() {
		composed, _ := into.Get(file)
		if entry.CatalogDirs != nil {
			composed.CatalogDirs = append([]string{}, entry.CatalogDirs...)
			composed.dirsFrom = append([]catalogDirEntry{}, entry.dirsFrom...)
		}
		composed.CatalogDirsAppend = append(composed.CatalogDirsAppend, entry.CatalogDirsAppend...)
		composed.appendFrom = append(composed.appendFrom, entry.appendFrom...)
		into.Set(file, composed)
	}
}
