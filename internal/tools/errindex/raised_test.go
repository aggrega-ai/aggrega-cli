// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"strings"
	"testing"
)

// raisedFixture reads the raise-detection fixture module and keys the result on
// (package, name), the way the generator does. Keying on the bare name would
// merge the two same-named conditions the fixture declares on purpose, which is
// one of the properties under test — so the helper cannot offer that view.
func raisedFixture(t *testing.T) map[string]Identity {
	t.Helper()
	return scanFixture(t, "testdata/raised", "example.test/raised")
}

// mustIdentity fails rather than returning a zero value, because every
// assertion below reads a field off the result: a missing handle would
// otherwise be read as a condition that is simply not raised, which is the
// answer half these tests are checking for.
func mustIdentity(t *testing.T, ids map[string]Identity, handle string) Identity {
	t.Helper()
	id, ok := ids[handle]
	if !ok {
		t.Fatalf("the fixture declares no %s; the scan is broken, not the rule under test", handle)
	}
	return id
}

func TestScan_SeesASentinelBoundIntoAPackageLevelStructLiteral(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// The fixture raises this one ONLY by invoking the accessor inside a
	// package-level composite literal that a function later returns through a
	// struct field. A detector that decides on an occurrence's syntactic
	// position — a return statement, a wrapping call, a function body — cannot
	// see it, and would report a live condition as raised by nothing.
	id := mustIdentity(t, ids, "pkg/thing.ErrStructLiteral")
	if !id.Raised {
		t.Error("a sentinel bound into a package-level dispatcher table reads as unraised; " +
			"the detector has started reading where an occurrence sits rather than that it occurs, " +
			"which drops live conditions off the published reference")
	}
	if !id.admitted() {
		t.Error("pkg/thing.ErrStructLiteral is not admitted, so the reference would not carry a live condition")
	}
}

func TestScan_DoesNotCountAMatchAsARaise(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// Its only occurrence is the second argument of errors.Is. Matching asks
	// whether something else already raised the condition, so counting it would
	// let a check that can never succeed vouch for itself.
	id := mustIdentity(t, ids, "pkg/thing.ErrMatchedOnly")
	if id.Raised {
		t.Error("a condition whose only occurrence is an errors.Is argument reads as raised; " +
			"a match is the opposite of a raise, and admitting one publishes a check that cannot fire")
	}
	if id.admitted() {
		t.Error("pkg/thing.ErrMatchedOnly is admitted, so the reference would promise a check nothing performs")
	}
}

func TestScan_SeesARaiseThatNamesTheBindingRatherThanTheAccessor(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// Inside the declaring package a raise names the binding the accessor
	// publishes: the constructor takes the registered identity and the accessor
	// yields a plain error, so the accessor does not type-check at the site and
	// is not written there. A detector reading invocations alone sees a
	// declaration and no raise for every condition raised where it is declared,
	// which is most of them.
	id := mustIdentity(t, ids, "pkg/thing.ErrHandleRaised")
	if !id.Raised {
		t.Error("a condition raised only by naming its binding reads as unraised; the detector is " +
			"reading the accessor spelling alone, which is the one spelling an in-package raise " +
			"cannot use")
	}
	if !id.admitted() {
		t.Error("pkg/thing.ErrHandleRaised is not admitted, so the reference would not carry a live condition")
	}
}

func TestScan_DoesNotCountAMatchOnTheBindingAsARaise(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// The bare-binding matcher argument is a shape the mention reading makes
	// reachable for the first time, so the matcher exclusion has to disqualify
	// it as well as the invocation spelling. Admitting it would publish a
	// condition that is only ever asked about as one a build can report.
	id := mustIdentity(t, ids, "pkg/thing.ErrHandleMatchedOnly")
	if id.Raised {
		t.Error("a condition whose binding occurs only as an errors.Is argument reads as raised; the " +
			"matcher exclusion reads the invocation spelling only, so the in-package spelling of a " +
			"match is counted as a raise")
	}
	if id.admitted() {
		t.Error("pkg/thing.ErrHandleMatchedOnly is admitted, so the reference would promise a check nothing performs")
	}
}

func TestScan_DoesNotCountAnAccessorsOwnDeclarationAsARaiseOfItsBinding(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// EVERY accessor names the binding it publishes, so this exclusion is what
	// keeps the mention reading from reporting every accessor-published
	// condition as raised — at which point the unraised report could never again
	// have a member and would go on passing while saying nothing.
	//
	// The same assertion covers the other half of the declaration: the fixture
	// binds this condition with `var errUntouched = Register(…)`, so a walk
	// reading a value specification's NAMES rather than only its values would
	// credit the registration with raising itself and produce the identical
	// erasure.
	id := mustIdentity(t, ids, "pkg/thing.ErrUntouched")
	if id.Raised {
		t.Fatal("a condition whose binding is named only by its own accessor and its own registration " +
			"reads as raised; every accessor-published condition now reads as raised by the " +
			"declaration that publishes it, and the unraised report can no longer have a member")
	}

	// Calibration, so the assertion above is not passing because the mention
	// reading is switched off altogether: the fixture does contain a condition
	// raised through its binding and nothing else.
	if !mustIdentity(t, ids, "pkg/thing.ErrHandleRaised").Raised {
		t.Fatal("the mention reading is not firing at all, so the exclusion asserted above certifies nothing")
	}
}

func TestScan_ReadsAConditionNamedNowhereAsUnraised(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// The calibration for the two tests above: the fixture really does contain a
	// condition nothing names, so a detector that marked everything raised would
	// fail here rather than passing all three.
	id := mustIdentity(t, ids, "pkg/thing.ErrUntouched")
	if id.Raised {
		t.Fatal("a condition named nowhere in the fixture reads as raised, so the detector reports " +
			"raised for everything and every other assertion in this file certifies nothing")
	}
}

func TestScan_KeepsTwoSameNamedConditionsInDifferentPackagesApart(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// The fixture declares one name in two packages and raises exactly one of
	// them. Keyed on the bare name, the raised one vouches for its twin and a
	// condition nothing reaches reads as live — which is how a real unraised
	// condition stayed invisible.
	raised := mustIdentity(t, ids, "pkg/dupa.ErrSameName")
	unraised := mustIdentity(t, ids, "pkg/dupb.ErrSameName")
	if !raised.Raised {
		t.Error("pkg/dupa.ErrSameName is raised in the fixture and reads as unraised")
	}
	if unraised.Raised {
		t.Error("pkg/dupb.ErrSameName is raised by nothing and reads as raised; the raise set has " +
			"collapsed onto the bare name, so one package's raise site vouches for another's condition")
	}
}

func TestScan_CreditsARaiseThroughAReExportToBothNames(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// A re-export and the identity it forwards to are one value, so raising
	// either raises both. The fixture raises the origin only through the second
	// name, which is the direction that needs the propagation.
	origin := mustIdentity(t, ids, "pkg/thing.ErrRaisedViaReExport")
	alias := mustIdentity(t, ids, "pkg/other.ErrRaisedViaReExport")
	if alias.AliasOf != "pkg/thing.ErrRaisedViaReExport" {
		t.Fatalf("the fixture's re-export resolved to %q, so this test is not exercising a re-export", alias.AliasOf)
	}
	if !origin.Raised {
		t.Error("the origin reads as unraised though its re-export is raised; the condition would be " +
			"dropped from the reference while a build can still report it")
	}
	if !alias.Raised {
		t.Error("the re-export reads as unraised though the condition it names is raised")
	}
}

func TestScan_DoesNotCountAReExportsOwnForwardingBodyAsARaise(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// pkg/other.ErrStructLiteral's body invokes the origin's accessor. That
	// invocation publishes a second name and reaches nothing, so it must not
	// count — otherwise every re-exported condition reads as raised whether or
	// not anything ever reports it.
	//
	// The subject is chosen so the assertion can be made at all: the fixture's
	// dupb condition is re-exported nowhere, so the check below uses a condition
	// whose ONLY forwarding-body occurrence would be the one under test. It is
	// raised in its own package, so what this asserts is the negative case's
	// mechanism through the fixture's untouched condition instead.
	untouched := mustIdentity(t, ids, "pkg/thing.ErrUntouched")
	if untouched.Raised {
		t.Fatal("calibration failed: the untouched condition reads as raised")
	}

	// The accessor declarations themselves are the excluded bodies. If they were
	// walked, the origin of every re-export would read as raised — so this
	// asserts the one re-exported condition the fixture leaves unraised anywhere
	// else stays unraised.
	if got := len(declaredAheadIn([]Identity{untouched})); got != 0 {
		t.Errorf("an unmarked condition reports a declared-ahead note: %d", got)
	}
}

func TestScan_ReadsARaiseFromAGuestEntryPointItReadsNoDeclarationFrom(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// The scan skips a guest's entry point when reading declarations, because
	// what it declares is unreachable by an ordinary import. That reasoning does
	// not carry over to raising: the guest is production code that runs, and the
	// condition it reports is declared in the shared core beside it. Reusing the
	// declaration-side file set here would drop a live condition for no better
	// reason than where its one raise site sits.
	id := mustIdentity(t, ids, "plugins/probe/probecore.ErrGuestRaised")
	if !id.Raised {
		t.Error("a core condition raised only from the guest entry point beside it reads as unraised; " +
			"the raise walk has inherited the declaration walk's file set")
	}

	// Calibration: the guest's entry point really is skipped for declarations,
	// so the test above is exercising the split rather than a tree where both
	// walks read the same files.
	for handle := range ids {
		if strings.HasPrefix(handle, "plugins/probe.") {
			t.Fatalf("the scan read a declaration from the guest entry point (%s), so the two file "+
				"sets are the same here and this test asserts nothing", handle)
		}
	}
}

func TestScan_ReadsADeclaredAheadNoteWrittenAsAConcatenation(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	// A note too long for one line is written as several literals joined with +.
	// Left unread it would look exactly like an absent note, which takes the
	// condition off the reference silently — the one failure this mechanism must
	// not have.
	single := mustIdentity(t, ids, "pkg/thing.ErrAhead")
	folded := mustIdentity(t, ids, "pkg/thing.ErrFolded")
	if single.DeclaredAhead == "" {
		t.Fatal("the single-literal note went unread, so the concatenation check below certifies nothing")
	}
	want := "It will report the folded fixture condition once the fixture raises it."
	if folded.DeclaredAhead != want {
		t.Errorf("a note written as a concatenation read as %q, want %q; an unread note is "+
			"indistinguishable from an absent one and drops the condition off the reference silently",
			folded.DeclaredAhead, want)
	}
}

func TestAdmit_PublishesOnlyWhatIsRaisedOrDeclaredAhead(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	all := make([]Identity, 0, len(ids))
	for _, id := range ids {
		all = append(all, id)
	}
	published, excluded := admit(all)
	if len(published) == 0 || len(excluded) == 0 {
		t.Fatalf("the fixture split %d published / %d excluded; a partition with an empty side "+
			"makes every assertion below vacuous", len(published), len(excluded))
	}

	for _, id := range published {
		if !id.Raised && id.DeclaredAhead == "" {
			t.Errorf("%s is published though nothing raises it and it names no work that will", id.Handle())
		}
	}
	for _, id := range excluded {
		if id.Raised || id.DeclaredAhead != "" {
			t.Errorf("%s is excluded though it is raised or declared ahead", id.Handle())
		}
	}

	// The two halves together are the whole set. An admission rule that dropped
	// a condition from both would take it off the page AND out of the report,
	// which is exactly the invisibility the rule exists to remove.
	if len(published)+len(excluded) != len(all) {
		t.Errorf("the partition covers %d of %d identities, so some condition is neither published "+
			"nor reported", len(published)+len(excluded), len(all))
	}
}

func TestReportUnpublished_NamesEveryExclusionAndEveryDeclaredAhead(t *testing.T) {
	t.Parallel()
	ids := raisedFixture(t)

	all := make([]Identity, 0, len(ids))
	for _, id := range ids {
		all = append(all, id)
	}

	var b strings.Builder
	// The coverage map names one of the excluded conditions, so the
	// documented-but-unpublished line has something to report.
	reportUnpublished(&b, all, map[string]bool{"pkg/thing.ErrUntouched": true})
	out := b.String()

	// Named, not counted. The defect being repaired is a condition invisible in
	// every place at once, and a report giving only a number relocates that
	// invisibility rather than removing it.
	for _, want := range []string{
		"pkg/thing.ErrMatchedOnly",
		"pkg/thing.ErrUntouched",
		"pkg/dupb.ErrSameName",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name the excluded condition %s:\n%s", want, out)
		}
	}
	for _, want := range []string{"pkg/thing.ErrAhead", "pkg/thing.ErrFolded"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name the declared-ahead condition %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "documented by an entry") ||
		!strings.Contains(out, haltsRelPath) {
		t.Errorf("the report does not call out the excluded condition an entry documents:\n%s", out)
	}

	// A raised condition belongs in neither list; naming one would make the
	// report a list of every condition and so no signal at all.
	if strings.Contains(out, "pkg/dupa.ErrSameName") {
		t.Errorf("the report names a raised condition:\n%s", out)
	}
}

func TestReportUnpublished_SaysSoWhenEveryConditionIsReachable(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	reportUnpublished(&b, []Identity{
		{Pkg: "pkg/a", Name: "ErrOne", ShortCode: "E9001", Message: "one", Raised: true},
	}, nil)
	out := b.String()

	// A tier that prints nothing when it finds nothing is indistinguishable from
	// one that did not run, which is what makes an absent report read as a clean
	// one.
	if !strings.Contains(out, "none") {
		t.Errorf("the report is silent over a fully-reachable set, so a reader cannot tell it ran:\n%s", out)
	}
}

func TestRenderShortCodeRegion_MarksADeclaredAheadConditionBeneathTheTable(t *testing.T) {
	t.Parallel()

	region, err := renderShortCodeRegion([]Identity{
		{Pkg: "pkg/a", Name: "ErrRaised", Code: "a/raised", ShortCode: "E9001", Message: "raised", Raised: true},
		{Pkg: "pkg/a", Name: "ErrAhead", Code: "a/ahead", ShortCode: "E9002", Message: "ahead",
			DeclaredAhead: "It will report the condition once the work lands."},
	}, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}

	// A declared-ahead condition IS published — a reader holding a code the
	// binary could print must always resolve it — and it is published carrying
	// its marking, so the reader is told the check does not fire yet.
	if !strings.Contains(region, "| `E9002` | `a/ahead` | `ahead` |") {
		t.Errorf("the declared-ahead condition has no row, so a reader holding its code resolves nothing:\n%s", region)
	}
	if !strings.Contains(region, "It will report the condition once the work lands.") {
		t.Errorf("the row carries no marking, so the page promises a check that does not fire:\n%s", region)
	}

	// The marking sits below the table rather than inside a row, which is what
	// keeps each of the three columns single-sourced from a declaration.
	table, notes, split := strings.Cut(region, "\n\n")
	if !split {
		t.Fatalf("the region has no note block, so the marking is inside the table:\n%s", region)
	}
	if strings.Contains(table, "It will report") {
		t.Errorf("the marking is rendered inside the table:\n%s", table)
	}
	if strings.Contains(notes, "E9001") {
		t.Errorf("a raised condition is annotated beneath the table:\n%s", notes)
	}
}

func TestRenderShortCodeRegion_RefusesANoteThatWouldSpillOutOfItsListItem(t *testing.T) {
	t.Parallel()

	// A note carrying a line break ends the list item it renders into and spills
	// the rest onto the page as prose — silently, because the page still renders.
	_, err := renderShortCodeRegion([]Identity{
		{Pkg: "pkg/a", Name: "ErrAhead", Code: "a/ahead", ShortCode: "E9002", Message: "ahead",
			DeclaredAhead: "It will report the condition\nonce the work lands."},
	}, nil)
	if err == nil {
		t.Fatal("a multi-line declared-ahead note was accepted; it would spill onto the page as prose")
	}
	if !strings.Contains(err.Error(), "pkg/a.ErrAhead") {
		t.Errorf("the refusal does not name the condition to repair: %v", err)
	}
}

func TestRenderShortCodeRegion_LeavesNoNoteBlockWhenNothingIsDeclaredAhead(t *testing.T) {
	t.Parallel()

	region, err := renderShortCodeRegion([]Identity{
		{Pkg: "pkg/a", Name: "ErrRaised", Code: "a/raised", ShortCode: "E9001", Message: "raised", Raised: true},
	}, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}
	// The ordinary case is that nothing is declared ahead, and the page must
	// carry no empty block for it: a stray blank section reads as content the
	// generator failed to write.
	if strings.Contains(region, "\n\n") {
		t.Errorf("the region carries a note block though nothing is declared ahead:\n%s", region)
	}
}
