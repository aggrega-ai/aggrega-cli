// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestEveryOperationIsDeclaredOnTheToolProtocol is the tool protocol's half of
// the operation-completeness guard: every operation the compiler exposes is
// either reachable under a named tool or declined with a reason, and no entry
// names an operation that no longer exists.
//
// The count it walks is derived from the operation set at run time rather than
// written down, so the guard cannot rot into agreement with a stale number.
//
// Whether an exposed tool name is a tool a session actually serves is checked
// where the command tree is available to be walked, which is not here: the
// generated tools exist only for a session that was handed a command surface.
// The bound's reach is reported alongside the verdict, because a check that
// goes quiet about what it did not assert is indistinguishable from one that
// asserted everything. It travels on two channels and neither substitutes for
// the other: it is folded into the failure text, which is what the reader of a
// red sees, and it is logged, which the framework shows under -v and whenever
// anything in this package fails. A package that passes without -v prints
// nothing on any channel, so the logged copy is the weaker of the two.
func TestEveryOperationIsDeclaredOnTheToolProtocol(t *testing.T) {
	bound := opsurface.BoundStatement(opsurface.SurfaceMCP)
	t.Log(bound)

	if findings := opsurface.CheckAvailability(opsurface.SurfaceMCP, OperationAvailability()); len(findings) != 0 {
		t.Fatalf("%d of %d operations are not accounted for on the tool protocol:\n  %s\n%s",
			len(findings), len(opsurface.Operations()), strings.Join(findings, "\n  "), bound)
	}
}

// TestEveryStaticToolBelongsToAnOperation is the direction the availability walk
// structurally cannot see, for the half of the tool surface this package
// declares itself: a curated or long-tail tool added without a matching
// operation leaves nothing missing from any availability table.
//
// The generated command tools are excluded because they exist only for a
// session holding a command surface; they are checked against the live command
// tree where that tree is built.
func TestEveryStaticToolBelongsToAnOperation(t *testing.T) {
	static := StaticToolNames()
	if len(static) == 0 {
		t.Fatal("no static tools")
	}

	claimed := map[string]bool{}
	for _, a := range OperationAvailability() {
		for _, n := range a.Names {
			claimed[n] = true
		}
	}
	for _, name := range static {
		if !claimed[name] {
			t.Errorf("the tool protocol serves %q, which no operation claims. Add the capability to the "+
				"operation set and declare its availability on every surface, or name it in the availability "+
				"record of the operation it already implements.", name)
		}
	}
}

// TestEveryStaticToolIsNamedFromTheTable pins the registration binding: every
// tool in either registry carries the name the availability table gives for the
// operation it answers, and none carries a name of its own.
//
// It is the assertion the definitions can no longer make for themselves. A spec
// stopped naming itself when the registries began naming it from the table, so
// what stops a tool being advertised under an undeclared name is the registry
// entry, and this is what checks that it does.
func TestEveryStaticToolIsNamedFromTheTable(t *testing.T) {
	declared := map[string]string{}
	for id, names := range curatedToolNames {
		for _, n := range names {
			declared[n] = id
		}
	}
	for _, name := range StaticToolNames() {
		if _, ok := declared[name]; !ok {
			t.Errorf("the tool protocol serves %q, which the curated table gives for no operation: a tool's "+
				"name comes from that table, so this one was named somewhere else", name)
		}
	}
}

// TestTheCommandBackedRecordsAreDerivedNotRestated pins that the tool name for a
// command-backed operation comes from the command path the command line
// declares, rather than from a second copy here.
//
// It is the guard on the derivation, not on the data: a rename that reached
// only one of the two surfaces is exactly what a restated name would hide.
func TestTheCommandBackedRecordsAreDerivedNotRestated(t *testing.T) {
	mcpTable := OperationAvailability()

	for id, cli := range clisurface.OperationAvailability() {
		if !cli.IsExposed() {
			continue
		}
		for _, path := range cli.Names {
			exemption, exempt := CommandExempted(path)
			got, declared := mcpTable[id]
			if !declared {
				t.Errorf("the %q operation is a command at %q but the tool protocol declares its availability "+
					"nowhere", id, path)
				continue
			}
			if exempt {
				if got.IsExposed() {
					continue // a curated tool answers it under a name of its own
				}
				if !strings.Contains(got.UnavailabilityReason, exemption.Reason) {
					t.Errorf("the %q operation's reason does not carry the exemption's own words:\n got %q\n want it to contain %q",
						id, got.UnavailabilityReason, exemption.Reason)
				}
				continue
			}
			want := commandToolName(path)
			if !containsName(got.Names, want) {
				t.Errorf("the %q operation is a command at %q, so the tool protocol should expose it as %q; got %v",
					id, path, want, got.Names)
			}
		}
	}
}

// TestEveryUnavailableToolProtocolOperationCarriesItsReason pins that a declined
// operation states a reason a reader can check rather than a placeholder.
func TestEveryUnavailableToolProtocolOperationCarriesItsReason(t *testing.T) {
	for id, a := range OperationAvailability() {
		if a.IsExposed() {
			continue
		}
		if len(a.UnavailabilityReason) < 40 {
			t.Errorf("the %q operation is declined with too little to check: %q", id, a.UnavailabilityReason)
		}
	}
}

// TestOperationAvailabilityReturnsACopy pins that a caller cannot reach into the
// table.
func TestOperationAvailabilityReturnsACopy(t *testing.T) {
	first := OperationAvailability()
	first["build"] = opsurface.Unavailable("clobbered")

	if again := OperationAvailability(); !again["build"].IsExposed() {
		t.Fatal("mutating the returned map reached the table")
	}
}

// TestCuratedRefusesWhatTheTableDoesNotAccountFor is the standing proof that a
// tool cannot reach a registry without the availability table naming it and
// giving it the name it is advertised under.
//
// The first row is the positive control, and it earns its place: the other rows
// assert panics, and a constructor broken for an unrelated reason panics on
// everything. Without a correct registration that still succeeds — and whose
// resulting name is checked — a uniformly broken constructor would read as a
// uniformly effective one.
func TestCuratedRefusesWhatTheTableDoesNotAccountFor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		id       string
		wantName string
		refuses  string
	}{
		{
			name:     "an operation the table gives one curated name for",
			id:       "inspect",
			wantName: "describe",
		},
		{
			name:    "an identifier that is not an operation",
			id:      "not-an-operation",
			refuses: "is not an operation",
		},
		{
			name:    "an operation with no curated name",
			id:      anOperationWithNoCuratedName(t),
			refuses: "declares no curated name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refusal string
			var got toolSpec
			func() {
				defer func() {
					if r := recover(); r != nil {
						refusal, _ = r.(string)
					}
				}()
				got = curated(tc.id, toolSpec{description: "probe"})
			}()

			switch {
			case tc.refuses == "" && refusal != "":
				t.Fatalf("a tool the table accounts for was refused: %s", refusal)
			case tc.refuses != "" && refusal == "":
				t.Fatalf("a tool the table does not account for was registered as %q", got.name)
			case tc.refuses != "" && !strings.Contains(refusal, tc.refuses):
				t.Fatalf("the refusal does not name what was wrong: want %q in %q", tc.refuses, refusal)
			case tc.refuses == "" && got.name != tc.wantName:
				t.Fatalf("the tool was named %q, and the table gives %q", got.name, tc.wantName)
			}
		})
	}
}

// TestCuratedOverwritesANameASpecCarries pins the half of the binding that a
// missing name does not prove: a spec that names itself does not keep that name.
//
// Removing the name from every definition is what makes the table the only place
// a name is written today; this is what keeps it so once somebody adds one back.
func TestCuratedOverwritesANameASpecCarries(t *testing.T) {
	got := curated("lint", toolSpec{name: "not-from-the-table", description: "probe"})
	if got.name != "lint" {
		t.Fatalf("a spec's own name survived registration: the tool is named %q, and the table gives %q",
			got.name, "lint")
	}
}

// anOperationWithNoCuratedName returns an operation this protocol declares no
// curated tool name for, walking the operation set in its own order so the answer
// is deterministic.
//
// It is derived rather than named because naming one is how this case rots: the
// identifier that used to stand for "no curated tool" is exactly what stops
// standing for it the day a curated tool is written for that operation, and the
// symptom is this assertion failing for a reason that has nothing to do with what
// it tests. It fails loudly when every operation has a curated name, because then
// the refusal under test is unreachable and the case would be asserting nothing.
func anOperationWithNoCuratedName(t *testing.T) string {
	t.Helper()
	for _, op := range opsurface.Operations() {
		if _, declared := curatedToolNames[op.ID]; !declared {
			return op.ID
		}
	}
	t.Fatal("every operation has a curated tool name, so the refusal this case drives is unreachable")
	return ""
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
