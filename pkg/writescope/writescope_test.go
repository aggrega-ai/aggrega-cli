// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package writescope_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestCheckWriteAndDeleteHoldAPathToTheWorkspace drives both judgements over one
// workspace, one fault per row beside a control that is plainly inside.
//
// A refused row is asserted against the condition itself, its short code, and the
// two places its message must name — the link a reader removes and where it leads
// — because a refusal that only said "outside" would leave a reader holding a path
// that, as spelled, looks inside the workspace.
//
// Two rows separate the judgements, and they are the reason there are two: a link
// leading nowhere at the last part of the path is passed over by the definition,
// so removing it removes only the link, while creating a file through it creates
// the file the link points at.
func TestCheckWriteAndDeleteHoldAPathToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustMkdir(t, filepath.Join(root, "real"))
	mustLink(t, outside, filepath.Join(root, "out-link"))
	mustLink(t, filepath.Join(outside, "missing.lock"), filepath.Join(root, "dangling-out.lock"))
	mustLink(t, filepath.Join(root, "real", "missing.lock"), filepath.Join(root, "dangling-in.lock"))

	type check func(root, path string) error
	write, remove := check(writescope.CheckWrite), check(writescope.CheckDelete)
	rows := []struct {
		name    string
		check   check
		root    string
		path    string
		refused bool
		link    string
	}{
		{name: "control: a new file inside", check: write, root: root, path: filepath.Join(root, "real", "new.yaml")},
		{name: "control: removing a directory inside", check: remove, root: root, path: filepath.Join(root, "real")},
		{name: "writing beneath a link leading out", check: write, root: root, path: filepath.Join(root, "out-link", "x.yaml"), refused: true, link: filepath.Join(root, "out-link")},
		{name: "removing beneath a link leading out", check: remove, root: root, path: filepath.Join(root, "out-link", "boundaries"), refused: true, link: filepath.Join(root, "out-link")},
		{name: "writing through a last link leading nowhere outside", check: write, root: root, path: filepath.Join(root, "dangling-out.lock"), refused: true, link: filepath.Join(root, "dangling-out.lock")},
		{name: "removing a last link leading nowhere removes the link itself", check: remove, root: root, path: filepath.Join(root, "dangling-out.lock")},
		{name: "writing through a last link leading nowhere inside", check: write, root: root, path: filepath.Join(root, "dangling-in.lock")},
		{name: "a path spelled outside the workspace is not this rule's", check: write, root: root, path: filepath.Join(outside, "x.yaml")},
		{name: "no workspace root holds nothing", check: write, root: "", path: filepath.Join(root, "out-link", "x.yaml")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := row.check(row.root, row.path)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a path the workspace holds: %v", err)
				}
				return
			}
			if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
				t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0320" {
				t.Errorf("short code = %q, want E0320", code)
			}
			if !strings.Contains(err.Error(), row.link) {
				t.Errorf("the refusal must name the link %s:\n%v", row.link, err)
			}
			if !strings.Contains(err.Error(), "leads to "+filepath.Clean(outside)) && !strings.Contains(err.Error(), "leads to "+resolved(t, outside)) {
				t.Errorf("the refusal must name where the link leads (%s):\n%v", outside, err)
			}
		})
	}
}

func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return r
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
