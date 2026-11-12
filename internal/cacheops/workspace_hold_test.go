// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestClearAndPruneHoldTheWorkspaceCacheToTheWorkspace drives a clear and a prune
// over a workspace whose artifact cache is, one row at a time, a plain directory,
// a link leading out, and a plain directory whose entity class root is a link
// leading out — each holding one slot no lockfile references, which a prune
// removes and a clear removes with everything else.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: a clear and a prune are deletions, and what the link points at must
// hold afterwards exactly what it held before.
func TestClearAndPruneHoldTheWorkspaceCacheToTheWorkspace(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	slot := filepath.Join("entities", entitySlotRel())
	rows := []struct {
		name    string
		prune   bool
		layout  func(t *testing.T, ws, outside string)
		refused bool
	}{
		{name: "control: clear a cache inside", layout: func(t *testing.T, ws, _ string) {
			seedSlot(t, filepath.Join(ws, cacheDir, slot))
		}},
		{name: "control: prune a cache inside", prune: true, layout: func(t *testing.T, ws, _ string) {
			seedSlot(t, filepath.Join(ws, cacheDir, slot))
		}},
		{name: "clear through a cache directory that is a link leading out", refused: true, layout: func(t *testing.T, ws, outside string) {
			seedSlot(t, filepath.Join(outside, slot))
			linkInto(t, outside, filepath.Join(ws, cacheDir))
		}},
		{name: "prune through a cache directory that is a link leading out", prune: true, refused: true, layout: func(t *testing.T, ws, outside string) {
			seedSlot(t, filepath.Join(outside, slot))
			linkInto(t, outside, filepath.Join(ws, cacheDir))
		}},
		{name: "clear through a class root that is a link leading out", refused: true, layout: func(t *testing.T, ws, outside string) {
			seedSlot(t, filepath.Join(outside, entitySlotRel()))
			linkInto(t, outside, filepath.Join(ws, cacheDir, "entities"))
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			if err := lockfile.WriteLockfile(filepath.Join(ws, "release.lock"), &lockfile.Lockfile{
				Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			row.layout(t, ws, outside)
			before := tree(t, outside)
			loc := Locations{WorkspaceRoot: ws, CacheDir: cacheDir}

			var err error
			if row.prune {
				_, err = Prune(t.Context(), loc)
			} else {
				_, err = Clear(t.Context(), loc)
			}
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a cache the workspace holds: %v", err)
				}
				if _, serr := os.Stat(filepath.Join(ws, cacheDir, slot)); !os.IsNotExist(serr) {
					t.Errorf("the unreferenced slot survived: %v", serr)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if after := tree(t, outside); !reflect.DeepEqual(before, after) {
				t.Errorf("the directory the link leads to changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

// TestClearAndPruneHoldACacheDirectoryOutsideTheWorkspaceToItself drives a clear
// and a prune over a cache directory the configuration names climbing out of the
// workspace, and over the default one inside it, each holding one slot no lockfile
// references. One shape per row beside a control clearing and one pruning plainly
// beneath the directory outside.
//
// Each cache directory is the root of everything beneath it. A class root beneath
// it that is a link leading out of it is refused, by a clear and by a prune — for
// the default directory inside the workspace, a link to elsewhere inside the
// workspace as much as one leading out of it — and the place the link leads to
// holds afterwards exactly what it held before. A class root linked elsewhere
// inside the cache directory is cleared and pruned where it leads. The directory
// outside being itself a link is honoured where it was named, and the clear empties
// the place it leads to.
func TestClearAndPruneHoldACacheDirectoryOutsideTheWorkspaceToItself(t *testing.T) {
	const outsideCache = "../shared-cache"
	slot := filepath.Join("entities", entitySlotRel())
	rows := []struct {
		name     string
		cacheDir string
		prune    bool
		// layout lays the cache out, given the cache directory as the writers spell
		// it, the workspace root and a directory outside both, and returns the
		// directory a refused row must leave exactly as it was.
		layout  func(t *testing.T, cache, ws, elsewhere string) string
		refused bool
	}{
		{name: "control: clear a cache outside the workspace", cacheDir: outsideCache, layout: func(t *testing.T, cache, _, elsewhere string) string {
			seedSlot(t, filepath.Join(cache, slot))
			return elsewhere
		}},
		{name: "control: prune a cache outside the workspace", cacheDir: outsideCache, prune: true, layout: func(t *testing.T, cache, _, elsewhere string) string {
			seedSlot(t, filepath.Join(cache, slot))
			return elsewhere
		}},
		{name: "clear through a class root beneath a cache outside that is a link leading out of it", cacheDir: outsideCache, refused: true,
			layout: func(t *testing.T, cache, _, elsewhere string) string {
				seedSlot(t, filepath.Join(elsewhere, entitySlotRel()))
				linkInto(t, elsewhere, filepath.Join(cache, "entities"))
				return elsewhere
			}},
		{name: "prune through a class root beneath a cache outside that is a link leading out of it", cacheDir: outsideCache, prune: true, refused: true,
			layout: func(t *testing.T, cache, _, elsewhere string) string {
				seedSlot(t, filepath.Join(elsewhere, entitySlotRel()))
				linkInto(t, elsewhere, filepath.Join(cache, "entities"))
				return elsewhere
			}},
		{name: "clear a cache outside that is itself a link, honoured where it was named", cacheDir: outsideCache,
			layout: func(t *testing.T, cache, _, elsewhere string) string {
				seedSlot(t, filepath.Join(elsewhere, slot))
				linkInto(t, elsewhere, cache)
				return elsewhere
			}},
		{name: "clear through a class root of the cache inside linked out of it, elsewhere inside the workspace", cacheDir: ".aggrega/cache", refused: true,
			layout: func(t *testing.T, cache, ws, _ string) string {
				kept := filepath.Join(ws, "kept-entities")
				seedSlot(t, filepath.Join(kept, entitySlotRel()))
				linkInto(t, kept, filepath.Join(cache, "entities"))
				return kept
			}},
		{name: "prune through a class root of the cache inside linked out of it, elsewhere inside the workspace", cacheDir: ".aggrega/cache", prune: true, refused: true,
			layout: func(t *testing.T, cache, ws, _ string) string {
				kept := filepath.Join(ws, "kept-entities")
				seedSlot(t, filepath.Join(kept, entitySlotRel()))
				linkInto(t, kept, filepath.Join(cache, "entities"))
				return kept
			}},
		{name: "clear a cache inside whose class root links elsewhere inside the cache directory", cacheDir: ".aggrega/cache",
			layout: func(t *testing.T, cache, _, elsewhere string) string {
				seedSlot(t, filepath.Join(cache, "kept-entities", entitySlotRel()))
				linkInto(t, "kept-entities", filepath.Join(cache, "entities"))
				return elsewhere
			}},
		{name: "prune a cache inside whose class root links elsewhere inside the cache directory", cacheDir: ".aggrega/cache", prune: true,
			layout: func(t *testing.T, cache, _, elsewhere string) string {
				seedSlot(t, filepath.Join(cache, "kept-entities", entitySlotRel()))
				linkInto(t, "kept-entities", filepath.Join(cache, "entities"))
				return elsewhere
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			elsewhere := t.TempDir()
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := lockfile.WriteLockfile(filepath.Join(ws, "release.lock"), &lockfile.Lockfile{
				Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(ws, row.cacheDir)
			held := row.layout(t, cache, ws, elsewhere)
			before := tree(t, held)
			loc := Locations{WorkspaceRoot: ws, CacheDir: row.cacheDir}

			var err error
			if row.prune {
				_, err = Prune(t.Context(), loc)
			} else {
				_, err = Clear(t.Context(), loc)
			}
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a cache its root holds: %v", err)
				}
				if _, serr := os.Stat(filepath.Join(cache, slot)); !os.IsNotExist(serr) {
					t.Errorf("the unreferenced slot survived: %v", serr)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if after := tree(t, held); !reflect.DeepEqual(before, after) {
				t.Errorf("the directory the link leads to changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func seedSlot(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob"), []byte("not referenced by any lockfile"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func linkInto(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}

// tree is every entry under dir with each file's bytes.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	return out
}
