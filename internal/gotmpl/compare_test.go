// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gotmpl

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"text/template"
)

// compareRun executes text over data once, with the assertion comparisons bound
// when bound is set and with the engine's own built-ins otherwise, returning the
// output and the error text.
func compareRun(t *testing.T, bound bool, text string, data any) (string, string) {
	t.Helper()
	tpl := template.New("t")
	if bound {
		tpl = tpl.Funcs(AssertionComparisons())
	}
	tpl, err := tpl.Parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	var b strings.Builder
	if err := tpl.Execute(&b, data); err != nil {
		return "", err.Error()
	}
	return b.String(), ""
}

// The operands the table crosses: every kind of value a comparison can meet, with
// a number in each of the three forms a template meets one in — an integer, a
// floating-point value and an authored number.
var compareOperands = map[string]any{
	"int3":       3,
	"float3":     3.0,
	"float2_5":   2.5,
	"authored3":  json.Number("3"),
	"authored10": json.Number("10"),
	"authored9":  json.Number("9"),
	"text3":      "3",
	"textA":      "a",
	"textB":      "b",
	"list":       []any{1, 2},
	"map":        map[string]any{"k": 1},
	"bool":       true,
	"absent":     nil,
}

// TestAssertionComparisons_EachOperatorOverEachKind is the table: each of the six
// operators over numbers carried as an integer, a floating-point value and an
// authored number, text, a list, a map, a true/false value and no value. A row
// either answers true or false, or is refused with a sentence naming the two
// kinds.
func TestAssertionComparisons_EachOperatorOverEachKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		expr    string
		want    string // "true" or "false" where it answers
		refusal string // the sentence's opening where it is refused
	}{
		// Numbers compare by value whatever carries them.
		{`eq .int3 .float3`, "true", ""},
		{`eq .authored3 .int3`, "true", ""},
		{`eq .authored3 .float3`, "true", ""},
		{`eq .authored3 .float2_5`, "false", ""},
		{`ne .authored3 .float2_5`, "true", ""},
		{`ne .authored3 .int3`, "false", ""},
		{`lt .authored3 .int3`, "false", ""},
		{`lt .float2_5 .authored3`, "true", ""},
		{`le .authored3 .float3`, "true", ""},
		{`le .authored10 .int3`, "false", ""},
		{`gt .authored10 .authored9`, "true", ""}, // by value, not by digits
		{`gt .float2_5 .int3`, "false", ""},
		{`ge .int3 .authored3`, "true", ""},
		{`ge .float2_5 .authored3`, "false", ""},
		{`lt .authored9 .authored10`, "true", ""},
		{`eq .authored3 1 2 3`, "true", ""},
		{`eq .authored3 1 2`, "false", ""},
		// Text with text, as the engine's built-ins answer it.
		{`eq .text3 "3"`, "true", ""},
		{`lt .textA .textB`, "true", ""},
		{`ge .textA .textB`, "false", ""},
		{`ne .textA .textB`, "true", ""},
		// True/false and no value, equality only.
		{`eq .bool true`, "true", ""},
		{`ne .bool false`, "true", ""},
		{`eq .absent .absent`, "true", ""},
		{`eq .absent .int3`, "false", ""},
		{`ne .absent .text3`, "true", ""},
		// A number never compares with text.
		{`eq .authored3 .text3`, "", "cannot compare a number with text"},
		{`ne .int3 .text3`, "", "cannot compare a number with text"},
		{`lt .text3 .authored3`, "", "cannot compare text with a number"},
		{`ge .float3 "3"`, "", "cannot compare a number with text"},
		// Lists, maps, true/false and no value cannot be ordered.
		{`lt .list 2`, "", "cannot compare a list with a number"},
		{`gt .map .map`, "", "cannot compare a map with a map"},
		{`le .bool .bool`, "", "cannot compare a true/false value with a true/false value"},
		{`lt .absent .int3`, "", "cannot compare no value with a number"},
		{`eq .list .list`, "", "cannot compare a list with a list"},
		{`eq .map .text3`, "", "cannot compare a map with text"},
		{`eq .bool .int3`, "", "cannot compare a true/false value with a number"},
		{`eq .int3`, "", "needs a second value to compare the first with"},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			out, errText := compareRun(t, true, "{{ "+tc.expr+" }}", compareOperands)
			if tc.refusal == "" {
				if errText != "" {
					t.Fatalf("%s refused: %s", tc.expr, errText)
				}
				if out != tc.want {
					t.Errorf("%s = %s, want %s", tc.expr, out, tc.want)
				}
				return
			}
			if errText == "" {
				t.Fatalf("%s answered %s, want the refusal %q", tc.expr, out, tc.refusal)
			}
			if !strings.Contains(errText, tc.refusal) {
				t.Errorf("%s refused with %q, want it to contain %q", tc.expr, errText, tc.refusal)
			}
		})
	}
}

// goTypeNames is the set of Go type spellings a refusal must never carry: what
// the engine's own refusal names, and what a value's dynamic type would print as.
var goTypeNames = regexp.MustCompile(`json\.Number|\bfloat(32|64)\b|\bu?int(8|16|32|64)?\b|\bstring\b|interface \{\}|\[\]any|map\[`)

// TestAssertionComparisons_RefusalsNameKindsNeverGoTypes pins the refusal's
// wording over every pair of kinds that is refused: it names the two kinds in an
// author's words, the first value's kind first, and never a Go type.
func TestAssertionComparisons_RefusalsNameKindsNeverGoTypes(t *testing.T) {
	t.Parallel()
	refused := []struct{ expr, first, second string }{
		{`lt .authored3 .text3`, "a number", "text"},
		{`eq .text3 .authored3`, "text", "a number"},
		{`gt .list .int3`, "a list", "a number"},
		{`ne .map .bool`, "a map", "a true/false value"},
		{`le .absent .float2_5`, "no value", "a number"},
	}
	for _, r := range refused {
		_, errText := compareRun(t, true, "{{ "+r.expr+" }}", compareOperands)
		if errText == "" {
			t.Errorf("%s answered; want a refusal", r.expr)
			continue
		}
		want := "cannot compare " + r.first + " with " + r.second
		if !strings.Contains(errText, want) {
			t.Errorf("%s refused with %q, want %q", r.expr, errText, want)
		}
		// The engine's prefix carries the template's own text, which may legitimately
		// spell anything; only the sentence after "error calling" is the refusal's.
		sentence := errText[strings.Index(errText, "error calling"):]
		if m := goTypeNames.FindString(sentence); m != "" {
			t.Errorf("%s: the refusal names the Go type %q: %q", r.expr, m, sentence)
		}
	}

	// The matcher fires on the engine's own refusal of the same comparison, which
	// names Go types — so its silence above is a finding about the wording.
	_, engine := compareRun(t, false, `{{ lt .authored3 .int3 }}`, compareOperands)
	if goTypeNames.FindString(engine) == "" {
		t.Fatalf("calibration: the engine's refusal %q drew no match, so the check above proves nothing", engine)
	}
}

// TestAssertionComparisons_AgreeWithTheEngineWhereItAnswers holds every case the
// engine's built-ins answer without a refusal, over operands no authored number
// is among, to the engine's answer: the bindings change what a number is, and
// nothing else.
func TestAssertionComparisons_AgreeWithTheEngineWhereItAnswers(t *testing.T) {
	t.Parallel()
	data := map[string]any{
		"i1": 1, "i2": 2, "f1": 1.5, "f2": 2.5, "u2": uint(2),
		"s1": "apple", "s2": "banana", "t": true, "f": false, "n": nil,
	}
	exprs := []string{
		`eq .i1 .i2`, `eq .i2 .i2`, `ne .i1 .i2`, `lt .i1 .i2`, `le .i2 .i2`, `gt .i1 .i2`, `ge .i2 .i1`,
		`eq .f1 .f2`, `lt .f1 .f2`, `ge .f2 .f2`, `gt .f2 .f1`,
		`eq .i2 .u2`, `lt .i1 .u2`, `ge .u2 .i2`,
		`eq .s1 .s2`, `eq .s1 "apple"`, `ne .s1 .s2`, `lt .s1 .s2`, `le .s2 .s1`, `gt .s2 .s1`, `ge .s1 .s1`,
		`eq .t true`, `eq .t .f`, `ne .t .f`,
		`eq .n .n`, `eq .n .i1`, `ne .n .s1`,
		`eq .i2 1 2 3`, `eq .s1 "x" "apple"`,
	}
	for _, expr := range exprs {
		engineOut, engineErr := compareRun(t, false, "{{ "+expr+" }}", data)
		if engineErr != "" {
			t.Fatalf("%s: the engine refused (%s); this table holds only cases it answers", expr, engineErr)
		}
		boundOut, boundErr := compareRun(t, true, "{{ "+expr+" }}", data)
		if boundErr != "" {
			t.Errorf("%s: refused (%s) where the engine answered %s", expr, boundErr, engineOut)
			continue
		}
		if boundOut != engineOut {
			t.Errorf("%s = %s, the engine answers %s", expr, boundOut, engineOut)
		}
	}
}

// TestAssertionComparisons_HelpersCarryNoComparison pins that the shared helper
// set does not bind the comparisons: a surface taking Helpers or FuncMap keeps
// the engine's built-ins, and only a surface merging AssertionComparisons reads
// numbers as numbers.
func TestAssertionComparisons_HelpersCarryNoComparison(t *testing.T) {
	t.Parallel()
	funcs := FuncMap()
	for name := range AssertionComparisons() {
		if _, bound := funcs[name]; bound {
			t.Errorf("FuncMap binds %q; the comparisons are the assertion surface's alone", name)
		}
	}
}
