// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Binary mutation-baseline produces a per-package mutation testing
// baseline for the aggrega-cli codebase. The binary implements ten
// AST-aware mutation operators (six binary-comparison flips, two
// logical-operator flips, two return-bool flips), applies each at every
// detected site across a fixed list of packages, runs the test suite,
// classifies each outcome as killed/survived/equivalent, and emits a
// markdown report.
//
// Usage:
//
//	mutation-baseline                        # smoke check + full baseline; report to stdout
//	mutation-baseline --output PATH          # smoke + full; report to PATH
//	mutation-baseline --smoke-only           # smoke check only; exit after
//	mutation-baseline --packages a,b,c       # override the default package list
//
// The binary always runs the smoke check first. The smoke targets are
// the two functions in the smokeprobe subpackage: TrueAlways (tested,
// must be killed) and UncoveredFunc (untested, must survive). If either
// outcome doesn't match expectation, the binary aborts before the full
// baseline so the operator can investigate the classification mechanism.
//
// # Scratch-copy isolation
//
// Mutations apply against an isolated copy of the aggrega-cli source
// tree under $TMPDIR (see scratch.go). The original source tree is never
// touched. A SIGINT/SIGTERM during the run triggers cleanup of the
// scratch directory; a SIGKILL leaks one tmpdir but cannot corrupt the
// source. Tests spawned by the runner execute with HOME pointing inside
// the scratch directory so any tool that writes under $HOME (caches,
// editor state, AI-harness directories) cannot pollute the operator's
// real $HOME. The binary itself spawns no editor and no AI-harness; any
// such directories appearing under the source tree during a run come
// from the operator's surrounding terminal harness, not from this
// binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// defaultPackages enumerates the packages mutated by a no-argument
// invocation. The list is the load-bearing test-coverage cluster;
// cmd/aggrega is intentionally excluded because the carve-outs recorded
// in scripts/coverage-baseline.txt already explain why per-function unit
// tests aren't the load-bearing signal there.
var defaultPackages = []string{
	"pkg/resolver",
	"pkg/validator",
	"pkg/validator/jsonschema",
	"pkg/catalog",
	"internal/compiler/ir",
	"internal/compiler/analyzer",
	"internal/compiler/topology",
	"internal/compiler/renderer",
	"plugins/internal/helmcore",
	"plugins/internal/helmchartcore",
	"pkg/types",
}

func main() {
	var (
		outputPath = flag.String("output", "", "write the markdown report to PATH (default: stdout)")
		smokeOnly  = flag.Bool("smoke-only", false, "run only the smoke check; exit after")
		pkgArg     = flag.String("packages", "", "comma-separated package paths (default: built-in cluster)")
		offset     = flag.Int("offset", 0, "skip the first N mutation sites (for chunked runs)")
		limit      = flag.Int("limit", 0, "stop after N mutation sites (0 = no limit; for chunked runs)")
	)
	flag.Parse()

	srcRoot, err := findAggregaCliRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mutation-baseline: %v\n", err)
		os.Exit(2)
	}

	fmt.Fprintln(os.Stderr, "=== mutation-baseline: preparing scratch copy ===")
	scratchDir, err := setupScratch(srcRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mutation-baseline: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(scratchDir)
	installSignalHandler(scratchDir)
	fmt.Fprintf(os.Stderr, "scratch root: %s\n", scratchDir)

	runner := &goTestRunner{rootDir: scratchDir}

	// Smoke check is mandatory before any full run. Operates on the
	// scratch copy, not on srcRoot.
	fmt.Fprintln(os.Stderr, "=== mutation-baseline: smoke check ===")
	if err := runSmoke(scratchDir, runner); err != nil {
		fmt.Fprintf(os.Stderr, "smoke check FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "smoke check PASSED")

	if *smokeOnly {
		return
	}

	packages := defaultPackages
	if *pkgArg != "" {
		packages = strings.Split(*pkgArg, ",")
		for i := range packages {
			packages[i] = strings.TrimSpace(packages[i])
		}
	}

	fmt.Fprintf(os.Stderr, "\n=== mutation-baseline: full run (%d packages) ===\n", len(packages))
	muts, err := collectMutationsForPackages(scratchDir, packages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect mutations: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Found %d mutation site(s)\n", len(muts))

	// Apply --offset / --limit slicing so chunked runs can resume across
	// invocations without re-running prior chunks. Site order is
	// deterministic per the collectMutationsForPackages walk.
	if *offset > 0 || *limit > 0 {
		start := *offset
		if start > len(muts) {
			start = len(muts)
		}
		end := len(muts)
		if *limit > 0 {
			end = start + *limit
			if end > len(muts) {
				end = len(muts)
			}
		}
		muts = muts[start:end]
		fmt.Fprintf(os.Stderr, "Running slice [%d..%d) of %d total\n", start, end, len(muts)+start)
	}

	started := time.Now()
	progress := func(i, total int, m Mutation, o Outcome) {
		if i == 1 || i == total || i%50 == 0 {
			fmt.Fprintln(os.Stderr, progressLine(i, total, m, o, started))
		}
	}
	results, err := RunMutations(context.Background(), scratchDir, muts, runner, progress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run mutations: %v\n", err)
		os.Exit(1)
	}

	commit := commitHash(srcRoot)
	date := time.Now().Format("2006-01-02")
	w := os.Stdout
	if *outputPath != "" {
		f, err := os.Create(*outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create %s: %v\n", *outputPath, err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if err := WriteMarkdown(w, results, scratchDir, commit, date); err != nil {
		fmt.Fprintf(os.Stderr, "write markdown: %v\n", err)
		os.Exit(1)
	}

	// Summary on stderr regardless of report destination.
	var killed, survived, equivalent int
	for _, r := range results {
		switch r.Outcome {
		case OutcomeKilled:
			killed++
		case OutcomeSurvived:
			survived++
		case OutcomeEquivalent:
			equivalent++
		}
	}
	score := 0.0
	if killed+survived > 0 {
		score = float64(killed) / float64(killed+survived) * 100
	}
	fmt.Fprintf(os.Stderr, "\nFinal: %d killed, %d survived, %d equivalent — score %.1f%% (elapsed %s)\n",
		killed, survived, equivalent, score, time.Since(started).Round(time.Second))
}

// findAggregaCliRoot locates the aggrega-cli module root by walking
// upward from the binary's invocation directory until a go.mod file
// with the expected module path appears.
func findAggregaCliRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		gomod := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(gomod); err == nil {
			first := string(data)
			if i := strings.Index(first, "\n"); i > 0 {
				first = first[:i]
			}
			if strings.Contains(first, "aggrega-cli") {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("aggrega-cli go.mod not found from %s", cwd)
		}
		dir = parent
	}
}

// commitHash returns the short git commit hash at aggregaCliRoot, or
// "unknown" when git is unavailable or the tree is not a git checkout.
func commitHash(aggregaCliRoot string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = aggregaCliRoot
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
