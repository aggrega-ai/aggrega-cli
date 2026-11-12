// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// ReleaseReach says which workspace documents belong to an answer given in one
// Release's terms.
//
// A workspace can keep several variants of one entity side by side, each under a
// catalog directory a different Release reads. An answer given in one Release's
// terms holds a document under a catalog directory that Release's search stack
// reads, and leaves out a document under a catalog directory only some other stack
// reads, so variants kept side by side are never conflated in one answer. A
// document under no catalog directory at all — a Release, its Aggregate, its
// Manifest, written beside the catalog rather than in it — belongs to every
// Release's answer, because no Release's directories are what decide it.
//
// Which directories are catalog directories is read from the workspace itself:
// every directory the resolver returns for the workspace's own stack or for any
// Release the configuration names under `workspace.releases`. Nothing here keeps a
// second list of them, so a directory a Release is given is a catalog directory the
// moment the configuration says so.
//
// The zero value is the reach of a request that names no Release. Such a request
// is answered over the workspace as a whole, so every document belongs to it, and
// a surface holding no Release passes the zero value rather than branching.
//
// It is the one home of this rule. Every surface that answers about workspace
// documents in a Release's terms — the entity listing, a lookup by name, the
// requests that start from a cursor, what may attach to a graph node — asks it,
// so no two of them can come to disagree about which variant a Release sees.
type ReleaseReach struct {
	root     string
	narrowed bool
	reached  []string
	catalogs []string
}

// ReleaseReach returns the reach of the Release release names, read through the
// same search stack ResolveCatalogDirs returns for release and flagDirs. release
// takes the form ResolveCatalogDirs takes it in, and an empty release returns the
// zero value, which holds every document.
//
// A release naming no file is refused, and so is one naming a directory. Neither
// can be a Release, and answering anyway would answer in the terms of a Release
// the configuration does not name — the workspace's own list — while reading to
// its asker as an answer in the terms of the one they named. The refusals are the
// two the configuration's own entries are held to when it loads. A Release whose
// stack the resolver refuses is refused with that refusal, since the reach is
// read through that stack.
func (r *ResolvedWorkspace) ReleaseReach(release string, flagDirs []string) (ReleaseReach, error) {
	if r == nil || release == "" {
		return ReleaseReach{}, nil
	}
	if err := r.requireReleaseFile(release); err != nil {
		return ReleaseReach{}, err
	}
	reached, err := r.ResolveCatalogDirs(release, flagDirs)
	if err != nil {
		return ReleaseReach{}, err
	}
	return ReleaseReach{
		root:     r.root,
		narrowed: true,
		reached:  reached,
		catalogs: r.CatalogDirsOfEveryStack(flagDirs),
	}, nil
}

// CatalogDirsOfEveryStack returns every directory a catalog of this workspace is
// read from: the workspace's own stack, then the stack of each Release the
// configuration names under `workspace.releases`, in the order the configuration
// names them, each resolved with flagDirs as ResolveCatalogDirs resolves it. A
// directory more than one stack reads appears once per stack.
//
// Every Release is asked of the resolver rather than read off its entry, so the
// directories returned are exactly the ones each stack is searched in, and a
// caller needing the whole set — to tell a catalog directory from any other, or
// to recognise one in prose — reads it here rather than walking the map itself.
//
// It never refuses. A stack the resolver refuses contributes its directories as
// listed, the refused one among them: this answers which directories are catalog
// directories, which a directory that does not exist, or is not a directory,
// changes nothing about, since nothing lies beneath it. A read of a stack the
// resolver refuses is refused where that stack is asked for.
func (r *ResolvedWorkspace) CatalogDirsOfEveryStack(flagDirs []string) []string {
	if r == nil {
		return nil
	}
	dirs, _ := r.ResolveCatalogDirs("", flagDirs)
	for release := range r.config.Workspace.Releases.All() {
		stack, _ := r.ResolveCatalogDirs(release, flagDirs)
		dirs = append(dirs, stack...)
	}
	return dirs
}

// Holds reports whether the workspace document at path belongs to an answer given
// in the terms of the Release this reach was read for: it does unless it sits
// under a catalog directory of this workspace that the Release's stack does not
// read.
//
// path is the document's absolute path, or its path relative to the workspace
// root. A document under a directory the Release's stack reads is held even where
// another stack's directory encloses it, since that Release reads it.
func (r ReleaseReach) Holds(path string) bool {
	if !r.narrowed {
		return true
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.root, path)
	}
	if underAnyDir(path, r.reached) {
		return true
	}
	return !underAnyDir(path, r.catalogs)
}

// underAnyDir reports whether path lies inside any of dirs. Containment is the
// shared predicate's, so a sibling directory whose name merely extends one of dirs
// is not mistaken for it.
func underAnyDir(path string, dirs []string) bool {
	for _, dir := range dirs {
		if _, inside := pathscope.Relative(dir, path); inside {
			return true
		}
	}
	return false
}

// requireReleaseFile refuses a release naming nothing, or naming a directory,
// spelling it the way the account of a failed lookup spells a Release.
//
// Each refusal is filed under the operator's concern, since the value at fault
// is the Release a request or a command line named, and it names no registered
// condition because no rule fired: what was refused is the argument. The
// category is set where the refusal is made, so it reads the same wherever the
// refusal is reported.
func (r *ResolvedWorkspace) requireReleaseFile(release string) error {
	abs, err := filepath.Abs(release)
	if err != nil {
		return releaseArgumentRefusal(fmt.Sprintf("the Release %s cannot be located: %v", release, err))
	}
	fi, err := os.Stat(abs)
	switch {
	case err != nil:
		return releaseArgumentRefusal(fmt.Sprintf("the Release %s does not exist; name an existing Release file",
			r.reportedReleaseName(release)))
	case fi.IsDir():
		return releaseArgumentRefusal(fmt.Sprintf("the Release %s is a directory; name one Release file",
			r.reportedReleaseName(release)))
	}
	return nil
}

// releaseArgumentRefusal is the refusal of a value that cannot name a Release
// file, carrying the sentence that says why.
func releaseArgumentRefusal(message string) error {
	return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{}, message, nil, nil)
}
