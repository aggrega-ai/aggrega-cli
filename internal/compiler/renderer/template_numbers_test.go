// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// numbersFixture carries authored numbers the way a build hands them to a value
// template — as each number's text — under values, and under infra one more
// carried as a floating-point value, the form an attachment's operator values
// reach a template in.
func numbersFixture() *types.TemplateContext {
	values := types.OrderedMap[any]{}
	for _, kv := range [][2]string{
		{"zero", "0"}, {"one", "1"}, {"big", "1000000"}, {"half", "0.5"},
		{"tiny", "0.00001"}, {"e7", "1e-7"}, {"mid", "1234567.5"}, {"huge", "1e+21"}, {"neg", "-0.00001"},
	} {
		values.Set(kv[0], json.Number(kv[1]))
	}
	infra := types.OrderedMap[any]{}
	infra.Set("ratio", 0.0000001)
	infra.Set("count", 0)
	return &types.TemplateContext{Context: types.ContextBlock{InstanceID: "db"}, Values: values, Infra: infra}
}

// A value template reads an authored number as a number: a zero is empty to
// `default` and `empty`, false to `if` and `with`, and equal to a zero written in
// the template; an integral number still prints whole.
func TestValueTemplate_ReadsAnAuthoredNumberAsANumber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want string
	}{
		{`{{ .values.zero | default 1 }}`, "1"},
		{`{{ empty .values.zero }}`, "true"},
		{`{{ if .values.zero }}true{{ else }}false{{ end }}`, "false"},
		{`{{ with .values.zero }}set{{ else }}empty{{ end }}`, "empty"},
		{`{{ eq .values.zero 0 }}`, "true"},
		{`{{ lt .values.zero 1 }}`, "true"},
		{`{{ .infra.count | default 1 }}`, "1"},
		// A present non-zero number is kept, and an integral one prints whole.
		{`{{ .values.one | default 9 }}`, "1"},
		{`{{ .values.big }}`, "1000000"},
		{`{{ add .values.big 1 }}`, "1000001"},
		{`{{ printf "%d" .values.big }}`, "1000000"},
		// An integer still prints as its digits through a text verb, as it did
		// while it reached the template as its text.
		{`{{ printf "%s" .values.big }}`, "1000000"},
		// Presence is still told apart from emptiness by the presence helpers.
		{`{{ hasKey .values "zero" }}`, "true"},
		{`{{ optional .values "zero" "fallback" }}`, "0"},
	}
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("t", tc.tmpl, numbersFixture(), redact.Keys{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// A non-integral number prints in its shortest exact decimal form — never in
// exponent form — wherever a template prints it or turns it into text, whether it
// reached the template as its text or as a floating-point value.
func TestValueTemplate_PrintsANumberInShortestDecimalForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want string
	}{
		{`{{ .values.half }}`, "0.5"},
		{`{{ .values.tiny }}`, "0.00001"},
		{`{{ .values.e7 }}`, "0.0000001"},
		{`{{ .values.mid }}`, "1234567.5"},
		{`{{ .values.huge }}`, "1000000000000000000000"},
		{`{{ .values.neg }}`, "-0.00001"},
		{`{{ toString .values.e7 }}`, "0.0000001"},
		{`{{ printf "%v" .values.e7 }}`, "0.0000001"},
		{`{{ printf "%s" .values.mid }}`, "1234567.5"},
		{`{{ quote .values.huge }}`, `"1000000000000000000000"`},
		{`{{ toJson .values.e7 }}`, "0.0000001"},
		{`{{ toJson .values.huge }}`, "1000000000000000000000"},
		{`{{ .infra.ratio }}`, "0.0000001"},
		{`{{ toJson .infra }}`, `{"count":0,"ratio":0.0000001}`},
	}
	exponent := regexp.MustCompile(`[0-9]e[-+]?[0-9]`)
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("t", tc.tmpl, numbersFixture(), redact.Keys{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
			if exponent.MatchString(got) {
				t.Errorf("rendered %q in exponent form", got)
			}
		})
	}
}

// comparisonOutcome is what one comparison came to on a surface: "true", "false",
// or the sentence its refusal carries.
func comparisonOutcome(t *testing.T, err error, valueOut string, assertion bool) string {
	t.Helper()
	switch {
	case assertion && AssertionCalledFail(err):
		return "true"
	case assertion && err == nil:
		return "false"
	case !assertion && err == nil:
		return valueOut
	}
	// The sentence the comparison refused with, without what each surface writes
	// around it: the function the engine names before it, and the rest of the
	// refusal after it. A value template's diagnostic prints its own heading and
	// carries the engine's failure as a cause beneath it, so the chain is read link
	// by link, as a reader of the printed diagnostic reads every cause.
	var msg string
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		text := cause.Error()
		if i := strings.Index(text, "error calling "); i >= 0 {
			msg = text[i:]
			break
		}
	}
	if msg == "" {
		return "refused, naming no comparison: " + err.Error()
	}
	if j := strings.Index(msg, ": "); j >= 0 {
		msg = msg[j+2:]
	}
	if k := strings.Index(msg, ":"); k >= 0 {
		msg = msg[:k]
	}
	return "refused: " + msg
}

// A value template compares numbers exactly as an assertion does, because both
// install one comparison set: an integer against a decimal, an authored number
// against a literal or another authored number, and a number against text, which
// both refuse in the same words.
func TestValueTemplate_ComparesNumbersAsTheAssertionSurfaceDoes(t *testing.T) {
	t.Parallel()
	assertionValues := map[string]any{}
	ctx := numbersFixture()
	for _, k := range ctx.Values.Keys() {
		v, _ := ctx.Values.Get(k)
		assertionValues[k] = v
	}
	cases := []struct {
		cmp  string
		want string
	}{
		{`lt .values.one 1.5`, "true"},
		{`gt .values.one .values.half`, "true"},
		{`eq .values.half 0.5`, "true"},
		{`eq .values.one 1.0`, "true"},
		{`lt .values.zero 1`, "true"},
		{`ge .values.big .values.mid`, "false"},
		{`le .values.half 0.25`, "false"},
		{`eq .values.big "1000000"`, "refused: cannot compare a number with text"},
	}
	for _, tc := range cases {
		t.Run(tc.cmp, func(t *testing.T) {
			t.Parallel()
			out, err := EvalTemplate("t", `{{ if `+tc.cmp+` }}true{{ else }}false{{ end }}`, numbersFixture(), redact.Keys{})
			valueSide := comparisonOutcome(t, err, out, false)
			aerr := EvaluateAssertion("t", `{{ if `+tc.cmp+` }}{{ fail "true" }}{{ end }}`, AssertionContext{Values: assertionValues}, redact.Keys{})
			assertionSide := comparisonOutcome(t, aerr, "", true)
			if !strings.HasPrefix(valueSide, tc.want) {
				t.Errorf("value template: got %q, want %q", valueSide, tc.want)
			}
			if !strings.HasPrefix(assertionSide, tc.want) {
				t.Errorf("assertion: got %q, want %q", assertionSide, tc.want)
			}
			if valueSide != assertionSide {
				t.Errorf("the two surfaces disagree: value template %q, assertion %q", valueSide, assertionSide)
			}
		})
	}
}

// A component-level self-inject value: template reads an authored number as a
// number and prints it as every other value template does.
func TestSelfInjectValue_ReadsAnAuthoredNumberAsANumber(t *testing.T) {
	t.Parallel()
	fixture := numbersFixture()
	ctx := SelfInjectContext{Values: fixture.Values, Context: SelfInjectContextBlock{InstanceID: "db"}}
	cases := []struct {
		tmpl string
		want string
	}{
		{`{{ .values.zero | default 1 }}`, "1"},
		{`{{ if .values.zero }}true{{ else }}false{{ end }}`, "false"},
		{`{{ eq .values.zero 0 }}`, "true"},
		{`{{ lt .values.one 1.5 }}`, "true"},
		{`{{ .values.e7 }}`, "0.0000001"},
		{`{{ .values.huge }}`, "1000000000000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			got, err := evalSelfInjectValue("t", tc.tmpl, ctx, redact.Keys{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// The reading is confined to the execution that made it: the caller's context
// still carries each number as its text, so a surface reading numbers as their
// text — an assertion — is handed exactly what it always was.
func TestValueTemplate_NumbersAsNumbersLeavesTheContextAlone(t *testing.T) {
	t.Parallel()
	ctx := numbersFixture()
	if _, err := EvalTemplate("t", `{{ .values.zero | default 1 }}{{ .values.tiny }}`, ctx, redact.Keys{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, k := range ctx.Values.Keys() {
		v, _ := ctx.Values.Get(k)
		if _, isText := v.(json.Number); !isText {
			t.Errorf("values.%s is %T after an evaluation; the caller's context must be untouched", k, v)
		}
	}
	err := EvaluateAssertion("t", `{{ if .values.zero }}{{ fail "an authored 0 reads as true here" }}{{ end }}`,
		AssertionContext{Values: map[string]any{"zero": json.Number("0")}}, redact.Keys{})
	if !AssertionCalledFail(err) {
		t.Errorf("an assertion reads an authored number as its text, so its 0 is true; got %v", err)
	}
	if err != nil && !errors.Is(err, ErrAssertionFailed()) {
		t.Errorf("unexpected error: %v", err)
	}
}
