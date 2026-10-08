// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package namesegment_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// lowercaseNameRune is the alphabet the option assertions are made over: the
// lowercase ASCII letters, the digits, and the three punctuation characters a
// file name carries freely. It admits the full stop, which is what gives the
// last-character option something to narrow.
func lowercaseNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
}

// notFullStop is the last-character alphabet the assertions narrow with.
func notFullStop(r rune) bool { return r != '.' }

// reservedForTest names the segments the reserved-name assertions refuse whole:
// two all-letter names and one carrying a full stop, so the rewrite is asserted
// on a segment the alphabet carries whole as well as on a short one.
func reservedForTest(segment string) bool {
	switch segment {
	case "con", "nul", "aux.txt":
		return true
	}
	return false
}

// optionCorpus is the shared corpus for the plus opener, extended with the
// segments each option changes the spelling of: a full stop at the end, a full
// stop alone and doubled, an opener before a final full stop, and the reserved
// names beside near misses that are not reserved.
func optionCorpus() []string {
	extra := []string{
		"a.", ".", "..", "a..", "a.b.", "a+.", "+.", "a+a.",
		"con", "nul", "aux.txt", "cona", "acon", "co", "aux", "aux.tx",
		"Con", "con.", "con.txt",
	}
	corpus := namesegmenttest.Corpus('+')
	seen := make(map[string]bool, len(corpus)+len(extra))
	for _, s := range corpus {
		seen[s] = true
	}
	for _, s := range extra {
		if !seen[s] {
			seen[s] = true
			corpus = append(corpus, s)
		}
	}
	return corpus
}

// TestAdmitsLastEscapesOnlyTheCharacterEndingASegment asserts the last-character
// alphabet narrows the last position and no other: a full stop inside a segment
// is carried as authored, one ending it is escaped, no encoded segment ends with a
// full stop, and the encoding stays injective, join-safe and recoverable over the
// corpus. The rows pin the spelling of each shape, so a regression that escaped
// every full stop, or none, fails on the row that shows it.
func TestAdmitsLastEscapesOnlyTheCharacterEndingASegment(t *testing.T) {
	enc := namesegment.NewEncoding('+', lowercaseNameRune, namesegment.AdmitsLast(notFullStop))
	for _, tc := range []struct{ in, want string }{
		{"a.b", "a.b"},
		{"a.", "a+zd"},
		{".", "+zd"},
		{"..", ".+zd"},
		{"1.0.0", "1.0.0"},
		{"a+.", "a+zx00002b+zd"},
		{"a+b", "a+b"},
	} {
		if got := enc.Segment(tc.in); got != tc.want {
			t.Errorf("Segment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	assertEncodingProperties(t, enc, '+', optionCorpus())
	for _, s := range optionCorpus() {
		if encoded := enc.Segment(s); strings.HasSuffix(encoded, ".") {
			t.Errorf("segment %q encoded to %q, which ends with a full stop", s, encoded)
		}
	}
}

// TestReservedEscapesTheFirstCharacterOfAReservedSegment asserts a segment the
// predicate holds for is written with its first character escaped, that a near
// miss is written as authored, that no encoded segment is one the predicate holds
// for, and that the encoding stays injective, join-safe and recoverable.
func TestReservedEscapesTheFirstCharacterOfAReservedSegment(t *testing.T) {
	enc := namesegment.NewEncoding('+', lowercaseNameRune, namesegment.Reserved(reservedForTest))
	for _, tc := range []struct{ in, want string }{
		{"con", "+zx000063on"},
		{"nul", "+zx00006eul"},
		{"aux.txt", "+zx000061ux.txt"},
		{"cona", "cona"},
		{"aux", "aux"},
	} {
		if got := enc.Segment(tc.in); got != tc.want {
			t.Errorf("Segment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	assertEncodingProperties(t, enc, '+', optionCorpus())
	for _, s := range optionCorpus() {
		if encoded := enc.Segment(s); reservedForTest(encoded) {
			t.Errorf("segment %q encoded to the reserved %q", s, encoded)
		}
	}

	// The rewrite applies to each segment of an assembled name.
	if got, want := enc.Name("con", "a"), "+zx000063on++a"; got != want {
		t.Errorf("Name(con, a) = %q, want %q", got, want)
	}
}

// TestBothOptionsTogetherKeepTheEncodingSound asserts the two options compose: a
// segment that is reserved and ends in a full stop, and a single reserved
// character, are both written so the predicate does not hold and nothing ends in
// a full stop, and the properties hold over the corpus.
func TestBothOptionsTogetherKeepTheEncodingSound(t *testing.T) {
	enc := namesegment.NewEncoding('+', lowercaseNameRune,
		namesegment.AdmitsLast(notFullStop), namesegment.Reserved(reservedForTest))
	assertEncodingProperties(t, enc, '+', optionCorpus())
	for _, s := range optionCorpus() {
		encoded := enc.Segment(s)
		if reservedForTest(encoded) || strings.HasSuffix(encoded, ".") {
			t.Errorf("segment %q encoded to %q, which the options should have kept it from", s, encoded)
		}
	}
}

// TestReadSegmentReversesSegmentUnderEveryAlphabet asserts the encoding's own
// reader returns each corpus segment from its encoding and reports it as the
// encoding's own, under every alphabet, and agrees with the shared harness's
// reader — which is written independently of the encoder, so the agreement is a
// cross-check rather than a restatement.
func TestReadSegmentReversesSegmentUnderEveryAlphabet(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.opener, a.admits)
			for _, s := range namesegmenttest.Corpus(a.opener) {
				encoded := enc.Segment(s)
				got, ok := enc.ReadSegment(encoded)
				if !ok || got != s {
					t.Fatalf("ReadSegment(%q) = (%q, %v), want (%q, true)", encoded, got, ok, s)
				}
				if harness := namesegmenttest.DecodeSegment(t, a.opener, encoded); harness != got {
					t.Fatalf("ReadSegment(%q) = %q, the harness reads %q", encoded, got, harness)
				}
			}
		})
	}
}

// TestReadSegmentRefusesWhatTheEncodingWouldNotWrite asserts the reader reports a
// string as the encoding's own only where the encoding writes exactly that
// string: an unknown selector, a truncated escape, an escape spelling an admitted
// character, a carried opener the encoding would have escaped, and an uppercase
// hex payload are each refused, and the positive rows are each accepted.
func TestReadSegmentRefusesWhatTheEncodingWouldNotWrite(t *testing.T) {
	enc := namesegment.NewEncoding('+', lowercaseNameRune,
		namesegment.AdmitsLast(notFullStop), namesegment.Reserved(reservedForTest))
	for _, tc := range []struct {
		name string
		in   string
		ok   bool
		want string
	}{
		{"a plain name", "api-server", true, "api-server"},
		{"an escaped colon", "moo+zx00003a0.2.4", true, "moo:0.2.4"},
		{"the absent mark", "+ze", true, ""},
		{"a reserved name, rewritten", "+zx000063on", true, "con"},
		{"an unknown selector", "a+zq", false, ""},
		{"a truncated code point", "a+zx0000", false, ""},
		{"a code point spelling an admitted character", "+zx000061", false, ""},
		{"a reserved name as written", "con", false, ""},
		{"a final full stop as written", "a.", false, ""},
		{"a doubled opener", "a++b", false, ""},
		{"an uppercase hex payload", "+zx00004A", false, ""},
		{"an uppercase letter", "Api", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := enc.ReadSegment(tc.in)
			if ok != tc.ok {
				t.Fatalf("ReadSegment(%q) reported %v, want %v (read %q)", tc.in, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("ReadSegment(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNewEncodingRefusesAnUnsoundOption asserts the constructor refuses a
// last-character alphabet that would leave an escape ending a segment outside it,
// and a reserved-name predicate the rewrite could never clear, and accepts the
// sound pair the other assertions are made with.
func TestNewEncodingRefusesAnUnsoundOption(t *testing.T) {
	for _, tc := range []struct {
		name        string
		options     []namesegment.Option
		wantRefusal bool
	}{
		{"sound options are accepted", []namesegment.Option{
			namesegment.AdmitsLast(notFullStop), namesegment.Reserved(reservedForTest)}, false},
		{"a last alphabet refusing a hex digit", []namesegment.Option{
			namesegment.AdmitsLast(func(r rune) bool { return r != 'f' })}, true},
		{"a last alphabet refusing a selector", []namesegment.Option{
			namesegment.AdmitsLast(func(r rune) bool { return r != 'e' })}, true},
		{"a reserved predicate holding for a segment opening with the opener", []namesegment.Option{
			namesegment.Reserved(func(s string) bool { return strings.HasPrefix(s, "+") })}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused, message := optionRefusal(tc.options...)
			if refused != tc.wantRefusal {
				t.Fatalf("refused = %v, want %v (message %q)", refused, tc.wantRefusal, message)
			}
		})
	}
}

// optionRefusal reports whether NewEncoding refused the options over the
// assertions' alphabet and opener, and what it said.
func optionRefusal(options ...namesegment.Option) (refused bool, message string) {
	defer func() {
		if r := recover(); r != nil {
			refused = true
			if s, ok := r.(string); ok {
				message = s
			}
		}
	}()
	namesegment.NewEncoding('+', lowercaseNameRune, options...)
	return false, ""
}

// assertEncodingProperties asserts, over corpus, the four properties every
// encoding owes whatever options it was built with: no two segments share an
// encoding, no encoded segment carries the join or ends with the opener, every
// character stays inside the alphabet or is the opener, and the encoding's reader
// and the harness's both return each segment.
func assertEncodingProperties(t *testing.T, enc *namesegment.Encoding, opener rune, corpus []string) {
	t.Helper()
	seen := make(map[string]string, len(corpus))
	for _, s := range corpus {
		encoded := enc.Segment(s)
		if prior, clash := seen[encoded]; clash {
			t.Fatalf("segments %q and %q both encode to %q", prior, s, encoded)
		}
		seen[encoded] = s
		if strings.Contains(encoded, enc.SegmentSeparator()) || strings.HasSuffix(encoded, string(opener)) {
			t.Fatalf("segment %q encoded to %q, which carries the join or ends with the opener", s, encoded)
		}
		for _, r := range encoded {
			if r != opener && !lowercaseNameRune(r) {
				t.Fatalf("segment %q encoded to %q, which carries the unadmitted %q", s, encoded, r)
			}
		}
		if got, ok := enc.ReadSegment(encoded); !ok || got != s {
			t.Fatalf("ReadSegment(%q) = (%q, %v), want (%q, true)", encoded, got, ok, s)
		}
		if got := namesegmenttest.DecodeSegment(t, opener, encoded); got != s {
			t.Fatalf("the harness read %q back as %q, want %q", encoded, got, s)
		}
	}
}
