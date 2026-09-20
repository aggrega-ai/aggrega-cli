// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// twinnedServer builds a server over the commands the operation set records, so
// the pairs a session actually derives are the pairs under test.
func twinnedServer() *server {
	return &server{
		out:          io.Discard,
		logw:         io.Discard,
		commandTools: deriveCommandTools(commandBackedSurface()),
	}
}

// twiceExposedPairs returns, per operation, the curated tool that answers it
// inside this server and the generated tool that reaches it by running its
// command — for every operation both forms exist for.
//
// The pairing is computed here from the two records independently of the
// disclosure under test, so a disclosure that stopped being emitted is reported
// as a missing pair rather than as an empty set that agrees with itself.
func twiceExposedPairs(s *server) map[string][2]string {
	out := map[string][2]string{}
	for op, twin := range commandTwins(s.commandTools) {
		sibling, paired := curatedSibling(op)
		if !paired {
			continue
		}
		out[op] = [2]string{sibling.name, twin.tool}
	}
	return out
}

// TestTwiceExposedOperationsDiscloseEachOther is the guard on the ruling that a
// capability reachable both in-process and by running its command says so on
// both tools.
//
// Silence is the outcome the ruling forbids: two similarly-named tools where
// neither mentions the other leave a caller choosing blind, and one of the two
// can write to that caller's disk. So the assertion runs in both directions —
// the curated tool naming the generated one, and the generated one naming the
// curated one — because a change satisfying one direction and quietly dropping
// the other would still look green from whichever end someone checked.
func TestTwiceExposedOperationsDiscloseEachOther(t *testing.T) {
	s := twinnedServer()
	pairs := twiceExposedPairs(s)
	if len(pairs) == 0 {
		t.Fatal("no operation is reachable both in-process and by running its command, so this asserts nothing")
	}

	served := map[string]string{}
	for _, spec := range s.curatedTools(context.Background()) {
		served[spec.name] = spec.description
	}
	for _, spec := range s.deferredTools(context.Background()) {
		served[spec.name] = spec.description
	}

	for op, pair := range pairs {
		curatedName, generatedName := pair[0], pair[1]

		curatedDesc, ok := served[curatedName]
		if !ok {
			t.Errorf("the %q operation claims the curated tool %q, which this session does not serve", op, curatedName)
			continue
		}
		generatedDesc, ok := served[generatedName]
		if !ok {
			t.Errorf("the %q operation claims the generated tool %q, which this session does not serve", op, generatedName)
			continue
		}

		if !strings.Contains(curatedDesc, "`"+generatedName+"`") {
			t.Errorf("the %q tool does not name %q, which reaches the same capability by running a command:\n%s",
				curatedName, generatedName, curatedDesc)
		}
		if !strings.Contains(generatedDesc, "`"+curatedName+"`") {
			t.Errorf("the %q tool does not name %q, which answers the same capability in-process:\n%s",
				generatedName, curatedName, generatedDesc)
		}

		// Naming the other is half of it. What a caller needs in order to choose
		// is what each one does to the machine, so each side carries the other's
		// standing on that.
		for _, want := range []struct{ where, desc, phrase string }{
			{curatedName, curatedDesc, "anything it writes included"},
			{generatedName, generatedDesc, "anything it writes included"},
		} {
			if !strings.Contains(want.desc, want.phrase) {
				t.Errorf("the %q tool discloses its twin without saying what that twin does to the machine:\n%s",
					want.where, want.desc)
			}
		}

		// The curated tool's own opening sentence is what the generated tool
		// quotes, and it is where that tool states what it produces and what it
		// writes. A first sentence that stopped saying so would leave the
		// generated tool's caller unable to tell the two apart on that axis.
		lead := firstSentence(curatedDesc)
		if !strings.Contains(generatedDesc, lead) {
			t.Errorf("the %q tool does not carry %q's own opening sentence, so its caller is told nothing about "+
				"what the in-process form writes:\n want it to contain %q\n got %s",
				generatedName, curatedName, lead, generatedDesc)
		}
		if !strings.Contains(strings.ToLower(lead), "writ") {
			t.Errorf("the %q tool's opening sentence does not say what it writes, so the disclosure %q carries "+
				"about it is silent on the axis that matters most: %q", curatedName, generatedName, lead)
		}
	}
}

// TestACommandCarriesTheOperationItIsRatherThanOneItOffers pins the tie-break
// the pairing depends on, and it exists because a command path is no longer
// claimed by exactly one operation.
//
// A capability several commands hand back under a flag is exposed at each of
// their paths. It is OFFERED by each and is the identity of none, so the record
// naming a path and no other wins. Resolving the tie by declaration order would
// work today and silently stop working the day the operation set is reordered —
// and the only symptom would be a pairing disclosure quietly going missing, which
// every assertion above is a "contains" over and would therefore report.
//
// The rows are read from the availability records rather than listed, so this
// follows a rename instead of pinning one.
func TestACommandCarriesTheOperationItIsRatherThanOneItOffers(t *testing.T) {
	table := clisurface.OperationAvailability()

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
		if got := commandOperation(path); got != want {
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
		if got := commandOperation(path); got != id {
			t.Errorf("the command at %q is the only claimant of %q, and it was resolved to %q", path, id, got)
		}
	}
}

// TestAToolWithNoCommandTwinDisclosesNothing is the negative control. Every
// assertion above is a "contains", and a note appended unconditionally would
// satisfy all of them while telling a caller about a tool that does not exist.
//
// One sentence is subtracted before the check rather than admitted through it,
// and the distinction is the whole reason this stays a broad "contains". A tool
// may legitimately describe the generated FAMILY — what a kind-scoped answer
// leaves out, and why — which is a different claim from naming a generated tool
// as its own pair. That sentence has one home on the spec and is composed on its
// own predicate, so it can be removed exactly; what is left is every OTHER
// mention, and any of those still fails. Admitting the tool that carries it
// instead would have exempted a whole tool from the control, which is how a
// negative control quietly stops covering its subject.
func TestAToolWithNoCommandTwinDisclosesNothing(t *testing.T) {
	s := twinnedServer()
	twins := commandTwins(s.commandTools)

	var checked, subtracted int
	for _, spec := range s.curatedTools(context.Background()) {
		if _, paired := twins[spec.operation]; paired {
			continue
		}
		checked++
		declared, _ := lookupToolIn(spec.name, toolRegistry, deferredToolRegistry)
		residue := spec.description
		if declared != nil && declared.commandSurfaceNote != "" {
			before := residue
			residue = strings.ReplaceAll(residue, declared.commandSurfaceNote, "")
			if residue != before {
				subtracted++
			}
		}
		if strings.Contains(residue, commandToolPrefix) {
			t.Errorf("the %q tool answers a capability no command carries, yet its description names a generated "+
				"tool outside the one sentence that describes the family:\n%s", spec.name, spec.description)
		}
	}
	if checked == 0 {
		t.Fatal("every curated tool has a command twin, so this control asserts nothing")
	}
	// The vacuity control for the subtraction itself. A session that composed no
	// family sentence would make the removal above a no-op, and the check would
	// then be passing over text nobody had to think about.
	if subtracted == 0 {
		t.Fatal("no unpaired tool carried the family sentence, so the subtraction above removed nothing and " +
			"this control no longer covers the case it was widened for")
	}
}

// TestASessionWithNoCommandSurfaceClaimsNoTwin pins that the disclosure is a
// property of the session rather than of the registry.
//
// A note written into the registry at startup would be a claim about tools a
// session may never have derived — a caller handed no command surface would be
// pointed at a tool it cannot call. Deriving it per session is what makes the
// claim true rather than usually true.
func TestASessionWithNoCommandSurfaceClaimsNoTwin(t *testing.T) {
	bare := &server{out: io.Discard, logw: io.Discard}
	for _, spec := range bare.curatedTools(context.Background()) {
		if strings.Contains(spec.description, commandToolPrefix) {
			t.Errorf("a session holding no command surface advertises %q as naming a generated tool:\n%s",
				spec.name, spec.description)
		}
	}

	// The positive control: the same registry over a session that does hold one
	// says so, which is what makes the silence above a session property rather
	// than a disclosure that never fires.
	var disclosed int
	for _, spec := range twinnedServer().curatedTools(context.Background()) {
		if strings.Contains(spec.description, commandToolPrefix) {
			disclosed++
		}
	}
	if disclosed == 0 {
		t.Fatal("no curated tool disclosed a twin over a session holding a command surface")
	}
}

// TestTheDisclosureIsDerivedFromTheOperation pins that the pairing is read from
// the operation each tool answers rather than keyed on a tool's name.
//
// A name-keyed skip is the enumeration that goes stale: the two sides of a pair
// are named independently — one for the goal it serves, one for the command path
// it runs — so a rename on either side would silently stop matching. The two
// sides carry the same operation and nothing else in common, and this is what
// asserts the pairing rides on that.
func TestTheDisclosureIsDerivedFromTheOperation(t *testing.T) {
	s := twinnedServer()
	pairs := twiceExposedPairs(s)
	if len(pairs) == 0 {
		t.Fatal("no pairs derived")
	}

	var differing int
	for op, pair := range pairs {
		curatedName, generatedName := pair[0], pair[1]
		if commandToolPrefix+strings.TrimPrefix(generatedName, commandToolPrefix) == curatedName {
			t.Errorf("the %q pair's two names are the same string, which they must not be", op)
		}
		// The pair whose two names share no words at all is the one a spelling
		// comparison could never have found.
		if strings.TrimPrefix(generatedName, commandToolPrefix) != curatedName {
			differing++
		}
	}
	if differing == 0 {
		t.Error("every pair's generated name is its curated name behind the prefix, so a spelling comparison " +
			"would have found them all and this asserts nothing about deriving from the operation")
	}

	// The operation an unpaired command records is still read, so an operation
	// gaining a curated tool later is paired with no edit here.
	//
	// Which command that is has to be DERIVED. Naming one is how this case rots:
	// the command that stands for "no curated tool answers this" stops standing for
	// it the day a curated tool is written for its operation, and the symptom is
	// this assertion failing for a reason that has nothing to do with what it tests.
	id, path := anUncuratedCommand(t)
	unpaired := deriveCommandTool(clisurface.Descriptor{Path: path})
	if unpaired.operation != id {
		t.Errorf("a generated tool for %q records the operation %q, want %q", path, unpaired.operation, id)
	}
	if strings.Contains(unpaired.description, "answers this same capability") {
		t.Errorf("a command no curated tool answers carries a disclosure anyway:\n%s", unpaired.description)
	}
}

// anUncuratedCommand returns an operation the command line exposes at exactly one
// path and no curated tool answers, together with that path.
//
// The single-path condition is what makes the answer usable here: a path claimed
// by several operations carries whichever one names it alone, so a multi-path
// record would leave the operation the generated tool records undecided by this
// walk. It fails loudly when no such command exists, because the negative control
// below would then be asserting nothing.
func anUncuratedCommand(t *testing.T) (operation, path string) {
	t.Helper()
	table := clisurface.OperationAvailability()
	for _, op := range opsurface.Operations() {
		a, ok := table[op.ID]
		if !ok || !a.IsExposed() || len(a.Names) != 1 {
			continue
		}
		if _, curatedName := curatedToolNames[op.ID]; curatedName {
			continue
		}
		return op.ID, a.Names[0]
	}
	t.Fatal("every command-backed operation has a curated tool, so the unpaired control asserts nothing")
	return "", ""
}
