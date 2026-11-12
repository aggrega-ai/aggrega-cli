// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestImportDraftsAreHeldToTheWorkspace drives the judgement an import makes over
// its drafts before it writes them, and before a reporting delivery answers, over
// an output directory that is, one row at a time, a plain directory inside the
// workspace, one that is itself a link leading out, one inside holding a link
// leading out on the way to one draft and one holding a link to elsewhere inside,
// and a plain directory outside the workspace with and without a link leading out
// of it on the way.
//
// The output directory is the one the caller named, so it is honoured where it
// was named — followed, if it is a link — and the drafts are held to the root it
// picks: the workspace root where it lies inside the workspace, and itself where
// it lies outside. A refused row is held to the directory the link leads to as
// well as to the condition: the judgement runs over the whole set before anything
// is written, so nothing may have landed there. An admitted row is held to where
// the draft under the linked directory landed, so a write that went somewhere
// else does not pass.
func TestImportDraftsAreHeldToTheWorkspace(t *testing.T) {
	files := []abi.ScaffoldFile{
		{Path: "aggregate.yaml", Content: "kind: Aggregate\n"},
		{Path: "catalog/components/api.yaml", Content: "kind: Component\n"},
	}
	rows := []struct {
		name string
		// layout returns the output directory and, for an admitted row, the place
		// the draft catalog/components/api.yaml lands at on disk.
		layout  func(t *testing.T, ws, outside, elsewhere string) (out, lands string)
		refused bool
	}{
		{name: "control: an output directory inside", layout: func(t *testing.T, ws, _, _ string) (string, string) {
			return filepath.Join(ws, "drafts"), filepath.Join(ws, "drafts", "catalog", "components", "api.yaml")
		}},
		{name: "an output directory that is itself a link leading out is followed where it was named", layout: func(t *testing.T, ws, outside, _ string) (string, string) {
			symlinkOrSkipImport(t, outside, filepath.Join(ws, "drafts"))
			return filepath.Join(ws, "drafts"), filepath.Join(outside, "catalog", "components", "api.yaml")
		}},
		{name: "a directory on the way to a draft that is a link leading out", refused: true, layout: func(t *testing.T, ws, _, elsewhere string) (string, string) {
			mkdirImport(t, filepath.Join(ws, "drafts"))
			symlinkOrSkipImport(t, elsewhere, filepath.Join(ws, "drafts", "catalog"))
			return filepath.Join(ws, "drafts"), ""
		}},
		{name: "a directory on the way to a draft that is a link to elsewhere inside the workspace is followed", layout: func(t *testing.T, ws, _, _ string) (string, string) {
			mkdirImport(t, filepath.Join(ws, "drafts"))
			mkdirImport(t, filepath.Join(ws, "shared"))
			symlinkOrSkipImport(t, filepath.Join(ws, "shared"), filepath.Join(ws, "drafts", "catalog"))
			return filepath.Join(ws, "drafts"), filepath.Join(ws, "shared", "components", "api.yaml")
		}},
		{name: "an output directory outside the workspace is written beneath", layout: func(t *testing.T, _, outside, _ string) (string, string) {
			return outside, filepath.Join(outside, "catalog", "components", "api.yaml")
		}},
		{name: "a directory on the way to a draft beneath an outside output directory leading out of it", refused: true, layout: func(t *testing.T, _, outside, elsewhere string) (string, string) {
			symlinkOrSkipImport(t, elsewhere, filepath.Join(outside, "catalog"))
			return outside, ""
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
			out, lands := row.layout(t, ws, outside, elsewhere)

			err := checkFiles(ws, out, files)
			if err == nil {
				_, err = writeFiles(out, files)
			}
			if !row.refused {
				if err != nil {
					t.Fatalf("refused drafts their root holds: %v", err)
				}
				if _, serr := os.Stat(lands); serr != nil {
					t.Errorf("the draft did not land at %s: %v", lands, serr)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			entries, rerr := os.ReadDir(elsewhere)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(entries) != 0 {
				t.Errorf("something was written where the link leads: %v", entries)
			}
		})
	}
}

func mkdirImport(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlinkOrSkipImport(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
