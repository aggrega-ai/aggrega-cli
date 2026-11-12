// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// catalogWalk is how one walk of a catalog directory answers the two things a
// walk can meet besides a document it reads: an entry it cannot read, and a link
// leading out of the root the directory is held to.
//
// Each is a function rather than a flag, because the walks differ in exactly
// these two answers and in nothing else. A whole-catalog load stops on both. The
// lazy lookup's indexes find what they can, passing both over — a link leading out
// is then not read, and so names nothing in any index — and the check a compile
// asks of that lookup stops on the second and passes over the first, so a catalog
// directory missing from the workspace is answered as the lookup has always
// answered it while a link leading out halts the compile.
type catalogWalk struct {
	workspaceRoot string
	cacheDir      string
	// named is the part of the walked stack somebody named, handed on unjudged to
	// [workspaceconfig.CatalogDirRoot]; nil holds the conventional directory to the
	// workspace.
	named []string
	// unreadable answers an entry the walk cannot read — a directory that does not
	// exist or cannot be listed. A nil return passes over it.
	unreadable func(err error) error
	// leadsOut answers an entry whose links lead out of the directory's root, with
	// the halt readscope raises for it. A nil return passes over it unread.
	leadsOut func(err error) error
}

// walk walks the catalog directory dir, handing visit every YAML document beneath
// it that it may read, in the order filepath.WalkDir visits them, with the
// workspace's artifact cache stepped over.
//
// # A link is judged where the walk meets it
//
// Every entry is held to the root the directory picks
// ([workspaceconfig.CatalogDirRoot], given the part of the stack somebody named)
// by [readscope.CheckRead], the one judgement of whether a read leads out:
//
//   - dir itself being a link is honoured where it leads when that place is inside
//     its root — a directory somebody named, the conventional one included, is a
//     root of its own wherever it lies, and the conventional directory where nobody
//     named it must lead to a place inside the workspace — and its entries are named
//     beneath the link, as they are spelled; otherwise nothing beneath it is read
//     and leadsOut answers.
//   - A link beneath dir that leads to a directory, or to a file named like a YAML
//     document, is answered by leadsOut where it leads out: it is not read, and
//     nothing beneath it is. One leading inside is read where the walk meets it, as
//     a file is, and a link to a directory is not entered, exactly as a directory
//     link always was: that directory is walked where it actually sits, if a
//     catalog directory holds it.
//   - Any other link — to a file no walk of a catalog reads, or leading nowhere —
//     is met as it always was: one named like a YAML document is handed to visit,
//     whose read then fails as a missing file does.
func (w catalogWalk) walk(dir string, visit func(path string) error) error {
	root := workspaceconfig.CatalogDirRoot(w.workspaceRoot, dir, w.named)
	if info, err := os.Lstat(dir); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		if err := readscope.CheckRead(root, dir); err != nil {
			return w.leadsOut(err)
		}
	}
	// A dir that is a link, judged above, is walked where it leads while every path
	// handed on is still spelled beneath dir (pathscope.WalkDir). A link leading
	// nowhere is walked as it stands, which finds nothing beneath it.
	return pathscope.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return w.unreadable(err)
		}
		if d.IsDir() {
			return SkipDirIfArtifactCache(w.workspaceRoot, path, w.cacheDir)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if SkipDirIfArtifactCache(w.workspaceRoot, path, w.cacheDir) == fs.SkipDir {
				return nil
			}
			if err := readscope.CheckRead(root, path); err != nil && readsWhereItLeads(path) {
				return w.leadsOut(err)
			}
		}
		if !isYAMLDocument(path) {
			return nil
		}
		return visit(path)
	})
}

// readsWhereItLeads reports whether a catalog walk would read anything through
// the link at path: it leads to a directory, which may hold documents, or it is
// named like a YAML document and leads to a file. A link to anything else
// carries nothing a catalog walk reads.
func readsWhereItLeads(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir() || isYAMLDocument(path)
}

// isYAMLDocument reports whether path is named like a YAML document, the one
// shape of file a catalog walk reads.
func isYAMLDocument(path string) bool {
	return strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")
}

// WalkDocuments walks the catalog directory dir as the lookup's own indexes walk
// it, handing visit every YAML document beneath it that may be read, with the
// workspace's artifact cache stepped over. It is the one walk of a catalog
// directory, for a reader outside this package that reads every document a
// directory holds — the conventions check reports on each. named is the part of
// dir's stack somebody named ([workspaceconfig.ResolvedWorkspace.NamedCatalogDirs]).
//
// A directory it cannot enter is passed over, as the lookup's indexes pass it
// over. A link leading out of the root the directory is held to is not read and
// stops the walk with the [readscope.ErrReadLeadsOutOfWorkspace] halt naming the
// link, as a compile's CheckLinks stops on it: a reader reading every document a
// directory holds answers for all of them, and an answer that passed over a
// document behind such a link in silence would read as complete. An error visit
// returns stops the walk and is returned.
func WalkDocuments(workspaceRoot, dir, cacheDir string, named []string, visit func(path string) error) error {
	w := findingWhatItCan(workspaceRoot, cacheDir, named)
	w.leadsOut = stopOn
	return w.walk(dir, visit)
}

// findingWhatItCan is the walk that passes over an entry it cannot read and a
// link leading out of a directory's root alike, so the reader running it holds
// what it can read and nothing it may not.
func findingWhatItCan(workspaceRoot, cacheDir string, named []string) catalogWalk {
	return catalogWalk{workspaceRoot: workspaceRoot, cacheDir: cacheDir, named: named, unreadable: passOver, leadsOut: passOver}
}

// stopOn returns err, for a walk that stops on what it met.
func stopOn(err error) error { return err }

// passOver returns nil, for a walk that passes over what it met.
func passOver(error) error { return nil }

// leadsOutOfCatalog reports whether err carries the halt a catalog walk raises for
// a link leading out of a directory's root.
func leadsOutOfCatalog(err error) bool {
	return errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace())
}
