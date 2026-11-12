// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"slices"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// TestPipelineHeads_ReturnsTheCommandOpeningEveryPipeline walks each node kind a
// pipeline sits in, one row per kind, and the shapes a command is NOT a head in.
// A kind the walk missed would return nothing for its row, which is the safe
// direction for the release it serves and exactly why it needs a row: the miss is
// silent there.
func TestPipelineHeads_ReturnsTheCommandOpeningEveryPipeline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want []string
	}{
		{name: "an action", text: `{{ fail "a" }}`, want: []string{`fail "a"`}},
		{name: "an if, its branch and its else", text: `{{ if eq 1 1 }}{{ fail "a" }}{{ else }}{{ fail "b" }}{{ end }}`,
			want: []string{`eq 1 1`, `fail "a"`, `fail "b"`}},
		{name: "an else-if chain", text: `{{ if eq 1 2 }}{{ else if eq 1 1 }}{{ fail "a" }}{{ end }}`,
			want: []string{`eq 1 2`, `eq 1 1`, `fail "a"`}},
		{name: "a range, its body and its else", text: `{{ range list 1 }}{{ fail "a" }}{{ else }}{{ fail "b" }}{{ end }}`,
			want: []string{`list 1`, `fail "a"`, `fail "b"`}},
		{name: "a with, its body and its else", text: `{{ with list 1 }}{{ fail "a" }}{{ else }}{{ fail "b" }}{{ end }}`,
			want: []string{`list 1`, `fail "a"`, `fail "b"`}},
		{name: "a template invocation's pipeline", text: `{{ define "x" }}{{ end }}{{ template "x" (fail "a") }}`,
			want: []string{`(fail "a")`, `fail "a"`}},
		{name: "a parenthesized pipeline inside an argument", text: `{{ printf "%s" (fail "a") }}`,
			want: []string{`printf "%s" (fail "a")`, `fail "a"`}},
		{name: "a chain's receiver", text: `{{ (fail "a").x }}`,
			want: []string{`(fail "a").x`, `fail "a"`}},
		{name: "a command after the first is not a head", text: `{{ "a" | fail }}`,
			want: []string{`"a"`}},
		{name: "text and comments contribute nothing", text: `plain {{/* note */}} text`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tmpl, err := template.New("t").Funcs(gotmpl.FuncMap()).Parse(c.text)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var got []string
			for _, defined := range tmpl.Templates() {
				if defined.Name() != "t" || defined.Tree == nil {
					continue
				}
				for _, cmd := range pipelineHeads(defined.Tree.Root) {
					got = append(got, cmd.String())
				}
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("heads = %q, want %q", got, c.want)
			}
		})
	}
}

// TestFailComposesFromMessage_TrustsTheTwoKnownBindingsAndNoOther pins which
// functions bound under fail a written-out sentence is released for: the shared
// function set's, taken by an evaluation that binds no fail of its own, and the
// assertion surface's. A function of any other identity bound under the name is
// not trusted whatever its signature, because what it composes its text from is
// its own business.
func TestFailComposesFromMessage_TrustsTheTwoKnownBindingsAndNoOther(t *testing.T) {
	t.Parallel()
	other := func(msg string) (string, error) { return "", errors.New(msg) }
	cases := []struct {
		name  string
		funcs map[string]any
		want  bool
	}{
		{name: "no fail of the surface's own", funcs: nil, want: true},
		{name: "other functions only", funcs: gotmpl.AssertionComparisons(), want: true},
		{name: "the assertion surface's fail", funcs: map[string]any{failFunction: assertionFail}, want: true},
		{name: "a fail of another identity", funcs: map[string]any{failFunction: other}, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := (templateEval{extraFuncs: c.funcs}).failComposesFromMessage(); got != c.want {
				t.Errorf("failComposesFromMessage() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestTheSharedFailComposesItsTextFromItsMessage pins the premise the release
// rests on for an evaluation binding no fail of its own: the shared function
// set's fail fails, and its failure text is the message and nothing else. If the
// shared set ever bound a fail composing more, a written-out sentence would no
// longer be the whole of the text this package releases.
func TestTheSharedFailComposesItsTextFromItsMessage(t *testing.T) {
	t.Parallel()
	fail, ok := gotmpl.FuncMap()[failFunction].(func(string) (string, error))
	if !ok {
		t.Fatalf("the shared function set binds fail as %T, not as a function of one message", gotmpl.FuncMap()[failFunction])
	}
	const msg = "a written-out sentence"
	_, err := fail(msg)
	if err == nil || err.Error() != msg {
		t.Errorf("the shared fail(%q) failed with %v; want exactly the message", msg, err)
	}
	_, err = assertionFail(msg)
	if want := ErrAssertionFailed().Error() + ": " + msg; err == nil || err.Error() != want {
		t.Errorf("assertionFail(%q) failed with %v; want %q", msg, err, want)
	}
}
