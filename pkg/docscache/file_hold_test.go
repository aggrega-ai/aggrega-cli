// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docscache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestBothFilesOfABundleSlotAreHeldBeforeEitherIsWritten drives the write a
// documentation bundle is cached by into a slot that is a plain directory inside
// the workspace, with its content or its manifest a link leading out, one per row
// beside a control.
//
// Judging the content alone would pass the manifest row, and writing a file
// through a link overwrites whatever the link points to. A refused row is held to
// the condition, to the link it names, to the file the link points to holding what
// it held, and to nothing having been written in the slot.
func TestBothFilesOfABundleSlotAreHeldBeforeEitherIsWritten(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs,
		buildBundle(t, map[string]string{componentPage: componentBody}), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	rows := []struct {
		name string
		file string // the file of the slot made a link leading out; "" for the control
	}{
		{name: "control: a plain slot"},
		{name: "the content a link leading out", file: "blob"},
		{name: "the manifest a link leading out", file: "manifest"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			slot := filepath.Join(docscache.DocsBundlesRoot(ws, cacheDir), pathsegment.Encode(testRef), pathsegment.Encode(testVersion))
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

			got, err := docscache.ResolveAndCache(ctx, store, testRef, testVersion, ws, cacheDir, nil)
			if link == "" {
				if err != nil {
					t.Fatalf("refused a slot the workspace holds: %v", err)
				}
				if got != slot {
					t.Errorf("cached into %s, want %s", got, slot)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if !strings.Contains(err.Error(), link) {
				t.Errorf("the refusal does not name the link %s: %v", link, err)
			}
			if data, rerr := os.ReadFile(kept); rerr != nil || string(data) != "kept outside" {
				t.Errorf("the file the link points to changed: %q (%v)", data, rerr)
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
