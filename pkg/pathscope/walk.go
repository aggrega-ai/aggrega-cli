// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope

import (
	"io/fs"
	"os"
	"path/filepath"
)

// WalkDir walks the tree beneath root as [filepath.WalkDir] does, with one
// difference: a root that is itself a symbolic link to a directory is walked
// where it leads, and every path handed to fn is still spelled beneath root as
// root is spelled.
//
// filepath.WalkDir looks at its root without following it, so a root that is a
// link is reported as the link it is and nothing beneath it is walked. A
// directory somebody opened through a link is the directory it leads to, and a
// walk of it that found nothing would answer as though that directory were empty.
//
// Only the root is followed. A link met beneath it is handed to fn as the link it
// is and is not entered, exactly as filepath.WalkDir hands one on, so a caller
// still judges each such link where it meets it. A root that is a link leading
// nowhere, or to anything but a directory, is walked as it stands.
//
// fn is handed root itself spelled exactly as it was passed, with the entry of the
// directory it leads to, named as root names it; the paths beneath are joined to
// root and so spelled beneath it. Everything else — the order, what an error or
// [fs.SkipDir] or [fs.SkipAll] from fn does — is filepath.WalkDir's.
//
// It decides nothing about where a path leads: whether a link met beneath root
// lies inside a root it is held to is [ResolvesInside]'s question.
func WalkDir(root string, fn fs.WalkDirFunc) error {
	start := root
	if info, err := os.Lstat(root); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		if target, err := os.Stat(root); err == nil && target.IsDir() {
			// A trailing separator makes the walk's first look at root follow the
			// link standing there; every path beneath is joined to it, and so is
			// spelled beneath root.
			start = root + string(filepath.Separator)
		}
	}
	if start == root {
		return filepath.WalkDir(root, fn)
	}
	return filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if path == start {
			path = root
		}
		return fn(path, d, err)
	})
}
