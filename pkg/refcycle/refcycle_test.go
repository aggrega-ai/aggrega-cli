// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package refcycle

import (
	"reflect"
	"testing"
)

// TestCheckDetectsRepeatedKey is the load-bearing assertion for the cycle
// detector: a key already in flight is reported as a cycle, with the chain
// running oldest-in-flight → … → the repeated key. This is the safe,
// non-recursive proof of the primitive's logic — a Check that stops detecting
// repeats fails here instantly, with no recursion and no runaway (the recursive
// consumers depend on Check to terminate; that termination must be proven HERE,
// at the boolean level, never by disabling Check against a cyclic fixture).
func TestCheckDetectsRepeatedKey(t *testing.T) {
	t.Parallel()
	chain, cyclic := Check([]string{"A", "B"}, "A")
	if !cyclic {
		t.Fatal("expected a cycle when the key is already in flight; got none")
	}
	want := []string{"A", "B", "A"}
	if !reflect.DeepEqual(chain, want) {
		t.Errorf("chain = %v; want %v (oldest in-flight -> ... -> repeated key)", chain, want)
	}
}

// TestCheckDetectsImmediateSelfCycle covers the single-element self-reference.
func TestCheckDetectsImmediateSelfCycle(t *testing.T) {
	t.Parallel()
	chain, cyclic := Check([]string{"A"}, "A")
	if !cyclic {
		t.Fatal("expected a self-cycle; got none")
	}
	if want := []string{"A", "A"}; !reflect.DeepEqual(chain, want) {
		t.Errorf("chain = %v; want %v", chain, want)
	}
}

// TestCheckNoCycle confirms a fresh key is not a cycle and returns no chain —
// the case that lets recursion descend one more level.
func TestCheckNoCycle(t *testing.T) {
	t.Parallel()
	chain, cyclic := Check([]string{"A", "B"}, "C")
	if cyclic {
		t.Errorf("expected no cycle for a fresh key; got cyclic with chain %v", chain)
	}
	if chain != nil {
		t.Errorf("chain = %v; want nil for a non-cycle", chain)
	}
	chain, cyclic = Check(nil, "A")
	if cyclic || chain != nil {
		t.Errorf("empty stack: cyclic=%v chain=%v; want false, nil", cyclic, chain)
	}
}

// TestPushDoesNotMutateInput pins the immutability contract: Push returns a new
// slice with the key appended and leaves the caller's stack untouched, so one
// recursive branch's descent cannot corrupt a sibling branch's in-flight set.
func TestPushDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	base := []string{"A", "B"}
	got := Push(base, "C")
	if want := []string{"A", "B", "C"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Push result = %v; want %v", got, want)
	}
	if !reflect.DeepEqual(base, []string{"A", "B"}) {
		t.Errorf("Push mutated its input: base = %v; want [A B]", base)
	}
	// The returned slice must be independent of the input's backing array.
	got[0] = "X"
	if base[0] != "A" {
		t.Error("Push result shares backing array with input; mutation leaked")
	}
}
