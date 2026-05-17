// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Outcome classifies the result of running the test suite against one
// mutant.
type Outcome int

const (
	// OutcomeSurvived — the mutant compiled and every test passed. The
	// test suite did not detect the change; the mutant survives.
	OutcomeSurvived Outcome = iota

	// OutcomeKilled — the mutant compiled and at least one test failed.
	// The test suite detected the change; the mutant is killed.
	OutcomeKilled

	// OutcomeEquivalent — the mutant failed to compile. The mutation
	// produced syntactically- or type-invalid Go code. Such mutations
	// don't count toward the killed/survived score because they don't
	// represent a real behavior change the test suite could detect.
	OutcomeEquivalent
)

// String returns the outcome name suitable for logs and reports.
func (o Outcome) String() string {
	switch o {
	case OutcomeSurvived:
		return "survived"
	case OutcomeKilled:
		return "killed"
	case OutcomeEquivalent:
		return "equivalent"
	default:
		return "unknown"
	}
}

// Result pairs a Mutation with the outcome of running tests against it.
type Result struct {
	Mutation Mutation
	Outcome  Outcome
}

// Runner is the abstraction over `go test` invocation. Production code
// uses goTestRunner; tests use a fake implementation to assert orchestration
// logic without spawning subprocesses.
//
// The packagePath argument names which package(s) to run tests in,
// using Go's standard package-import syntax (e.g. "./pkg/resolver" or
// "./..." for the whole module). RunMutations passes the package
// containing the mutated file so per-package scope avoids cross-
// package test-ordering surprises.
type Runner interface {
	RunTests(ctx context.Context, packagePath string) (Outcome, error)
}

// goTestRunner runs `go test -timeout 60s -count=1 <packagePath>` from
// the given directory and classifies the result.
//
// The runner uses per-package scope rather than `./...` to avoid pre-
// existing test-suite order-sensitive flakiness in the broader cluster:
// some tests in this codebase pass when their containing package runs
// as a whole, but fail when run in isolation under -count=1 (the
// failures aren't related to mutations). Per-package scope runs the
// full package's test suite together, matching the ordering shape that
// keeps those tests green. The cost: a mutation in package A whose
// effect would only surface in a test in package B doesn't get caught
// — acceptable for a baseline metric where the goal is to measure the
// substance of each package's own tests against its own production
// code.
type goTestRunner struct {
	rootDir string
}

func (r *goTestRunner) RunTests(ctx context.Context, packagePath string) (Outcome, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-timeout", "60s", "-count=1", packagePath)
	cmd.Dir = r.rootDir
	output, err := cmd.CombinedOutput()
	return classify(string(output), err), nil
}

// compileErrorPattern matches Go build-error output lines like
//
//	./path/file.go:42:5: undefined: foo
//
// The pattern is conservative: lines starting with a .go: prefix carrying
// line:column position markers are compile-time output, not test-runtime
// FAIL markers. Test-runtime FAILs show up as `--- FAIL:` or `FAIL\t<pkg>`.
var compileErrorPattern = regexp.MustCompile(`(?m)^[^[:space:]]*\.go:\d+:\d+:[[:space:]]`)

// buildFailedMarker is the literal Go emits when reporting per-package
// build failure during `go test ./...`. It appears on its own line as
// `FAIL\t<pkg> [build failed]`.
const buildFailedMarker = "[build failed]"

// classify turns the (output, err) pair from `go test` into one of the
// three outcomes. The exit code alone is insufficient: a mutant that
// breaks the build produces non-zero exit but is semantically distinct
// from a mutant that breaks the tests.
//
// Decision tree:
//
//  1. err == nil → tests passed → SURVIVED.
//  2. err != nil AND output contains "[build failed]" → EQUIVALENT.
//  3. err != nil AND output matches the compile-error line pattern but
//     contains NO `--- FAIL:` test marker → EQUIVALENT (build failed
//     before any test ran).
//  4. err != nil AND output contains `--- FAIL:` or `FAIL\t<pkg>` test
//     failure markers → KILLED.
//  5. err != nil otherwise → KILLED (defensive default; some test
//     harnesses produce no `--- FAIL:` line, e.g. a panic exiting the
//     test binary before the FAIL marker prints).
func classify(output string, err error) Outcome {
	if err == nil {
		return OutcomeSurvived
	}
	if strings.Contains(output, buildFailedMarker) {
		return OutcomeEquivalent
	}
	hasTestFail := strings.Contains(output, "--- FAIL:") || regexp.MustCompile(`(?m)^FAIL\t`).MatchString(output)
	if !hasTestFail && compileErrorPattern.MatchString(output) {
		return OutcomeEquivalent
	}
	return OutcomeKilled
}

// RunMutations applies each mutation in muts, runs the test suite via
// runner, classifies the outcome, restores the original file, and returns
// the results in the same order as muts. Sequential by design — parallel
// mutation would require per-mutant working copies of every file, which
// adds complexity disproportionate to a one-shot baseline run.
//
// rootDir is the aggrega-cli module root; mutation file paths are made
// relative to it to derive the package-import path the runner uses.
//
// Restore happens via defer inside the per-mutation closure so a panic in
// runner.RunTests still restores the file. A SIGKILL between mutate and
// restore would leave the file mutated; for a one-shot tool invoked at
// the developer's terminal this is acceptable (git status surfaces the
// drift immediately).
func RunMutations(ctx context.Context, rootDir string, muts []Mutation, runner Runner, progress func(i, total int, m Mutation, o Outcome)) ([]Result, error) {
	results := make([]Result, 0, len(muts))
	for i, m := range muts {
		orig, err := os.ReadFile(m.File)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", m.File, err)
		}
		mutated := ApplyMutation(orig, m)
		// If ApplyMutation returned the source unchanged (out-of-range
		// or stale-content guard), skip this site — the runner would
		// report SURVIVED but the mutation didn't actually apply.
		if &mutated[0] == &orig[0] || string(mutated) == string(orig) {
			// Slice-aliasing check first, then content equality.
			results = append(results, Result{Mutation: m, Outcome: OutcomeEquivalent})
			if progress != nil {
				progress(i+1, len(muts), m, OutcomeEquivalent)
			}
			continue
		}
		if err := os.WriteFile(m.File, mutated, 0o644); err != nil {
			return nil, fmt.Errorf("write mutant %s: %w", m.File, err)
		}
		pkgPath := packageImportPathForFile(m.File, rootDir)
		var outcome Outcome
		func() {
			defer func() {
				_ = os.WriteFile(m.File, orig, 0o644)
			}()
			outcome, _ = runner.RunTests(ctx, pkgPath)
		}()
		results = append(results, Result{Mutation: m, Outcome: outcome})
		if progress != nil {
			progress(i+1, len(muts), m, outcome)
		}
	}
	return results, nil
}

// packageImportPathForFile derives the package-import path (e.g.
// "./pkg/resolver") for a mutation's file, relative to the aggrega-cli
// module root. The shape matches what `go test` accepts as a positional
// argument: a relative path with the `./` prefix. Falls back to "./..."
// when the file isn't under rootDir (defensive; should not happen for
// mutations collected from the package list).
func packageImportPathForFile(file, rootDir string) string {
	idx := strings.Index(file, rootDir)
	if idx < 0 {
		return "./..."
	}
	rel := file[idx+len(rootDir):]
	rel = strings.TrimPrefix(rel, "/")
	// Drop the trailing filename component.
	if slash := strings.LastIndex(rel, "/"); slash >= 0 {
		rel = rel[:slash]
	}
	return "./" + rel
}

// progressLine returns a one-line status string suitable for stderr output
// during a long-running mutation pass. Format intentionally compact so
// hundreds of lines remain scannable in a CI log.
func progressLine(i, total int, m Mutation, o Outcome, started time.Time) string {
	elapsed := time.Since(started).Round(time.Second)
	return fmt.Sprintf("[%d/%d %s] %s %s:%d %-10s",
		i, total, elapsed, m.Operator, m.File, m.Position.Line, o)
}
