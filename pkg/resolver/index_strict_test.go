// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// TestStrictIndex_AggregateInterpolationHaltsOnAnAbsentMapKey drives the
// Aggregate's own text through the interpolation that renders it: indexing the
// parameter map with a parameter nobody supplied halts naming the key, reported
// as the surface's own condition with the evaluation's report — key included —
// in its message; a supplied parameter, a list position and the presence-testing
// reads Sprig offers here render as before.
func TestStrictIndex_AggregateInterpolationHaltsOnAnAbsentMapKey(t *testing.T) {
	t.Parallel()
	params := map[string]any{"region": "eu-west-1", "zones": []any{"a", "b"}}

	for _, tmpl := range []string{
		`x: {{ index .parameters "nothing" }}`,
		`x: {{ index .parameters "nothing" | default "fb" }}`,
		`x: {{ if index .parameters "nothing" }}y{{ end }}`,
	} {
		out, err := InterpolateParameters([]byte(tmpl), params)
		if err == nil {
			t.Errorf("%s rendered %q; want a halt naming the key", tmpl, out)
			continue
		}
		if !errors.Is(err, ErrInvalidParameterRef()) {
			t.Errorf("%s: want the interpolation surface's condition; got %v", tmpl, err)
		}
		if !strings.Contains(err.Error(), `key "nothing"`) {
			t.Errorf("%s: the halt does not name the key: %v", tmpl, err)
		}
	}

	for _, row := range []struct{ tmpl, want string }{
		{`x: {{ index .parameters "region" }}`, "x: eu-west-1"},
		{`x: {{ index .parameters "zones" 1 }}`, "x: b"},
		{`x: [{{ get .parameters "nothing" }}]`, "x: []"},
		{`x: {{ if hasKey .parameters "nothing" }}y{{ else }}n{{ end }}`, "x: n"},
		{`x: {{ .parameters.nothing | default "fb" }}`, "x: fb"},
	} {
		out, err := InterpolateParameters([]byte(row.tmpl), params)
		if err != nil {
			t.Errorf("%s halted: %v", row.tmpl, err)
			continue
		}
		if string(out) != row.want {
			t.Errorf("%s rendered %q, want %q", row.tmpl, out, row.want)
		}
	}
}

// TestStrictIndex_ValidatePredicateHaltsOnAnAbsentMapKey drives a parameter's
// validate: predicate: indexing the value under validation with a key it does not
// hold halts naming the key, carrying the strict index's condition; a present key
// and a presence-tested read evaluate as before.
func TestStrictIndex_ValidatePredicateHaltsOnAnAbsentMapKey(t *testing.T) {
	t.Parallel()
	value := map[string]any{"tier": "gold"}

	_, err := evalValidatePredicate(`{{ if index .value "nothing" }}true{{ else }}false{{ end }}`, value)
	if err == nil {
		t.Fatal("a predicate indexing an absent key evaluated; want a halt naming the key")
	}
	if !errors.Is(err, gotmpl.ErrIndexOfAbsentMapKey()) {
		t.Errorf("the halt does not carry the strict index's condition: %v", err)
	}
	if !strings.Contains(err.Error(), `key "nothing"`) {
		t.Errorf("the halt does not name the key: %v", err)
	}

	for _, row := range []struct {
		predicate string
		want      bool
	}{
		{`{{ eq (index .value "tier") "gold" }}`, true},
		{`{{ hasKey .value "nothing" }}`, false},
		{`{{ eq (get .value "nothing") "" }}`, true},
	} {
		got, err := evalValidatePredicate(row.predicate, value)
		if err != nil {
			t.Errorf("%s halted: %v", row.predicate, err)
			continue
		}
		if got != row.want {
			t.Errorf("%s = %v, want %v", row.predicate, got, row.want)
		}
	}
}
