// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGo puts a source file under a fresh temporary directory and returns its
// path, so each case parses a real file through the same entry point the command
// uses rather than a string the test hands straight to the parser.
func writeGo(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestCommentHitIsFoundAndReportsItsCodepoint(t *testing.T) {
	path := writeGo(t, "a.go", "package a\n\n// Thing does a thing. \U0001F534 It matters.\nfunc Thing() {}\n")
	hits, err := scan(path, -1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one hit, got %d: %v", len(hits), hits)
	}
	if !strings.Contains(hits[0], "U+1F534") {
		t.Errorf("hit does not name the codepoint: %s", hits[0])
	}
	if !strings.Contains(hits[0], ":3:") {
		t.Errorf("hit does not carry the comment's line: %s", hits[0])
	}
}

// The structural claim the whole tool rests on: a pictograph in a string is not
// a comment, and no exception list is what keeps it out.
func TestStringLiteralIsOutOfReach(t *testing.T) {
	body := "package a\n\n" +
		"// Thing marks a finished step.\n" +
		"const markDone = \"[✓] \"\n\n" +
		"var raw = `a raw literal with \U0001F389 in it`\n\n" +
		"var withComment = \"a string holding // and \U0001F534 on one line\"\n"
	path := writeGo(t, "b.go", body)
	hits, err := scan(path, -1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("string literals must yield no hits, got: %v", hits)
	}
}

// A block comment's continuation lines open with an asterisk, which is the case
// a line-anchored text pattern misses.
func TestBlockCommentContinuationLineIsReached(t *testing.T) {
	body := "package a\n\n/*\n * A block comment.\n * \U0001F534 On a continuation line.\n */\nfunc Thing() {}\n"
	path := writeGo(t, "c.go", body)
	hits, err := scan(path, -1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want one hit from the continuation line, got %d: %v", len(hits), hits)
	}
	if !strings.Contains(hits[0], ":5:") {
		t.Errorf("hit does not carry the continuation line's number: %s", hits[0])
	}
}

func TestTypographyAndNotationAreLeftAlone(t *testing.T) {
	body := "package a\n\n" +
		"// An em dash — an arrow → a section § an ellipsis …\n" +
		"// plus-minus ± times × at least ≥ and a box edge ─│┌\n" +
		"// a block element █ a geometric shape ■ a technical symbol ⌘\n" +
		"// and the legal marks © ® ™\n" +
		"func Thing() {}\n"
	path := writeGo(t, "d.go", body)
	hits, err := scan(path, -1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("typography and notation must yield no hits, got: %v", hits)
	}
}

// An invisible member of the class is exactly the one a reader cannot spot, so
// it is the one the report has to name in words.
func TestInvisibleMembersAreFoundAndNamed(t *testing.T) {
	path := writeGo(t, "e.go", "package a\n\n// A marker whose glyph was deleted ️ leaving the selector behind.\nfunc Thing() {}\n")
	hits, err := scan(path, -1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want one hit for the orphaned selector, got %d: %v", len(hits), hits)
	}
	if !strings.Contains(hits[0], "U+FE0F") || !strings.Contains(hits[0], "invisible") {
		t.Errorf("hit does not name the invisible codepoint: %s", hits[0])
	}
}

// The paired form is two codepoints and must be reported as two, so a census
// written for one form cannot silently under-count the other.
func TestPairedFormReportsBothCodepoints(t *testing.T) {
	path := writeGo(t, "f.go", "package a\n\n// A warning ⚠️ with its selector.\nfunc Thing() {}\n")
	hits, err := scan(path, -1)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want both codepoints reported, got %d: %v", len(hits), hits)
	}
	joined := strings.Join(hits, "\n")
	if !strings.Contains(joined, "U+26A0") || !strings.Contains(joined, "U+FE0F") {
		t.Errorf("both codepoints must be named: %s", joined)
	}
}

func TestOnlyRestrictsToOneCodepoint(t *testing.T) {
	path := writeGo(t, "g.go", "package a\n\n// A warning ⚠ and a circle \U0001F534.\nfunc Thing() {}\n")
	hits, err := scan(path, 0x26A0)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0], "U+26A0") {
		t.Fatalf("want only the requested codepoint, got: %v", hits)
	}
}

func TestCorpusPredicate(t *testing.T) {
	in := []string{
		"pkg/errors/errors.go",
		"cmd/aggrega/build_progress.go",
		"./pkg/types/plugin.go",
		"aggrega-cli/pkg/resolver/objection_findings.go",
		"pkg/render/scripts/emit.go",
		"pkg/build/tools/helper.go",
	}
	out := []string{
		"pkg/errors/errors_test.go",
		"internal/wasm/loader.go",
		"aggrega-cli/internal/buildtools/pictographcensus/main.go",
		"pkg/errors/testdata/legitimate/is.go",
		"vendor/example.com/dep/dep.go",
		"tools/cmd/records/main.go",
		"./tools/internal/cli/cli.go",
		"scripts/prose-only-compare.go",
		"./scripts/dev-registry/htpasswdgen/main.go",
		"docs/architecture/overview.md",
		"pkg/errors/errors.rs",
	}
	for _, p := range in {
		if !inCorpus(p) {
			t.Errorf("%s must be in the corpus", p)
		}
	}
	for _, p := range out {
		if inCorpus(p) {
			t.Errorf("%s must be outside the corpus", p)
		}
	}
}

// collect is what a caller handing over a whole change set relies on: the paths
// the corpus rejects are dropped rather than refused, and the ones it keeps come
// back in a stable order.
func TestCollectNarrowsANamedChangeSet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "a_test.go", "b.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package a\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	files, err := collect([]string{
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "a_test.go"),
		filepath.Join(dir, "b.md"),
	}, false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], "a.go") {
		t.Fatalf("want only the published Go file, got: %v", files)
	}
}

func TestCollectRefusesAPathThatDoesNotExist(t *testing.T) {
	if _, err := collect([]string{filepath.Join(t.TempDir(), "absent.go")}, false); err == nil {
		t.Fatal("naming a path that does not exist must be an error, not an empty clean run")
	}
}

// A file that does not parse still yields the comments the parser recovered,
// and never turns a syntax error into this check's refusal.
func TestUnparseableFileStillYieldsRecoveredComments(t *testing.T) {
	path := writeGo(t, "h.go", "package a\n\n// A marker \U0001F534 here.\nfunc Thing( {}\n")
	hits, err := scan(path, -1)
	if err != nil {
		t.Fatalf("a syntax error must not become this check's error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want the recovered comment's hit, got %d: %v", len(hits), hits)
	}
}

func TestClassBoundaries(t *testing.T) {
	for _, r := range []rune{0x1F000, 0x1FAFF, 0x2600, 0x27BF, 0x2B00, 0x2BFF, 0xFE0F, 0x200D, 0x26A0, 0x1F534} {
		if !isPictograph(r) {
			t.Errorf("U+%04X must be in the class", r)
		}
	}
	for _, r := range []rune{'a', 0x2014, 0x2192, 0x00A7, 0x2026, 0x00B1, 0x00D7, 0x2265, 0x2500, 0x257F,
		0x2580, 0x25FF, 0x2300, 0x23FF, 0x00A9, 0x00AE, 0x2122, 0x21FF, 0x2200, 0x25FF, 0x2AFF, 0x1EFFF} {
		if isPictograph(r) {
			t.Errorf("U+%04X must be outside the class", r)
		}
	}
}
