// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import "testing"

// twoDirFixture writes the same Component and Feature identity into two ordered
// catalog directories with distinguishable bodies (no within-directory
// duplicates, so the eager strict load does not halt) and returns the dirs in
// precedence order.
func twoDirFixture(t *testing.T) (dirA, dirB string) {
	t.Helper()
	dirA, dirB = t.TempDir(), t.TempDir()
	writeFile(t, dirA+"/component.yaml", componentBody("dir-a"))
	writeFile(t, dirB+"/component.yaml", componentBody("dir-b"))
	writeFile(t, dirA+"/feature.yaml", featureBody("dir-a"))
	writeFile(t, dirB+"/feature.yaml", featureBody("dir-b"))
	return dirA, dirB
}

// eagerWinners resolves each identity through the eager whole-catalog merge.
func eagerWinners(t *testing.T, dirA, dirB string) (comp, feat string) {
	t.Helper()
	cat, err := LoadAll("", dirA, dirB)
	if err != nil {
		t.Fatalf("eager LoadAll: %v", err)
	}
	return cat.Components["core/thing:1.0.0"].Metadata.Description,
		cat.Features["core/thing-feature:1.0.0"].Metadata.Description
}

// lazyWinners resolves each identity through the lazy filesystem index.
func lazyWinners(t *testing.T, dirA, dirB string) (comp, feat string) {
	t.Helper()
	ll := NewLazyLookup("", "", []string{dirA, dirB}, nil)
	c, ok := ll.tier1ReadComponent("core/thing")
	if !ok {
		t.Fatalf("lazy tier1ReadComponent: not found")
	}
	f, ok := ll.tier1ReadFeature("core/thing-feature")
	if !ok {
		t.Fatalf("lazy tier1ReadFeature: not found")
	}
	return c.Metadata.Description, f.Metadata.Description
}

// TestPrecedenceParity_EagerEqualsLazy is the standing anti-drift guard: the same
// ordered-directory fixture, resolved through the eager whole-catalog merge and
// through the lazy filesystem index, must pick the same winner for every
// identity. Both fold cross-occurrence precedence through the shared foldFirstWin
// primitive, so this equivalence holds by construction; the test fails loudly if
// either strategy is re-homed onto a divergent precedence rule.
func TestPrecedenceParity_EagerEqualsLazy(t *testing.T) {
	t.Parallel()
	dirA, dirB := twoDirFixture(t)

	eComp, eFeat := eagerWinners(t, dirA, dirB)
	lComp, lFeat := lazyWinners(t, dirA, dirB)

	if eComp != "dir-a" {
		t.Errorf("eager Component winner: want dir-a, got %q", eComp)
	}
	if eComp != lComp {
		t.Errorf("Component winner diverges: eager=%q lazy=%q", eComp, lComp)
	}
	if eFeat != lFeat {
		t.Errorf("Feature winner diverges: eager=%q lazy=%q", eFeat, lFeat)
	}
}

// TestPrecedenceParity_Deterministic pins the byte-identical-output invariant:
// resolving the same fixture twice through each strategy yields the same winner
// both times, so the resolved set is a pure function of directory order.
func TestPrecedenceParity_Deterministic(t *testing.T) {
	t.Parallel()
	dirA, dirB := twoDirFixture(t)

	eComp1, eFeat1 := eagerWinners(t, dirA, dirB)
	eComp2, eFeat2 := eagerWinners(t, dirA, dirB)
	if eComp1 != eComp2 || eFeat1 != eFeat2 {
		t.Errorf("eager resolution not deterministic: run1=(%q,%q) run2=(%q,%q)", eComp1, eFeat1, eComp2, eFeat2)
	}

	lComp1, lFeat1 := lazyWinners(t, dirA, dirB)
	lComp2, lFeat2 := lazyWinners(t, dirA, dirB)
	if lComp1 != lComp2 || lFeat1 != lFeat2 {
		t.Errorf("lazy resolution not deterministic: run1=(%q,%q) run2=(%q,%q)", lComp1, lFeat1, lComp2, lFeat2)
	}
}
