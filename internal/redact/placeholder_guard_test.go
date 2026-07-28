// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package redact

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// modulePath is the primitive's home relative to the module root. The guard
// asserts an EXACT match against this one entry rather than "at least this
// one": a subset check would pass while a fourth surface quietly spelled its
// own copy, which is the whole failure the guard exists to catch.
const primitiveFile = "internal/redact/redact.go"

// composedPlaceholder rebuilds the literal from fragments so this file can
// search for it without itself becoming a second spelling. The seam is
// deliberate; concatenating here is what keeps the exact-match assertion below
// honest.
func composedPlaceholder() string { return "<" + "redacted" + ">" }

// TestPlaceholderLiteralHasOneHome is the chokepoint guard for the redaction
// replacement. It walks every Go file in the module and requires the
// placeholder literal to appear in exactly one file, exactly once.
//
// The check is exact on both axes. An extra file means a surface spelled its
// own placeholder instead of calling Placeholder — the drift this package
// exists to prevent. A missing or renamed primitive file means the single home
// moved without the guard following it, which is equally a failure: a guard
// that silently stops guarding is worse than no guard.
//
// Scope is Go source. Golden files and testdata legitimately CONTAIN the
// rendered placeholder as expected output; they are not a second spelling of
// the rule.
func TestPlaceholderLiteralHasOneHome(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q does not carry a go.mod; the guard is looking in the wrong place: %v", root, err)
	}

	needle := composedPlaceholder()
	counts := map[string]int{}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		n := strings.Count(string(body), needle)
		if n == 0 {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		counts[filepath.ToSlash(rel)] = n
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	// This guard file rebuilds the literal at run time rather than spelling it,
	// so it must not appear in the census. If it does, the composition seam was
	// flattened and the guard has started guarding itself.
	if n, self := counts["internal/redact/placeholder_guard_test.go"]; self {
		t.Errorf("the guard file spells the placeholder literally (%d occurrence(s)); it must compose it instead", n)
	}

	got := make([]string, 0, len(counts))
	for file := range counts {
		got = append(got, file)
	}
	sort.Strings(got)

	want := []string{primitiveFile}
	if len(got) != len(want) || (len(got) == 1 && got[0] != want[0]) {
		t.Fatalf("the placeholder literal must live in exactly one file.\n  want: %v\n  got:  %v\nEvery surface reaches it through redact.Placeholder().", want, got)
	}
	if n := counts[primitiveFile]; n != 1 {
		t.Errorf("%s must spell the placeholder exactly once; got %d occurrences", primitiveFile, n)
	}
}
