// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func number(s string) *json.Number { n := json.Number(s); return &n }

func length(n int) *int { return &n }

func TestCarryCarriesEveryConstraintItCan(t *testing.T) {
	name := prop("Name", KindString)
	name.Enum = []json.RawMessage{json.RawMessage(`"a"`), json.RawMessage(`"b"`)}
	name.Pattern = `^[a-z]+$`
	name.MinLength, name.MaxLength = length(1), length(80)
	name.Format = "uri"
	name.Required = true
	n, ds := Carry(name, "name")
	if len(ds) != 0 {
		t.Fatalf("diagnostics %+v, want none", ds)
	}
	if n.Name != "name" || !reflect.DeepEqual(n.Type, []string{"string"}) || len(n.Enum) != 2 || n.Pattern != `^[a-z]+$` ||
		*n.MinLength != 1 || *n.MaxLength != 80 || n.Format != "uri" || !n.Required {
		t.Fatalf("carried %+v", n)
	}

	size := prop("Size", KindInteger)
	size.Min, size.Max = number("1"), number("65536")
	n, ds = Carry(size, "size")
	if len(ds) != 0 || n.Min.String() != "1" || n.Max.String() != "65536" {
		t.Fatalf("carried %+v, diagnostics %+v; want min 1 and max 65536", n, ds)
	}
}

func TestCarryDropsAPatternRE2Refuses(t *testing.T) {
	p := prop("Name", KindString)
	p.Pattern = `(?<!-)x`
	n, ds := Carry(p, "name")
	d := onlyWarning(t, ds)
	if n.Pattern != "" || d.SourceRef != "Name" || !strings.Contains(d.Message, "pattern") ||
		!strings.Contains(d.Message, "RE2") {
		t.Fatalf("carried pattern %q, warning %+v; want the pattern dropped naming the property, pattern and RE2", n.Pattern, d)
	}
}

func TestCarryDropsAFormatTheCompilerDoesNotCheck(t *testing.T) {
	for _, format := range []string{"int64", "password"} {
		t.Run(format, func(t *testing.T) {
			p := prop("Value", KindString)
			p.Format = format
			n, ds := Carry(p, "value")
			d := onlyWarning(t, ds)
			if n.Format != "" || !strings.Contains(d.Message, `format "`+format+`"`) ||
				!strings.Contains(d.Message, "date-time") {
				t.Fatalf("carried format %q, warning %+v; want it dropped naming the format and the ones checked", n.Format, d)
			}
		})
	}
}

// TestCarryOneOfIsTheUnionOfBranchTypes reads a oneOf of a string and an
// integer, lifted as the union of the two kinds with the branches' own shape
// recorded as dropped.
func TestCarryOneOfIsTheUnionOfBranchTypes(t *testing.T) {
	p := prop("Retention", KindString, KindInteger)
	p.Dropped = []string{"oneOf: a branch's own maxLength is not carried, only the branch's type"}
	n, ds := Carry(p, "retention")
	d := onlyWarning(t, ds)
	if !reflect.DeepEqual(n.Type, []string{"string", "integer"}) || !strings.Contains(d.Message, "oneOf") {
		t.Fatalf("carried type %v, warning %+v; want [string integer] and the branch shape reported", n.Type, d)
	}
}

func TestCarryDropsBoundsOnAUnion(t *testing.T) {
	p := prop("Retention", KindString, KindInteger)
	p.Min = number("1")
	n, ds := Carry(p, "retention")
	d := onlyWarning(t, ds)
	if n.Min != nil || !strings.Contains(d.Message, "minimum") || !strings.Contains(d.Message, "union") {
		t.Fatalf("carried min %v, warning %+v; want the minimum dropped naming the union", n.Min, d)
	}
}

func TestCarryCutIsAnOpenObject(t *testing.T) {
	for _, c := range []struct{ cut, why string }{{"cycle", "refer back"}, {"depth", "deeper than"}} {
		cut, why := c.cut, c.why
		t.Run(cut, func(t *testing.T) {
			p := &Property{Name: "Seventh", Path: []string{"A", "B", "C", "D", "E", "F", "Seventh"},
				Kinds: []Kind{KindObject}, Open: true, Cut: cut}
			n, ds := Carry(p, "seventh")
			d := onlyWarning(t, ds)
			if !reflect.DeepEqual(n.Type, []string{"object"}) || !n.Open || len(n.Properties) != 0 ||
				d.SourceRef != "A.B.C.D.E.F.Seventh" || !strings.Contains(d.Message, why) {
				t.Fatalf("carried %+v, warning %+v; want an open object and the cut reported", n, d)
			}
		})
	}
}

func TestCarryReportsEveryKeywordTheReaderDropped(t *testing.T) {
	p := prop("Tags", KindArray)
	p.Items = &Property{Name: "Tags", Kinds: []Kind{KindString}}
	p.Dropped = []string{
		"uniqueItems: the compact form states no uniqueness",
		"multipleOf: the compact form states no multiple",
		"const: the draft carries no fixed value",
		"patternProperties: the draft describes no member by a pattern",
	}
	_, ds := Carry(p, "tags")
	if len(ds) != len(p.Dropped) {
		t.Fatalf("diagnostics %+v, want one for each dropped keyword", ds)
	}
	for i, keyword := range []string{"uniqueItems", "multipleOf", "const", "patternProperties"} {
		if ds[i].Code != codeConstraintDropped || ds[i].SourceRef != "Tags" || !strings.Contains(ds[i].Message, keyword) {
			t.Errorf("diagnostic %d = %+v, want %s on Tags", i, ds[i], keyword)
		}
	}
}

func TestCarryNestedMembersAreSnakeCase(t *testing.T) {
	p := prop("Settings", KindObject)
	key := member(p.Path, "KmsKeyId", KindString)
	key.Required = true
	limits := member(p.Path, "Limits", KindObject)
	maxSize := member(limits.Path, "MaxSize", KindInteger)
	limits.Properties = []*Property{maxSize}
	status := member(p.Path, "Status", KindString)
	status.ReadOnly = true
	p.Properties = []*Property{key, limits, status}
	n, ds := Carry(p, "settings")
	if len(ds) != 0 || n.Open || len(n.Properties) != 2 {
		t.Fatalf("carried %+v, diagnostics %+v; want a closed object of two members", n, ds)
	}
	if got := n.Properties[0]; got.Name != "kms_key_id" || !got.Required {
		t.Errorf("first member %+v, want required kms_key_id", got)
	}
	if got := n.Properties[1]; got.Name != "limits" || len(got.Properties) != 1 || got.Properties[0].Name != "max_size" {
		t.Errorf("second member %+v, want limits holding max_size", got)
	}
}

func TestCarryOpenObjectIsPassedWhole(t *testing.T) {
	p := prop("Labels", KindObject)
	p.Open = true
	p.Properties = []*Property{member(p.Path, "Team", KindString)}
	n, ds := Carry(p, "labels")
	d := onlyWarning(t, ds)
	if !n.Open || len(n.Properties) != 0 || !strings.Contains(d.Message, "open object") {
		t.Fatalf("carried %+v, warning %+v; want an open object with no members described", n, d)
	}
}

func TestCarryMarksVendorDescriptions(t *testing.T) {
	p := prop("Settings", KindObject)
	p.Description = "The settings."
	inner := member(p.Path, "Mode", KindString)
	inner.Description = "The mode."
	p.Properties = []*Property{inner}
	n, _ := Carry(p, "settings")
	if len(n.Properties) != 1 {
		t.Fatalf("carried members %+v, want one", n.Properties)
	}
	if n.Description != VendorTextMarker+" The settings." || n.Properties[0].Description != VendorTextMarker+" The mode." {
		t.Fatalf("descriptions %q and %q, want each opening with the marker", n.Description, n.Properties[0].Description)
	}
}
