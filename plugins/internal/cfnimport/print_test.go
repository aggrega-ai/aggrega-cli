// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// longForm decodes the JSON text s as the lowering decodes a long form: its
// numbers kept as written.
func longForm(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return v
}

// TestShortFormQuotesOnlyWhereYamlNeedsIt prints each long form as one line of
// short form: a string plain where YAML reads it back as that text, and
// single-quoted otherwise, a single quote doubled; a function directly under
// another written in its long form, as YAML gives a node one tag; the members
// of a mapping in key order.
func TestShortFormQuotesOnlyWhereYamlNeedsIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ long, want string }{
		{`{"Ref": "AWS::Region"}`, `!Ref AWS::Region`},
		{`{"Fn::ImportValue": "shared-vpc"}`, `!ImportValue shared-vpc`},
		{`{"Fn::Select": [0, {"Fn::GetAZs": ""}]}`, `!Select [0, !GetAZs '']`},
		{`{"Fn::GetAtt": ["Db", "Endpoint.Address"]}`, `!GetAtt [Db, Endpoint.Address]`},
		{`{"Fn::GetAtt": "Db.Endpoint.Address"}`, `!GetAtt Db.Endpoint.Address`},
		{`{"Fn::Sub": "${AWS::StackName}-queue"}`, `!Sub '${AWS::StackName}-queue'`},
		{`{"Fn::Sub": ["${Host}-x", {"Host": {"Ref": "Env"}, "M": "it's"}]}`,
			`!Sub ['${Host}-x', {Host: !Ref Env, M: 'it''s'}]`},
		{`{"Fn::Base64": {"Fn::Sub": "echo ${AWS::Region}"}}`, `!Base64 {Fn::Sub: 'echo ${AWS::Region}'}`},
		{`{"Fn::Join": [":", ["true", "1", "a b", "x:", "y", "No", true, 1.5, null]]}`,
			`!Join [':', ['true', '1', 'a b', 'x:', 'y', 'No', true, 1.5, null]]`},
		{`{"Fn::If": ["C", {"Fn::Length": [1]}, {"Ref": "AWS::NoValue"}]}`,
			`!If [C, {Fn::Length: [1]}, !Ref AWS::NoValue]`},
		{`{"Fn::Cidr": ["10.0.0.0/16", 6, 5]}`, `!Cidr ['10.0.0.0/16', 6, 5]`},
	} {
		got, ok := shortForm(longForm(t, c.long))
		if !ok || got != c.want {
			t.Errorf("shortForm(%s) = %q, %v; want %q", c.long, got, ok, c.want)
		}
	}
}

// TestShortFormRefusesWhatOneLineCannotHold refuses a long form with no one
// line of short form a Recipe's placement reads back: a string holding a double
// quote, a backslash, a line break or another control character, or that is
// itself wholly a short form; an object that is no intrinsic function; a
// function with no short form, or one taking a mapping; and a scalar under a
// tag that is no text, which the tag would read as text.
func TestShortFormRefusesWhatOneLineCannotHold(t *testing.T) {
	t.Parallel()
	for _, long := range []string{
		`{"Fn::Sub": "say \"hi\""}`,
		`{"Fn::Sub": "a\\b"}`,
		`{"Fn::Sub": "a\nb"}`,
		`{"Fn::Sub": "a\tb"}`,
		`{"Fn::Select": [0, ["!Ref Db"]]}`,
		`{"Name": "x"}`,
		`"plain"`,
		`{"Fn::GetStackOutput": {"StackName": "shared", "OutputName": "VpcId"}}`,
		`{"Fn::Transform": {"Name": "AWS::Include", "Parameters": {"Location": "s3://b/k"}}}`,
		`{"Fn::Length": [1, 2]}`,
		`{"Fn::Base64": 5}`,
		`{"Ref": "A", "Fn::Sub": "b"}`,
	} {
		if got, ok := shortForm(longForm(t, long)); ok {
			t.Errorf("shortForm(%s) = %q, want none", long, got)
		}
	}
}

// allDeployTime returns every deploy-time text the lowering tests draw.
func allDeployTime() []deployTime {
	return slices.Concat(pseudoCases, importValueCases, functionCases, placedCases, dynamicCases, ssmParameterCases,
		joinCases, mappingCases, owedCases)
}

// canonical returns the long form v with each Fn::GetAtt written in its list
// form, as the fragment reader writes a dotted one, so a long form and the one
// read back compare by meaning.
func canonical(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			if k == "Fn::GetAtt" {
				item = cfntemplate.GetAttLongForm(item)
			}
			out[k] = canonical(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = canonical(item)
		}
		return out
	}
	return v
}

// readBack returns the value of the property P a Recipe's fragment holding the
// line text as placed reads back: written bare, inside a double-quoted scalar
// as postgres-rds writes it, or through toJson.
func readBack(t *testing.T, placed string) any {
	t.Helper()
	fragment := "Resources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n      P: " + placed + "\n"
	out, err := cfncore.NormalizeFragment([]byte(fragment))
	if err != nil {
		t.Fatalf("the fragment holding %s does not read: %v", placed, err)
	}
	var doc struct {
		Resources map[string]struct{ Properties map[string]any }
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Resources["X"].Properties["P"]
}

// TestShortFormRoundTrips places the text of each deploy-time value the
// lowering tests draw as a catalog Recipe places a value, and reads the
// fragment back as the provider reads it: bare, inside a double-quoted scalar
// and through toJson, it reads as the long form the text writes. A dynamic
// reference is text, not a short form, and a Recipe places text quoted or
// through toJson. A value whose one line would need a double quote, a
// backslash or a line break is not carried, naming its property.
func TestShortFormRoundTrips(t *testing.T) {
	t.Parallel()
	for _, c := range allDeployTime() {
		t.Run(c.value, func(t *testing.T) {
			t.Parallel()
			p, _, _ := lowerP(t, c.value)
			o, ok := p.Value.(cloudimport.Opaque)
			if !ok {
				t.Fatalf("%s lowers to %#v, want deploy-time text", c.value, p.Value)
			}
			var want any
			if err := json.Unmarshal([]byte(c.long), &want); err != nil {
				t.Fatal(err)
			}
			want = canonical(want)
			quoted, err := json.Marshal(o.Text)
			if err != nil {
				t.Fatal(err)
			}
			placements := []string{`"` + o.Text + `"`, string(quoted)}
			if cfntemplate.LooksLikeShortForm(o.Text) {
				placements = append(placements, o.Text)
			}
			for _, placed := range placements {
				if got := canonical(readBack(t, placed)); !reflect.DeepEqual(got, want) {
					t.Errorf("placed as %s, it reads back %#v, want %#v", placed, got, want)
				}
			}
		})
	}

	for name, value := range map[string]string{
		"a double quote": `!Join ['', ['say "hi"', !Ref AWS::Region]]`,
		"a backslash":    `!Join ['', ['C:\temp', !Ref AWS::Region]]`,
		"a line break":   `!Join ['', ["one\ntwo", !Ref AWS::Region]]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, present, diags := lowerP(t, value)
			if !present || p.Value != nil || len(p.Props) > 0 || len(p.Items) > 0 {
				t.Errorf("%s lowers to %+v, present %v; want the property with no value", value, p, present)
			}
			onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
				Message: "the template template.yaml writes at " + atP + " an intrinsic function holding a double " +
					"quote, a backslash, a line break or another control character, or text that is wholly a short " +
					"form, which its one-line short form cannot hold where a catalog Recipe places it, so it is not " +
					"carried: write the value by hand",
				SourceRef: "template.yaml#" + atP})
		})
	}
}

// TestShortFormIsOneLine checks every deploy-time text the lowering tests draw
// is one line holding no double quote and no backslash, so a Recipe's
// double-quoted placement holds it as written.
func TestShortFormIsOneLine(t *testing.T) {
	t.Parallel()
	for _, c := range allDeployTime() {
		if strings.ContainsAny(c.want.Text, "\"\\\r\n") {
			t.Errorf("%s writes %q, which a double-quoted placement does not hold as written", c.value, c.want.Text)
		}
	}
}
