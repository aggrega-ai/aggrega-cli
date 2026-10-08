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
// every other character as an escape: the encoding's escape opener, the escape
// mark, a selector naming what the escape stands for, and that selector's
// fixed-width payload. The payload width is fixed rather than trimmed, so an
// escape is self-delimiting and what follows one can never be read as part of
// it.
//
// The opener is kept as authored only where the character emitted after it is an
// admitted character other than the escape mark. Two properties follow, and both
// are load-bearing. An encoded segment never ends with the opener and never
// contains two in a row, which reserves a two-opener run for the join
// [Encoding.Name] writes between segments — so an assembled name divides back
// into exactly the segments it was built from. And an opener followed by the
// escape mark is always an escape, never an opener an author wrote, so reading a
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
// # The admitted alphabet and the opener are both the caller's
//
// Which characters are admitted is the caller's to state, because it is a
// property of the target a name is written for rather than of the encoding. So
// is the opener, and for the same reason: a target whose alphabet refuses the
// punctuation a name is usually opened with still has to be written in
// something, and a target whose alphabet admits that punctuation may need to
// keep it. The scheme's own remaining characters are fixed, so an alphabet has
// to admit every character an escape emits and must not admit the opener;
// [NewEncoding] refuses one that does not.
//
// # Where a character sits, and names a target refuses whole
//
// Some targets refuse a name for where a character sits or for what the whole
// name spells rather than for any one character in it: a file name may not end
// in a character it accepts anywhere else, and a handful of names made entirely
// of accepted characters are reserved. Both are properties of the target, so
// both are the caller's to state, as options to [NewEncoding]: [AdmitsLast]
// narrows the alphabet for a segment's last character, and [Reserved] names the
// segments the target refuses whole. Each is met by writing one more character as
// an escape — the last one, or the first — so neither costs the encoding its
// injectivity: an escape always reads back as the character it stands for,
// whichever position it was written at.
//
// Every derivation here is deterministic: identical input yields byte-identical
// output.
package namesegment

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// escapeMark is the character that, immediately after an encoding's opener,
// marks the start of an escape rather than an opener an author wrote. An opener
// an author wrote before this character is escaped rather than kept, so the two
// readings never compete.
const escapeMark = 'z'

// Escape selectors. Each names what the escape stands for and fixes how much
// payload follows it, so an escape is self-delimiting and the encoding of one
// character can never be read as the beginning of another.
//
// The four punctuation selectors are the characters derived names actually carry
// — a hyphen or an underscore inside a name an author typed, and the dot and
// slash a nested identity is composed from — so the common cases stay short and
// legible. Anything else falls to the code-point selector.
//
// Each names a character rather than a role, which is what keeps the scheme
// sound under a caller-declared opener: an encoding whose opener is not the
// hyphen still writes an authored hyphen as the hyphen's own selector, so no two
// characters can reach one escape.
const (
	escapeHyphen     = 'h' // the hyphen U+002D
	escapeUnderscore = 'u' // the underscore U+005F
	escapeDot        = 'd' // the full stop U+002E
	escapeSlash      = 's' // the solidus U+002F
	escapeCodePoint  = 'x' // any other character, as six hex digits
	escapeByte       = 'b' // a byte carrying no valid character, as two hex digits
	escapeAbsent     = 'e' // a segment with no value at all; never written for any character
)

// hexDigits is the payload alphabet of the two selectors that carry a value.
const hexDigits = "0123456789abcdef"

// Encoding is the segment encoding over one admitted alphabet and one escape
// opener. Build one with [NewEncoding] and reuse it: an Encoding holds no
// mutable state, so it is safe for concurrent use and every call with identical
// input yields identical output.
//
// The zero Encoding carries no alphabet and must not be used; [NewEncoding] is
// the only way to obtain a usable one, which is what keeps an unsound alphabet
// from reaching [Encoding.Segment].
type Encoding struct {
	admits     func(rune) bool
	admitsLast func(rune) bool
	reserved   func(string) bool
	opener     rune
	separator  string
	absent     string
}

// An Option states a property of the target an encoding writes for beyond the
// characters it admits. Pass options to [NewEncoding]; an encoding built without
// any writes exactly what one over the same alphabet and opener always has.
type Option func(*Encoding)

// AdmitsLast narrows the admitted alphabet for the last character of a segment.
// A character the alphabet admits and last does not is written as an escape
// where it ends a segment, and carried as authored everywhere else; a character
// the alphabet does not admit is escaped wherever it sits, whatever last reports.
//
// It is for a target that refuses a name ending in a character it accepts
// anywhere else, such as a file name ending in a full stop. [NewEncoding] refuses
// a last alphabet that does not admit every character an escape can end with,
// because an escape ending a segment would otherwise leave the target's alphabet.
// last has to be a pure function of its argument.
func AdmitsLast(last func(rune) bool) Option {
	return func(e *Encoding) { e.admitsLast = last }
}

// Reserved names the segments a target refuses whole although it accepts every
// character in them, such as the device names a file system reserves. Where an
// encoded segment is one reserved reports true for, its first character is
// written as an escape rather than as authored, which yields a segment opening
// with the encoding's opener.
//
// reserved has to report false for every segment that opens with the opener —
// that is the shape the rewrite produces, so a segment the predicate held for in
// that shape could not be cleared by it — and [NewEncoding] refuses one that holds for the
// absent-segment mark, the one segment opening with the opener the encoding
// always writes. It applies to each segment [Encoding.Segment] writes, and so to
// each segment of a name [Encoding.Name] assembles. reserved has to be a pure
// function of its argument.
func Reserved(reserved func(segment string) bool) Option {
	return func(e *Encoding) { e.reserved = reserved }
}

// NewEncoding returns the encoding whose escape opener is opener and whose
// admitted alphabet is the set of characters admits reports true for. An
// admitted character is carried through as authored; every other character is
// escaped, and every escape opens with opener.
//
// admits has to be a pure function of its argument, because the encoding's
// determinism is exactly its determinism.
//
// # What the opener is for, and why the caller declares it
//
// The opener is the one character an admitted alphabet may not carry: were it
// carried through as authored, a segment could hold the separator, and an escape
// could not be told apart from an opener an author wrote. Which character can be
// spared is a property of the target rather than of the encoding — a target
// admitting only letters and digits has to spare one of those, while a target
// admitting punctuation can spare a character no name would otherwise use — so
// the caller states it alongside the alphabet it belongs to.
//
// # What an alphabet has to satisfy, and why an unsound one is refused here
//
// The scheme's remaining characters are fixed, so an alphabet is sound only
// where it admits every character an escape emits — the escape mark, the seven
// selectors, the ten digits and the six lowercase hex letters — and does not
// admit the opener. An alphabet failing either condition yields output that is
// ambiguous, or that leaves the very alphabet it was encoded into. The two
// conditions together also settle the opener itself: an opener that is one of
// the characters an escape emits would have to be both admitted and not
// admitted, so it is refused without a third check needing to name it.
//
// The opener has to be an ASCII character. An escape's payload and the separator
// are read back byte by byte by anything that divides an assembled name, so a
// multi-byte opener would make the separator two characters and four bytes wide
// and no reader assumes that.
//
// Every condition is checked here and a failure panics, because an alphabet and
// an opener are fixed properties of the caller rather than anything a build
// encounters: they are settled when the caller is written, so a failure is a
// defect in the caller and the first call reaching this constructor reports it.
// Returning an error instead would put the report at a call site that has no
// answer for it. The options are checked the same way, for the same reason.
func NewEncoding(opener rune, admits func(rune) bool, options ...Option) *Encoding {
	if admits == nil {
		panic("namesegment: NewEncoding needs an alphabet, and was given none")
	}
	if opener < 0 || opener > 0x7F {
		panic(fmt.Sprintf("namesegment: the escape opener %q is not an ASCII character, so the separator "+
			"would be wider than the one character a reader dividing a name assumes", opener))
	}
	if admits(opener) {
		panic(fmt.Sprintf("namesegment: the alphabet admits %q, which opens every escape and composes "+
			"the segment separator, so an escape could not be told apart from an opener an author wrote", opener))
	}
	for _, r := range escapeCharacters() {
		if !admits(r) {
			panic(fmt.Sprintf("namesegment: the alphabet does not admit %q, which an escape emits, "+
				"so an encoded segment would leave the alphabet it is encoded into", r))
		}
	}
	e := &Encoding{
		admits:    admits,
		opener:    opener,
		separator: string(opener) + string(opener),
		absent:    string(opener) + string(escapeMark) + string(escapeAbsent),
	}
	for _, option := range options {
		option(e)
	}
	if e.admitsLast != nil {
		for _, r := range escapeEndings() {
			if !e.admitsLast(r) {
				panic(fmt.Sprintf("namesegment: the last-character alphabet does not admit %q, which an escape "+
					"can end with, so a segment ending in that escape would leave the alphabet", r))
			}
		}
	}
	if e.reserved != nil && e.reserved(e.absent) {
		panic(fmt.Sprintf("namesegment: the reserved-name predicate holds for %q, a segment opening with the "+
			"opener, so writing a reserved segment's first character as an escape could not clear it", e.absent))
	}
	return e
}

// SegmentSeparator returns the sequence joining the encoded segments of a name
// this encoding assembles.
//
// It is two of the encoding's openers rather than one because an encoded segment
// may contain a single one and never two: the opener is kept only where the
// character emitted after it is admitted and is not [escapeMark], and every other
// character is written as an escape that begins with the opener and ends with an
// admitted character. So inside a segment every run of openers is exactly one
// long, and a segment can neither end with an opener nor contain a pair. A run of
// two or more therefore occurs only at a join, and the join is its first two
// openers — which is what lets the segments be told apart again, and so what
// makes an assembled name injective.
//
// The property rests on the encoding's own admitted set rather than on any
// target's alphabet, and the distinction is what lets an opener be declared per
// encoding: a target may perfectly well admit the character an encoding opens
// with, so long as the encoding itself does not carry it through — which
// [NewEncoding] refuses to build.
//
// # Why a caller needs it, rather than only [Encoding.Name]
//
// A caller that joins an assembled name to a further component — a suffix, or
// text this package did not write — needs that boundary to be as unambiguous as
// the joins inside the name, and this is the only sequence that is. Joining on
// one opener is unsound: an encoded segment may end in an admitted character and
// the appended text may begin with the opener, so one opener leaves the boundary
// indistinguishable from an opener either side happens to carry, and two distinct
// pairs assemble to one string. Joining on this instead keeps the assembled name
// readable back out of the result whatever was appended, because no encoded
// segment can contain it.
//
// It is exported for exactly that reason, and a caller spelling the two
// characters for itself would be holding a second copy of the one rule the whole
// encoding's injectivity rests on.
func (e *Encoding) SegmentSeparator() string { return e.separator }

// Segment encodes one segment into the admitted alphabet and the opener.
//
// The encoding is injective: distinct inputs always produce distinct output. An
// admitted character is kept as authored. The opener is kept where the character
// emitted after it is admitted and is not the escape mark, which is the ordinary
// case in an authored name. Every other character, and every opener that does
// not qualify, is written as an escape.
//
// An empty input yields the absent-segment mark, which carries a selector that
// stands for no character and so cannot be produced by any non-empty input.
//
// Where the encoding was built with [AdmitsLast], a character ending the segment
// is carried only where that alphabet admits it too; where it was built with
// [Reserved], a segment the predicate holds for has its first character written
// as an escape. Both leave the properties above intact, because an escape reads
// back as the character it stands for wherever it was written.
func (e *Encoding) Segment(s string) string {
	if s == "" {
		return e.absent
	}
	var b strings.Builder
	b.Grow(len(s))
	firstAsAuthored := false
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
			e.writeEscape(&b, escapeByte, uint32(s[i]), 2)
		case e.admitsAt(r, i+size == len(s)):
			firstAsAuthored = firstAsAuthored || i == 0
			b.WriteRune(r)
		case r == e.opener && e.keepsAuthoredOpener(s[i+size:]):
			b.WriteRune(e.opener)
		default:
			e.writeEscape(&b, selectorFor(r), uint32(r), 6)
		}
		i += size
	}
	encoded := b.String()
	if firstAsAuthored && e.reserved != nil && e.reserved(encoded) {
		// The first character was carried as authored, so it occupies the same
		// bytes at the front of the encoded segment as it does in the input.
		r, size := utf8.DecodeRuneInString(s)
		var escaped strings.Builder
		e.writeEscape(&escaped, selectorFor(r), uint32(r), 6)
		encoded = escaped.String() + encoded[size:]
	}
	return encoded
}

// ReadSegment returns the segment s is the encoding of, and whether s is exactly
// what [Encoding.Segment] writes for it.
//
// It reverses the scheme: the opener followed by the escape mark opens an escape
// whose selector fixes how much follows it, and every other byte stands for
// itself. The second result is false for a string carrying an escape the scheme
// does not define, and for one that reads back to a segment the encoding would
// have written differently — a name nothing but this encoding should have
// written, spelled some other way — so a caller reading names it did not write
// itself can tell its own apart from the rest. The first result is meaningful
// only where the second is true.
func (e *Encoding) ReadSegment(s string) (string, bool) {
	if s == e.absent {
		return "", true
	}
	open := byte(e.opener)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != open || i+2 >= len(s) || s[i+1] != escapeMark {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch s[i+2] {
		case escapeHyphen:
			b.WriteByte('-')
			i += 3
		case escapeUnderscore:
			b.WriteByte('_')
			i += 3
		case escapeDot:
			b.WriteByte('.')
			i += 3
		case escapeSlash:
			b.WriteByte('/')
			i += 3
		case escapeCodePoint:
			if i+9 > len(s) {
				return "", false
			}
			cp, err := strconv.ParseUint(s[i+3:i+9], 16, 32)
			if err != nil {
				return "", false
			}
			b.WriteRune(rune(cp))
			i += 9
		case escapeByte:
			if i+5 > len(s) {
				return "", false
			}
			raw, err := strconv.ParseUint(s[i+3:i+5], 16, 8)
			if err != nil {
				return "", false
			}
			b.WriteByte(byte(raw))
			i += 5
		default:
			return "", false
		}
	}
	segment := b.String()
	return segment, e.Segment(segment) == s
}

// Name assembles a name from segments, encoding each with [Encoding.Segment] and
// joining them with [Encoding.SegmentSeparator], which no encoded segment
// contains.
//
// Reading a name back at its joins yields exactly the segments it was built
// from, so two calls produce one name only where both were given the same
// segments in the same order — including where they were given different
// numbers of them, since the joins say how many there were. The name is
// byte-stable across calls with identical input.
//
// A caller appending anything to the result joins it with
// [Encoding.SegmentSeparator] too, for the reason stated there: any shorter
// boundary is ambiguous.
func (e *Encoding) Name(segments ...string) string {
	encoded := make([]string, len(segments))
	for i, s := range segments {
		encoded[i] = e.Segment(s)
	}
	return strings.Join(encoded, e.separator)
}

// keepsAuthoredOpener reports whether an authored opener may be emitted as the
// opener, given what follows it in the input. It may where the next character is
// admitted and is not the escape mark: that character is emitted as itself, so
// the opener is neither left at the end of the segment, nor placed against the
// opener that begins another escape, nor left looking like the start of one.
//
// A byte beginning no valid character never qualifies, whatever the alphabet
// admits, because such a byte is always written as an escape and an escape opens
// with the opener. Nor does a character that ends the segment where the
// last-character alphabet refuses it, for the same reason.
func (e *Encoding) keepsAuthoredOpener(rest string) bool {
	if rest == "" {
		return false
	}
	r, size := utf8.DecodeRuneInString(rest)
	if r == utf8.RuneError && size == 1 {
		return false
	}
	return e.admitsAt(r, size == len(rest)) && r != escapeMark
}

// admitsAt reports whether r is carried as authored at its place in a segment:
// admitted by the alphabet, and — where it ends the segment and the encoding was
// built with [AdmitsLast] — by the last-character alphabet too.
func (e *Encoding) admitsAt(r rune, last bool) bool {
	if !e.admits(r) {
		return false
	}
	return !last || e.admitsLast == nil || e.admitsLast(r)
}

// selectorFor returns the selector naming the character an escape stands for.
// The punctuation an authored or composed identity actually carries has its own
// selector so the escape stays short; everything else is carried by code point.
//
// Each arm names a character literally rather than reading the encoding's
// opener, and that is what keeps two characters from reaching one escape: an
// encoding whose opener is not the hyphen has to write both an authored hyphen
// and an authored opener, and reading the opener here would give them the same
// selector.
func selectorFor(r rune) rune {
	switch r {
	case '-':
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

// writeEscape writes one escape: the opener, the escape mark, the selector, and
// — for the two selectors that carry a value — that value as a fixed run of
// lowercase hex digits. The run is fixed rather than trimmed so an escape is
// self-delimiting and what follows it can never be read as part of it.
func (e *Encoding) writeEscape(b *strings.Builder, selector rune, value uint32, digits int) {
	b.WriteRune(e.opener)
	b.WriteRune(escapeMark)
	b.WriteRune(selector)
	if selector != escapeCodePoint && selector != escapeByte {
		return
	}
	for shift := (digits - 1) * 4; shift >= 0; shift -= 4 {
		b.WriteByte(hexDigits[(value>>uint(shift))&0xF])
	}
}

// escapeCharacters returns every character an escape can emit after its opener.
// An admitted alphabet has to carry all of them, or an encoded segment would
// leave the alphabet it was encoded into.
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

// escapeEndings returns every character an escape can end with: a selector that
// carries no value, or the last of a value's hex digits. A last-character
// alphabet has to carry all of them, or a segment ending in an escape would leave
// it.
func escapeEndings() []rune {
	endings := []rune{escapeHyphen, escapeUnderscore, escapeDot, escapeSlash, escapeAbsent}
	for _, r := range hexDigits {
		endings = append(endings, r)
	}
	return endings
}
