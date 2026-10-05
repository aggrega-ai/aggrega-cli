// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// a workspace whose output directory is, one row at a time, a plain directory, a
// link leading out, and a plain directory holding a link leading out on the way
// to one draft.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: the judgement runs over the whole set before anything is written, so
// nothing may have landed there.
func TestImportDraftsAreHeldToTheWorkspace(t *testing.T) {
	files := []abi.ScaffoldFile{
		{Path: "aggregate.yaml", Content: "kind: Aggregate\n"},
		{Path: "catalog/components/api.yaml", Content: "kind: Component\n"},
	}
	rows := []struct {
		name    string
		layout  func(t *testing.T, ws, outside string) string
		refused bool
	}{
		{name: "control: an output directory inside", layout: func(t *testing.T, ws, _ string) string {
			return filepath.Join(ws, "drafts")
		}},
		{name: "an output directory that is a link leading out", refused: true, layout: func(t *testing.T, ws, outside string) string {
			symlinkOrSkipImport(t, outside, filepath.Join(ws, "drafts"))
			return filepath.Join(ws, "drafts")
		}},
		{name: "a directory on the way to a draft that is a link leading out", refused: true, layout: func(t *testing.T, ws, outside string) string {
			if err := os.MkdirAll(filepath.Join(ws, "drafts"), 0o755); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkipImport(t, outside, filepath.Join(ws, "drafts", "catalog"))
			return filepath.Join(ws, "drafts")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			out := row.layout(t, ws, outside)

			err := checkFiles(ws, out, files)
			if err == nil {
				_, err = writeFiles(out, files)
			}
			if !row.refused {
				if err != nil {
					t.Fatalf("refused drafts the workspace holds: %v", err)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			entries, rerr := os.ReadDir(outside)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(entries) != 0 {
				t.Errorf("something was written outside the workspace: %v", entries)
			}
		})
	}
}

func symlinkOrSkipImport(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
