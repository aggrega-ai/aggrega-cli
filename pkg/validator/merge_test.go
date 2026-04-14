package validator

import (
	"reflect"
	"testing"
)

func TestDeepMergeYAML(t *testing.T) {
	tests := []struct {
		name     string
		base     map[string]interface{}
		child    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "nil base",
			base:     nil,
			child:    map[string]interface{}{"a": "1"},
			expected: map[string]interface{}{"a": "1"},
		},
		{
			name:     "nil child",
			base:     map[string]interface{}{"a": "1"},
			child:    nil,
			expected: map[string]interface{}{"a": "1"},
		},
		{
			name:     "both nil",
			base:     nil,
			child:    nil,
			expected: map[string]interface{}{},
		},
		{
			name:     "child scalar overrides base",
			base:     map[string]interface{}{"a": "old"},
			child:    map[string]interface{}{"a": "new"},
			expected: map[string]interface{}{"a": "new"},
		},
		{
			name: "child adds new key",
			base: map[string]interface{}{"a": "1"},
			child: map[string]interface{}{
				"a": "1",
				"b": "2",
			},
			expected: map[string]interface{}{"a": "1", "b": "2"},
		},
		{
			name: "nested maps merge recursively",
			base: map[string]interface{}{
				"top": map[string]interface{}{"a": "1", "b": "2"},
			},
			child: map[string]interface{}{
				"top": map[string]interface{}{"b": "override", "c": "3"},
			},
			expected: map[string]interface{}{
				"top": map[string]interface{}{"a": "1", "b": "override", "c": "3"},
			},
		},
		{
			name: "3-level nested merge",
			base: map[string]interface{}{
				"l1": map[string]interface{}{
					"l2": map[string]interface{}{
						"a": "base",
					},
				},
			},
			child: map[string]interface{}{
				"l1": map[string]interface{}{
					"l2": map[string]interface{}{
						"b": "child",
					},
				},
			},
			expected: map[string]interface{}{
				"l1": map[string]interface{}{
					"l2": map[string]interface{}{
						"a": "base",
						"b": "child",
					},
				},
			},
		},
		{
			name: "array replaces not merges",
			base: map[string]interface{}{
				"items": []interface{}{"a", "b"},
			},
			child: map[string]interface{}{
				"items": []interface{}{"c"},
			},
			expected: map[string]interface{}{
				"items": []interface{}{"c"},
			},
		},
		{
			name: "type conflict child wins",
			base: map[string]interface{}{
				"x": map[string]interface{}{"nested": "val"},
			},
			child: map[string]interface{}{
				"x": "scalar",
			},
			expected: map[string]interface{}{
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
