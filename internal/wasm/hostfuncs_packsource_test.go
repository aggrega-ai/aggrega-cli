// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"os"
	"path/filepath"
	"testing"
)

// writePackFixture lays out a small source tree for the packer tests: a.txt at
// the root, and sub/b.txt + sub/c.txt one level down.
func writePackFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("AAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("BBB"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "c.txt"), []byte("CCC"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestPackSourceSelection_File pins the file op: a single file packed keyed by
// its base name.
func TestPackSourceSelection_File(t *testing.T) {
	t.Parallel()
	p := &Plugin{sourceRoot: writePackFixture(t)}
	proj, err := p.packSourceSelection("file", "sub/b.txt")
	if err != nil {
		t.Fatalf("file pack: %v", err)
	}
	if len(proj.Files) != 1 || proj.Files[0].Path != "b.txt" || string(proj.Files[0].Content) != "BBB" {
		t.Errorf("file pack = %+v; want one file keyed b.txt with content BBB", proj.Files)
	}
}

// TestPackSourceSelection_Dir pins the dir op: the immediate files of a
// directory, each keyed by its base name, in sorted order — and directories
// under it are not descended into.
func TestPackSourceSelection_Dir(t *testing.T) {
	t.Parallel()
	p := &Plugin{sourceRoot: writePackFixture(t)}
	proj, err := p.packSourceSelection("dir", "sub")
	if err != nil {
		t.Fatalf("dir pack: %v", err)
	}
	if len(proj.Files) != 2 {
		t.Fatalf("dir pack = %+v; want two files", proj.Files)
	}
	if proj.Files[0].Path != "b.txt" || proj.Files[1].Path != "c.txt" {
		t.Errorf("dir pack keys = [%q %q]; want [b.txt c.txt] in sorted order", proj.Files[0].Path, proj.Files[1].Path)
	}
}

// TestPackSourceSelection_Tree pins the tree op: the whole subtree, recursively,
// keyed by slash-separated root-relative paths in the host's deterministic walk
// order.
func TestPackSourceSelection_Tree(t *testing.T) {
	t.Parallel()
	p := &Plugin{sourceRoot: writePackFixture(t)}
	proj, err := p.packSourceSelection("tree", "")
	if err != nil {
		t.Fatalf("tree pack: %v", err)
	}
	got := make([]string, 0, len(proj.Files))
	for _, f := range proj.Files {
		got = append(got, f.Path)
	}
	want := []string{"a.txt", "sub/b.txt", "sub/c.txt"}
	if len(got) != len(want) {
		t.Fatalf("tree pack keys = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tree pack keys = %v; want %v", got, want)
			break
		}
	}
}

// TestPackSourceSelection_UnknownOpHalts pins that an unrecognised op is a loud
// error rather than a silent empty pack.
func TestPackSourceSelection_UnknownOpHalts(t *testing.T) {
	t.Parallel()
	p := &Plugin{sourceRoot: writePackFixture(t)}
	if _, err := p.packSourceSelection("bogus", ""); err == nil {
		t.Error("unknown op should halt")
	}
}

// TestPackSourceSelection_SandboxRejectsEscape pins the sandbox: a selection path
// escaping above the source root is refused, so a guest cannot read outside the
// acquired source.
func TestPackSourceSelection_SandboxRejectsEscape(t *testing.T) {
	t.Parallel()
	p := &Plugin{sourceRoot: writePackFixture(t)}
	for _, rel := range []string{"../outside.txt", "sub/../../outside.txt", "../../etc/passwd"} {
		if _, err := p.packSourceSelection("file", rel); err == nil {
			t.Errorf("selection %q escaping the source root should be refused", rel)
		}
	}
}

// TestResolveUnderSourceRoot_RootItself pins that the empty and dot paths both
// resolve to the source root itself, so a single-file source is packed with an
// empty path.
func TestResolveUnderSourceRoot_RootItself(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := &Plugin{sourceRoot: root}
	for _, rel := range []string{"", "."} {
		got, err := p.resolveUnderSourceRoot(rel)
		if err != nil {
			t.Fatalf("resolve %q: %v", rel, err)
		}
		if got != filepath.Clean(root) {
			t.Errorf("resolve %q = %q; want the root %q", rel, got, filepath.Clean(root))
		}
	}
}
