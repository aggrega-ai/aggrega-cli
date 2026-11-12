// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cacheslot_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestEveryFileOfASourceSlotIsHeldBeforeAnyIsWritten drives the write a source
// resolver performs after a fetch into a slot that is a plain directory inside the
// workspace, with one of the files it writes — the archive, the metadata, the
// manifest — a link leading out, one per row beside a control.
//
// Judging the slot directory alone would pass every row, and writing a file
// through a link overwrites whatever the link points to. A refused row is held to
// the condition, to the link it names, to the file the link points to holding what
// it held, and to nothing having been written in the slot.
func TestEveryFileOfASourceSlotIsHeldBeforeAnyIsWritten(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const sourceURL = "oci://charts.example.invalid/demo"
	archive := []byte("archive")
	dg := digest.ContentDigest(archive)
	manifest := []byte(`{"schemaVersion":2}`)
	rows := []struct {
		name string
		file string // the file of the slot made a link leading out; "" for the control
	}{
		{name: "control: a plain slot"},
		{name: "the archive a link leading out", file: "blob.tgz"},
		{name: "the metadata a link leading out", file: "metadata.yaml"},
		{name: "the manifest a link leading out", file: cacheslot.ManifestFile},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			slot, err := cacheslot.Slot(ws, cacheDir, "helm-oci-chart", sourceURL, "", "", "1.0.0", dg)
			if err != nil {
				t.Fatalf("Slot: %v", err)
			}
			if err := os.MkdirAll(slot, 0o755); err != nil {
				t.Fatal(err)
			}
			kept := filepath.Join(outside, "kept")
			if err := os.WriteFile(kept, []byte("kept outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			link := ""
			if row.file != "" {
				link = filepath.Join(slot, row.file)
				if err := os.Symlink(kept, link); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}

			err = cacheslot.WriteBlobAndMetadata(ws, cacheDir, slot, archive, "helm-oci-chart", sourceURL, "1.0.0", dg,
				"application/vnd.cncf.helm.chart.content.v1.tar+gzip", "", manifest)
			if link == "" {
				if err != nil {
					t.Fatalf("refused a slot the workspace holds: %v", err)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if !strings.Contains(err.Error(), link) {
				t.Errorf("the refusal does not name the link %s: %v", link, err)
			}
			if got, rerr := os.ReadFile(kept); rerr != nil || string(got) != "kept outside" {
				t.Errorf("the file the link points to changed: %q (%v)", got, rerr)
			}
			entries, rerr := os.ReadDir(slot)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(entries) != 1 {
				t.Errorf("the slot holds %d entries, want only the link: something was written before the refusal", len(entries))
			}
		})
	}
}
