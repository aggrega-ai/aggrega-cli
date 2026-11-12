// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmchartcore

import (
	"encoding/json"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// TestFindActions_QuotingAndBraceDepthBoundTheActionSpan asserts the action-span
// contract on the shapes where scanning for the first `}}` picks the wrong
// boundary: a closing delimiter inside a quoted string, a brace-nested action
// inside a template comment, escaped quotes, and character-constant / raw-string
// arguments. The span is the whole action in every case. A wrong boundary is not
// a cosmetic error — it leaves chart bytes outside the hole table, so the
// document restores corrupted.
func TestFindActions_QuotingAndBraceDepthBoundTheActionSpan(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{
			// The helper-file usage-comment idiom: a template comment quoting a
			// whole action. The inner `}}` closes the quoted example, so brace
			// depth rather than the first `}}` ends the comment's span.
			name: "usage-comment-quoting-an-action",
			body: "{{/* Usage: {{ include \"mychart.labels\" . }} */}}\n",
			want: []string{"{{/* Usage: {{ include \"mychart.labels\" . }} */}}"},
		},
		{
			// A chart emitting a literal template string for something else to
			// render later: the `}}` inside the double quote is data, not a closer.
			name: "closing-delimiter-inside-a-double-quote",
			body: "data:\n  tmpl: {{ printf \"{{ .Release.Name }}\" }}\n",
			want: []string{"{{ printf \"{{ .Release.Name }}\" }}"},
		},
		{
			// A JSON-shaped default: the backslash escapes keep the scan inside the
			// string until its real closing quote, past the braces it contains.
			name: "backslash-escaped-quotes-in-a-default",
			body: "data:\n  cfg: {{ .Values.cfg | default \"{\\\"k\\\":\\\"v\\\"}\" }}\n",
			want: []string{"{{ .Values.cfg | default \"{\\\"k\\\":\\\"v\\\"}\" }}"},
		},
		{
			name: "character-constant-and-raw-string-arguments",
			body: "data:\n  ch: {{ printf \"%c\" 'x' }}\n  raw: {{ printf `%s` .Values.s }}\n",
			want: []string{"{{ printf \"%c\" 'x' }}", "{{ printf `%s` .Values.s }}"},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			acts, err := findActions([]byte(tc.body))
			if err != nil {
				t.Fatalf("findActions: %v", err)
			}
			got := make([]string, len(acts))
			for i, a := range acts {
				got[i] = a.text
				// The recorded text and the recorded span must be the same bytes;
				// blank writes by span and the hole table restores by text, so a
				// disagreement between them corrupts the round trip.
				if span := tc.body[a.start:a.end]; a.text != span {
					t.Errorf("action %d: text %q disagrees with its span [%d,%d) = %q", i, a.text, a.start, a.end, span)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d action(s) %q; want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("action %d = %q; want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestHelmCodec_UnterminatedActionOrQuoteHalts asserts the tokenizer refuses to
// guess a boundary. An action, or a quote inside one, left open is a halt naming
// the malformed construct and the byte offset the action started at, so the
// author can find it. The alternative — a silently truncated span — would blank
// the wrong bytes and restore a corrupt chart file.
func TestHelmCodec_UnterminatedActionOrQuoteHalts(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/deployment.yaml"
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{"action-never-closed", "metadata:\n  name: {{ .Values.name\n", "unterminated template action"},
		{"double-quote-never-closed", "metadata:\n  name: {{ printf \"abc }}\n", "unterminated double-quoted string"},
		{"character-constant-never-closed", "metadata:\n  name: {{ printf \"%c\" 'x }}\n", "unterminated single-quoted string"},
		{"raw-string-never-closed", "metadata:\n  name: {{ printf `abc }}\n", "unterminated backtick string"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := codec.Segment(name, []byte(tc.body))
			if err == nil {
				t.Fatalf("expected a halt for %s; got nil", tc.name)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantErr) {
				t.Errorf("error does not name the malformed construct %q: %v", tc.wantErr, err)
			}
			if !strings.Contains(msg, "at byte ") {
				t.Errorf("error does not locate the action by byte offset: %v", err)
			}
			if !strings.Contains(msg, name) {
				t.Errorf("error does not name the chart file: %v", err)
			}
		})
	}
}

// TestHelmCodec_ReassembleHaltsOnAnUntokenizableBody asserts the paired half
// refuses what Segment refuses. Reassemble re-derives the hole table from the
// body, so a body it cannot tokenize must halt rather than splice a patch into
// bytes it cannot map back to their holes.
func TestHelmCodec_ReassembleHaltsOnAnUntokenizableBody(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/deployment.yaml"
	body := []byte("metadata:\n  name: {{ .Values.name\n")
	patched := []frp.PatchedUnit{{Token: name, Tree: json.RawMessage(`{"metadata":{"name":"patched"}}`)}}

	out, _, err := codec.Reassemble(name, body, nil, patched)
	if err == nil {
		t.Fatalf("expected a halt; got nil (output %q)", out)
	}
	if !strings.Contains(err.Error(), "unterminated template action") {
		t.Errorf("error does not name the malformed construct: %v", err)
	}
	if out != nil {
		t.Errorf("expected no bytes alongside the halt; got %q", out)
	}
}

// TestHelmCodec_HolePositionDecidesPatchability asserts which template-action
// positions leave a chart document RFC-6902-addressable and which force it
// opaque. Two positions are ordinary value holes — a list item, and a value
// whose name merely contains a structure-emitting function's name — and one is
// not: a hole in a MAP KEY is control flow over the document's structure, so the
// document has no compile-time shape to address and the operator patches
// values.yaml instead.
func TestHelmCodec_HolePositionDecidesPatchability(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	for _, tc := range []struct {
		name       string
		body       string
		structured bool
	}{
		{
			// Holes in list-item and list-item-member positions are values.
			name:       "holes-in-list-positions",
			body:       "spec:\n  containers:\n    - image: {{ .Values.image }}\n      args:\n        - {{ .Values.arg }}\n",
			structured: true,
		},
		{
			// A templated map key: the document's key set is decided at deploy
			// time, so there is no compile-time path to address.
			name:       "templated-map-key",
			body:       "data:\n  {{ .Values.keyName }}: enabled\n",
			structured: false,
		},
		{
			// `includes` is a chart value, not an `include` call. The
			// function-name match is delimiter-bounded precisely so a value whose
			// name contains one stays a plain, patchable value hole.
			name:       "value-name-containing-a-function-name",
			body:       "data:\n  items: {{ .Values.includes }}\n",
			structured: true,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			units, err := codec.Segment("templates/"+tc.name+".yaml", []byte(tc.body))
			if err != nil {
				t.Fatalf("Segment: %v", err)
			}
			if len(units) != 1 {
				t.Fatalf("expected 1 unit, got %d", len(units))
			}
			if got := units[0].Structured(); got != tc.structured {
				t.Fatalf("Structured() = %v, want %v (tree=%s)", got, tc.structured, string(units[0].Tree))
			}
			// Whatever the classification, the guest's view is the real chart
			// source and the round trip is byte-identical.
			if string(units[0].Bytes) != tc.body {
				t.Errorf("unit bytes are not the original chart source:\n got %q\nwant %q", units[0].Bytes, tc.body)
			}
		})
	}
}
