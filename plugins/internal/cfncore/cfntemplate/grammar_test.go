// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfntemplate

import (
	"reflect"
	"testing"
)

func TestSubPiecesKeepsBangVariablesLiteral(t *testing.T) {
	t.Parallel()
	got := SubPieces("a${!B}c${D.E}")
	want := []SubPiece{{Text: "a${B}c"}, {Text: "D.E", Variable: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SubPieces = %#v, want %#v", got, want)
	}
	if vars := SubVariables("a${!B}c${D.E}${D.E}${F}"); !reflect.DeepEqual(vars, []string{"D.E", "F"}) {
		t.Errorf("SubVariables = %#v, want [D.E F]", vars)
	}
}

func TestSubPiecesReadsUnclosedAndEmptyAsLiteral(t *testing.T) {
	t.Parallel()
	got := SubPieces("x${}y${open")
	want := []SubPiece{{Text: "x${}y${open"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SubPieces = %#v, want %#v", got, want)
	}
	if got := SubPieces(""); got != nil {
		t.Errorf("SubPieces(\"\") = %#v, want nil", got)
	}
}

func TestSubPartsReadsEveryShape(t *testing.T) {
	t.Parallel()
	vars := map[string]any{"N": map[string]any{"Ref": "Foo"}}
	cases := []struct {
		name     string
		arg      any
		wantText string
		wantVars map[string]any
	}{
		{"string", "${A}", "${A}", map[string]any{}},
		{"list", []any{"${N}", vars}, "${N}", vars},
		{"list without map", []any{"${N}"}, "${N}", map[string]any{}},
		{"empty list", []any{}, "", map[string]any{}},
		{"other", 5, "", map[string]any{}},
	}
	for _, c := range cases {
		text, got := SubParts(c.arg)
		if text != c.wantText || !reflect.DeepEqual(got, c.wantVars) {
			t.Errorf("%s: SubParts = (%q, %#v), want (%q, %#v)", c.name, text, got, c.wantText, c.wantVars)
		}
	}
}

func TestGetAttPartsReadsBothForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		arg      any
		id, attr string
		ok       bool
	}{
		{"list", []any{"A", "B"}, "A", "B", true},
		{"string", "A.B.C", "A", "B.C", true},
		{"string without dot", "A", "A", "", true},
		{"list of one", []any{"A"}, "A", "", true},
		{"empty list", []any{}, "", "", false},
		{"empty string", "", "", "", false},
		{"list with a mapping first", []any{map[string]any{"Ref": "X"}, "B"}, "", "", false},
		{"other", 5, "", "", false},
	}
	for _, c := range cases {
		id, attr, ok := GetAttParts(c.arg)
		if id != c.id || attr != c.attr || ok != c.ok {
			t.Errorf("%s: GetAttParts = (%q, %q, %v), want (%q, %q, %v)", c.name, id, attr, ok, c.id, c.attr, c.ok)
		}
	}
	if got := GetAttLongForm("A.B"); !reflect.DeepEqual(got, []any{"A", "B"}) {
		t.Errorf("GetAttLongForm(\"A.B\") = %#v", got)
	}
}

func TestLooksLikeShortForm(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"!Ref X":            true,
		"!GetAZs":           true,
		"!Sub a${B}":        true,
		"!!str 5":           false,
		"!NotATag x":        false,
		"see !Ref A":        false,
		"!Ref X\nY":         false,
		"!Ref X\rY":         false,
		"!RefX":             false,
		"":                  false,
		"!GetStackOutput x": false,
	}
	for s, want := range cases {
		if got := LooksLikeShortForm(s); got != want {
			t.Errorf("LooksLikeShortForm(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestIsDynamicReferenceAndShortFormTag(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"{{resolve:ssm:/a/b}}":            true,
		"{{resolve:ssm-secure:/a/b:1}}":   true,
		"{{resolve:secretsmanager:id:x}}": true,
		"{{resolve:other:x}}":             false,
		"prefix {{resolve:ssm:/a}}":       false,
		"{{resolve:ssm:/a}} suffix":       false,
	} {
		if got := IsDynamicReference(s); got != want {
			t.Errorf("IsDynamicReference(%q) = %v, want %v", s, got, want)
		}
	}
	if !IsShortFormTag("Ref") || !IsShortFormTag("Transform") || IsShortFormTag("Bogus") || IsShortFormTag("!Ref") {
		t.Error("IsShortFormTag disagrees with the tag set")
	}
}
