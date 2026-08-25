// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import (
	"fmt"
	"strings"
	"testing"
)

// This file is an INTERNAL test — package errors rather than errors_test — for
// one reason: the retirement record is unexported, and driving these probes from
// the record itself is what keeps them from becoming a second copy of it. A test
// that named the retired codes in its own source would pass while the record
// said something else, which is precisely the drift the record exists to remove.
//
// The probes below all register, so they run against the process-wide registry
// the package under test owns. Every code and short code declared here is
// therefore distinct from every other in this package's tests, and the short
// codes sit in a high band so nobody reads one as a number taken off the
// published sequence.

// recovered runs f and returns whatever it panicked with, or nil.
//
// It is declared here rather than shared with the external test file because the
// two are different packages: a helper in one is not visible from the other, and
// exporting one to share it would put a test helper on the package's API.
func recovered(f func()) (v any) {
	defer func() { v = recover() }()
	f()
	return nil
}

// TestRegister_RefusesEveryRetiredShortCode is acceptance for the whole item:
// the refusal is exercised through INVOCATION, over the record's own membership,
// rather than by asserting that a list contains some strings.
//
// It is driven from retiredShortCodes so that the corpus and the assertion
// cannot disagree, and it reports how many entries it covered — a clean pass
// over an empty record would otherwise read exactly like a clean pass over a
// full one.
//
// 🔴 The live-collision case is NOT what this proves, and a test written against
// it would have passed before this refusal existed. Every probe below claims a
// number no condition holds, so the collision index cannot fire and the
// retirement record is the only thing that can refuse it.
func TestRegister_RefusesEveryRetiredShortCode(t *testing.T) {
	if len(retiredShortCodes) == 0 {
		t.Fatal("the retirement record is empty, so every assertion below would pass over nothing")
	}

	for _, r := range retiredShortCodes {
		// A distinct descriptive code per probe, so no probe can be refused by
		// the duplicate-code check instead of the one under test.
		code := fmt.Sprintf("retirement-probe/claims-%s", strings.ToLower(r.shortCode))

		// The number really is free in the live index: nothing holds it, so the
		// collision refusal above it cannot fire and only the retirement record
		// can refuse this registration.
		registryMu.RLock()
		_, live := shortCodeIndex[r.shortCode]
		registryMu.RUnlock()
		if live {
			t.Errorf("%s is recorded retired and is also registered; the record and the registry "+
				"contradict each other, and this probe would be refused by the collision check", r.shortCode)
			continue
		}

		v := recovered(func() {
			Register(code, Meta{ShortCode: r.shortCode, Message: "a condition claiming a retired number"})
		})
		if v == nil {
			t.Errorf("registering the retired short code %s under a fresh code did not panic", r.shortCode)
			continue
		}
		msg := fmt.Sprint(v)
		if !strings.Contains(msg, r.shortCode) {
			t.Errorf("refusal %q does not name the retired short code %s", msg, r.shortCode)
		}
		if !strings.Contains(msg, code) {
			t.Errorf("refusal %q does not name the code %q it was declared for, so a reader cannot "+
				"find the declaration", msg, code)
		}
		// The word is the acceptance: a reader has to learn WHICH of the two
		// reuse rules they hit, and "already registered" would send them looking
		// for a live declaration that does not exist.
		if !strings.Contains(msg, "retired") {
			t.Errorf("refusal %q does not say the code is retired, so a reader cannot tell this "+
				"refusal from a collision with a live condition", msg)
		}
		if strings.Contains(msg, "already registered") {
			t.Errorf("refusal %q reports a retired code as already registered, which sends a reader "+
				"looking for a live declaration that does not exist", msg)
		}
		// The note travels with the refusal, so whoever hit it learns what the
		// number named without going to the record.
		if !strings.Contains(msg, r.note) {
			t.Errorf("refusal %q does not carry the note recorded for %s, so it says the number is "+
				"spent without saying what spent it", msg, r.shortCode)
		}
	}

	t.Logf("exercised the refusal against all %d entries of the retirement record", len(retiredShortCodes))
}

// A gap in the sequence, manufactured so that the refusal can be shown to
// accept one. Never-reusing guarantees permanent gaps, so a gap is the ordinary
// legal state and something has to occupy that state for a test to observe it.
//
// The three are registered at package initialisation, the way every other
// registration in this package's tests is, and for a reason beyond idiom: a
// registration inside a test body runs again on every repetition of that test,
// and the second run is a duplicate the registry refuses. Declared here it runs
// once per binary however many times the tests are repeated.
//
// gapAbove is declared BEFORE gapInside deliberately. The convention a gap check
// would key on is "the next free number is one above the highest allocated", so
// gapInside has to be claimed while a higher number is already held for the
// question to mean anything.
const (
	gapBelow  = "E9401"
	gapAbove  = "E9403"
	gapInside = "E9402"
)

var (
	_ = Register("retirement-probe/below-the-gap", Meta{ShortCode: gapBelow, Message: "the number below the gap"})
	_ = Register("retirement-probe/above-the-gap", Meta{ShortCode: gapAbove, Message: "the number above the gap"})
	// The number in between: below the highest this binary has allocated, and
	// recorded nowhere as retired. A refusal keyed on "below the highest and not
	// currently registered" would be a gap check and would panic HERE, taking the
	// whole test binary down before a single test in the package ran — which is
	// the loudest failure available and is the point of declaring it here.
	inTheGapIdentity = Register("retirement-probe/in-the-gap", Meta{ShortCode: gapInside, Message: "the number in the gap"})
)

// TestRegister_AcceptsAGapThatIsNotRetired is the other half of acceptance: the
// assertion that fails if the refusal is ever rewritten as a gap check.
//
// A gap is the ABSENCE of a record and settles nothing, so the refusal must
// decline the numbers the retirement record names and accept every other gap.
// The declarations above manufacture one; this reads back the state they left,
// which is what says the acceptance happened rather than merely that nothing
// crashed.
func TestRegister_AcceptsAGapThatIsNotRetired(t *testing.T) {
	// Calibration: the probe is only a probe while the number really is absent
	// from the record. A number added to the record later would silently turn
	// this test into an assertion about a retired code.
	for _, r := range retiredShortCodes {
		if r.shortCode == gapInside {
			t.Fatalf("%s is recorded retired, so it is the wrong probe for a gap that is not", gapInside)
		}
	}

	registryMu.RLock()
	defer registryMu.RUnlock()
	// The higher number is held, so the number in between really was a gap below
	// the highest allocated at the moment it was claimed.
	if _, held := shortCodeIndex[gapAbove]; !held {
		t.Fatalf("%s is not registered, so %s was never below the highest allocated and this test "+
			"asserts nothing about a gap", gapAbove, gapInside)
	}
	got, registered := shortCodeIndex[gapInside]
	if !registered {
		t.Fatalf("%s is a gap below the highest allocated and is recorded nowhere as retired, yet it "+
			"is not registered; refusing it would make this a gap check rather than a tombstone check",
			gapInside)
	}
	if got != inTheGapIdentity {
		t.Errorf("%s resolves to a different identity than the registration that claimed it", gapInside)
	}
}

// TestRetirementRecord_IsDisjointFromTheLiveRegistry is the integrity property
// the two mechanisms cannot state about themselves.
//
// Register refuses a retired number, so a number that is both retired and
// registered is unreachable in a program that ran to completion — but only for
// the registrations that happen to run before the record is consulted. Asserted
// over the whole record and the whole registry once every package's identities
// are in hand, it says the two never contradict each other, which is what makes
// the published page's retired notes and its rows two views of one truth rather
// than two claims.
func TestRetirementRecord_IsDisjointFromTheLiveRegistry(t *testing.T) {
	if len(retiredShortCodes) == 0 {
		t.Fatal("the retirement record is empty, so this assertion covers nothing")
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, r := range retiredShortCodes {
		if held, live := shortCodeIndex[r.shortCode]; live {
			t.Errorf("%s is recorded retired and is registered for %q; the reference would carry a row "+
				"resolving the code to a condition and a note beneath saying nothing reports it",
				r.shortCode, held.code)
		}
	}
}

// TestIndexRetirements_RefusesARecordItCannotServe covers the guard that runs at
// package initialisation, which cannot be observed from a passing binary: by the
// time a test runs, a defective record would already have stopped the process.
// So the guard is invoked directly, once per defect it names, because a record
// admitted quietly is a number that is silently free again.
func TestIndexRetirements_RefusesARecordItCannotServe(t *testing.T) {
	cases := []struct {
		name   string
		record []retirement
		names  string
	}{
		{
			name:   "a malformed short code",
			record: []retirement{{"E42", "It named something."}},
			names:  "E42",
		},
		{
			name:   "a note carrying no text",
			record: []retirement{{"E9411", "   "}},
			names:  "E9411",
		},
		{
			name:   "a note spanning two lines",
			record: []retirement{{"E9412", "It named something.\nAnd then some more."}},
			names:  "E9412",
		},
		{
			name:   "one number recorded twice",
			record: []retirement{{"E9413", "It named something."}, {"E9413", "It named something else."}},
			names:  "E9413",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := recovered(func() { indexRetirements(tc.record) })
			if v == nil {
				t.Fatalf("a record carrying %s was accepted", tc.name)
			}
			if msg := fmt.Sprint(v); !strings.Contains(msg, tc.names) {
				t.Errorf("refusal %q does not name %s, so a reader cannot find the entry to fix",
					msg, tc.names)
			}
		})
	}
}

// TestIndexRetirements_ServesTheNoteRecordedForEachCode is the positive half:
// the guard is what the refusal reads, so a record it accepted but keyed wrongly
// would refuse the right numbers with the wrong text.
func TestIndexRetirements_ServesTheNoteRecordedForEachCode(t *testing.T) {
	if len(retiredShortCodes) == 0 {
		t.Fatal("the retirement record is empty, so this assertion covers nothing")
	}
	index := indexRetirements(retiredShortCodes)
	if len(index) != len(retiredShortCodes) {
		t.Fatalf("the record declares %d entries and the index holds %d", len(retiredShortCodes), len(index))
	}
	for _, r := range retiredShortCodes {
		note, ok := index[r.shortCode]
		if !ok {
			t.Errorf("the index holds no entry for %s", r.shortCode)
			continue
		}
		if note != r.note {
			t.Errorf("the index serves %q for %s, and the record declares %q", note, r.shortCode, r.note)
		}
	}
}
