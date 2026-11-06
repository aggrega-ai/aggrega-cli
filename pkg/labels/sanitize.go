// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package labels turns the strings the compiler holds into values that satisfy
// Kubernetes label-value rules. The compiler stamps labels on rendered
// resources for provenance, attribution and tooling integration, and some of
// those values have to be built rather than copied.
//
// Which mechanic a caller needs is decided by what the value it holds is: a
// value composed of several authored parts, a node identity the compiler
// assembled, or one already-authored name with nothing to compose.
//
// Where a value identifies something composed of several authored parts,
// [ComposeComponentIdentity] COMPOSES it from those parts through an encoding
// that writes out what its alphabet — the lowercase letters and the digits —
// refuses instead of folding it away, so two identities differing at all reach
// two values, the value reads back into the parts it was built from, and it is a
// resource name a target can spend as well as a label value.
//
// Where a value is a node identity, [EncodeNodeIdentity] writes it into the
// alphabet every authored name already satisfies. A node one aggregate composes
// into another carries a qualified identity joining the outer name to the inner
// one, and the character that joins them is one no authored name may carry — so
// that character, and only that character, is written out as an escape and every
// ordinary authored name is returned byte for byte as it was written.
//
// Where a value carries one already-authored name and there is nothing to
// compose, [SanitizeForK8sLabelValue] rewrites that name into the permitted
// charset by substitution. That mapping is many-to-one, which is stated on the
// function itself: a caller needing two distinct inputs to reach two distinct
// values cannot get it from there.
package labels

import "strings"

// SanitizeForK8sLabelValue rewrites s into a string that satisfies the
// Kubernetes label-value spec (RFC 1123 + DNS_LABEL): max 63 characters;
// permitted characters [A-Za-z0-9_.-]; must start and end with an
// alphanumeric character; the empty string is also valid.
//
// Transformation rules, applied in order:
//
//  1. Every character outside the permitted set [A-Za-z0-9_.-] is
//     replaced with `-`.
//  2. Runs of consecutive `-` are collapsed to a single `-`.
//  3. Leading and trailing characters that are not alphanumeric (i.e.,
//     any of `-`, `_`, `.`) are trimmed.
//  4. The result is truncated to at most 63 characters; if truncation
//     leaves a trailing non-alphanumeric character, that suffix is
//     stripped to preserve the start/end alphanumeric rule.
//
// The mapping is MANY-TO-ONE, and that is a property of the rules above rather
// than an accident of this implementation: every character outside the
// permitted set becomes the same hyphen, runs of hyphens collapse, and the
// result is cut to length. So `a:b`, `a/b` and `a-b` all arrive at `a-b`.
//
// A caller that needs two distinct inputs to reach two distinct values must not
// use this. Compose the value from its parts instead — which is what
// [ComposeComponentIdentity] does, writing each refused character out rather
// than folding it onto a hyphen — or declare the result somewhere a duplicate
// is refused.
//
// The case the fold cannot damage is a label value carrying a single authored
// name that is already unique within the document naming it, where there are no
// parts to compose and nothing a separator could straddle — or one whose
// verbatim form travels beside it, so the folded value is a readable label and
// never the only record of the name. No component identity is of that shape: an
// identity an author wrote is carried through unmodified and held to the label
// rules by refusal, and every identity the compiler derives is composed from its
// parts.
//
// Returns "" when s is empty or when sanitization removes every
// character (e.g., a string of only invalid characters).
func SanitizeForK8sLabelValue(s string) string {
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isPermittedLabelChar(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	collapsed := collapseRuns(b.String(), '-')
	trimmed := strings.TrimFunc(collapsed, func(r rune) bool {
		return !isAlphanumeric(r)
	})
	if len(trimmed) <= 63 {
		return trimmed
	}
	out := trimmed[:63]
	out = strings.TrimRightFunc(out, func(r rune) bool {
		return !isAlphanumeric(r)
	})
	return out
}

func isPermittedLabelChar(r rune) bool {
	return isAlphanumeric(r) || r == '-' || r == '_' || r == '.'
}

func isAlphanumeric(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func collapseRuns(s string, c byte) string {
	if !strings.Contains(s, string([]byte{c, c})) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == c && prev == c {
			continue
		}
		b.WriteByte(ch)
		prev = ch
	}
	return b.String()
}
