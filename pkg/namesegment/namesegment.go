// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package namesegment encodes the segments of a derived name into a restricted
// alphabet without losing what told two inputs apart.
//
// A derived name usually has to cross into an alphabet poorer than the one its
// input was written in: a name a deployment target accepts, built from an
// identity an author typed. Dropping or folding the characters the target does
// not accept is lossy — two inputs differing only in the dropped characters
// arrive at one name, and a name two inputs reach identifies neither. This
// package escapes instead, so distinct inputs always reach distinct names and an
// assembled name can be read back into the segments it was built from.
//
// # The scheme
//
// [Encoding.Segment] carries an admitted character through as itself and writes
// every other character as an escape: a hyphen, the escape mark, a selector
// naming what the escape stands for, and that selector's fixed-width payload.
// The payload width is fixed rather than trimmed, so an escape is
// self-delimiting and what follows one can never be read as part of it.
//
// A hyphen is kept as authored only where the character emitted after it is an
// admitted character other than the escape mark. Two properties follow, and both
// are load-bearing. An encoded segment never ends with a hyphen and never
// contains two in a row, which reserves a two-hyphen run for the join
// [Encoding.Name] writes between segments — so an assembled name divides back
// into exactly the segments it was built from. And a hyphen followed by the
// escape mark is always an escape, never an authored hyphen, so reading a
// segment back is unambiguous.
//
// An empty segment yields a mark whose selector stands for no character, so no
// non-empty input can produce it: an absent segment and a segment an author
// wrote stay distinguishable whatever the author wrote.
//
// A byte that begins no valid character is carried by its own value rather than
// folded onto the replacement character, so a segment that is not valid UTF-8
// still encodes distinctly — which is where an encoding's input is least
// controlled.
//
// # The admitted alphabet is the caller's
//
// Which characters are admitted is the caller's to state, because it is a
// property of the target a name is written for rather than of the encoding. The
// scheme's own characters are fixed, so an alphabet has to admit every character
// an escape emits and must not admit the hyphen; [NewEncoding] refuses one that
// does not.
//
// Every derivation here is deterministic: identical input yields byte-identical
// output.
package namesegment

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// escapeOpener opens every escape and composes the segment separator. It is the
// one character an admitted alphabet may not carry: were it carried through as
// authored, a segment could hold the separator, and an escape could not be told
// apart from a hyphen an author wrote.
const escapeOpener = '-'

// escapeMark is the character that, immediately after [escapeOpener], marks the
// start of an escape rather than an authored hyphen. A hyphen an author wrote
// before this character is escaped rather than kept, so the two readings never
// compete.
const escapeMark = 'z'

// segmentSeparator joins the encoded segments of an assembled name.
//
// It is two [escapeOpener] characters rather than one because an encoded segment
// may contain a single one and never two: a hyphen is kept only where the
// character emitted after it is admitted and is not [escapeMark], and every
// other character is written as an escape that begins with a hyphen and ends
// with an admitted character. So inside a segment every run of hyphens is
// exactly one long, and a segment can neither end with a hyphen nor contain a
// pair. A run of two or more therefore occurs only at a join, and the join is
// its first two hyphens — which is what lets the segments be told apart again,
// and so what makes an assembled name injective.
const segmentSeparator = string(escapeOpener) + string(escapeOpener)

// Escape selectors. Each names what the escape stands for and fixes how much
// payload follows it, so an escape is self-delimiting and the encoding of one
// character can never be read as the beginning of another.
//
// The four punctuation selectors are the characters derived names actually carry
// — a hyphen or an underscore inside a name an author typed, and the dot and
// slash a nested identity is composed from — so the common cases stay short and
// legible. Anything else falls to the code-point selector.
const (
	escapeHyphen     = 'h' // the hyphen U+002D
	escapeUnderscore = 'u' // the underscore U+005F
	escapeDot        = 'd' // the full stop U+002E
	escapeSlash      = 's' // the solidus U+002F
	escapeCodePoint  = 'x' // any other character, as six hex digits
	escapeByte       = 'b' // a byte carrying no valid character, as two hex digits
	escapeAbsent     = 'e' // a segment with no value at all; never written for any character
)

// absentSegmentMark is the whole-segment stand-in for a segment whose input is
// the empty string, so an absent segment still yields a well-formed, stable name
// rather than a name carrying an empty run between separators.
//
// It is an escape whose selector stands for no character, so no non-empty input
// can produce it: every escape written for a character carries one of the
// character selectors, and every other emitted byte is an admitted character or
// a hyphen kept between two of them.
const absentSegmentMark = string(escapeOpener) + string(escapeMark) + string(escapeAbsent)

// hexDigits is the payload alphabet of the two selectors that carry a value.
const hexDigits = "0123456789abcdef"

// Encoding is the segment encoding over one admitted alphabet. Build one with
// [NewEncoding] and reuse it: an Encoding holds no mutable state, so it is safe
// for concurrent use and every call with identical input yields identical
// output.
//
// The zero Encoding carries no alphabet and must not be used; [NewEncoding] is
// the only way to obtain a usable one, which is what keeps an unsound alphabet
// from reaching [Encoding.Segment].
type Encoding struct {
	admits func(rune) bool
}

// NewEncoding returns the encoding whose admitted alphabet is the set of
// characters admits reports true for. An admitted character is carried through
// as authored; every other character is escaped.
//
// admits has to be a pure function of its argument, because the encoding's
// determinism is exactly its determinism.
//
// # What an alphabet has to satisfy, and why an unsound one is refused here
//
// The scheme's own characters are fixed, so an alphabet is sound only where it
// admits every character an escape emits — the escape mark, the seven selectors,
// the ten digits and the six lowercase hex letters — and does not admit the
// hyphen, which
// opens every escape and composes the separator. An alphabet failing either
// condition yields output that is ambiguous, or that leaves the very alphabet it
// was encoded into.
//
// Both conditions are checked here and a failure panics, because an alphabet is
// a fixed property of the caller rather than anything a build encounters: it is
// settled when the caller is written, so a failure is a defect in the caller and
// the first call reaching this constructor reports it. Returning an error
// instead would put the report at a call site that has no answer for it.
func NewEncoding(admits func(rune) bool) *Encoding {
	if admits == nil {
		panic("namesegment: NewEncoding needs an alphabet, and was given none")
	}
	if admits(escapeOpener) {
		panic(fmt.Sprintf("namesegment: the alphabet admits %q, which opens every escape and composes "+
			"the segment separator, so an escape could not be told apart from an authored hyphen", escapeOpener))
	}
	for _, r := range escapeCharacters() {
		if !admits(r) {
			panic(fmt.Sprintf("namesegment: the alphabet does not admit %q, which an escape emits, "+
				"so an encoded segment would leave the alphabet it is encoded into", r))
		}
	}
	return &Encoding{admits: admits}
}

// Segment encodes one segment into the admitted alphabet and the hyphen.
//
// The encoding is injective: distinct inputs always produce distinct output. An
// admitted character is kept as authored. A hyphen is kept where the character
// emitted after it is admitted and is not the escape mark, which is the ordinary
// case in an authored name. Every other character, and every hyphen that does
// not qualify, is written as an escape.
//
// An empty input yields the absent-segment mark, which carries a selector that
// stands for no character and so cannot be produced by any non-empty input.
func (e *Encoding) Segment(s string) string {
	if s == "" {
		return absentSegmentMark
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// A byte that begins no valid character is carried by its own value,
			// so a segment that is not valid UTF-8 still encodes distinctly
			// rather than collapsing onto the replacement character. This is
			// read before the alphabet is consulted, because an alphabet
			// admitting the replacement character would otherwise fold every
			// such byte onto it and lose the distinction — a one-byte decode
			// failure and an authored replacement character, which decodes as
			// three bytes, are different inputs.
			writeEscape(&b, escapeByte, uint32(s[i]), 2)
		case e.admits(r):
			b.WriteRune(r)
		case r == escapeOpener && e.keepsAuthoredOpener(s[i+size:]):
			b.WriteRune(escapeOpener)
		default:
			writeEscape(&b, selectorFor(r), uint32(r), 6)
		}
		i += size
	}
	return b.String()
}

// Name assembles a name from segments, encoding each with [Encoding.Segment] and
// joining them with the two-hyphen separator no encoded segment contains.
//
// Reading a name back at its joins yields exactly the segments it was built
// from, so two calls produce one name only where both were given the same
// segments in the same order — including where they were given different
// numbers of them, since the joins say how many there were. The name is
// byte-stable across calls with identical input.
func (e *Encoding) Name(segments ...string) string {
	encoded := make([]string, len(segments))
	for i, s := range segments {
		encoded[i] = e.Segment(s)
	}
	return strings.Join(encoded, segmentSeparator)
}

// keepsAuthoredOpener reports whether an authored hyphen may be emitted as a
// hyphen, given what follows it in the input. It may where the next character is
// admitted and is not the escape mark: that character is emitted as itself, so
// the hyphen is neither left at the end of the segment, nor placed against the
// hyphen that opens another escape, nor left looking like the start of one.
//
// A byte beginning no valid character never qualifies, whatever the alphabet
// admits, because such a byte is always written as an escape and an escape opens
// with a hyphen.
func (e *Encoding) keepsAuthoredOpener(rest string) bool {
	if rest == "" {
		return false
	}
	r, size := utf8.DecodeRuneInString(rest)
	if r == utf8.RuneError && size == 1 {
		return false
	}
	return e.admits(r) && r != escapeMark
}

// selectorFor returns the selector naming the character an escape stands for.
// The punctuation an authored or composed identity actually carries has its own
// selector so the escape stays short; everything else is carried by code point.
func selectorFor(r rune) rune {
	switch r {
	case escapeOpener:
		return escapeHyphen
	case '_':
		return escapeUnderscore
	case '.':
		return escapeDot
	case '/':
		return escapeSlash
	default:
		return escapeCodePoint
	}
}

// writeEscape writes one escape: the opening hyphen, the escape mark, the
// selector, and — for the two selectors that carry a value — that value as a
// fixed run of lowercase hex digits. The run is fixed rather than trimmed so an
// escape is self-delimiting and what follows it can never be read as part of it.
func writeEscape(b *strings.Builder, selector rune, value uint32, digits int) {
	b.WriteRune(escapeOpener)
	b.WriteRune(escapeMark)
	b.WriteRune(selector)
	if selector != escapeCodePoint && selector != escapeByte {
		return
	}
	for shift := (digits - 1) * 4; shift >= 0; shift -= 4 {
		b.WriteByte(hexDigits[(value>>uint(shift))&0xF])
	}
}

// escapeCharacters returns every character an escape can emit after its opening
// hyphen. An admitted alphabet has to carry all of them, or an encoded segment
// would leave the alphabet it was encoded into.
func escapeCharacters() []rune {
	emitted := []rune{
		escapeMark,
		escapeHyphen, escapeUnderscore, escapeDot, escapeSlash,
		escapeCodePoint, escapeByte, escapeAbsent,
	}
	for _, r := range hexDigits {
		emitted = append(emitted, r)
	}
	return emitted
}
