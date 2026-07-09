// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// fragmentBucket is a sourced-style fragment: a bucket resource plus a provides
// entry exposing the bucket's ARN through a short-form !GetAtt intrinsic.
const fragmentBucket = `
Resources:
  DataBucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: my-data-bucket
provides:
  bucket_arn: !GetAtt DataBucket.Arn
`

// fragmentQueue is a second fragment whose provides entry references the first
// fragment's resource by !Ref — an intra-stack cross-reference that must resolve
// to a long-form intrinsic in the assembled Outputs.
const fragmentQueue = `
Resources:
  WorkQueue:
    Type: AWS::SQS::Queue
provides:
  bucket_ref: !Ref DataBucket
  queue_url: !GetAtt WorkQueue.QueueUrl
`

// TestAssembleStackMergeAndOutputs assembles two fragments — one carrying a
// short-form !GetAtt provides value, one carrying a !Ref intra-stack reference —
// and asserts the merged stack template carries both resources, projects every
// provides entry into a long-form Outputs entry, and validates as well-formed
// JSON. It prints the assembled bytes so the long-form output is inspectable.
func TestAssembleStackMergeAndOutputs(t *testing.T) {
	nodes := []NodeFragment{
		{NodeID: "data/bucket", Files: [][]byte{[]byte(fragmentBucket)}},
		{NodeID: "work/queue", Files: [][]byte{[]byte(fragmentQueue)}},
	}

	out, err := AssembleStack(nodes)
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}

	t.Logf("assembled stack template:\n%s", out)

	// Well-formed JSON.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}

	// Envelope.
	var fmtVer string
	if err := json.Unmarshal(top["AWSTemplateFormatVersion"], &fmtVer); err != nil || fmtVer != "2010-09-09" {
		t.Fatalf("AWSTemplateFormatVersion = %q (err %v), want 2010-09-09", fmtVer, err)
	}

	// Both resources present.
	var resources map[string]json.RawMessage
	if err := json.Unmarshal(top["Resources"], &resources); err != nil {
		t.Fatalf("Resources not an object: %v", err)
	}
	for _, id := range []string{"DataBucket", "WorkQueue"} {
		if _, ok := resources[id]; !ok {
			t.Fatalf("assembled Resources missing %q; got keys %v", id, keysOf(resources))
		}
	}

	// Every provides entry became an Output.
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(top["Outputs"], &outputs); err != nil {
		t.Fatalf("Outputs not an object: %v", err)
	}
	for _, k := range []string{"bucket_arn", "bucket_ref", "queue_url"} {
		if _, ok := outputs[k]; !ok {
			t.Fatalf("Outputs missing %q; got keys %v", k, keysOf(outputs))
		}
	}

	// Long-form intrinsics only — no short-form tags survive.
	if bytes.Contains(out, []byte("!GetAtt")) || bytes.Contains(out, []byte("!Ref")) {
		t.Fatalf("assembled output carries a short-form intrinsic tag:\n%s", out)
	}
	// The !GetAtt provides value resolved to a long-form Fn::GetAtt array, and
	// the !Ref value resolved to a long-form Ref.
	if !strings.Contains(string(out), `"Fn::GetAtt"`) {
		t.Fatalf("expected a long-form Fn::GetAtt in the assembled output:\n%s", out)
	}
	if !strings.Contains(string(out), `"Ref": "DataBucket"`) {
		t.Fatalf("expected the intra-stack !Ref to resolve to a long-form Ref:\n%s", out)
	}
}

// TestAssembleStackDeterministic asserts the assembly is byte-identical across
// repeated runs of identical input (Deterministic Output invariant).
func TestAssembleStackDeterministic(t *testing.T) {
	nodes := []NodeFragment{
		{NodeID: "data/bucket", Files: [][]byte{[]byte(fragmentBucket)}},
		{NodeID: "work/queue", Files: [][]byte{[]byte(fragmentQueue)}},
	}
	first, err := AssembleStack(nodes)
	if err != nil {
		t.Fatalf("AssembleStack first: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := AssembleStack(nodes)
		if err != nil {
			t.Fatalf("AssembleStack run %d: %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("assembly not byte-identical on run %d:\nfirst:\n%s\nagain:\n%s", i, first, again)
		}
	}
}

// TestAssembleStackLogicalIDCollision asserts two fragments contributing the same
// logical id do not clobber each other: the second is disambiguated by a
// node-id-derived prefix.
func TestAssembleStackLogicalIDCollision(t *testing.T) {
	frag := []byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\n")
	nodes := []NodeFragment{
		{NodeID: "alpha", Files: [][]byte{frag}},
		{NodeID: "beta", Files: [][]byte{frag}},
	}
	out, err := AssembleStack(nodes)
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var resources map[string]json.RawMessage
	if err := json.Unmarshal(top["Resources"], &resources); err != nil {
		t.Fatalf("Resources not an object: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 distinct resources after collision disambiguation, got %d: %v", len(resources), keysOf(resources))
	}
	if _, ok := resources["Thing"]; !ok {
		t.Fatalf("first contributor's logical id Thing should survive; got %v", keysOf(resources))
	}
	if _, ok := resources["betaThing"]; !ok {
		t.Fatalf("colliding logical id should be disambiguated to betaThing; got %v", keysOf(resources))
	}
}

// TestAssembleStackEmptyResourcesHalts asserts an assembly that produces no
// resources halts loudly rather than emitting an empty stack.
func TestAssembleStackEmptyResourcesHalts(t *testing.T) {
	nodes := []NodeFragment{
		{NodeID: "outputs-only", Files: [][]byte{[]byte("provides:\n  x: literal\n")}},
	}
	if _, err := AssembleStack(nodes); err == nil {
		t.Fatal("expected a loud halt for an assembly with no Resources; got nil error")
	}
}

// TestNormalizeFragmentLongForm asserts render_node's fragment normalization
// expands short-form intrinsics to long form and emits deterministic JSON.
func TestNormalizeFragmentLongForm(t *testing.T) {
	got, err := NormalizeFragment([]byte(fragmentBucket))
	if err != nil {
		t.Fatalf("NormalizeFragment: %v", err)
	}
	if bytes.Contains(got, []byte("!GetAtt")) {
		t.Fatalf("normalized fragment still carries short-form tag:\n%s", got)
	}
	if !bytes.Contains(got, []byte(`"Fn::GetAtt":["DataBucket","Arn"]`)) {
		t.Fatalf("normalized fragment missing expanded long-form Fn::GetAtt:\n%s", got)
	}
}

// sourcedTemplate is a sourced-style CloudFormation stack template carrying every
// short-form intrinsic spelling the normalizer must expand to long form: a !Ref in a
// resource property, a !GetAtt and a !Sub in the Outputs, and a nested !Join.
const sourcedTemplate = `
AWSTemplateFormatVersion: "2010-09-09"
Resources:
  DataBucket:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Ref BucketNameParam
  WorkQueue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Join ["-", [!Ref BucketNameParam, "queue"]]
Outputs:
  BucketArn:
    Value: !GetAtt DataBucket.Arn
  Endpoint:
    Value: !Sub "https://${DataBucket}.example.com"
`

// TestNormalizeSourcedTemplateLongForm normalizes a sourced CloudFormation YAML
// template and asserts every short-form intrinsic expanded to its long-form object
// spelling with no short-form tag surviving. It prints the resulting long-form JSON so
// the normalized output is inspectable.
func TestNormalizeSourcedTemplateLongForm(t *testing.T) {
	out, err := NormalizeSourcedTemplate([]byte(sourcedTemplate))
	if err != nil {
		t.Fatalf("NormalizeSourcedTemplate: %v", err)
	}

	t.Logf("normalized sourced template:\n%s", out)

	// Well-formed JSON.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("normalized template is not valid JSON: %v", err)
	}

	// No short-form intrinsic tag survives.
	for _, tag := range []string{"!Ref", "!GetAtt", "!Sub", "!Join"} {
		if bytes.Contains(out, []byte(tag)) {
			t.Fatalf("normalized output still carries short-form tag %q:\n%s", tag, out)
		}
	}
	// Long-form spellings present; intrinsics are not evaluated — the Ref and GetAtt
	// pointers carry through untouched rather than being resolved to values.
	for _, want := range []string{
		`"Ref": "BucketNameParam"`,
		`"Fn::GetAtt": "DataBucket.Arn"`,
		`"Fn::Sub": "https://${DataBucket}.example.com"`,
		`"Fn::Join"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("normalized output missing long-form %s:\n%s", want, out)
		}
	}
}

// TestNormalizeSourcedTemplateDeterministic asserts sourced-template normalization is
// byte-identical across repeated runs of identical input (Deterministic Output
// invariant).
func TestNormalizeSourcedTemplateDeterministic(t *testing.T) {
	first, err := NormalizeSourcedTemplate([]byte(sourcedTemplate))
	if err != nil {
		t.Fatalf("NormalizeSourcedTemplate first: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := NormalizeSourcedTemplate([]byte(sourcedTemplate))
		if err != nil {
			t.Fatalf("NormalizeSourcedTemplate run %d: %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("normalization not byte-identical on run %d:\nfirst:\n%s\nagain:\n%s", i, first, again)
		}
	}
}

// TestValidateSourcedMalformedHalts asserts a malformed sourced template produces one
// error-severity diagnostic naming the offending file, so the build halts loudly
// rather than proceeding to a later render failure.
func TestValidateSourcedMalformedHalts(t *testing.T) {
	files := []SourcedFile{
		{Path: "broken.yaml", Content: []byte("Resources: {\n  this is : not valid : yaml\n")},
	}
	diags := ValidateSourced(files, false)
	if len(diags) != 1 {
		t.Fatalf("expected exactly one diagnostic for a malformed template, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity != "error" {
		t.Fatalf("diagnostic severity = %q, want error", diags[0].Severity)
	}
	if diags[0].Code != "cloudformation_template_parse" {
		t.Fatalf("diagnostic code = %q, want cloudformation_template_parse", diags[0].Code)
	}
	if !strings.Contains(diags[0].Message, "broken.yaml") {
		t.Fatalf("diagnostic message does not name the offending file: %q", diags[0].Message)
	}
	if diags[0].Location == nil || diags[0].Location.URI != "broken.yaml" {
		t.Fatalf("diagnostic location does not pin the offending file: %+v", diags[0].Location)
	}
}

// TestValidateSourcedCleanTemplate asserts a well-formed sourced template yields no
// diagnostics (an empty, valid check).
func TestValidateSourcedCleanTemplate(t *testing.T) {
	files := []SourcedFile{{Path: "stack.yaml", Content: []byte(sourcedTemplate)}}
	if diags := ValidateSourced(files, false); len(diags) != 0 {
		t.Fatalf("expected no diagnostics for a clean template, got %d: %+v", len(diags), diags)
	}
}

// TestNormalizeSourcedDirectoryVendorsNonTemplate asserts a directory source
// normalizes its CloudFormation template files while vendoring any non-template file
// verbatim, preserving the packed order and paths.
func TestNormalizeSourcedDirectoryVendorsNonTemplate(t *testing.T) {
	readme := []byte("# stack\nThis vendors a bucket. !Ref is prose here, not an intrinsic.\n")
	files := []SourcedFile{
		{Path: "template.yaml", Content: []byte(sourcedTemplate)},
		{Path: "README.md", Content: readme},
	}
	out, err := NormalizeSourced(files, true)
	if err != nil {
		t.Fatalf("NormalizeSourced: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 files back, got %d", len(out))
	}
	if out[0].Path != "template.yaml" || out[1].Path != "README.md" {
		t.Fatalf("packed order/paths not preserved: %q, %q", out[0].Path, out[1].Path)
	}
	// The template normalized to long-form JSON.
	if bytes.Contains(out[0].Content, []byte("!Ref")) {
		t.Fatalf("template file was not normalized:\n%s", out[0].Content)
	}
	// The README vendored verbatim, untouched.
	if !bytes.Equal(out[1].Content, readme) {
		t.Fatalf("non-template file was not vendored verbatim:\ngot:\n%s\nwant:\n%s", out[1].Content, readme)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
