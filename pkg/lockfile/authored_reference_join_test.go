// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// What this file pins.
//
// A row is found by what the author wrote. For a kind whose resolver pins the
// authored version verbatim that is the version, and the join is what it always
// was. For a kind that resolves a reference to something immutable — a git tag to
// a commit — the row is pinned at a value the authored document never contains,
// so the reference is the only term that can reach it.
//
// The rows below are chosen to disagree with each other. A join that matched on
// the reference alone would fail the version-only row; one that matched on the
// version alone would fail the reference row; one that matched too loosely would
// fail the two miss rows, which exist so that "found" is never satisfiable by a
// lookup that returns whatever it saw first.

// gitRowAtCommit is the shape a git source authored at a tag produces: pinned at
// the commit, carrying the reference that resolved to it.
func gitRowAtCommit(commit, ref string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:00:00Z",
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind:          "git-repository",
			URL:           "git://example.invalid/repo",
			PinnedVersion: commit,
			Digest:        "sha256:aa",
			ReferencedBy: []lockfile.SourceArtifactReference{
				{Entity: "recipe", URI: "u", Field: "spec.source", RequestedRef: ref},
			},
		}},
	}
}

func TestFindSourceArtifactJoinsOnTheAuthoredReference(t *testing.T) {
	t.Parallel()
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// A helm row pins what its author wrote, so it carries no reference at all —
	// the non-regression side of the join.
	helm := &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:00:00Z",
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind:          "helm-oci-chart",
			URL:           "oci://r/c",
			PinnedVersion: "25.0.8",
			Digest:        "sha256:bb",
			ReferencedBy: []lockfile.SourceArtifactReference{
				{Entity: "recipe", URI: "u", Field: "spec.source"},
			},
		}},
	}

	cases := []struct {
		name      string
		lf        *lockfile.Lockfile
		kind, url string
		version   string
		ref       string
		wantFound bool
		wantPin   string
	}{
		{
			name: "a reference reaches the row pinned at the commit it resolved to",
			lf:   gitRowAtCommit(commit, "v1.2.3"), kind: "git-repository", url: "git://example.invalid/repo",
			version: "", ref: "v1.2.3", wantFound: true, wantPin: commit,
		},
		{
			name: "the commit still reaches its own row when the author wrote the commit",
			lf:   gitRowAtCommit(commit, "v1.2.3"), kind: "git-repository", url: "git://example.invalid/repo",
			version: commit, ref: "", wantFound: true, wantPin: commit,
		},
		{
			name: "a reference and the commit together reach the one row",
			lf:   gitRowAtCommit(commit, "v1.2.3"), kind: "git-repository", url: "git://example.invalid/repo",
			version: commit, ref: "v1.2.3", wantFound: true, wantPin: commit,
		},
		{
			name: "a row recorded before any reference was is still reached by its pinned version",
			lf:   gitRowAtCommit(commit, ""), kind: "git-repository", url: "git://example.invalid/repo",
			version: commit, ref: "v1.2.3", wantFound: true, wantPin: commit,
		},
		{
			name: "a reference the record does not carry finds nothing",
			lf:   gitRowAtCommit(commit, "v1.2.3"), kind: "git-repository", url: "git://example.invalid/repo",
			version: "", ref: "v9.9.9", wantFound: false,
		},
		{
			name: "an authored version that is not the pinned one finds nothing without a reference",
			lf:   gitRowAtCommit(commit, "v1.2.3"), kind: "git-repository", url: "git://example.invalid/repo",
			version: "v1.2.3", ref: "", wantFound: false,
		},
		{
			name: "a kind that pins the authored version verbatim is unaffected",
			lf:   helm, kind: "helm-oci-chart", url: "oci://r/c",
			version: "25.0.8", ref: "", wantFound: true, wantPin: "25.0.8",
		},
		{
			name: "the location terms still separate artifacts",
			lf:   gitRowAtCommit(commit, "v1.2.3"), kind: "git-repository", url: "git://example.invalid/other",
			version: "", ref: "v1.2.3", wantFound: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := tc.lf.FindSourceArtifact(tc.kind, tc.url, "", "", tc.version, tc.ref)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
			if !tc.wantFound {
				return
			}
			if got.PinnedVersion != tc.wantPin {
				t.Errorf("pinned version = %q, want %q", got.PinnedVersion, tc.wantPin)
			}
		})
	}
}

// TestFindSourceArtifactPrefersTheReferenceOverAnUnrelatedPinnedVersion pins the
// ORDER of the two passes. With two rows of one repository — one pinned at a
// commit reached by a reference, one whose pinned version happens to equal the
// string the caller passes as its version — a lookup carrying both terms must
// return the row the reference names. Order is invisible while only one row can
// match, which is why this needs two.
func TestFindSourceArtifactPrefersTheReferenceOverAnUnrelatedPinnedVersion(t *testing.T) {
	t.Parallel()
	const wanted = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:00:00Z",
		SourceArtifacts: []lockfile.SourceArtifact{
			{
				Kind: "git-repository", URL: "git://example.invalid/repo",
				PinnedVersion: "v1.2.3", Digest: "sha256:decoy",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "recipe", URI: "u", Field: "spec.source"},
				},
			},
			{
				Kind: "git-repository", URL: "git://example.invalid/repo",
				PinnedVersion: wanted, Digest: "sha256:aa",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "recipe", URI: "u", Field: "spec.source", RequestedRef: "v1.2.3"},
				},
			},
		},
	}
	got, found := lf.FindSourceArtifact("git-repository", "git://example.invalid/repo", "", "", "v1.2.3", "v1.2.3")
	if !found {
		t.Fatal("the reference names a row this record carries; the lookup found nothing")
	}
	if got.PinnedVersion != wanted {
		t.Errorf("pinned version = %q, want %q — the pinned-version pass answered ahead of the reference",
			got.PinnedVersion, wanted)
	}
}
