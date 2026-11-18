// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// lowercaseOrDigitRune is the alphabet the first-character assertions are made
// over: the lowercase ASCII letters and the digits, and nothing else. It does not
// admit the underscore, so the underscore is the opener those assertions use.
func lowercaseOrDigitRune(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }

// notDigit is the first-character alphabet the assertions narrow with.
func notDigit(r rune) bool { return r < '0' || r > '9' }

// assertWithinAlphabet asserts every encoded segment carries only the opener and
// characters the alphabet admits, which the shared property harness does not
// check against an alphabet narrower than its own.
func assertWithinAlphabet(t *testing.T, enc *namesegment.Encoding, opener rune, admits func(rune) bool, corpus []string) {
	t.Helper()
	for _, s := range corpus {
		for _, r := range enc.Segment(s) {
			if r != opener && !admits(r) {
				t.Fatalf("segment %q encoded to %q, which carries %q outside the alphabet", s, enc.Segment(s), r)
			}
		}
	}
}

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

// TestBothOptionsTogetherKeepTheEncodingSound asserts the options compose: a
// segment that is reserved and ends in a full stop, and a single reserved
// character, are both written so the predicate does not hold and nothing ends in
// a full stop, and the properties hold over the corpus. The same holds with the
// first-character option added, where a segment opening with a digit is also
// written so it no longer opens with one.
func TestBothOptionsTogetherKeepTheEncodingSound(t *testing.T) {
	for _, enc := range []*namesegment.Encoding{
		namesegment.NewEncoding('+', lowercaseNameRune,
			namesegment.AdmitsLast(notFullStop), namesegment.Reserved(reservedForTest)),
		namesegment.NewEncoding('+', lowercaseNameRune,
			namesegment.AdmitsFirst(notDigit), namesegment.AdmitsLast(notFullStop),
			namesegment.Reserved(reservedForTest)),
	} {
		assertEncodingProperties(t, enc, '+', optionCorpus())
		for _, s := range optionCorpus() {
			encoded := enc.Segment(s)
			if reservedForTest(encoded) || strings.HasSuffix(encoded, ".") {
				t.Errorf("segment %q encoded to %q, which the options should have kept it from", s, encoded)
			}
		}
	}
	enc := namesegment.NewEncoding('+', lowercaseNameRune,
		namesegment.AdmitsFirst(notDigit), namesegment.AdmitsLast(notFullStop),
		namesegment.Reserved(reservedForTest))
	for _, s := range optionCorpus() {
		if encoded := enc.Segment(s); encoded != "" && !notDigit(rune(encoded[0])) {
			t.Errorf("segment %q encoded to %q, which opens with a character the first option refuses", s, encoded)
		}
	}
}

// TestAdmitsFirstEscapesOnlyTheCharacterOpeningASegment asserts the first-character
// alphabet narrows the first position and no other: a digit opening a segment is
// escaped, the same digit inside one is carried as authored, the escape reads back
// to the segment it came from, and the encoding stays injective, join-safe and
// recoverable over the corpus.
func TestAdmitsFirstEscapesOnlyTheCharacterOpeningASegment(t *testing.T) {
	enc := namesegment.NewEncoding('_', lowercaseOrDigitRune, namesegment.AdmitsFirst(notDigit))
	for _, tc := range []struct{ in, want string }{
		{"1db", "_zx000031db"},
		{"db1", "db1"},
		{"1", "_zx000031"},
		{"a1", "a1"},
		{"11", "_zx0000311"},
	} {
		if got := enc.Segment(tc.in); got != tc.want {
			t.Errorf("Segment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got, ok := enc.ReadSegment(enc.Segment("1db")); !ok || got != "1db" {
		t.Errorf("ReadSegment(Segment(%q)) = (%q, %v), want (%q, true)", "1db", got, ok, "1db")
	}
	corpus := namesegmenttest.Corpus('_')
	assertEncodingProperties(t, enc, '_', corpus)
	assertWithinAlphabet(t, enc, '_', lowercaseOrDigitRune, corpus)
	for _, s := range corpus {
		if encoded := enc.Segment(s); encoded != "" && !notDigit(rune(encoded[0])) {
			t.Errorf("segment %q encoded to %q, which opens with a digit", s, encoded)
		}
	}
}

// TestAdmitsFirstKeepsTheEncodingInjective asserts the segments that differ only
// in where a digit or the opener sits encode distinctly, and that every name built
// from them reads back to the segments it was built from.
func TestAdmitsFirstKeepsTheEncodingInjective(t *testing.T) {
	enc := namesegment.NewEncoding('_', lowercaseOrDigitRune, namesegment.AdmitsFirst(notDigit))
	segments := []string{"1", "_1", "1_", "x1", "", "11"}
	seen := make(map[string]string, len(segments))
	for _, s := range segments {
		encoded := enc.Segment(s)
		if prior, clash := seen[encoded]; clash {
			t.Fatalf("segments %q and %q both encode to %q", prior, s, encoded)
		}
		seen[encoded] = s
	}
	corpus := namesegmenttest.Corpus('_')
	present := make(map[string]bool, len(corpus))
	for _, s := range corpus {
		present[s] = true
	}
	for _, s := range segments {
		if !present[s] {
			present[s] = true
			corpus = append(corpus, s)
		}
	}
	assertEncodingProperties(t, enc, '_', corpus)
	for _, a := range segments {
		for _, b := range segments {
			got := namesegmenttest.ReadName(t, '_', enc.Name(a, b))
			if len(got) != 2 || got[0] != a || got[1] != b {
				t.Fatalf("ReadName(Name(%q, %q)) = %q, want [%q %q]", a, b, got, a, b)
			}
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
// a first-character alphabet that refuses the opener every escape opens with, and
// a reserved-name predicate the rewrite could never clear, and accepts the sound
// options the other assertions are made with.
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
		{"a first alphabet admitting the opener", []namesegment.Option{
			namesegment.AdmitsFirst(notDigit)}, false},
		{"a first alphabet refusing the opener", []namesegment.Option{
			namesegment.AdmitsFirst(func(r rune) bool { return r != '+' })}, true},
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
