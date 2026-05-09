// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package labels provides helpers for transforming arbitrary strings into
// values that satisfy Kubernetes label-value rules. The compiler emits
// labels on rendered resources for provenance, attribution, and tooling
// integration; some label values originate from internal identifiers
// (e.g., synthetic NodeIDs for FR-emitted resources) that contain
// characters K8s rejects. This package centralises the transformation
// rule so every emission site sanitizes consistently.
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
// Use cases:
//
//   - FR-emitted resource NodeIDs use a synthetic marker syntax like
//     `<boundary-fr>:bridge@cluster` that must be sanitized before
//     emission as the `aggrega.ai/component-id` label value. Result:
//     `boundary-fr-bridge-cluster`.
//   - Future emission sites that derive label values from internal
//     identifiers (e.g., dispatch keys, fragment names, synthetic
//     instance IDs) can call this helper rather than reimplementing
//     the rule. The compose engine accepts unrestricted label values
//     today; the helper still applies for provenance-uniformity across
//     engines and for future portability when boundary-scope FRs ship
//     to K8s targets.
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
