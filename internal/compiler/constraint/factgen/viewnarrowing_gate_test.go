// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/viewnarrowing"
)

// constraintPackageDir is the framework's own directory, where the view chain is
// declared, relative to this test package.
//
// It is a relative path rather than a derived one, and the reason it is safe to
// spell is that a wrong value cannot pass quietly: the chain read refuses a
// directory that declares no check type, so the gate below fails naming its own
// cause rather than analysing an empty chain. The same spelling reaches the same
// directory for the regeneration targets in this package.
const constraintPackageDir = ".."

// shippedCheckCorpus is every directory the module's production source declares a
// check in, relative to the module root, sorted.
//
// It is derived from the module read rather than listed. A listed corpus goes
// stale the moment a package declares its first check, and a corpus that silently
// misses a package answers "nothing is wrong" about code it never opened — which
// is the one failure a gate like this must not have.
func shippedCheckCorpus(t *testing.T) (root string, dirs []string, checks int) {
	t.Helper()

	m, err := readModuleSource()
	if err != nil {
		t.Fatalf("read the module's source: %v", err)
	}
	state, err := readInvocationState()
	if err != nil {
		t.Fatalf("read the declared checks: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range state.Checks {
		seen[c.Package] = true
	}
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return m.Root, dirs, len(state.Checks)
}

// TestShippedChecksDeclareTheViewTheirPredicateReads runs the narrowing read over
// every check the module ships, and fails on anything it reports.
//
// The read answers a question the compiler cannot: a check's declared view fixes
// where it runs, and declaring a view wider than the predicate reads places the
// check later in the compilation than it needs to be. A wider view satisfies a
// narrower need, so the type system has no objection. The same read reports a
// declaration naming a view the chain does not carry at all, which is how a rule
// that names a whole authored document as its view becomes visible: the view
// contract declares no method, so such a declaration compiles and dispatches, and
// nothing else in the tree says so.
//
// It blocks rather than reports, and the reason is mechanical rather than a matter
// of strictness: a Go test that gathers findings and prints them prints nothing at
// all on a passing package, so a reporting form of this gate would be a mechanism
// nothing ever reads. There is no reporting tier available that is not inert.
//
// Every kind the read emits fails here, not only the unplaceable view. The kinds
// answer one question between them — whether a check's declaration matches what
// its predicate needs — and a gate admitting four of the five would be a narrower
// instrument than the read it invokes.
//
// What it cannot see, stated so a green is not read as more than it is: the read
// is syntactic, so a predicate whose view escapes to a helper is reported as
// unreadable rather than cleared, and a check declared somewhere other than at
// package level is outside the corpus derivation, which reads package-level
// declarations. A check reaching behind an accessor to re-derive a fact its view
// does not name is invisible to this read by construction, and no additional
// reading here would find it.
func TestShippedChecksDeclareTheViewTheirPredicateReads(t *testing.T) {
	t.Parallel()

	root, dirs, checks := shippedCheckCorpus(t)

	// The corpus is asserted rather than assumed. The narrowing read over an empty
	// set of directories returns no findings and no error, so an empty corpus is
	// indistinguishable from a clean one from the outside: the count is what makes
	// the green mean something.
	if len(dirs) == 0 || checks == 0 {
		t.Fatalf("the corpus derivation found %d director(ies) and %d declared check(s); "+
			"a clean result over an empty corpus certifies nothing", len(dirs), checks)
	}
	t.Logf("narrowing read over %d director(ies), %d declared check(s): %s",
		len(dirs), checks, strings.Join(dirs, ", "))

	chain, err := viewnarrowing.ReadChain(constraintPackageDir)
	if err != nil {
		t.Fatalf("read the view chain from %s: %v", constraintPackageDir, err)
	}

	absolute := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		absolute = append(absolute, filepath.Join(root, dir))
	}
	findings, err := viewnarrowing.Analyze(chain, absolute...)
	if err != nil {
		t.Fatalf("run the narrowing read over the shipped corpus: %v", err)
	}
	if len(findings) == 0 {
		return
	}

	for i := range findings {
		if rel, rerr := filepath.Rel(root, findings[i].File); rerr == nil {
			findings[i].File = rel
		}
	}
	t.Fatalf("the narrowing read reports %d finding(s) over the shipped checks:\n%s",
		len(findings), viewnarrowing.Report(findings))
}

// TestTheNarrowingReadFiresOnAPlantedDeclaration is the calibration for the gate
// above: it plants a declaration the read must report and asserts it is reported.
//
// Without it, the gate's green says only that the read returned nothing, which is
// also what a read wired to the wrong chain, or one that reached no file, returns.
// The planted declaration is the difference between a gate and a decoration.
//
// This deliberately duplicates a little of what the narrowing package's own tests
// establish, and the duplication is the point rather than an oversight: those
// tests calibrate the read against fixtures of their own, while this one
// calibrates the pair the gate above actually uses — the chain as read from this
// directory, and the analysis entry it calls. Unifying the two would blind
// whichever one was removed.
func TestTheNarrowingReadFiresOnAPlantedDeclaration(t *testing.T) {
	t.Parallel()

	chain, err := viewnarrowing.ReadChain(constraintPackageDir)
	if err != nil {
		t.Fatalf("read the view chain from %s: %v", constraintPackageDir, err)
	}

	planted := t.TempDir()
	source := `package planted

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	Run: func(v constraint.RenderedView, s constraint.NodeSubject) []constraint.Objection {
		return nil
	},
}
`
	if err := os.WriteFile(filepath.Join(planted, "planted.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write the planted declaration: %v", err)
	}

	findings, err := viewnarrowing.Analyze(chain, planted)
	if err != nil {
		t.Fatalf("run the narrowing read over the planted declaration: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("the narrowing read reports %d finding(s) for one planted declaration, want 1:\n%s",
			len(findings), viewnarrowing.Report(findings))
	}
	if got, want := findings[0].Kind, viewnarrowing.NoFactRead; got != want {
		t.Fatalf("the planted declaration reads no fact from its view; the read calls it %q, want %q", got, want)
	}
	if findings[0].Check != "plantedCheck" {
		t.Fatalf("the read names the planted check %q, want %q", findings[0].Check, "plantedCheck")
	}
}

// TestTheGateStillRefusesAnOverDeclaredViewAndAnUnsupportedSubjectOnlyDeclaration
// is the calibration for the admitted shape: a check may declare that its input
// comes from its subject, and that declaration must buy exactly that shape and
// nothing else.
//
// The rows are the cells of the pair of conditions rather than their margins.
// Whether the predicate reads a view fact and whether it declares itself
// subject-only are independent, so the interesting cases are the combinations —
// above all a declaration sitting on a predicate that does read a fact, which is
// the shape that would let the declaration launder the very defect the read
// exists to find. Testing the two conditions separately would leave that cell
// unstated while every row passed.
//
// The declaration's reason is checked against the body here for the same reason
// it is checked at all: a reason nothing reads is a reason nobody has to mean.
func TestTheGateStillRefusesAnOverDeclaredViewAndAnUnsupportedSubjectOnlyDeclaration(t *testing.T) {
	t.Parallel()

	chain, err := viewnarrowing.ReadChain(constraintPackageDir)
	if err != nil {
		t.Fatalf("read the view chain from %s: %v", constraintPackageDir, err)
	}

	const header = `package planted

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"

`

	for _, row := range []struct {
		name string
		body string
		want viewnarrowing.Kind // empty means the read must report nothing
	}{
		{
			name: "a view later than the facts require is still refused",
			body: `var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	Run: func(v constraint.RenderedView, s constraint.NodeSubject) []constraint.Objection {
		_ = v.RawAggregateText("aggregate.yaml")
		return nil
	},
}`,
			want: viewnarrowing.OverDeclared,
		},
		{
			name: "declaring the input comes from the subject does not excuse a later view",
			body: `var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	SubjectOnly: "reads NodeID, the identity the subject carries.",
	Run: func(v constraint.RenderedView, s constraint.NodeSubject) []constraint.Objection {
		_ = v.RawAggregateText("aggregate.yaml")
		_ = s.NodeID()
		return nil
	},
}`,
			want: viewnarrowing.SubjectOnlyUnsupported,
		},
		{
			name: "a declaration stating no reason is refused",
			body: `var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	SubjectOnly: "",
	Run: func(_ constraint.RenderedView, s constraint.NodeSubject) []constraint.Objection {
		_ = s.NodeID()
		return nil
	},
}`,
			want: viewnarrowing.SubjectOnlyUnsupported,
		},
		{
			name: "a reason naming nothing the predicate reads is refused",
			body: `var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	SubjectOnly: "it has nothing to narrow to.",
	Run: func(_ constraint.RenderedView, s constraint.NodeSubject) []constraint.Objection {
		_ = s.NodeID()
		return nil
	},
}`,
			want: viewnarrowing.SubjectOnlyUnsupported,
		},
		{
			name: "a declaration on a predicate reading nothing at all is refused",
			body: `var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	SubjectOnly: "reads NodeID, the identity the subject carries.",
	Run: func(_ constraint.RenderedView, _ constraint.NodeSubject) []constraint.Objection {
		return nil
	},
}`,
			want: viewnarrowing.SubjectOnlyUnsupported,
		},
		{
			name: "reading no fact without declaring why is still refused",
			body: `var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	Run: func(_ constraint.RenderedView, s constraint.NodeSubject) []constraint.Objection {
		_ = s.NodeID()
		return nil
	},
}`,
			want: viewnarrowing.NoFactRead,
		},
		{
			name: "a grounded declaration over a subject-only predicate is cleared",
			body: `var plantedCheck = constraint.Check[constraint.RenderedView, constraint.NodeSubject]{
	SubjectOnly: "reads NodeID, the identity the subject carries, which no view produces.",
	Run: func(_ constraint.RenderedView, s constraint.NodeSubject) []constraint.Objection {
		_ = s.NodeID()
		return nil
	},
}`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			planted := t.TempDir()
			if err := os.WriteFile(filepath.Join(planted, "planted.go"), []byte(header+row.body+"\n"), 0o600); err != nil {
				t.Fatalf("write the planted declaration: %v", err)
			}

			findings, err := viewnarrowing.Analyze(chain, planted)
			if err != nil {
				t.Fatalf("run the narrowing read over the planted declaration: %v", err)
			}

			if row.want == "" {
				if len(findings) != 0 {
					t.Fatalf("the read reports %d finding(s) for a declaration it should clear:\n%s",
						len(findings), viewnarrowing.Report(findings))
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("the read reports %d finding(s) for one planted declaration, want 1:\n%s",
					len(findings), viewnarrowing.Report(findings))
			}
			if got := findings[0].Kind; got != row.want {
				t.Fatalf("the read calls the planted declaration %q, want %q:\n%s",
					got, row.want, viewnarrowing.Report(findings))
			}
		})
	}
}
