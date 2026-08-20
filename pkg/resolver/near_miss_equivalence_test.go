// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The two near-miss readings are each implemented twice, and this file is the
// comparison that makes the second implementation worth keeping.
//
// 🔴 Each case calls the hand-written implementation DIRECTLY, and that is the
// whole point of the file rather than an incidental choice. A build now produces
// these advisories THROUGH the framework, so a comparison against a build's own
// output would compare the framework with itself: it would pass forever while
// proving nothing about coverage.
//
// The hand-written implementations are therefore deliberate near-duplicates,
// retained as an independent cross-check. They must not be folded into the
// declared checks or re-expressed in terms of them: a cross-check sharing an
// implementation with what it checks cannot see the two drift apart. What the two
// sides legitimately share is one definition of what counts as a near miss —
// both reach [nearMissCandidates] — and what is compared here are their
// conclusions.
//
// # What is compared, and why it is the authored names rather than the text
//
// The two sides report the same offences in three deliberately different shapes,
// each following from its own contract rather than from an implementation slip.
// The WORDING differs: the declared check's message opens with the sentinel's own
// registered text and the values it raised follow, while the hand-written form
// composes a sentence at the call site. The LOCATOR differs: the declared check's
// is the framework's dotted dialect, the hand-written one's is a bracketed
// spelling. And the ORDER differs: the declared check raises in the order the
// document declares, the hand-written form sorts.
//
// Coverage is the claim that matters, so what is compared is the SET of authored
// names each side reported — which offences each found — and the pairing each
// side names is asserted separately, per side, against that side's own wording.
// Each fixture gives every offending element a distinct authored name, and that
// distinctness is asserted rather than assumed: were two names equal, the set
// comparison would collapse two findings into one and a side that dropped one of
// them would still agree.

// bracketedKeys returns the keys a hand-written locator names in its bracketed
// spelling, in the order they appear.
//
// It parses, and the parse is confined to this file: the spelling it reads is
// composed a few lines away in this same package, and the alternative — comparing
// the two sides' locators directly — is impossible, because they are two
// dialects.
func bracketedKeys(locator string) []string {
	var out []string
	rest := locator
	for {
		open := strings.Index(rest, `["`)
		if open < 0 {
			return out
		}
		rest = rest[open+2:]
		close := strings.Index(rest, `"]`)
		if close < 0 {
			return out
		}
		out = append(out, rest[:close])
		rest = rest[close+2:]
	}
}

// handWrittenAuthoredNames projects the authored element each hand-written
// finding is about: the LAST bracketed key in its locator, which is the offending
// socket or attachment name.
func handWrittenAuthoredNames(t *testing.T, findings []*ResolverError) []string {
	t.Helper()
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		keys := bracketedKeys(f.Field)
		if len(keys) == 0 {
			t.Fatalf("the hand-written locator %q names no bracketed key, so the comparison "+
				"below would run over a projection that reads nothing", f.Field)
		}
		out = append(out, keys[len(keys)-1])
	}
	return out
}

// declaredAuthoredNames projects the authored element each declared-check
// objection is about, read from the detail the check raised rather than parsed:
// the values are the authored name, the component type, and the declared name it
// sits within one edit of.
func declaredAuthoredNames(t *testing.T, objections []constraint.Objection) []string {
	t.Helper()
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		detail := o.Detail()
		if len(detail) != 3 {
			t.Fatalf("the declared check raised %d detail value(s); the projection below reads "+
				"the first of three (authored name, component type, declared name)", len(detail))
		}
		out = append(out, detail[0])
	}
	return out
}

// declaredPairs projects the (authored, declared) pair each objection names.
func declaredPairs(objections []constraint.Objection) [][2]string {
	out := make([][2]string, 0, len(objections))
	for _, o := range objections {
		detail := o.Detail()
		out = append(out, [2]string{detail[0], detail[2]})
	}
	return out
}

// sameSet reports whether two projections cover the same elements, and names what
// is missing from each side rather than reporting a bare inequality: a count
// comparison passes when both sides are short by one different element.
func sameSet(a, b []string) (missingFromB, missingFromA []string) {
	count := func(in []string) map[string]int {
		m := map[string]int{}
		for _, s := range in {
			m[s]++
		}
		return m
	}
	ca, cb := count(a), count(b)
	for k, n := range ca {
		for range n - cb[k] {
			missingFromB = append(missingFromB, k)
		}
	}
	for k, n := range cb {
		for range n - ca[k] {
			missingFromA = append(missingFromA, k)
		}
	}
	return missingFromB, missingFromA
}

// TestAuthoredSocketNearMissAgreesWithTheHandWrittenReading compares the declared
// socket reading against the hand-written implementation beside it.
func TestAuthoredSocketNearMissAgreesWithTheHandWrittenReading(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		sockets []string
		// wantNames is what BOTH sides must find, written out so the comparison
		// is against a stated expectation rather than only against each other:
		// two implementations that both went silent would otherwise agree.
		wantNames []string
		// wantPairs is the (authored, declared) pairing the declared reading
		// names, asserted so agreement on coverage cannot hide disagreement on
		// which declaration each offence was paired with.
		wantPairs [][2]string
	}{
		{
			name:      "one transposition",
			sockets:   []string{"databses"},
			wantNames: []string{"databses"},
			wantPairs: [][2]string{{"databses", "databases"}},
		},
		{
			name:      "two offences against two declarations",
			sockets:   []string{"databses", "cahces"},
			wantNames: []string{"databses", "cahces"},
			wantPairs: [][2]string{{"databses", "databases"}, {"cahces", "caches"}},
		},
		{
			// Both sides silent. The row exists because a cross-check made only
			// over offending documents cannot see a side that OVER-reports.
			name:      "declared socket and a distinct ad-hoc socket",
			sockets:   []string{"databases", "telemetry"},
			wantNames: nil,
			wantPairs: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDistinct(t, tc.sockets)
			na := aggWithSockets(t, tc.sockets)
			catalog := socketCatalog()

			view := newNormalizedView(na, nil, catalog, nil)
			objections, err := constraint.RunOnNodes(
				nil, newCoordinateSet(view), constraint.NormalizedView(view),
				authoredSocketNameNearADeclaredOne)
			if err != nil {
				t.Fatalf("declared reading: %v", err)
			}
			fromHandWritten := handWrittenAuthoredNames(t, lintUsesSocketNearMiss(na, catalog))
			fromDeclared := declaredAuthoredNames(t, objections)

			assertSameCoverage(t, tc.wantNames, fromDeclared, fromHandWritten)
			assertPairs(t, tc.wantPairs, declaredPairs(objections))
			// The hand-written side's own pairing, read against its own wording:
			// the pair assertion above reads the declared side's detail, which
			// says nothing about what the other side named.
			assertHandWrittenNamesPairs(t, tc.wantPairs, lintUsesSocketNearMiss(na, catalog))
		})
	}
}

// TestAttachmentNearMissAgreesWithTheHandWrittenReading compares the declared
// attachment reading against the hand-written implementation beside it.
func TestAttachmentNearMissAgreesWithTheHandWrittenReading(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		attachments []string
		wantNames   []string
		wantPairs   [][2]string
	}{
		{
			name:        "one transposition",
			attachments: []string{"db_passwrod"},
			wantNames:   []string{"db_passwrod"},
			wantPairs:   [][2]string{{"db_passwrod", "db_password"}},
		},
		{
			name:        "two offences against two declarations",
			attachments: []string{"db_passwor", "app_user_passwrod"},
			wantNames:   []string{"db_passwor", "app_user_passwrod"},
			wantPairs: [][2]string{
				{"db_passwor", "db_password"},
				{"app_user_passwrod", "app_user_password"},
			},
		},
		{
			name:        "declared slot and a distinct free attachment",
			attachments: []string{"db_password", "monitoring"},
			wantNames:   nil,
			wantPairs:   nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertDistinct(t, tc.attachments)
			na := aggWithAttachments(t, tc.attachments)
			catalog := slotCatalog()

			view := newNormalizedView(na, nil, catalog, nil)
			coordinates := newCoordinateSet(view)
			objections, err := constraint.RunOnNodeAttachments(
				nil, coordinates, coordinates, constraint.NormalizedView(view),
				attachmentNameNearADeclaredSlot)
			if err != nil {
				t.Fatalf("declared reading: %v", err)
			}
			fromHandWritten := handWrittenAuthoredNames(t, lintAttachmentSlotNearMiss(na, catalog))
			fromDeclared := declaredAuthoredNames(t, objections)

			assertSameCoverage(t, tc.wantNames, fromDeclared, fromHandWritten)
			assertPairs(t, tc.wantPairs, declaredPairs(objections))
			assertHandWrittenNamesPairs(t, tc.wantPairs, lintAttachmentSlotNearMiss(na, catalog))
		})
	}
}

// assertDistinct refuses a fixture whose authored names repeat, because the set
// comparison below would collapse two findings into one and a side that dropped
// one of them would still agree.
func assertDistinct(t *testing.T, names []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("this row authors %q twice; the coverage comparison cannot tell a dropped "+
				"finding from a merged one", n)
		}
		seen[n] = true
	}
}

// assertSameCoverage holds both readings to the stated expectation and to each
// other, naming what each side is missing.
func assertSameCoverage(t *testing.T, want, fromDeclared, fromHandWritten []string) {
	t.Helper()
	for _, pair := range []struct {
		label string
		got   []string
	}{
		{"the declared reading", fromDeclared},
		{"the hand-written reading", fromHandWritten},
	} {
		if missing, extra := sameSet(want, pair.got); len(missing) > 0 || len(extra) > 0 {
			t.Errorf("%s: missing %v, unexpected %v (want %v, got %v)",
				pair.label, missing, extra, want, pair.got)
		}
	}
	if missing, extra := sameSet(fromDeclared, fromHandWritten); len(missing) > 0 || len(extra) > 0 {
		t.Errorf("the two readings disagree: the hand-written one is missing %v and reports %v "+
			"the declared one does not", missing, extra)
	}
}

// assertPairs holds the declared reading's (authored, declared) pairing to the
// stated expectation, as a set: the order a reading raises in is its own contract
// and is asserted elsewhere.
func assertPairs(t *testing.T, want, got [][2]string) {
	t.Helper()
	flatten := func(in [][2]string) []string {
		out := make([]string, 0, len(in))
		for _, p := range in {
			out = append(out, p[0]+"→"+p[1])
		}
		return out
	}
	if missing, extra := sameSet(flatten(want), flatten(got)); len(missing) > 0 || len(extra) > 0 {
		t.Errorf("the declared reading's pairing: missing %v, unexpected %v", missing, extra)
	}
}

// assertHandWrittenNamesPairs holds the hand-written reading to the same pairing,
// read from its own composed sentence: it names both spellings, so a finding
// whose message carries the pair is a finding that paired them.
func assertHandWrittenNamesPairs(t *testing.T, want [][2]string, findings []*ResolverError) {
	t.Helper()
	for _, pair := range want {
		found := false
		for _, f := range findings {
			if strings.Contains(f.Message, pair[0]) && strings.Contains(f.Message, pair[1]) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no hand-written finding names both %q and %q; the two readings agree on "+
				"coverage but not on the pairing", pair[0], pair[1])
		}
	}
}

// TestHandWrittenNearMissReadingsReachNoProductionPath is the structural half of
// the retention decision: the hand-written readings exist only as the
// cross-check's independent side, so nothing may call them but a test.
//
// It is asserted rather than trusted because the failure it guards against is
// silent: a build wired to both readings reports one authoring fault twice, and
// nothing about that shows up as an error.
func TestHandWrittenNearMissReadingsReachNoProductionPath(t *testing.T) {
	t.Parallel()

	// Both are unexported, so a caller outside this package cannot reach them at
	// all — that half is the compiler's. What this asserts is the half the
	// compiler cannot: that the two readings are still two, by driving each over
	// a document the other reports on and requiring an answer from each.
	na := aggWithSockets(t, []string{"databses"})
	if got := lintUsesSocketNearMiss(na, socketCatalog()); len(got) != 1 {
		t.Fatalf("the hand-written socket reading is no longer answering; the cross-check above "+
			"would compare against nothing: %v", got)
	}
	attachments := aggWithAttachments(t, []string{"db_passwrod"})
	if got := lintAttachmentSlotNearMiss(attachments, slotCatalog()); len(got) != 1 {
		t.Fatalf("the hand-written attachment reading is no longer answering; the cross-check "+
			"above would compare against nothing: %v", got)
	}
	// And the advisory suite must reach neither of them: the suite's findings
	// carry the framework's dotted locator, which the hand-written readings
	// cannot produce.
	for _, f := range advisoriesOf(t, na, socketCatalog()) {
		if len(bracketedKeys(f.Field)) > 0 {
			t.Errorf("an advisory the suite produced carries a bracketed locator (%q), so a "+
				"hand-written reading reached a production path", f.Field)
		}
	}
}
