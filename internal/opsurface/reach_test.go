// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package opsurface

import (
	"strings"
	"testing"
)

// TestEveryOperationDeclaresItsReach is the completeness assertion over the real
// set, driven through the exported check so that what runs here is what any
// other caller would run.
//
// Every finding is reported rather than the first, because an operation added
// without a class is usually added alongside others and a check that stopped at
// one would be run once per entry.
func TestEveryOperationDeclaresItsReach(t *testing.T) {
	findings := CheckReach()
	for _, f := range findings {
		t.Error(f)
	}
	if len(findings) != 0 {
		t.Fatalf("%d of %d operation(s) do not declare a class from the closed set", len(findings), len(operations))
	}
}

// TestTheReachVocabularyIsWellFormed pins the properties the check rests on: the
// classes are distinct, they are in the descriptive register the identifiers
// use, and the zero value is not one of them.
//
// The last is the load-bearing one. An entry added without a class carries the
// empty string, so the check can only report it while no constant spells that —
// a vocabulary that admitted it would turn every forgotten declaration into a
// silent one.
func TestTheReachVocabularyIsWellFormed(t *testing.T) {
	if len(reachClasses) == 0 {
		t.Fatal("the reach vocabulary is empty")
	}

	seen := map[Reach]bool{}
	for _, c := range reachClasses {
		if seen[c] {
			t.Errorf("the %q reach class is declared twice", string(c))
		}
		seen[c] = true
		if !identifierForm.MatchString(string(c)) {
			t.Errorf("the %q reach class is not lowercase words joined by dashes", string(c))
		}
	}

	if isReachClass("") {
		t.Error("the empty value is a reach class, so an operation added without a declaration " +
			"would pass the check rather than be reported by it")
	}
}

// TestCheckReachNamesEveryDisagreement drives each failure class the check
// reports, against a set built for it.
//
// The sets are synthetic here, which is the opposite of the choice the
// availability check's test makes, and for the opposite reason: that check is
// called by the surfaces over the real set, so a synthetic one would test a
// different function than they call, while this check's whole assertion over the
// real set is that it finds NOTHING. A failure class can only be watched fire
// against a set that carries one.
func TestCheckReachNamesEveryDisagreement(t *testing.T) {
	wellFormed := []Operation{
		{"first-capability", ReachNone, "A capability that leaves nothing behind."},
		{"second-capability", ReachRegistry, "A capability that changes what a registry holds."},
	}

	if findings := checkReach(wellFormed); len(findings) != 0 {
		t.Fatalf("a set declaring a class for every operation produced findings: %v", findings)
	}

	tests := []struct {
		name     string
		mutate   func([]Operation)
		offender string
		innocent string
		wantHas  []string
	}{
		{
			name:     "an operation declaring no class",
			mutate:   func(ops []Operation) { ops[1].Reach = "" },
			offender: "second-capability",
			innocent: "first-capability",
			wantHas:  []string{"declares no reach class", `"workspace"`},
		},
		{
			name:     "an operation declaring a value outside the closed set",
			mutate:   func(ops []Operation) { ops[0].Reach = Reach("everywhere") },
			offender: "first-capability",
			innocent: "second-capability",
			wantHas:  []string{"everywhere", "is not a class", `"machine"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops := append([]Operation(nil), wellFormed...)
			tc.mutate(ops)
			findings := checkReach(ops)
			if len(findings) != 1 {
				t.Fatalf("the check reported %d finding(s) for one malformed entry: %v", len(findings), findings)
			}
			joined := strings.Join(findings, "\n")
			if !strings.Contains(joined, tc.offender) {
				t.Errorf("the finding does not name the operation %q:\n%s", tc.offender, joined)
			}
			for _, want := range tc.wantHas {
				if !strings.Contains(joined, want) {
					t.Errorf("the finding does not mention %q:\n%s", want, joined)
				}
			}
			// The entry that declares a class must not be named, or a reader
			// repairing the set would be sent to one that is already right.
			if strings.Contains(joined, tc.innocent) {
				t.Errorf("the finding names %q, which declares a class:\n%s", tc.innocent, joined)
			}
		})
	}
}
