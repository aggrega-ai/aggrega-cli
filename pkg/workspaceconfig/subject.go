// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
)

// ForSubject returns the workspace a run uses when its subject — the Release or
// the document the run is about — is the file at subject, spelled as the reader
// or the caller named it.
//
// It is r itself, unchanged, except in one case, where the workspace is found
// again from the folder that file sits in. All of these hold in that case:
//
//   - nothing named a root for r: no --workspace-root, no AGGREGA_WORKSPACE_ROOT
//     and no root a call named, which reach a resolution as its explicit root;
//   - nothing named where r began either: it started from the process working
//     directory rather than from a directory somebody gave it, such as the root
//     a server was launched with;
//   - r anchored on nothing and fell back to that start folder (MarkerCWD);
//   - subject is an absolute path; and
//   - the folder it names is there. A folder that is not cannot hold a workspace,
//     and resolving from it would root one where nothing exists, so the run keeps
//     r and fails on the missing file as it would have.
//
// The second resolution runs through the one merge path with r's own inputs —
// its environment, its platform, whether it read the global file, and the
// command-line layer laid over it — and only its start moved, to the subject's
// folder. Whatever it anchors on is the answer, including its own fallback to that
// folder when no workspace contains it, and its advisories are its own.
//
// A start folder that is a workspace is used as it is, wherever the subject sits,
// and so is a root somebody named. A relative subject is read against the
// directory the reader runs in, which is that same start folder, so naming one
// changes nothing either. An empty subject is a run with none.
//
// It is the one place the case is decided. Every surface that runs an operation
// on a named Release or document asks it, with that argument, before anything
// the workspace configures is read; none of them restates the condition.
func (r *ResolvedWorkspace) ForSubject(subject string) (*ResolvedWorkspace, error) {
	if r == nil || !filepath.IsAbs(subject) {
		return r, nil
	}
	// A named root is reported as MarkerNone, so the marker answers both "nothing
	// named a root" and "nothing anchored"; the start answers whether resolution
	// began anywhere but the working directory.
	if r.start != "" || r.marker != MarkerCWD {
		return r, nil
	}
	start := filepath.Dir(filepath.Clean(subject))
	if fi, err := os.Stat(start); err != nil || !fi.IsDir() {
		return r, nil
	}
	rw, err := resolve(start, "", r.environment(), r.machinePlatform(), r.withoutGlobal, nil)
	if err != nil {
		return nil, err
	}
	rw.start = start
	rw.lookup = r.environment()
	rw.withoutGlobal = r.withoutGlobal
	return rw.WithFlags(r.flags), nil
}
