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

// composedWithheldLead rebuilds the withholding statement's opening from
// fragments, for the same reason and with the same seam. The lead is enough to
// find a second spelling: a surface writing its own statement has to open it.
func composedWithheldLead() string { return "<" + "withheld: " }

// composedSubstitutedLead rebuilds the re-render announcement's opening the same
// way. This one matters most of the three: it is the only text that tells a reader
// the document they are looking at is not the one the build emitted, and a second
// spelling would let one surface announce a weaker guarantee than another.
func composedSubstitutedLead() string { return "<" + "re-rendered: " }

// composedMarkerKey rebuilds the structured withheld marker's key from fragments,
// with the same seam and for the same reason.
func composedMarkerKey() string { return "aggrega" + "_withheld" }

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
	assertLiteralHasOneHome(t, composedPlaceholder(),
		"the placeholder literal", "redact.Placeholder()")
}

// TestWithheldProjectionLiteralHasOneHome is the same chokepoint guard for the
// whole-projection withholding statement, and it exists for a sharper reason than
// consistency. A reader of a shortened display has exactly one question — was this
// withheld, or is there simply nothing here — and a second spelling of the
// statement makes that question unanswerable, because two texts that look alike
// cannot be told apart from one that means something else.
func TestWithheldProjectionLiteralHasOneHome(t *testing.T) {
	t.Parallel()
	assertLiteralHasOneHome(t, composedWithheldLead(),
		"the withholding statement", "redact.WithheldProjection()")
}

// TestSubstitutedProjectionLiteralHasOneHome is the same chokepoint guard for the
// re-render announcement, and it carries the sharpest version of the reason. A
// display that shows a re-rendered document without saying so tells the reader
// something false; two spellings of the saying-so would let one surface's version
// drift into promising more than the substitution delivers.
func TestSubstitutedProjectionLiteralHasOneHome(t *testing.T) {
	t.Parallel()
	assertLiteralHasOneHome(t, composedSubstitutedLead(),
		"the re-render announcement", "redact.SubstitutedProjection()")
}

// TestWithheldMarkerKeyHasOneHome is the same chokepoint guard for the structured
// withheld marker's key, and it guards one more boundary than the three above: the
// key is the join between what this module emits into a document and what a reader
// of that document recognises as a withholding. A surface spelling its own would
// emit an object no reader recognises, which reads as an authored value rather
// than as a refusal — the one failure in this axis that looks like data.
func TestWithheldMarkerKeyHasOneHome(t *testing.T) {
	t.Parallel()
	assertLiteralHasOneHome(t, composedMarkerKey(),
		"the withheld marker key", "redact.WithheldMarkerKey()")
}

// assertLiteralHasOneHome is the ONE census walk both guards run. A second copy of
// the walk would be the very drift the guards exist to catch, one level up.
//
// The check is exact on both axes. An extra file means a surface spelled its own
// copy instead of calling the accessor — the drift this package exists to prevent.
// A missing or renamed primitive file means the single home moved without the
// guard following it, which is equally a failure: a guard that silently stops
// guarding is worse than no guard.
//
// Scope is Go source. Golden files and testdata legitimately CONTAIN the rendered
// text as expected output; they are not a second spelling of the rule.
func assertLiteralHasOneHome(t *testing.T, needle, subject, accessor string) {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q does not carry a go.mod; the guard is looking in the wrong place: %v", root, err)
	}

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

	// This guard file rebuilds each literal at run time rather than spelling it,
	// so it must not appear in the census. If it does, the composition seam was
	// flattened and the guard has started guarding itself.
	if n, self := counts["internal/redact/placeholder_guard_test.go"]; self {
		t.Errorf("the guard file spells %s literally (%d occurrence(s)); it must compose it instead", subject, n)
	}

	got := make([]string, 0, len(counts))
	for file := range counts {
		got = append(got, file)
	}
	sort.Strings(got)

	want := []string{primitiveFile}
	if len(got) != len(want) || (len(got) == 1 && got[0] != want[0]) {
		t.Fatalf("%s must live in exactly one file.\n  want: %v\n  got:  %v\nEvery surface reaches it through %s.", subject, want, got, accessor)
	}
	if n := counts[primitiveFile]; n != 1 {
		t.Errorf("%s must spell %s exactly once; got %d occurrences", primitiveFile, subject, n)
	}
}
