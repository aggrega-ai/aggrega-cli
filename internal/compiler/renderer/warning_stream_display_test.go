// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// wsdWarningLines drives a real render whose fixture raises an advisory from
// three separate sites at once, and returns the lines a terminal would print for
// them, in the order the render raised them.
//
// Three sites rather than one is the point. Each composes its own attribution,
// so a change that keeps one site's line and moves another's passes a
// single-site check and fails this one; and the ORDER across the three is a
// property of where each drains into the result, which a per-site check cannot
// see at all.
func wsdWarningLines(t *testing.T) []string {
	t.Helper()

	input := testInput()
	// Two authored infra keys the mapping never reads — the unread-key reading.
	input.Graph.Spec.InfraMapping = func() types.OrderedMap[json.RawMessage] {
		m := types.OrderedMap[json.RawMessage]{}
		m.Set("api", json.RawMessage(`{"replicas":3,"cpu":"500m"}`))
		return m
	}()

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "ghost-recipe"},
				Spec: types.RecipeSpec{
					ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest", "port": {{ .values.port }}}`},
					// A promoted label the component does not declare — the
					// output-label reading.
					OutputComponentLabels: []string{"tier"},
					// A pipeline the render cannot execute — the validate-only notice.
					Pipeline: []types.PipelineStep{{ID: "s1", Plugin: "example/core/nothing@v1"}},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(t.Context(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	lines := make([]string, 0, len(result.Warnings))
	for _, d := range result.Warnings {
		lines = append(lines, renderer.RenderDiagnosticText(d))
	}
	return lines
}

// wsdWantLines is what a terminal prints for that fixture, byte for byte and in
// order. It is not an aspiration: every line was captured from a real build, and
// a line that moves by one character fails here.
//
// Each line's attribution — the `node "api": ` prefix — is composed by the one
// display formatter from a position the raising site carries as a field, rather
// than written into the message by the site itself. Holding the whole stream
// still is what makes that composition invisible to a reader, which is the
// property these bytes exist to guard.
var wsdWantLines = []string{
	`node "api": .infra key "cpu" is read by no template in recipe "ghost-recipe" — correct the spelling, or remove the key`,
	`node "api": .infra key "replicas" is read by no template in recipe "ghost-recipe" — correct the spelling, or remove the key`,
	`node "api": pipeline (1 steps) validated but not executed (no plugin path resolver configured)`,
	`node "api": recipe "ghost-recipe" promotes component label "tier", which component "core/compute/api-server" does not declare; no label is stamped for it`,
}

// TestRenderAll_WarningStreamDisplayIsUnchanged is the byte-identity guard on
// what a build tells its operator.
//
// The advisory stream carries structured entries so a consumer that reads no
// terminal can act on them, and the entries are assembled from what each raising
// site holds rather than from text it composed. That substitution is invisible to
// a reader only if every line still renders identically, which is what this
// asserts — the whole stream, in order, exactly.
func TestRenderAll_WarningStreamDisplayIsUnchanged(t *testing.T) {
	t.Parallel()

	got := wsdWarningLines(t)
	if len(got) != len(wsdWantLines) {
		t.Fatalf("the stream changed length: got %d line(s), want %d\ngot:\n%s",
			len(got), len(wsdWantLines), strings.Join(got, "\n"))
	}
	for i := range wsdWantLines {
		if got[i] != wsdWantLines[i] {
			t.Errorf("line %d moved:\n got: %q\nwant: %q", i, got[i], wsdWantLines[i])
		}
	}
}

// TestRenderAll_WarningStreamDisplayComparisonCanFail is the negative control on
// the guard above, and it is not decoration: an equality assertion whose failure
// mode nobody has demonstrated is indistinguishable from one that compares
// nothing. It plants a single byte in the expectation and requires the comparison
// to reject it.
//
// The planted byte is a character, not a whole line, because the failure worth
// catching is the small one — a re-worded attribution or a moved quote — and a
// control that only proves a wholly different line is rejected would leave that
// class unproven.
func TestRenderAll_WarningStreamDisplayComparisonCanFail(t *testing.T) {
	t.Parallel()

	got := wsdWarningLines(t)
	if len(got) == 0 {
		t.Fatal("the fixture raised no advisory, so the control asserts nothing")
	}

	planted := append([]string(nil), wsdWantLines...)
	// One byte: the node's name loses its last character.
	planted[0] = strings.Replace(planted[0], `node "api"`, `node "ap"`, 1)
	if planted[0] == wsdWantLines[0] {
		t.Fatal("the plant did not change the expectation, so this control proves nothing")
	}

	var rejected bool
	for i := range planted {
		if got[i] != planted[i] {
			rejected = true
			break
		}
	}
	if !rejected {
		t.Errorf("the comparison accepted a one-byte-different expectation, so it cannot detect a moved line:\n got: %q\nplanted: %q",
			got[0], planted[0])
	}
}
