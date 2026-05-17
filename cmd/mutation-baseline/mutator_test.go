// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFindMutations_AllBinaryOperators pins detection for every
// binary-operator-flip operator: ==, !=, >, <, >=, <=, &&, ||.
func TestFindMutations_AllBinaryOperators(t *testing.T) {
	t.Parallel()
	src := []byte(`package p

func f(a, b int, x, y bool) {
	_ = a == b
	_ = a != b
	_ = a > b
	_ = a < b
	_ = a >= b
	_ = a <= b
	_ = x && y
	_ = x || y
}
`)
	muts, err := FindMutations("test.go", src)
	if err != nil {
		t.Fatalf("FindMutations: %v", err)
	}
	wantOperators := []string{
		"equality_to_inequality",
		"inequality_to_equality",
		"greater_to_greater_or_equal",
		"less_to_less_or_equal",
		"greater_or_equal_to_greater",
		"less_or_equal_to_less",
		"logical_and_to_or",
		"logical_or_to_and",
	}
	if len(muts) != len(wantOperators) {
		t.Fatalf("got %d mutations; want %d (%v)", len(muts), len(wantOperators), opNames(muts))
	}
	for i, want := range wantOperators {
		if muts[i].Operator != want {
			t.Errorf("mutation %d operator = %q; want %q", i, muts[i].Operator, want)
		}
	}
}

// TestFindMutations_ReturnBoolFlips pins detection for the two
// return-bool-flip operators.
func TestFindMutations_ReturnBoolFlips(t *testing.T) {
	t.Parallel()
	src := []byte(`package p

func True() bool {
	return true
}

func False() bool {
	return false
}
`)
	muts, err := FindMutations("test.go", src)
	if err != nil {
		t.Fatalf("FindMutations: %v", err)
	}
	if len(muts) != 2 {
		t.Fatalf("got %d mutations; want 2 (%v)", len(muts), opNames(muts))
	}
	if muts[0].Operator != "return_true_to_false" {
		t.Errorf("first = %q; want return_true_to_false", muts[0].Operator)
	}
	if muts[1].Operator != "return_false_to_true" {
		t.Errorf("second = %q; want return_false_to_true", muts[1].Operator)
	}
}

// TestFindMutations_SkipsStringLiteralsAndComments pins the AST-walker's
// implicit-skip semantic for non-code positions. A textual mutator would
// flip the == inside the string literal and the comment; the AST walker
// only visits expression nodes so neither produces a mutation site.
func TestFindMutations_SkipsStringLiteralsAndComments(t *testing.T) {
	t.Parallel()
	src := []byte(`package p

// This comment contains == and != and && and ||.
const S = "x == y && z != w"

func f() {}
`)
	muts, err := FindMutations("test.go", src)
	if err != nil {
		t.Fatalf("FindMutations: %v", err)
	}
	if len(muts) != 0 {
		t.Errorf("got %d mutations from literals/comments; want 0 (%v)", len(muts), opNames(muts))
	}
}

// TestFindMutations_MultipleSitesInOneFile pins per-site offset
// independence — multiple mutations in the same file should each have
// distinct StartOffset values pointing at the correct operator position.
func TestFindMutations_MultipleSitesInOneFile(t *testing.T) {
	t.Parallel()
	src := []byte(`package p

func f(a, b int) bool {
	if a == b {
		return true
	}
	if a != b {
		return false
	}
	return true
}
`)
	muts, err := FindMutations("test.go", src)
	if err != nil {
		t.Fatalf("FindMutations: %v", err)
	}
	if len(muts) < 4 {
		t.Fatalf("got %d mutations; want at least 4", len(muts))
	}
	offsets := map[int]bool{}
	for _, m := range muts {
		if offsets[m.StartOffset] {
			t.Errorf("duplicate offset %d", m.StartOffset)
		}
		offsets[m.StartOffset] = true
		// Every offset must be a valid index into src.
		if m.StartOffset < 0 || m.EndOffset > len(src) {
			t.Errorf("mutation offset out of range: %d..%d (src len %d)",
				m.StartOffset, m.EndOffset, len(src))
		}
		// Original bytes at the site must match m.Original.
		got := string(src[m.StartOffset:m.EndOffset])
		if got != m.Original {
			t.Errorf("offset %d: src[%d:%d] = %q; want %q",
				m.StartOffset, m.StartOffset, m.EndOffset, got, m.Original)
		}
	}
}

// TestFindMutations_ParseError pins the error path: malformed Go input
// surfaces the parser error wrapped with the filename.
func TestFindMutations_ParseError(t *testing.T) {
	t.Parallel()
	_, err := FindMutations("test.go", []byte("not Go syntax {{{"))
	if err == nil {
		t.Errorf("malformed input should error")
	}
}

// TestApplyMutation_HappyPath pins the byte-replacement contract.
func TestApplyMutation_HappyPath(t *testing.T) {
	t.Parallel()
	src := []byte("if a == b { return true }")
	// "==" is at offset 5..7. Mutate to "!=".
	m := Mutation{
		StartOffset: 5,
		EndOffset:   7,
		Original:    "==",
		Replacement: "!=",
	}
	out := ApplyMutation(src, m)
	want := "if a != b { return true }"
	if string(out) != want {
		t.Errorf("got %q; want %q", string(out), want)
	}
	// src is unchanged.
	if string(src) != "if a == b { return true }" {
		t.Errorf("src mutated in place: %q", string(src))
	}
}

// TestApplyMutation_OutOfRange pins the defensive guards.
func TestApplyMutation_OutOfRange(t *testing.T) {
	t.Parallel()
	src := []byte("short")
	// EndOffset past src.
	m := Mutation{StartOffset: 0, EndOffset: 100, Original: "short", Replacement: "x"}
	out := ApplyMutation(src, m)
	if string(out) != string(src) {
		t.Errorf("out-of-range should return unchanged; got %q", string(out))
	}
	// Negative StartOffset.
	m = Mutation{StartOffset: -1, EndOffset: 3, Original: "sho", Replacement: "x"}
	out = ApplyMutation(src, m)
	if string(out) != string(src) {
		t.Errorf("negative offset should return unchanged; got %q", string(out))
	}
}

// TestApplyMutation_StaleContent pins the stale-mutation guard: if the
// bytes at the recorded position no longer match m.Original (because
// src has changed since the mutation was computed), the function
// returns src unchanged rather than corrupting it.
func TestApplyMutation_StaleContent(t *testing.T) {
	t.Parallel()
	src := []byte("if a != b { return true }")
	// Mutation expects "==" at 5..7 but src now has "!=" there.
	m := Mutation{StartOffset: 5, EndOffset: 7, Original: "==", Replacement: "!="}
	out := ApplyMutation(src, m)
	if string(out) != string(src) {
		t.Errorf("stale content should return unchanged; got %q", string(out))
	}
}

// TestShouldSkipFile_TestFiles pins the _test.go skip rule.
func TestShouldSkipFile_TestFiles(t *testing.T) {
	t.Parallel()
	if !shouldSkipFile("/path/to/foo_test.go") {
		t.Errorf("_test.go should skip")
	}
	if shouldSkipFile("/path/to/foo.go") {
		// foo.go isn't generated, isn't a test → should NOT skip.
		// But shouldSkipFile reads the file; a non-existent path returns false.
	}
}

// TestShouldSkipFile_GeneratedMarker pins the generated-file skip.
func TestShouldSkipFile_GeneratedMarker(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	genFile := filepath.Join(tmp, "gen.go")
	if err := os.WriteFile(genFile, []byte("// Code generated by stringer. DO NOT EDIT.\n\npackage p\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !shouldSkipFile(genFile) {
		t.Errorf("generated file should skip")
	}

	plainFile := filepath.Join(tmp, "plain.go")
	if err := os.WriteFile(plainFile, []byte("// Plain comment\npackage p\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if shouldSkipFile(plainFile) {
		t.Errorf("plain file should NOT skip")
	}
}

// TestCollectMutationsForPackages_HappyPath pins the directory-walk
// flow: writes two .go files in a temp package directory, asserts the
// mutation collector finds the expected sites and ignores _test.go +
// generated files.
func TestCollectMutationsForPackages_HappyPath(t *testing.T) {
	t.Parallel()
	tmpRoot := t.TempDir()
	pkgDir := filepath.Join(tmpRoot, "pkg", "mypkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Production file with one == mutation site.
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"),
		[]byte("package mypkg\n\nfunc Eq(a, b int) bool { return a == b }\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	// Test file — should be skipped.
	if err := os.WriteFile(filepath.Join(pkgDir, "main_test.go"),
		[]byte("package mypkg\n\nfunc TestEq() { _ = 1 == 1 }\n"), 0o644); err != nil {
		t.Fatalf("write test: %v", err)
	}
	// Generated file — should be skipped.
	if err := os.WriteFile(filepath.Join(pkgDir, "gen.go"),
		[]byte("// Code generated by stringer. DO NOT EDIT.\n\npackage mypkg\n\nfunc Gen() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("write gen: %v", err)
	}
	muts, err := collectMutationsForPackages(tmpRoot, []string{"pkg/mypkg"})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// Only main.go's one == should appear.
	if len(muts) != 1 {
		t.Fatalf("got %d mutations; want 1 (%v)", len(muts), opNames(muts))
	}
	if muts[0].Operator != "equality_to_inequality" {
		t.Errorf("got %s; want equality_to_inequality", muts[0].Operator)
	}
	if !strings.HasSuffix(muts[0].File, "main.go") {
		t.Errorf("got file %q; want path ending in main.go", muts[0].File)
	}
}

// opNames returns the operator names for diagnostic output.
func opNames(muts []Mutation) []string {
	out := make([]string, len(muts))
	for i, m := range muts {
		out[i] = m.Operator
	}
	return out
}
