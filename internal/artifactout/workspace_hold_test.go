// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package artifactout

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestBuildOutputAndTraceAreHeldToTheWorkspace drives both writes a build performs
// — its output, through WriteBuildOutput, and its trace, through ReplaceAll — over
// a workspace in which one link at a time leads out, beside a control with none.
//
// Each refused row is checked twice: against the condition, and against the
// directory the link leads to, which must hold after the call exactly what it held
// before. That second check is the one that matters: the output's clear and the
// trace's replacement are deletions, and a refusal raised after either had run
// would still have destroyed what the link points at.
func TestBuildOutputAndTraceAreHeldToTheWorkspace(t *testing.T) {
	files := map[string][]byte{"app/compose/docker-compose.yaml": []byte("services: {}\n")}
	type write func(root, dir string) error
	build := write(func(root, dir string) error {
		_, err := WriteBuildOutput(root, dir, files)
		return err
	})
	trace := write(func(root, dir string) error { return ReplaceAll(root, dir, files) })

	rows := []struct {
		name    string
		write   write
		layout  func(t *testing.T, root, outside string) string
		refused bool
	}{
		{name: "control: an output directory inside", write: build, layout: func(t *testing.T, root, _ string) string {
			return filepath.Join(root, "dist")
		}},
		{name: "control: a trace directory inside", write: trace, layout: func(t *testing.T, root, _ string) string {
			return filepath.Join(root, ".trace")
		}},
		{name: "an output directory that is a link leading out", write: build, refused: true, layout: func(t *testing.T, root, outside string) string {
			linkTo(t, outside, filepath.Join(root, "dist"))
			return filepath.Join(root, "dist")
		}},
		{name: "a directory beneath the output that is a link leading out", write: build, refused: true, layout: func(t *testing.T, root, outside string) string {
			if err := os.MkdirAll(filepath.Join(root, "dist"), 0o755); err != nil {
				t.Fatal(err)
			}
			linkTo(t, outside, filepath.Join(root, "dist", "app"))
			return filepath.Join(root, "dist")
		}},
		{name: "a trace directory that is a link leading out", write: trace, refused: true, layout: func(t *testing.T, root, outside string) string {
			linkTo(t, outside, filepath.Join(root, ".trace"))
			return filepath.Join(root, ".trace")
		}},
		{name: "an output directory that is a link pointing inside", write: build, layout: func(t *testing.T, root, _ string) string {
			if err := os.MkdirAll(filepath.Join(root, "real-dist"), 0o755); err != nil {
				t.Fatal(err)
			}
			linkTo(t, filepath.Join(root, "real-dist"), filepath.Join(root, "dist"))
			return filepath.Join(root, "dist")
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(outside, "boundaries", "old"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "boundaries", "old", "keep.txt"), []byte("not the build's"), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := row.layout(t, root, outside)
			before := listing(t, outside)

			err := row.write(root, dir)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a write the workspace holds: %v", err)
				}
				if _, serr := os.Stat(filepath.Join(dir, "app", "compose", "docker-compose.yaml")); serr != nil {
					t.Errorf("the write did not land: %v", serr)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if after := listing(t, outside); !reflect.DeepEqual(before, after) {
				t.Errorf("the directory the link leads to changed:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func linkTo(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}

// listing is every entry under dir with the bytes of each file, the state a
// refused write must leave untouched.
func listing(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	return out
}
