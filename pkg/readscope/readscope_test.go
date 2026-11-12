// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package readscope_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestCheckReadHoldsAReadToItsRoot drives the judgement over one root, one fault
// per row beside controls that are plainly inside.
//
// A refused row is asserted against the condition itself, its short code, and the
// two places its message must name — the link a reader removes and where it leads
// — because a refusal that only said "outside" would leave a reader holding a path
// that, as spelled, looks inside the root.
func TestCheckReadHoldsAReadToItsRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, "real", "entity.yaml"))
	mustWrite(t, filepath.Join(outside, "entity.yaml"))
	mustLink(t, filepath.Join(root, "real", "entity.yaml"), filepath.Join(root, "inside-link.yaml"))
	mustLink(t, filepath.Join(outside, "entity.yaml"), filepath.Join(root, "file-out.yaml"))
	mustLink(t, outside, filepath.Join(root, "dir-out"))
	mustLink(t, filepath.Join(outside, "missing.yaml"), filepath.Join(root, "dangling.yaml"))

	rows := []struct {
		name    string
		root    string
		path    string
		refused bool
		link    string
	}{
		{name: "control: a file inside", root: root, path: filepath.Join(root, "real", "entity.yaml")},
		{name: "control: a link leading to a file inside", root: root, path: filepath.Join(root, "inside-link.yaml")},
		{name: "a file that is a link leading out", root: root, path: filepath.Join(root, "file-out.yaml"), refused: true, link: filepath.Join(root, "file-out.yaml")},
		{name: "a file beneath a directory that is a link leading out", root: root, path: filepath.Join(root, "dir-out", "entity.yaml"), refused: true, link: filepath.Join(root, "dir-out")},
		{name: "a directory that is a link leading out", root: root, path: filepath.Join(root, "dir-out"), refused: true, link: filepath.Join(root, "dir-out")},
		{name: "a link leading nowhere carries nothing to read", root: root, path: filepath.Join(root, "dangling.yaml")},
		{name: "a path spelled outside the root is not this root's", root: root, path: filepath.Join(outside, "entity.yaml")},
		{name: "no root holds nothing", root: "", path: filepath.Join(root, "file-out.yaml")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := readscope.CheckRead(row.root, row.path)
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a read the root holds: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
				t.Errorf("short code = %q, want E0321", code)
			}
			if !strings.Contains(err.Error(), "the link "+row.link+" leads to ") {
				t.Errorf("the refusal must name the link %s:\n%v", row.link, err)
			}
			if !strings.Contains(err.Error(), resolved(t, outside)) {
				t.Errorf("the refusal must name where the link leads (%s):\n%v", outside, err)
			}
			if !strings.Contains(err.Error(), "which must stay inside "+filepath.Clean(row.root)) {
				t.Errorf("the refusal must name the root the read must stay inside (%s):\n%v", row.root, err)
			}
			// The remedy names the directory to list as the one holding what was kept
			// outside, and says a link beneath a listed directory does not reach it: a
			// reader who already listed the directory the link sits in must not be told
			// to list it.
			if !strings.Contains(err.Error(), "list the directory holding it as a catalog directory") ||
				!strings.Contains(err.Error(), "rather than reaching it through a link beneath one") {
				t.Errorf("the remedy must point past the directory the link sits in, to the one holding what it leads to:\n%v", err)
			}
		})
	}
}

// TestCheckBesideEntityReadIsTheOneJudgementWithItsOwnRemedy holds the entry for a
// file read beside an entity's document to the judgement every other entry makes —
// the same verdict and condition over the same layouts — while its refusal names
// the remedy such a file has rather than the catalog directory a listing reaches,
// which changes nothing for a file beside an entity.
func TestCheckBesideEntityReadIsTheOneJudgementWithItsOwnRemedy(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(root, "e", "real.yaml"))
	mustWrite(t, filepath.Join(outside, "neighbour.yaml"))
	mustLink(t, filepath.Join(root, "e", "real.yaml"), filepath.Join(root, "e", "inside.yaml"))
	mustLink(t, filepath.Join(outside, "neighbour.yaml"), filepath.Join(root, "e", "out.yaml"))
	for _, row := range []struct {
		name    string
		path    string
		refused bool
	}{
		{name: "control: a file beside the entity", path: filepath.Join(root, "e", "real.yaml")},
		{name: "control: a link to a file inside the root", path: filepath.Join(root, "e", "inside.yaml")},
		{name: "a link leading out of the root", path: filepath.Join(root, "e", "out.yaml"), refused: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			err := readscope.CheckBesideEntityRead(root, row.path)
			if general := readscope.CheckRead(root, row.path); (general == nil) != (err == nil) {
				t.Fatalf("the verdict must be CheckRead's; CheckRead %v, CheckBesideEntityRead %v", general, err)
			}
			if !row.refused {
				if err != nil {
					t.Fatalf("refused a read the root holds: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
				t.Errorf("short code = %q, want E0321", code)
			}
			for _, want := range []string{
				"the link " + row.path + " leads to " + resolved(t, filepath.Join(outside, "neighbour.yaml")),
				"which must stay inside " + root,
				"A file beside an entity — another document in its directory, or the built .wasm beside a Plugin entity",
				"keep the file itself there rather than a link to it",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), "as a catalog directory") {
				t.Errorf("a listed catalog directory reaches nothing beside an entity, so the remedy must not offer one:\n%v", err)
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

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("kind: Component\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
