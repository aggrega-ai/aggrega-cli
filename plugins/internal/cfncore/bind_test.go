// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// bindTemplate is a sourced template referencing its parameters in every section a
// reference to one is evaluated in, and naming them as Fn::Sub variables in both
// forms.
const bindTemplate = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  BucketName:
    Type: String
  Retention:
    Type: Number
    Default: 30
  Stage:
    Type: String
    AllowedValues: [dev, prod]
  Zones:
    Type: CommaDelimitedList
  Kept:
    Type: String
    Default: unchanged
Conditions:
  IsProd:
    Fn::Equals: [!Ref Stage, prod]
Rules:
  StageIsKnown:
    Assertions:
      - Assert:
          Fn::Contains: [[dev, prod], !Ref Stage]
Resources:
  Bucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Ref BucketName
      Tags:
        - Key: label
          Value: !Sub "${BucketName}-${Stage}-${Kept}"
        - Key: mapped
          Value: !Sub
            - "${BucketName}-${Own}"
            - BucketName: authored
              Own: !Ref Kept
      LifecycleConfiguration:
        Rules:
          - ExpirationInDays: !Ref Retention
            Status: Enabled
  Subnets:
    Type: AWS::EC2::SubnetGroupLike
    Properties:
      Zones: !Ref Zones
Outputs:
  Name:
    Value: !Ref BucketName
`

// bindNodeID is the node every binding here runs for. A halt prints it as its
// source location, so it shares no word with any value a row binds, and the values
// a halting row binds are tokens no refusal sentence contains: a row asserting that
// its halt quotes no value then fails only when the value itself is quoted.
const bindNodeID = "bind-node-under-test"

// bindSource normalizes one template body into the single-file source a sourced
// render hands the binding.
func bindSource(t *testing.T, body string) []SourcedFile {
	t.Helper()
	files, err := NormalizeSourced([]SourcedFile{{Path: "template.yaml", Content: []byte(body)}}, false)
	if err != nil {
		t.Fatalf("NormalizeSourced: %v", err)
	}
	return files
}

// mappedValues builds the node values a values mapping produced, in key order.
func mappedValues(kv ...any) types.OrderedMap[any] {
	var m types.OrderedMap[any]
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i].(string), kv[i+1])
	}
	return m
}

// boundDoc decodes the one template a binding returned.
func boundDoc(t *testing.T, files []SourcedFile) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(files[0].Content, &doc); err != nil {
		t.Fatalf("bound template is not JSON: %v\n%s", err, files[0].Content)
	}
	return doc
}

// at reads one value of a decoded template by its path of keys and list positions.
func at(t *testing.T, doc map[string]any, path ...any) any {
	t.Helper()
	var v any = doc
	for _, seg := range path {
		switch s := seg.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("at %v: %q is not under a mapping", path, s)
			}
			v = m[s]
		case int:
			l, ok := v.([]any)
			if !ok || s >= len(l) {
				t.Fatalf("at %v: position %d is not in a list", path, s)
			}
			v = l[s]
		}
	}
	return v
}

// TestBindSourced is the binding, row by row: each row binds a set of mapped values
// into one template and states what the bound template carries, or which condition
// the binding halts under and what its sentence names.
func TestBindSourced(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		body  string
		input BindInput
		// check reads the bound template; nil for a row that halts.
		check func(t *testing.T, doc map[string]any)
		// wantErr is the condition a halting row reports under, and wantText the
		// words its sentence must carry.
		wantErr  error
		wantText []string
		// wantAbsent are words a halting sentence must not carry: the values, or a
		// name the halt must not pick.
		wantAbsent []string
	}{
		{
			name:  "a Ref in Resources, Outputs, Conditions and Rules is replaced and the parameter leaves",
			body:  bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("BucketName", "store", "Stage", "prod")},
			check: func(t *testing.T, doc map[string]any) {
				if got := at(t, doc, "Resources", "Bucket", "Properties", "BucketName"); got != "store" {
					t.Errorf("Resources: BucketName = %#v, want the bound literal", got)
				}
				if got := at(t, doc, "Outputs", "Name", "Value"); got != "store" {
					t.Errorf("Outputs: Value = %#v, want the bound literal", got)
				}
				if got := at(t, doc, "Conditions", "IsProd", "Fn::Equals", 0); got != "prod" {
					t.Errorf("Conditions: first operand = %#v, want the bound literal", got)
				}
				if got := at(t, doc, "Rules", "StageIsKnown", "Assertions", 0, "Assert", "Fn::Contains", 1); got != "prod" {
					t.Errorf("Rules: operand = %#v, want the bound literal", got)
				}
				params, _ := doc["Parameters"].(map[string]any)
				for _, gone := range []string{"BucketName", "Stage"} {
					if _, present := params[gone]; present {
						t.Errorf("bound parameter %s is still declared", gone)
					}
				}
				if _, present := params["Kept"]; !present {
					t.Errorf("unbound parameter Kept is no longer declared")
				}
			},
		},
		{
			name:  "Fn::Sub in the string form receives the value through a variable map",
			body:  bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("BucketName", "store", "Stage", "dev")},
			check: func(t *testing.T, doc map[string]any) {
				got := at(t, doc, "Resources", "Bucket", "Properties", "Tags", 0, "Value", "Fn::Sub")
				want := []any{"${BucketName}-${Stage}-${Kept}", map[string]any{"BucketName": "store", "Stage": "dev"}}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("string-form Fn::Sub = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:  "Fn::Sub in the list form keeps a variable its map already names",
			body:  bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("BucketName", "store")},
			check: func(t *testing.T, doc map[string]any) {
				got := at(t, doc, "Resources", "Bucket", "Properties", "Tags", 1, "Value", "Fn::Sub")
				want := []any{"${BucketName}-${Own}", map[string]any{"BucketName": "authored", "Own": map[string]any{"Ref": "Kept"}}}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("list-form Fn::Sub = %#v, want the map's own value kept: %#v", got, want)
				}
			},
		},
		{
			name:  "a Number is converted to the string a Ref to it yields",
			body:  bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("Retention", 7)},
			check: func(t *testing.T, doc map[string]any) {
				if got := at(t, doc, "Resources", "Bucket", "Properties", "LifecycleConfiguration", "Rules", 0, "ExpirationInDays"); got != "7" {
					t.Errorf("ExpirationInDays = %#v, want the string \"7\"", got)
				}
			},
		},
		{
			name:  "a list-typed parameter takes a comma-separated string as its trimmed members",
			body:  bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("Zones", "eu-west-1a, eu-west-1b")},
			check: func(t *testing.T, doc map[string]any) {
				got := at(t, doc, "Resources", "Subnets", "Properties", "Zones")
				if want := []any{"eu-west-1a", "eu-west-1b"}; !reflect.DeepEqual(got, want) {
					t.Errorf("Zones = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:  "a value that is wholly one short-form intrinsic is read as that intrinsic",
			body:  bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("BucketName", "!GetAtt Namer.Name")},
			check: func(t *testing.T, doc map[string]any) {
				got := at(t, doc, "Resources", "Bucket", "Properties", "BucketName")
				if want := map[string]any{"Fn::GetAtt": []any{"Namer", "Name"}}; !reflect.DeepEqual(got, want) {
					t.Errorf("BucketName = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:  "a long-form intrinsic value reaches the reference site as it is",
			body:  bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("BucketName", map[string]any{"Fn::ImportValue": "shared-name"})},
			check: func(t *testing.T, doc map[string]any) {
				got := at(t, doc, "Resources", "Bucket", "Properties", "BucketName")
				if want := map[string]any{"Fn::ImportValue": "shared-name"}; !reflect.DeepEqual(got, want) {
					t.Errorf("BucketName = %#v, want %#v", got, want)
				}
			},
		},
		{
			name:       "a key naming no parameter halts naming the key and what the template declares",
			body:       bindTemplate,
			input:      BindInput{Mapped: true, Values: mappedValues("BuckName", "zq-unknown-literal")},
			wantErr:    wasm.ErrPluginRequiredInputUnresolved(),
			wantText:   []string{`"BuckName"`, `template "template.yaml" declares`, `"BucketName"`, `"Zones"`},
			wantAbsent: []string{"zq-unknown-literal"},
		},
		{
			name:       "a literal outside the parameter's AllowedValues halts",
			body:       bindTemplate,
			input:      BindInput{Mapped: true, Values: mappedValues("Stage", "staging")},
			wantErr:    wasm.ErrPluginValueNotRepresentable(),
			wantText:   []string{`"Stage"`, "AllowedValues"},
			wantAbsent: []string{"staging"},
		},
		{
			name:       "a NoEcho parameter refuses a literal",
			body:       "Parameters:\n  Token:\n    Type: String\n    NoEcho: true\nResources:\n  Topic:\n    Type: AWS::SNS::Topic\n    Properties:\n      DisplayName: !Ref Token\n",
			input:      BindInput{Mapped: true, Values: mappedValues("Token", "s3cr3t")},
			wantErr:    wasm.ErrPluginValueNotRepresentable(),
			wantText:   []string{`"Token"`, "NoEcho", "dynamic reference"},
			wantAbsent: []string{"s3cr3t"},
		},
		{
			name:  "a NoEcho parameter admits a dynamic reference",
			body:  "Parameters:\n  Token:\n    Type: String\n    NoEcho: true\nResources:\n  Topic:\n    Type: AWS::SNS::Topic\n    Properties:\n      DisplayName: !Ref Token\n",
			input: BindInput{Mapped: true, Values: mappedValues("Token", "{{resolve:secretsmanager:app-token}}")},
			check: func(t *testing.T, doc map[string]any) {
				if got := at(t, doc, "Resources", "Topic", "Properties", "DisplayName"); got != "{{resolve:secretsmanager:app-token}}" {
					t.Errorf("DisplayName = %#v, want the dynamic reference", got)
				}
			},
		},
		{
			name: "a value the payload marks sensitive is admitted only as a dynamic reference",
			body: bindTemplate,
			input: BindInput{Mapped: true, Values: mappedValues("BucketName", "zq-sensitive-literal"),
				SensitiveKeys: map[string]string{"BucketName": "values.bucket"}},
			wantErr:    wasm.ErrPluginValueNotRepresentable(),
			wantText:   []string{`"BucketName"`, "values.bucket", "dynamic reference"},
			wantAbsent: []string{"zq-sensitive-literal"},
		},
		{
			// Kept rather than Stage is the second parameter: Stage admits only values
			// short enough to occur inside the refusal's own words, and a value outside
			// its AllowedValues would halt on the constraint first.
			name:       "with the whole values marked, every literal binding is refused by name",
			body:       bindTemplate,
			input:      BindInput{Mapped: true, SensitiveValues: true, Values: mappedValues("BucketName", "zq-bucket-literal", "Kept", "zq-kept-literal")},
			wantErr:    wasm.ErrPluginValueNotRepresentable(),
			wantText:   []string{`"BucketName"`, `"Kept"`},
			wantAbsent: []string{"zq-bucket-literal", "zq-kept-literal"},
		},
		{
			name:       "a dynamic reference is refused where its parameter is referenced in Outputs",
			body:       bindTemplate,
			input:      BindInput{Mapped: true, Values: mappedValues("BucketName", "{{resolve:ssm:/zq-dynref/path}}")},
			wantErr:    wasm.ErrPluginValueNotRepresentable(),
			wantText:   []string{`"BucketName"`, "Outputs"},
			wantAbsent: []string{"/zq-dynref/path"},
		},
		{
			name:     "a rule reading a bound parameter through Fn::ValueOf halts",
			body:     "Parameters:\n  Vpc:\n    Type: AWS::EC2::VPC::Id\nRules:\n  Tagged:\n    Assertions:\n      - Assert:\n          Fn::EachMemberEquals: [{Fn::ValueOf: [Vpc, Tags.Team]}, platform]\nResources:\n  Group:\n    Type: AWS::EC2::SecurityGroup\n    Properties:\n      VpcId: !Ref Vpc\n",
			input:    BindInput{Mapped: true, Values: mappedValues("Vpc", "vpc-0abc")},
			wantErr:  wasm.ErrPluginCannotExpressInput(),
			wantText: []string{`"Tagged"`, `"Vpc"`, "Fn::ValueOf"},
		},
		{
			// The rule's two keys hold one bound parameter each, so a walk following the
			// map's own iteration order would name either; the halt names the one under
			// the lexically first key, which is also the first parameter by name.
			name:       "a rule reading two bound parameters through Fn::ValueOf halts naming the first in sorted order",
			body:       "Parameters:\n  Alpha:\n    Type: AWS::EC2::VPC::Id\n  Zeta:\n    Type: AWS::EC2::VPC::Id\nRules:\n  Tagged:\n    RuleCondition:\n      Fn::Equals: [{Fn::ValueOf: [Zeta, Tags.Env]}, prod]\n    Assertions:\n      - Assert:\n          Fn::EachMemberEquals: [{Fn::ValueOf: [Alpha, Tags.Team]}, platform]\nResources:\n  Group:\n    Type: AWS::EC2::SecurityGroup\n    Properties:\n      VpcId: !Ref Alpha\n      Description: !Ref Zeta\n",
			input:      BindInput{Mapped: true, Values: mappedValues("Zeta", "vpc-0zeta", "Alpha", "vpc-0alpha")},
			wantErr:    wasm.ErrPluginCannotExpressInput(),
			wantText:   []string{`"Tagged"`, `parameter "Alpha"`, "Fn::ValueOf"},
			wantAbsent: []string{`"Zeta"`},
		},
		{
			name:       "a list bound to a parameter named as an Fn::Sub variable halts",
			body:       "Parameters:\n  Zones:\n    Type: CommaDelimitedList\nResources:\n  Topic:\n    Type: AWS::SNS::Topic\n    Properties:\n      DisplayName: !Sub \"${Zones}\"\n",
			input:      BindInput{Mapped: true, Values: mappedValues("Zones", []any{"a", "b"})},
			wantErr:    wasm.ErrPluginValueNotRepresentable(),
			wantText:   []string{`"Zones"`, "Fn::Sub"},
			wantAbsent: []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := bindSource(t, tc.body)
			out, err := BindSourced(files, false, tc.input, bindNodeID)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("binding succeeded; want a halt under %v", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("binding halted under %v; want %v", err, tc.wantErr)
				}
				for _, want := range tc.wantText {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the halt does not name %s: %v", want, err)
					}
				}
				for _, value := range tc.wantAbsent {
					if strings.Contains(err.Error(), value) {
						t.Errorf("the halt carries %q, which it must not: %v", value, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("BindSourced: %v", err)
			}
			tc.check(t, boundDoc(t, out))
		})
	}
}

// TestBindSourcedLeavesUnmappedValuesUnread is the byte-identity leg: values no
// values mapping produced — a Component's own values, written for the Recipe's
// artifacts — are left unread, and the templates come back exactly as handed in,
// whether or not a key happens to name a declared parameter.
func TestBindSourcedLeavesUnmappedValuesUnread(t *testing.T) {
	t.Parallel()
	files := bindSource(t, bindTemplate)
	for _, tc := range []struct {
		name  string
		input BindInput
	}{
		{"values a mapping did not produce", BindInput{Mapped: false, Values: mappedValues("BucketName", "store", "retention_days", "45")}},
		{"a mapping that produced nothing", BindInput{Mapped: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := BindSourced(files, false, tc.input, bindNodeID)
			if err != nil {
				t.Fatalf("BindSourced: %v", err)
			}
			if !bytes.Equal(out[0].Content, files[0].Content) {
				t.Errorf("the template changed:\n--- got ---\n%s\n--- want ---\n%s", out[0].Content, files[0].Content)
			}
		})
	}
}
