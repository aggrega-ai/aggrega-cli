// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// runSmoke executes the two mandatory smoke-check mutations and confirms
// each yields the expected outcome. Returns nil when both match; returns
// a diagnostic error when either is mismatched so the operator can
// investigate before committing to a multi-hour full baseline run.
//
// rootDir is the scratch root produced by setupScratch; smoke targets
// live in the scratch copy's smokeprobe subpackage so they share the
// isolation guarantees of the rest of the run.
//
// Both targets are return-bool-flip mutations because that operator is
// the simplest to reason about and the smoke check's purpose is to
// validate the classification mechanism, not to exercise every operator.
func runSmoke(rootDir string, runner Runner) error {
	probeFile := filepath.Join(rootDir, "cmd", "mutation-baseline", "smokeprobe", "probe.go")
	uncovFile := filepath.Join(rootDir, "cmd", "mutation-baseline", "smokeprobe", "uncovered.go")

	// Read the two files, find a single return-bool mutation in each.
	probeMut, err := findSingleSmokeMutation(probeFile, "return_true_to_false")
	if err != nil {
		return fmt.Errorf("locate smoke target in probe.go: %w", err)
	}
	uncovMut, err := findSingleSmokeMutation(uncovFile, "return_false_to_true")
	if err != nil {
		return fmt.Errorf("locate smoke target in uncovered.go: %w", err)
	}

	// Target 1: TrueAlways. Expected outcome: killed.
	fmt.Printf("smoke target 1: %s %s:%d — expecting killed\n", probeMut.Operator, "smokeprobe/probe.go", probeMut.Position.Line)
	if outcome, err := runSingleMutation(context.Background(), rootDir, probeMut, runner); err != nil {
		return fmt.Errorf("smoke target 1: %w", err)
	} else if outcome != OutcomeKilled {
		return fmt.Errorf("smoke target 1: expected killed; got %s", outcome)
	}
	fmt.Println("smoke target 1: KILLED (as expected)")

	// Target 2: UncoveredFunc. Expected outcome: survived.
	fmt.Printf("smoke target 2: %s %s:%d — expecting survived\n", uncovMut.Operator, "smokeprobe/uncovered.go", uncovMut.Position.Line)
	if outcome, err := runSingleMutation(context.Background(), rootDir, uncovMut, runner); err != nil {
		return fmt.Errorf("smoke target 2: %w", err)
	} else if outcome != OutcomeSurvived {
		return fmt.Errorf("smoke target 2: expected survived; got %s", outcome)
	}
	fmt.Println("smoke target 2: SURVIVED (as expected)")

	return nil
}

// findSingleSmokeMutation walks the named file, finds every mutation
// site, and returns the first one matching wantOperator. The smoke
// probes have exactly one such site each by construction; if zero or
// multiple match, the smokeprobe package has drifted and the operator
// needs to investigate.
func findSingleSmokeMutation(file, wantOperator string) (Mutation, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return Mutation{}, fmt.Errorf("read %s: %w", file, err)
	}
	muts, err := FindMutations(file, src)
	if err != nil {
		return Mutation{}, err
	}
	var hits []Mutation
	for _, m := range muts {
		if m.Operator == wantOperator {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		return Mutation{}, fmt.Errorf("no %s mutation site in %s", wantOperator, file)
	}
	if len(hits) > 1 {
		return Mutation{}, fmt.Errorf("expected one %s site in %s; found %d", wantOperator, file, len(hits))
	}
	return hits[0], nil
}

// runSingleMutation applies one mutation, runs tests, restores the
// original, and returns the classified outcome.
func runSingleMutation(ctx context.Context, rootDir string, m Mutation, runner Runner) (Outcome, error) {
	results, err := RunMutations(ctx, rootDir, []Mutation{m}, runner, nil)
	if err != nil {
		return 0, err
	}
	if len(results) != 1 {
		return 0, fmt.Errorf("expected one result; got %d", len(results))
	}
	return results[0].Outcome, nil
}
