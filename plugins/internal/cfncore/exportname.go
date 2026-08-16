// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"strings"
	"unicode/utf8"
)

// exportNamePrefix is the fixed leading segment stamped on every assembled
// export name, so a name this core produces is recognisable as Aggrega-authored
// and never collides with an unrelated export in the same account and region.
const exportNamePrefix = "aggrega"

// exportSegmentSeparator joins the encoded segments of an export or stack name.
//
// It is two hyphens rather than one because an encoded segment may contain a
// single hyphen and never two: [encodeExportSegment] keeps an authored hyphen
// only where the next character it emits is a letter or a digit, and writes
// every other character as an escape that begins with a hyphen and ends with a
// letter or a digit. So inside a segment every run of hyphens is exactly one
// long, and a segment can neither end with a hyphen nor contain a pair. A run of
// two or more therefore occurs only at a join, and the join is its first two
// hyphens — which is what lets the segments be told apart again, and so what
// makes the assembled name injective.
const exportSegmentSeparator = "--"

// exportEscapeMark is the character that, immediately after a hyphen, marks the
// start of an escape rather than an authored hyphen. A hyphen an author wrote
// before this character is therefore escaped rather than kept, so the two
// readings never compete.
const exportEscapeMark = 'z'

// Escape selectors. Each names what the escape stands for and fixes how much
// payload follows it, so an escape is self-delimiting and the encoding of one
// character can never be read as the beginning of another.
//
// The four punctuation selectors are the characters authored names actually
// carry — a hyphen or an underscore inside a name an author typed, and the dot
// and slash the compiler composes a nested identity from — so the common cases
// stay short and legible. Anything else falls to the code-point selector.
const (
	exportEscapeHyphen     = 'h' // the hyphen U+002D
	exportEscapeUnderscore = 'u' // the underscore U+005F
	exportEscapeDot        = 'd' // the full stop U+002E
	exportEscapeSlash      = 's' // the solidus U+002F
	exportEscapeCodePoint  = 'x' // any other character, as six hex digits
	exportEscapeByte       = 'b' // a byte carrying no valid character, as two hex digits
	exportEscapeAbsent     = 'e' // a segment with no value at all; never written for any character
)

// absentSegmentMark is the whole-segment stand-in for a segment whose input is
// the empty string. An absent deployment identity, boundary, component, or
// provides key still yields a well-formed, stable name rather than a name
// carrying an empty run between separators.
//
// It is an escape whose selector stands for no character, so no non-empty input
// can produce it: every escape [encodeExportSegment] writes for a character
// carries one of the character selectors, and every other emitted byte is a
// letter, a digit, or a hyphen kept between two of them. An absent segment and a
// segment an author wrote are therefore always distinguishable, whatever the
// author wrote.
const absentSegmentMark = string('-') + string(exportEscapeMark) + string(exportEscapeAbsent)

// ExportName builds the deterministic CloudFormation export name for one
// cross-stack value, as the fixed prefix and four encoded segments joined by
// [exportSegmentSeparator]. The four caller-supplied segments carry, in order,
// the deployment/environment identity, the boundary path (a boundary is one
// stack), the component identity, and the provides key whose value the export
// publishes.
//
// The derivation is injective: two calls produce one name only where all four
// segments match. Each segment is encoded rather than filtered
// ([encodeExportSegment]), so segments differing only in punctuation stay
// distinct, and the two-hyphen join is a sequence no encoded segment contains,
// so a name cannot be re-divided into a different set of segments. The name is
// byte-stable across runs of identical input.
//
// The name is a deterministic pointer string: it is assembled ahead of time from
// the build's own identity and never resolved against any AWS state.
//
// # What the name is unique against, and where that stops
//
// Because the derivation is injective, one export name within a build belongs to
// exactly one deployment identity, boundary, component, and provides key. That
// is the whole of what a build can establish, and it is established here rather
// than assumed.
//
// An export name also has to be unique against every export already present in
// the account and region the stack is deployed into. That is a property of the
// deployment, not of the build, and it is beyond any derivation's reach rather
// than beyond this one's: the compiler does not authenticate, does not query,
// and cannot read the output of another build, so nothing it computes can
// observe what is already deployed. What carries the name across that boundary
// is the deployment identity segment leading it — two deployments sharing an
// account and region keep their exports apart by being named apart.
func ExportName(release, boundary, component, providesKey string) string {
	return strings.Join([]string{
		exportNamePrefix,
		encodeExportSegment(release),
		encodeExportSegment(boundary),
		encodeExportSegment(component),
		encodeExportSegment(providesKey),
	}, exportSegmentSeparator)
}

// StackName builds the deterministic CloudFormation stack name for one boundary,
// as the fixed prefix and two encoded segments joined by
// [exportSegmentSeparator]. A boundary assembles into one stack, so the boundary
// path scopes the name; the deployment/environment identity leads it.
//
// It is built the same way an export name is, and is injective for the same
// reason: two calls produce one stack name only where both segments match. The
// name is byte-stable across runs of identical input.
//
// The name is a deterministic pointer string: it is assembled ahead of time from
// the build's own identity and never resolved against any AWS state — a
// cross-stack read names the producer stack by this pointer, and the runtime
// environment resolves it at deploy time. As with an export name, uniqueness
// against stacks already deployed into the account and region belongs to the
// deployment rather than to the build.
func StackName(release, boundary string) string {
	return strings.Join([]string{
		exportNamePrefix,
		encodeExportSegment(release),
		encodeExportSegment(boundary),
	}, exportSegmentSeparator)
}

// encodeExportSegment encodes one segment into ASCII letters, digits, and the
// hyphen — the characters the names this core assembles are built from.
//
// The encoding is injective: distinct inputs always produce distinct output. A
// letter or digit is kept as authored. A hyphen is kept where the next character
// emitted after it is a letter or a digit other than [exportEscapeMark], which is
// the ordinary case in an authored name. Every other character, and every hyphen
// that does not qualify, is written as an escape: a hyphen, [exportEscapeMark],
// a selector naming what was replaced, and that selector's fixed payload.
//
// Two properties follow, and the assembled name's injectivity rests on both. A
// hyphen is emitted only where a letter or digit follows it, so the output never
// ends with a hyphen and never contains two in a row — which is what reserves a
// two-hyphen run for the join. And a hyphen followed by [exportEscapeMark] is
// always an escape, never an authored hyphen, so reading the output back is
// unambiguous.
//
// An empty input yields [absentSegmentMark], which carries a selector that stands
// for no character and so cannot be produced by any non-empty input.
func encodeExportSegment(s string) string {
	if s == "" {
		return absentSegmentMark
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case isExportWordRune(r):
			b.WriteRune(r)
		case r == '-' && keepsAuthoredHyphen(s[i+size:]):
			b.WriteByte('-')
		case r == utf8.RuneError && size == 1:
			// A byte that begins no valid character is carried by its own value,
			// so a segment that is not valid UTF-8 still encodes distinctly
			// rather than collapsing onto the replacement character.
			writeExportEscape(&b, exportEscapeByte, uint32(s[i]), 2)
		default:
			writeExportEscape(&b, exportEscapeSelector(r), uint32(r), 6)
		}
		i += size
	}
	return b.String()
}

// keepsAuthoredHyphen reports whether an authored hyphen may be emitted as a
// hyphen, given what follows it in the input. It may where the next character is
// a letter or a digit other than [exportEscapeMark]: that character is emitted as
// itself, so the hyphen is neither left at the end of the segment, nor placed
// against the hyphen that opens another escape, nor left looking like the start
// of one.
func keepsAuthoredHyphen(rest string) bool {
	if rest == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return isExportWordRune(r) && r != exportEscapeMark
}

// isExportWordRune reports whether a character is carried through the encoding
// as itself: an ASCII letter or digit.
func isExportWordRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// exportEscapeSelector returns the selector naming the character an escape
// stands for. The punctuation an authored or composed identity actually carries
// has its own selector so the escape stays short; everything else is carried by
// code point.
func exportEscapeSelector(r rune) rune {
	switch r {
	case '-':
		return exportEscapeHyphen
	case '_':
		return exportEscapeUnderscore
	case '.':
		return exportEscapeDot
	case '/':
		return exportEscapeSlash
	default:
		return exportEscapeCodePoint
	}
}

// writeExportEscape writes one escape: the opening hyphen, the escape mark, the
// selector, and — for the two selectors that carry a value — that value as a
// fixed run of lowercase hex digits. The run is fixed rather than trimmed so an
// escape is self-delimiting and what follows it can never be read as part of it.
func writeExportEscape(b *strings.Builder, selector rune, value uint32, digits int) {
	b.WriteByte('-')
	b.WriteRune(exportEscapeMark)
	b.WriteRune(selector)
	if selector != exportEscapeCodePoint && selector != exportEscapeByte {
		return
	}
	for shift := (digits - 1) * 4; shift >= 0; shift -= 4 {
		b.WriteByte("0123456789abcdef"[(value>>uint(shift))&0xF])
	}
}
