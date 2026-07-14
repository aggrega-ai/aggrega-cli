// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import "testing"

// TestIsAggregaExtensionKey pins the `x-aggrega-*` extension-keyword
// pattern recognition that makes catalog authoring of extension
// annotations (e.g. `x-aggrega-selector` on typed-reference fields)
// pass the compactform vocabulary check.
//
// The pattern catches the `x-aggrega-*` namespace specifically rather
// than the broader JSON Schema 2020-12 `x-*` extension prefix — catalog
// typos like `x-agrega-source` (single 'g') are rejected at compile time
// rather than silently passed through as unknown third-party extensions.
func TestIsAggregaExtensionKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key    string
		expect bool
	}{
		// Aggrega extension namespace
		{"x-aggrega-source", true},
		{"x-aggrega-validate", true},
		{"x-aggrega-when", true},
		{"x-aggrega-validateMessage", true},
		{"x-aggrega-selector", true},
		{"x-aggrega-narrowed-from", true}, // future extension; should pass through
		{"x-aggrega-anything-at-all", true},

		// Adjacent / non-matching
		{"x-aggrega", false},        // no trailing hyphen + suffix
		{"x-aggrega-", true},        // technically empty suffix, but prefix matches
		{"x-agrega-source", false},  // typo: single 'g'
		{"x-other-source", false},   // arbitrary third-party x-* extension
		{"X-Aggrega-Source", false}, // case-sensitive
		{"aggrega-source", false},   // missing x- prefix
		{"type", false},
		{"properties", false},
		{"", false},
	}

	for _, c := range cases {
		c := c
		t.Run(c.key, func(t *testing.T) {
			t.Parallel()
			if got := isAggregaExtensionKey(c.key); got != c.expect {
				t.Errorf("isAggregaExtensionKey(%q) = %v; want %v", c.key, got, c.expect)
			}
		})
	}
}

// TestIsKnownKeyword_AggregaExtensions verifies the vocabulary check
// accepts `x-aggrega-*` extension keywords. This is the path that makes
// authoring patterns like `target_agent: { type: string, x-aggrega-selector: "..." }`
// compile correctly — the keyword in `emitCompactConstraint`'s default
// branch passes `isKnownKeyword` and routes through the generic
// passthrough rather than raising `ErrUnknownKeyword`.
func TestIsKnownKeyword_AggregaExtensions(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"x-aggrega-selector",
		"x-aggrega-source",
		"x-aggrega-validate",
		"x-aggrega-when",
		"x-aggrega-validateMessage",
		"x-aggrega-narrowed-from",
	} {
		if !isKnownKeyword(key) {
			t.Errorf("isKnownKeyword(%q) = false; want true (x-aggrega-* extension)", key)
		}
	}

	for _, key := range []string{
		"x-agrega-source",     // typo
		"x-foreign-extension", // third-party
		"unknownkeyword",
	} {
		if isKnownKeyword(key) {
			t.Errorf("isKnownKeyword(%q) = true; want false", key)
		}
	}
}
