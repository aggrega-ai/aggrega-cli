// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"slices"
	"testing"
)

// TestRootKeysRead pins which keys of one root a template is found to read, and —
// the half a caller relies on — every shape that makes the answer incomplete.
//
// A caller evaluates the keys returned here and leaves the rest unevaluated, so a
// row reporting complete for a template that can reach another key would hand that
// template a root missing a value it reads. The incomplete rows are therefore the
// ones that matter most, and each names a way of reaching the root's keys without
// spelling one.
func TestRootKeysRead(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name     string
		text     string
		keys     []string
		complete bool
	}{
		// Complete: every read spells its key.
		{"no template", "1.0.0", nil, true},
		{"empty text", "", nil, true},
		{"a direct read", "{{ .locals.version }}", []string{"version"}, true},
		{"a read below a key names the key", "{{ .locals.chart.name }}", []string{"chart"}, true},
		{"two reads, first-appearance order, deduplicated",
			"{{ .locals.b }}-{{ .locals.a }}-{{ .locals.b }}", []string{"b", "a"}, true},
		{"through the root variable", "{{ $.locals.version }}", []string{"version"}, true},
		{"inside a range body, through the root variable",
			"{{ range .values.list }}{{ $.locals.v }}{{ end }}", []string{"v"}, true},
		{"a with body over one key", "{{ with .locals.chart }}{{ .name }}{{ end }}", []string{"chart"}, true},
		{"a function the render registers is still read through",
			"{{ add1 (int .locals.port) }}", []string{"port"}, true},
		{"a read of another root", "{{ optional .infra `source_version` \"1.0.0\" }}", nil, true},
		{"a key of the same name under another root", "{{ .values.locals.x }}", nil, true},
		{"the root's name inside a with body over another root",
			"{{ with .values }}{{ .locals.x }}{{ end }}", nil, true},
		{"the root's name in a string literal", `{{ printf "%s" ".locals.x" }}`, nil, true},
		{"a range element's field", "{{ range .values.list }}{{ .locals }}{{ end }}", nil, true},

		// Incomplete: the root is read whole.
		{"index over the root", `{{ index .locals "version" }}`, nil, false},
		{"optional over the root", "{{ optional .locals `version` \"1.0.0\" }}", nil, false},
		{"ranging the root", "{{ range $k, $v := .locals }}{{ $v }}{{ end }}", nil, false},
		{"a with body over the root", "{{ with .locals }}{{ .version }}{{ end }}", nil, false},
		{"the root bound to a variable", "{{ $l := .locals }}{{ $l.version }}", nil, false},
		{"the root through the root variable, bound", "{{ $l := $.locals }}{{ index $l `v` }}", nil, false},

		// Incomplete: the context itself is handed on. Binding it to a variable is
		// handing it on too, even where every later read through the variable spells
		// its key: the binding is read as the whole context, exactly as binding the
		// root itself is read as the whole root above.
		{"the bare dot", "{{ . }}", nil, false},
		{"a variable bound to the context, then a key read through it",
			"{{ $r := . }}{{ $r.locals.version }}", nil, false},
		{"index over the dot", `{{ index . "locals" "version" }}`, nil, false},
		{"index over the root variable", `{{ index $ "locals" "version" }}`, nil, false},
		{"a variable bound to the context, handed on", `{{ $r := . }}{{ index $r "locals" "v" }}`, nil, false},
		{"a with body over the context", `{{ with $ }}{{ index . "locals" "v" }}{{ end }}`, nil, false},
		{"a named template given the dot", `{{ define "v" }}{{ .locals.v }}{{ end }}{{ template "v" . }}`, nil, false},
	}
	for _, row := range rows {
		keys, complete, err := RootKeysRead(row.text, "{{", "}}", LocalsContextRoot)
		if err != nil {
			t.Errorf("%s: unexpected parse error: %v", row.name, err)
			continue
		}
		if complete != row.complete {
			t.Errorf("%s: complete = %v, want %v (keys %q)", row.name, complete, row.complete, keys)
		}
		if row.complete && !slices.Equal(keys, row.keys) {
			t.Errorf("%s: keys = %q, want %q", row.name, keys, row.keys)
		}
	}
}

// TestRootKeysReadReportsAParseError: a text no function set parses yields its
// error and no answer about its keys, so the caller can leave the text to the
// evaluation that reports it.
func TestRootKeysReadReportsAParseError(t *testing.T) {
	t.Parallel()
	keys, complete, err := RootKeysRead("{{ .locals.v ", "{{", "}}", LocalsContextRoot)
	if err == nil {
		t.Fatalf("an unclosed action parsed; keys %q complete %v", keys, complete)
	}
	if complete || keys != nil {
		t.Errorf("a text that does not parse reported keys %q complete %v", keys, complete)
	}
}

// TestCollectRootRefsDoesNotReportAWholeRootRead: recording a whole-context read
// for RootKeysRead must not add a reference with no path to the walk every
// reference check reads.
func TestCollectRootRefsDoesNotReportAWholeRootRead(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"{{ . }}", "{{ $ }}", "{{ $r := . }}{{ $r }}", `{{ index . "locals" }}`} {
		for _, ref := range CollectRootRefs(mustParseForRoots(t, text)) {
			if len(ref.Segs) == 0 || ref.Segs[0] == "" {
				t.Errorf("%s: the walk reported a reference with no root: %+v", text, ref)
			}
		}
	}
}
