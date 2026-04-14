package selector

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestMatchLabels(t *testing.T) {
	t.Parallel()

	labels := map[string]string{
		"component.aggrega.ai/type": "server",
		"env":                       "prod",
	}

	tests := []struct {
		name        string
		matchLabels map[string]string
		want        bool
	}{
		{"nil matches all", nil, true},
		{"empty matches all", map[string]string{}, true},
		{"single match", map[string]string{"env": "prod"}, true},
		{"single no match", map[string]string{"env": "staging"}, false},
		{"both match", map[string]string{"component.aggrega.ai/type": "server", "env": "prod"}, true},
		{"one fails", map[string]string{"component.aggrega.ai/type": "server", "env": "staging"}, false},
		{"missing key", map[string]string{"nonexistent": "value"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MatchLabels(tt.matchLabels, labels)
			if got != tt.want {
				t.Errorf("MatchLabels(%v) = %v, want %v", tt.matchLabels, got, tt.want)
			}
		})
	}
}

func TestMatchExpressions(t *testing.T) {
	t.Parallel()

	labels := map[string]string{
		"component.aggrega.ai/type": "server",
		"env":                       "prod",
	}

	tests := []struct {
		name  string
		exprs []types.LabelSelectorRequirement
		want  bool
	}{
		{"nil matches all", nil, true},
		{"empty matches all", []types.LabelSelectorRequirement{}, true},
		{
			"In match",
			[]types.LabelSelectorRequirement{{Key: "env", Operator: "In", Values: []string{"prod", "staging"}}},
			true,
		},
		{
			"In no match",
			[]types.LabelSelectorRequirement{{Key: "env", Operator: "In", Values: []string{"dev", "staging"}}},
			false,
		},
		{
			"NotIn match",
			[]types.LabelSelectorRequirement{{Key: "env", Operator: "NotIn", Values: []string{"dev", "staging"}}},
			true,
		},
		{
			"NotIn no match",
			[]types.LabelSelectorRequirement{{Key: "env", Operator: "NotIn", Values: []string{"prod", "staging"}}},
			false,
		},
		{
			"Exists match",
			[]types.LabelSelectorRequirement{{Key: "env", Operator: "Exists"}},
			true,
		},
		{
			"Exists no match",
			[]types.LabelSelectorRequirement{{Key: "missing", Operator: "Exists"}},
			false,
		},
		{
			"DoesNotExist match",
			[]types.LabelSelectorRequirement{{Key: "missing", Operator: "DoesNotExist"}},
			true,
		},
		{
			"DoesNotExist no match",
			[]types.LabelSelectorRequirement{{Key: "env", Operator: "DoesNotExist"}},
			false,
		},
		{
			"multiple AND all pass",
			[]types.LabelSelectorRequirement{
				{Key: "env", Operator: "In", Values: []string{"prod"}},
				{Key: "component.aggrega.ai/type", Operator: "In", Values: []string{"server", "worker"}},
			},
			true,
		},
		{
			"multiple AND one fails",
			[]types.LabelSelectorRequirement{
				{Key: "env", Operator: "In", Values: []string{"prod"}},
				{Key: "component.aggrega.ai/type", Operator: "In", Values: []string{"worker"}},
			},
			false,
		},
		{
			"NotIn missing key matches",
			[]types.LabelSelectorRequirement{{Key: "absent", Operator: "NotIn", Values: []string{"a"}}},
			true,
		},
		{
			"In missing key fails",
			[]types.LabelSelectorRequirement{{Key: "absent", Operator: "In", Values: []string{"a"}}},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MatchExpressions(tt.exprs, labels)
			if got != tt.want {
				t.Errorf("MatchExpressions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchAnyOf(t *testing.T) {
	t.Parallel()

	labels := map[string]string{
		"component.aggrega.ai/type": "server",
		"env":                       "prod",
	}

	tests := []struct {
		name  string
		terms []types.SelectorTerm
		want  bool
	}{
		{"nil matches all", nil, true},
		{"empty matches all", []types.SelectorTerm{}, true},
		{
			"single term matches",
			[]types.SelectorTerm{{MatchLabels: map[string]string{"env": "prod"}}},
			true,
		},
		{
			"single term fails",
			[]types.SelectorTerm{{MatchLabels: map[string]string{"env": "staging"}}},
			false,
		},
		{
			"OR first matches",
			[]types.SelectorTerm{
				{MatchLabels: map[string]string{"env": "prod"}},
				{MatchLabels: map[string]string{"env": "staging"}},
			},
			true,
		},
		{
			"OR second matches",
			[]types.SelectorTerm{
				{MatchLabels: map[string]string{"env": "staging"}},
				{MatchLabels: map[string]string{"env": "prod"}},
			},
			true,
		},
		{
			"OR none match",
			[]types.SelectorTerm{
				{MatchLabels: map[string]string{"env": "staging"}},
				{MatchLabels: map[string]string{"env": "dev"}},
			},
			false,
		},
		{
			"term with matchLabels AND matchExpressions",
			[]types.SelectorTerm{
				{
					MatchLabels:      map[string]string{"env": "prod"},
					MatchExpressions: []types.LabelSelectorRequirement{{Key: "component.aggrega.ai/type", Operator: "In", Values: []string{"server"}}},
				},
			},
			true,
		},
		{
			"term matchLabels pass but matchExpressions fail",
			[]types.SelectorTerm{
				{
					MatchLabels:      map[string]string{"env": "prod"},
					MatchExpressions: []types.LabelSelectorRequirement{{Key: "component.aggrega.ai/type", Operator: "In", Values: []string{"worker"}}},
				},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MatchAnyOf(tt.terms, labels)
			if got != tt.want {
				t.Errorf("MatchAnyOf() = %v, want %v", got, tt.want)
			}
		})
	}
}
