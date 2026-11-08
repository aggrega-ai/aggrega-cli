// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"fmt"
	"slices"
	"testing"
	"text/template/parse"
)

// TestWalkPipelines_VisitsEveryPipelineWithItsDot walks each node kind a pipeline
// sits in, one row per kind, and reads both what is visited and the dot's standing
// reported with it. A kind the walk missed would leave its row short, and a wrong
// standing would move a row's flag — the two things every reader of the walk relies
// on: the census for what it records as read, and the reader of pipeline heads for
// which commands exist.
func TestWalkPipelines_VisitsEveryPipelineWithItsDot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want []string // each visited pipeline as the source spells it, then "data" or "rebound"
	}{
		{name: "an action", text: `{{ .a }}`, want: []string{".a data"}},
		{name: "an if, its branch and its else", text: `{{ if .a }}{{ .b }}{{ else }}{{ .c }}{{ end }}`,
			want: []string{".a data", ".b data", ".c data"}},
		{name: "a range: its pipeline holds the data, its body is rebound, its else is not",
			text: `{{ range .a }}{{ .b }}{{ else }}{{ .c }}{{ end }}`,
			want: []string{".a data", ".b rebound", ".c data"}},
		{name: "a with: its pipeline holds the data, its body is rebound, its else is not",
			text: `{{ with .a }}{{ .b }}{{ else }}{{ .c }}{{ end }}`,
			want: []string{".a data", ".b rebound", ".c data"}},
		{name: "a nested body stays rebound", text: `{{ range .a }}{{ if .b }}{{ .c }}{{ end }}{{ end }}`,
			want: []string{".a data", ".b rebound", ".c rebound"}},
		{name: "a template invocation", text: `{{ define "x" }}{{ end }}{{ template "x" .a }}`,
			want: []string{".a data"}},
		{name: "a parenthesized pipeline, after the one it sits in", text: `{{ printf "%s" (lower .a) }}`,
			want: []string{`printf "%s" (lower .a) data`, "lower .a data"}},
		{name: "a chain's receiver", text: `{{ (index .a "k").b }}`,
			want: []string{`(index .a "k").b data`, `index .a "k" data`}},
		{name: "a pipeline inside a rebound body's arguments", text: `{{ range .a }}{{ printf "%s" (lower .b) }}{{ end }}`,
			want: []string{".a data", `printf "%s" (lower .b) rebound`, "lower .b rebound"}},
		{name: "text and comments contribute nothing", text: `plain {{/* note */}} text`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root, err := ParseTree(c.text, "{{", "}}")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var got []string
			WalkPipelines(root, func(p *parse.PipeNode, dotIsData bool) {
				standing := "rebound"
				if dotIsData {
					standing = "data"
				}
				got = append(got, fmt.Sprintf("%s %s", p.String(), standing))
			})
			if !slices.Equal(got, c.want) {
				t.Errorf("visited %q, want %q", got, c.want)
			}
		})
	}
}

// TestWalkPipelines_NilRoots pins the walk's guards: a nil node, a nil list and a
// nil pipeline visit nothing and do not panic.
func TestWalkPipelines_NilRoots(t *testing.T) {
	t.Parallel()
	visited := 0
	visit := func(*parse.PipeNode, bool) { visited++ }
	WalkPipelines(nil, visit)
	WalkPipelines((*parse.ListNode)(nil), visit)
	walkPipeline(nil, true, visit)
	if visited != 0 {
		t.Errorf("visited %d pipelines under nil roots, want 0", visited)
	}
}
