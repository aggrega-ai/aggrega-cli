// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
	"text/template"
)

// indexRun executes text over data twice under one function set: once with the
// set's `index` binding left out, so Go's own `index` answers, and once with the
// set whole, so the strict one does. It returns each side's output and error
// text. Nothing else differs between the two runs — the same functions, the same
// template name — so an answer that differs is the `index` binding's, and an
// error the engine prefixes with the template's position reads the same on both.
func indexRun(t *testing.T, funcs template.FuncMap, text string, data any) (builtinOut, builtinErr, strictOut, strictErr string, strictErrValue error) {
	t.Helper()
	engineFuncs := maps.Clone(funcs)
	delete(engineFuncs, "index")
	run := func(set template.FuncMap) (string, error) {
		tpl, err := template.New("t").Option("missingkey=zero").Funcs(set).Parse(text)
		if err != nil {
			t.Fatalf("parse %q: %v", text, err)
		}
		var b strings.Builder
		err = tpl.Execute(&b, data)
		return b.String(), err
	}
	errText := func(err error) string {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	bOut, bErr := run(engineFuncs)
	sOut, sErr := run(funcs)
	return bOut, errText(bErr), sOut, errText(sErr), sErr
}

// TestStrictIndexAnswersAsTheBuiltinOutsideAnAbsentMapKey holds the deliberate
// near-duplicate to the engine it reproduces: every case below is one the
// engine's own `index` answers without meeting an absent map key, and the strict
// binding must answer each byte for byte the same — the value printed, or the
// error and its text. A Go upgrade changing the engine's answer fails here rather
// than leaving the two to drift.
func TestStrictIndexAnswersAsTheBuiltinOutsideAnAbsentMapKey(t *testing.T) {
	var nilMapPtr *map[string]any
	cases := []struct {
		name string
		text string
		data any
	}{
		{"present string key", `{{ index .m "a" }}`, map[string]any{"m": map[string]any{"a": "x"}}},
		{"present key holding an empty string", `[{{ index .m "a" }}]`, map[string]any{"m": map[string]any{"a": ""}}},
		{"present key holding null prints the placeholder", `{{ index .m "a" }}`, map[string]any{"m": map[string]any{"a": nil}}},
		{"present key holding false", `{{ index .m "a" }}`, map[string]any{"m": map[string]any{"a": false}}},
		{"nested present keys", `{{ index .m "a" "b" }}`, map[string]any{"m": map[string]any{"a": map[string]any{"b": 7}}}},
		{"typed string map", `{{ index .m "a" }}`, map[string]any{"m": map[string]string{"a": "s"}}},
		{"integer key converted to the map's key type", `{{ index .m 1 }}`, map[string]any{"m": map[int8]string{1: "one"}}},
		{"no key returns the item", `{{ index .m }}`, map[string]any{"m": []int{1, 2}}},
		{"slice position", `{{ index .s 1 }}`, map[string]any{"s": []string{"a", "b"}}},
		{"array position", `{{ index .a 0 }}`, map[string]any{"a": [2]int{5, 6}}},
		{"string position", `{{ index .str 1 }}`, map[string]any{"str": "hey"}},
		{"slice then map", `{{ index .s 0 "k" }}`, map[string]any{"s": []any{map[string]any{"k": "v"}}}},
		{"position past the end", `{{ index .s 5 }}`, map[string]any{"s": []string{"a"}}},
		{"position equal to the length", `{{ index .s 1 }}`, map[string]any{"s": []string{"a"}}},
		{"negative position", `{{ index .s -1 }}`, map[string]any{"s": []string{"a"}}},
		{"string position equal to the length", `{{ index .str 3 }}`, map[string]any{"str": "hey"}},
		{"slice indexed by a string", `{{ index .s "x" }}`, map[string]any{"s": []string{"a"}}},
		{"slice indexed by nil", `{{ index .s nil }}`, map[string]any{"s": []string{"a"}}},
		{"map indexed by a key of the wrong type", `{{ index .m 3 }}`, map[string]any{"m": map[string]any{"a": 1}}},
		{"map indexed by nil", `{{ index .m nil }}`, map[string]any{"m": map[string]any{"a": 1}}},
		{"an item that cannot be indexed", `{{ index .n 0 }}`, map[string]any{"n": 42}},
		{"index of untyped nil", `{{ index nil 0 }}`, map[string]any{}},
		{"index through a present null", `{{ index .m "a" "b" }}`, map[string]any{"m": map[string]any{"a": nil}}},
		{"index of a nil pointer", `{{ index .p "a" }}`, map[string]any{"p": nilMapPtr}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bOut, bErr, sOut, sErr, _ := indexRun(t, BuiltinReplacements(), tc.text, tc.data)
			if bOut != sOut || bErr != sErr {
				t.Errorf("the strict index answers %q (error %q) where the engine's answers %q (error %q)",
					sOut, sErr, bOut, bErr)
			}
		})
	}
}

// TestStrictIndexHaltsOnAnAbsentMapKey is the one case the strict binding answers
// differently, and every row first proves the engine's own `index` tolerates it —
// so each row is a read the strict binding newly halts, never one that already
// failed.
//
// The rows run under [FuncMap], the set the renderer evaluates its templates
// with, because one row reads through `index` and then applies Sprig's `default`:
// a set without Sprig refuses that row at parse, before either `index` runs, and
// the row would then prove nothing about the halt. The engine's side is the same
// set with its `index` binding left out, so the two sides differ in that binding
// alone.
func TestStrictIndexHaltsOnAnAbsentMapKey(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		data    any
		wantKey string
	}{
		{"printed read", `{{ index .m "missing" }}`, map[string]any{"m": map[string]any{"a": 1}}, `key "missing"`},
		{"read by a computed key", `{{ $k := "VLLM_API_KEY" }}{{ index .m $k }}`, map[string]any{"m": map[string]any{"OTHER": "s"}}, `key "VLLM_API_KEY"`},
		{"read piped through default", `{{ index .m "missing" | default "fb" }}`, map[string]any{"m": map[string]any{}}, `key "missing"`},
		{"read tested by if", `{{ if index .m "missing" }}y{{ end }}`, map[string]any{"m": map[string]any{}}, `key "missing"`},
		{"read tested by with", `{{ with index .m "missing" }}y{{ end }}`, map[string]any{"m": map[string]any{}}, `key "missing"`},
		{"second key absent", `{{ index .m "a" "missing" }}`, map[string]any{"m": map[string]any{"a": map[string]any{"b": 1}}}, `key "missing"`},
		{"nil map", `{{ index .m "missing" }}`, map[string]any{"m": map[string]any(nil)}, `key "missing"`},
		{"typed string map", `{{ index .m "missing" }}`, map[string]any{"m": map[string]string{"a": "s"}}, `key "missing"`},
		{"integer-keyed map", `{{ index .m 2 }}`, map[string]any{"m": map[int]string{1: "one"}}, `key 2`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, bErr, sOut, _, sErr := indexRun(t, FuncMap(), tc.text, tc.data)
			if bErr != "" {
				t.Fatalf("the engine's own index fails here too (%s), so the row proves nothing about the strict one", bErr)
			}
			if sErr == nil {
				t.Fatalf("the strict index rendered %q for a map key the map does not hold; want a halt", sOut)
			}
			if !errors.Is(sErr, ErrIndexOfAbsentMapKey()) {
				t.Errorf("the halt does not carry the condition: %v", sErr)
			}
			if !strings.Contains(sErr.Error(), tc.wantKey) {
				t.Errorf("the halt does not name the key (%s): %v", tc.wantKey, sErr)
			}
			if !strings.Contains(sErr.Error(), "error calling index") {
				t.Errorf("the halt lost the engine's position prefix, which names the failing action: %v", sErr)
			}
		})
	}
}

// TestPresenceTestingReadsDoNotHalt pins the guard forms the documentation teaches
// in place of `index` + `default`: each reads an absent key through the full
// function set without halting, and renders what it rendered before.
func TestPresenceTestingReadsDoNotHalt(t *testing.T) {
	data := map[string]any{"m": map[string]any{"present": "v"}}
	cases := []struct {
		text string
		want string
	}{
		{`{{ optional .m "missing" "fb" }}`, "fb"},
		{`[{{ optional .m "missing" "" }}]`, "[]"},
		{`[{{ get .m "missing" }}]`, "[]"},
		{`{{ if hasKey .m "missing" }}y{{ else }}n{{ end }}`, "n"},
		{`{{ if and (hasKey .m "missing") (index .m "missing") }}y{{ else }}n{{ end }}`, "n"},
		{`{{ dig "missing" "fb" .m }}`, "fb"},
		{`{{ index .m "present" }}`, "v"},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			tpl, err := template.New("t").Funcs(FuncMap()).Parse(tc.text)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var b strings.Builder
			if err := tpl.Execute(&b, data); err != nil {
				t.Fatalf("a presence-testing read halted: %v", err)
			}
			if b.String() != tc.want {
				t.Errorf("rendered %q, want %q", b.String(), tc.want)
			}
		})
	}
}

// TestEveryBindingCarriesTheOneStrictIndex holds the single definition: the set a
// Sprig-only surface merges, the helper set and the full set all bind `index` to
// the same function — one implementation reached three ways, never a copy per
// binding.
func TestEveryBindingCarriesTheOneStrictIndex(t *testing.T) {
	want := reflect.ValueOf(strictIndex).Pointer()
	for name, funcs := range map[string]template.FuncMap{
		"BuiltinReplacements": BuiltinReplacements(),
		"Helpers":             Helpers(),
		"FuncMap":             FuncMap(),
	} {
		fn, ok := funcs["index"]
		if !ok {
			t.Errorf("%s binds no index, so a template parsed with it reads Go's own", name)
			continue
		}
		if got := reflect.ValueOf(fn).Pointer(); got != want {
			t.Errorf("%s binds index to a function other than the strict one", name)
		}
	}
}
