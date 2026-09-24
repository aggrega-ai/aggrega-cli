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
// cache rebuild their extracted trees through. Whatever the archive's tree held
// before the call, a file changed, a file added, is gone after it, and what is
// left is exactly the archive's content, in the tree named by the archive's
// digest.
func TestRegenerateExtractedRebuildsTheDirectoryFromTheArchive(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	archive := buildTGZ(t, map[string]string{
		"chart/Chart.yaml":        "name: chart\n",
		"chart/templates/cm.yaml": "kind: ConfigMap\n",
		"recipe.yaml":             "kind: Recipe\n",
	})
	tree := sourcecache.ExtractedTree(slot, digestOf(archive))
	if err := os.MkdirAll(filepath.Join(tree, "chart", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "chart", "templates", "cm.yaml"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "planted.yaml"), []byte("added by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir, err := sourcecache.RegenerateExtracted(slot, archive)
	if err != nil {
		t.Fatalf("RegenerateExtracted: %v", err)
	}
	if dir != tree {
		t.Errorf("returned %q; want the tree named by the archive's digest %q", dir, tree)
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
// archive's tree is refused as an integrity failure before anything is written —
// not beside the tree, where the climb would land, and not the tree itself.
func TestRegenerateExtractedRefusesAnEntryLeavingTheDirectory(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{
		{"inside.yaml", "inside\n"},
		{"../escaped.yaml", "escaped\n"},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: e.name, Mode: 0o644, Size: int64(len(e.body)),
			Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()

	_, err := sourcecache.RegenerateExtracted(slot, buf.Bytes())
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("an entry leaving the directory must be refused as an integrity failure; got %v", err)
	}
	for _, p := range []string{
		filepath.Join(slot, "escaped.yaml"),
		filepath.Join(sourcecache.ExtractedDir(slot), "escaped.yaml"),
		sourcecache.ExtractedTree(slot, digestOf(buf.Bytes())),
	} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Errorf("%s was written although the archive was refused (stat err = %v)", p, statErr)
		}
	}
}

// TestRegenerateExtractedDocumentLaysOutTheOneFile pins the single-document form: the
// tree named by the document's digest holds that document under its name and
// nothing else, whatever an earlier read or a hand edit left there.
func TestRegenerateExtractedDocumentLaysOutTheOneFile(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	document := []byte("kind: Component\n")
	tree := sourcecache.ExtractedTree(slot, digestOf(document))
	if err := os.MkdirAll(filepath.Join(tree, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "sub", "left.yaml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "entity.yaml"), []byte("kind: Edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir, err := sourcecache.RegenerateExtractedDocument(slot, "entity.yaml", document)
	if err != nil {
		t.Fatalf("RegenerateExtractedDocument: %v", err)
	}
	if dir != tree {
		t.Errorf("returned %q; want the tree named by the document's digest %q", dir, tree)
	}
	if got := treeFiles(t, dir); len(got) != 1 || got["entity.yaml"] != string(document) {
		t.Errorf("the tree holds %v; want exactly entity.yaml holding the document", got)
	}
}

// TestRegenerateExtractedDocumentRefusesANameLeavingTheTree pins that the document's
// name is judged by the same containment rule an archive entry is.
func TestRegenerateExtractedDocumentRefusesANameLeavingTheTree(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	_, err := sourcecache.RegenerateExtractedDocument(slot, "../entity.yaml", []byte("kind: Component\n"))
	if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
		t.Fatalf("a document name leaving the tree must be refused as an integrity failure; got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(sourcecache.ExtractedDir(slot), "entity.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("the document was written beside the tree (stat err = %v)", statErr)
	}
}
