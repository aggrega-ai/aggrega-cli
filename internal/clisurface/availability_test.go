// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package clisurface

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// checkCommandReach reports every row of the declared command classes that does
// not belong there: one naming something that is not an operation, one naming an
// operation no command carries, one whose class the vocabulary does not hold, and
// one restating the class its operation already declares — a second declaration
// of a fact the operation set owns, which would agree with it until one of the
// two was edited.
//
// The rows and the availability table are handed in rather than read, so each
// way a row can be wrong is driven against rows built for it below.
func checkCommandReach(rows map[string]opsurface.Reach, table map[string]opsurface.OperationAvailability) []string {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var findings []string
	for _, id := range ids {
		class := rows[id]
		op, known := opsurface.Lookup(id)
		switch {
		case !known:
			findings = append(findings, fmt.Sprintf("the command class for %q names something that is not an operation", id))
		case !table[id].IsExposed():
			findings = append(findings, fmt.Sprintf("the command class for %q names an operation no command carries", id))
		case !slices.Contains(opsurface.Classes(), class):
			findings = append(findings, fmt.Sprintf("the command class for %q is %q, which is not a class", id, class))
		case class == op.Reach:
			findings = append(findings, fmt.Sprintf("the command class for %q restates the class its operation "+
				"declares, %q", id, class))
		}
	}
	return findings
}

// TestEveryDeclaredCommandClassBelongsBesideTheTable holds the command line's
// declared command classes to the records they sit beside: each names an
// operation a command carries, with a class the vocabulary holds and other than
// the operation's own.
//
// The real rows are checked here; which generated tool each one makes writing is
// checked where the real command tree is, against what each command does.
func TestEveryDeclaredCommandClassBelongsBesideTheTable(t *testing.T) {
	if len(commandReach) == 0 {
		t.Fatal("no command declares a class of its own, so the check below reads nothing")
	}
	if findings := checkCommandReach(commandReach, OperationAvailability()); len(findings) != 0 {
		t.Fatalf("%d declared command class(es) do not belong:\n  %s", len(findings), strings.Join(findings, "\n  "))
	}
}

// TestTheCommandClassCheckRefusesEachRowThatDoesNotBelong drives the check above
// with one malformed row per way a row can be wrong, beside a well-formed one, so
// its green over the real rows is a check that could have failed.
func TestTheCommandClassCheckRefusesEachRowThatDoesNotBelong(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id      string
		class   opsurface.Reach
		refuses string
	}{
		{name: "a command that saves what its operation only renders", id: "build",
			class: opsurface.ReachWorkspace, refuses: ""},
		{name: "an identifier that is not an operation", id: "not-an-operation",
			class: opsurface.ReachWorkspace, refuses: "is not an operation"},
		{name: "an operation no command carries", id: "hover-at-position",
			class: opsurface.ReachWorkspace, refuses: "no command carries"},
		{name: "a value the vocabulary does not hold", id: "build",
			class: opsurface.Reach("everywhere"), refuses: "is not a class"},
		{name: "the class the operation already declares", id: "validate",
			class: opsurface.ReachNone, refuses: "restates the class"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := checkCommandReach(map[string]opsurface.Reach{tc.id: tc.class}, OperationAvailability())
			switch {
			case tc.refuses == "" && len(findings) != 0:
				t.Fatalf("a row that belongs was refused: %v", findings)
			case tc.refuses != "" && len(findings) != 1:
				t.Fatalf("want one finding naming %q, got %v", tc.refuses, findings)
			case tc.refuses != "" && !strings.Contains(findings[0], tc.refuses):
				t.Fatalf("the finding does not name what was wrong: want %q in %q", tc.refuses, findings[0])
			}
		})
	}
}

// checkWorkspaceScopes reports every way the declared workspace scopes and the
// operation set disagree: an operation with no scope declared for it, a scope
// declared for an identifier that is not an operation, and a scope the vocabulary
// does not hold. Each finding names the identifier, so a missing row is named
// rather than counted.
//
// The rows are handed in rather than read, so each way a row can be wrong is
// driven against rows built for it below.
func checkWorkspaceScopes(rows map[string]WorkspaceScope) []string {
	var findings []string
	for _, op := range opsurface.Operations() {
		if _, declared := rows[op.ID]; !declared {
			findings = append(findings, fmt.Sprintf("the %q operation declares no workspace scope", op.ID))
		}
	}
	for id, scope := range rows {
		_, known := opsurface.Lookup(id)
		switch {
		case !known:
			findings = append(findings, fmt.Sprintf("a workspace scope is declared for %q, which is not an operation", id))
		case scope != WorkspaceScoped && scope != MachineScoped:
			findings = append(findings, fmt.Sprintf("the %q operation declares the workspace scope %q, which is not a scope", id, scope))
		}
	}
	sort.Strings(findings)
	return findings
}

// TestEveryOperationDeclaresItsWorkspaceScope holds the declared workspace scopes
// to the operation set: every operation declares whether its result depends on
// the workspace root, the ones no command carries included, and no declaration
// names an identifier that is not an operation.
//
// Whether every runnable command in the live tree resolves to a declaration is
// checked where the tree is built, which is not here.
func TestEveryOperationDeclaresItsWorkspaceScope(t *testing.T) {
	if len(operationWorkspaceScope) == 0 {
		t.Fatal("no operation declares a workspace scope, so the check below reads nothing")
	}
	if findings := checkWorkspaceScopes(operationWorkspaceScope); len(findings) != 0 {
		t.Fatalf("%d workspace scope finding(s):\n  %s", len(findings), strings.Join(findings, "\n  "))
	}
}

// TestTheWorkspaceScopeCheckRefusesEachRowThatDoesNotBelong drives the check
// above with rows built to be wrong in one way each, beside the real rows, so its
// green over the real rows is a check that could have failed.
func TestTheWorkspaceScopeCheckRefusesEachRowThatDoesNotBelong(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(rows map[string]WorkspaceScope)
		refuses string
	}{
		{name: "the real rows", edit: func(map[string]WorkspaceScope) {}, refuses: ""},
		{name: "an operation with no row",
			edit:    func(rows map[string]WorkspaceScope) { delete(rows, "validate") },
			refuses: `the "validate" operation declares no workspace scope`},
		{name: "an operation no command carries, with no row",
			edit:    func(rows map[string]WorkspaceScope) { delete(rows, "release-candidates") },
			refuses: `the "release-candidates" operation declares no workspace scope`},
		{name: "a row naming something that is not an operation",
			edit:    func(rows map[string]WorkspaceScope) { rows["not-an-operation"] = MachineScoped },
			refuses: `declared for "not-an-operation", which is not an operation`},
		{name: "a value the vocabulary does not hold",
			edit:    func(rows map[string]WorkspaceScope) { rows["build"] = WorkspaceScope("everywhere") },
			refuses: `declares the workspace scope "everywhere", which is not a scope`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make(map[string]WorkspaceScope, len(operationWorkspaceScope))
			for id, scope := range operationWorkspaceScope {
				rows[id] = scope
			}
			tc.edit(rows)
			findings := checkWorkspaceScopes(rows)
			switch {
			case tc.refuses == "" && len(findings) != 0:
				t.Fatalf("rows that belong were refused: %v", findings)
			case tc.refuses != "" && len(findings) != 1:
				t.Fatalf("want one finding carrying %q, got %v", tc.refuses, findings)
			case tc.refuses != "" && !strings.Contains(findings[0], tc.refuses):
				t.Fatalf("the finding does not name what was wrong: want %q in %q", tc.refuses, findings[0])
			}
		})
	}
}

// TestCommandWorkspaceScopeAnswersTheCarriedOperationsRow pins the command
// accessor against the declaration it reads through the operation a command
// carries: a command answers its operation's row, a path claimed by two
// operations answers the one it carries, and a path the table does not give
// answers false.
func TestCommandWorkspaceScopeAnswersTheCarriedOperationsRow(t *testing.T) {
	for _, tc := range []struct {
		path     string
		want     WorkspaceScope
		declared bool
	}{
		{path: "aggrega build", want: WorkspaceScoped, declared: true},
		{path: "aggrega version", want: MachineScoped, declared: true},
		{path: "aggrega config show", want: MachineScoped, declared: true},
		{path: "aggrega moo", declared: false},
	} {
		got, declared := CommandWorkspaceScope(tc.path)
		if got != tc.want || declared != tc.declared {
			t.Errorf("CommandWorkspaceScope(%q) = (%q, %v), want (%q, %v)", tc.path, got, declared, tc.want, tc.declared)
		}
	}
}

// TestACommandCarriesTheOperationItIsRatherThanOneItOffers pins the tie-break
// every reader of a command's operation depends on, and it exists because a
// command path is not claimed by exactly one operation.
//
// A capability several commands hand back under a flag is exposed at each of
// their paths. It is OFFERED by each and is the identity of none, so the record
// naming a path and no other wins. Resolving the tie by declaration order would
// work today and silently stop working the day the operation set is reordered.
//
// The rows are read from the availability records rather than listed, so this
// follows a rename instead of pinning one.
func TestACommandCarriesTheOperationItIsRatherThanOneItOffers(t *testing.T) {
	table := OperationAvailability()

	// Every path claimed by more than one record, with the record that names it
	// alone — the command the path IS.
	claims := map[string][]string{}
	soleClaimant := map[string]string{}
	for id, a := range table {
		if !a.IsExposed() {
			continue
		}
		for _, path := range a.Names {
			claims[path] = append(claims[path], id)
			if len(a.Names) == 1 {
				soleClaimant[path] = id
			}
		}
	}

	var contested int
	for path, ids := range claims {
		if len(ids) < 2 {
			continue
		}
		contested++
		want, ok := soleClaimant[path]
		if !ok {
			t.Errorf("%q is claimed by %v and by no record naming it alone, so which operation the command "+
				"carries is undecidable from the table", path, ids)
			continue
		}
		if got := CommandOperation(path); got != want {
			t.Errorf("the command at %q carries %q; it was resolved to %q (claimants: %v)", path, want, got, ids)
		}
	}
	if contested == 0 {
		t.Skip("no command path is claimed by more than one operation, so the tie-break has nothing to resolve")
	}

	// The uncontested direction still has to hold, or the tie-break has broken
	// the ordinary case while fixing the contested one.
	for path, id := range soleClaimant {
		if len(claims[path]) != 1 {
			continue
		}
		if got := CommandOperation(path); got != id {
			t.Errorf("the command at %q is the only claimant of %q, and it was resolved to %q", path, id, got)
		}
	}
}

// TestTheNoWorkspaceWarningNamesTheFolderAndTheSeedingCommand holds the shared
// sentence to the words every surface says, written out rather than composed, so
// a change to the sentence or to the seeding command's path shows here.
func TestTheNoWorkspaceWarningNamesTheFolderAndTheSeedingCommand(t *testing.T) {
	const want = "/work/here is not inside an Aggrega workspace. To make it one, run: aggrega config init --workspace"
	if got := NoWorkspaceWarning("/work/here"); got != want {
		t.Errorf("NoWorkspaceWarning = %q, want %q", got, want)
	}
}

// TestEveryOperationIsDeclaredOnTheCommandLine is the command line's half of
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
func TestEveryOperationIsDeclaredOnTheCommandLine(t *testing.T) {
	table := OperationAvailability()
	bound := opsurface.BoundStatement(opsurface.SurfaceCLI)
	t.Log(bound)

	if findings := opsurface.CheckAvailability(opsurface.SurfaceCLI, table); len(findings) != 0 {
		t.Fatalf("%d of %d operations are not accounted for on the command line:\n  %s\n%s",
			len(findings), len(opsurface.Operations()), strings.Join(findings, "\n  "), bound)
	}
}

// TestEveryUnavailableCommandLineOperationCarriesItsReason pins that a declined
// operation states a reason a reader can check, rather than a placeholder. The
// reasons are shared constants, so this asserts the shape they all satisfy: a
// full clause, not a word.
func TestEveryUnavailableCommandLineOperationCarriesItsReason(t *testing.T) {
	for id, a := range OperationAvailability() {
		if a.IsExposed() {
			continue
		}
		if len(a.UnavailabilityReason) < 40 {
			t.Errorf("the %q operation is declined with too little to check: %q", id, a.UnavailabilityReason)
		}
		if strings.HasSuffix(strings.TrimSpace(a.UnavailabilityReason), ".") {
			t.Errorf("the %q operation's reason reads as a sentence rather than a clause the finding embeds: %q",
				id, a.UnavailabilityReason)
		}
	}
}

// TestExposedCommandLineOperationsNameAnInvocationPath pins that an exposure
// names a command path — the identifier the catalog reports — rather than a bare
// command name, because the member check compares against catalog paths and the
// registrar compares against the path cobra computes.
func TestExposedCommandLineOperationsNameAnInvocationPath(t *testing.T) {
	for id, a := range OperationAvailability() {
		if !a.IsExposed() {
			continue
		}
		for _, name := range a.Names {
			if !strings.HasPrefix(name, "aggrega ") {
				t.Errorf("the %q operation is exposed as %q, which is not a command invocation path", id, name)
			}
		}
	}
}

// TestOperationAvailabilityReturnsACopy pins that a caller cannot reach into the
// table.
func TestOperationAvailabilityReturnsACopy(t *testing.T) {
	first := OperationAvailability()
	first["build"] = opsurface.Unavailable("clobbered")

	again := OperationAvailability()
	if !again["build"].IsExposed() {
		t.Fatal("mutating the returned map reached the table")
	}
}

// TestAttachRefusesWhatTheTableDoesNotAccountFor is the standing proof that a
// command cannot reach the tree without the operation table naming it and giving
// it the path it is reached at.
//
// The first row is the positive control, and it earns its place: every other row
// asserts a refusal, and a registrar broken for an unrelated reason refuses
// everything. Without a correct registration that still succeeds, a uniformly
// broken registrar would read as a uniformly effective one.
//
// The tree is built here rather than taken from the command package, because
// this package builds no tree: what is under test is the registrar's rule, and
// a bare root plus one command is the whole input it reads.
func TestAttachRefusesWhatTheTableDoesNotAccountFor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		use     string
		id      string
		refuses string
	}{
		{
			name:    "a command at the path the table gives",
			use:     "build",
			id:      "build",
			refuses: "",
		},
		{
			name:    "an identifier that is not an operation",
			use:     "build",
			id:      "not-an-operation",
			refuses: "is not an operation",
		},
		{
			name:    "an operation this surface declares unavailable",
			use:     "hover",
			id:      "hover-at-position",
			refuses: "does not carry",
		},
		{
			name:    "a command whose path is not the one the table gives",
			use:     "buildish",
			id:      "build",
			refuses: "invocation path is",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "aggrega"}
			cmd := &cobra.Command{Use: tc.use, RunE: func(*cobra.Command, []string) error { return nil }}

			err := Attach(root, cmd, tc.id)
			switch {
			case tc.refuses == "" && err != nil:
				t.Fatalf("a command the table accounts for was refused: %v", err)
			case tc.refuses != "" && err == nil:
				t.Fatalf("a command the table does not account for was attached")
			case tc.refuses != "" && !strings.Contains(err.Error(), tc.refuses):
				t.Fatalf("the refusal does not name what was wrong: want %q in %q", tc.refuses, err)
			}

			attached := len(Children(Catalog(root), "aggrega")) == 1
			if want := tc.refuses == ""; attached != want {
				t.Errorf("after the call the tree carries the command = %v, want %v", attached, want)
			}
		})
	}
}

// TestAttachGroupRefusesARunnableCommand pins the one way the grouping entry
// point could otherwise become a bypass: a runnable command attached as a group
// would reach the tree carrying a capability, without ever naming an operation.
func TestAttachGroupRefusesARunnableCommand(t *testing.T) {
	root := &cobra.Command{Use: "aggrega"}

	group := &cobra.Command{Use: "config"}
	if err := AttachGroup(root, group); err != nil {
		t.Fatalf("a grouping command was refused: %v", err)
	}

	runnable := &cobra.Command{Use: "build", RunE: func(*cobra.Command, []string) error { return nil }}
	err := AttachGroup(root, runnable)
	if err == nil {
		t.Fatal("a runnable command was attached as a grouping command")
	}
	if !strings.Contains(err.Error(), "is runnable") {
		t.Errorf("the refusal does not name what was wrong: %v", err)
	}
}
