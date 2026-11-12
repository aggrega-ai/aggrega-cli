// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package opsurface

import (
	"regexp"
	"strings"
	"testing"
)

// identifierForm is the descriptive register the operation identifiers use:
// lowercase words joined by single dashes.
var identifierForm = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestTheOperationSetIsWellFormed pins the properties the completeness checks
// rely on: a unique identifier per operation, in the descriptive register, and a
// summary a reader can judge an availability record against.
//
// The count is deliberately not asserted. A pinned total agrees with the set
// only until the set grows, and the growth is the thing every other check here
// exists to notice, so a total would have to be edited by whoever added an
// operation — at which point it records their intent rather than measuring
// anything.
func TestTheOperationSetIsWellFormed(t *testing.T) {
	ops := Operations()
	if len(ops) == 0 {
		t.Fatal("the operation set is empty")
	}

	seen := map[string]bool{}
	for _, op := range ops {
		if seen[op.ID] {
			t.Errorf("the %q operation is declared twice", op.ID)
		}
		seen[op.ID] = true

		if !identifierForm.MatchString(op.ID) {
			t.Errorf("the %q operation identifier is not lowercase words joined by dashes", op.ID)
		}
		if strings.TrimSpace(op.Summary) == "" {
			t.Errorf("the %q operation carries no summary", op.ID)
		}
		if !strings.HasSuffix(op.Summary, ".") {
			t.Errorf("the %q operation's summary is not a sentence: %q", op.ID, op.Summary)
		}
	}
}

// TestOperationsReturnsACopy pins that a caller cannot reach into the set.
func TestOperationsReturnsACopy(t *testing.T) {
	first := Operations()
	original := first[0]
	first[0] = Operation{ID: "clobbered", Summary: "."}

	if again := Operations(); again[0] != original {
		t.Fatalf("mutating the returned slice reached the set: got %+v, want %+v", again[0], original)
	}
}

// TestLookupResolvesAndRefuses pins both answers, because the stale-entry check
// reads the refusal.
func TestLookupResolvesAndRefuses(t *testing.T) {
	known := Operations()[0].ID
	if op, ok := Lookup(known); !ok || op.ID != known {
		t.Errorf("Lookup(%q) = %+v, %v; want the operation", known, op, ok)
	}
	if _, ok := Lookup("no-such-operation"); ok {
		t.Error("Lookup reported an operation that is not in the set")
	}
}

// TestCheckAvailabilityNamesEveryDisagreement drives each failure class the
// check reports, so no class is left as an assertion nobody has seen fire.
//
// The table is built against the real operation set rather than a synthetic one:
// the check reads that set directly, so a synthetic set would test a different
// function than the surfaces call.
//
// "Complete" here means complete for the surface — every operation disposed of
// except the ones the bound names as undecided on it. Disposing of those too
// would be over-disposal, which the check refuses as a stale bound row, so a
// table that ignored the bound would fail on the bound rather than on the class
// under test.
func TestCheckAvailabilityNamesEveryDisagreement(t *testing.T) {
	var ids []string
	for _, op := range operations {
		if isUndecided(undecidedPairs, SurfaceCLI, op.ID) {
			continue
		}
		ids = append(ids, op.ID)
	}
	if len(ids) < 2 {
		t.Fatalf("the bound leaves %d operation(s) disposable on %s; this test needs two", len(ids), SurfaceCLI)
	}
	first, second := ids[0], ids[1]

	complete := func() map[string]OperationAvailability {
		out := map[string]OperationAvailability{}
		for _, id := range ids {
			out[id] = Exposed(id)
		}
		return out
	}

	if findings := CheckAvailability(SurfaceCLI, complete()); len(findings) != 0 {
		t.Fatalf("a complete table produced findings: %v", findings)
	}

	tests := []struct {
		name    string
		mutate  func(map[string]OperationAvailability)
		wantHas []string
	}{
		{
			name:    "an operation the table says nothing about",
			mutate:  func(m map[string]OperationAvailability) { delete(m, first) },
			wantHas: []string{"declares nothing about", first, "Unavailable"},
		},
		{
			name:    "an entry naming an operation that does not exist",
			mutate:  func(m map[string]OperationAvailability) { m["retired-operation"] = Exposed("x") },
			wantHas: []string{"retired-operation", "is not an operation", "stale"},
		},
		{
			name: "an entry that is both exposed and declined",
			mutate: func(m map[string]OperationAvailability) {
				m[second] = OperationAvailability{Names: []string{"x"}, UnavailabilityReason: "y"}
			},
			wantHas: []string{second, "both exposed and unavailable"},
		},
		{
			name:    "an entry that is neither",
			mutate:  func(m map[string]OperationAvailability) { m[second] = OperationAvailability{} },
			wantHas: []string{second, "neither a name nor an unavailability reason"},
		},
		{
			name:    "an exposure under an empty name",
			mutate:  func(m map[string]OperationAvailability) { m[second] = Exposed("  ") },
			wantHas: []string{second, "an empty name"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			table := complete()
			tc.mutate(table)
			findings := CheckAvailability(SurfaceCLI, table)
			if len(findings) == 0 {
				t.Fatal("the check reported nothing")
			}
			joined := strings.Join(findings, "\n")
			for _, want := range tc.wantHas {
				if !strings.Contains(joined, want) {
					t.Errorf("the finding does not mention %q:\n%s", want, joined)
				}
			}
			if !strings.Contains(joined, string(SurfaceCLI)) {
				t.Errorf("the finding does not name the surface:\n%s", joined)
			}
		})
	}
}

// TestCheckMembersNoticesBothDirections pins the half of the check that sees
// growth: a member no operation claims, and a claimed name the surface does not
// carry.
func TestCheckMembersNoticesBothDirections(t *testing.T) {
	ids := IDs()
	table := map[string]OperationAvailability{}
	for _, id := range ids {
		table[id] = Exposed(id)
	}

	if findings := CheckMembers(SurfaceServe, table, ids); len(findings) != 0 {
		t.Fatalf("an exactly-covering surface produced findings: %v", findings)
	}

	t.Run("a member no operation claims", func(t *testing.T) {
		findings := CheckMembers(SurfaceServe, table, append(append([]string(nil), ids...), "aggrega/brandNew"))
		joined := strings.Join(findings, "\n")
		if !strings.Contains(joined, "aggrega/brandNew") || !strings.Contains(joined, "no operation claims") {
			t.Fatalf("the check did not name the uncovered member:\n%s", joined)
		}
	})

	t.Run("a claimed name the surface does not carry", func(t *testing.T) {
		findings := CheckMembers(SurfaceServe, table, ids[1:])
		joined := strings.Join(findings, "\n")
		if !strings.Contains(joined, ids[0]) || !strings.Contains(joined, "does not carry") {
			t.Fatalf("the check did not name the absent exposure:\n%s", joined)
		}
	})

	t.Run("a declined operation is not required to be carried", func(t *testing.T) {
		declined := map[string]OperationAvailability{}
		for _, id := range ids {
			declined[id] = Unavailable("stated on purpose")
		}
		if findings := CheckMembers(SurfaceMCP, declined, nil); len(findings) != 0 {
			t.Fatalf("declining every operation produced findings: %v", findings)
		}
	})
}
