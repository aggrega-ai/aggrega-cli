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

// layoutAt lays a cached artifact out in the slot at slot, for the workspace at ws
// and its cache directory cacheDir.
type layoutAt func(ws, cacheDir, slot string) error

// layoutForms are the two forms a cached artifact is laid out in: an archive, and a
// single document.
func layoutForms(t *testing.T) (asArchive, asDocument layoutAt) {
	t.Helper()
	archive := buildTGZ(t, map[string]string{"chart/Chart.yaml": "name: chart\n"})
	asArchive = func(ws, cacheDir, slot string) error {
		_, err := sourcecache.RegenerateExtracted(ws, cacheDir, slot, archive)
		return err
	}
	asDocument = func(ws, cacheDir, slot string) error {
		_, err := sourcecache.RegenerateExtractedDocument(ws, cacheDir, slot, "entity.yaml", []byte("kind: Component\n"))
		return err
	}
	return asArchive, asDocument
}

// TestExtractedTreesAreHeldToTheWorkspace drives the layout a build performs on a
// cached artifact, for an archive and for a single document, over a workspace
// whose cache slot sits, one row at a time, in a plain directory and beneath a
// cache directory that is a link leading out.
//
// A refused row is held to the directory the link leads to as well as to the
// condition: nothing may have been laid out there.
func TestExtractedTreesAreHeldToTheWorkspace(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	asArchive, asDocument := layoutForms(t)
	rows := []struct {
		name    string
		layout  layoutAt
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
			cache := filepath.Join(ws, cacheDir)
			if row.link {
				if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, cache); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
			}
			slot := filepath.Join(cache, "entities", "demo", "1.0.0")

			err := row.layout(ws, cacheDir, slot)
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

// TestExtractedTreesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt drives
// the same layout over a cache directory the configuration names climbing out of
// the workspace, for both forms, one shape per row beside a control laying out
// plainly beneath it.
//
// The directory is a root of its own: an entities subtree beneath it that is a link
// leading out of it is refused, and the place the link leads to is left as it was;
// the directory itself being a link is honoured where it was named, and the tree is
// laid out where it leads.
func TestExtractedTreesBeneathACacheDirectoryOutsideTheWorkspaceAreHeldToIt(t *testing.T) {
	const cacheDir = "../shared-cache"
	asArchive, asDocument := layoutForms(t)
	linkSubtree := func(t *testing.T, cache, elsewhere string) {
		linkOrSkipTree(t, elsewhere, filepath.Join(cache, "entities"))
	}
	linkCache := func(t *testing.T, cache, elsewhere string) { linkOrSkipTree(t, elsewhere, cache) }
	rows := []struct {
		name    string
		layout  layoutAt
		arrange func(t *testing.T, cache, elsewhere string)
		refused bool
	}{
		{name: "control: an archive laid out beneath a cache directory outside the workspace", layout: asArchive},
		{name: "control: a document laid out beneath a cache directory outside the workspace", layout: asDocument},
		{name: "an archive beneath an entities subtree that is a link leading out of it is refused", layout: asArchive, arrange: linkSubtree, refused: true},
		{name: "a document beneath an entities subtree that is a link leading out of it is refused", layout: asDocument, arrange: linkSubtree, refused: true},
		{name: "an archive beneath a cache directory that is itself a link is honoured where it was named", layout: asArchive, arrange: linkCache},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			elsewhere := t.TempDir()
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(ws, cacheDir)
			if row.arrange != nil {
				row.arrange(t, cache, elsewhere)
			}
			slot := filepath.Join(cache, "entities", "demo", "1.0.0")

			err := row.layout(ws, cacheDir, slot)
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if entries, rerr := os.ReadDir(elsewhere); rerr != nil || len(entries) != 0 {
					t.Errorf("something was laid out where the link leads: %v (%v)", entries, rerr)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a layout its root holds: %v", err)
			}
			if entries, rerr := os.ReadDir(sourcecache.ExtractedDir(slot)); rerr != nil || len(entries) == 0 {
				t.Errorf("nothing was laid out beneath the slot: %v (%v)", entries, rerr)
			}
		})
	}
}

// linkOrSkipTree makes link a symbolic link to target, creating the directory
// holding it, or skips where links are not available.
func linkOrSkipTree(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
