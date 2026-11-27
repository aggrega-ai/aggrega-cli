// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"reflect"
	"testing"
)

// TestSourcedTemplateConditionShortFormReachesTheStack holds a sourced template's
// !Condition, !Transform and !GetStackOutput short forms, and an alias to a short
// form, to the long-form intrinsics CloudFormation reads them as.
func TestSourcedTemplateConditionShortFormReachesTheStack(t *testing.T) {
	t.Parallel()
	doc := sourcedJSON(t, `Conditions:
  Both: !And [!Condition C1, !Condition C2]
Resources:
  X:
    Type: AWS::S3::Bucket
    Condition: !Condition C1
    Properties:
      Name: &n !Ref Foo
      Same: *n
      T: !Transform {Name: AWS::Include}
      S: !GetStackOutput {StackName: Producer, OutputName: VpcId}
`)
	cond := func(name string) map[string]any { return map[string]any{"Condition": name} }
	if got, want := doc["Conditions"].(map[string]any)["Both"], any(map[string]any{"Fn::And": []any{cond("C1"), cond("C2")}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Both = %#v, want %#v", got, want)
	}
	x := doc["Resources"].(map[string]any)["X"].(map[string]any)
	if got := x["Condition"]; !reflect.DeepEqual(got, any(cond("C1"))) {
		t.Errorf("Condition = %#v", got)
	}
	props := x["Properties"].(map[string]any)
	want := map[string]any{
		"Name": map[string]any{"Ref": "Foo"},
		"Same": map[string]any{"Ref": "Foo"},
		"T":    map[string]any{"Fn::Transform": map[string]any{"Name": "AWS::Include"}},
		"S":    map[string]any{"Fn::GetStackOutput": map[string]any{"StackName": "Producer", "OutputName": "VpcId"}},
	}
	if !reflect.DeepEqual(props, want) {
		t.Errorf("Properties = %#v, want %#v", props, want)
	}
}

// TestSourcedTemplateUserDataBase64OverSubMappingReachesTheStack holds a short-form
// function written over a block mapping to the function over that mapping.
func TestSourcedTemplateUserDataBase64OverSubMappingReachesTheStack(t *testing.T) {
	t.Parallel()
	doc := sourcedJSON(t, `Resources:
  X:
    Type: AWS::EC2::Instance
    Properties:
      UserData: !Base64
        Fn::Sub: 'echo ${AWS::Region}'
`)
	props := doc["Resources"].(map[string]any)["X"].(map[string]any)["Properties"].(map[string]any)
	want := map[string]any{"Fn::Base64": map[string]any{"Fn::Sub": "echo ${AWS::Region}"}}
	if got := props["UserData"]; !reflect.DeepEqual(got, any(want)) {
		t.Errorf("UserData = %#v, want %#v", got, want)
	}
}
