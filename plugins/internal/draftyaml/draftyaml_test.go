// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package draftyaml

import (
	"strings"
	"testing"
	"text/template"
)

// TestCommentLineReadsBackAsWrittenOnceEvaluated writes comment lines holding
// "{{" — in the line's own words, in a name written through Comment, and in a
// name Comment quotes — and requires each, placed in a YAML document evaluated as
// a template as the compiler evaluates an Aggregate, to read back as the line
// with each name as Comment writes it; a line holding none is written as it is.
//
// 🔴 PLANTED DEFECT: in importer.CommentLine
// (aggrega-pdk/go/pkg/importer/definition.go), return the line unescaped: the
// document no longer parses as a template, and every row holding "{{" fails.
func TestCommentLineReadsBackAsWrittenOnceEvaluated(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, indent, text string
	}{
		{"a line with no braces", "    ", "The model " + Comment("gpt-4o") + "."},
		{"a name written as it is", "    ", "The agent " + Comment("writer{{ topic }}") + "."},
		{"a name Comment quotes", "", "  - " + Comment("tools (beta) {{x}}\nnext")},
		{"two actions in one line", "      ", "Tools: " + Comment("a{{b}}") + ", " + Comment("{{c}}d") + "."},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			line := CommentLine(row.indent, row.text)
			if !strings.HasPrefix(line, row.indent+"# ") || !strings.HasSuffix(line, "\n") ||
				strings.Count(line, "\n") != 1 {
				t.Fatalf("CommentLine(%q, %q) = %q, want one line opening with the indent and the hash", row.indent,
					row.text, line)
			}
			if !strings.Contains(row.text, "{{") && line != row.indent+"# "+row.text+"\n" {
				t.Errorf("a line holding no double brace is written as %q, want it as it is", line)
			}
			doc := "spec:\n  components:\n" + line + "    a:\n      type: core/x/y\n"
			tmpl, err := template.New("aggregate").Parse(doc)
			if err != nil {
				t.Fatalf("the document does not parse as a template: %v\n%s", err, doc)
			}
			var evaluated strings.Builder
			if err := tmpl.Execute(&evaluated, nil); err != nil {
				t.Fatalf("the document does not run as a template: %v\n%s", err, doc)
			}
			if want := row.indent + "# " + row.text + "\n"; !strings.Contains(evaluated.String(), want) {
				t.Errorf("the comment reads back as\n%s\nwant %q", evaluated.String(), want)
			}
		})
	}
}

// TestInjectWritesOneMechanismsEntries writes an inject block of one
// mechanism, each name escaped so it reads back once evaluated, and InjectEnv
// as the block of the env mechanism; no entry writes nothing.
func TestInjectWritesOneMechanismsEntries(t *testing.T) {
	t.Parallel()
	entries := []Injected{{In: "properties_target_id", From: "id"}, {In: "DB_{{HOST}}", From: "host"}}
	want := "      inject:\n" +
		"        property:\n" +
		"          - in: \"properties_target_id\"\n" +
		"            from: id\n" +
		"          - in: \"DB_{{`{{`}}HOST}}\"\n" +
		"            from: host\n"
	if got := Inject("      ", "property", entries); got != want {
		t.Errorf("Inject wrote\n%s\nwant\n%s", got, want)
	}
	if got := InjectEnv("  ", entries[:1]); got != Inject("  ", MechanismEnv, entries[:1]) ||
		!strings.HasPrefix(got, "  inject:\n    env:\n") {
		t.Errorf("InjectEnv wrote\n%s\nwant the block of the env mechanism", got)
	}
	if got := Inject("  ", "property", nil); got != "" {
		t.Errorf("no entry wrote %q, want nothing", got)
	}
}
