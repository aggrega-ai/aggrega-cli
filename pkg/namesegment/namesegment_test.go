// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package namesegment_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// alphabet is one admitted set the properties below are established over,
// carrying the predicate itself so an assertion can ask what the alphabet
// admitted rather than assume it.
type alphabet struct {
	name   string
	admits func(rune) bool
}

// alphabets are the admitted sets every property is asserted over. Each is
// chosen for a distinct edge of the parameterisation rather than for realism:
//
//   - asciiLetterOrDigit is the narrow shape a cross-stack export name is built
//     from, and is the one an in-tree consumer uses today.
//   - lettersDigitsDotUnderscore is wider on exactly the two punctuation
//     characters that have their own short escape, so it is where a character
//     stops being escaped and starts being carried.
//   - escapeCharactersOnly is the narrowest sound alphabet there is: the escape
//     mark, the seven selectors and the hex digits, and nothing else.
//   - unicodeLetterOrDigit admits characters whose encoding is more than one
//     byte, so a multi-byte character has to survive being carried as authored.
//   - admitsTheReplacementCharacter admits U+FFFD itself, which is the alphabet
//     that would fold every byte beginning no valid character onto one character
//     if the encoding consulted the alphabet before reading such a byte.
var alphabets = []alphabet{
	{
		name:   "asciiLetterOrDigit",
		admits: func(r rune) bool { return isASCIILetter(r) || isASCIIDigit(r) },
	},
	{
		name: "lettersDigitsDotUnderscore",
		admits: func(r rune) bool {
			return isASCIILetter(r) || isASCIIDigit(r) || r == '.' || r == '_'
		},
	},
	{
		name:   "escapeCharactersOnly",
		admits: func(r rune) bool { return strings.ContainsRune("0123456789abcdefhsuxz", r) },
	},
	{
		name:   "unicodeLetterOrDigit",
		admits: func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) },
	},
	{
		name: "admitsTheReplacementCharacter",
		admits: func(r rune) bool {
			return isASCIILetter(r) || isASCIIDigit(r) || r == utf8.RuneError
		},
	},
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// TestSegmentIsInjective asserts that no two distinct segments reach one encoded
// segment, over the whole corpus and under every alphabet. A collision is
// reported with both inputs that produced it, so a regression names the pair
// rather than only the count.
func TestSegmentIsInjective(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.admits)
			seen := make(map[string]string)
			for _, s := range namesegmenttest.Corpus() {
				encoded := enc.Segment(s)
				if prior, clash := seen[encoded]; clash {
					t.Fatalf("segments %q and %q both encode to %q", prior, s, encoded)
				}
				seen[encoded] = s
			}
			t.Logf("%d segments reached %d distinct encodings", len(namesegmenttest.Corpus()), len(seen))
		})
	}
}

// TestNameIsInjectiveAcrossSegmentsAndArity asserts that no two distinct segment
// lists reach one assembled name, under every alphabet — including lists of
// different lengths, which is the part a fixed-arity assertion cannot see. Every
// name built from one, two, or three corpus segments goes into one table, so a
// name reachable from both a shorter and a longer list is caught.
func TestNameIsInjectiveAcrossSegmentsAndArity(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.admits)
			corpus := namesegmenttest.Corpus()
			seen := make(map[string][]string)
			total := 0
			record := func(segments ...string) {
				name := enc.Name(segments...)
				total++
				if prior, clash := seen[name]; clash {
					t.Fatalf("%q and %q both reach the name %q", prior, segments, name)
				}
				seen[name] = append([]string(nil), segments...)
			}
			for _, x := range corpus {
				record(x)
				for _, y := range corpus {
					record(x, y)
					for _, z := range corpus {
						record(x, y, z)
					}
				}
			}
			if len(seen) != total {
				t.Fatalf("built %d names from %d segment lists; every list must reach its own name", len(seen), total)
			}
			t.Logf("%d segment lists reached %d distinct names", total, len(seen))
		})
	}
}

// TestNameSegmentsAreRecoverable reads an assembled name back into the segments
// it was built from, at one, two and three segments, under every alphabet, and
// asserts each segment comes back exactly as supplied.
//
// Distinctness alone says no two lists in the corpus collided; recovering the
// segments says why — the name still carries them.
func TestNameSegmentsAreRecoverable(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.admits)
			corpus := namesegmenttest.Corpus()
			check := func(want ...string) {
				name := enc.Name(want...)
				got := namesegmenttest.ReadName(t, name)
				if len(got) != len(want) {
					t.Fatalf("name %q read back as %d segments, want %d", name, len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("name %q read back segment %d as %q, want %q", name, i, got[i], want[i])
					}
				}
			}
			for _, x := range corpus {
				check(x)
				for _, y := range corpus {
					check(x, y)
					for _, z := range corpus {
						check(x, y, z)
					}
				}
			}
		})
	}
}

// TestEncodedSegmentReservesTheJoin asserts the two structural properties the
// join rests on, over the whole corpus and under every alphabet: an encoded
// segment never contains two hyphens in a row, and never ends with one. Without
// both, a run of hyphens in an assembled name would not say where the join is.
func TestEncodedSegmentReservesTheJoin(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.admits)
			for _, s := range namesegmenttest.Corpus() {
				encoded := enc.Segment(s)
				if strings.Contains(encoded, "--") {
					t.Fatalf("segment %q encoded to %q, which contains the join sequence", s, encoded)
				}
				if strings.HasSuffix(encoded, "-") {
					t.Fatalf("segment %q encoded to %q, which ends with a hyphen", s, encoded)
				}
				if encoded == "" {
					t.Fatalf("segment %q encoded to the empty string", s)
				}
			}
		})
	}
}

// TestEncodedSegmentStaysInsideItsAlphabet asserts every character an encoded
// segment carries is one the alphabet admits or the hyphen the encoding reserves
// — under every alphabet, including the narrowest sound one, where an escape's
// own characters are the whole of what is admitted.
//
// This is what the constructor's refusal protects: an alphabet omitting a
// character an escape emits would fail here, and it is refused before it can.
func TestEncodedSegmentStaysInsideItsAlphabet(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.admits)
			for _, s := range namesegmenttest.Corpus() {
				encoded := enc.Segment(s)
				for _, r := range encoded {
					if r != '-' && !a.admits(r) {
						t.Fatalf("segment %q encoded to %q, which carries the unadmitted character %q", s, encoded, r)
					}
				}
			}
		})
	}
}

// TestEncodedSegmentCarriesInvalidBytesDistinctly asserts a byte that begins no
// valid character is carried by its own value rather than folded onto the
// replacement character, so two segments differing only in such a byte stay
// distinct. Without this the encoding would be lossy exactly where its input is
// least controlled.
//
// It runs under every alphabet, and the alphabet admitting U+FFFD is the one
// that makes it bite: consulting the alphabet before reading a one-byte decode
// failure would carry every such byte through as one character, and the two
// invalid bytes below would collapse.
func TestEncodedSegmentCarriesInvalidBytesDistinctly(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.admits)
			first := enc.Segment("a\xffb")
			second := enc.Segment("a\xfeb")
			valid := enc.Segment("a�b")
			t.Logf("0xff -> %q, 0xfe -> %q, U+FFFD -> %q", first, second, valid)
			if first == second {
				t.Fatalf("bytes 0xff and 0xfe both encode to %q", first)
			}
			if first == valid || second == valid {
				t.Fatalf("an invalid byte and the replacement character share an encoding: %q / %q / %q", first, second, valid)
			}
			for _, encoded := range []string{first, second, valid} {
				if got := namesegmenttest.DecodeSegment(t, encoded); got == "" {
					t.Fatalf("segment %q decoded to nothing", encoded)
				}
			}
		})
	}
}

// TestSegmentAndNameAreDeterministic asserts the encoding is byte-stable across
// repeated calls with identical input, under every alphabet.
func TestSegmentAndNameAreDeterministic(t *testing.T) {
	for _, a := range alphabets {
		t.Run(a.name, func(t *testing.T) {
			enc := namesegment.NewEncoding(a.admits)
			segment := enc.Segment("prod/api.orders-db")
			name := enc.Name("prod", "prod/api", "orders-db", "host")
			for i := 0; i < 8; i++ {
				if again := enc.Segment("prod/api.orders-db"); again != segment {
					t.Fatalf("Segment not stable on run %d: %q != %q", i, again, segment)
				}
				if again := enc.Name("prod", "prod/api", "orders-db", "host"); again != name {
					t.Fatalf("Name not stable on run %d: %q != %q", i, again, name)
				}
			}
		})
	}
}

// TestTheAlphabetDecidesTheEncoding asserts the admitted alphabet is what it
// claims to be — a parameter. Two encodings differing only in their alphabet are
// each asked to encode one input, and the outputs differ: the wider alphabet
// carries the dot and the underscore as authored, and the narrower one escapes
// both.
//
// It fails on the specific regression of an alphabet being ignored or fixed:
// were the admitted set hard-wired, both call sites would return one string.
func TestTheAlphabetDecidesTheEncoding(t *testing.T) {
	narrow := namesegment.NewEncoding(func(r rune) bool { return isASCIILetter(r) || isASCIIDigit(r) })
	wide := namesegment.NewEncoding(func(r rune) bool {
		return isASCIILetter(r) || isASCIIDigit(r) || r == '.' || r == '_'
	})

	const input = "cache.primary_1"
	narrowed, widened := narrow.Segment(input), wide.Segment(input)
	t.Logf("narrow(%q) = %q", input, narrowed)
	t.Logf("wide(%q)   = %q", input, widened)
	if narrowed == widened {
		t.Fatalf("two alphabets both encode %q to %q, so the alphabet is not a parameter", input, narrowed)
	}
	if widened != input {
		t.Fatalf("the wider alphabet encoded %q to %q; it admits every character in it", input, widened)
	}
	if !strings.Contains(narrowed, "-zd") || !strings.Contains(narrowed, "-zu") {
		t.Fatalf("the narrower alphabet encoded %q to %q, which escapes neither the dot nor the underscore", input, narrowed)
	}

	// Two alphabets are two encodings, and each stays injective on its own. A
	// parameter that broke the property for one of them would be worse than no
	// parameter at all.
	for _, enc := range []*namesegment.Encoding{narrow, wide} {
		seen := make(map[string]string)
		for _, s := range namesegmenttest.Corpus() {
			encoded := enc.Segment(s)
			if prior, clash := seen[encoded]; clash {
				t.Fatalf("segments %q and %q both encode to %q", prior, s, encoded)
			}
			seen[encoded] = s
		}
	}
}

// TestNewEncodingRefusesAnUnsoundAlphabet asserts the constructor refuses every
// alphabet that would make the encoding ambiguous or push its output outside the
// alphabet, and accepts a sound one.
//
// The accepting row is the calibration: without it, a refusal assertion passes
// on a constructor that refuses everything.
func TestNewEncodingRefusesAnUnsoundAlphabet(t *testing.T) {
	sound := func(r rune) bool { return isASCIILetter(r) || isASCIIDigit(r) }

	cases := []struct {
		name         string
		admits       func(rune) bool
		wantRefusal  bool
		whatItGuards string
	}{
		{
			name:         "a sound alphabet is accepted",
			admits:       sound,
			wantRefusal:  false,
			whatItGuards: "the refusal must discriminate, not refuse everything",
		},
		{
			name:         "no alphabet at all",
			admits:       nil,
			wantRefusal:  true,
			whatItGuards: "an encoding with no alphabet would decide nothing",
		},
		{
			name:         "admits the hyphen",
			admits:       func(r rune) bool { return sound(r) || r == '-' },
			wantRefusal:  true,
			whatItGuards: "a carried hyphen lets a segment hold the separator and hides every escape",
		},
		{
			name:         "omits the escape mark",
			admits:       func(r rune) bool { return sound(r) && r != 'z' },
			wantRefusal:  true,
			whatItGuards: "every escape emits the mark, so output would leave the alphabet",
		},
		{
			name:         "omits a selector",
			admits:       func(r rune) bool { return sound(r) && r != 'h' },
			wantRefusal:  true,
			whatItGuards: "an escaped hyphen emits its selector",
		},
		{
			name:         "omits a hex digit",
			admits:       func(r rune) bool { return sound(r) && r != 'f' },
			wantRefusal:  true,
			whatItGuards: "a code-point escape emits hex digits",
		},
		{
			name:         "admits nothing",
			admits:       func(rune) bool { return false },
			wantRefusal:  true,
			whatItGuards: "an empty alphabet cannot carry an escape",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refused, message := refusal(tc.admits)
			if refused != tc.wantRefusal {
				t.Fatalf("refused = %v, want %v; %s (message %q)", refused, tc.wantRefusal, tc.whatItGuards, message)
			}
			if refused {
				t.Logf("refused with: %s", message)
			}
		})
	}
}

// refusal reports whether NewEncoding refused the alphabet, and what it said.
func refusal(admits func(rune) bool) (refused bool, message string) {
	defer func() {
		if r := recover(); r != nil {
			refused = true
			if s, ok := r.(string); ok {
				message = s
				return
			}
			if err, ok := r.(error); ok {
				message = err.Error()
			}
		}
	}()
	namesegment.NewEncoding(admits)
	return false, ""
}
