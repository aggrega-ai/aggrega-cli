// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// assertComposedDiagnostic pins, against a diagnostic STREAM rather than an
// error value, the composition every diagnostic naming a halting condition
// follows: one line opens with the condition's registered sentence, the
// occurrence's own values follow it in parentheses, and the sentence is stated
// exactly ONCE on that line. The values the caller names must all land on that
// same line.
//
// The sentence is read off the condition through [cerrors.MessageOf] rather than
// written out at the call site, and that is the point of the helper. A test that
// spells the wording out is a second copy of a sentence that already has one
// home beside the condition's identity, and the two agree until one is edited —
// at which point the test keeps passing on text the product no longer prints.
//
// Three properties are asserted together because each is blind on its own:
//
//   - The sentence LEADS. A reader scanning a wall of diagnostics reads the
//     leftmost words of each line, and the sentence is the only part that says
//     which condition was met. Merely finding the sentence somewhere in the
//     stream would also be satisfied by the shape where a locator is pasted in
//     front of it.
//   - Its values follow it in PARENTHESES. This is what distinguishes a composed
//     diagnostic from a bare sentence with the occurrence's detail lost.
//   - The sentence is stated exactly ONCE. A diagnostic that opens with the right
//     sentence and also embeds it further along says one thing twice, and a
//     leading-position assertion cannot see that.
//
// Requiring the caller's values on the one composed line, rather than anywhere in
// the stream, is what keeps the assertion from passing on a diagnostic about some
// other key of the same document that merely mentions this one elsewhere.
//
// A line's own indentation is not part of the composition, so it is trimmed
// before the line is read; what leads is the line's content.
func assertComposedDiagnostic(t *testing.T, stream string, condition error, values ...string) {
	t.Helper()

	sentence, ok := cerrors.MessageOf(condition)
	if !ok || sentence == "" {
		t.Fatalf("the condition under test carries no registered sentence, so there is nothing to compose against")
	}

	opening := sentence + " ("
	composed := ""
	for _, line := range strings.Split(stream, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, opening) {
			composed = trimmed
			break
		}
	}
	if composed == "" {
		t.Errorf("no diagnostic opens with the condition's registered sentence followed by its values\n  sentence: %q\n  stream:\n%s", sentence, stream)
		return
	}
	if n := strings.Count(composed, sentence); n != 1 {
		t.Errorf("the diagnostic states the registered sentence %d times; want exactly 1\n  diagnostic: %q", n, composed)
	}
	for _, want := range values {
		if !strings.Contains(composed, want) {
			t.Errorf("the composed diagnostic does not carry %q\n  diagnostic: %q", want, composed)
		}
	}
}
