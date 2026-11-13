// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestASelectionThatIsItselfALinkIsRefusedInEveryShape drives the pack a guest asks
// the host for over a selection that is itself a symbolic link to a directory
// inside the source root — one the recorded rules leave out, one beneath a
// directory they leave out, and one they keep — in each of the three shapes, and
// reads back what was packed or why it was refused.
//
// A render's own listing never shows the guest such a link, because the walk of
// the source root refuses every link it meets and does not enter what the rules
// leave out; a guest naming a path of its own still can. A tree packed through
// a walk that did not judge its root answers such a selection with no file and no
// refusal; a directory listed through the link hands on what it points to under
// its name. Every shape refuses it as the file shape does, with the read-failure
// code and naming the selection as the guest spelled it.
func TestASelectionThatIsItselfALinkIsRefusedInEveryShape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "chart")
	writeSourceFile(t, filepath.Join(root, "real", "values.yaml"), "inside\n")
	sourceLink(t, "real", filepath.Join(root, "excluded"))
	sourceLink(t, "real", filepath.Join(root, "kept"))
	writeSourceFile(t, filepath.Join(root, "vendor", "README"), "vendored\n")
	sourceLink(t, filepath.Join("..", "real"), filepath.Join(root, "vendor", "dep"))
	rules := []types.IgnoreRule{
		{Pattern: "excluded", Within: "chart", From: sourcewalk.AuthorIgnoreFile},
		{Pattern: "vendor/", Within: "chart", From: sourcewalk.AuthorIgnoreFile},
	}
	p := &Plugin{}
	p.SetSource(root, sourcewalk.NewScope(base, rules))

	rows := []struct {
		name    string
		op, rel string
		refused bool
	}{
		{name: "control: a tree of a directory inside the root", op: "tree", rel: "real"},
		{name: "control: a directory inside the root", op: "dir", rel: "real"},
		{name: "a tree of a link the rules leave out", op: "tree", rel: "excluded", refused: true},
		{name: "a tree of a link beneath a directory the rules leave out", op: "tree", rel: "vendor/dep", refused: true},
		{name: "a tree of a link the rules keep", op: "tree", rel: "kept", refused: true},
		{name: "a directory that is a link the rules leave out", op: "dir", rel: "excluded", refused: true},
		{name: "a directory that is a link the rules keep", op: "dir", rel: "kept", refused: true},
		{name: "a file selection of the same link", op: "file", rel: "kept", refused: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			proj, code, err := p.packSourceSelection(row.op, row.rel)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a selection the source root holds: %v (code %q)", err, code)
				}
				if len(proj.Files) != 1 || string(proj.Files[0].Content) != "inside\n" {
					t.Fatalf("packed %+v, want the one file inside", proj.Files)
				}
				return
			}
			if err == nil {
				var paths []string
				if proj != nil {
					for _, f := range proj.Files {
						paths = append(paths, f.Path)
					}
				}
				t.Fatalf("packed %q with no refusal; want the link refused", paths)
			}
			if proj != nil {
				t.Errorf("a refusal packed %+v; want nothing", proj.Files)
			}
			if code != abi.CodeSourceReadFailed {
				t.Errorf("code = %q, want %q", code, abi.CodeSourceReadFailed)
			}
			if !strings.Contains(err.Error(), "symbolic link") || !strings.Contains(err.Error(), "("+row.rel+")") {
				t.Errorf("the refusal must be the link refusal naming %q: %v", row.rel, err)
			}
		})
	}
}
