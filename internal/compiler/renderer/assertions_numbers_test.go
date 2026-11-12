// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestEvaluateAssertion_NumbersCompareAcrossKinds pins the assertion surface's
// comparisons through the one evaluator every assertion kind runs on. Values
// carry numbers the way a build hands them to an assertion — as authored
// numbers — and the body compares them with number literals written as
// integers and as decimals, the form the assertion pages teach. Each row either
// passes, halts on the author's own fail, or is refused naming both kinds.
func TestEvaluateAssertion_NumbersCompareAcrossKinds(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"replicas": json.Number("3"),
		"ratio":    json.Number("0.5"),
		"big":      json.Number("10"),
		"label":    "3",
		"zones":    []any{"a", "b"},
	}
	cases := []struct {
		name    string
		body    string
		outcome string // "pass", "fail" (the author's fail ran) or the refusal's sentence
	}{
		{"the taught form holds", `{{- if lt .values.replicas 2 -}}{{- fail "at least 2" -}}{{- end -}}`, "pass"},
		{"the taught form fails on its own fail", `{{- if lt .values.replicas 4 -}}{{- fail "at least 4" -}}{{- end -}}`, "fail"},
		{"eq against an integer", `{{- if not (eq .values.replicas 3) -}}{{- fail "x" -}}{{- end -}}`, "pass"},
		{"eq against a decimal", `{{- if not (eq .values.replicas 3.0) -}}{{- fail "x" -}}{{- end -}}`, "pass"},
		{"ne against an integer", `{{- if ne .values.replicas 3 -}}{{- fail "x" -}}{{- end -}}`, "pass"},
		{"le against a decimal", `{{- if not (le .values.ratio 0.5) -}}{{- fail "x" -}}{{- end -}}`, "pass"},
		{"gt against an integer", `{{- if gt .values.ratio 1 -}}{{- fail "x" -}}{{- end -}}`, "pass"},
		{"ge between two authored numbers", `{{- if ge .values.replicas .values.big -}}{{- fail "x" -}}{{- end -}}`, "pass"},
		{"a number against text is refused", `{{- if eq .values.replicas "3" -}}{{- fail "x" -}}{{- end -}}`,
			"cannot compare a number with text"},
		{"text against a number is refused", `{{- if lt .values.label 5 -}}{{- fail "x" -}}{{- end -}}`,
			"cannot compare text with a number"},
		{"a list against a number is refused", `{{- if gt .values.zones 1 -}}{{- fail "x" -}}{{- end -}}`,
			"cannot compare a list with a number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := EvaluateAssertion("numbers", tc.body, AssertionContext{Values: values}, redact.Keys{})
			switch tc.outcome {
			case "pass":
				if err != nil {
					t.Fatalf("want a pass; got %v", err)
				}
			case "fail":
				if !AssertionCalledFail(err) {
					t.Fatalf("want the author's fail to halt the assertion; got %v", err)
				}
			default:
				if err == nil {
					t.Fatalf("want the refusal %q; the assertion passed", tc.outcome)
				}
				if !errors.Is(err, ErrAssertionFailed()) || AssertionCalledFail(err) {
					t.Errorf("want a refusal under the assertion condition that is not a fail call; got %v", err)
				}
				if !strings.Contains(err.Error(), tc.outcome) {
					t.Errorf("the refusal %q does not say %q", err.Error(), tc.outcome)
				}
				// The action that refused stands beside the sentence, so the path read is
				// named.
				if !strings.Contains(err.Error(), ".values.") {
					t.Errorf("the refusal does not carry the path it read: %q", err.Error())
				}
			}
		})
	}
}

// assertionNumbers is the authored numbers both surfaces are handed, each as its
// text, the way a build carries a Component's or a Recipe's own values.
var assertionNumbers = [][2]string{
	{"zero", "0"}, {"one", "1"}, {"tenth", "0.1"}, {"e7", "1e-7"}, {"huge", "1e+21"},
}

func assertionNumbersContexts() (*types.TemplateContext, AssertionContext) {
	ordered := types.OrderedMap[any]{}
	plain := map[string]any{}
	for _, kv := range assertionNumbers {
		ordered.Set(kv[0], json.Number(kv[1]))
		plain[kv[0]] = json.Number(kv[1])
	}
	return &types.TemplateContext{Context: types.ContextBlock{InstanceID: "db"}, Values: ordered},
		AssertionContext{Values: plain}
}

// An assertion reads an authored number exactly as a value template does: the
// same condition comes to the same outcome on both surfaces, a zero is false to
// `if` and empty to `default`, an authored decimal equals the same literal, and a
// number turned into text prints in its shortest decimal form.
func TestEvaluateAssertion_ReadsAnAuthoredNumberAsAValueTemplateDoes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cond string
		want string
	}{
		{`.values.zero`, "false"},
		{`not .values.zero`, "true"},
		{`empty .values.zero`, "true"},
		{`eq (.values.zero | default 7) 7`, "true"},
		{`eq .values.zero 0`, "true"},
		{`eq .values.tenth 0.1`, "true"},
		{`lt .values.tenth 0.1`, "false"},
		{`eq .values.one 1.0`, "true"},
		{`eq (printf "%v" .values.e7) "0.0000001"`, "true"},
		{`eq (printf "%d" .values.one) "1"`, "true"},
		{`eq (toString .values.huge) "1000000000000000000000"`, "true"},
		// Presence is still told apart from emptiness by the presence helpers.
		{`hasKey .values "zero"`, "true"},
		{`eq (optional .values "zero" 9) 0`, "true"},
		{`eq .values.one "1"`, "refused: cannot compare a number with text"},
	}
	for _, tc := range cases {
		t.Run(tc.cond, func(t *testing.T) {
			t.Parallel()
			valueCtx, assertionCtx := assertionNumbersContexts()
			out, err := EvalTemplate("t", `{{ if `+tc.cond+` }}true{{ else }}false{{ end }}`, valueCtx, redact.Keys{})
			valueSide := comparisonOutcome(t, err, out, false)
			aerr := EvaluateAssertion("t", `{{ if `+tc.cond+` }}{{ fail "true" }}{{ end }}`, assertionCtx, redact.Keys{})
			assertionSide := comparisonOutcome(t, aerr, "", true)
			if !strings.HasPrefix(assertionSide, tc.want) {
				t.Errorf("assertion: got %q, want %q", assertionSide, tc.want)
			}
			if valueSide != assertionSide {
				t.Errorf("the two surfaces disagree: value template %q, assertion %q", valueSide, assertionSide)
			}
		})
	}
}

// An authored 0 passes a check written with `with` or `if`, which read it as
// empty, and is refused by one written with `hasKey`, which asks whether the
// author wrote the key at all; a number an assertion prints in its sentence
// prints in its shortest decimal form.
func TestEvaluateAssertion_AnAuthoredZeroIsEmptyAndStillPresent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		wantMsg string // empty: the assertion passes
	}{
		{"with passes a zero", `{{ with optional .values "zero" "" }}{{ fail (printf "zero is %v" .) }}{{ end }}`, ""},
		{"if passes a zero", `{{ if optional .values "zero" "" }}{{ fail "zero is set" }}{{ end }}`, ""},
		{"hasKey refuses a zero", `{{ if hasKey .values "zero" }}{{ fail (printf "zero is %v" .values.zero) }}{{ end }}`, "zero is 0"},
		{"hasKey passes an absent key", `{{ if hasKey .values "absent" }}{{ fail "absent is set" }}{{ end }}`, ""},
		{"a decimal prints whole", `{{ fail (printf "e7 is %v, huge is %v" .values.e7 .values.huge) }}`, "e7 is 0.0000001, huge is 1000000000000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ctx := assertionNumbersContexts()
			err := EvaluateAssertion("t", tc.body, ctx, redact.Keys{})
			if tc.wantMsg == "" {
				if err != nil {
					t.Fatalf("want the assertion to pass, got %v", err)
				}
				return
			}
			if !AssertionCalledFail(err) {
				t.Fatalf("want the assertion to call fail, got %v", err)
			}
			if !strings.HasSuffix(err.Error(), tc.wantMsg) {
				t.Errorf("message %q, want it to end %q", err.Error(), tc.wantMsg)
			}
		})
	}
}
