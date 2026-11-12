// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// listingOf builds a source listing from a root, whether that root is a
// directory, and the root-relative entries under it. Each entry is written as
// its path, with a trailing "/" marking a directory entry.
func listingOf(root string, rootIsDir bool, entries ...string) *rendercore.SourceListing {
	l := &rendercore.SourceListing{Root: root, RootIsDir: rootIsDir}
	for _, e := range entries {
		if dir := strings.HasSuffix(e, "/"); dir {
			l.Entries = append(l.Entries, rendercore.SourceEntry{Path: strings.TrimSuffix(e, "/"), IsDir: true})
			continue
		}
		l.Entries = append(l.Entries, rendercore.SourceEntry{Path: e})
	}
	return l
}

// TestResolveSourceSelection_ResolvesTheSingleTopLevelDocument asserts the
// source-selection contract for the two acquired-source shapes a compose Recipe
// produces. A single-file source root IS the compose document, so it resolves to
// the root itself (the empty relative path). A directory source root resolves to
// its one top-level .yaml/.yml entry — nested paths and directory entries are
// not candidates, whatever their suffix.
func TestResolveSourceSelection_ResolvesTheSingleTopLevelDocument(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		listing *rendercore.SourceListing
		want    string
	}{
		{
			name:    "file-root-is-itself-the-document",
			listing: listingOf("/src/docker-compose.yaml", false),
			want:    "",
		},
		{
			name:    "directory-with-one-yaml-entry",
			listing: listingOf("/src/frag", true, "README.md", "docker-compose.yaml"),
			want:    "docker-compose.yaml",
		},
		{
			// .yml is the same candidate class as .yaml.
			name:    "directory-with-one-yml-entry",
			listing: listingOf("/src/frag", true, "compose.yml"),
			want:    "compose.yml",
		},
		{
			// A nested document is not a top-level candidate, so the single
			// top-level one resolves unambiguously despite the deeper file.
			name:    "nested-yaml-is-not-a-candidate",
			listing: listingOf("/src/frag", true, "compose.yaml", "overlays/prod.yaml"),
			want:    "compose.yaml",
		},
		{
			// A directory whose name ends in .yaml is a directory, not a document.
			name:    "directory-entry-named-like-a-document-is-not-a-candidate",
			listing: listingOf("/src/frag", true, "conf.yaml/", "compose.yaml"),
			want:    "compose.yaml",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveSourceSelection(tc.listing)
			if err != nil {
				t.Fatalf("ResolveSourceSelection: %v", err)
			}
			if got != tc.want {
				t.Errorf("relative path = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestResolveSourceSelection_AmbiguousOrEmptyDirectoryHalts asserts the halt
// side of the same contract: neither zero nor several top-level documents is
// resolved by a silent first-wins. Each halt names the source the author
// supplied, and the several-documents halt also names how many it found, so an
// author of a multi-file fragment can see what to remove.
func TestResolveSourceSelection_AmbiguousOrEmptyDirectoryHalts(t *testing.T) {
	t.Parallel()
	const root = "/src/frag"
	for _, tc := range []struct {
		name    string
		listing *rendercore.SourceListing
		wantErr string
	}{
		{
			name:    "directory-with-no-document",
			listing: listingOf(root, true, "README.md", "Makefile"),
			wantErr: "contains no .yaml/.yml entries",
		},
		{
			name:    "directory-whose-only-documents-are-nested",
			listing: listingOf(root, true, "overlays/", "overlays/prod.yaml"),
			wantErr: "contains no .yaml/.yml entries",
		},
		{
			name:    "directory-with-two-documents",
			listing: listingOf(root, true, "a.yaml", "b.yml"),
			wantErr: "contains 2 .yaml/.yml entries; expected exactly one",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveSourceSelection(tc.listing)
			if err == nil {
				t.Fatalf("expected a halt; got %q", got)
			}
			if got != "" {
				t.Errorf("expected no selection alongside the halt; got %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error does not state the resolution failure %q: %v", tc.wantErr, err)
			}
			if !strings.Contains(err.Error(), root) {
				t.Errorf("error does not name the source root %q: %v", root, err)
			}
		})
	}
}
