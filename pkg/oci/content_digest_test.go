// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// fails names the property that broke: a digest that took any permission bit
// splits the umask row or the execute row, one that read the mode an archive
// records splits the other-client row, one over compressed bytes splits the
// recompression row, and an author's digest over anything but what the
// publication carries splits the author-and-consumer rows.
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
	otherClient := packWithModes(t, doc644, files644, map[string]int64{"chart/scripts/hook.sh": 0o755})
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
		{"two checkouts of one commit differing only in a script's execute bit", carried(t, doc644, files644, nil), carried(t, docExec, filesExec, nil), true},
		{"an archive another client packed with the script at 0755, and the author's files", carried(t, doc644, files644, nil), published(t, otherClient), true},
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

// TestTheExecuteBitReachesNeitherTheDigestNorTheArchive builds one entity from
// files whose permission bits differ row by row — the script executable, a
// group-writable checkout, every bit set — and holds each build to the build
// from files at 0644: the same digest, and the same archive entries, header bytes
// included, with every entry's header recording 0644. A system that records no
// execute bit checks the script out as 0644 where another checks it out as
// executable, so any difference here is an entity that pins, and publishes,
// differently depending on where it was checked out.
//
// The control row changes the script's bytes instead, and must move both the
// digest and the archive, so a comparison that saw nothing could not pass the
// other rows.
func TestTheExecuteBitReachesNeitherTheDigestNorTheArchive(t *testing.T) {
	t.Parallel()

	script := []byte("#!/bin/sh\necho hook\n")
	baseDoc, baseFiles := modeFixture(t, 0o644, 0o644, script)
	baseDigest := carried(t, baseDoc, baseFiles, nil)
	baseStream := tarStream(t, mustBundle(t, baseDoc, baseFiles))

	rows := []struct {
		name       string
		fileMode   os.FileMode
		scriptMode os.FileMode
		script     []byte
		same       bool
	}{
		{"the script executable", 0o644, 0o755, script, true},
		{"the script executable by its owner alone", 0o644, 0o744, script, true},
		{"a group-writable checkout with the script executable", 0o664, 0o775, script, true},
		{"every permission bit set on every file", 0o777, 0o777, script, true},
		{"control: the script's bytes changed, its mode not", 0o644, 0o644, []byte("#!/bin/sh\necho other\n"), false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			doc, files := modeFixture(t, r.fileMode, r.scriptMode, r.script)
			archive := mustBundle(t, doc, files)
			stream := tarStream(t, archive)

			if got := carried(t, doc, files, nil) == baseDigest; got != r.same {
				t.Errorf("digest equal to the 0644 build: %v, want %v", got, r.same)
			}
			if got := published(t, archive) == baseDigest; got != r.same {
				t.Errorf("digest of the published archive equal to the 0644 build's: %v, want %v", got, r.same)
			}
			if got := bytes.Equal(stream, baseStream); got != r.same {
				t.Errorf("archive entries, header bytes included, equal to the 0644 build's: %v, want %v", got, r.same)
			}
			for name, mode := range tarModes(t, stream) {
				if mode != 0o644 {
					t.Errorf("%s packed with mode %o, want 644", name, mode)
				}
			}
		})
	}
}

// modeFixture is digestFixture with the script at scriptMode holding script, and
// every other file at fileMode.
func modeFixture(t *testing.T, fileMode, scriptMode os.FileMode, script []byte) (string, []string) {
	t.Helper()
	doc, files := digestFixture(t, fileMode)
	hook := filepath.Join(filepath.Dir(doc), "chart", "scripts", "hook.sh")
	mustWrite(t, hook, script)
	if err := os.Chmod(hook, scriptMode); err != nil {
		t.Fatalf("chmod %s: %v", hook, err)
	}
	return doc, files
}

// tarStream returns the uncompressed tar inside archive: every header block and
// every entry, byte for byte, without the compression around them.
func tarStream(t *testing.T, archive []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	stream, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read tar: %v", err)
	}
	return stream
}

// tarModes returns the mode each entry's header in stream records, by name.
func tarModes(t *testing.T, stream []byte) map[string]int64 {
	t.Helper()
	modes := map[string]int64{}
	tr := tar.NewReader(bytes.NewReader(stream))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return modes
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		modes[hdr.Name] = hdr.Mode
	}
}

// packWithModes packs the document at doc and the carried files into a gzipped
// tar the way another client might: each entry's header records the mode modes
// names for it, or 0644, so a reader that took the mode from the header would
// see it.
func packWithModes(t *testing.T, doc string, files []string, modes map[string]int64) []byte {
	t.Helper()
	dir := filepath.Dir(doc)
	names := append([]string{filepath.Base(doc)}, files...)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		mode, ok := modes[name]
		if !ok {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func mustBundle(t *testing.T, doc string, files []string) []byte {
	t.Helper()
	archive, _, err := oci.Bundle("Recipe", doc, files)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	return archive
}
