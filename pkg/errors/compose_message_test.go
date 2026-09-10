// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors_test

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

var errComposeSubject = cerrors.Register("compose-subject", cerrors.Meta{
	// A test's own short code, in the band this package's tests use. It is not
	// an allocation: this test binary registers only what its own test files
	// declare, so the value has to be distinct from its siblings here and from
	// nothing else.
	ShortCode: "E9201",
	Message:   "the declared budget was exceeded",
})

// TestComposeMessage_ShapeAcrossEveryCombination pins the composition every
// diagnostic naming a condition follows, over the whole grid of what an
// occurrence may and may not supply.
//
// The grid is the point. Each part is independently optional, so the shapes a
// sparse failure produces are what a reader actually meets — a condition with
// no values must not render trailing empty parentheses, and advice with no
// sentence before it must not open with a stray separator.
func TestComposeMessage_ShapeAcrossEveryCombination(t *testing.T) {
	t.Parallel()

	sentence := errComposeSubject.Error()

	cases := []struct {
		name      string
		condition error
		values    []string
		advice    string
		want      string
	}{
		{
			name:      "sentence alone",
			condition: errComposeSubject,
			want:      sentence,
		},
		{
			name:      "sentence and one value",
			condition: errComposeSubject,
			values:    []string{"Component core/api:1.0.0"},
			want:      sentence + " (Component core/api:1.0.0)",
		},
		{
			name:      "sentence, values and advice",
			condition: errComposeSubject,
			values:    []string{"Component core/api:1.0.0", "at .spec.budget", "got 12"},
			advice:    "raise the declared budget or lower the request",
			want:      sentence + " (Component core/api:1.0.0, at .spec.budget, got 12). raise the declared budget or lower the request",
		},
		{
			name:      "sentence and advice, no values",
			condition: errComposeSubject,
			advice:    "raise the declared budget",
			want:      sentence + ". raise the declared budget",
		},
		{
			name:      "no condition falls back to the values alone",
			condition: stderrors.New("a plain error carrying no identity"),
			values:    []string{"at .spec.budget"},
			want:      "(at .spec.budget)",
		},
		{
			name:      "no condition and no values leaves the advice alone",
			condition: nil,
			advice:    "raise the declared budget",
			want:      "raise the declared budget",
		},
		{
			name:      "nothing at all renders nothing",
			condition: nil,
			want:      "",
		},
		{
			name:      "an empty value slice is the same as none",
			condition: errComposeSubject,
			values:    []string{},
			want:      sentence,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cerrors.ComposeMessage(tc.condition, tc.values, tc.advice)
			if got != tc.want {
				t.Errorf("ComposeMessage:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestComposeMessage_ReadsTheSentenceThroughWraps proves the sentence is read
// off the condition's identity rather than off the outermost frame's text. A
// composer many frames above the raise site still opens with the wording the
// condition was registered under, which is the property that lets one sentence
// be true of a condition however it was wrapped on the way up.
func TestComposeMessage_ReadsTheSentenceThroughWraps(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("outer frame saying something else: %w", errComposeSubject)
	got := cerrors.ComposeMessage(wrapped, []string{"at .spec.budget"}, "")
	want := errComposeSubject.Error() + " (at .spec.budget)"
	if got != want {
		t.Errorf("the sentence must come from the identity, not the outer frame:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "outer frame") {
		t.Errorf("the outer frame's own text must not be rendered as the condition's sentence; got %q", got)
	}
}

// TestComposeMessage_DoesNotRepeatAValueAlreadyInTheSentence is a guard on the
// caller's side of the contract rather than on this function's: the composer
// cannot know that a value restates the sentence, so it renders what it is
// given. The test states that plainly so nobody reads a de-duplicating
// behaviour into it and then relies on one that is not there.
func TestComposeMessage_DoesNotRepeatAValueAlreadyInTheSentence(t *testing.T) {
	t.Parallel()

	got := cerrors.ComposeMessage(errComposeSubject, []string{errComposeSubject.Error()}, "")
	want := errComposeSubject.Error() + " (" + errComposeSubject.Error() + ")"
	if got != want {
		t.Errorf("the composer renders the values it is handed:\n got %q\nwant %q", got, want)
	}
}
