// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package clisurface

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestEveryOperationIsDisposedOnTheCommandLine is the command line's half of
// the operation-completeness guard: every operation the compiler exposes is
// either reachable at a named command or declined with a reason, and no entry
// names an operation that no longer exists.
//
// The count it walks is derived from the operation set at run time rather than
// written down, so the guard cannot rot into agreement with a stale number.
//
// Whether an exposed command path is a command that actually exists is checked
// where the command tree is available to be walked, which is not here: this
// package projects a tree it is handed and builds none.
// The bound's reach is reported alongside the verdict, because a check that
// goes quiet about what it did not assert is indistinguishable from one that
// asserted everything. It travels on two channels and neither substitutes for
// the other: it is folded into the failure text, which is what the reader of a
// red sees, and it is logged, which the framework shows under -v and whenever
// anything in this package fails. A package that passes without -v prints
// nothing on any channel, so the logged copy is the weaker of the two.
func TestEveryOperationIsDisposedOnTheCommandLine(t *testing.T) {
	table := Dispositions()
	bound := opsurface.BoundStatement(opsurface.SurfaceCLI)
	t.Log(bound)

	if findings := opsurface.CheckDispositions(opsurface.SurfaceCLI, table); len(findings) != 0 {
		t.Fatalf("%d of %d operations are not accounted for on the command line:\n  %s\n%s",
			len(findings), len(opsurface.Operations()), strings.Join(findings, "\n  "), bound)
	}
}

// TestEveryDeclinedCommandLineOperationCarriesItsGround pins that a declined
// operation states a reason a reader can check, rather than a placeholder. The
// grounds are shared constants, so this asserts the shape they all satisfy: a
// full clause, not a word.
func TestEveryDeclinedCommandLineOperationCarriesItsGround(t *testing.T) {
	for id, d := range Dispositions() {
		if d.IsExposed() {
			continue
		}
		if len(d.Reason) < 40 {
			t.Errorf("the %q operation is declined with too little to check: %q", id, d.Reason)
		}
		if strings.HasSuffix(strings.TrimSpace(d.Reason), ".") {
			t.Errorf("the %q operation's ground reads as a sentence rather than a clause the finding embeds: %q",
				id, d.Reason)
		}
	}
}

// TestExposedCommandLineOperationsNameAnInvocationPath pins that an exposure
// names a command path — the identifier the catalog reports — rather than a bare
// command name, because the member check compares against catalog paths.
func TestExposedCommandLineOperationsNameAnInvocationPath(t *testing.T) {
	for id, d := range Dispositions() {
		if !d.IsExposed() {
			continue
		}
		for _, name := range d.Names {
			if !strings.HasPrefix(name, "aggrega ") {
				t.Errorf("the %q operation is exposed as %q, which is not a command invocation path", id, name)
			}
		}
	}
}

// TestDispositionsReturnsACopy pins that a caller cannot reach into the table.
func TestDispositionsReturnsACopy(t *testing.T) {
	first := Dispositions()
	first["build"] = opsurface.NotApplicable("clobbered")

	again := Dispositions()
	if !again["build"].IsExposed() {
		t.Fatal("mutating the returned map reached the table")
	}
}
