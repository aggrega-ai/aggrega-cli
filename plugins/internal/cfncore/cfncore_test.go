// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
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

	// Every provides entry became an Output, named for the contributing node and
	// the entry together so one node's entries cannot displace another's.
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(top["Outputs"], &outputs); err != nil {
		t.Fatalf("Outputs not an object: %v", err)
	}
	for _, k := range []string{"DataBucketBucketArn", "WorkQueueBucketRef", "WorkQueueQueueUrl"} {
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

// TestDeclaredNamesReportsTheResourceIDsTwoNodesShare asserts that two nodes
// authoring one logical id each declare that same id, which is what the host
// compares to refuse the pair. The declaration is the whole mechanism here: this
// core reads the ids a Recipe authored and never rewrites one, so a name shared
// by two nodes has to leave the guest as a claim on that name rather than as a
// merged template quietly carrying one resource where two were declared.
func TestDeclaredNamesReportsTheResourceIDsTwoNodesShare(t *testing.T) {
	frag := []byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\n")

	alphaRes, _, err := DeclaredNames("alpha", [][]byte{frag})
	if err != nil {
		t.Fatalf("DeclaredNames(alpha): %v", err)
	}
	betaRes, _, err := DeclaredNames("beta", [][]byte{frag})
	if err != nil {
		t.Fatalf("DeclaredNames(beta): %v", err)
	}
	if len(alphaRes) != 1 || alphaRes[0] != "Thing" {
		t.Fatalf("alpha declared %v, want the authored id [Thing] verbatim", alphaRes)
	}
	if len(betaRes) != 1 || betaRes[0] != "Thing" {
		t.Fatalf("beta declared %v, want the authored id [Thing] verbatim", betaRes)
	}
}

// TestDeclaredNamesScopesOutputsPerNode asserts that two nodes projecting one
// entry name declare two distinct output ids, so the entry each node exports
// reaches an output of its own. This is the half the declaration does not have to
// refuse, because the projection has already kept the two apart.
func TestDeclaredNamesScopesOutputsPerNode(t *testing.T) {
	frag := []byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nprovides:\n  host: literal\n")

	_, alphaOut, err := DeclaredNames("alpha", [][]byte{frag})
	if err != nil {
		t.Fatalf("DeclaredNames(alpha): %v", err)
	}
	_, betaOut, err := DeclaredNames("beta-two", [][]byte{frag})
	if err != nil {
		t.Fatalf("DeclaredNames(beta-two): %v", err)
	}
	if len(alphaOut) != 1 || alphaOut[0] != "AlphaHost" {
		t.Fatalf("alpha declared outputs %v, want [AlphaHost]", alphaOut)
	}
	if len(betaOut) != 1 || betaOut[0] != "BetaTwoHost" {
		t.Fatalf("beta-two declared outputs %v, want [BetaTwoHost]", betaOut)
	}
}

// TestOutputLogicalID covers the shape of a projected output's name across the
// separators an instance name and an entry name can carry, including the case
// two names differing only in where their separators sit stay distinct.
func TestOutputLogicalID(t *testing.T) {
	for _, tc := range []struct {
		name  string
		node  string
		entry string
		want  string
	}{
		{name: "plain", node: "db", entry: "host", want: "DbHost"},
		{name: "hyphen in node", node: "payments-db", entry: "host", want: "PaymentsDbHost"},
		{name: "underscore in node", node: "payments_db", entry: "host", want: "PaymentsDbHost"},
		{name: "dot in node", node: "shop.payments", entry: "port", want: "ShopPaymentsPort"},
		{name: "underscore in entry", node: "db", entry: "queue_url", want: "DbQueueUrl"},
		{name: "digits kept", node: "db2-primary", entry: "host", want: "Db2PrimaryHost"},
		{name: "authored capitals kept", node: "orders-DB", entry: "host", want: "OrdersDBHost"},
		{name: "separator position is significant", node: "payments.d.b", entry: "host", want: "PaymentsDBHost"},
		{name: "empty entry", node: "db", entry: "", want: "Db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := OutputLogicalID(tc.node, tc.entry); got != tc.want {
				t.Fatalf("OutputLogicalID(%q, %q) = %q, want %q", tc.node, tc.entry, got, tc.want)
			}
		})
	}

	// Every produced name is a legal logical id: letters and digits only.
	for _, node := range []string{"payments-db", "shop.payments", "a_b-c.d", "db2"} {
		got := OutputLogicalID(node, "queue_url")
		for _, r := range got {
			isAlnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !isAlnum {
				t.Fatalf("OutputLogicalID(%q, %q) = %q, which carries %q — a logical id admits letters and digits only", node, "queue_url", got, r)
			}
		}
	}
}

// TestAssembleStackScopesAnExplicitOutputsBlock asserts an explicit Outputs
// block is scoped by its contributing node exactly as a projected provides entry
// is, so a fragment that publishes its own outputs cannot displace another
// fragment's. A sourced template is the shape that carries one.
func TestAssembleStackScopesAnExplicitOutputsBlock(t *testing.T) {
	frag := []byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nOutputs:\n  BucketArn:\n    Value: literal\n")
	other := []byte("Resources:\n  OtherThing:\n    Type: AWS::SQS::Queue\nOutputs:\n  BucketArn:\n    Value: other\n")
	nodes := []NodeFragment{
		{NodeID: "alpha", Files: [][]byte{frag}},
		{NodeID: "beta", Files: [][]byte{other}},
	}
	out, err := AssembleStack(nodes)
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	t.Logf("assembled stack template:\n%s", out)

	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(top["Outputs"], &outputs); err != nil {
		t.Fatalf("Outputs not an object: %v", err)
	}
	for _, want := range []string{"AlphaBucketArn", "BetaBucketArn"} {
		if _, ok := outputs[want]; !ok {
			t.Fatalf("Outputs missing %q; got keys %v", want, keysOf(outputs))
		}
	}
	if _, ok := outputs["BucketArn"]; ok {
		t.Fatalf("an explicit output landed unscoped under %q, where a second fragment's entry of the same name would displace it", "BucketArn")
	}
}

// TestDeclaredNamesSourced covers the sourced path's declaration: a directory
// source's template files contribute their names and its vendored content does
// not, and a single-file source's lone template is read whole.
func TestDeclaredNamesSourced(t *testing.T) {
	template := []byte("Resources:\n  DataBucket:\n    Type: AWS::S3::Bucket\nOutputs:\n  BucketArn:\n    Value: literal\n")
	readme := []byte("this file is prose, not a template\n")

	dirRes, dirOut, err := DeclaredNamesSourced("api", []SourcedFile{
		{Path: "stack.yaml", Content: template},
		{Path: "README.md", Content: readme},
	}, true)
	if err != nil {
		t.Fatalf("DeclaredNamesSourced(directory): %v", err)
	}
	if len(dirRes) != 1 || dirRes[0] != "DataBucket" {
		t.Fatalf("directory source declared resources %v, want [DataBucket]", dirRes)
	}
	if len(dirOut) != 1 || dirOut[0] != "ApiBucketArn" {
		t.Fatalf("directory source declared outputs %v, want [ApiBucketArn]", dirOut)
	}

	fileRes, fileOut, err := DeclaredNamesSourced("api", []SourcedFile{
		{Path: "whatever", Content: template},
	}, false)
	if err != nil {
		t.Fatalf("DeclaredNamesSourced(single file): %v", err)
	}
	if len(fileRes) != 1 || fileRes[0] != "DataBucket" {
		t.Fatalf("single-file source declared resources %v, want [DataBucket]", fileRes)
	}
	if len(fileOut) != 1 || fileOut[0] != "ApiBucketArn" {
		t.Fatalf("single-file source declared outputs %v, want [ApiBucketArn]", fileOut)
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
// halt-severity diagnostic naming the offending file, so the build halts loudly
// rather than proceeding to a later render failure.
func TestValidateSourcedMalformedHalts(t *testing.T) {
	files := []SourcedFile{
		{Path: "broken.yaml", Content: []byte("Resources: {\n  this is : not valid : yaml\n")},
	}
	diags := ValidateSourced(files, false)
	if len(diags) != 1 {
		t.Fatalf("expected exactly one diagnostic for a malformed template, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity != "halt" {
		t.Fatalf("diagnostic severity = %q, want halt", diags[0].Severity)
	}
	// Asserted as a literal on purpose. Comparing against templateParseCode would
	// pass for any value that constant happened to hold, which is exactly the
	// change this assertion exists to catch.
	if diags[0].Code != "cloudformation-template-parse" {
		t.Fatalf("diagnostic code = %q, want cloudformation-template-parse", diags[0].Code)
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

// TestAssembleStackRefusesANameAlreadyCarryingDifferentContent covers the merge's
// refusal across both key spaces and both doors into the outputs space. Each row
// is one contributor claiming one name twice with content that differs, which a
// map cannot hold: whichever write ran second would displace the first, and the
// displaced content would leave the stack with nothing said about it.
//
// One contributor is what every row uses, deliberately. A name two contributors
// claim is refused before assembly runs — the guest declares each fragment's
// names and the host halts on the duplicate — so the case that actually reaches
// this merge is the one a per-contributor declaration cannot see.
//
// Every row also asserts the halt is NOT the Output-name collision, and that is
// what keeps the rows covering their subject. Each of them restates ONE entry name
// with two bodies, which the collision check admits by design — it answers whether
// two DIFFERENT authored entries collapsed into one name, and one entry restated is
// not that. A row that only checked something failed would keep passing if the
// earlier check ever began rejecting it, at which point it would have stopped
// exercising the merge at all.
func TestAssembleStackRefusesANameAlreadyCarryingDifferentContent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     [][]byte
		wantSpace string
		wantName  string
	}{
		{
			name: "two fragments author one resource logical id",
			files: [][]byte{
				[]byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\n"),
				[]byte("Resources:\n  Thing:\n    Type: AWS::SQS::Queue\n"),
			},
			wantSpace: KeySpaceResourceLogicalIDs,
			wantName:  "Thing",
		},
		{
			name: "two fragments author one explicit output name",
			files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: first\n"),
				[]byte("Resources:\n  B:\n    Type: AWS::SQS::Queue\nOutputs:\n  host:\n    Value: second\n"),
			},
			wantSpace: KeySpaceOutputLogicalIDs,
			wantName:  "ApiHost",
		},
		{
			name: "two fragments project one provides key",
			files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nprovides:\n  host: first\n"),
				[]byte("Resources:\n  B:\n    Type: AWS::SQS::Queue\nprovides:\n  host: second\n"),
			},
			wantSpace: KeySpaceOutputLogicalIDs,
			wantName:  "ApiHost",
		},
		{
			// One fragment reaches the outputs space through both doors: an
			// explicit Outputs entry and a provides key of the same name project
			// to one output id, so the projection overwrites the explicit entry.
			name: "one fragment's explicit output and provides key project to one name",
			files: [][]byte{
				[]byte("Resources:\n  A:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: explicit\n    Description: the explicit one\nprovides:\n  host: projected\n"),
			},
			wantSpace: KeySpaceOutputLogicalIDs,
			wantName:  "ApiHost",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AssembleStack([]NodeFragment{{NodeID: "api", Files: tc.files}})
			if err == nil {
				t.Fatal("expected a loud halt for a name carrying two different bodies; got nil error — a declared entry would have been displaced silently")
			}
			if errors.Is(err, ErrOutputNameCollision()) {
				t.Fatalf("the merge's refusal was preempted by the Output-name collision check, which this row does not exercise: %v", err)
			}
			msg := err.Error()
			t.Logf("halt: %s", msg)
			for _, want := range []string{tc.wantSpace, tc.wantName, `"api"`} {
				if !strings.Contains(msg, want) {
					t.Errorf("halt does not name %q: %s", want, msg)
				}
			}
		})
	}
}

// TestAssembleStackKeepsANameRestatedWithIdenticalContent asserts the refusal is
// scoped to the case that actually loses something. A name restated with the
// identical body displaces nothing — the space already holds exactly that value —
// so it proceeds, and the entry is present once.
//
// The provides row is the counterpart of the explicit-output row in the refusal
// table above: an explicit Outputs entry whose whole body is a Value, and a
// provides key of that name, project to one id carrying one identical body. It
// is the same collision, and it is the half that is not lossy.
func TestAssembleStackKeepsANameRestatedWithIdenticalContent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files [][]byte
	}{
		{
			name: "two fragments restate one resource identically",
			files: [][]byte{
				[]byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\n    Properties:\n      BucketName: shared\n"),
				[]byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\n    Properties:\n      BucketName: shared\n"),
			},
		},
		{
			name: "one fragment's explicit output and provides key agree",
			files: [][]byte{
				[]byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: same\nprovides:\n  host: same\n"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := AssembleStack([]NodeFragment{{NodeID: "api", Files: tc.files}})
			if err != nil {
				t.Fatalf("a restatement with identical content loses nothing and must proceed: %v", err)
			}
			t.Logf("assembled stack template:\n%s", out)

			var top map[string]json.RawMessage
			if err := json.Unmarshal(out, &top); err != nil {
				t.Fatalf("assembled stack is not valid JSON: %v", err)
			}
			var resources map[string]json.RawMessage
			if err := json.Unmarshal(top["Resources"], &resources); err != nil {
				t.Fatalf("Resources not an object: %v", err)
			}
			if _, ok := resources["Thing"]; !ok {
				t.Fatalf("the restated resource is absent; got keys %v", keysOf(resources))
			}
		})
	}
}

// TestAssembleStackMergesEveryDistinctNameFromEveryFragment is the guard on the
// legitimate case the refusal must not reach. Several fragments per node is the
// design — a dispatch group folds every per-node fragment into one template — so
// collecting many distinct names from many fragments has to stay untouched:
// every id lands, and nothing is refused.
//
// The count is asserted as a set difference rather than as a total, because two
// sides short by the same entry agree on a count and still lost it.
func TestAssembleStackMergesEveryDistinctNameFromEveryFragment(t *testing.T) {
	nodes := []NodeFragment{
		{NodeID: "orders", Files: [][]byte{
			[]byte("Resources:\n  OrdersDb:\n    Type: AWS::RDS::DBInstance\nprovides:\n  host: orders-host\n"),
			[]byte("Resources:\n  OrdersQueue:\n    Type: AWS::SQS::Queue\nprovides:\n  queue_url: orders-queue\n"),
			[]byte("Resources:\n  OrdersBucket:\n    Type: AWS::S3::Bucket\nOutputs:\n  bucket_arn:\n    Value: orders-arn\n"),
		}},
		{NodeID: "payments-db", Files: [][]byte{
			[]byte("Resources:\n  PaymentsDb:\n    Type: AWS::RDS::DBInstance\nprovides:\n  host: payments-host\n"),
			[]byte("Resources:\n  PaymentsTopic:\n    Type: AWS::SNS::Topic\nprovides:\n  queue_url: payments-topic\n"),
		}},
	}

	out, err := AssembleStack(nodes)
	if err != nil {
		t.Fatalf("merging distinct names from many fragments must not be refused: %v", err)
	}
	t.Logf("assembled stack template:\n%s", out)

	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}
	var resources, outputs map[string]json.RawMessage
	if err := json.Unmarshal(top["Resources"], &resources); err != nil {
		t.Fatalf("Resources not an object: %v", err)
	}
	if err := json.Unmarshal(top["Outputs"], &outputs); err != nil {
		t.Fatalf("Outputs not an object: %v", err)
	}

	for _, want := range []string{"OrdersDb", "OrdersQueue", "OrdersBucket", "PaymentsDb", "PaymentsTopic"} {
		if _, ok := resources[want]; !ok {
			t.Errorf("resource %q is missing from the assembled stack; got %v", want, keysOf(resources))
		}
	}
	for _, want := range []string{
		"OrdersHost", "OrdersQueueUrl", "OrdersBucketArn",
		"PaymentsDbHost", "PaymentsDbQueueUrl",
	} {
		if _, ok := outputs[want]; !ok {
			t.Errorf("output %q is missing from the assembled stack; got %v", want, keysOf(outputs))
		}
	}
}

// TestDeclaredNamesAreTheNamesTheMergeWrites computes both derivations and
// compares them, which is what a shared extraction buys: the names the guest
// hands the host are the keys the assembled stack ends up carrying, so the host
// cannot be checking one derivation for duplicates while the merge writes
// another.
//
// Asserted by computing both sides rather than by asserting a literal list: a
// literal would agree with whatever the two sites happened to produce, including
// the case where they had drifted together.
func TestDeclaredNamesAreTheNamesTheMergeWrites(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fragment string
	}{
		{name: "resources and provides", fragment: fragmentBucket},
		{name: "resources and an intra-stack reference", fragment: fragmentQueue},
		{
			name:     "resources, explicit outputs and provides together",
			fragment: "Resources:\n  Thing:\n    Type: AWS::S3::Bucket\n  Other:\n    Type: AWS::SQS::Queue\nOutputs:\n  bucket_arn:\n    Value: literal\nprovides:\n  host: h\n  port: 5432\n",
		},
		{
			name:     "outputs only, no resources",
			fragment: "Outputs:\n  bucket_arn:\n    Value: literal\n",
		},
		{
			// Both doors into the outputs space claim one name with one body.
			// The stack carries that name once, so the declaration reports it
			// once — the row that fails if the two derivations count a
			// restatement differently.
			name:     "an explicit output and a provides key that agree",
			fragment: "Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: same\nprovides:\n  host: same\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const nodeID = "payments-db"

			normalized, err := NormalizeFragment([]byte(tc.fragment))
			if err != nil {
				t.Fatalf("NormalizeFragment: %v", err)
			}
			declaredRes, declaredOut, err := DeclaredNames(nodeID, [][]byte{normalized})
			if err != nil {
				t.Fatalf("DeclaredNames: %v", err)
			}

			// A stack needs at least one resource to assemble, so a
			// resource-free fragment is paired with a carrier that shares none
			// of its names.
			files := [][]byte{normalized}
			nodes := []NodeFragment{{NodeID: nodeID, Files: files}}
			if len(declaredRes) == 0 {
				nodes = append(nodes, NodeFragment{
					NodeID: "carrier",
					Files:  [][]byte{[]byte("Resources:\n  CarrierOnly:\n    Type: AWS::S3::Bucket\n")},
				})
			}

			out, err := AssembleStack(nodes)
			if err != nil {
				t.Fatalf("AssembleStack: %v", err)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal(out, &top); err != nil {
				t.Fatalf("assembled stack is not valid JSON: %v", err)
			}
			var resources, outputs map[string]json.RawMessage
			if err := json.Unmarshal(top["Resources"], &resources); err != nil {
				t.Fatalf("Resources not an object: %v", err)
			}
			if raw, ok := top["Outputs"]; ok {
				if err := json.Unmarshal(raw, &outputs); err != nil {
					t.Fatalf("Outputs not an object: %v", err)
				}
			}
			delete(resources, "CarrierOnly")

			if got, want := sortedCopy(keysOf(resources)), sortedCopy(declaredRes); !reflect.DeepEqual(got, want) {
				t.Errorf("the stack carries resources %v; the guest declared %v", got, want)
			}
			if got, want := sortedCopy(keysOf(outputs)), sortedCopy(declaredOut); !reflect.DeepEqual(got, want) {
				t.Errorf("the stack carries outputs %v; the guest declared %v", got, want)
			}
		})
	}
}

// TestDeclaredNamesReportsOneNameOnceWhenTwoProjectionsAgree pins the shape both
// doors into the outputs space reach: a fragment carrying an explicit Output and
// a provides key of the same name projects both to one output id. The two
// projections are real, and the stack carries that id once, so the declaration
// reports it once.
//
// Counting it twice would be a name the stack does not carry, offered to a reader
// that has the names and not the bodies — which is a collision that never
// happened, indistinguishable from one that did.
func TestDeclaredNamesReportsOneNameOnceWhenTwoProjectionsAgree(t *testing.T) {
	frag := []byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: same\nprovides:\n  host: same\n")

	_, outs, err := DeclaredNames("api", [][]byte{frag})
	if err != nil {
		t.Fatalf("DeclaredNames: %v", err)
	}
	t.Logf("declared output ids: %v", outs)
	if want := []string{"ApiHost"}; !reflect.DeepEqual(outs, want) {
		t.Fatalf("declared output ids = %v, want %v — one name the stack carries once", outs, want)
	}

	// And the stack it declares for does assemble, carrying that one name — the
	// two sides of the same claim, measured rather than assumed to agree.
	out, err := AssembleStack([]NodeFragment{{NodeID: "api", Files: [][]byte{frag}}})
	if err != nil {
		t.Fatalf("a restatement that loses nothing must assemble: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}
	var outputs map[string]json.RawMessage
	if err := json.Unmarshal(top["Outputs"], &outputs); err != nil {
		t.Fatalf("Outputs not an object: %v", err)
	}
	if want := []string{"ApiHost"}; !reflect.DeepEqual(sortedCopy(keysOf(outputs)), want) {
		t.Fatalf("the stack carries outputs %v, want %v", sortedCopy(keysOf(outputs)), want)
	}
}

// TestDeclaredNamesKeepsTheRepeatWhenTwoProjectionsDisagree is the companion of
// the test above and the reason the fold is scoped by body rather than by name.
// The same two doors claim one name with different bodies: one of the two would
// be lost, so the merge refuses the fragment — and the declaration keeps the
// repeat.
//
// Keeping it is what a check reading names without bodies depends on. Such a
// check cannot tell a restatement that loses nothing from an overwrite that loses
// a declared output; the repetition is the whole of what it has to go on, so
// folding this case away would hide a real collision from the layer whose job is
// to catch it.
func TestDeclaredNamesKeepsTheRepeatWhenTwoProjectionsDisagree(t *testing.T) {
	frag := []byte("Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: explicit\nprovides:\n  host: projected\n")

	_, outs, err := DeclaredNames("api", [][]byte{frag})
	if err != nil {
		t.Fatalf("DeclaredNames: %v", err)
	}
	t.Logf("declared output ids: %v", outs)
	if want := []string{"ApiHost", "ApiHost"}; !reflect.DeepEqual(outs, want) {
		t.Fatalf("declared output ids = %v, want %v — a lossy repeat stays visible", outs, want)
	}

	if _, err := AssembleStack([]NodeFragment{{NodeID: "api", Files: [][]byte{frag}}}); err == nil {
		t.Fatal("expected a loud halt: two bodies claim one output name and one would be lost")
	} else {
		t.Logf("halt: %s", err)
	}
}

// stackNames assembles one node's fragments and reads back the names the
// assembled stack addresses that node's content by. It is the other half of every
// declaration comparison below: the comparison is against what the merge actually
// wrote, never against a literal, so the two sides cannot pass by having drifted
// together.
func stackNames(t *testing.T, nodeID string, fragments [][]byte) (resources, outputs []string) {
	t.Helper()
	out, err := AssembleStack([]NodeFragment{{NodeID: nodeID, Files: fragments}})
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}
	var res map[string]json.RawMessage
	if err := json.Unmarshal(top["Resources"], &res); err != nil {
		t.Fatalf("Resources not an object: %v", err)
	}
	var outs map[string]json.RawMessage
	if raw, ok := top["Outputs"]; ok {
		if err := json.Unmarshal(raw, &outs); err != nil {
			t.Fatalf("Outputs not an object: %v", err)
		}
	}
	return keysOf(res), keysOf(outs)
}

// TestDeclaredNamesAreTheNamesTheMergeWritesAcrossFragments is the node-scope
// form of the property: a node contributes several fragments and hands over one
// declaration, so the comparison that matters is between that whole declaration
// and what the assembled stack carries for the node.
//
// The rows that make it bite are the ones where two fragments claim one name with
// one body. The merge holds one value per name, so the stack carries such a name
// once; a declaration derived per fragment and joined afterwards would offer it
// twice, and a reader holding names without bodies cannot tell that from two
// contributors colliding. Counting across the fragments is what closes that,
// which is why the fragments are read as one set rather than one at a time.
func TestDeclaredNamesAreTheNamesTheMergeWritesAcrossFragments(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fragments []string
	}{
		{
			name: "two fragments, entirely distinct names",
			fragments: []string{
				"Resources:\n  DataBucket:\n    Type: AWS::S3::Bucket\nprovides:\n  bucket_arn: literal\n",
				"Resources:\n  WorkQueue:\n    Type: AWS::SQS::Queue\nprovides:\n  queue_url: literal\n",
			},
		},
		{
			name: "two fragments restate one resource with the same body",
			fragments: []string{
				"Resources:\n  Shared:\n    Type: AWS::S3::Bucket\n  OnlyHere:\n    Type: AWS::SQS::Queue\n",
				"Resources:\n  Shared:\n    Type: AWS::S3::Bucket\n  OnlyThere:\n    Type: AWS::SNS::Topic\n",
			},
		},
		{
			name: "two fragments restate one provides entry with the same body",
			fragments: []string{
				"Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nprovides:\n  host: same\n",
				"Resources:\n  Other:\n    Type: AWS::SQS::Queue\nprovides:\n  host: same\n",
			},
		},
		{
			name: "an explicit output in one fragment and the matching provides key in another",
			fragments: []string{
				"Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nOutputs:\n  host:\n    Value: same\n",
				"Resources:\n  Other:\n    Type: AWS::SQS::Queue\nprovides:\n  host: same\n",
			},
		},
		{
			name: "three fragments, one name restated twice with the same body",
			fragments: []string{
				"Resources:\n  Shared:\n    Type: AWS::S3::Bucket\n",
				"Resources:\n  Shared:\n    Type: AWS::S3::Bucket\n",
				"Resources:\n  Shared:\n    Type: AWS::S3::Bucket\n  Third:\n    Type: AWS::SNS::Topic\n",
			},
		},
		{
			name: "a fragment carrying no addressable section alongside one that does",
			fragments: []string{
				"Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nprovides:\n  host: literal\n",
				"Description: prose only\n",
			},
		},
		{
			name: "a short-form intrinsic restated identically across two fragments",
			fragments: []string{
				"Resources:\n  DataBucket:\n    Type: AWS::S3::Bucket\nprovides:\n  bucket_arn: !GetAtt DataBucket.Arn\n",
				"Resources:\n  WorkQueue:\n    Type: AWS::SQS::Queue\nprovides:\n  bucket_arn: !GetAtt DataBucket.Arn\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const nodeID = "payments-db"

			normalized := make([][]byte, 0, len(tc.fragments))
			for _, f := range tc.fragments {
				n, err := NormalizeFragment([]byte(f))
				if err != nil {
					t.Fatalf("NormalizeFragment: %v", err)
				}
				normalized = append(normalized, n)
			}

			declaredRes, declaredOut, err := DeclaredNames(nodeID, normalized)
			if err != nil {
				t.Fatalf("DeclaredNames: %v", err)
			}
			t.Logf("declared resources %v, outputs %v", declaredRes, declaredOut)

			carriedRes, carriedOut := stackNames(t, nodeID, normalized)

			if got, want := sortedCopy(carriedRes), sortedCopy(declaredRes); !reflect.DeepEqual(got, want) {
				t.Errorf("the stack carries resources %v; the node declared %v", got, want)
			}
			if got, want := sortedCopy(carriedOut), sortedCopy(declaredOut); !reflect.DeepEqual(got, want) {
				t.Errorf("the stack carries outputs %v; the node declared %v", got, want)
			}
		})
	}
}

// TestDeclaredNamesKeepsACrossFragmentRepeatWhoseBodiesDiffer is the companion of
// the property above, and the reason the fold is scoped by body rather than by
// name. Two fragments of one node claim one name with different bodies: one of
// the two would be lost, so the merge refuses the node — and the declaration
// keeps the repeat.
//
// Folding by name would be cheaper and would make every declaration duplicate-
// free, which is exactly the failure: a check reading names without bodies cannot
// tell a restatement that loses nothing from an overwrite that loses a declared
// resource, so the repeated name is the whole of what it has to go on.
func TestDeclaredNamesKeepsACrossFragmentRepeatWhoseBodiesDiffer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fragments   []string
		wantRes     []string
		wantOutputs []string
	}{
		{
			name: "one resource logical id, two bodies",
			fragments: []string{
				"Resources:\n  Thing:\n    Type: AWS::S3::Bucket\n",
				"Resources:\n  Thing:\n    Type: AWS::SQS::Queue\n",
			},
			wantRes:     []string{"Thing", "Thing"},
			wantOutputs: nil,
		},
		{
			name: "one provides entry, two values",
			fragments: []string{
				"Resources:\n  Thing:\n    Type: AWS::S3::Bucket\nprovides:\n  host: one\n",
				"Resources:\n  Other:\n    Type: AWS::SQS::Queue\nprovides:\n  host: two\n",
			},
			wantRes:     []string{"Thing", "Other"},
			wantOutputs: []string{"ApiHost", "ApiHost"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized := make([][]byte, 0, len(tc.fragments))
			for _, f := range tc.fragments {
				n, err := NormalizeFragment([]byte(f))
				if err != nil {
					t.Fatalf("NormalizeFragment: %v", err)
				}
				normalized = append(normalized, n)
			}

			res, outs, err := DeclaredNames("api", normalized)
			if err != nil {
				t.Fatalf("DeclaredNames: %v", err)
			}
			t.Logf("declared resources %v, outputs %v", res, outs)
			if !reflect.DeepEqual(res, tc.wantRes) {
				t.Errorf("declared resources = %v, want %v — a lossy repeat stays visible", res, tc.wantRes)
			}
			if !reflect.DeepEqual(outs, tc.wantOutputs) {
				t.Errorf("declared outputs = %v, want %v — a lossy repeat stays visible", outs, tc.wantOutputs)
			}

			// The other side of the same claim: the merge refuses the node,
			// which is what makes the repeated name a report of a real loss.
			if _, err := AssembleStack([]NodeFragment{{NodeID: "api", Files: normalized}}); err == nil {
				t.Fatal("expected a loud halt: two bodies claim one name across the node's fragments and one would be lost")
			} else {
				t.Logf("halt: %s", err)
			}
		})
	}
}

// TestDeclaredNamesAcrossFragmentsKeepsEveryDistinctName pins the ordinary case
// the fold must leave alone: many distinct names spread over several fragments
// are all declared, in the order they are derived — each fragment's names ordered
// by name, and the fragments in the order they were contributed.
//
// Asserted as an exact sequence rather than as a set, because a fold that
// collapsed too eagerly and a fold that reordered are different defects and a set
// comparison sees neither.
func TestDeclaredNamesAcrossFragmentsKeepsEveryDistinctName(t *testing.T) {
	fragments := [][]byte{
		[]byte("Resources:\n  Zeta:\n    Type: AWS::S3::Bucket\n  Alpha:\n    Type: AWS::SQS::Queue\nprovides:\n  zulu: literal\n  alfa: literal\n"),
		[]byte("Resources:\n  Beta:\n    Type: AWS::SNS::Topic\nOutputs:\n  yankee:\n    Value: literal\nprovides:\n  bravo: literal\n"),
	}

	res, outs, err := DeclaredNames("api", fragments)
	if err != nil {
		t.Fatalf("DeclaredNames: %v", err)
	}
	t.Logf("declared resources %v, outputs %v", res, outs)

	if want := []string{"Alpha", "Zeta", "Beta"}; !reflect.DeepEqual(res, want) {
		t.Errorf("declared resources = %v, want %v", res, want)
	}
	if want := []string{"ApiAlfa", "ApiZulu", "ApiBravo", "ApiYankee"}; !reflect.DeepEqual(outs, want) {
		t.Errorf("declared outputs = %v, want %v", outs, want)
	}

	carriedRes, carriedOut := stackNames(t, "api", fragments)
	if got, want := sortedCopy(carriedRes), sortedCopy(res); !reflect.DeepEqual(got, want) {
		t.Errorf("the stack carries resources %v; the node declared %v", got, want)
	}
	if got, want := sortedCopy(carriedOut), sortedCopy(outs); !reflect.DeepEqual(got, want) {
		t.Errorf("the stack carries outputs %v; the node declared %v", got, want)
	}
}

// TestDeclaredNamesSourcedFoldsARestatementAcrossFiles carries the same property
// to the sourced path, where a source's several template files are the node's
// fragments. A directory source can restate a name across two of its templates,
// and the assembled stack carries it once, so the node declares it once — while a
// restatement whose bodies differ stays declared twice and the merge refuses it.
//
// Covered on its own rather than folded into the fragment rows because the
// sourced path gathers its own files, and a fold reached by one path and missed
// by the other is precisely the divergence a shared gathering exists to prevent.
func TestDeclaredNamesSourcedFoldsARestatementAcrossFiles(t *testing.T) {
	same := []byte("Resources:\n  Shared:\n    Type: AWS::S3::Bucket\nprovides:\n  host: same\n")
	sameAgain := []byte("Resources:\n  Shared:\n    Type: AWS::S3::Bucket\n  Extra:\n    Type: AWS::SQS::Queue\nprovides:\n  host: same\n")
	readme := []byte("this file is prose, not a template\n")

	res, outs, err := DeclaredNamesSourced("api", []SourcedFile{
		{Path: "a.yaml", Content: same},
		{Path: "b.yaml", Content: sameAgain},
		{Path: "README.md", Content: readme},
	}, true)
	if err != nil {
		t.Fatalf("DeclaredNamesSourced: %v", err)
	}
	t.Logf("declared resources %v, outputs %v", res, outs)
	if want := []string{"Shared", "Extra"}; !reflect.DeepEqual(res, want) {
		t.Errorf("declared resources = %v, want %v — the restated name is carried once", res, want)
	}
	if want := []string{"ApiHost"}; !reflect.DeepEqual(outs, want) {
		t.Errorf("declared outputs = %v, want %v — the restated name is carried once", outs, want)
	}

	carriedRes, carriedOut := stackNames(t, "api", [][]byte{same, sameAgain})
	if got, want := sortedCopy(carriedRes), sortedCopy(res); !reflect.DeepEqual(got, want) {
		t.Errorf("the stack carries resources %v; the node declared %v", got, want)
	}
	if got, want := sortedCopy(carriedOut), sortedCopy(outs); !reflect.DeepEqual(got, want) {
		t.Errorf("the stack carries outputs %v; the node declared %v", got, want)
	}

	// The lossy arm on the same path: two template files claiming one name with
	// different bodies keep the name declared twice, and the merge refuses it.
	differs := []byte("Resources:\n  Shared:\n    Type: AWS::SQS::Queue\n")
	lossyRes, _, err := DeclaredNamesSourced("api", []SourcedFile{
		{Path: "a.yaml", Content: same},
		{Path: "b.yaml", Content: differs},
	}, true)
	if err != nil {
		t.Fatalf("DeclaredNamesSourced(lossy): %v", err)
	}
	t.Logf("lossy declaration: %v", lossyRes)
	if want := []string{"Shared", "Shared"}; !reflect.DeepEqual(lossyRes, want) {
		t.Errorf("declared resources = %v, want %v — a lossy repeat stays visible", lossyRes, want)
	}
	if _, err := AssembleStack([]NodeFragment{{NodeID: "api", Files: [][]byte{same, differs}}}); err == nil {
		t.Fatal("expected a loud halt: two sourced templates claim one name and one body would be lost")
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	if len(out) == 0 {
		return []string{}
	}
	return out
}
