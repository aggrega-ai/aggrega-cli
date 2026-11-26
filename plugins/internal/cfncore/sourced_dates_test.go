// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"encoding/json"
	"reflect"
	"testing"
)

// sourcedJSON normalizes one sourced template and decodes the resulting JSON.
func sourcedJSON(t *testing.T, template string) map[string]any {
	t.Helper()
	out, err := NormalizeSourcedTemplate([]byte(template))
	if err != nil {
		t.Fatalf("NormalizeSourcedTemplate: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return doc
}

// policyDocument returns the PolicyDocument mapping of the template the dates tests share.
func policyDocument(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	res := doc["Resources"].(map[string]any)["X"].(map[string]any)
	props := res["Properties"].(map[string]any)
	return props["P"].(map[string]any)["PolicyDocument"].(map[string]any)
}

// TestSourcedTemplateKeepsAnUnquotedDateAsWritten holds a plain date, which a YAML
// reader resolves as a timestamp, to the text the template wrote.
func TestSourcedTemplateKeepsAnUnquotedDateAsWritten(t *testing.T) {
	t.Parallel()
	doc := sourcedJSON(t, `AWSTemplateFormatVersion: 2010-09-09
Resources:
  X:
    Type: AWS::S3::BucketPolicy
    Properties:
      P:
        PolicyDocument:
          Version: 2012-10-17
`)
	if got := doc["AWSTemplateFormatVersion"]; got != "2010-09-09" {
		t.Errorf("AWSTemplateFormatVersion = %#v, want \"2010-09-09\"", got)
	}
	if got := policyDocument(t, doc)["Version"]; got != "2012-10-17" {
		t.Errorf("Version = %#v, want \"2012-10-17\"", got)
	}
}

// TestSourcedTemplateKeepsAnUnquotedDateTimeAsWritten holds a plain date-time to its
// source text, including the lower-case separator and the trailing zero.
func TestSourcedTemplateKeepsAnUnquotedDateTimeAsWritten(t *testing.T) {
	t.Parallel()
	doc := sourcedJSON(t, `Resources:
  X:
    Type: AWS::S3::BucketPolicy
    Properties:
      P:
        PolicyDocument:
          DateTime: 2001-12-14t21:59:43.10-05:00
`)
	if got := policyDocument(t, doc)["DateTime"]; got != "2001-12-14t21:59:43.10-05:00" {
		t.Errorf("DateTime = %#v, want \"2001-12-14t21:59:43.10-05:00\"", got)
	}
}

// TestSourcedTemplateKeepsAQuotedDateAsWritten holds a quoted date, already a string,
// to the same text.
func TestSourcedTemplateKeepsAQuotedDateAsWritten(t *testing.T) {
	t.Parallel()
	doc := sourcedJSON(t, `Resources:
  X:
    Type: AWS::S3::BucketPolicy
    Properties:
      P:
        PolicyDocument:
          Quoted: '2012-10-17'
          Double: "2012-10-17"
`)
	pd := policyDocument(t, doc)
	if got := pd["Quoted"]; got != "2012-10-17" {
		t.Errorf("Quoted = %#v, want \"2012-10-17\"", got)
	}
	if got := pd["Double"]; got != "2012-10-17" {
		t.Errorf("Double = %#v, want \"2012-10-17\"", got)
	}
}

// TestSourcedTemplateWithADateKeepsItsShortForms reads a directory source whose
// template holds an unquoted date beside short forms: the date is kept, each short
// form reaches the long form it reaches without a date, a non-template file is
// vendored verbatim, and the validator reports nothing.
func TestSourcedTemplateWithADateKeepsItsShortForms(t *testing.T) {
	t.Parallel()
	const body = `Resources:
  A:
    Type: AWS::S3::Bucket
  B:
    Type: AWS::SNS::Topic
    Properties:
      Ref1: !Ref A
      Sub1: !Sub "${A}-x"
      Att1: !GetAtt A.B
`
	withDate := []SourcedFile{
		{Path: "stack.yaml", Content: []byte("AWSTemplateFormatVersion: 2010-09-09\n" + body)},
		{Path: "README.md", Content: []byte("notes: 2010-09-09\n")},
	}
	got, err := NormalizeSourced(withDate, true)
	if err != nil {
		t.Fatalf("NormalizeSourced: %v", err)
	}
	if len(got) != 2 || got[0].Path != "stack.yaml" || got[1].Path != "README.md" {
		t.Fatalf("files = %#v", got)
	}
	if string(got[1].Content) != "notes: 2010-09-09\n" {
		t.Errorf("non-template file changed: %q", got[1].Content)
	}
	var doc map[string]any
	if err := json.Unmarshal(got[0].Content, &doc); err != nil {
		t.Fatalf("template output is not JSON: %v", err)
	}
	if doc["AWSTemplateFormatVersion"] != "2010-09-09" {
		t.Errorf("AWSTemplateFormatVersion = %#v", doc["AWSTemplateFormatVersion"])
	}

	without, err := NormalizeSourced([]SourcedFile{{Path: "stack.yaml", Content: []byte(body)}}, true)
	if err != nil {
		t.Fatalf("NormalizeSourced without a date: %v", err)
	}
	var base map[string]any
	if err := json.Unmarshal(without[0].Content, &base); err != nil {
		t.Fatal(err)
	}
	delete(doc, "AWSTemplateFormatVersion")
	if !reflect.DeepEqual(doc, base) {
		t.Errorf("short forms differ with a date:\nwith:    %#v\nwithout: %#v", doc, base)
	}
	props := doc["Resources"].(map[string]any)["B"].(map[string]any)["Properties"].(map[string]any)
	want := map[string]any{
		"Ref1": map[string]any{"Ref": "A"},
		"Sub1": map[string]any{"Fn::Sub": "${A}-x"},
		"Att1": map[string]any{"Fn::GetAtt": "A.B"},
	}
	if !reflect.DeepEqual(props, want) {
		t.Errorf("properties = %#v, want %#v", props, want)
	}

	if diags := ValidateSourced(withDate, true); len(diags) != 0 {
		t.Errorf("ValidateSourced = %#v, want none", diags)
	}
}
