// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
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
