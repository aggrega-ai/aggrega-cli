// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"os"
	"path/filepath"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
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
	proj, _, err := p.packSourceSelection("file", "sub/b.txt")
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
	proj, _, err := p.packSourceSelection("dir", "sub")
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
	proj, _, err := p.packSourceSelection("tree", "")
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
	_, code, err := p.packSourceSelection("bogus", "")
	if err == nil {
		t.Error("unknown op should halt")
	}
	if code != abi.CodeUnknownSelectionOp {
		t.Errorf("unknown op code = %q; want %q", code, abi.CodeUnknownSelectionOp)
	}
}

// TestPackSourceSelection_SandboxRejectsEscape pins the sandbox: a selection path
// escaping above the source root is refused, so a guest cannot read outside the
// acquired source.
func TestPackSourceSelection_SandboxRejectsEscape(t *testing.T) {
	t.Parallel()
	p := &Plugin{sourceRoot: writePackFixture(t)}
	for _, rel := range []string{"../outside.txt", "sub/../../outside.txt", "../../etc/passwd"} {
		_, code, err := p.packSourceSelection("file", rel)
		if err == nil {
			t.Errorf("selection %q escaping the source root should be refused", rel)
		}
		if code != abi.CodeSourceEscape {
			t.Errorf("selection %q escape code = %q; want %q", rel, code, abi.CodeSourceEscape)
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
		got, _, err := p.resolveUnderSourceRoot(rel)
		if err != nil {
			t.Fatalf("resolve %q: %v", rel, err)
		}
		if got != filepath.Clean(root) {
			t.Errorf("resolve %q = %q; want the root %q", rel, got, filepath.Clean(root))
		}
	}
}

// TestResolveUnderSourceRoot_RefusesClimbBackToTheRoot pins a tightening that
// came with routing the sandbox through the shared containment predicate.
//
// The previous check compared the JOINED path against the root, so a selection
// that climbed above the root and descended back into a directory of the same
// name — "../<root-leaf>/x" — landed inside the root and was accepted. A guest
// has no business naming its own root's parent, and accepting it meant the
// sandbox's answer depended on what the root was called. Containment is now
// judged on the selection itself, so any climb above the root is refused
// whatever it descends into afterwards.
func TestResolveUnderSourceRoot_RefusesClimbBackToTheRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := &Plugin{sourceRoot: root}

	climbBack := "../" + filepath.Base(root) + "/inside.txt"
	if _, code, err := p.resolveUnderSourceRoot(climbBack); err == nil {
		t.Errorf("selection %q was accepted; a climb above the root is refused however it descends back", climbBack)
	} else if code != abi.CodeSourceEscape {
		t.Errorf("selection %q escape code = %q; want %q", climbBack, code, abi.CodeSourceEscape)
	}

	// The interior climb that resolves back inside is still accepted, so the
	// tightening above is about leaving the root rather than about "..".
	got, _, err := p.resolveUnderSourceRoot("sub/../inside.txt")
	if err != nil {
		t.Fatalf("interior climb refused: %v", err)
	}
	if want := filepath.Join(filepath.Clean(root), "inside.txt"); got != want {
		t.Errorf("interior climb resolved to %q; want %q", got, want)
	}
}

// TestResolveUnderSourceRoot_AcceptsLeadingDoubleDotName pins that a directory
// inside the acquired source whose name begins with two dots is readable. The
// containment answer turns on where the selection lands, not on how its first
// two characters look.
func TestResolveUnderSourceRoot_AcceptsLeadingDoubleDotName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := &Plugin{sourceRoot: root}
	got, code, err := p.resolveUnderSourceRoot("..shared/values.yaml")
	if err != nil {
		t.Fatalf("resolve: %v (code %q)", err, code)
	}
	if want := filepath.Join(filepath.Clean(root), "..shared", "values.yaml"); got != want {
		t.Errorf("resolved to %q; want %q", got, want)
	}
}

// TestResolveUnderSourceRoot_RefusesAbsoluteSelection pins that a selection
// naming an absolute path is refused rather than re-rooted. Joining an absolute
// selection onto the root yields a path under the root, so the previous check
// accepted it and the guest silently received a different file from the one it
// named.
func TestResolveUnderSourceRoot_RefusesAbsoluteSelection(t *testing.T) {
	t.Parallel()
	p := &Plugin{sourceRoot: t.TempDir()}
	for _, rel := range []string{"/etc/passwd", "/"} {
		if _, code, err := p.resolveUnderSourceRoot(rel); err == nil {
			t.Errorf("absolute selection %q was accepted", rel)
		} else if code != abi.CodeSourceEscape {
			t.Errorf("absolute selection %q code = %q; want %q", rel, code, abi.CodeSourceEscape)
		}
	}
}
