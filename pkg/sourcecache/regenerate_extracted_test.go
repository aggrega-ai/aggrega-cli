// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourcecache_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// TestRegenerateExtractedRebuildsTheDirectoryFromTheArchive drives the exported
// regeneration directly — the step both the source-artifact cache and the entity
// cache rebuild their extracted directories through. Whatever the directory held
// before the call, a file changed, a file added, is gone after it, and what is
// left is exactly the archive's content.
func TestRegenerateExtractedRebuildsTheDirectoryFromTheArchive(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	archive := buildTGZ(t, map[string]string{
		"chart/Chart.yaml":        "name: chart\n",
		"chart/templates/cm.yaml": "kind: ConfigMap\n",
		"recipe.yaml":             "kind: Recipe\n",
	})
	stale := sourcecache.ExtractedDir(slot)
	if err := os.MkdirAll(filepath.Join(stale, "chart", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "chart", "templates", "cm.yaml"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "planted.yaml"), []byte("added by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir, err := sourcecache.RegenerateExtracted(slot, archive)
	if err != nil {
		t.Fatalf("RegenerateExtracted: %v", err)
	}
	if dir != sourcecache.ExtractedDir(slot) {
		t.Errorf("returned %q; want the slot's extracted directory %q", dir, sourcecache.ExtractedDir(slot))
	}
	got, err := os.ReadFile(filepath.Join(dir, "chart", "templates", "cm.yaml"))
	if err != nil || !bytes.Equal(got, []byte("kind: ConfigMap\n")) {
		t.Errorf("the edited file was not rebuilt from the archive: %q (err %v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "planted.yaml")); !os.IsNotExist(err) {
		t.Errorf("a file the archive does not carry survived regeneration (stat err = %v)", err)
	}
	for _, rel := range []string{"chart/Chart.yaml", "recipe.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s from the archive is missing after regeneration: %v", rel, err)
		}
	}
}

// TestRegenerateExtractedRefusesAnEntryLeavingTheDirectory pins the traversal
// refusal on the exported step itself: an entry that would land outside the
// extracted directory is refused as an integrity failure, and nothing is written
// beside the directory.
func TestRegenerateExtractedRefusesAnEntryLeavingTheDirectory(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("escaped\n")
	if err := tw.WriteHeader(&tar.Header{
		Name: "../escaped.yaml", Mode: 0o644, Size: int64(len(body)),
		Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()

	_, err := sourcecache.RegenerateExtracted(slot, buf.Bytes())
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("an entry leaving the directory must be refused as an integrity failure; got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(slot, "escaped.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("the escaping entry was written beside the extracted directory (stat err = %v)", statErr)
	}
}

// TestResetExtractedEmptiesTheDirectory pins the first half on its own: the
// directory exists afterwards and holds nothing, whatever it held before.
func TestResetExtractedEmptiesTheDirectory(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourcecache.ExtractedDir(slot), "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcecache.ExtractedDir(slot), "sub", "left.yaml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := sourcecache.ResetExtracted(slot)
	if err != nil {
		t.Fatalf("ResetExtracted: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the extracted directory does not exist after a reset: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the extracted directory holds %d entries after a reset; want none", len(entries))
	}
}
