// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestEntitySlotWritesAreHeldToTheWorkspace drives the one write every file of an
// entity slot goes through over a workspace whose artifact cache directory is, one
// row at a time, a plain directory, a link leading out, and a link pointing
// inside.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: nothing may have been written there.
func TestEntitySlotWritesAreHeldToTheWorkspace(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	rows := []struct {
		name    string
		layout  func(t *testing.T, ws, outside string)
		refused bool
	}{
		{name: "control: a cache directory inside", layout: func(*testing.T, string, string) {}},
		{name: "a cache directory that is a link leading out", refused: true, layout: func(t *testing.T, ws, outside string) {
			linkCacheDir(t, outside, filepath.Join(ws, cacheDir))
		}},
		{name: "a cache directory that is a link pointing inside", layout: func(t *testing.T, ws, _ string) {
			target := filepath.Join(ws, "shared-cache")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			linkCacheDir(t, target, filepath.Join(ws, cacheDir))
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			row.layout(t, ws, outside)

			err := WriteCached(ws, cacheDir, "Component", "oci://hub.example.invalid/component/demo/api", "1.0.0", []byte("blob"))
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a slot the workspace holds: %v", err)
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

// TestEntitySlotWritesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt drives
// the same write over a cache directory the configuration names climbing out of
// the workspace, one shape per row beside a control writing plainly beneath it.
//
// The directory is a root of its own: an entities subtree beneath it that is a
// link leading out of it is refused, and the place the link leads to is left as it
// was; the directory itself being a link is honoured where it was named, and the
// slot is written where it leads.
func TestEntitySlotWritesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt(t *testing.T) {
	const cacheDir = "../shared-cache"
	const uri = "oci://hub.example.invalid/component/demo/api"
	rows := []struct {
		name    string
		layout  func(t *testing.T, cache, elsewhere string)
		refused bool
	}{
		{name: "control: a cache directory outside the workspace", layout: func(*testing.T, string, string) {}},
		{name: "an entities subtree that is a link leading out of it is refused", refused: true, layout: func(t *testing.T, cache, elsewhere string) {
			linkCacheDir(t, elsewhere, filepath.Join(cache, "entities"))
		}},
		{name: "the cache directory itself a link is honoured where it was named", layout: func(t *testing.T, cache, elsewhere string) {
			linkCacheDir(t, elsewhere, cache)
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			elsewhere := t.TempDir()
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			row.layout(t, filepath.Join(ws, cacheDir), elsewhere)

			err := WriteCached(ws, cacheDir, "Component", uri, "1.0.0", []byte("blob"))
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if entries, rerr := os.ReadDir(elsewhere); rerr != nil || len(entries) != 0 {
					t.Errorf("something was written where the link leads: %v (%v)", entries, rerr)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a slot its root holds: %v", err)
			}
			if got, rerr := ReadCached(ws, cacheDir, "Component", uri, "1.0.0"); rerr != nil || string(got) != "blob" {
				t.Errorf("the slot does not hold what was written: %q (%v)", got, rerr)
			}
		})
	}
}

// TestEntitySlotWritesAreHeldToTheCacheDirectory drives the one write every file
// of an entity slot goes through over a workspace whose default cache directory is
// laid out one way per row, beside a control writing plainly beneath it.
//
// The cache directory is the one root of everything beneath it. A class root, or a
// file of a slot, that is a link leading out of the cache directory is refused —
// elsewhere inside the workspace as much as out of it — naming the link and the
// cache directory as the place the path must stay inside, and the place the link
// leads to holds exactly what it held before. A class root leading elsewhere inside
// the cache directory is written where it leads. The cache directory itself is
// followed where it leads inside the workspace, and refused, held to the workspace,
// where it leads out.
func TestEntitySlotWritesAreHeldToTheCacheDirectory(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const uri = "oci://hub.example.invalid/component/demo/api"
	type place struct{ ws, cache, elsewhere string }
	rows := []struct {
		name string
		// layout lays the row out and returns the directory a refusal must leave as it
		// was, the link it must name and the root it must name; a row that writes
		// returns the directory the slot must land in and two empty strings.
		layout func(t *testing.T, p place) (dir, link, root string)
	}{
		{"control: a plain cache directory", func(t *testing.T, p place) (string, string, string) {
			return filepath.Join(p.cache, "entities"), "", ""
		}},
		{"a class root linked elsewhere inside the cache directory is written where it leads", func(t *testing.T, p place) (string, string, string) {
			kept := filepath.Join(p.cache, "kept-entities")
			mustMkdir(t, kept)
			linkCacheDir(t, "kept-entities", filepath.Join(p.cache, "entities"))
			return kept, "", ""
		}},
		{"a class root linked out of the cache directory, elsewhere inside the workspace, is refused", func(t *testing.T, p place) (string, string, string) {
			kept := filepath.Join(p.ws, "kept")
			seedSlotFile(t, kept, "README")
			linkCacheDir(t, filepath.Join("..", "..", "kept"), filepath.Join(p.cache, "entities"))
			return kept, filepath.Join(p.cache, "entities"), p.cache
		}},
		{"a class root linked out of the workspace is refused", func(t *testing.T, p place) (string, string, string) {
			seedSlotFile(t, p.elsewhere, "README")
			linkCacheDir(t, p.elsewhere, filepath.Join(p.cache, "entities"))
			return p.elsewhere, filepath.Join(p.cache, "entities"), p.cache
		}},
		{"a slot's file linked to a workspace file outside the cache directory is refused", func(t *testing.T, p place) (string, string, string) {
			kept := filepath.Join(p.ws, "kept")
			seedSlotFile(t, kept, "blob")
			slot := slotDir(t, p.ws, cacheDir, uri, "1.0.0")
			mustMkdir(t, slot)
			linkCacheDir(t, filepath.Join(kept, "blob"), filepath.Join(slot, "blob"))
			return kept, filepath.Join(slot, "blob"), p.cache
		}},
		{"the cache directory itself linked elsewhere inside the workspace is written where it leads", func(t *testing.T, p place) (string, string, string) {
			kept := filepath.Join(p.ws, "state", "cache")
			mustMkdir(t, kept)
			linkCacheDir(t, filepath.Join("..", "state", "cache"), p.cache)
			return filepath.Join(kept, "entities"), "", ""
		}},
		{"the cache directory itself linked out of the workspace is refused, held to the workspace", func(t *testing.T, p place) (string, string, string) {
			seedSlotFile(t, p.elsewhere, "README")
			linkCacheDir(t, p.elsewhere, p.cache)
			return p.elsewhere, p.cache, p.ws
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			mustMkdir(t, ws)
			p := place{ws: ws, cache: filepath.Join(ws, cacheDir), elsewhere: t.TempDir()}
			dir, link, root := row.layout(t, p)
			before := listTree(t, dir)

			err := WriteCached(ws, cacheDir, "Component", uri, "1.0.0", []byte("blob"))
			if link == "" {
				if err != nil {
					t.Fatalf("refused a slot the cache directory holds: %v", err)
				}
				if after := listTree(t, dir); len(after) == len(before) {
					t.Errorf("the slot did not land in %s: %v", dir, after)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			for _, want := range []string{"the link " + link + " leads to ", "which must stay inside " + root} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q:\n%v", want, err)
				}
			}
			if after := listTree(t, dir); strings.Join(after, "\n") != strings.Join(before, "\n") {
				t.Errorf("the place the link leads to changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

// listTree lists every file beneath dir with its content, in walk order, or
// nothing where dir does not exist.
func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir+string(filepath.Separator), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out = append(out, path+"="+string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func linkCacheDir(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
