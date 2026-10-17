// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selectormatch

import "testing"

// TestMatchReadsASocketSelectorInTheCompilersGrammar holds Match to the
// selectors the flow platforms' sockets write — an equality on every term, a set
// membership with a key that need only be present — against labels a Component
// carries, handed as a map: each satisfied, each refused on the one label it
// lacks, and a selector that does not read refused with an error rather than
// read as a match.
func TestMatchReadsASocketSelectorInTheCompilersGrammar(t *testing.T) {
	t.Parallel()
	mcp := map[string]string{"component.aggrega.ai/category": "ai-tool", "tool.aggrega.ai/transport": "http",
		"tool.aggrega.ai/impact": "mutating"}
	model := map[string]string{"component.aggrega.ai/category": "ai-model", "model.aggrega.ai/api": "openai"}
	const tools = "component.aggrega.ai/category=ai-tool, tool.aggrega.ai/transport=http"
	const models = "component.aggrega.ai/category in (ai-model, gateway), model.aggrega.ai/api"
	for _, tc := range []struct {
		name, selector string
		labels         map[string]string
		want           bool
	}{
		{name: "a tool reached over HTTP", selector: tools, labels: mcp, want: true},
		{name: "a tool reached otherwise", selector: tools,
			labels: map[string]string{"component.aggrega.ai/category": "ai-tool"}},
		{name: "a model naming its API", selector: models, labels: model, want: true},
		{name: "a model naming none", selector: models,
			labels: map[string]string{"component.aggrega.ai/category": "ai-model"}},
		{name: "no labels", selector: tools},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Match(tc.selector, tc.labels)
			if err != nil {
				t.Fatalf("Match(%q) refused the selector: %v", tc.selector, err)
			}
			if got != tc.want {
				t.Errorf("Match(%q, %v) = %v, want %v", tc.selector, tc.labels, got, tc.want)
			}
		})
	}
	if ok, err := Match("component.aggrega.ai/category in (", mcp); err == nil {
		t.Errorf("a selector that does not read was answered %v with no error", ok)
	}
}
