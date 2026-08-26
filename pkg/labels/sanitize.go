// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package labels turns the strings the compiler holds into values that satisfy
// Kubernetes label-value rules. The compiler stamps labels on rendered
// resources for provenance, attribution and tooling integration, and some of
// those values have to be built rather than copied.
//
// There are two mechanics here because there are two jobs, and which one a
// caller needs is decided by whether the value identifies one authored name or
// several parts.
//
// Where a value identifies something composed of several authored parts,
// [BoundaryFeatureComponentID] COMPOSES it from those parts through an encoding
// that writes out what the label alphabet refuses instead of folding it away —
// so two identities differing at all reach two values, and the value reads back
// into the parts it was built from.
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
// [BoundaryFeatureComponentID] does, writing each refused character out rather
// than folding it onto a hyphen — or declare the result somewhere a duplicate
// is refused.
//
// What is left here is the case the fold cannot damage: a label value carrying
// a single authored name that is already unique within the document naming it,
// where there are no parts to compose and nothing a separator could straddle.
// A feature attachment's own instance name, stamped on the component it is
// attached to, is that case.
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
