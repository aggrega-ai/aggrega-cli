// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// TestExportName asserts the deterministic export-name scheme for representative
// inputs: the aggrega prefix, the four ordered segments joined by the two-hyphen
// separator, an authored hyphen carried through as authored, and an escape for
// every character the name is not built from.
func TestExportName(t *testing.T) {
	cases := []struct {
		name                                      string
		release, boundary, component, providesKey string
		want                                      string
	}{
		{
			name:        "plain segments",
			release:     "prod",
			boundary:    "api",
			component:   "orders-db",
			providesKey: "host",
			want:        "aggrega--prod--api--orders-db--host",
		},
		{
			name:        "boundary path slash escaped, authored hyphen kept",
			release:     "prod",
			boundary:    "prod/api",
			component:   "orders-db",
			providesKey: "host",
			want:        "aggrega--prod--prod-zsapi--orders-db--host",
		},
		{
			name:        "each punctuation class reaches its own escape",
			release:     "staging",
			boundary:    "eu_west/1",
			component:   "cache.primary",
			providesKey: "port",
			want:        "aggrega--staging--eu-zuwest-zs1--cache-zdprimary--port",
		},
		{
			name:        "absent release rendered as the absent-segment mark",
			release:     "",
			boundary:    "net",
			component:   "db",
			providesKey: "endpoint",
			want:        "aggrega---ze--net--db--endpoint",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExportName(tc.release, tc.boundary, tc.component, tc.providesKey)
			t.Logf("ExportName(%q, %q, %q, %q) = %q", tc.release, tc.boundary, tc.component, tc.providesKey, got)
			if got != tc.want {
				t.Fatalf("ExportName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExportNameDistinguishesInputsThatShareOneName pins, pair by pair, the three
// ways distinct authored inputs used to reach one export name. Each row is two
// inputs an author can legitimately write; the assertion is that the two names
// differ.
//
// Each row fails on a specific regression rather than on any change: mapping
// every unadmitted character to one character again collapses the punctuation
// row, joining the segments with a single hyphen again collapses the join row,
// and a fixed word standing in for an absent segment again collapses the absent
// row.
func TestExportNameDistinguishesInputsThatShareOneName(t *testing.T) {
	cases := []struct {
		name         string
		left, right  [4]string
		whatItGuards string
	}{
		{
			name:         "two punctuation characters inside one segment",
			left:         [4]string{"prod", "api", "payments-db", "host"},
			right:        [4]string{"prod", "api", "payments_db", "host"},
			whatItGuards: "distinct characters must not share one replacement",
		},
		{
			name:         "a hyphen on either side of the join",
			left:         [4]string{"a", "b-c", "d", "e"},
			right:        [4]string{"a", "b", "c-d", "e"},
			whatItGuards: "the join must be a sequence a segment cannot contain",
		},
		{
			name:         "an absent segment against the mark written out",
			left:         [4]string{"", "b", "c", "d"},
			right:        [4]string{"-ze", "b", "c", "d"},
			whatItGuards: "the absent-segment mark must be unreachable from authored text",
		},
		{
			name:         "an absent segment against a word standing in for one",
			left:         [4]string{"", "b", "c", "d"},
			right:        [4]string{"none", "b", "c", "d"},
			whatItGuards: "an absent segment must not be spelled as an authorable word",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left := ExportName(tc.left[0], tc.left[1], tc.left[2], tc.left[3])
			right := ExportName(tc.right[0], tc.right[1], tc.right[2], tc.right[3])
			t.Logf("%v -> %q", tc.left, left)
			t.Logf("%v -> %q", tc.right, right)
			if left == right {
				t.Fatalf("%v and %v both reach %q; %s", tc.left, tc.right, left, tc.whatItGuards)
			}
		})
	}
}

// TestStackNameDistinguishesInputsThatShareOneName asserts the same for the stack
// name, which is built from the same segment encoding and the same join. A stack
// name carries two segments rather than four, so the join pair is the one that
// reaches it.
func TestStackNameDistinguishesInputsThatShareOneName(t *testing.T) {
	if a, b := StackName("a-b", "c"), StackName("a", "b-c"); a == b {
		t.Fatalf("StackName(%q, %q) and StackName(%q, %q) both reach %q", "a-b", "c", "a", "b-c", a)
	}
	if a, b := StackName("", "c"), StackName("none", "c"); a == b {
		t.Fatalf("an absent release and a release named %q both reach the stack name %q", "none", a)
	}
	if a, b := StackName("a_b", "c"), StackName("a-b", "c"); a == b {
		t.Fatalf("releases %q and %q both reach the stack name %q", "a_b", "a-b", a)
	}
}

// TestExportNameIsInjective asserts that no two distinct segment tuples reach one
// export name, over the full cross product of the shared segment corpus.
//
// A collision is reported with both tuples that produced it, so a regression
// names the two inputs rather than only the count.
func TestExportNameIsInjective(t *testing.T) {
	corpus := namesegmenttest.Corpus('-')
	seen := make(map[string][4]string)
	total := 0
	for _, a := range corpus {
		for _, b := range corpus {
			for _, c := range corpus {
				for _, d := range corpus {
					tuple := [4]string{a, b, c, d}
					name := ExportName(a, b, c, d)
					total++
					if prior, clash := seen[name]; clash {
						t.Fatalf("%v and %v both reach the export name %q", prior, tuple, name)
					}
					seen[name] = tuple
				}
			}
		}
	}
	if len(seen) != total {
		t.Fatalf("built %d names from %d tuples; every tuple must reach its own name", len(seen), total)
	}
	t.Logf("%d distinct segment tuples reached %d distinct export names", total, len(seen))
}

// TestStackNameIsInjective asserts the same property for the two-segment stack
// name. It is a separate assertion rather than a corollary because the stack name
// is a separate name space with its own arity: an export-name-only proof would
// leave a stack-name regression unobserved.
func TestStackNameIsInjective(t *testing.T) {
	corpus := namesegmenttest.Corpus('-')
	seen := make(map[string][2]string)
	total := 0
	for _, a := range corpus {
		for _, b := range corpus {
			tuple := [2]string{a, b}
			name := StackName(a, b)
			total++
			if prior, clash := seen[name]; clash {
				t.Fatalf("%v and %v both reach the stack name %q", prior, tuple, name)
			}
			seen[name] = tuple
		}
	}
	if len(seen) != total {
		t.Fatalf("built %d names from %d tuples; every tuple must reach its own name", len(seen), total)
	}
}

// TestExportNameSegmentsAreRecoverable reads an assembled export name back into
// the four segments it was built from, over the same corpus, and asserts each one
// comes back exactly as supplied.
//
// Distinctness alone says no two tuples in the corpus collided; recovering the
// segments says why — the name still carries them. The reader is written against
// the encoding's stated contract and deliberately shares no code with the
// encoder: a reader that called into the encoder would agree with it whatever the
// encoder did, which is the one thing this assertion must not do.
func TestExportNameSegmentsAreRecoverable(t *testing.T) {
	corpus := namesegmenttest.Corpus('-')
	for _, a := range corpus {
		for _, b := range corpus {
			for _, c := range corpus {
				for _, d := range corpus {
					want := []string{"aggrega", a, b, c, d}
					name := ExportName(a, b, c, d)
					got := namesegmenttest.ReadName(t, '-', name)
					if len(got) != len(want) {
						t.Fatalf("export name %q read back as %d parts, want %d", name, len(got), len(want))
					}
					for i := range want {
						if got[i] != want[i] {
							t.Fatalf("export name %q read back part %d as %q, want %q", name, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

// TestEncodedSegmentReservesTheJoin asserts the two structural properties the
// join rests on, over the whole corpus: an encoded segment never contains two
// hyphens in a row, and never ends with one. Without both, a run of hyphens in an
// assembled name would not say where the join is.
func TestEncodedSegmentReservesTheJoin(t *testing.T) {
	for _, s := range namesegmenttest.Corpus('-') {
		encoded := exportNameEncoding.Segment(s)
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
}

// TestEncodedSegmentUsesOnlyTheNameAlphabet asserts every byte an encoded segment
// emits is an ASCII letter, digit, or hyphen — the characters the export and
// stack names this core assembles are built from. The corpus deliberately
// includes a space, a tab, and a non-ASCII letter, none of which may survive.
func TestEncodedSegmentUsesOnlyTheNameAlphabet(t *testing.T) {
	for _, s := range namesegmenttest.Corpus('-') {
		encoded := exportNameEncoding.Segment(s)
		for i := 0; i < len(encoded); i++ {
			c := encoded[i]
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
			if !ok {
				t.Fatalf("segment %q encoded to %q, which carries byte %q at %d", s, encoded, c, i)
			}
		}
	}
}

// TestEncodedSegmentCarriesInvalidBytesDistinctly asserts a byte that begins no
// valid character is carried by its own value rather than folded onto the
// replacement character, so two segments differing only in such a byte stay
// distinct. Without this the encoding would be lossy exactly where its input is
// least controlled.
func TestEncodedSegmentCarriesInvalidBytesDistinctly(t *testing.T) {
	first := exportNameEncoding.Segment("a\xffb")
	second := exportNameEncoding.Segment("a\xfeb")
	valid := exportNameEncoding.Segment("a�b")
	t.Logf("0xff -> %q, 0xfe -> %q, U+FFFD -> %q", first, second, valid)
	if first == second {
		t.Fatalf("bytes 0xff and 0xfe both encode to %q", first)
	}
	if first == valid || second == valid {
		t.Fatalf("an invalid byte and the replacement character share an encoding: %q / %q / %q", first, second, valid)
	}
}

// TestExportNameDeterministic asserts the export name is byte-stable across
// repeated calls with identical inputs (Deterministic Output invariant).
func TestExportNameDeterministic(t *testing.T) {
	first := ExportName("prod", "prod/api", "orders-db", "host")
	for i := 0; i < 8; i++ {
		if again := ExportName("prod", "prod/api", "orders-db", "host"); again != first {
			t.Fatalf("ExportName not stable on run %d: %q != %q", i, again, first)
		}
	}
}
