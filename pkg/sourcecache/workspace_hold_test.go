// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourcecache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestExtractedTreesAreHeldToTheWorkspace drives the layout a build performs on a
// cached artifact, for an archive and for a single document, over a workspace
// whose cache slot sits, one row at a time, in a plain directory and beneath a
// cache directory that is a link leading out.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: nothing may have been laid out there.
func TestExtractedTreesAreHeldToTheWorkspace(t *testing.T) {
	archive := buildTGZ(t, map[string]string{"chart/Chart.yaml": "name: chart\n"})
	type layout func(ws, slot string) error
	asArchive := layout(func(ws, slot string) error { _, err := sourcecache.RegenerateExtracted(ws, slot, archive); return err })
	asDocument := layout(func(ws, slot string) error {
		_, err := sourcecache.RegenerateExtractedDocument(ws, slot, "entity.yaml", []byte("kind: Component\n"))
		return err
	})
	rows := []struct {
		name    string
		layout  layout
		link    bool
		refused bool
	}{
		{name: "control: an archive laid out inside", layout: asArchive},
		{name: "control: a document laid out inside", layout: asDocument},
		{name: "an archive beneath a cache directory leading out", layout: asArchive, link: true, refused: true},
		{name: "a document beneath a cache directory leading out", layout: asDocument, link: true, refused: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			cache := filepath.Join(ws, ".aggrega", "cache")
			if row.link {
				if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, cache); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			slot := filepath.Join(cache, "entities", "demo", "1.0.0")

			err := row.layout(ws, slot)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a layout the workspace holds: %v", err)
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
				t.Errorf("something was laid out outside the workspace: %v", entries)
			}
		})
	}
}
