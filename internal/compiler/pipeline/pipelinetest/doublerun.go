// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipelinetest

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
)

// defaultDeterminismRuns is how many times AssertDeterministic compiles a
// workspace. Map-iteration nondeterminism surfaces probabilistically, so the
// count makes a single clean run improbable while keeping CI fast.
const defaultDeterminismRuns = 5

// AssertDeterministic compiles in defaultDeterminismRuns times and fails the
// test unless every run produces a byte-identical artifact map. It is the
// always-on double-run guard every core-provider end-to-end test calls: the
// whole-pipeline proof of the deterministic-output contract that GitOps diffs
// rely on.
func AssertDeterministic(t *testing.T, in Input) {
	t.Helper()
	AssertDeterministicN(t, in, defaultDeterminismRuns)
}

// AssertDeterministicN is AssertDeterministic with an explicit run count.
func AssertDeterministicN(t *testing.T, in Input, n int) {
	t.Helper()
	first := Build(t, in).Artifacts
	for i := 1; i < n; i++ {
		this := Build(t, in).Artifacts
		if err := compareArtifactMaps(first, this); err != nil {
			t.Fatalf("non-deterministic compilation: run %d differs from run 0: %v", i, err)
		}
	}
}

// compareArtifactMaps reports a non-nil error unless a and b carry the same
// file set with byte-identical contents. It is the comparison core
// AssertDeterministicN wraps, factored out so it can be tested directly: the
// guard's value is that it catches a difference, which is provable against two
// deliberately-differing maps without a nondeterministic pipeline.
func compareArtifactMaps(a, b map[string][]byte) error {
	if len(a) != len(b) {
		return fmt.Errorf("artifact-count mismatch: %d vs %d", len(a), len(b))
	}
	// Walk in sorted key order so the first reported difference is stable.
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		bv, ok := b[k]
		if !ok {
			return fmt.Errorf("artifact %q present in first map, missing in second", k)
		}
		if !bytes.Equal(a[k], bv) {
			return fmt.Errorf("artifact %q differs between runs", k)
		}
	}
	return nil
}
