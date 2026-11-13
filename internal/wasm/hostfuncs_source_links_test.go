// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// TestASourceSelectionLeadingOutThroughALinkIsRefused drives every shape of
// selection a plugin may ask the host to pack, over a source root holding a
// directory per row, and reads back what was packed or why it was refused.
//
// The rows that discriminate are the ones a last-part-only judgement passes: the
// file a selection names beneath a directory that is a link leading out of the
// source root is a regular file, so only a judgement of every component of the
// selection refuses it. A refused row asserts the sandbox-escape code a guest
// branches on, the link the refusal names, and that nothing was packed — no byte
// from beyond the link reaches the guest.
func TestASourceSelectionLeadingOutThroughALinkIsRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeSourceFile(t, filepath.Join(root, "real", "values.yaml"), "inside\n")
	writeSourceFile(t, filepath.Join(outside, "values.yaml"), "outside\n")
	sourceLink(t, outside, filepath.Join(root, "out"))
	sourceLink(t, filepath.Join(root, "real"), filepath.Join(root, "alias"))
	p := &Plugin{sourceRoot: root}

	rows := []struct {
		name    string
		op      string
		rel     string
		refused bool
		want    string
	}{
		{name: "control: a file in a directory inside the root", op: "file", rel: "real/values.yaml", want: "inside\n"},
		{name: "control: a directory inside the root", op: "dir", rel: "real", want: "inside\n"},
		{name: "control: a tree inside the root", op: "tree", rel: "real", want: "inside\n"},
		{name: "a file beneath a directory that is a link leading out", op: "file", rel: "out/values.yaml", refused: true},
		{name: "a directory that is a link leading out", op: "dir", rel: "out", refused: true},
		{name: "a tree rooted at a link leading out", op: "tree", rel: "out", refused: true},
		{name: "a file beneath a directory that is a link leading inside the root", op: "file", rel: "alias/values.yaml", want: "inside\n"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			proj, code, err := p.packSourceSelection(row.op, row.rel)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a selection the source root holds: %v (code %q)", err, code)
				}
				if len(proj.Files) != 1 || string(proj.Files[0].Content) != row.want {
					t.Fatalf("packed %+v, want one file holding %q", proj.Files, row.want)
				}
				return
			}
			if proj != nil {
				t.Errorf("a refusal packed %+v; want nothing", proj.Files)
			}
			if code != abi.CodeSourceEscape {
				t.Errorf("code = %q, want %q", code, abi.CodeSourceEscape)
			}
			if err == nil || !strings.Contains(err.Error(), "the link "+filepath.Join(root, "out")+" leads to ") {
				t.Errorf("the refusal must name the link %s: %v", filepath.Join(root, "out"), err)
			}
		})
	}
}

func writeSourceFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sourceLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
