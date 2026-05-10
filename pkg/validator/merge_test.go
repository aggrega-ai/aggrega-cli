// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"reflect"
	"testing"
)

func TestDeepMergeYAML(t *testing.T) {
	tests := []struct {
		name     string
		base     map[string]any
		child    map[string]any
		expected map[string]any
	}{
		{
			name:     "nil base",
			base:     nil,
			child:    map[string]any{"a": "1"},
			expected: map[string]any{"a": "1"},
		},
		{
			name:     "nil child",
			base:     map[string]any{"a": "1"},
			child:    nil,
			expected: map[string]any{"a": "1"},
		},
		{
			name:     "both nil",
			base:     nil,
			child:    nil,
			expected: map[string]any{},
		},
		{
			name:     "child scalar overrides base",
			base:     map[string]any{"a": "old"},
			child:    map[string]any{"a": "new"},
			expected: map[string]any{"a": "new"},
		},
		{
			name: "child adds new key",
			base: map[string]any{"a": "1"},
			child: map[string]any{
				"a": "1",
				"b": "2",
			},
			expected: map[string]any{"a": "1", "b": "2"},
		},
		{
			name: "nested maps merge recursively",
			base: map[string]any{
				"top": map[string]any{"a": "1", "b": "2"},
			},
			child: map[string]any{
				"top": map[string]any{"b": "override", "c": "3"},
			},
			expected: map[string]any{
				"top": map[string]any{"a": "1", "b": "override", "c": "3"},
			},
		},
		{
			name: "3-level nested merge",
			base: map[string]any{
				"l1": map[string]any{
					"l2": map[string]any{
						"a": "base",
					},
				},
			},
			child: map[string]any{
				"l1": map[string]any{
					"l2": map[string]any{
						"b": "child",
					},
				},
			},
			expected: map[string]any{
				"l1": map[string]any{
					"l2": map[string]any{
						"a": "base",
						"b": "child",
					},
				},
			},
		},
		{
			name: "array replaces not merges",
			base: map[string]any{
				"items": []any{"a", "b"},
			},
			child: map[string]any{
				"items": []any{"c"},
			},
			expected: map[string]any{
				"items": []any{"c"},
			},
		},
		{
			name: "type conflict child wins",
			base: map[string]any{
				"x": map[string]any{"nested": "val"},
			},
			child: map[string]any{
				"x": "scalar",
			},
			expected: map[string]any{
				"x": "scalar",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deepMergeYAML(tt.base, tt.child)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got %v, want %v", got, tt.expected)
			}
		})
	}
}
