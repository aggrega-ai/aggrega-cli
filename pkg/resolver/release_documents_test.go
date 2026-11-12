// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// The tests in this file hold what ReleaseDocuments records for a Release: every
// Release file a composition of it reads, in the order the extends chain is
// walked, each by the content digest of its bytes, under a path that does not
// depend on where the workspace sits on disk.

// fileDigest is the digest an independent read of path produces — the value a
// recorded digest must equal for the record to describe the file.
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return digest.ContentDigest(data)
}

// TestReleaseDocumentsRecordsTheReleaseAlone holds that a Release heading no
// chain records exactly itself, under its base name, with the digest of its
// bytes, and that LoadResolvedRelease carries that digest from the read it
// decoded the document from.
func TestReleaseDocumentsRecordsTheReleaseAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeReleaseFile(t, dir, "release.yaml", releaseDoc("solo", ""))
	path := filepath.Join(dir, "release.yaml")

	child, err := LoadResolvedRelease(path)
	if err != nil {
		t.Fatalf("LoadResolvedRelease: %v", err)
	}
	if child.Digest != fileDigest(t, path) {
		t.Fatalf("LoadResolvedRelease digest %s does not describe the file (%s)", child.Digest, fileDigest(t, path))
	}
	got, err := ReleaseDocuments("", dir, child)
	if err != nil {
		t.Fatalf("ReleaseDocuments: %v", err)
	}
	want := []closure.ReleaseDocument{{Path: "release.yaml", Digest: fileDigest(t, path)}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("ReleaseDocuments = %+v, want %+v", got, want)
	}
}

// TestReleaseDocumentsRecordsEveryReleaseAlongTheChain holds that each Release a
// chain passes through is recorded — a same-directory parent and one reached
// through a parent directory — in walk order, each under a forward-slash path
// relative to the named Release's directory, each with its own digest.
//
// Breaking it looks like a chain whose parent edit goes unnoticed: a record
// holding only the child passes a build over an edited parent.
func TestReleaseDocumentsRecordsEveryReleaseAlongTheChain(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "envs")
	shared := filepath.Join(root, "shared")
	for _, d := range []string{dir, shared} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	// Every extends reference is resolved beside the Release that writes it, and
	// mid.yaml sits in envs/, so the grandparent reference is written from envs/.
	writeReleaseFile(t, dir, "prod.yaml", releaseDoc("prod", "./mid.yaml"))
	writeReleaseFile(t, dir, "mid.yaml", releaseDoc("mid", "../shared/base.yaml"))
	writeReleaseFile(t, shared, "base.yaml", releaseDoc("base", ""))

	child, err := LoadResolvedRelease(filepath.Join(dir, "prod.yaml"))
	if err != nil {
		t.Fatalf("LoadResolvedRelease: %v", err)
	}
	got, err := ReleaseDocuments("", dir, child)
	if err != nil {
		t.Fatalf("ReleaseDocuments: %v", err)
	}
	want := []closure.ReleaseDocument{
		{Path: "prod.yaml", Digest: fileDigest(t, filepath.Join(dir, "prod.yaml"))},
		{Path: "mid.yaml", Digest: fileDigest(t, filepath.Join(dir, "mid.yaml"))},
		{Path: "../shared/base.yaml", Digest: fileDigest(t, filepath.Join(shared, "base.yaml"))},
	}
	if len(got) != len(want) {
		t.Fatalf("ReleaseDocuments recorded %d files, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ReleaseDocuments[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, d := range got {
		if filepath.IsAbs(d.Path) || strings.Contains(d.Path, `\`) {
			t.Errorf("recorded path %q is not a forward-slash relative path; it would differ between machines", d.Path)
		}
	}
}

// TestReleaseDocumentsDigestMovesWithEveryByte holds the inert-edit rule: a
// comment appended to a Release is a change to what is recorded. A normalised
// digest would leave the two equal, and this test would fail on it.
func TestReleaseDocumentsDigestMovesWithEveryByte(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "release.yaml")
	writeReleaseFile(t, dir, "release.yaml", releaseDoc("solo", ""))
	before, err := LoadResolvedRelease(path)
	if err != nil {
		t.Fatalf("LoadResolvedRelease: %v", err)
	}
	writeReleaseFile(t, dir, "release.yaml", releaseDoc("solo", "")+"# a comment\n")
	after, err := LoadResolvedRelease(path)
	if err != nil {
		t.Fatalf("LoadResolvedRelease after comment: %v", err)
	}
	if before.Digest == after.Digest {
		t.Fatalf("a comment edit left the recorded digest unchanged (%s); the record would not notice it", before.Digest)
	}
}

// TestReleaseDocumentsRefusesAReleaseWithoutItsDigest holds that a Release
// assembled from a value already in hand — no read, so no digest — is refused
// rather than recorded with an empty digest, which would report the unchanged
// file as edited at every build.
func TestReleaseDocumentsRefusesAReleaseWithoutItsDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeReleaseFile(t, dir, "release.yaml", releaseDoc("solo", ""))
	rel, err := LoadRelease(filepath.Join(dir, "release.yaml"))
	if err != nil {
		t.Fatalf("LoadRelease: %v", err)
	}
	_, err = ReleaseDocuments("", dir, ResolvedRelease{Release: rel, Path: filepath.Join(dir, "release.yaml")})
	if err == nil || !strings.Contains(err.Error(), "LoadResolvedRelease") {
		t.Fatalf("a Release without its digest must be refused naming the loader to use; got %v", err)
	}
}

// TestReleaseDocumentsFailsWhereTheCompositionFails holds that a chain the
// composition cannot follow fails here with the composition's own failure, so a
// caller recording documents before composing reports nothing an author has not
// already been told by the composition.
func TestReleaseDocumentsFailsWhereTheCompositionFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeReleaseFile(t, dir, "release.yaml", releaseDoc("orphan", "./missing.yaml"))
	child, err := LoadResolvedRelease(filepath.Join(dir, "release.yaml"))
	if err != nil {
		t.Fatalf("LoadResolvedRelease: %v", err)
	}
	_, docErr := ReleaseDocuments("", dir, child)
	_, _, composeErr := ResolveReleaseExtends("", dir, child)
	if docErr == nil || composeErr == nil {
		t.Fatalf("both must refuse a chain naming a missing parent; ReleaseDocuments=%v, composition=%v", docErr, composeErr)
	}
	if docErr.Error() != composeErr.Error() {
		t.Errorf("ReleaseDocuments reports a different failure than the composition:\n  documents:   %v\n  composition: %v",
			docErr, composeErr)
	}
}
