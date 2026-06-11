// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the non-string-entry-point matchers (MatchLabels, MatchAnyOf) —
// the surface Feature catalog YAML lands on via Feature.Spec.AppliesToAnyOf
// and the flat-map arm of its items. Coverage is one row per behaviour so a
// regression renames itself in the failure output: equality-only AND for
// MatchLabels; OR-between-items, per-arm dispatch (string arm through the
// selector grammar, map arm through equality), the empty-list
// "no constraint" contract, and parse-error surfacing for MatchAnyOf.
// Per-operator coverage for the string grammar (including the asymmetric
// missing-key behaviour of In / NotIn) lives in selector_test.go on the
// MatchString path.

package selector

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func makeLabels(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func TestMatchLabels(t *testing.T) {
	t.Parallel()

	labels := makeLabels(
		"component.aggrega.ai/type", "server",
		"env", "prod",
	)

	tests := []struct {
		name        string
		matchLabels types.OrderedMap[string]
		want        bool
	}{
		{"nil matches all", types.OrderedMap[string]{}, true},
		{"empty matches all", types.OrderedMap[string]{}, true},
		{"single match", makeLabels("env", "prod"), true},
		{"single no match", makeLabels("env", "staging"), false},
		{"both match", makeLabels("component.aggrega.ai/type", "server", "env", "prod"), true},
		{"one fails", makeLabels("component.aggrega.ai/type", "server", "env", "staging"), false},
		{"missing key", makeLabels("nonexistent", "value"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MatchLabels(tt.matchLabels, labels)
			if got != tt.want {
				t.Errorf("MatchLabels(%v) = %v, want %v", tt.matchLabels.Keys(), got, tt.want)
			}
		})
	}
}

func TestMatchAnyOf(t *testing.T) {
	t.Parallel()

	labels := makeLabels(
		"component.aggrega.ai/type", "server",
		"env", "prod",
	)

	tests := []struct {
		name  string
		terms []types.SelectorTerm
		want  bool
	}{
		{"nil matches all", nil, true},
		{"empty matches all", []types.SelectorTerm{}, true},
		{
			"single map item matches",
			[]types.SelectorTerm{types.MapTerm(makeLabels("env", "prod"))},
			true,
		},
		{
			"single map item fails",
			[]types.SelectorTerm{types.MapTerm(makeLabels("env", "staging"))},
			false,
		},
		{
			"single string item matches",
			[]types.SelectorTerm{types.StringTerm("env in (prod, staging)")},
			true,
		},
		{
			"single string item fails",
			[]types.SelectorTerm{types.StringTerm("env in (dev, staging)")},
			false,
		},
		{
			"string item ANDs within the item",
			[]types.SelectorTerm{types.StringTerm("env=prod, component.aggrega.ai/type=worker")},
			false,
		},
		{
			"OR across mixed arms, second matches",
			[]types.SelectorTerm{
				types.MapTerm(makeLabels("env", "staging")),
				types.StringTerm("component.aggrega.ai/type in (server, worker)"),
			},
			true,
		},
		{
			"OR none match",
			[]types.SelectorTerm{
				types.MapTerm(makeLabels("env", "staging")),
				types.StringTerm("component.aggrega.ai/type=worker"),
			},
			false,
		},
		{
			"empty string item matches all (no constraint)",
			[]types.SelectorTerm{types.StringTerm("")},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MatchAnyOf(tt.terms, labels)
			if err != nil {
				t.Fatalf("MatchAnyOf() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchAnyOf() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMatchAnyOf_ParseError pins the loud-guardrail contract: a malformed
// string item surfaces the parse error rather than silently changing the
// disjunction, and evaluation is short-circuiting in author order — an
// item that matches before the malformed one is reached satisfies the
// disjunction without an error.
func TestMatchAnyOf_ParseError(t *testing.T) {
	t.Parallel()

	labels := makeLabels("env", "prod")

	_, err := MatchAnyOf([]types.SelectorTerm{
		types.StringTerm("env in (prod"), // unclosed set: the parser's hard error
	}, labels)
	if err == nil {
		t.Fatal("malformed string item should surface a parse error")
	}
	if !strings.Contains(err.Error(), "selector") {
		t.Errorf("parse error should come from the selector parser; got %v", err)
	}

	got, err := MatchAnyOf([]types.SelectorTerm{
		types.MapTerm(makeLabels("env", "prod")), // matches first
		types.StringTerm("env in (prod"),         // never reached
	}, labels)
	if err != nil || !got {
		t.Errorf("match before malformed item should short-circuit: got=%v err=%v", got, err)
	}
}
