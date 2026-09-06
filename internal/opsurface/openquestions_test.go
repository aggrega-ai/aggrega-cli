// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opsurface

import (
	"strings"
	"testing"
)

// boundCeiling is the number of operation-and-surface pairs the bound may
// hold.
//
// 🔴 IT ONLY EVER GOES DOWN. The check below refuses a disagreement in either
// direction, and each direction stops a different thing. Refusing a bound that
// GREW is what makes it a bound rather than somewhere to put the next
// undecided pair: excusing one costs an edit here as well as a row there, so it
// is a change that has to argue for itself. Refusing one that SHRANK without
// this number following it down is what stops the room staying open to absorb a
// future addition for free.
//
// It is declared in the test file rather than beside the bound deliberately.
// Nothing in the package can read it, so no production path can grow the bound
// and satisfy its own ceiling in one edit — the second edit is in a file whose
// only purpose is to say the number goes down.
//
// It was raised by one for the connection-candidates pair on the tool protocol,
// and the argument the raise owes is this. That capability reports an entity's
// dependency sockets and each one's valid targets; the tool protocol's existing
// grounds for declining are that a request addresses a cursor in a document the
// caller holds open, that it carries state across calls, that it returns what an
// earlier call recorded, or that it reports a workspace-wide Release selection.
// None of them is true of it: it takes an entity name, holds nothing, records
// nothing and selects no Release. So there is no reason to write, and a reason
// written anyway would be the unexamined absence dressed as a deliberate one
// that this bound exists to keep visible.
//
// What makes it a question rather than an omission is that the identical
// question is already open one row above it, for the attachment half of the same
// authoring question. The two are one decision: whichever way candidacy on this
// surface is settled, it settles both, and at that point two rows leave together
// and this number goes down by two. A single row disposed of on its own would be
// the split the pairing exists to prevent.
//
// It came down by one when the daemon began reporting the effective
// configuration and each setting's originating layer. That row asked whether the
// handshake's three fields were the intended extent for a session or whether the
// daemon owed the full report; the daemon now answers the full report at a named
// method, so the pair is disposed of and the room it held does not stay open.
//
// 🔴 It was then raised by sixteen, and this is the largest raise it will ever
// take. The argument it owes:
//
// Sixteen capabilities on the daemon were declined on one reason — that the
// daemon's only writing path was the artifact set a build renders, so an
// operation whose product is some other write had no output there. That reason
// was retired, because it drew the line in the wrong place: what makes a write
// acceptable is that somebody asked for it, not which artifact it is. Every one
// of the sixteen would be explicitly invoked; what stays out of bounds is a
// write, or a reach for a registry, performed while validating or inspecting,
// where nobody asked.
//
// Retiring a reason does not decide the capabilities that rested on it. It
// leaves them where they were before anybody wrote that reason down: undecided.
// So they moved to the bound rather than to another reason, and the raise is the
// bound doing exactly what it is for — making an unexamined absence visible as
// one instead of letting it keep the clothes of a deliberate one.
//
// Two things it deliberately did NOT do. It exposed nothing: an operation
// carrying a reason and an operation named here both register nothing, so no
// wire method appeared and no behaviour changed. And it re-keyed nothing onto a
// surviving reason on a resemblance — the capabilities declined for having their
// subject outside the workspace keep that reason, which holds on its own and was
// never the retired one, and no row was moved onto it because it looked close
// enough.
//
// The number goes down as each row is settled, one capability at a time, and
// each of the sixteen states the fork that settles it.
//
// It came down by one when the command line began drafting a starter document
// for an entity kind. That row asked whether a command was withheld on purpose
// or whether the absence was one nobody had examined; the answer is that it was
// unexamined, and a command now carries the capability, so the pair is disposed
// of and the room it held does not stay open. The row's own observation — that
// the nearest command converts a foreign source description rather than starting
// from a kind's contract — is why the two stayed separate commands rather than
// becoming two modes of one.
//
// It came down by one again when the daemon began resolving an entity by name
// and reporting a Release's dependency closure. That row asked whether by-name
// resolution was left to the surfaces that have it on purpose or whether a
// session owed it; the answer is that a session owes it, and two methods now
// carry the capability, so the pair is disposed of and the room it held does not
// stay open.
//
// Both halves shipped together, and that was the condition on the row leaving.
// The capability is reporting a resolved definition OR a dependency closure, so
// half of it would have been half a decision — a row disposed of on a capability
// the surface still only partly carried, with nothing left to record the part it
// did not.
//
// It came down by TWO when the tool protocol began enumerating a workspace's
// entities and its Releases. Two rows left in one edit, and the argument for
// taking both together is that they were one question asked about two
// populations: a caller on that surface could learn neither what a workspace
// contains nor which Releases it holds.
//
// What settled them is that every other capability there takes a name and
// refuses one it cannot resolve — deliberately, since an empty answer would
// report a typo as a subject with nothing to offer. Enumeration is the only
// capability that takes no subject at all, so it is the only way to obtain a
// name the rest will accept, and withholding it left a surface whose inputs a
// caller had no way to come by. Disposing of one and leaving the other would
// have split that: naming an entity and naming a Release are the two inputs
// almost every remaining capability requires, and a caller able to discover only
// one of them is still unable to start.
//
// Neither is a capability this surface grew of its own. Both call the
// implementation an editor's answer is computed by, so what counts as an entity
// and what counts as a Release is decided in one place — which matters most for
// the Release walk, where membership is read from each file's own kind header
// and a second implementation would pass every test written over
// conventionally-named files while disagreeing on the ones that matter.
//
// It came down by one when the command line began explaining a halting condition
// from the code a build printed. That row asked whether a command was withheld on
// purpose or whether the absence was one nobody had examined; the answer is that
// it was unexamined, and a command now carries the capability, so the pair is
// disposed of and the room it held does not stay open.
//
// What settled it is that the row's own observation — the capability was
// reachable as a tool and nowhere else — turned out to rest on something absent
// rather than on anything about the command line. The conditions a build can
// report are declared inside the binary and the explanations are published, but
// nothing read either: the declarations could not be asked what a code names, and
// nothing resolved a code to the entry that teaches the fix. With both reads in
// place the capability has no property of the command line standing against it,
// and a reason written anyway would have been the unexamined absence dressed as a
// deliberate one that this bound exists to keep visible.
//
// The daemon's row for the same capability is a different row and is untouched:
// it asks whether the surface that emits a diagnostic owes the expansion of it,
// which is a question about that surface and not about this one.
//
// It came down by TWO when a run began handing back the graph it resolved. Two
// rows left in one edit — the command line's and the tool protocol's — and the
// argument for taking both together is that they were one question asked about
// two surfaces: on each, the graph was produced and not offered.
//
// What settled them is that neither absence rested on a property of its surface.
// The command line's row observed that a command resolves the graph to draw a
// readable view of a Release and stops there; the tool protocol's observed that a
// build cannot render without the graph and returns the files instead. Both are
// observations that the document was reachable and unoffered, which is the
// unexamined absence this bound exists to keep visible rather than a reason.
//
// Disposing of one and leaving the other would have split that, and it would have
// split it in the sharpest way available: the tool protocol reaches the capability
// by running the command, so the command line's answer IS the tool protocol's
// answer. A row left standing on one surface would have asked a question the other
// surface's disposal had already settled.
//
// It came down by TWO again when the tool protocol began answering both halves of
// candidacy — which Features may attach to a node, and what a node may connect to.
// The pairing was not decided here: the rows themselves recorded it, in the words
// the connection row carried, and taking both together is the condition those words
// set. They are one capability asked about the two kinds of edge, a Feature
// attaching to a node and a node connecting to another, so settling one and leaving
// the other would have split one decision across two surfaces and left a caller able
// to ask about one kind of edge and not the other for no reason it could discover.
//
// What settled them is that the parity argument the rows recorded as not applying
// had become the argument FOR carrying them. Each row observed that the capability
// exists so a caller offering choices offers the set the compiler would match, and
// that nothing on this surface offered any — which made the parity half moot. A
// caller on this surface is an assistant proposing an attachment or an edge, so it
// was offering them from memory of the schema instead, which is precisely the
// divergence the capability exists to prevent. The absence was unexamined rather
// than deliberate, and a reason written for it would have been that unexamined
// absence dressed as a deliberate one.
//
// Neither is a capability this surface grew of its own: both call the
// implementation an editor's answer is computed by, so what the compiler would
// match is decided in one place. A second label match or a second socket walk would
// agree on the ordinary case and part company on the awkward one, and the feature
// would then be a source of wrong suggestions rather than a guard against them.
// It came down by TWO once more when the daemon began resolving a Release's
// dependencies and pinning the external inputs its render consumes. Both rows
// asked the same question in different words — whether a session owes an
// operation that goes to the network and writes what it fetched — and both are
// answered by the same rule: what admits such an operation is that a caller
// invoked it, not which surface the caller reached for. Settling one and leaving
// the other would have split one answer across two rows that only ever had one.
//
// Nothing about the daemon's inspection posture moved with them. What that
// posture protects is a workspace that changes, or a network that is reached,
// while somebody is validating or typing; both of these run only when a request
// asks for them by name, so the protection is untouched and the surface's other
// methods still answer from the workspace and what is already cached.
//
// It came down by THREE when the daemon took on the caches that serve the work:
// reclaiming entries no lockfile references, trimming the compiled-module store
// to a cap, and wiping every cache. All three write, and all three are admitted
// by the rule the rows themselves pointed at — a caller that sent one has asked
// for the reclamation, exactly as a caller sending a build has asked for
// artifacts. Taking them separately would have split one answer three ways.
//
// The eviction row is the one that decided the shape. It observed that the
// compiled-module store sits outside every workspace, which is what a
// neighbouring set of declines rested on, AND that this daemon is what fills that
// store, once per guest, for every session on the machine. The second fact is the
// stronger one: a surface that populates a store is not neutral about it, so the
// decline was resting on where the store sits rather than on anything the daemon
// decided. The reason those neighbours carry was restated accordingly, on what a
// session holds rather than on which directory a subject occupies, and its row set
// was checked one at a time against the new wording.
//
// What did NOT move with them is the protection: these run only when a request
// names them, so nothing writes while a document is being validated or inspected.
//
// It came down by THREE again when the daemon took on the registries a workspace
// reaches: registering one with its trust policy, dropping one, and refreshing
// the local index the search reads. The three ship together for the reason the
// rows themselves gave — the removal row said outright that settling the
// registration and leaving it would split one decision — and all three are
// admitted by the same invocation rule.
//
// The registration row is the one that decided the shape here, and it decided it
// by being WRONG. It argued for exposure on the ground that the write's "default
// target is inside" the workspace. Measured, it is not: a registry is tool-scoped
// tooling, so with no override the write resolves to the global configuration
// file every workspace on the machine reads through. The correct argument is the
// opposite one — the reach is machine-wide, which is why each of these responses
// carries the file it wrote and whether that file is the shared one, rather than
// letting a session-scoped answer be read as a workspace-scoped change.
//
// The refresh row was wrong the other way. It said the index "is not in the
// workspace, which is why search itself is declined"; the index is written under
// the workspace's own configured cache directory, and search was exposed on that
// very ground. What is genuinely distinctive about the refresh is the reach, not
// the write: it is the only method the daemon carries that goes out to a
// registry, and it does so without prompting, which is what the credential
// posture already required of every surface with nobody watching.
const boundCeiling = 10

// TestTheBoundOnlyShrinks is the ratchet. See boundCeiling for why both
// directions refuse.
func TestTheBoundOnlyShrinks(t *testing.T) {
	switch {
	case len(openQuestions) > boundCeiling:
		t.Fatalf("the bound GREW: it enumerates %d pair(s) and its ceiling is %d.\n"+
			"An undecided pair was added to the bound instead of being disposed of. Dispose of it — "+
			"Exposed(name…) or Unavailable(reason) on the surface — or raise boundCeiling in this file "+
			"deliberately and say why in review.", len(openQuestions), boundCeiling)
	case len(openQuestions) < boundCeiling:
		t.Fatalf("the bound SHRANK: it enumerates %d pair(s) and its ceiling is %d.\n"+
			"A pair was decided without the ceiling following it down. Lower boundCeiling to %d so the "+
			"room does not stay open.", len(openQuestions), boundCeiling, len(openQuestions))
	}
}

// TestTheBoundIsWellFormed pins the properties every row has to have for the
// bound to mean anything: a surface the checks actually run over, an operation
// that exists, a question that poses one, and one row per pair.
//
// The question-mark assertion is the shape that keeps a bound row
// distinguishable from an availability record. A declined operation carries a
// reason — a clause a finding embeds, which the surfaces' own tests hold to that
// shape — and a bound row carries a question. A row that had drifted into
// asserting a reason would be an unexamined absence wearing the clothes of a
// deliberate one, which is the whole thing this bound exists to keep visible.
func TestTheBoundIsWellFormed(t *testing.T) {
	known := map[Surface]bool{SurfaceCLI: true, SurfaceServe: true, SurfaceMCP: true}
	seen := map[string]bool{}

	for _, q := range openQuestions {
		pair := string(q.Surface) + "\x00" + q.ID

		if !known[q.Surface] {
			t.Errorf("the bound names surface %q, which no check runs over, so the row excuses nothing "+
				"and refuses nothing", q.Surface)
		}
		if _, ok := Lookup(q.ID); !ok {
			t.Errorf("the bound names %q on %s, which is not an operation", q.ID, q.Surface)
		}
		if seen[pair] {
			t.Errorf("the bound names %q on %s twice", q.ID, q.Surface)
		}
		seen[pair] = true

		if !strings.Contains(q.Question, "?") {
			t.Errorf("the bound's row for %q on %s poses no question, so it reads as a reason: %q",
				q.ID, q.Surface, q.Question)
		}
		if len(q.Question) < 80 {
			t.Errorf("the bound's row for %q on %s carries too little to settle: %q",
				q.ID, q.Surface, q.Question)
		}
	}
}

// TestTheBoundIsHeldFromBothEnds drives the two ways a row goes stale plus the
// excuse working, so no leg of the bound is an assertion nobody has seen fire.
//
// The bound is supplied rather than read from the package, which is what lets
// each leg be built for itself; that the surfaces reach this same function with
// the real bound is pinned separately below.
func TestTheBoundIsHeldFromBothEnds(t *testing.T) {
	ids := IDs()
	first, second := ids[0], ids[1]

	// Every operation disposed of except the first, which the bound excuses.
	tableWithout := func(omit string) map[string]OperationAvailability {
		out := map[string]OperationAvailability{}
		for _, id := range ids {
			if id == omit {
				continue
			}
			out[id] = Exposed(id)
		}
		return out
	}

	t.Run("an excused pair produces no finding", func(t *testing.T) {
		bound := []OpenQuestion{{SurfaceCLI, first, "is it wanted here?"}}
		if findings := checkAvailability(SurfaceCLI, tableWithout(first), bound); len(findings) != 0 {
			t.Fatalf("the bound did not excuse the pair: %v", findings)
		}
	})

	t.Run("the bound is per surface, not global", func(t *testing.T) {
		bound := []OpenQuestion{{SurfaceCLI, first, "is it wanted here?"}}
		findings := checkAvailability(SurfaceServe, tableWithout(first), bound)
		joined := strings.Join(findings, "\n")
		if !strings.Contains(joined, "declares nothing about") || !strings.Contains(joined, first) {
			t.Fatalf("a row bound on one surface excused another surface:\n%s", joined)
		}
	})

	t.Run("a row for an operation that has since been disposed of", func(t *testing.T) {
		bound := []OpenQuestion{{SurfaceCLI, second, "is it wanted here?"}}
		findings := checkAvailability(SurfaceCLI, tableWithout(first), bound)
		joined := strings.Join(findings, "\n")
		if !strings.Contains(joined, "One of the two is wrong") || !strings.Contains(joined, second) {
			t.Fatalf("a decided pair left on the bound was not refused:\n%s", joined)
		}
		if !strings.Contains(joined, boundLocation) {
			t.Errorf("the finding does not name where the row is:\n%s", joined)
		}
	})

	t.Run("a row naming something that is not an operation", func(t *testing.T) {
		bound := []OpenQuestion{{SurfaceCLI, "retired-operation", "is it wanted here?"}}
		findings := checkAvailability(SurfaceCLI, tableWithout(""), bound)
		joined := strings.Join(findings, "\n")
		if !strings.Contains(joined, "retired-operation") || !strings.Contains(joined, "is not an operation") {
			t.Fatalf("a stale bound row was not refused:\n%s", joined)
		}
		if !strings.Contains(joined, boundLocation) {
			t.Errorf("the finding does not name where the row is:\n%s", joined)
		}
	})
}

// TestTheExportedCheckReadsTheRealBound pins the delegation, so the legs above
// are evidence about the function the surfaces call rather than about a
// near-copy of it.
//
// It builds the table the real bound implies for one surface — every operation
// disposed of except the ones that surface's rows excuse — and asserts the
// exported check is silent over it, then asserts it is not silent when one
// operation the bound does NOT name is dropped. Both directions, because a
// check that excused everything would pass the first on its own.
func TestTheExportedCheckReadsTheRealBound(t *testing.T) {
	if len(openQuestions) == 0 {
		t.Skip("the bound is empty, so there is no delegation to observe")
	}
	surface := openQuestions[0].Surface

	table := map[string]OperationAvailability{}
	var notNamed string
	for _, op := range operations {
		if isOpen(openQuestions, surface, op.ID) {
			continue
		}
		table[op.ID] = Exposed(op.ID)
		notNamed = op.ID
	}
	if notNamed == "" {
		t.Fatalf("the bound excuses every operation on %s, so there is nothing to contrast against", surface)
	}

	if findings := CheckAvailability(surface, table); len(findings) != 0 {
		t.Fatalf("the exported check did not read the real bound for %s: %v", surface, findings)
	}

	delete(table, notNamed)
	findings := CheckAvailability(surface, table)
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, notNamed) || !strings.Contains(joined, "declares nothing about") {
		t.Fatalf("the exported check excused %q, which the bound does not name:\n%s", notNamed, joined)
	}
}

// TestTheBoundReportsItsOwnReach pins that the census states how much it
// excused and that it asserts nothing about those pairs, rather than reporting a
// bare verdict. Every figure in it is derived at the moment of the call, so
// nothing here can rot into agreement with a number somebody wrote down.
func TestTheBoundReportsItsOwnReach(t *testing.T) {
	census := BoundCensus()
	for _, want := range []string{boundLocation, "no availability is asserted"} {
		if !strings.Contains(census, want) {
			t.Errorf("the census does not mention %q:\n%s", want, census)
		}
	}

	t.Run("a surface the bound excuses nothing on says so", func(t *testing.T) {
		got := boundStatement(SurfaceCLI, nil)
		if !strings.Contains(got, "excuses no operation") {
			t.Fatalf("an empty bound did not report itself as empty:\n%s", got)
		}
	})

	t.Run("a surface with rows names them and disclaims them", func(t *testing.T) {
		surface := openQuestions[0].Surface
		got := BoundStatement(surface)
		if !strings.Contains(got, "asserts") || !strings.Contains(got, "NOTHING") {
			t.Errorf("the statement does not disclaim what it did not cover:\n%s", got)
		}
		for _, q := range openQuestions {
			if q.Surface != surface {
				continue
			}
			if !strings.Contains(got, q.ID) {
				t.Errorf("the statement for %s does not name the excused %q:\n%s", surface, q.ID, got)
			}
		}
	})

	t.Run("a stale row is counted rather than dropped from the census", func(t *testing.T) {
		got := boundStatement(SurfaceCLI, []OpenQuestion{{SurfaceCLI, "retired-operation", "is it wanted?"}})
		if !strings.Contains(got, "retired-operation") {
			t.Fatalf("a row naming no operation vanished from the census:\n%s", got)
		}
	})
}
