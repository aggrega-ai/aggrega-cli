// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

const (
	releaseDigestA = "sha256:" + "aa00000000000000000000000000000000000000000000000000000000000000"
	releaseDigestB = "sha256:" + "bb00000000000000000000000000000000000000000000000000000000000000"
	parentDigest   = "sha256:" + "cc00000000000000000000000000000000000000000000000000000000000000"
)

// TestVerifyReleaseDocuments holds the Release-file axis of the drift comparison
// to its four outcomes. Each row carries exactly one fault, and the clean row is
// the positive control: without it a comparison that reported every Release file
// as drifted would pass every other row.
//
// Every faulting row asserts the condition, the file it names and what the entry
// itself says clears it, because the entry is all a reader is shown — the
// enclosing frame's text does not reach the terminal. And every faulting row
// asserts the entry names no placeholder Release: the entry does not hold the
// Release as the reader named it, so the command is the reporting surface's to
// print, and an entry writing `<release>` would show the reader a command they
// cannot run as written. A comparison with the axis removed returns nil for every
// faulting row, so each fails on its errors.Is.
func TestVerifyReleaseDocuments(t *testing.T) {
	t.Parallel()
	const clears = "re-resolving the Release records the files it reads as they now stand"
	current := []closure.ReleaseDocument{
		{Path: "release.yaml", Digest: releaseDigestA},
		{Path: "../shared/base-release.yaml", Digest: parentDigest},
	}
	cases := []struct {
		name     string
		recorded []lockfile.ReleaseDocument
		want     []string // substrings every faulting row's message must carry; nil = no drift
	}{
		{
			name: "every recorded file matches",
			recorded: []lockfile.ReleaseDocument{
				{Path: "release.yaml", Digest: releaseDigestA},
				{Path: "../shared/base-release.yaml", Digest: parentDigest},
			},
		},
		{
			name: "the Release was edited",
			recorded: []lockfile.ReleaseDocument{
				{Path: "release.yaml", Digest: releaseDigestB},
				{Path: "../shared/base-release.yaml", Digest: parentDigest},
			},
			want: []string{`release document "release.yaml" was edited after the lockfile was written`,
				"lockfile=" + releaseDigestB, "current=" + releaseDigestA, clears},
		},
		{
			name: "a Release it extends was edited",
			recorded: []lockfile.ReleaseDocument{
				{Path: "release.yaml", Digest: releaseDigestA},
				{Path: "../shared/base-release.yaml", Digest: releaseDigestB},
			},
			want: []string{`release document "../shared/base-release.yaml" was edited`, clears},
		},
		{
			name:     "a lockfile recording no Release file at all",
			recorded: nil,
			want: []string{`release document "release.yaml" is not recorded in the lockfile`,
				`release document "../shared/base-release.yaml" is not recorded`, clears},
		},
		{
			name: "a recorded file the Release no longer reads",
			recorded: []lockfile.ReleaseDocument{
				{Path: "release.yaml", Digest: releaseDigestA},
				{Path: "../shared/base-release.yaml", Digest: parentDigest},
				{Path: "old-base.yaml", Digest: releaseDigestB},
			},
			want: []string{`release document "old-base.yaml" is recorded in the lockfile but the Release no longer reads it`,
				clears},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := lockfile.Verify(&lockfile.Lockfile{ReleaseDocuments: tc.recorded},
				closure.Closure{ReleaseDocuments: current})
			if tc.want == nil {
				if err != nil {
					t.Fatalf("an unchanged Release must verify clean; got %v", err)
				}
				return
			}
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Fatalf("expected the lockfile-drift condition; got %v", err)
			}
			msg := err.Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("drift message does not carry %q; got:\n%s", w, msg)
				}
			}
			// The Release edit is the user's own; nothing about it is a fetch, and the
			// entry names no Release it was not given.
			for _, wrong := range []string{"--upgrade", "offline", "URL", "<release>"} {
				if strings.Contains(msg, wrong) {
					t.Errorf("drift message for a Release file points at %q, which is not what fixes it:\n%s", wrong, msg)
				}
			}
		})
	}
}

// TestReleaseDocumentsRoundTripThroughTheLoader writes a lockfile carrying the
// section and loads it back through the schema-validating loader. The load is
// the assertion that matters: the schema forbids unknown top-level keys, so a
// section the writer emits and the schema does not declare would make every
// lockfile a pull writes fail to load.
func TestReleaseDocumentsRoundTripThroughTheLoader(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "release.lock")
	docs := []lockfile.ReleaseDocument{
		{Path: "release.yaml", Digest: releaseDigestA},
		{Path: "../shared/base-release.yaml", Digest: parentDigest},
	}
	if err := lockfile.WriteLockfile(path, &lockfile.Lockfile{
		Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z", ReleaseDocuments: docs,
	}); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("a lockfile carrying release_documents must load; got %v", err)
	}
	if len(lf.ReleaseDocuments) != len(docs) {
		t.Fatalf("round trip kept %d release documents, want %d: %+v", len(lf.ReleaseDocuments), len(docs), lf.ReleaseDocuments)
	}
	// The recorded order is the chain's — the Release first — and is kept rather
	// than sorted: "../shared/…" sorts before "release.yaml".
	for i := range docs {
		if lf.ReleaseDocuments[i] != docs[i] {
			t.Errorf("release_documents[%d] = %+v, want %+v", i, lf.ReleaseDocuments[i], docs[i])
		}
	}
}

// TestReleaseDocumentsSectionIsOmittedWhenEmpty pins that a lockfile recording
// no Release file writes no section at all rather than an empty list, and that
// such a lockfile still loads — which is what a lockfile written before the
// section existed looks like to the loader.
func TestReleaseDocumentsSectionIsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := lockfile.WriteLockfile(path, &lockfile.Lockfile{
		Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "release_documents") {
		t.Errorf("an empty section was written:\n%s", raw)
	}
	if _, _, err := lockfile.LoadLockfile(path); err != nil {
		t.Errorf("a lockfile without release_documents must still load; got %v", err)
	}
}
