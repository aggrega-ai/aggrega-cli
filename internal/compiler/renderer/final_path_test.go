// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
			got, err := computeFinalPath("web", tc.bp, "helm", "web-recipe", tc.bi, nil, nil, nil)
			if err != nil {
				t.Fatalf("computeFinalPath: %v", err)
			}
			if got.path != tc.want {
				t.Errorf("computeFinalPath = %q, want %q", got.path, tc.want)
			}
			if strings.Contains(got.path, `\`) {
				t.Errorf("computeFinalPath = %q carries a backslash; an output group's directory is a slash-separated name", got.path)
			}
		})
	}
}

// boundaryPathView presents one group's boundary path declarations, and nothing
// else: the rule under test reads that one fact, so any other read panics through
// the embedded interface rather than answering a zero value.
type boundaryPathView struct {
	constraint.DispatchedView
	declared []constraint.BoundaryPathDeclaration
}

func (v boundaryPathView) AssemblyGroupBoundaryPathDeclarations(
	_ string, _ string,
) []constraint.BoundaryPathDeclaration {
	return v.declared
}

// runBoundaryPathRule invokes the rule declared over a group's path the way a
// build does — through the traversal, over the one group the composed path keys —
// and returns what it raised.
func runBoundaryPathRule(t *testing.T, assemblyPath string, declared ...constraint.BoundaryPathDeclaration) []constraint.Objection {
	t.Helper()
	axes := derivedNamesAxes{path: assemblyPath, provider: "helm"}
	var view constraint.DispatchedView = boundaryPathView{declared: declared}
	raised, err := constraint.RunOnAssemblyPathProviders(
		func() error { return nil }, axes, axes, view,
		AssemblyPathNamesADirectoryBeneathTheOutputRoot,
	)
	if err != nil {
		t.Fatalf("the traversal failed: %v", err)
	}
	return raised
}

// TestTheDispatchPathIsCleanedOnceAndRefusedWhereItNamesNoDirectoryBeneathTheRoot
// drives the one composition of a boundary's dispatch path with the evaluated
// `directory:` and `subDirectory:` values a Release can produce, and the rule
// declared over the group that path keys.
//
// The cleaned rows are the reason the path is cleaned at all: the dispatch path is
// the key contributors are grouped by, compared as a string, so every spelling of
// one directory has to arrive as one string or two boundaries writing one
// directory form two groups that never see each other. The rows with no
// sub-directory matter most, because a joined path was already cleaned by the
// join and the directory alone was not. Each cleaned row is also handed to the rule,
// which raises nothing over it — the admitted control for every refused row.
//
// The refused rows each assert the condition's own identity and the field named,
// so a refusal blaming the wrong field — the author would then edit a value that
// is not at fault — fails here. Every refusal is recovered from a panic too, since
// a recovered panic does not fail a subtest on its own.
func TestTheDispatchPathIsCleanedOnceAndRefusedWhereItNamesNoDirectoryBeneathTheRoot(t *testing.T) {
	t.Parallel()

	declaration := func(dir, sub string) constraint.BoundaryPathDeclaration {
		return constraint.BoundaryPathDeclaration{Release: "shop", Boundary: "prod", Directory: dir, SubDirectory: sub}
	}

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
			got := cleanDispatchPath(tc.dir, tc.sub)
			if got != tc.want {
				t.Errorf("directory %q, subDirectory %q composed %q; want %q", tc.dir, tc.sub, got, tc.want)
			}
			if raised := runBoundaryPathRule(t, got, declaration(tc.dir, tc.sub)); len(raised) != 0 {
				t.Errorf("directory %q, subDirectory %q composed %q, which the rule refused: %v",
					tc.dir, tc.sub, got, constraint.ObjectionMessage(raised[0]))
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
			got := cleanDispatchPath(tc.dir, tc.sub)
			raised := runBoundaryPathRule(t, got, declaration(tc.dir, tc.sub))
			if len(raised) != 1 {
				t.Fatalf("directory %q, subDirectory %q composed %q and the rule raised %d objection(s); want one",
					tc.dir, tc.sub, got, len(raised))
			}
			if !errors.Is(raised[0].Sentinel(), ErrBoundaryDirectoryNotBeneathOutputRoot()) {
				t.Fatalf("directory %q, subDirectory %q raised %v; want ErrBoundaryDirectoryNotBeneathOutputRoot",
					tc.dir, tc.sub, raised[0].Sentinel())
			}
			message := constraint.ObjectionMessage(raised[0])
			for _, want := range []string{`Release "shop"`, `boundary "prod"`, tc.field + ` "` + tc.value + `"`,
				`output path "` + got + `"`, "write a directory beneath the output root"} {
				if !strings.Contains(message, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, message)
				}
			}
		})
	}

	// One boundary whose two components evaluated its declaration alike is one
	// objection, and two boundaries meeting at one refused path are one each, so a
	// finding names every site an author edits and none of them twice.
	raised := runBoundaryPathRule(t, "../escape",
		declaration("../escape", "."), declaration("../escape", "."),
		constraint.BoundaryPathDeclaration{Release: "shop", Boundary: "staging", Directory: "../escape", SubDirectory: "."})
	if len(raised) != 2 {
		t.Fatalf("two boundaries at one refused path raised %d objection(s); want one per boundary", len(raised))
	}

	// Through computeFinalPath itself, so the composition is reached from the
	// evaluated templates and not only from the two values: an authored directory
	// climbing out, with the sub-directory disabled as the documented "." does, is
	// composed as it is and refused by the rule; a doubled separator is one directory.
	eval, err := computeFinalPath("web", "prod", "helm", "web-recipe",
		&resolver.BoundaryInfra{Directory: "../escape", SubDirectory: "."}, nil, nil, nil)
	if err != nil || eval.path != "../escape" || eval.directory != "../escape" || eval.subDirectory != "." {
		t.Fatalf("computeFinalPath(directory \"../escape\", subDirectory \".\") = %+v, %v", eval, err)
	}
	if raised := runBoundaryPathRule(t, eval.path, declaration(eval.directory, eval.subDirectory)); len(raised) != 1 {
		t.Fatalf("the rule raised %d objection(s) over directory \"../escape\"; want one", len(raised))
	}
	eval, err = computeFinalPath("web", "prod", "helm", "web-recipe",
		&resolver.BoundaryInfra{Directory: "out//a", SubDirectory: "."}, nil, nil, nil)
	if err != nil || eval.path != "out/a" {
		t.Fatalf("computeFinalPath(directory \"out//a\", subDirectory \".\") = %+v, %v; want \"out/a\"", eval, err)
	}
}
