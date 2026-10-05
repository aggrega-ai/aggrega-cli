// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheslot_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestSourceSlotWritesAreHeldToTheWorkspace drives the write every source
// resolver performs after a fetch over a workspace whose artifact cache directory
// is, one row at a time, a plain directory and a link leading out.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: nothing may have been written there.
func TestSourceSlotWritesAreHeldToTheWorkspace(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	archive := []byte("archive")
	dg := digest.ContentDigest(archive)
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
				if err := os.MkdirAll(filepath.Join(ws, ".aggrega"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(ws, cacheDir)); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			slot, err := cacheslot.Slot(ws, cacheDir, "helm-https-chart", "https://charts.example.invalid/demo.tgz", "", "", "1.0.0", dg)
			if err != nil {
				t.Fatalf("Slot: %v", err)
			}

			err = cacheslot.WriteBlobAndMetadata(ws, cacheDir, slot, archive, "helm-https-chart", "https://charts.example.invalid/demo.tgz", "1.0.0", dg, "application/gzip", "", nil)
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

// TestSourceSlotWritesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt drives
// the same write over a cache directory the configuration names climbing out of
// the workspace, one shape per row beside a control writing plainly beneath it.
//
// The directory is a root of its own: a source-artifacts subtree beneath it that is
// a link leading out of it is refused, and the place the link leads to is left as
// it was; the directory itself being a link is honoured where it was named, and the
// slot is written where it leads.
func TestSourceSlotWritesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt(t *testing.T) {
	const cacheDir = "../shared-cache"
	const sourceURL = "https://charts.example.invalid/demo.tgz"
	archive := []byte("archive")
	dg := digest.ContentDigest(archive)
	rows := []struct {
		name    string
		layout  func(t *testing.T, cache, elsewhere string)
		refused bool
	}{
		{name: "control: a cache directory outside the workspace", layout: func(*testing.T, string, string) {}},
		{name: "a source-artifacts subtree that is a link leading out of it is refused", refused: true, layout: func(t *testing.T, cache, elsewhere string) {
			linkOrSkipSlot(t, elsewhere, filepath.Join(cache, "source-artifacts"))
		}},
		{name: "the cache directory itself a link is honoured where it was named", layout: func(t *testing.T, cache, elsewhere string) {
			linkOrSkipSlot(t, elsewhere, cache)
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
			slot, err := cacheslot.Slot(ws, cacheDir, "helm-https-chart", sourceURL, "", "", "1.0.0", dg)
			if err != nil {
				t.Fatalf("Slot: %v", err)
			}

			err = cacheslot.WriteBlobAndMetadata(ws, cacheDir, slot, archive, "helm-https-chart", sourceURL, "1.0.0", dg, "application/gzip", "", nil)
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
			if got, rerr := os.ReadFile(filepath.Join(slot, "blob.tgz")); rerr != nil || string(got) != string(archive) {
				t.Errorf("the slot does not hold what was written: %q (%v)", got, rerr)
			}
		})
	}
}

// linkOrSkipSlot makes link a symbolic link to target, creating the directory
// holding it, or skips where links are not available.
func linkOrSkipSlot(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
