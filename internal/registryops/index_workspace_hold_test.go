// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestTheHostIndexIsHeldToTheWorkspace drives the index write — which removes the
// host's index directory and lays it out again — over a workspace whose artifact
// cache directory is, one row at a time, a plain directory and a link leading out
// holding an earlier index.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: the earlier index there must still be there, because the write's
// first act is a removal.
func TestTheHostIndexIsHeldToTheWorkspace(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const host = "registry.example.invalid"
	idx := HostIndex{Host: host, Records: []registryindex.Record{{
		URI: "oci://" + host + "/component/core/api", Name: "component/core/api", Version: "1.0.0", Kind: "Component",
	}}}
	rows := []struct {
		name    string
		link    bool
		refused bool
	}{
		{name: "control: a cache directory inside"},
		{name: "a cache directory that is a link leading out", link: true, refused: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			if row.link {
				// An earlier index the link leads to, written by the product's own
				// writer through a workspace of its own.
				if err := WriteHostIndex(outside, ".", idx, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
					t.Fatalf("seed the earlier index: %v", err)
				}
				if err := os.MkdirAll(filepath.Join(ws, ".aggrega"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(ws, cacheDir)); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			before := snapshotFiles(t, outside)

			err := WriteHostIndex(ws, cacheDir, HostIndex{Host: host}, time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC))
			if !row.refused {
				if err != nil {
					t.Fatalf("refused an index the workspace holds: %v", err)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if len(before) == 0 {
				t.Fatal("precondition: no earlier index was laid out outside the workspace")
			}
			if after := snapshotFiles(t, outside); !reflect.DeepEqual(before, after) {
				t.Errorf("the earlier index outside the workspace was changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

// TestTheHostIndexBeneathACacheDirectoryOutsideTheWorkspaceIsHeldToIt drives the
// same index write over a cache directory the configuration names climbing out of
// the workspace, one shape per row beside a control writing plainly beneath it.
//
// The directory is a root of its own: an index subtree beneath it that is a link
// leading out of it, holding an earlier index, is refused, and that earlier index
// is left exactly as it was — the write's first act is a removal; the directory
// itself being a link is honoured where it was named, and the index is laid out
// where it leads.
func TestTheHostIndexBeneathACacheDirectoryOutsideTheWorkspaceIsHeldToIt(t *testing.T) {
	const cacheDir = "../shared-cache"
	const host = "registry.example.invalid"
	idx := HostIndex{Host: host, Records: []registryindex.Record{{
		URI: "oci://" + host + "/component/core/api", Name: "component/core/api", Version: "1.0.0", Kind: "Component",
	}}}
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	rows := []struct {
		name    string
		layout  func(t *testing.T, cache, elsewhere string)
		refused bool
	}{
		{name: "control: a cache directory outside the workspace", layout: func(*testing.T, string, string) {}},
		{name: "an index subtree that is a link leading out of it is refused", refused: true, layout: func(t *testing.T, cache, elsewhere string) {
			// An earlier index the link leads to, written by the product's own writer
			// through a workspace of its own.
			if err := WriteHostIndex(elsewhere, ".", idx, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
				t.Fatalf("seed the earlier index: %v", err)
			}
			link(t, filepath.Join(elsewhere, "index"), filepath.Join(cache, "index"))
		}},
		{name: "the cache directory itself a link is honoured where it was named", layout: func(t *testing.T, cache, elsewhere string) {
			link(t, elsewhere, cache)
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
			before := snapshotFiles(t, elsewhere)

			err := WriteHostIndex(ws, cacheDir, idx, time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC))
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if len(before) == 0 {
					t.Fatal("precondition: no earlier index was laid out where the link leads")
				}
				if after := snapshotFiles(t, elsewhere); !reflect.DeepEqual(before, after) {
					t.Errorf("the earlier index where the link leads was changed:\nbefore %v\nafter  %v", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused an index its root holds: %v", err)
			}
			if _, serr := os.Stat(filepath.Join(ws, cacheDir, "index")); serr != nil {
				t.Errorf("no index was laid out beneath the cache directory: %v", serr)
			}
		})
	}
}

// snapshotFiles is every file under dir with its bytes.
func snapshotFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	return out
}
