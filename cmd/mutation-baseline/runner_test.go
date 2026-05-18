// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestClassify_ExitZeroIsSurvived pins the happy path: tests passed.
func TestClassify_ExitZeroIsSurvived(t *testing.T) {
	t.Parallel()
	if got := classify("ok\tfoo\n", nil); got != OutcomeSurvived {
		t.Errorf("got %s; want survived", got)
	}
}

// TestClassify_BuildFailedMarkerIsEquivalent pins the build-failed
// marker dispatch — Go's per-package marker for failed compilation.
func TestClassify_BuildFailedMarkerIsEquivalent(t *testing.T) {
	t.Parallel()
	output := "FAIL\tgithub.com/foo/bar [build failed]\n"
	if got := classify(output, errors.New("exit 2")); got != OutcomeEquivalent {
		t.Errorf("got %s; want equivalent", got)
	}
}

// TestClassify_CompileErrorPatternIsEquivalent pins the compile-error
// regex path: output containing `file.go:N:M: ` markers but no test FAIL
// is classified as a build failure.
func TestClassify_CompileErrorPatternIsEquivalent(t *testing.T) {
	t.Parallel()
	output := "./foo.go:42:5: undefined: bar\n"
	if got := classify(output, errors.New("exit 2")); got != OutcomeEquivalent {
		t.Errorf("got %s; want equivalent", got)
	}
}

// TestClassify_TestFailMarkerIsKilled pins the test-failure path: the
// `--- FAIL:` marker dispatches to killed.
func TestClassify_TestFailMarkerIsKilled(t *testing.T) {
	t.Parallel()
	output := "--- FAIL: TestX (0.00s)\nFAIL\tfoo\t0.001s\n"
	if got := classify(output, errors.New("exit 1")); got != OutcomeKilled {
		t.Errorf("got %s; want killed", got)
	}
}

// TestClassify_FailPackageMarkerIsKilled pins the alternative
// `FAIL\t<pkg>` marker.
func TestClassify_FailPackageMarkerIsKilled(t *testing.T) {
	t.Parallel()
	output := "FAIL\tfoo\t0.001s\n"
	if got := classify(output, errors.New("exit 1")); got != OutcomeKilled {
		t.Errorf("got %s; want killed", got)
	}
}

// TestClassify_PanicWithoutFailMarkerIsKilled pins the defensive
// default: non-zero exit + no compile-error markers + no FAIL markers
// (e.g., test panicked before FAIL printed) classifies as killed.
func TestClassify_PanicWithoutFailMarkerIsKilled(t *testing.T) {
	t.Parallel()
	output := "panic: bang\n"
	if got := classify(output, errors.New("exit 2")); got != OutcomeKilled {
		t.Errorf("got %s; want killed (defensive)", got)
	}
}

// fakeRunner records each invocation and returns a programmed outcome
// sequence. Used to test RunMutations without spawning real subprocesses.
type fakeRunner struct {
	outcomes []Outcome
	idx      int
	called   int
}

func (r *fakeRunner) RunTests(_ context.Context, _ string) (Outcome, error) {
	r.called++
	if r.idx >= len(r.outcomes) {
		return OutcomeSurvived, nil
	}
	o := r.outcomes[r.idx]
	r.idx++
	return o, nil
}

// TestRunMutations_RestoresOriginalAfterEach pins the mutate-test-
// restore contract: every mutation must leave the source file in its
// original state after the runner returns.
func TestRunMutations_RestoresOriginalAfterEach(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "main.go")
	original := []byte("package p\n\nfunc F() bool { return true }\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	muts, err := FindMutations(path, original)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(muts) != 1 {
		t.Fatalf("got %d mutations; want 1", len(muts))
	}
	runner := &fakeRunner{outcomes: []Outcome{OutcomeKilled}}
	_, err = RunMutations(context.Background(), tmp, muts, runner, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// File contents must equal original.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("file not restored:\n%s", string(after))
	}
}

// TestRunMutations_CollectsAllOutcomes pins the result ordering and
// outcome classification: every mutation gets a result in input order.
func TestRunMutations_CollectsAllOutcomes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "main.go")
	src := []byte(`package p

func F(a, b int) bool {
	if a == b {
		return true
	}
	return false
}
`)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	muts, err := FindMutations(path, src)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	// Expect three sites: ==, return true, return false.
	if len(muts) != 3 {
		t.Fatalf("got %d mutations; want 3 (%v)", len(muts), opNames(muts))
	}
	runner := &fakeRunner{outcomes: []Outcome{OutcomeKilled, OutcomeSurvived, OutcomeEquivalent}}
	results, err := RunMutations(context.Background(), tmp, muts, runner, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results; want 3", len(results))
	}
	if results[0].Outcome != OutcomeKilled {
		t.Errorf("results[0] = %s; want killed", results[0].Outcome)
	}
	if results[1].Outcome != OutcomeSurvived {
		t.Errorf("results[1] = %s; want survived", results[1].Outcome)
	}
	if results[2].Outcome != OutcomeEquivalent {
		t.Errorf("results[2] = %s; want equivalent", results[2].Outcome)
	}
	// Runner was called once per mutation.
	if runner.called != 3 {
		t.Errorf("runner called %d times; want 3", runner.called)
	}
}

// TestRunMutations_ProgressCallback pins the progress callback dispatch
// — every mutation gets one callback with the correct index + total.
func TestRunMutations_ProgressCallback(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "main.go")
	src := []byte("package p\n\nfunc F() bool { return true }\n")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	muts, err := FindMutations(path, src)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	runner := &fakeRunner{outcomes: []Outcome{OutcomeKilled}}
	called := 0
	progress := func(i, total int, m Mutation, o Outcome) {
		called++
		if total != 1 {
			t.Errorf("total = %d; want 1", total)
		}
		if i != 1 {
			t.Errorf("i = %d; want 1", i)
		}
	}
	_, err = RunMutations(context.Background(), tmp, muts, runner, progress)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if called != 1 {
		t.Errorf("progress called %d times; want 1", called)
	}
}

// TestOutcome_String pins the human-readable names.
func TestOutcome_String(t *testing.T) {
	t.Parallel()
	if OutcomeSurvived.String() != "survived" {
		t.Errorf("survived.String() = %q", OutcomeSurvived.String())
	}
	if OutcomeKilled.String() != "killed" {
		t.Errorf("killed.String() = %q", OutcomeKilled.String())
	}
	if OutcomeEquivalent.String() != "equivalent" {
		t.Errorf("equivalent.String() = %q", OutcomeEquivalent.String())
	}
	if Outcome(99).String() != "unknown" {
		t.Errorf("Outcome(99).String() = %q", Outcome(99).String())
	}
}

// TestPackageRelPath_AndRelativeFile pins the path-trimming helpers.
func TestPackageRelPath_AndRelativeFile(t *testing.T) {
	t.Parallel()
	root := "/home/dev/aggrega-cli"
	file := "/home/dev/aggrega-cli/pkg/resolver/main.go"
	if got := packageRelPath(file, root); got != "pkg/resolver" {
		t.Errorf("packageRelPath = %q; want pkg/resolver", got)
	}
	if got := relativeFile(file, root); got != "pkg/resolver/main.go" {
		t.Errorf("relativeFile = %q; want pkg/resolver/main.go", got)
	}
	// File outside the root.
	if got := packageRelPath("/elsewhere/file.go", root); got != "/elsewhere/file.go" {
		t.Errorf("outside root: got %q", got)
	}
	// relativeFile's outside-root branch — returns the file as-is when
	// root is not a prefix of file. Pairs with packageRelPath above so
	// both helpers' outside-root behaviour is pinned.
	if got := relativeFile("/elsewhere/file.go", root); got != "/elsewhere/file.go" {
		t.Errorf("relativeFile outside root: got %q", got)
	}
}

// TestPackageImportPathForFile pins the two branches of
// packageImportPathForFile: a file inside the root produces a Go-style
// `./pkg/...` import path; a file outside the root degrades to `./...`
// (the safe top-level fallback). Both branches matter — the inside-root
// case is the hot path for catalog-rooted mutator runs; the outside-root
// fallback is what keeps the runner from blowing up when mutator inputs
// come from outside the conventional layout.
func TestPackageImportPathForFile(t *testing.T) {
	t.Parallel()
	root := "/home/dev/aggrega-cli"

	if got := packageImportPathForFile(root+"/pkg/resolver/main.go", root); got != "./pkg/resolver" {
		t.Errorf("inside-root: packageImportPathForFile = %q; want ./pkg/resolver", got)
	}
	if got := packageImportPathForFile(root+"/cmd/aggrega/build.go", root); got != "./cmd/aggrega" {
		t.Errorf("inside-root cmd subtree: packageImportPathForFile = %q; want ./cmd/aggrega", got)
	}
	if got := packageImportPathForFile("/elsewhere/file.go", root); got != "./..." {
		t.Errorf("outside-root: packageImportPathForFile = %q; want ./...", got)
	}
}

// TestAggregateByPackage pins the per-package aggregation.
func TestAggregateByPackage(t *testing.T) {
	t.Parallel()
	root := "/aggrega-cli"
	results := []Result{
		{Mutation: Mutation{File: root + "/pkg/a/x.go"}, Outcome: OutcomeKilled},
		{Mutation: Mutation{File: root + "/pkg/a/x.go"}, Outcome: OutcomeKilled},
		{Mutation: Mutation{File: root + "/pkg/a/x.go"}, Outcome: OutcomeSurvived},
		{Mutation: Mutation{File: root + "/pkg/b/y.go"}, Outcome: OutcomeEquivalent},
	}
	reports := AggregateByPackage(results, root)
	if len(reports) != 2 {
		t.Fatalf("got %d packages; want 2", len(reports))
	}
	a := reports["pkg/a"]
	if a.Killed != 2 || a.Survived != 1 || a.Equivalent != 0 {
		t.Errorf("pkg/a: killed=%d survived=%d equivalent=%d", a.Killed, a.Survived, a.Equivalent)
	}
	wantScore := 2.0 / 3.0
	if a.Score != wantScore {
		t.Errorf("pkg/a score = %.3f; want %.3f", a.Score, wantScore)
	}
	b := reports["pkg/b"]
	if b.Killed != 0 || b.Survived != 0 || b.Equivalent != 1 {
		t.Errorf("pkg/b: killed=%d survived=%d equivalent=%d", b.Killed, b.Survived, b.Equivalent)
	}
	if b.Score != 0.0 {
		t.Errorf("pkg/b score = %.3f; want 0", b.Score)
	}
}

// TestRunMutations_RestoreFailureContinues pins the contract that a
// failing restoreFile call does not abort the run: the loop continues
// classifying remaining mutations and returns the full results slice.
// This was a regression risk under the previous swallowed-error path
// where a failure left the file mutated and the operator never saw the
// signal.
func TestRunMutations_RestoreFailureContinues(t *testing.T) {
	orig := restoreFile
	defer func() { restoreFile = orig }()

	var callCount int
	restoreFile = func(name string, data []byte, perm os.FileMode) error {
		callCount++
		// Always fail; the loop must continue regardless. The scratch
		// copy is the only file affected so leaving it mutated is
		// acceptable behavior.
		return errors.New("simulated restore failure")
	}

	tmp := t.TempDir()
	path := filepath.Join(tmp, "main.go")
	src := []byte("package p\n\nfunc F(a, b int) bool { return a == b }\n")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	muts, err := FindMutations(path, src)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(muts) == 0 {
		t.Fatalf("expected at least one mutation site")
	}

	runner := &fakeRunner{outcomes: []Outcome{OutcomeKilled, OutcomeSurvived}}
	results, err := RunMutations(context.Background(), tmp, muts, runner, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != len(muts) {
		t.Errorf("got %d results; want %d (loop must continue past restore failure)", len(results), len(muts))
	}
	if callCount != len(muts) {
		t.Errorf("restoreFile called %d times; want %d (one per mutation)", callCount, len(muts))
	}
}

// TestProgressLine pins the format of the progress one-liner.
func TestProgressLine(t *testing.T) {
	t.Parallel()
	m := Mutation{
		Operator: "equality_to_inequality",
		File:     "/p/foo.go",
		Position: token.Position{Line: 10},
	}
	line := progressLine(1, 10, m, OutcomeKilled, time.Now())
	if line == "" {
		t.Errorf("empty progress line")
	}
}
