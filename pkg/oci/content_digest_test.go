// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// digestFixture lays out a Recipe carrying a two-file source in a fresh
// directory, every file at mode, and returns the document's path and the files
// it carries.
func digestFixture(t *testing.T, mode os.FileMode) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "chart", "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string][]byte{
		"recipe.yaml":           []byte("kind: Recipe\nmetadata:\n  name: server\n  version: 1.0.0\n"),
		"chart/Chart.yaml":      []byte("name: server\n"),
		"chart/scripts/hook.sh": []byte("#!/bin/sh\necho hook\n"),
	}
	for rel, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		mustWrite(t, p, data)
		if err := os.Chmod(p, mode); err != nil {
			t.Fatalf("chmod %s: %v", p, err)
		}
	}
	return filepath.Join(dir, "recipe.yaml"), []string{"chart/Chart.yaml", "chart/scripts/hook.sh"}
}

// carried is CarriedDigest over the fixture, failing the test on an error.
func carried(t *testing.T, entityPath string, files []string, supplied map[string][]byte) string {
	t.Helper()
	d, err := oci.CarriedDigest(entityPath, files, supplied)
	if err != nil {
		t.Fatalf("CarriedDigest: %v", err)
	}
	return d
}

// published is PublishedDigest, failing the test on an error.
func published(t *testing.T, b []byte) string {
	t.Helper()
	d, err := oci.PublishedDigest(b)
	if err != nil {
		t.Fatalf("PublishedDigest: %v", err)
	}
	return d
}

// recompress re-gzips the tar inside archive at level, leaving the entries byte
// for byte as they were.
func recompress(t *testing.T, archive []byte, level int) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	tarBytes, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read tar: %v", err)
	}
	var out bytes.Buffer
	w, err := gzip.NewWriterLevel(&out, level)
	if err != nil {
		t.Fatalf("gzip writer: %v", err)
	}
	if _, err := w.Write(tarBytes); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return out.Bytes()
}

// TestTheEntityDigestIsTheContentAPublicationCarries holds the digest an entity
// row records to each property it is defined by, one row per property. Each row
// names the two digests it compares and whether they must agree, so a row that
// fails names the property that broke: a digest that took every permission bit
// splits the umask row, one that dropped the execute bit joins the execute row,
// one over compressed bytes splits the recompression row, and an author's digest
// over anything but what the publication carries splits the author-and-consumer
// rows.
func TestTheEntityDigestIsTheContentAPublicationCarries(t *testing.T) {
	t.Parallel()

	doc644, files644 := digestFixture(t, 0o644)
	doc664, files664 := digestFixture(t, 0o664)
	docExec, filesExec := digestFixture(t, 0o644)
	if err := os.Chmod(filepath.Join(filepath.Dir(docExec), "chart", "scripts", "hook.sh"), 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	archive, _, err := oci.Bundle("Recipe", doc644, files644)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	fastest := recompress(t, archive, gzip.BestSpeed)
	if bytes.Equal(fastest, archive) {
		t.Fatal("the recompressed archive is byte-identical to the original, so the recompression row compares nothing")
	}
	document, err := os.ReadFile(doc644)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	alone, _, err := oci.Bundle("Recipe", doc644, nil)
	if err != nil {
		t.Fatalf("Bundle alone: %v", err)
	}

	rows := []struct {
		name  string
		a, b  string
		agree bool
	}{
		{"two checkouts of one commit differing only in a group-write bit", carried(t, doc644, files644, nil), carried(t, doc664, files664, nil), true},
		{"a carried script made executable", carried(t, doc644, files644, nil), carried(t, docExec, filesExec, nil), false},
		{"the same entries compressed at another level", published(t, archive), published(t, fastest), true},
		{"the author's files and the archive a consumer fetches", carried(t, doc644, files644, nil), published(t, archive), true},
		{"the author's executable script and the archive published from it", carried(t, docExec, filesExec, nil), published(t, mustBundle(t, docExec, filesExec)), true},
		{"a document published alone, by its author and by a consumer", carried(t, doc644, nil, nil), published(t, alone), true},
		{"a document published alone and the one-entry set of it", carried(t, doc644, nil, nil), oci.DocumentDigest(document), true},
		{"a document published alone and the digest of its bytes", oci.DocumentDigest(document), digest.ContentDigest(document), false},
		{"an archive's entries and its compressed bytes", published(t, archive), digest.ContentDigest(archive), false},
	}
	for _, r := range rows {
		if got := r.a == r.b; got != r.agree {
			t.Errorf("%s: agree=%v, want %v (%s vs %s)", r.name, got, r.agree, r.a, r.b)
		}
	}
}

// TestCarriedDigestReadsWhatItIsSupplied holds the supplied bytes to replacing
// what is on disk: a lock a report would write, which is not on disk yet, digests
// as the file a writing run leaves behind.
func TestCarriedDigestReadsWhatItIsSupplied(t *testing.T) {
	t.Parallel()

	doc, files := digestFixture(t, 0o644)
	lock := []byte("version: 1\n")
	withLock := append(append([]string(nil), files...), "entity.lock")

	supplied := carried(t, doc, withLock, map[string][]byte{"entity.lock": lock})
	mustWrite(t, filepath.Join(filepath.Dir(doc), "entity.lock"), lock)
	written := carried(t, doc, withLock, nil)
	if supplied != written {
		t.Errorf("a lock supplied before it is written digests %s; written, %s", supplied, written)
	}
	if without := carried(t, doc, files, nil); without == written {
		t.Errorf("the lock is one of the files the digest covers, but the digest with and without it is %s", written)
	}
}

// TestAnArchiveCarriesOnlyTheExecuteBit holds packArchive to the two modes it
// writes, whatever the checkout's permission bits were.
func TestAnArchiveCarriesOnlyTheExecuteBit(t *testing.T) {
	t.Parallel()

	doc, files := digestFixture(t, 0o664)
	if err := os.Chmod(filepath.Join(filepath.Dir(doc), "chart", "scripts", "hook.sh"), 0o775); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	archive := mustBundle(t, doc, files)
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	tr := tar.NewReader(gz)
	want := map[string]int64{"recipe.yaml": 0o644, "chart/Chart.yaml": 0o644, "chart/scripts/hook.sh": 0o755}
	seen := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		seen++
		if hdr.Mode != want[hdr.Name] {
			t.Errorf("%s packed with mode %o, want %o", hdr.Name, hdr.Mode, want[hdr.Name])
		}
	}
	if seen != len(want) {
		t.Errorf("the archive holds %d entries, want %d", seen, len(want))
	}
}

func mustBundle(t *testing.T, doc string, files []string) []byte {
	t.Helper()
	archive, _, err := oci.Bundle("Recipe", doc, files)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	return archive
}
