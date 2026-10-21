// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestComputeFinalPathIsSlashSeparated pins that the directory a node's output
// is grouped under is a slash-separated name on every platform.
//
// The value prefixes every file the group emits and reaches an assembling
// provider as its boundary path, so a host-spelled join would make the rendered
// output tree, and what an assembler derives from that path, depend on which
// platform ran the build. On a platform whose separator is a slash the two
// spellings coincide, so this passes there either way; it fails on one whose
// separator is a backslash the moment the join stops being a slash join.
func TestComputeFinalPathIsSlashSeparated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		bp   string
		bi   *resolver.BoundaryInfra
		want string
	}{
		{
			name: "default directory and sub-directory",
			bp:   "prod",
			want: "boundaries/prod/helm",
		},
		{
			name: "nested boundary path under the default directory",
			bp:   "aws-prod/eks-main",
			want: "boundaries/aws-prod/eks-main/helm",
		},
		{
			name: "authored directory and sub-directory each spanning segments",
			bp:   "prod",
			bi: &resolver.BoundaryInfra{
				Directory:    "clusters/{{ .boundary_name }}",
				SubDirectory: "apps/{{ .capability }}",
			},
			want: "clusters/prod/apps/helm",
		},
		{
			name: "synthesized default boundary emits without a directory segment",
			bp:   "default",
			bi:   &resolver.BoundaryInfra{Synthesized: true},
			want: "helm",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := computeFinalPath("release", "web", tc.bp, "helm", "web-recipe", tc.bi, nil, nil, nil)
			if err != nil {
				t.Fatalf("computeFinalPath: %v", err)
			}
			if got != tc.want {
				t.Errorf("computeFinalPath = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, `\`) {
				t.Errorf("computeFinalPath = %q carries a backslash; an output group's directory is a slash-separated name", got)
			}
		})
	}
}

// TestTheDispatchPathIsCleanedOnceAndRefusedWhereItNamesNoDirectoryBeneathTheRoot
// drives the one composition of a boundary's dispatch path with the evaluated
// `directory:` and `subDirectory:` values a Release can produce.
//
// The cleaned rows are the reason the path is cleaned at all: the dispatch path is
// the key contributors are grouped by, compared as a string, so every spelling of
// one directory has to arrive as one string or two boundaries writing one
// directory form two groups that never see each other. The rows with no
// sub-directory matter most, because a joined path was already cleaned by the
// join and the directory alone was not.
//
// The refused rows each assert the condition's own identity and the field named,
// so a refusal blaming the wrong field — the author would then edit a value that
// is not at fault — fails here. Every refusal is recovered from a panic too, since
// a recovered panic does not fail a subtest on its own.
func TestTheDispatchPathIsCleanedOnceAndRefusedWhereItNamesNoDirectoryBeneathTheRoot(t *testing.T) {
	t.Parallel()

	cleaned := []struct {
		name, dir, sub, want string
	}{
		{"a plain directory, no sub-directory", "out/a", ".", "out/a"},
		{"a doubled separator, no sub-directory", "out//a", ".", "out/a"},
		{"a leading dot segment, no sub-directory", "./out/a", ".", "out/a"},
		{"a trailing separator, no sub-directory", "out/a/", ".", "out/a"},
		{"an interior climb that stays inside, no sub-directory", "out/x/../a", ".", "out/a"},
		{"an evaluated empty sub-directory is no sub-directory", "out//a", "", "out/a"},
		{"a directory and a sub-directory", "out", "a", "out/a"},
		{"a doubled separator across the join", "out/", "/a", "out/a"},
		{"a directory at the root with a sub-directory", ".", "helm", "helm"},
		{"the synthesized boundary's empty directory with its sub-directory", "", "helm", "helm"},
		{"a sub-directory climbing back inside the directory", "out/a", "x/..", "out/a"},
		{"a name opening with two dots", "..out", ".", "..out"},
	}
	for _, tc := range cleaned {
		tc := tc
		t.Run("cleaned: "+tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cleanDispatchPath("shop", "prod", tc.dir, tc.sub)
			if err != nil {
				t.Fatalf("directory %q, subDirectory %q was refused: %v", tc.dir, tc.sub, err)
			}
			if got != tc.want {
				t.Errorf("directory %q, subDirectory %q composed %q; want %q", tc.dir, tc.sub, got, tc.want)
			}
		})
	}

	refused := []struct {
		name, dir, sub, field, value string
	}{
		{"the parent of the root", "..", ".", "directory:", ".."},
		{"a sibling of the root", "../escape", ".", "directory:", "../escape"},
		{"a climb hidden behind a descent", "a/../..", ".", "directory:", "a/../.."},
		{"a climbing directory under a sub-directory", "../escape", "helm", "directory:", "../escape"},
		{"an absolute directory", "/srv/out", "helm", "directory:", "/srv/out"},
		{"the root itself, no sub-directory", ".", ".", "directory:", "."},
		{"an evaluated empty directory, no sub-directory", "", "", "directory:", ""},
		{"a climb back onto the root, no sub-directory", "a/..", ".", "directory:", "a/.."},
		{"a sub-directory climbing out", "out", "../../x", "subDirectory:", "../../x"},
		{"a sub-directory climbing back onto the root", "out", "..", "subDirectory:", ".."},
		{"a climbing sub-directory under the synthesized boundary", "", "../x", "subDirectory:", "../x"},
	}
	for _, tc := range refused {
		tc := tc
		t.Run("refused: "+tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("directory %q, subDirectory %q panicked: %v", tc.dir, tc.sub, r)
				}
			}()
			got, err := cleanDispatchPath("shop", "prod", tc.dir, tc.sub)
			if !errors.Is(err, ErrBoundaryDirectoryNotBeneathOutputRoot()) {
				t.Fatalf("directory %q, subDirectory %q composed %q (err %v); want "+
					"ErrBoundaryDirectoryNotBeneathOutputRoot", tc.dir, tc.sub, got, err)
			}
			for _, want := range []string{`Release "shop"`, `boundary "prod"`, tc.field + ` "` + tc.value + `"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, err)
				}
			}
		})
	}

	// Through computeFinalPath itself, so the refusal is reached from the evaluated
	// templates and not only from the composition: an authored directory climbing
	// out, with the sub-directory disabled as the documented "." does.
	_, err := computeFinalPath("shop", "web", "prod", "helm", "web-recipe",
		&resolver.BoundaryInfra{Directory: "../escape", SubDirectory: "."}, nil, nil, nil)
	if !errors.Is(err, ErrBoundaryDirectoryNotBeneathOutputRoot()) {
		t.Fatalf("computeFinalPath admitted directory \"../escape\": %v", err)
	}
	got, err := computeFinalPath("shop", "web", "prod", "helm", "web-recipe",
		&resolver.BoundaryInfra{Directory: "out//a", SubDirectory: "."}, nil, nil, nil)
	if err != nil || got != "out/a" {
		t.Fatalf("computeFinalPath(directory \"out//a\", subDirectory \".\") = %q, %v; want \"out/a\"", got, err)
	}
}
