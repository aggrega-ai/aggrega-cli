// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import "testing"

// TestArmLiteral pins the rule that a literal string beginning with an opening
// bracket and ending with a closing bracket is written with one extra opening
// bracket, so ARM reads it as text rather than as an expression.
func TestArmLiteral(t *testing.T) {
	cases := map[string]string{
		"[x]": "[[x]",
		"[x":  "[x",
		"x]":  "x]",
		"a":   "a",
	}
	for in, want := range cases {
		if got := ArmLiteral(in); got != want {
			t.Errorf("ArmLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFormatLiteral pins the rewrite of text placed inside the first argument of
// format(): a single quote is doubled for the ARM string literal, and each brace
// is doubled for the composite format string.
func TestFormatLiteral(t *testing.T) {
	if got, want := FormatLiteral("it's {a}"), "it''s {{a}}"; got != want {
		t.Errorf("FormatLiteral(it's {a}) = %q, want %q", got, want)
	}
}

// TestStringLiteral pins the ARM string literal: single quotes around the text,
// each inner single quote doubled.
func TestStringLiteral(t *testing.T) {
	if got, want := StringLiteral("it's"), "'it''s'"; got != want {
		t.Errorf("StringLiteral(it's) = %q, want %q", got, want)
	}
}
