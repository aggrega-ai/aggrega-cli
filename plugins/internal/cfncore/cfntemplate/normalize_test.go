// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfntemplate

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// property normalizes a template whose one resource carries body under Properties
// and returns the decoded Properties mapping.
func property(t *testing.T, body string) map[string]any {
	t.Helper()
	doc := normalizeDoc(t, "AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n"+body)
	return doc["Resources"].(map[string]any)["X"].(map[string]any)["Properties"].(map[string]any)
}

func normalizeDoc(t *testing.T, template string) map[string]any {
	t.Helper()
	out, err := Normalize([]byte(template))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !strings.HasSuffix(string(out), "}\n") {
		t.Fatalf("output has no trailing newline: %q", out)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return doc
}

func ref(name string) map[string]any { return map[string]any{"Ref": name} }

func TestNormalizeReadsEveryShortForm(t *testing.T) {
	t.Parallel()
	eq := map[string]any{"Fn::Equals": []any{ref("Env"), "prod"}}
	cases := []struct {
		name string
		form string
		want any
	}{
		{"Ref", "!Ref Foo", ref("Foo")},
		{"GetAtt dot", "!GetAtt A.B", map[string]any{"Fn::GetAtt": "A.B"}},
		{"GetAtt two dots", "!GetAtt A.B.C", map[string]any{"Fn::GetAtt": "A.B.C"}},
		{"GetAtt list", "!GetAtt [A, B]", map[string]any{"Fn::GetAtt": []any{"A", "B"}}},
		{"Sub string", "!Sub 'arn:${AWS::Region}:${Foo}:${A.B}'", map[string]any{"Fn::Sub": "arn:${AWS::Region}:${Foo}:${A.B}"}},
		{"Sub list", "!Sub ['${N}-x', {N: !Ref Foo}]", map[string]any{"Fn::Sub": []any{"${N}-x", map[string]any{"N": ref("Foo")}}}},
		{"Join", "!Join [',', [a, !Ref Foo]]", map[string]any{"Fn::Join": []any{",", []any{"a", ref("Foo")}}}},
		{"Select", "!Select [0, !GetAZs '']", map[string]any{"Fn::Select": []any{float64(0), map[string]any{"Fn::GetAZs": ""}}}},
		{"Split", "!Split [',', 'a,b']", map[string]any{"Fn::Split": []any{",", "a,b"}}},
		{"FindInMap", "!FindInMap [M, K1, K2]", map[string]any{"Fn::FindInMap": []any{"M", "K1", "K2"}}},
		{"GetAZs", "!GetAZs ''", map[string]any{"Fn::GetAZs": ""}},
		{"Base64", "!Base64 'hello'", map[string]any{"Fn::Base64": "hello"}},
		{"Cidr", "!Cidr [!GetAtt V.CidrBlock, 6, 5]", map[string]any{"Fn::Cidr": []any{map[string]any{"Fn::GetAtt": "V.CidrBlock"}, float64(6), float64(5)}}},
		{"ImportValue", "!ImportValue shared-vpc", map[string]any{"Fn::ImportValue": "shared-vpc"}},
		{"If nested", "!If [C1, !If [C2, a, b], !Ref 'AWS::NoValue']", map[string]any{"Fn::If": []any{"C1", map[string]any{"Fn::If": []any{"C2", "a", "b"}}, ref("AWS::NoValue")}}},
		{"Equals", "!Equals [!Ref Env, 'prod']", eq},
		{"And with Condition", "!And [!Equals [!Ref Env, 'prod'], !Condition C1]", map[string]any{"Fn::And": []any{eq, map[string]any{"Condition": "C1"}}}},
		{"Or with Condition", "!Or [!Equals [!Ref Env, 'prod'], !Condition C1]", map[string]any{"Fn::Or": []any{eq, map[string]any{"Condition": "C1"}}}},
		{"Not", "!Not [!Equals [!Ref Env, 'prod']]", map[string]any{"Fn::Not": []any{eq}}},
		{"Condition", "!Condition C1", map[string]any{"Condition": "C1"}},
		{"Transform", "!Transform {Name: 'AWS::Include', Parameters: {Location: 's3://bucket/snippet.yaml'}}",
			map[string]any{"Fn::Transform": map[string]any{"Name": "AWS::Include", "Parameters": map[string]any{"Location": "s3://bucket/snippet.yaml"}}}},
		{"GetStackOutput", "!GetStackOutput {StackName: ProducerStack, OutputName: VpcId}",
			map[string]any{"Fn::GetStackOutput": map[string]any{"StackName": "ProducerStack", "OutputName": "VpcId"}}},
		{"unknown tag", "!Bogus x", "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := property(t, "      P: "+c.form+"\n")["P"]
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("P = %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestNormalizeKeepsAQuotedShortFormAString(t *testing.T) {
	t.Parallel()
	if got := property(t, "      P: \"!Ref X\"\n")["P"]; got != "!Ref X" {
		t.Errorf("P = %#v, want the string \"!Ref X\"", got)
	}
}

func TestNormalizeRefusesRecursiveAnchorAndDuplicateKey(t *testing.T) {
	t.Parallel()
	_, err := Normalize([]byte("Resources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n      A: &loop\n        Inner: *loop\n"))
	if err == nil || !strings.Contains(err.Error(), "anchor &loop is aliased by *loop inside its own content") {
		t.Errorf("recursive anchor: err = %v, want the anchor named", err)
	}
	_, err = Normalize([]byte("Resources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n      BucketName: one\n      BucketName: two\n"))
	if err == nil || !strings.Contains(err.Error(), `mapping key "BucketName" already defined`) {
		t.Errorf("duplicate key: err = %v, want the key named", err)
	}
}

func TestNormalizeRefusesExcessiveAliasingWithoutExpandingIt(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("Resources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n      l0: &l0 [!Ref A, !Ref A, !Ref A, !Ref A, !Ref A, !Ref A, !Ref A, !Ref A, !Ref A]\n")
	for i := 1; i <= 9; i++ {
		prev := "*l" + string(rune('0'+i-1))
		b.WriteString("      l" + string(rune('0'+i)) + ": &l" + string(rune('0'+i)) + " [" + strings.TrimSuffix(strings.Repeat(prev+",", 9), ",") + "]\n")
	}
	_, err := Normalize([]byte(b.String()))
	if err == nil || !strings.Contains(err.Error(), "excessive aliasing") {
		t.Errorf("err = %v, want the reader's excessive aliasing refusal", err)
	}
}

func TestNormalizeKeepsAnUnquotedDateAsWritten(t *testing.T) {
	t.Parallel()
	doc := normalizeDoc(t, "AWSTemplateFormatVersion: 2010-09-09\nResources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n      V: 2012-10-17\n")
	if got := doc["AWSTemplateFormatVersion"]; got != "2010-09-09" {
		t.Errorf("AWSTemplateFormatVersion = %#v", got)
	}
	if got := doc["Resources"].(map[string]any)["X"].(map[string]any)["Properties"].(map[string]any)["V"]; got != "2012-10-17" {
		t.Errorf("V = %#v", got)
	}
}

func TestNormalizeReadsJSONAndPassesItThrough(t *testing.T) {
	t.Parallel()
	out, err := Normalize([]byte(`{"Resources":{"X":{"Type":"AWS::S3::Bucket","Properties":{"P":{"Ref":"Foo"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"Resources\": {\n    \"X\": {\n      \"Properties\": {\n        \"P\": {\n          \"Ref\": \"Foo\"\n        }\n      },\n      \"Type\": \"AWS::S3::Bucket\"\n    }\n  }\n}\n"
	if string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestNormalizeOfUnparseableBytesReturnsTheReadersError(t *testing.T) {
	t.Parallel()
	_, err := Normalize([]byte("a: [unclosed\n"))
	want := "invalid YAML template: yaml: line 1: did not find expected ',' or ']'"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestAliasToAShortFormReadsAsTheIntrinsic(t *testing.T) {
	t.Parallel()
	props := property(t, "      A: &x !Ref Foo\n      B: *x\n")
	if !reflect.DeepEqual(props["A"], ref("Foo")) || !reflect.DeepEqual(props["B"], ref("Foo")) {
		t.Errorf("A = %#v, B = %#v, want both {Ref: Foo}", props["A"], props["B"])
	}
}

// A container holding a !Condition is expanded where it is anchored, so an alias to
// it reads the same; the old reader read the Condition as the bare name there.
func TestAliasToAContainerHoldingAShortFormReadsAsTheIntrinsic(t *testing.T) {
	t.Parallel()
	props := property(t, "      A: &x\n        Name: !Sub '${Foo}-n'\n        Cond: !Condition C\n      B: *x\n      C: [*x, 1]\n")
	want := map[string]any{"Name": map[string]any{"Fn::Sub": "${Foo}-n"}, "Cond": map[string]any{"Condition": "C"}}
	for _, k := range []string{"A", "B"} {
		if !reflect.DeepEqual(props[k], want) {
			t.Errorf("%s = %#v, want %#v", k, props[k], want)
		}
	}
	if got := props["C"].([]any)[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("C[0] = %#v, want %#v", got, want)
	}
}

// An anchor written twice is read at each alias as the definition before it, also
// inside a short form that is copied out in full.
func TestRedefinedAnchorReadsAsTheDefinitionBeforeEachAlias(t *testing.T) {
	t.Parallel()
	props := property(t, `      a: &a plain
      b: &s !Join [",", [*a, x]]
      a2: &a other
      c: *s
      d: [*a, *s]
`)
	joinPlain := map[string]any{"Fn::Join": []any{",", []any{"plain", "x"}}}
	if !reflect.DeepEqual(props["b"], joinPlain) || !reflect.DeepEqual(props["c"], joinPlain) {
		t.Errorf("b = %#v, c = %#v, want both %#v", props["b"], props["c"], joinPlain)
	}
	if want := []any{"other", joinPlain}; !reflect.DeepEqual(props["d"], want) {
		t.Errorf("d = %#v, want %#v", props["d"], want)
	}
}

// An aliased short form that expands past the copy bound is refused, naming the
// anchor, rather than read without its tag.
func TestNormalizeRefusesAnAliasedShortFormTooLargeToCopy(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("Resources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n      s: &s !Join [\",\", [a, b]]\n")
	for i := range maxCopiedNodes/3 + 1 {
		b.WriteString("      k" + strconv.Itoa(i) + ": *s\n")
	}
	_, err := Normalize([]byte(b.String()))
	want := "invalid YAML template: the short form anchored as &s, aliased by *s, expands to more than 100000 nodes; write the repeated content out in full or alias a smaller one"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestAliasToAnExpandedLongFormTagReadsAsTheIntrinsic(t *testing.T) {
	t.Parallel()
	props := property(t, "      A: &c !Condition C\n      B: *c\n")
	want := map[string]any{"Condition": "C"}
	if !reflect.DeepEqual(props["A"], want) || !reflect.DeepEqual(props["B"], want) {
		t.Errorf("A = %#v, B = %#v, want both %#v", props["A"], props["B"], want)
	}
}

func TestAliasToAPlainValueIsLeftAlone(t *testing.T) {
	t.Parallel()
	props := property(t, "      A: &x plain\n      B: *x\n")
	if props["A"] != "plain" || props["B"] != "plain" {
		t.Errorf("A = %#v, B = %#v", props["A"], props["B"])
	}
	merged := property(t, "      M: &base\n        A: 1\n      P:\n        <<: *base\n        B: 2\n")
	if want := map[string]any{"A": float64(1), "B": float64(2)}; !reflect.DeepEqual(merged["P"], want) {
		t.Errorf("P = %#v, want %#v", merged["P"], want)
	}
}

func TestConditionAndTransformShortFormsExpand(t *testing.T) {
	t.Parallel()
	doc := normalizeDoc(t, `Conditions:
  Both: !And
    - !Condition C1
    - !Not [!Condition C2]
Resources:
  X:
    Type: AWS::S3::Bucket
    Condition: !Condition C1
    Properties:
      T: !Transform
        Name: AWS::Include
        Parameters:
          Location: s3://bucket/snippet.yaml
      S: !GetStackOutput
        StackName: ProducerStack
        OutputName: VpcId
`)
	wantBoth := map[string]any{"Fn::And": []any{
		map[string]any{"Condition": "C1"},
		map[string]any{"Fn::Not": []any{map[string]any{"Condition": "C2"}}},
	}}
	if got := doc["Conditions"].(map[string]any)["Both"]; !reflect.DeepEqual(got, wantBoth) {
		t.Errorf("Both = %#v, want %#v", got, wantBoth)
	}
	x := doc["Resources"].(map[string]any)["X"].(map[string]any)
	if got := x["Condition"]; !reflect.DeepEqual(got, map[string]any{"Condition": "C1"}) {
		t.Errorf("Condition = %#v", got)
	}
	props := x["Properties"].(map[string]any)
	wantT := map[string]any{"Fn::Transform": map[string]any{"Name": "AWS::Include", "Parameters": map[string]any{"Location": "s3://bucket/snippet.yaml"}}}
	if !reflect.DeepEqual(props["T"], wantT) {
		t.Errorf("T = %#v, want %#v", props["T"], wantT)
	}
	wantS := map[string]any{"Fn::GetStackOutput": map[string]any{"StackName": "ProducerStack", "OutputName": "VpcId"}}
	if !reflect.DeepEqual(props["S"], wantS) {
		t.Errorf("S = %#v, want %#v", props["S"], wantS)
	}
}

func TestNormalizeIsDeterministic(t *testing.T) {
	t.Parallel()
	src := []byte("Resources:\n  X:\n    Type: AWS::S3::Bucket\n    Properties:\n      Z: !Condition C\n      A: &x !Ref Foo\n      B: *x\n")
	first, err := Normalize(src)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := Normalize(src)
		if err != nil || string(again) != string(first) {
			t.Fatalf("second reading differs: %v\n%s\n%s", err, first, again)
		}
	}
}

func TestNormalizeReadsAShortFormOverAMapping(t *testing.T) {
	t.Parallel()
	sub := map[string]any{"Fn::Sub": "echo ${AWS::Region}"}
	azs := map[string]any{"Fn::GetAZs": ref("AWS::Region")}
	cases := []struct {
		name string
		body string
		want any
	}{
		{"flow Base64", "      P: !Base64 {'Fn::Sub': 'echo ${AWS::Region}'}\n", map[string]any{"Fn::Base64": sub}},
		{"block Base64", "      P: !Base64\n        Fn::Sub: 'echo ${AWS::Region}'\n", map[string]any{"Fn::Base64": sub}},
		{"GetAZs", "      P: !GetAZs {Ref: AWS::Region}\n", azs},
		{"Select", "      P: !Select [0, !GetAZs {Ref: AWS::Region}]\n", map[string]any{"Fn::Select": []any{float64(0), azs}}},
		{"Join", "      P: !Join ['', [!Base64 {'Fn::Sub': x}]]\n", map[string]any{"Fn::Join": []any{"", []any{map[string]any{"Fn::Base64": map[string]any{"Fn::Sub": "x"}}}}}},
		{"Ref over a mapping", "      P: !Ref {K: v}\n", map[string]any{"Ref": map[string]any{"K": "v"}}},
	}
	for _, c := range cases {
		if got := property(t, c.body)["P"]; !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: P = %#v, want %#v", c.name, got, c.want)
		}
	}
}
