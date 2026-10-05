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
