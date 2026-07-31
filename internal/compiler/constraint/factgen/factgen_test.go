// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// inRepoTargets maps each target whose committed copy lives inside this module to
// the directory holding it, relative to this test package.
var inRepoTargets = map[string][]string{
	"views_gen.go":        {".."},
	"subjectkinds_gen.go": {".."},
}

// externalTargets names each target whose committed copy lives outside this
// module, so it is unreadable in a standalone checkout of this repository and
// cannot be drift-gated from here. Naming them is what lets the gate below
// account for every registered target: a new target that is neither read here nor
// named here fails the coverage check rather than being silently skipped.
var externalTargets = map[string]bool{
	"constraint-placement-table.md": true,
}

// TestRegenClean is the regeneration-drift gate for every target committed inside
// this module: re-rendering from the registry must byte-match the committed file.
// A failure means the registry (or the generator) changed without regenerating.
//
// This is what makes a placement change reviewable. Moving a fact to another
// rung is one line in the registry, and its consequences — a method moving
// between view interfaces, a row moving in the placement table, a subject kind
// appearing or disappearing — arrive as a diff rather than as a judgement a
// reader would have to reconstruct.
//
// A target committed outside this module is deliberately not read here, so this
// suite passes in a standalone checkout of this repository, where that file is
// absent. Such a target is drift-gated by the workspace build, which has every
// tree present and regenerates the target into a throwaway path to diff it.
func TestRegenClean(t *testing.T) {
	t.Parallel()
	for base, dir := range inRepoTargets {
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			got, err := generateFor(base)
			if err != nil {
				t.Fatalf("generate %s: %v", base, err)
			}
			path := filepath.Join(append(append([]string{}, dir...), base)...)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is stale: generator output differs from the committed file.\n"+
					"Run `go generate ./internal/compiler/constraint/factgen` and commit the result.", base)
			}
		})
	}
}

// TestRegenGateCoversEveryTarget closes the hole narrowing the gate above opened:
// once the gate walks a hand-listed subset rather than the target registry, a
// target added to the registry and forgotten here would be drift-gated by nothing
// and no test would say so. Every registered target must be accounted for as
// either read in-repo or gated by the workspace build, and the two sets must not
// overlap or claim a target that is not registered.
func TestRegenGateCoversEveryTarget(t *testing.T) {
	t.Parallel()
	for base := range targets {
		_, inRepo := inRepoTargets[base]
		if !inRepo && !externalTargets[base] {
			t.Errorf("target %q is registered but neither drift-gated in-repo nor named as "+
				"gated by the workspace build, so nothing checks it for staleness", base)
		}
		if inRepo && externalTargets[base] {
			t.Errorf("target %q is claimed both in-repo and external", base)
		}
	}
	for base := range inRepoTargets {
		if _, ok := targets[base]; !ok {
			t.Errorf("%q has a committed in-repo path but is not a registered target", base)
		}
	}
	for base := range externalTargets {
		if _, ok := targets[base]; !ok {
			t.Errorf("%q is named as externally gated but is not a registered target", base)
		}
	}
}

// TestLadderDerivesLinearChain pins the two properties the emitters rely on and
// the generator refuses to proceed without: the declared rung relation is a
// single chain, and every rung on it is occupied.
//
// The refusal itself is the enforcement — generation halts, so no ladder with an
// unordered pair in it can be emitted or committed. This test states the
// satisfied case, so a registry edit that breaks it fails here as well as at
// generation.
func TestLadderDerivesLinearChain(t *testing.T) {
	t.Parallel()
	l, err := deriveLadder()
	if err != nil {
		t.Fatalf("the rung chain does not derive: %v", err)
	}
	if len(l.order) != len(rungs) {
		t.Fatalf("the chain holds %d rungs, %d are declared", len(l.order), len(rungs))
	}
	for i, r := range l.order {
		if i == 0 {
			if r.Extends != "" {
				t.Errorf("the chain's root %q extends %q", r.Name, r.Extends)
			}
		} else if r.Extends != l.order[i-1].Name {
			t.Errorf("rung %q extends %q but follows %q in the chain",
				r.Name, r.Extends, l.order[i-1].Name)
		}
		if len(l.facts[r.Name]) == 0 {
			t.Errorf("rung %q is on the chain with no fact landing there", r.Name)
		}
	}
}

// TestSubjectKindsPartitionTheFacts checks the arithmetic that catches a fact
// dropped or double-counted while deriving the kind set: the per-kind tallies
// must sum to the number of declared facts.
func TestSubjectKindsPartitionTheFacts(t *testing.T) {
	t.Parallel()
	kinds, err := deriveKinds()
	if err != nil {
		t.Fatalf("the subject kinds do not derive: %v", err)
	}
	total := 0
	for _, k := range kinds {
		if len(k.Coordinates) == 0 {
			t.Errorf("kind %q has no coordinates", k.Display())
		}
		total += len(k.Facts)
	}
	if total != len(facts) {
		t.Errorf("the per-kind tallies sum to %d, %d facts are declared", total, len(facts))
	}
	t.Logf("%d facts over %d rungs, keyed by %d subject kinds", len(facts), len(rungs), len(kinds))
}

// TestTupleSharesAreReported checks that the near-duplicate print reports what it
// is given rather than reporting nothing.
//
// The calibration matters more than the assertion. An empty share list is the
// expected result for a registry with no near-duplicates, and an empty list is
// indistinguishable from a broken detector unless the detector is shown to fire.
// So this feeds it a pair sharing a rung, a carrier and a subject, and requires
// the pair back.
func TestTupleSharesAreReported(t *testing.T) {
	t.Parallel()
	for _, s := range deriveTupleShares(facts) {
		t.Logf("shared tuple (%s, %s, %s): %v", s.Rung, s.SourceField, s.Subject, s.Facts)
	}

	probe := facts[0]
	probe.Name = probe.Name + "-probe"
	probed := append(append([]factDecl(nil), facts...), probe)

	var found bool
	for _, s := range deriveTupleShares(probed) {
		if len(s.Facts) == 2 && s.Facts[0] == facts[0].Name && s.Facts[1] == probe.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("two facts sharing a rung, a source carrier and a subject were not reported as sharing "+
			"a tuple; the near-duplicate print is not firing, so an empty share list would mean nothing.\n"+
			"got: %+v", deriveTupleShares(probed))
	}
}
