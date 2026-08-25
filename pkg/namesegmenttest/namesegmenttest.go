// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package namesegmenttest is the shared harness the segment-encoding assertions
// are made with: the corpus they are established over, and a reader that turns
// an assembled name back into the segments it was built from.
//
// Both live here rather than beside each assertion because more than one package
// asserts these properties — the encoding itself, and each derivation built on
// it — and a corpus or a reader copied per caller would agree with its twin on
// the day it was copied and drift afterwards, which is precisely the kind of
// silent divergence these assertions exist to catch.
//
// The reader is written against the encoding's stated contract and deliberately
// shares no code with the encoder. A reader that called into the encoder would
// agree with it whatever the encoder did, which is the one thing it must not do.
package namesegmenttest

import (
	"strconv"
	"strings"
	"testing"
)

// Corpus returns the segment corpus the injectivity and recoverability
// properties are established over. It is chosen for the encoding's edges rather
// than for realism: the empty segment, the characters with their own escape, the
// escape mark itself, the absent-segment selector, a hyphen in every position
// that decides whether it is kept or escaped, a doubled hyphen, and text that
// spells an escape out.
//
// It also has to be closed under moving a hyphen across a join, or an
// injectivity assertion cannot see the join itself: for a one-character join,
// ("a-a", "a") and ("a", "a-a") assemble to one string, and only a corpus
// carrying both "a" and "a-a" contains that pair. A corpus that merely looks
// varied does not — every segment can be distinct and every assembled name still
// distinct, while the property the join carries goes untested.
//
// Each call returns a fresh slice, so a caller may reorder or extend its own copy
// without disturbing another's.
func Corpus() []string {
	return []string{
		"", "a", "z", "e", "1",
		"a-b", "a_b", "a.b", "a/b",
		"-a", "a-", "-", "--", "a--b",
		"a-z", "-z", "-ze", "-zh", "a-zhb",
		"none", "A", "aA", "a b", "a\tb", "é",
		"a-a", "b-a",
	}
}

// ReadName splits an assembled name at its joins and decodes each segment,
// returning the segments in the order the name carries them.
//
// The split is on the first two hyphens of any run of two or more, which is
// sound exactly because an encoded segment never contains two hyphens in a row
// and never ends with one — so the first such pair is always a join.
func ReadName(tb testing.TB, name string) []string {
	tb.Helper()
	var parts []string
	rest := name
	for {
		at := strings.Index(rest, "--")
		if at < 0 {
			return append(parts, DecodeSegment(tb, rest))
		}
		parts = append(parts, DecodeSegment(tb, rest[:at]))
		rest = rest[at+2:]
	}
}

// DecodeSegment reverses the segment encoding: a hyphen followed by the escape
// mark opens an escape whose selector fixes its payload, and every other byte
// stands for itself. The absent-segment mark decodes to the empty string.
//
// The decoding is independent of which characters the encoding admitted, because
// an escape is self-delimiting: what an alphabet admits decides which characters
// were escaped, never how an escape is read.
//
// A segment carrying an unreadable escape fails the calling test, since only an
// encoder defect can produce one.
func DecodeSegment(tb testing.TB, seg string) string {
	tb.Helper()
	if seg == "-ze" {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(seg); {
		if seg[i] != '-' || i+2 >= len(seg) || seg[i+1] != 'z' {
			out.WriteByte(seg[i])
			i++
			continue
		}
		switch seg[i+2] {
		case 'h':
			out.WriteByte('-')
			i += 3
		case 'u':
			out.WriteByte('_')
			i += 3
		case 'd':
			out.WriteByte('.')
			i += 3
		case 's':
			out.WriteByte('/')
			i += 3
		case 'x':
			if i+9 > len(seg) {
				tb.Fatalf("segment %q ends inside a code-point escape at %d", seg, i)
			}
			cp, err := strconv.ParseUint(seg[i+3:i+9], 16, 32)
			if err != nil {
				tb.Fatalf("segment %q carries an unreadable code-point escape at %d: %v", seg, i, err)
			}
			out.WriteRune(rune(cp))
			i += 9
		case 'b':
			if i+5 > len(seg) {
				tb.Fatalf("segment %q ends inside a byte escape at %d", seg, i)
			}
			raw, err := strconv.ParseUint(seg[i+3:i+5], 16, 8)
			if err != nil {
				tb.Fatalf("segment %q carries an unreadable byte escape at %d: %v", seg, i, err)
			}
			out.WriteByte(byte(raw))
			i += 5
		default:
			tb.Fatalf("segment %q carries an unknown escape selector %q at %d", seg, seg[i+2], i)
		}
	}
	return out.String()
}
