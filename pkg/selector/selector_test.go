// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the string-selector path. TestParse covers the syntactic surface
// — every supported clause shape (key=value, key!=value, key in (...),
// key notin (...), bare-key Exists, !key DoesNotExist), comma-AND of
// multiple clauses, and whitespace tolerance — by counting parsed
// Requirements per input rather than asserting structural equality, which
// keeps the table compact while still catching shape regressions.
// TestMatchString covers the end-to-end string→bool flow through
// Parse + matchRequirement against a labels set drawn from the standard
// 7-Type ontology + compliance + datastore vocabularies, with explicit
// rows for the asymmetric missing-key behaviour (NotEquals / NotIn match
// when the key is absent; In / Equals fail when it is) and for the
// nil-labels / empty-selector edge cases.

package selector

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{"empty", "", 0, false},
		{"simple equality", "key=value", 1, false},
		{"not equals", "key!=value", 1, false},
		{"in set", "key in (a,b,c)", 1, false},
		{"notin set", "key notin (x,y)", 1, false},
		{"exists", "key", 1, false},
		{"does not exist", "!key", 1, false},
		{"two requirements", "a=1, b!=2", 2, false},
		{"three mixed", "type=server, env in (prod,staging), !deprecated", 3, false},
		{"spaces", "  key = value  ", 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reqs, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(reqs) != tt.wantCount {
				t.Errorf("expected %d requirements, got %d", tt.wantCount, len(reqs))
			}
		})
	}
}

func TestMatchString(t *testing.T) {
	t.Parallel()

	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "stateful-server")
	labels.Set("compliance.aggrega.ai/data-class", "restricted")
	labels.Set("datastore.aggrega.ai/engine", "relational")

	nilLabels := types.OrderedMap[string]{}

	tests := []struct {
		name     string
		selector string
		labels   types.OrderedMap[string]
		want     bool
		wantErr  bool
	}{
		{
			name:     "empty selector matches all",
			selector: "",
			labels:   labels,
			want:     true,
		},
		{
			name:     "single equality match",
			selector: "component.aggrega.ai/type=stateful-server",
			labels:   labels,
			want:     true,
		},
		{
			name:     "single equality no match",
			selector: "component.aggrega.ai/type=server",
			labels:   labels,
			want:     false,
		},
		{
			name:     "AND both match",
			selector: "component.aggrega.ai/type=stateful-server, compliance.aggrega.ai/data-class=restricted",
			labels:   labels,
			want:     true,
		},
		{
			name:     "AND one fails",
			selector: "component.aggrega.ai/type=stateful-server, compliance.aggrega.ai/data-class=public",
			labels:   labels,
			want:     false,
		},
		{
			name:     "not equals match",
			selector: "compliance.aggrega.ai/data-class!=public",
			labels:   labels,
			want:     true,
		},
		{
			name:     "not equals no match",
			selector: "compliance.aggrega.ai/data-class!=restricted",
			labels:   labels,
			want:     false,
		},
		{
			name:     "in set match",
			selector: "datastore.aggrega.ai/engine in (relational, vector)",
			labels:   labels,
			want:     true,
		},
		{
			name:     "in set no match",
			selector: "datastore.aggrega.ai/engine in (vector, graph)",
			labels:   labels,
			want:     false,
		},
		{
			name:     "notin set match",
			selector: "datastore.aggrega.ai/engine notin (vector, graph)",
			labels:   labels,
			want:     true,
		},
		{
			name:     "notin set no match",
			selector: "datastore.aggrega.ai/engine notin (relational, graph)",
			labels:   labels,
			want:     false,
		},
		{
			name:     "exists match",
			selector: "component.aggrega.ai/type",
			labels:   labels,
			want:     true,
		},
		{
			name:     "exists no match",
			selector: "nonexistent.label",
			labels:   labels,
			want:     false,
		},
		{
			name:     "does not exist match",
			selector: "!nonexistent.label",
			labels:   labels,
			want:     true,
		},
		{
			name:     "does not exist no match",
			selector: "!component.aggrega.ai/type",
			labels:   labels,
			want:     false,
		},
		{
			name:     "not equals missing key matches",
			selector: "missing.key!=anything",
			labels:   labels,
			want:     true,
		},
		{
			name:     "notin missing key matches",
			selector: "missing.key notin (a,b)",
			labels:   labels,
			want:     true,
		},
		{
			name:     "in missing key does not match",
			selector: "missing.key in (a,b)",
			labels:   labels,
			want:     false,
		},
		{
			name:     "nil labels empty selector",
			selector: "",
			labels:   nilLabels,
			want:     true,
		},
		{
			name:     "nil labels exists fails",
			selector: "key",
			labels:   nilLabels,
			want:     false,
		},
		{
			name:     "nil labels does not exist matches",
			selector: "!key",
			labels:   nilLabels,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MatchString(tt.selector, tt.labels)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchString(%q) = %v, want %v", tt.selector, got, tt.want)
			}
		})
	}
}
