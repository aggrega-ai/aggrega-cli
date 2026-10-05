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

			err = cacheslot.WriteBlobAndMetadata(ws, slot, archive, "helm-https-chart", "https://charts.example.invalid/demo.tgz", "1.0.0", dg, "application/gzip", "", nil)
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
