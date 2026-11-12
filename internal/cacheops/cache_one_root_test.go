// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheops

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestTheCacheDirectoryIsTheOneRootOfEveryInventoryOperation drives the cache
// status, a prune and a clear, each through its own entry, over the workspace's
// default cache directory laid out one way per row, every row holding one slot no
// lockfile references.
//
// The cache directory is the one root of everything beneath it. A class root that
// is a link leading out of it — elsewhere inside the workspace as much as out of
// the workspace — is not read by the status, which halts with the read hold naming
// the link, and refuses a prune and a clear with the removal condition; a file of a
// slot that is such a link is not read by any of the three, which halt on it before
// anything is removed. In every refused cell the link is named, the cache directory
// is named as the place the path must stay inside, the place the link leads to
// holds afterwards exactly what it held before, and the link is still there. A
// class root that is a link leading elsewhere inside the cache directory is listed,
// pruned and cleared where it leads, as a plain one is.
func TestTheCacheDirectoryIsTheOneRootOfEveryInventoryOperation(t *testing.T) {
	slotRel := entitySlotRel()
	type place struct{ ws, cache, elsewhere string }
	// A layout lays the row out and returns the directory a refused cell must leave
	// as it was and the link it must name; a row every operation reads returns the
	// directory the slot lies in and no link.
	layouts := []struct {
		name   string
		entry  bool
		layout func(t *testing.T, p place) (held, link string)
	}{
		{"control: a plain class root", false, func(t *testing.T, p place) (string, string) {
			seedSlot(t, filepath.Join(p.cache, "entities", slotRel))
			return filepath.Join(p.cache, "entities"), ""
		}},
		{"a class root linked elsewhere inside the cache directory", false, func(t *testing.T, p place) (string, string) {
			seedSlot(t, filepath.Join(p.cache, "kept-entities", slotRel))
			linkInto(t, "kept-entities", filepath.Join(p.cache, "entities"))
			return filepath.Join(p.cache, "kept-entities"), ""
		}},
		{"a class root linked out of the cache directory, elsewhere inside the workspace", false, func(t *testing.T, p place) (string, string) {
			kept := filepath.Join(p.ws, "kept-entities")
			seedSlot(t, filepath.Join(kept, slotRel))
			linkInto(t, filepath.Join("..", "..", "kept-entities"), filepath.Join(p.cache, "entities"))
			return kept, filepath.Join(p.cache, "entities")
		}},
		{"a class root linked out of the workspace", false, func(t *testing.T, p place) (string, string) {
			seedSlot(t, filepath.Join(p.elsewhere, slotRel))
			linkInto(t, p.elsewhere, filepath.Join(p.cache, "entities"))
			return p.elsewhere, filepath.Join(p.cache, "entities")
		}},
		{"a slot's file linked out of the cache directory, elsewhere inside the workspace", true, func(t *testing.T, p place) (string, string) {
			kept := filepath.Join(p.ws, "kept")
			seedSlot(t, kept)
			slot := filepath.Join(p.cache, "entities", filepath.FromSlash(slotRel))
			if err := os.MkdirAll(slot, 0o755); err != nil {
				t.Fatal(err)
			}
			linkInto(t, filepath.Join(kept, "blob"), filepath.Join(slot, "blob"))
			return kept, filepath.Join(slot, "blob")
		}},
	}
	operations := []struct {
		name string
		// run performs the operation and reports whether it read the slot: listed it,
		// or removed it.
		run func(t *testing.T, loc Locations) (bool, error)
		// removes marks the operations that remove what they reach.
		removes bool
	}{
		{"status", func(t *testing.T, loc Locations) (bool, error) {
			report, err := Status(t.Context(), loc)
			if err != nil {
				return false, err
			}
			for _, cl := range report.Classes {
				if cl.Name == "workspace entity cache" && len(cl.Elements) == 1 {
					return true, nil
				}
			}
			return false, nil
		}, false},
		{"prune", func(t *testing.T, loc Locations) (bool, error) {
			res, err := Prune(t.Context(), loc)
			return len(res.Removed) == 1, err
		}, true},
		{"clear", func(t *testing.T, loc Locations) (bool, error) {
			res, err := Clear(t.Context(), loc)
			for _, c := range res.Cleared {
				if c.Class == "workspace entity cache" {
					return true, err
				}
			}
			return false, err
		}, true},
	}
	for _, l := range layouts {
		for _, op := range operations {
			t.Run(op.name+"/"+l.name, func(t *testing.T) {
				ws := filepath.Join(t.TempDir(), "ws")
				p := place{ws: ws, cache: filepath.Join(ws, testCacheDir), elsewhere: t.TempDir()}
				if err := os.MkdirAll(p.cache, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := lockfile.WriteLockfile(filepath.Join(ws, "release.lock"), &lockfile.Lockfile{
					Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z",
				}); err != nil {
					t.Fatal(err)
				}
				held, link := l.layout(t, p)
				before := tree(t, held)
				loc := Locations{WorkspaceRoot: ws, CacheDir: testCacheDir}

				reached, err := op.run(t, loc)
				if link == "" {
					if err != nil {
						t.Fatalf("refused a cache its directory holds: %v", err)
					}
					if !reached {
						t.Fatalf("the %s did not reach the slot laid in %s", op.name, held)
					}
					if op.removes {
						if _, serr := os.Stat(filepath.Join(held, filepath.FromSlash(slotRel))); !os.IsNotExist(serr) {
							t.Errorf("the %s left the slot where the class root leads: %v", op.name, serr)
						}
					}
					return
				}
				want := writescope.ErrWriteLeadsOutOfWorkspace()
				if op.name == "status" || l.entry {
					want = readscope.ErrReadLeadsOutOfWorkspace()
				}
				if !errors.Is(err, want) {
					t.Fatalf("want %v, got %v", want, err)
				}
				for _, says := range []string{"the link " + link + " leads to ", "which must stay inside " + p.cache} {
					if !strings.Contains(err.Error(), says) {
						t.Errorf("the refusal does not say %q:\n%v", says, err)
					}
				}
				if after := tree(t, held); !reflect.DeepEqual(before, after) {
					t.Errorf("the place the link leads to changed:\nbefore %v\nafter  %v", before, after)
				}
				if info, lerr := os.Lstat(link); lerr != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Errorf("the link is no longer there as a link: %v", lerr)
				}
			})
		}
	}
}

// TestAClearEmptiesAMachineWideStoreWhereItLeadsAndKeepsTheLink drives a clear
// over the machine-wide compiled-module cache, its directory itself (the control)
// and a link to the directory its owner keeps it in.
//
// A machine-wide store is one its owner names, so it is emptied where it leads: the
// inventory lists what lies there, the clear removes that and reports the bytes it
// removed there, and the link the owner placed is kept. Removing the link instead
// would report as reclaimed bytes still on disk and take away the owner's link.
func TestAClearEmptiesAMachineWideStoreWhereItLeadsAndKeepsTheLink(t *testing.T) {
	for _, row := range []struct {
		name   string
		linked bool
	}{
		{"control: the compiled-module cache directory itself", false},
		{"the compiled-module cache directory a link to where its owner keeps it", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "wasm-compile")
			kept := root
			if row.linked {
				kept = filepath.Join(base, "compile-kept")
			}
			module := strings.Repeat("m", 64)
			if err := os.MkdirAll(filepath.Join(kept, "wazero-v1-amd64-linux"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(kept, "wazero-v1-amd64-linux", "module"), []byte(module), 0o644); err != nil {
				t.Fatal(err)
			}
			if row.linked {
				linkInto(t, kept, root)
			}
			loc := Locations{CompileDir: root}

			report, err := Status(t.Context(), loc)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			var listed int64
			for _, cl := range report.Classes {
				if cl.Name == CompiledModuleClassName {
					for _, el := range cl.Elements {
						listed += el.Size
					}
				}
			}
			if listed != int64(len(module)) {
				t.Fatalf("the inventory lists %d bytes; want the %d laid where the store leads", listed, len(module))
			}
			res, err := Clear(t.Context(), loc)
			if err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if res.ReclaimedBytes != listed {
				t.Errorf("the clear reports %d bytes reclaimed; want the %d the inventory listed", res.ReclaimedBytes, listed)
			}
			if !row.linked {
				if _, serr := os.Lstat(root); !os.IsNotExist(serr) {
					t.Errorf("the store survived a clear: %v", serr)
				}
				return
			}
			if info, lerr := os.Lstat(root); lerr != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("the link the owner placed did not survive the clear: %v", lerr)
			}
			entries, rerr := os.ReadDir(kept)
			if rerr != nil {
				t.Fatalf("the directory the link leads to is gone rather than emptied: %v", rerr)
			}
			if len(entries) != 0 {
				t.Errorf("the directory the link leads to still holds %d entries after the clear", len(entries))
			}
		})
	}
}
