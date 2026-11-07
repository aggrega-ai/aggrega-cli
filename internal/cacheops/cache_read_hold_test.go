// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheops

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// cacheReadLayout lays a workspace's cache out for one row and returns the link a
// refusal must name, or "" for a layout every operation reads.
type cacheReadLayout func(t *testing.T, ws, cache, elsewhere string) string

// cacheReadLayouts are the cache shapes the inventory's reads are driven over: a
// plain cache inside the workspace; a cache directory that is a link leading out; a
// slot's blob, a source slot's metadata or an external-input blob that is a link
// leading out; a class root that is a link leading out where nothing beneath it is
// a file the enumeration opens; and a cache directory the configuration names
// outside the workspace, plainly and with a blob linking out of it.
func cacheReadLayouts() []struct {
	name     string
	cacheDir string
	layout   cacheReadLayout
} {
	keepOut := func(t *testing.T, elsewhere, at, body string) string {
		t.Helper()
		kept := filepath.Join(elsewhere, filepath.Base(at)+".kept")
		if err := os.WriteFile(kept, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		linkInto(t, kept, at)
		return at
	}
	entityBlob := func(cache string) string {
		return filepath.Join(cache, "entities", entitySlotRel(), "blob")
	}
	return []struct {
		name     string
		cacheDir string
		layout   cacheReadLayout
	}{
		{"control: a cache inside the workspace", ".aggrega/cache", func(t *testing.T, _, cache, _ string) string {
			seedSlot(t, filepath.Dir(entityBlob(cache)))
			return ""
		}},
		{"a cache directory that is a link leading out", ".aggrega/cache", func(t *testing.T, _, cache, elsewhere string) string {
			seedSlot(t, filepath.Join(elsewhere, "entities", entitySlotRel()))
			linkInto(t, elsewhere, cache)
			return cache
		}},
		{"an entity slot's blob that is a link leading out", ".aggrega/cache", func(t *testing.T, _, cache, elsewhere string) string {
			if err := os.MkdirAll(filepath.Dir(entityBlob(cache)), 0o755); err != nil {
				t.Fatal(err)
			}
			return keepOut(t, elsewhere, entityBlob(cache), "kept outside")
		}},
		{"a source slot's metadata that is a link leading out", ".aggrega/cache", func(t *testing.T, _, cache, elsewhere string) string {
			slot := filepath.Join(cache, "source-artifacts", "helm-https-chart", "charts.example", "redis", "1.0.0", "abcdefabcdef")
			if err := os.MkdirAll(slot, 0o755); err != nil {
				t.Fatal(err)
			}
			return keepOut(t, elsewhere, filepath.Join(slot, "metadata.yaml"), "digest: sha256:abcdef\n")
		}},
		{"an external-input blob that is a link leading out", ".aggrega/cache", func(t *testing.T, _, cache, elsewhere string) string {
			store := filepath.Join(cache, "inputs", "sha256")
			if err := os.MkdirAll(store, 0o755); err != nil {
				t.Fatal(err)
			}
			return keepOut(t, elsewhere, filepath.Join(store, strings.Repeat("0", 64)), "an input")
		}},
		{"an entity class root that is a link leading out", ".aggrega/cache", func(t *testing.T, _, cache, elsewhere string) string {
			// The place the link leads holds a slot directory and nothing the
			// enumeration would open, so only the hold on the class root itself can
			// refuse this layout.
			if err := os.MkdirAll(filepath.Join(elsewhere, pathsegment.Encode("hub.example/component"), "1.0.0"), 0o755); err != nil {
				t.Fatal(err)
			}
			linkInto(t, elsewhere, filepath.Join(cache, "entities"))
			return filepath.Join(cache, "entities")
		}},
		{"control: a cache directory the configuration names outside the workspace", "../shared-cache", func(t *testing.T, _, cache, _ string) string {
			seedSlot(t, filepath.Dir(entityBlob(cache)))
			return ""
		}},
		{"a blob linking out of a cache directory named outside the workspace", "../shared-cache", func(t *testing.T, _, cache, elsewhere string) string {
			if err := os.MkdirAll(filepath.Dir(entityBlob(cache)), 0o755); err != nil {
				t.Fatal(err)
			}
			return keepOut(t, elsewhere, entityBlob(cache), "kept outside")
		}},
	}
}

// TestTheInventoryReadsTheCacheOnlyFromWhereItsWritesAreHeld drives the cache
// inventory every surface's cache status answers from over each layout. A refused
// layout halts the inventory with the read hold's condition, naming the link, and
// the place the link leads to is left exactly as it was.
func TestTheInventoryReadsTheCacheOnlyFromWhereItsWritesAreHeld(t *testing.T) {
	for _, row := range cacheReadLayouts() {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			elsewhere := t.TempDir()
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(ws, row.cacheDir)
			want := row.layout(t, ws, cache, elsewhere)
			before := tree(t, elsewhere)

			report, err := Status(t.Context(), Locations{WorkspaceRoot: ws, CacheDir: row.cacheDir})
			if want == "" {
				if err != nil {
					t.Fatalf("refused a cache its root holds: %v", err)
				}
				if report.TotalCount != 1 {
					t.Errorf("listed %d elements, want the one slot", report.TotalCount)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read hold's refusal, got %v (report %+v)", err, report)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name the link %s: %v", want, err)
			}
			if after := tree(t, elsewhere); !reflect.DeepEqual(before, after) {
				t.Errorf("the place the link leads to changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

// TestAClearOrAPruneEnumeratesOnlyWhatItMayRead drives a clear and a prune over the
// layouts a removal hold alone would let through — a link beneath a class root —
// and over a cache directory, or a class root the operation removes, that is a link
// leading out, which the removal hold refuses first. A link beneath a class root
// halts the enumeration with the read hold before anything is removed, so the link
// is still there and the place it leads to is unchanged; a linked cache directory or
// class root is refused as the removal it would be, with nothing listed through it.
func TestAClearOrAPruneEnumeratesOnlyWhatItMayRead(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(t *testing.T, loc Locations) error
	}{
		{"clear", func(t *testing.T, loc Locations) error { _, err := Clear(t.Context(), loc); return err }},
		{"prune", func(t *testing.T, loc Locations) error { _, err := Prune(t.Context(), loc); return err }},
	} {
		for _, row := range cacheReadLayouts() {
			if strings.HasPrefix(row.name, "control:") {
				continue
			}
			t.Run(op.name+"/"+row.name, func(t *testing.T) {
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
				want := row.layout(t, ws, cache, elsewhere)
				before := tree(t, elsewhere)

				err := op.run(t, Locations{WorkspaceRoot: ws, CacheDir: row.cacheDir})
				// A linked cache directory, and a linked class root the operation removes,
				// are refused as the removal they would be, before anything is listed.
				if want == cache || want == filepath.Join(cache, "entities") {
					if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
						t.Fatalf("a linked root the %s removes must be refused as the removal it would be; got %v", op.name, err)
					}
				} else {
					if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
						t.Fatalf("want the read hold's refusal, got %v", err)
					}
					if _, lerr := os.Lstat(want); lerr != nil {
						t.Errorf("the %s removed the link it refused to read: %v", op.name, lerr)
					}
				}
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name the link %s: %v", want, err)
				}
				if after := tree(t, elsewhere); !reflect.DeepEqual(before, after) {
					t.Errorf("the place the link leads to changed:\nbefore %v\nafter  %v", before, after)
				}
			})
		}
	}
}
