// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTemplateReadsWithheldDelims_ReadsWithTheEvaluationsPair holds the reading
// behind every payload marking to the delimiter pair the template is evaluated
// with. A template read with the wrong pair has no actions and reads nothing, so a
// marking taken that way would be absent exactly where an alternate-delimiter
// provider's artifact reads a declared value — the row reading `[[ … ]]` text with
// the default pair is the control that shows the pair is what decides.
func TestTemplateReadsWithheldDelims_ReadsWithTheEvaluationsPair(t *testing.T) {
	t.Parallel()
	declared := redact.Keys{Values: map[string]bool{"adminPassword": true}}
	cases := []struct {
		name        string
		text        string
		left, right string
		keys        redact.Keys
		want        bool
	}{
		{"a default-pair template reading the declared value", "a: {{ .values.adminPassword }}", "{{", "}}", declared, true},
		{"an alternate-pair template reading the declared value", "a: [[ .values.adminPassword ]]", "[[", "]]", declared, true},
		{"the same alternate-pair text read with the default pair has no action", "a: [[ .values.adminPassword ]]", "{{", "}}", declared, false},
		{"an empty pair is the default pair", "a: {{ .values.adminPassword }}", "", "", declared, true},
		{"a template reading only an undeclared value", "a: {{ .values.adminNote }}", "{{", "}}", declared, false},
		{"a template handing on the whole of its data", "a: {{ toJson . }}", "{{", "}}", declared, true},
		{"a template that does not parse", "a: {{ .values.adminPassword ", "{{", "}}", declared, true},
		{"a set naming nothing reads nothing", "a: {{ .values.adminPassword }}", "{{", "}}", redact.Keys{}, false},
		{"text with no action reads nothing", "a: plain", "{{", "}}", declared, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := templateReadsWithheldDelims(tc.text, tc.left, tc.right, tc.keys); got != tc.want {
				t.Errorf("templateReadsWithheldDelims(%q, %q, %q) = %v, want %v", tc.text, tc.left, tc.right, got, tc.want)
			}
		})
	}
}

// TestNodeProjection_CarriesTheValuesMarkingOnlyWithValues pins where the marking
// over the values as a whole travels: on a projection that carries values — the
// sourced render and the input validation — and on no other, whatever the context
// holds, because it is a statement about that map.
func TestNodeProjection_CarriesTheValuesMarkingOnlyWithValues(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("auth", map[string]any{"password": "x"})
	cases := []struct {
		name   string
		marked bool
		values types.OrderedMap[any]
		want   bool
	}{
		{"marked context, values carried", true, values, true},
		{"marked context, no values carried", true, types.OrderedMap[any]{}, false},
		{"unmarked context, values carried", false, values, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NodeProjection(NodeRenderContext{NodeID: "api", SensitiveValues: tc.marked}, tc.values, nil)
			if got.SensitiveValues != tc.want {
				t.Errorf("SensitiveValues = %v, want %v", got.SensitiveValues, tc.want)
			}
		})
	}
}
