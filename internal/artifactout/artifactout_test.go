// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package artifactout

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteArtifacts_HappyPath pins the per-file write contract:
// each entry lands at outRoot/<relPath> with parent dirs created on
// demand.
func TestWriteArtifacts_HappyPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	files := map[string][]byte{
		"a/b/c.yaml":    []byte("c-content"),
		"top.txt":       []byte("top-content"),
		"e/nested.json": []byte("e-content"),
	}
	if err := WriteAll(tmp, tmp, files); err != nil {
		t.Fatalf("WriteAll: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(tmp, rel))
		if err != nil {
			t.Errorf("read %q: %v", rel, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%q = %q; want %q", rel, got, want)
		}
	}
}

// TestWriteArtifacts_RejectsEmptyPath pins the empty-path guard.
func TestWriteArtifacts_RejectsEmptyPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	err := WriteAll(tmp, tmp, map[string][]byte{"": []byte("x")})
	if err == nil {
		t.Errorf("empty path should error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty; got %v", err)
	}
}

// TestWriteArtifacts_RejectsTraversal pins the path-traversal guard.
func TestWriteArtifacts_RejectsTraversal(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	err := WriteAll(tmp, tmp, map[string][]byte{"../escape.yaml": []byte("x")})
	if err == nil {
		t.Errorf("traversal path should error")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error should mention invalid; got %v", err)
	}
	// Pure ".." should also fail.
	err = WriteAll(tmp, tmp, map[string][]byte{"..": []byte("x")})
	if err == nil {
		t.Errorf("pure .. path should error")
	}
}

// TestWriteArtifactsEmitsInDeterministicOrder pins that the artifact writer's
// emission order is a function of its input and not of Go's map iteration.
//
// Byte-comparing two runs' output would NOT establish this: a map with few keys
// can iterate the same way twice, so that comparison passes while the defect
// stands. This reads the order directly instead, through an outcome that depends
// on nothing else.
//
// The technique: several input names clean to the SAME destination, so the file
// left on disk carries whichever of them was written LAST. That makes emission
// order observable without a seam in the writer. Under sorted emission the last
// writer for a destination is its greatest name, every time; under map iteration
// it is whichever name the runtime happened to reach last, and a probe over this
// exact key set observed all four names taking that position.
//
// The whole check runs repeatedly in one process, because map order is
// randomised per range statement rather than per process: a single call cannot
// distinguish a sorted writer from a lucky one.
func TestWriteArtifactsEmitsInDeterministicOrder(t *testing.T) {
	t.Parallel()

	// Two destinations, each named by two input keys. Content is the key itself,
	// so the file on disk names its own last writer.
	files := map[string][]byte{
		"./p/one.yaml":    []byte("./p/one.yaml"),
		"p/one.yaml":      []byte("p/one.yaml"),
		"q/../q/two.yaml": []byte("q/../q/two.yaml"),
		"q/two.yaml":      []byte("q/two.yaml"),
	}
	// Sorted order puts each destination's greatest name last, so these are the
	// contents a sorted writer must leave behind on every single run.
	wantLastWriter := map[string]string{
		filepath.Join("p", "one.yaml"): "p/one.yaml",
		filepath.Join("q", "two.yaml"): "q/two.yaml",
	}

	const runs = 64
	for i := 0; i < runs; i++ {
		tmp := t.TempDir()
		if err := WriteAll(tmp, tmp, files); err != nil {
			t.Fatalf("run %d: WriteAll: %v", i, err)
		}
		for dest, want := range wantLastWriter {
			got, err := os.ReadFile(filepath.Join(tmp, dest))
			if err != nil {
				t.Fatalf("run %d: read %s: %v", i, dest, err)
			}
			if string(got) != want {
				t.Fatalf("run %d: %s was last written by %q, want %q — emission order is following map iteration rather than the input",
					i, dest, got, want)
			}
		}
	}
}

// TestWriteArtifactsAcceptsLeadingDoubleDotName pins that an artifact name
// beginning with two dots followed by a letter is an ordinary name. It is a
// rendered output name, not a path the compiler authored, so a provider free to
// emit one must not have the whole build refused for it.
func TestWriteArtifactsAcceptsLeadingDoubleDotName(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	files := map[string][]byte{
		"..shared/values.yaml": []byte("shared"),
		"..README":             []byte("readme"),
	}
	if err := WriteAll(tmp, tmp, files); err != nil {
		t.Fatalf("WriteAll: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

// TestWriteArtifactsRefusesAbsoluteName pins a tightening that came with routing
// this writer through the shared containment predicate.
//
// An absolute artifact name previously passed the escape test — it carries no
// leading two dots — and was then silently re-rooted under outRoot by the join,
// so a renderer emitting "/etc/config.yaml" got a file at "<out>/etc/config.yaml"
// with no diagnostic and no sign the name it asked for was not the name it got.
// An absolute name addresses a location of its own, so it is now a loud refusal.
func TestWriteArtifactsRefusesAbsoluteName(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	err := WriteAll(tmp, tmp, map[string][]byte{"/etc/config.yaml": []byte("x")})
	if err == nil {
		t.Fatal("WriteAll accepted an absolute artifact name; an absolute name is not a name relative to the output root")
	}
	if !strings.Contains(err.Error(), "escapes the output root") {
		t.Errorf("refusal = %v; want it to name the output root", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "etc", "config.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("the absolute name was re-rooted under the output root anyway; stat err = %v", statErr)
	}
}

// TestWriteBuildOutputClearsTheBoundariesSubtree pins the half of a build's
// write that is not a write: a file a previous build of a different variant left
// under boundaries/ must not survive into a tree that no longer produces it,
// because a consumer reading that tree would apply an artifact the compiler did
// not just render.
//
// The stale file is placed at a path the second build does not emit, so its
// disappearance can only be the clearing. The second build's own file is read
// back in the same run, so a clearing that removed everything would fail here
// rather than passing as a stricter version of the same guarantee.
func TestWriteBuildOutputClearsTheBoundariesSubtree(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	if err := WriteBuildOutput(tmp, tmp, map[string][]byte{
		"boundaries/prod/old.yaml": []byte("from the previous variant"),
	}); err != nil {
		t.Fatalf("first WriteBuildOutput: %v", err)
	}
	stale := filepath.Join(tmp, "boundaries", "prod", "old.yaml")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("precondition: the first build left nothing to clear: %v", err)
	}

	if err := WriteBuildOutput(tmp, tmp, map[string][]byte{
		"boundaries/dev/new.yaml": []byte("from this variant"),
	}); err != nil {
		t.Fatalf("second WriteBuildOutput: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the previous variant's %s survived the build; stat err = %v", stale, err)
	}
	got, err := os.ReadFile(filepath.Join(tmp, "boundaries", "dev", "new.yaml"))
	if err != nil {
		t.Fatalf("read this build's own output: %v", err)
	}
	if string(got) != "from this variant" {
		t.Errorf("this build's output = %q", got)
	}
}

// TestWriteBuildOutputLeavesTheRestOfTheOutputRootAlone pins the bound on the
// clearing. Only the subtree a build regenerates in full is cleared; anything a
// caller put beside it is theirs, and a write that emptied the output root would
// destroy content this package never produced.
func TestWriteBuildOutputLeavesTheRestOfTheOutputRootAlone(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	beside := filepath.Join(tmp, "notes.txt")
	if err := os.WriteFile(beside, []byte("not the compiler's"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := WriteBuildOutput(tmp, tmp, map[string][]byte{"boundaries/prod/app.yaml": []byte("x")}); err != nil {
		t.Fatalf("WriteBuildOutput: %v", err)
	}

	got, err := os.ReadFile(beside)
	if err != nil {
		t.Fatalf("the file beside the boundaries subtree was removed: %v", err)
	}
	if string(got) != "not the compiler's" {
		t.Errorf("%s = %q", beside, got)
	}
}

// TestWriteBuildOutputRefusesAnEscapingName pins that routing a build's
// write through the clearing entry point does not bypass the check the bare
// write owes. A name escaping the output root is refused here exactly as it is
// there, so neither surface performing a build is the one that accepts it.
func TestWriteBuildOutputRefusesAnEscapingName(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	err := WriteBuildOutput(tmp, tmp, map[string][]byte{"../escape.yaml": []byte("x")})
	if err == nil {
		t.Fatal("WriteBuildOutput accepted a name that escapes the output root")
	}
	if !strings.Contains(err.Error(), "escapes the output root") {
		t.Errorf("refusal = %v; want it to name the output root", err)
	}
}

// TestDryRunReportOrdersArtifactsDeterministically pins the report's ordering
// against Go's randomised map iteration. Rendered output is byte-identical for
// identical input, and a report of that output that reshuffled between runs would
// be useless for exactly the diff-based reading the guarantee exists for.
func TestDryRunReportOrdersArtifactsDeterministically(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"b/second.yaml": []byte("second\n"),
		"a/first.yaml":  []byte("first\n"),
		"c/third.yaml":  []byte("third"),
	}
	var first bytes.Buffer
	WriteDryRunReport(&first, "dist", files)

	for i := 0; i < 8; i++ {
		var again bytes.Buffer
		WriteDryRunReport(&again, "dist", files)
		if again.String() != first.String() {
			t.Fatalf("run %d differs from the first:\n%s\n---\n%s", i, first.String(), again.String())
		}
	}

	want := "==> dist/a/first.yaml <==\nfirst\n\n" +
		"==> dist/b/second.yaml <==\nsecond\n\n" +
		"==> dist/c/third.yaml <==\nthird\n\n"
	if first.String() != want {
		t.Errorf("report =\n%q\nwant\n%q", first.String(), want)
	}
}
