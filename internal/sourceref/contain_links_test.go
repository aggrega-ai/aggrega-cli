// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceref

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestAContentPathIsJudgedOnDiskAtEveryComponent drives the content anchor over
// one entity directory per row, each laid on disk with the declared path's parts
// either real or links, and reads back whether the path was anchored or refused.
//
// The rows that discriminate are the ones a last-part-only judgement passes: a
// directory on the way that is a link leading out of the entity's directory leaves
// the file named beneath it a regular file, so only a judgement of every component
// refuses it. A refused row asserts the condition, the link it names and the place
// that link leads, and that no path came back — so nothing a caller could open
// beyond the link is handed on.
func TestAContentPathIsJudgedOnDiskAtEveryComponent(t *testing.T) {
	rows := []struct {
		name     string
		lay      func(t *testing.T, entity, outside string)
		authored string
		// link is the link a refusal names, relative to the entity's directory;
		// empty where the path is anchored.
		link string
	}{
		{
			name: "control: a file in a directory inside the entity's own",
			lay: func(t *testing.T, entity, outside string) {
				writeContentFile(t, filepath.Join(entity, "frag", "fragment.yaml"))
			},
			authored: "./frag/fragment.yaml",
		},
		{
			name: "a directory on the way that is a link leading out of the entity's directory",
			lay: func(t *testing.T, entity, outside string) {
				writeContentFile(t, filepath.Join(outside, "frag", "fragment.yaml"))
				contentLink(t, filepath.Join(outside, "frag"), filepath.Join(entity, "frag"))
			},
			authored: "./frag/fragment.yaml",
			link:     "frag",
		},
		{
			name: "a source directory that is a link leading out of the entity's directory",
			lay: func(t *testing.T, entity, outside string) {
				writeContentFile(t, filepath.Join(outside, "chart", "Chart.yaml"))
				contentLink(t, filepath.Join(outside, "chart"), filepath.Join(entity, "chart"))
			},
			authored: "./chart",
			link:     "chart",
		},
		{
			name: "a file that is a link leading out of the entity's directory",
			lay: func(t *testing.T, entity, outside string) {
				writeContentFile(t, filepath.Join(outside, "fragment.yaml"))
				contentLink(t, filepath.Join(outside, "fragment.yaml"), filepath.Join(entity, "fragment.yaml"))
			},
			authored: "fragment.yaml",
			link:     "fragment.yaml",
		},
		{
			name: "a directory on the way that is a link leading inside the entity's directory",
			lay: func(t *testing.T, entity, outside string) {
				writeContentFile(t, filepath.Join(entity, "real", "fragment.yaml"))
				contentLink(t, filepath.Join(entity, "real"), filepath.Join(entity, "frag"))
			},
			authored: "./frag/fragment.yaml",
		},
		{
			name: "a path not on disk yet is judged by what of it exists",
			lay: func(t *testing.T, entity, outside string) {
				contentLink(t, outside, filepath.Join(entity, "later"))
			},
			authored: "./later/not-there-yet.yaml",
			link:     "later",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			entity := filepath.Join(t.TempDir(), "recipe", "app", "1.0.0")
			outside := t.TempDir()
			if err := os.MkdirAll(entity, 0o755); err != nil {
				t.Fatal(err)
			}
			row.lay(t, entity, outside)
			document := filepath.Join(entity, "recipe.yaml")
			got, err := AnchorContent(document, row.authored)
			if row.link == "" {
				if err != nil {
					t.Fatalf("refused a path the entity's directory holds: %v", err)
				}
				if want := filepath.Join(entity, filepath.FromSlash(row.authored)); got != filepath.Clean(want) {
					t.Errorf("anchored to %s, want %s", got, want)
				}
				return
			}
			if got != "" {
				t.Errorf("a refusal handed back a path %q; want none", got)
			}
			if !errors.Is(err, ErrContentOutsideEntityDirectory()) {
				t.Fatalf("want the content-outside-entity-directory halt, got %v", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0304" {
				t.Errorf("short code = %q, want E0304", code)
			}
			msg := err.Error()
			if wantLink := filepath.Join(entity, filepath.FromSlash(row.link)); !strings.Contains(msg, "the link "+wantLink+" leads to ") {
				t.Errorf("the halt must name the link %s:\n%s", wantLink, msg)
			}
			if !strings.Contains(msg, outside) {
				t.Errorf("the halt must name where the link leads, beneath %s:\n%s", outside, msg)
			}
			if !strings.Contains(msg, row.authored) {
				t.Errorf("the halt must name the path the author wrote, %q:\n%s", row.authored, msg)
			}
		})
	}

	// With no declaring document there is no directory for the content to sit in,
	// so only the lexical verdict applies — the negative control for the on-disk one.
	if _, err := AnchorContent("", "./frag/fragment.yaml"); err != nil {
		t.Errorf("a path with no declaring document was judged on disk: %v", err)
	}
}

func writeContentFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contentLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
