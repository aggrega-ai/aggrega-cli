// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// TestResolvesInside drives the one definition of inside over every shape of path
// it has to tell apart, one shape per row, beside a control that is plainly inside.
//
// Each row that leads out names the link the verdict leaves through, because that
// is what a caller's refusal names: a verdict that only said "outside" would leave
// a reader holding a path that, as spelled, looks inside. The rows that pass a link
// over — one leading nowhere, two pointing at each other — are inside because the
// directory holding them is, which is the definition rather than an exception to
// it.
func TestResolvesInside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	resolvedOutside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("resolve the outside directory: %v", err)
	}

	mustMkdir(t, filepath.Join(root, "real"))
	mustWrite(t, filepath.Join(root, "real", "file.txt"))
	mustWrite(t, filepath.Join(outside, "file.txt"))
	mustLink(t, filepath.Join(root, "real"), filepath.Join(root, "in-link"))
	mustLink(t, outside, filepath.Join(root, "out-link"))
	mustLink(t, filepath.Join(outside, "file.txt"), filepath.Join(root, "out-file"))
	mustLink(t, filepath.Join(outside, "missing"), filepath.Join(root, "dangling"))
	mustLink(t, filepath.Join(root, "loop-b"), filepath.Join(root, "loop-a"))
	mustLink(t, filepath.Join(root, "loop-a"), filepath.Join(root, "loop-b"))
	rootLink := filepath.Join(t.TempDir(), "root-link")
	mustLink(t, root, rootLink)

	rows := []struct {
		name     string
		root     string
		path     string
		inside   bool
		wantLink string
		wantTo   string
	}{
		{name: "control: a file inside", root: root, path: filepath.Join(root, "real", "file.txt"), inside: true},
		{name: "the root itself", root: root, path: root, inside: true},
		{name: "a path not made yet beneath a directory inside", root: root, path: filepath.Join(root, "real", "new", "x.yaml"), inside: true},
		{name: "a link pointing inside", root: root, path: filepath.Join(root, "in-link", "file.txt"), inside: true},
		{name: "a link leading nowhere is passed over for its directory", root: root, path: filepath.Join(root, "dangling"), inside: true},
		{name: "links round a loop are passed over for their directory", root: root, path: filepath.Join(root, "loop-a", "x"), inside: true},
		{name: "a root reached through a link of its own", root: rootLink, path: filepath.Join(rootLink, "real", "file.txt"), inside: true},
		{name: "a path entering the root through a link", root: root, path: filepath.Join(rootLink, "real", "file.txt"), inside: true},
		{name: "a path entering the root through a link and leaving it through another", root: root, path: filepath.Join(rootLink, "out-link", "new", "x.yaml"), wantLink: filepath.Join(rootLink, "out-link"), wantTo: resolvedOutside},
		{name: "a directory link leading out", root: root, path: filepath.Join(root, "out-link"), wantLink: filepath.Join(root, "out-link"), wantTo: resolvedOutside},
		{name: "a path not made yet beneath a link leading out", root: root, path: filepath.Join(root, "out-link", "new", "x.yaml"), wantLink: filepath.Join(root, "out-link"), wantTo: resolvedOutside},
		{name: "a file link leading out", root: root, path: filepath.Join(root, "out-file"), wantLink: filepath.Join(root, "out-file"), wantTo: filepath.Join(resolvedOutside, "file.txt")},
		{name: "a path spelled outside the root", root: root, path: filepath.Join(outside, "file.txt")},
		{name: "a path climbing out of the root", root: root, path: filepath.Join(root, "..", filepath.Base(outside), "file.txt")},
		{name: "an empty root names nothing to judge against", root: "", path: filepath.Join(root, "real", "file.txt")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, inside := pathscope.ResolvesInside(row.root, row.path)
			if inside != row.inside {
				t.Fatalf("ResolvesInside(%s, %s) inside = %t, want %t (judged %+v)", row.root, row.path, inside, row.inside, got)
			}
			if got.Link != row.wantLink {
				t.Errorf("Link = %q, want %q", got.Link, row.wantLink)
			}
			if got.LinkLeadsTo != row.wantTo {
				t.Errorf("LinkLeadsTo = %q, want %q", got.LinkLeadsTo, row.wantTo)
			}
		})
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
