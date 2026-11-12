// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestReachesComparesPlacesNotSpellings pins the one comparison every surface makes
// between what somebody named and a path it meets: places, with the links of both
// sides resolved, and a directory reached by a named directory above it while a
// file is reached only by being named.
//
// The rows that discriminate a comparison of places from one of spellings are the
// ones where the two spell different paths to one place — a link named and the
// place it leads to met, and the other way round. The rows that discriminate a
// directory from a file are the ones beneath a named directory. Every row's place
// exists, so no row is decided by the path missing; the missing path has a row of
// its own.
func TestReachesComparesPlacesNotSpellings(t *testing.T) {
	outside := t.TempDir()
	shared := filepath.Join(outside, "shared")
	for _, dir := range []string{filepath.Join(shared, "feature"), filepath.Join(outside, "shared-old")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	release := filepath.Join(outside, "release.yaml")
	other := filepath.Join(outside, "other.yaml")
	inShared := filepath.Join(shared, "release.yaml")
	for _, file := range []string{release, other, inShared} {
		if err := os.WriteFile(file, []byte("kind: Release\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	catalogLink := filepath.Join(ws, "catalog")
	link(t, shared, catalogLink)
	link(t, filepath.Join(shared, "feature"), filepath.Join(ws, "beneath"))
	link(t, outside, filepath.Join(ws, "above"))
	releaseLink := filepath.Join(ws, "linked-release.yaml")
	link(t, release, releaseLink)

	for _, row := range []struct {
		name  string
		named []string
		path  string
		want  bool
	}{
		{name: "control: a directory named as it is spelled", named: []string{shared}, path: shared, want: true},
		{name: "a link met, leading to the place a directory named in another spelling leads to",
			named: []string{shared}, path: catalogLink, want: true},
		{name: "the place met, where the directory was named through a link to it",
			named: []string{catalogLink}, path: shared, want: true},
		{name: "a named directory spelled with a trailing separator", named: []string{shared + string(filepath.Separator)},
			path: catalogLink, want: true},
		{name: "a link to a directory beneath a named directory is reached by it",
			named: []string{shared}, path: filepath.Join(ws, "beneath"), want: true},
		{name: "a link to a directory above a named directory is not reached",
			named: []string{shared}, path: filepath.Join(ws, "above"), want: false},
		{name: "a sibling whose name merely extends the named directory's is not reached",
			named: []string{shared}, path: filepath.Join(outside, "shared-old"), want: false},
		{name: "a link to a place nothing named is not reached",
			named: []string{filepath.Join(shared, "feature")}, path: catalogLink, want: false},
		{name: "a Release file named, met through a link to it", named: []string{release}, path: releaseLink, want: true},
		{name: "a Release file named through a link, met where it lies", named: []string{releaseLink}, path: release, want: true},
		{name: "another file is not reached by a named file", named: []string{release}, path: other, want: false},
		{name: "a file beneath a named directory is not reached: a file is reached only by being named",
			named: []string{shared}, path: inShared, want: false},
		{name: "a path that is not there is reached by nothing, even where it is named as spelled",
			named: []string{filepath.Join(ws, "not-there")}, path: filepath.Join(ws, "not-there"), want: false},
		{name: "nothing named reaches nothing", named: nil, path: shared, want: false},
		{name: "an empty path is reached by nothing", named: []string{shared}, path: "", want: false},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := workspaceconfig.Reaches(row.named, row.path); got != row.want {
				t.Errorf("Reaches(%q, %s) = %v, want %v", row.named, row.path, got, row.want)
			}
		})
	}
}
