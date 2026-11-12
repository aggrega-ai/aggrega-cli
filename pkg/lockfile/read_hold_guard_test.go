// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot is this module's root, relative to this package's directory, where
// the test runs.
const moduleRoot = "../.."

// heldLockfileReads are the entry points through which the product reads a
// Release's lockfile, each holding the read to the root a write of the same file
// is held to before anything is opened.
var heldLockfileReads = []string{"Load", "LoadIfReadable", "CheckRead"}

// TestEveryLockfileReadIsHeldToTheReleasesRoot keeps every read of a Release's
// lockfile behind the hold its write already has. A production reader outside
// this package calling LoadLockfile, the unheld read, would read a lockfile that
// is a link leading out of the workspace — pins from elsewhere on the machine
// deciding what a build, a check or the editor resolves — which is the read this
// guard exists to refuse.
//
// It FAILS rather than reports, because a Go package that passes prints nothing.
// The walk's own reach is asserted, and so is a held read being found, so an
// empty walk or a renamed entry point cannot pass as a clean tree.
//
// 🔴 What it cannot see: a reader opening the lockfile's bytes itself, by a
// filesystem call rather than through this package. Those are the write path's
// reads of the file it is about to replace — after the write's own hold has judged
// the same path — and no such reader exists for pins; a new one is a defect its
// own tests have to catch.
func TestEveryLockfileReadIsHeldToTheReleasesRoot(t *testing.T) {
	fset := token.NewFileSet()
	visited, held := 0, 0
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, moduleRoot+string(filepath.Separator)))
		if d.IsDir() {
			if d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") && path != moduleRoot ||
				rel == "internal/buildtools" || rel == "internal/tools" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(rel, "pkg/lockfile/") {
			return nil
		}
		node, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		visited++
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "lockfile" {
				return true
			}
			switch {
			case sel.Sel.Name == "LoadLockfile":
				t.Errorf("%s: lockfile.LoadLockfile read outside the lockfile package — read a Release's lockfile "+
					"with lockfile.Load or lockfile.LoadIfReadable, which hold the read to the root a write of the "+
					"same file is held to, so a lockfile that is a link leading out is refused rather than read",
					fset.Position(sel.Pos()))
			case listedIn(heldLockfileReads, sel.Sel.Name):
				held++
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module's production source: %v", err)
	}
	if visited == 0 {
		t.Fatal("the walk visited no production Go file — the guard is inert, not the tree clean")
	}
	if held == 0 {
		t.Fatalf("no production reader was found reading a lockfile through %v — an entry point renamed leaves "+
			"this guard watching nothing; rename it here in the same change", heldLockfileReads)
	}
}

func listedIn(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
