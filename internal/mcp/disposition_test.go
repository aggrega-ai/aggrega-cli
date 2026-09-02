// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestEveryOperationIsDisposedOnTheToolProtocol is the tool protocol's half of
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
func TestEveryOperationIsDisposedOnTheToolProtocol(t *testing.T) {
	bound := opsurface.BoundStatement(opsurface.SurfaceMCP)
	t.Log(bound)

	if findings := opsurface.CheckDispositions(opsurface.SurfaceMCP, Dispositions()); len(findings) != 0 {
		t.Fatalf("%d of %d operations are not accounted for on the tool protocol:\n  %s\n%s",
			len(findings), len(opsurface.Operations()), strings.Join(findings, "\n  "), bound)
	}
}

// TestEveryStaticToolBelongsToAnOperation is the direction the disposition walk
// structurally cannot see, for the half of the tool surface this package
// declares itself: a curated or long-tail tool added without a matching
// operation leaves nothing missing from any disposition table.
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
	for _, d := range Dispositions() {
		for _, n := range d.Names {
			claimed[n] = true
		}
	}
	for _, name := range static {
		if !claimed[name] {
			t.Errorf("the tool protocol serves %q, which no operation claims. Add the capability to the "+
				"operation set and dispose of it on every surface, or name it in the disposition of the "+
				"operation it already implements.", name)
		}
	}
}

// TestTheCommandBackedDispositionsAreDerivedNotRestated pins that the tool name
// for a command-backed operation comes from the command path the command line
// declares, rather than from a second copy here.
//
// It is the guard on the derivation, not on the data: a rename that reached
// only one of the two surfaces is exactly what a restated name would hide.
func TestTheCommandBackedDispositionsAreDerivedNotRestated(t *testing.T) {
	mcpTable := Dispositions()

	for id, cli := range clisurface.Dispositions() {
		if !cli.IsExposed() {
			continue
		}
		for _, path := range cli.Names {
			exemption, exempt := CommandExempted(path)
			got, disposed := mcpTable[id]
			if !disposed {
				t.Errorf("the %q operation is a command at %q but the tool protocol disposes of it nowhere", id, path)
				continue
			}
			if exempt {
				if got.IsExposed() {
					continue // a curated tool answers it under a name of its own
				}
				if !strings.Contains(got.Reason, exemption.Reason) {
					t.Errorf("the %q operation's declined reason does not carry the exemption's own words:\n got %q\n want it to contain %q",
						id, got.Reason, exemption.Reason)
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

// TestEveryDeclinedToolProtocolOperationCarriesItsGround pins that a declined
// operation states a reason a reader can check rather than a placeholder.
func TestEveryDeclinedToolProtocolOperationCarriesItsGround(t *testing.T) {
	for id, d := range Dispositions() {
		if d.IsExposed() {
			continue
		}
		if len(d.Reason) < 40 {
			t.Errorf("the %q operation is declined with too little to check: %q", id, d.Reason)
		}
	}
}

// TestDispositionsReturnsACopy pins that a caller cannot reach into the table.
func TestDispositionsReturnsACopy(t *testing.T) {
	first := Dispositions()
	first["build"] = opsurface.NotApplicable("clobbered")

	if again := Dispositions(); !again["build"].IsExposed() {
		t.Fatal("mutating the returned map reached the table")
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
