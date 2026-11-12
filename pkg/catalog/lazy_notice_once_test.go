// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestANoticeAboutAFileIsPrintedOnceHoweverOftenItsWalkRuns holds the lazy view's
// notices about the catalog files it did not place — shadowed by an earlier
// catalog directory, or refused — to one line each for the life of the view.
//
// A pull and a build change the view's pins several times, and each change drops
// the recipe-shell enumeration, so the walk behind it runs again and meets every
// such file again. The file is no more shadowed the second time than the first,
// so the reader is told once. The drive below is that shape: three pin changes,
// each followed by the reads that re-run the walks.
//
// Reset is the other side of the contract: it is the workspace-invalidation
// operation, after which the files may differ, so the view announces what its
// next walk finds afresh — exactly once more, never zero times.
//
// Every row also asserts the line was printed at all, so a fix that silenced the
// walk would fail here rather than pass as "no repetition".
func TestANoticeAboutAFileIsPrintedOnceHoweverOftenItsWalkRuns(t *testing.T) {
	cases := []struct {
		name  string
		dirs  []string          // catalog directories, in precedence order, relative to the workspace
		files map[string]string // workspace-relative path -> document
		// subject is the workspace-relative file the notice names; marker is text the
		// notice line carries beside that path, so the line counted is the notice and
		// not some other line naming the file.
		subject string
		marker  string
	}{
		{
			name: "a recipe an earlier catalog directory shadows",
			dirs: []string{"a", "b"},
			files: map[string]string{
				"a/recipe.yaml": recipeYAML("svc", "test/svc"),
				"b/recipe.yaml": recipeYAML("svc", "test/svc"),
			},
			subject: "b/recipe.yaml",
			marker:  "this file is shadowed",
		},
		{
			name: "two files of one directory declaring one recipe identity",
			dirs: []string{"a"},
			files: map[string]string{
				"a/a.yaml": recipeYAML("svc", "test/svc"),
				"a/b.yaml": recipeYAML("svc", "test/svc"),
			},
			subject: "a/b.yaml",
			marker:  "duplicate recipe identity",
		},
		{
			// A helm Recipe with no output-bearing block is refused by envelope
			// validation, which is a parse refusal rather than a duplicate.
			name: "a recipe the walk refuses to parse",
			dirs: []string{"a"},
			files: map[string]string{
				"a/refused.yaml": "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: refused\n  version: 1.0.0\nspec:\n  capability: helm\n  implements: core/refused\n",
			},
			subject: "a/refused.yaml",
			marker:  "skipped:",
		},
		{
			// The Component index is not dropped by a pin change, so this row holds the
			// same contract for the other notice the view prints.
			name: "a Component an earlier catalog directory shadows",
			dirs: []string{"a", "b"},
			files: map[string]string{
				"a/component.yaml": baseComponentDoc,
				"b/component.yaml": baseComponentDoc,
			},
			subject: "b/component.yaml",
			marker:  "is shadowed (first-win precedence)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			for rel, body := range tc.files {
				path := filepath.Join(ws, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("write %s: %v", rel, err)
				}
			}
			dirs := make([]string, 0, len(tc.dirs))
			for _, d := range tc.dirs {
				dirs = append(dirs, filepath.Join(ws, d))
			}
			subject := filepath.Join(ws, tc.subject)
			view := NewLazyLookup(ws, ".aggrega/cache", dirs, nil)

			// The reads that walk: Skips re-runs the recipe-shell enumeration, and a
			// Component read builds that index on first need.
			walk := func() {
				_ = view.Skips()
				_, _ = view.Component("test/base:1.0.0")
			}
			out := captureNotices(t, view, func() {
				for i := 0; i < 3; i++ {
					view.SetPins(Pins{})
					walk()
				}
			})
			if got := noticeLines(out, subject, tc.marker); got != 1 {
				t.Errorf("across three pin changes the notice naming %s was printed %d times; want exactly once\nstderr:\n%s", tc.subject, got, out)
			}

			again := captureNotices(t, view, func() {
				view.Reset()
				walk()
			})
			if got := noticeLines(again, subject, tc.marker); got != 1 {
				t.Errorf("after Reset the notice naming %s was printed %d times; want exactly once — a reset view announces what its next walk finds\nstderr:\n%s", tc.subject, got, again)
			}
		})
	}
}

// noticeLines counts the lines of out that name path and carry marker.
func noticeLines(out, path, marker string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, path) && strings.Contains(line, marker) {
			n++
		}
	}
	return n
}
