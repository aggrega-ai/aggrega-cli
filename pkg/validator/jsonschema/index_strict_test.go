// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// TestStrictIndex_SchemaPredicatesHaltOnAnAbsentMapKey drives both predicate
// keywords through a validator compiled from authored compact form: indexing a
// map with a key it does not hold halts naming the key — the field value under
// x-aggrega-validate, the parameter map under x-aggrega-when — reported as the
// predicate surface's own condition with the evaluation's report, key included,
// in its message. The same predicates over a present key evaluate as before, and
// `param` still answers nil for a parameter nobody supplied.
func TestStrictIndex_SchemaPredicatesHaltOnAnAbsentMapKey(t *testing.T) {
	t.Parallel()
	src := `
limits:
  type: object
  validate: '{{ eq (index . "cpu") "1" }}'
  validateMessage: "limits.cpu must be 1"
secret:
  when: '{{ eq (index (parameters) "env") "prod" }}'
  then:
    value: { type: string, required: true }
`
	v, err := New(mustCompileAndConvert(t, src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name     string
		instance map[string]any
		params   map[string]any
		keyword  string
		wantKey  string
	}{
		{"x-aggrega-validate indexes the field value", map[string]any{"limits": map[string]any{"memory": "1Gi"}}, map[string]any{"env": "dev"}, "x-aggrega-validate", `key "cpu"`},
		{"x-aggrega-when indexes the parameter map", map[string]any{"secret": map[string]any{}}, map[string]any{}, "x-aggrega-when", `key "env"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Validate(tc.instance, &Context{Parameters: tc.params})
			if err == nil {
				t.Fatal("the predicate evaluated; want a halt naming the key")
			}
			if !errors.Is(err, ErrExtensionEval()) {
				t.Errorf("want the predicate surface's condition; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.keyword) || !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("the halt does not name the keyword %s and the %s: %v", tc.keyword, tc.wantKey, err)
			}
		})
	}

	t.Run("present keys evaluate as before", func(t *testing.T) {
		instance := map[string]any{"limits": map[string]any{"cpu": "1"}, "secret": map[string]any{"value": "s"}}
		rpt, err := v.Validate(instance, &Context{Parameters: map[string]any{"env": "prod"}})
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(rpt.Findings) != 0 {
			t.Errorf("present keys satisfying both predicates must pass; got %v", rpt.Findings)
		}
	})
}

// TestStrictIndex_EvaluateTemplateCarriesTheCondition holds the predicate
// evaluation one level down, where the error is still the evaluation's own: it
// carries the strict index's condition, and the presence-testing reads Sprig
// offers on this surface evaluate without halting.
func TestStrictIndex_EvaluateTemplateCarriesTheCondition(t *testing.T) {
	t.Parallel()
	data := map[string]any{"cpu": "1"}

	_, err := evaluateTemplate(mustParseTemplate(t, `{{ index . "nothing" }}`), data, nil)
	if err == nil || !errors.Is(err, gotmpl.ErrIndexOfAbsentMapKey()) || !strings.Contains(err.Error(), `key "nothing"`) {
		t.Errorf("want a halt carrying the strict index's condition and naming the key; got %v", err)
	}

	for _, row := range []struct {
		src  string
		want bool
	}{
		{`{{ eq (index . "cpu") "1" }}`, true},
		{`{{ hasKey . "nothing" }}`, false},
		{`{{ eq (get . "nothing") "" }}`, true},
		{`{{ if param "nothing" }}false{{ else }}true{{ end }}`, true},
	} {
		got, err := evaluateTemplate(mustParseTemplate(t, row.src), data, map[string]any{})
		if err != nil {
			t.Errorf("%s halted: %v", row.src, err)
			continue
		}
		if got != row.want {
			t.Errorf("%s = %v, want %v", row.src, got, row.want)
		}
	}
}
