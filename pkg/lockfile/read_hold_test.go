// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestALockfileIsReadOnlyFromInsideTheRootItsWriteIsHeldTo pins the read of a
// Release's lockfile to the root a write of the same file is held to: the
// workspace root for a Release inside the workspace, and the Release's own
// directory for one outside it.
//
// Each row lays out one lockfile and reads it through both held entry points. A
// lockfile that is a link leading out of its root is refused by both with the
// read-leads-out-of-workspace halt naming the link, and nothing is read through it
// — LoadIfReadable included, which passes over an absent or unparsable lockfile
// but must never pass over a refused one. The controls are a plain lockfile and a
// link to a lockfile elsewhere inside the root, which are read.
func TestALockfileIsReadOnlyFromInsideTheRootItsWriteIsHeldTo(t *testing.T) {
	for _, row := range []struct {
		name string
		// arrange lays the lockfile out and returns the workspace root, the
		// lockfile's path beside its Release, and where a refused row's link leads.
		arrange func(t *testing.T, ws, outside, plain string) (root, path, leadsTo string)
		// absent rows hold no lockfile LoadIfReadable could read.
		absent  bool
		refused bool
	}{
		{name: "control: a lockfile beside a Release in the workspace",
			arrange: func(t *testing.T, ws, _, plain string) (string, string, string) {
				return ws, copyTo(t, plain, filepath.Join(ws, "release.lock")), ""
			}},
		{name: "a link to a lockfile elsewhere inside the workspace is read",
			arrange: func(t *testing.T, ws, _, plain string) (string, string, string) {
				kept := copyTo(t, plain, filepath.Join(ws, "kept", "release.lock"))
				return ws, linkTo(t, kept, filepath.Join(ws, "release.lock")), ""
			}},
		{name: "a link to a lockfile outside the workspace is refused", refused: true,
			arrange: func(t *testing.T, ws, outside, plain string) (string, string, string) {
				kept := copyTo(t, plain, filepath.Join(outside, "release.lock"))
				return ws, linkTo(t, kept, filepath.Join(ws, "release.lock")), kept
			}},
		{name: "a lockfile beside a Release outside the workspace is read beside it",
			arrange: func(t *testing.T, ws, outside, plain string) (string, string, string) {
				return ws, copyTo(t, plain, filepath.Join(outside, "typed", "release.lock")), ""
			}},
		{name: "a link leading out of the directory of a Release outside the workspace is refused", refused: true,
			arrange: func(t *testing.T, ws, outside, plain string) (string, string, string) {
				kept := copyTo(t, plain, filepath.Join(outside, "elsewhere", "release.lock"))
				if err := os.MkdirAll(filepath.Join(outside, "typed"), 0o755); err != nil {
					t.Fatal(err)
				}
				return ws, linkTo(t, kept, filepath.Join(outside, "typed", "release.lock")), kept
			}},
		{name: "control: no lockfile at all is absent, not refused", absent: true,
			arrange: func(t *testing.T, ws, _, _ string) (string, string, string) {
				return ws, filepath.Join(ws, "release.lock"), ""
			}},
	} {
		t.Run(row.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			plain := filepath.Join(t.TempDir(), "release.lock")
			if err := lockfile.WriteLockfile(plain, &lockfile.Lockfile{Version: 1, Release: "release.yaml",
				GeneratedAt: "2026-08-20T09:00:00Z"}); err != nil {
				t.Fatalf("write the lockfile fixture: %v", err)
			}
			root, path, leadsTo := row.arrange(t, ws, outside, plain)

			_, _, loadErr := lockfile.Load(root, path)
			got, ifErr := lockfile.LoadIfReadable(root, path)
			if row.refused {
				for which, err := range map[string]error{"Load": loadErr, "LoadIfReadable": ifErr} {
					if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
						t.Errorf("%s: want the read-leads-out-of-workspace halt, got %v", which, err)
						continue
					}
					if !strings.Contains(err.Error(), "the link "+path+" leads to "+leadsTo) {
						t.Errorf("%s: the halt does not name the link and where it leads: %v", which, err)
					}
				}
				if got != nil {
					t.Error("LoadIfReadable handed back a lockfile it refused")
				}
				return
			}
			if ifErr != nil {
				t.Fatalf("LoadIfReadable refused a lockfile its root holds: %v", ifErr)
			}
			if row.absent {
				if got != nil {
					t.Error("LoadIfReadable read a lockfile that is not there")
				}
				return
			}
			if loadErr != nil {
				t.Fatalf("Load refused a lockfile its root holds: %v", loadErr)
			}
			if got == nil || got.Release != "release.yaml" {
				t.Errorf("LoadIfReadable did not read the lockfile: %+v", got)
			}
		})
	}
}

func copyTo(t *testing.T, from, to string) string {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return to
}

func linkTo(t *testing.T, target, at string) string {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
	return at
}
