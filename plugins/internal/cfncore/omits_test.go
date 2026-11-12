// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// omitsTemplate declares two resources of one Type and a third of another, and
// nothing in it names any resource, so leaving any one out leaves a template
// CloudFormation accepts. Its values exercise what a rewrite could disturb: numbers,
// characters JSON escapes, text outside ASCII, and intrinsics that name no resource.
const omitsTemplate = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Retention:
    Type: Number
    Default: 30
Resources:
  Store:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub "store-${AWS::Region}"
      Tags:
        - Key: note
          Value: "a <b> & c — café"
  Logs:
    Type: AWS::S3::Bucket
    Properties:
      LifecycleConfiguration:
        Rules:
          - ExpirationInDays: !Ref Retention
            Status: Enabled
  Queue:
    Type: AWS::SQS::Queue
    Properties:
      DelaySeconds: 5
      MessageRetentionPeriod: 1209600
      VisibilityTimeout: 2.5
Outputs:
  StoreName:
    Value: !Ref Store
`

// omitsTemplateWithout is omitsTemplate as its author would have written it without
// the Logs bucket, line for line otherwise.
const omitsTemplateWithout = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Retention:
    Type: Number
    Default: 30
Resources:
  Store:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: !Sub "store-${AWS::Region}"
      Tags:
        - Key: note
          Value: "a <b> & c — café"
  Queue:
    Type: AWS::SQS::Queue
    Properties:
      DelaySeconds: 5
      MessageRetentionPeriod: 1209600
      VisibilityTimeout: 2.5
Outputs:
  StoreName:
    Value: !Ref Store
`

// resourceOmits builds the entries a render is handed, each written Type/LogicalID.
func resourceOmits(pairs ...string) []rendercore.ResolvedOmit {
	out := make([]rendercore.ResolvedOmit, 0, len(pairs))
	for i, p := range pairs {
		kind, name, _ := strings.Cut(p, "/")
		out = append(out, rendercore.ResolvedOmit{Entry: rendercore.OmitEntry{Kind: kind, Name: name}, Position: i})
	}
	return out
}

// normalizedFiles normalizes a source's files the way the guest does before it
// hands them to LeaveOutSourced.
func normalizedFiles(t *testing.T, rootIsDir bool, files ...SourcedFile) []SourcedFile {
	t.Helper()
	out, err := NormalizeSourced(files, rootIsDir)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return out
}

// oneTemplate is a single-file source carrying content as its one template.
func oneTemplate(t *testing.T, content string) []SourcedFile {
	t.Helper()
	return normalizedFiles(t, false, SourcedFile{Path: "template.yaml", Content: []byte(content)})
}

// resourceIDs returns the logical IDs one written template declares, sorted.
func resourceIDs(t *testing.T, content []byte) []string {
	t.Helper()
	var doc struct {
		Resources map[string]json.RawMessage `json:"Resources"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatalf("parse the template: %v\n%s", err, content)
	}
	ids := make([]string, 0, len(doc.Resources))
	for id := range doc.Resources {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Every test in this file runs serially, subtests included, because each one first
// normalizes a sourced template the way the guest does. Normalization hands the
// template to the intrinsics processor, which keeps its tag resolvers in a
// package-level map and rewrites that map on every call, so two goroutines inside it
// at once abort the whole test binary with a concurrent map write — a runtime fatal
// error no recover catches and no assertion can attribute. The hazard belongs to the
// host test binary, whose goroutines share one address space; in a build this code
// runs only inside the single-threaded Wasm guest, whose instances share no Go map.
// Do not add t.Parallel to a test here.

// TestLeaveOutSourced_AResourceIsMatchedByItsTypeAndLogicalID leaves out the
// resource an entry names by both: it is gone from the template and the entry counts
// one, and what remains is byte for byte the template its author would have written
// without it, normalized — so the removal re-orders, re-spells and re-indents
// nothing else.
func TestLeaveOutSourced_AResourceIsMatchedByItsTypeAndLogicalID(t *testing.T) {
	files := oneTemplate(t, omitsTemplate)
	out, counts, err := LeaveOutSourced(files, false, resourceOmits("AWS::S3::Bucket/Logs"), "app")
	if err != nil {
		t.Fatalf("leave out: %v", err)
	}
	if !slices.Equal(counts, []int{1}) {
		t.Errorf("counts %v, want [1]", counts)
	}
	if got, want := resourceIDs(t, out[0].Content), []string{"Queue", "Store"}; !slices.Equal(got, want) {
		t.Errorf("the template declares %v, want %v", got, want)
	}
	without := oneTemplate(t, omitsTemplateWithout)
	if !bytes.Equal(out[0].Content, without[0].Content) {
		t.Errorf("the template without Logs is not the one normalization writes for the template authored without it:\ngot:\n%s\nwant:\n%s",
			out[0].Content, without[0].Content)
	}
}

// TestLeaveOutSourced_BothTheTypeAndTheLogicalIDHaveToMatch holds the match to the
// pair: an entry whose Type is right and whose logical ID is not matches nothing,
// and so does one whose logical ID is right and whose Type is not. Each counts zero —
// which the host halts on — and the template comes back exactly as it was handed in.
func TestLeaveOutSourced_BothTheTypeAndTheLogicalIDHaveToMatch(t *testing.T) {
	for _, entry := range []string{
		"AWS::S3::Bucket/Archive",
		"AWS::SQS::Queue/Logs",
		"aws::s3::bucket/Logs",
		"AWS::S3::Bucket/logs",
		"/Logs",
		"AWS::S3::Bucket/",
	} {
		t.Run(entry, func(t *testing.T) {
			files := oneTemplate(t, omitsTemplate)
			out, counts, err := LeaveOutSourced(files, false, resourceOmits(entry), "app")
			if err != nil {
				t.Fatalf("leave out: %v", err)
			}
			if !slices.Equal(counts, []int{0}) {
				t.Errorf("counts %v, want [0]", counts)
			}
			if !bytes.Equal(out[0].Content, files[0].Content) {
				t.Errorf("a template no entry matched was rewritten:\n%s", out[0].Content)
			}
		})
	}
}

// TestLeaveOutSourced_TheCountsSayWhatEachEntryLeftOut reads the counts: a render
// handed no entries returns its files as they were and no counts, so its output
// carries none; two entries naming one resource each count it; and every entry
// gets a count in the order it arrived.
func TestLeaveOutSourced_TheCountsSayWhatEachEntryLeftOut(t *testing.T) {
	files := oneTemplate(t, omitsTemplate)
	out, counts, err := LeaveOutSourced(files, false, nil, "app")
	if err != nil {
		t.Fatalf("no entries: %v", err)
	}
	if counts != nil {
		t.Errorf("a render handed no entries returned counts %v, want none", counts)
	}
	if len(out) != 1 || !bytes.Equal(out[0].Content, files[0].Content) {
		t.Errorf("a render handed no entries changed its files")
	}

	_, counts, err = LeaveOutSourced(files, false,
		resourceOmits("AWS::SQS::Queue/Queue", "AWS::S3::Bucket/Ghost", "AWS::SQS::Queue/Queue"), "app")
	if err != nil {
		t.Fatalf("three entries: %v", err)
	}
	if !slices.Equal(counts, []int{1, 0, 1}) {
		t.Errorf("counts %v, want [1 0 1]", counts)
	}
}

// TestLeaveOutSourced_EveryTemplateOfADirectorySourceIsMatched leaves out of a
// directory source: the entry's count is the number of resources it matched across
// every template, a template it matched nothing in comes back as it was, and a file
// the source vendors verbatim is never read — even one whose text declares the very
// resource the entry names.
func TestLeaveOutSourced_EveryTemplateOfADirectorySourceIsMatched(t *testing.T) {
	vendored := []byte(`{"Resources": {"Queue": {"Type": "AWS::SQS::Queue"}}}`)
	files := normalizedFiles(t, true,
		SourcedFile{Path: "a.yaml", Content: []byte("Resources:\n  Queue:\n    Type: AWS::SQS::Queue\n  Topic:\n    Type: AWS::SNS::Topic\n")},
		SourcedFile{Path: "notes.txt", Content: vendored},
		SourcedFile{Path: "b.json", Content: []byte(`{"Resources": {"Queue": {"Type": "AWS::SQS::Queue"}, "Table": {"Type": "AWS::DynamoDB::Table"}}}`)},
		SourcedFile{Path: "c.template", Content: []byte("Resources:\n  Key:\n    Type: AWS::KMS::Key\n")},
	)
	out, counts, err := LeaveOutSourced(files, true, resourceOmits("AWS::SQS::Queue/Queue", "AWS::KMS::Key/Missing"), "app")
	if err != nil {
		t.Fatalf("leave out: %v", err)
	}
	if !slices.Equal(counts, []int{2, 0}) {
		t.Errorf("counts %v, want [2 0]: the queue both templates declare, and nothing", counts)
	}
	if got := resourceIDs(t, out[0].Content); !slices.Equal(got, []string{"Topic"}) {
		t.Errorf("a.yaml declares %v, want [Topic]", got)
	}
	if !bytes.Equal(out[1].Content, vendored) || out[1].Path != "notes.txt" {
		t.Errorf("the vendored file was not left as it was: %q\n%s", out[1].Path, out[1].Content)
	}
	if got := resourceIDs(t, out[2].Content); !slices.Equal(got, []string{"Table"}) {
		t.Errorf("b.json declares %v, want [Table]", got)
	}
	if !bytes.Equal(out[3].Content, files[3].Content) {
		t.Errorf("c.template, which no entry matched, was rewritten")
	}
}

// TestLeaveOutSourced_ATemplateEveryResourceOfWhichIsLeftOutKeepsItsResources
// leaves out every resource a template declares: the template keeps an empty
// Resources section, which is what it reads as without them.
func TestLeaveOutSourced_ATemplateEveryResourceOfWhichIsLeftOutKeepsItsResources(t *testing.T) {
	files := oneTemplate(t, "Resources:\n  Queue:\n    Type: AWS::SQS::Queue\n")
	out, counts, err := LeaveOutSourced(files, false, resourceOmits("AWS::SQS::Queue/Queue"), "app")
	if err != nil {
		t.Fatalf("leave out: %v", err)
	}
	if !slices.Equal(counts, []int{1}) {
		t.Errorf("counts %v, want [1]", counts)
	}
	if got, want := string(out[0].Content), "{\n  \"Resources\": {}\n}\n"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestLeaveOutSourced_AnEntryWithANamespaceIsRefused refuses an entry carrying a
// namespace, which a CloudFormation template has nowhere, under the condition for a
// construct a plugin has no expression for, with the provider's own sentence. Every
// entry is checked before any is matched, so the refusal comes even where an earlier
// entry would have matched.
func TestLeaveOutSourced_AnEntryWithANamespaceIsRefused(t *testing.T) {
	entries := resourceOmits("AWS::SQS::Queue/Queue", "AWS::S3::Bucket/Logs")
	entries[1].Entry.Namespace = "prod"
	_, counts, err := LeaveOutSourced(oneTemplate(t, omitsTemplate), false, entries, "app")
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("got %v, want the refusal of an entry naming a namespace", err)
	}
	if counts != nil {
		t.Errorf("a refused render returned counts %v", counts)
	}
	want := `cloudformation-provider: an omits entry (kind "AWS::S3::Bucket", name "Logs") names namespace "prod", and a CloudFormation template has no namespaces: ` +
		omitTarget + `; remove the namespace from the entry`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not carry\n%s\ngot:\n%s", want, err.Error())
	}
}

// TestLeaveOutSourced_AnEntryWithASubchartIsRefused refuses an entry naming a
// subchart, which nothing a CloudFormation template declares came from, under the
// condition and in the shape the namespace refusal takes.
func TestLeaveOutSourced_AnEntryWithASubchartIsRefused(t *testing.T) {
	entries := resourceOmits("AWS::SQS::Queue/Queue", "AWS::S3::Bucket/Logs")
	entries[1].Entry.Subchart = "operator"
	_, counts, err := LeaveOutSourced(oneTemplate(t, omitsTemplate), false, entries, "app")
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("got %v, want the refusal of an entry naming a subchart", err)
	}
	if counts != nil {
		t.Errorf("a refused render returned counts %v", counts)
	}
	want := `cloudformation-provider: an omits entry (kind "AWS::S3::Bucket", name "Logs") names subchart "operator", and a CloudFormation template has no subcharts: ` +
		omitTarget + `; remove the subchart from the entry`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not carry\n%s\ngot:\n%s", want, err.Error())
	}
}

// omitsReferring is a template whose App resource can be given one reference to the
// Logs bucket; the Spare queue is named by nothing, so leaving it out is the
// control every reference row builds.
const omitsReferring = `Resources:
  Logs:
    Type: AWS::S3::Bucket
  Spare:
    Type: AWS::SQS::Queue
  App:
    Type: AWS::S3::Bucket
`

// TestLeaveOutSourced_ARemainingReferenceToALeftOutResourceHalts holds the halt on
// every form the template reference gives for naming a resource by its logical ID,
// one row each, wherever in a resource's body or an output it sits. Each halts under
// the condition for something a plugin could not resolve, naming the template, the
// resource, the form and where it sits; each row's control leaves the unreferenced
// queue out instead and passes, so the row fails on its own reference alone.
func TestLeaveOutSourced_ARemainingReferenceToALeftOutResourceHalts(t *testing.T) {
	const named = `template "template.yaml" names resource "Logs" (type "AWS::S3::Bucket") by `
	for _, tc := range []struct {
		name, extra, finding string
	}{
		{"Ref in a property", "    Properties:\n      Target: !Ref Logs\n",
			"Ref at /Resources/App/Properties/Target"},
		{"Ref written in full", "    Properties:\n      Target:\n        Ref: Logs\n",
			"Ref at /Resources/App/Properties/Target"},
		{"Ref in an output", "Outputs:\n  LogsName:\n    Value: !Ref Logs\n",
			"Ref at /Outputs/LogsName/Value"},
		{"Ref in an output's export name", "Outputs:\n  Out:\n    Value: x\n    Export:\n      Name: !Ref Logs\n",
			"Ref at /Outputs/Out/Export/Name"},
		{"Fn::GetAtt as a list", "    Properties:\n      Target:\n        Fn::GetAtt: [Logs, Arn]\n",
			"Fn::GetAtt at /Resources/App/Properties/Target"},
		{"Fn::GetAtt short form", "    Properties:\n      Target: !GetAtt Logs.Arn\n",
			"Fn::GetAtt at /Resources/App/Properties/Target"},
		{"Fn::GetAtt as a dotted string written in full", "    Properties:\n      Target:\n        Fn::GetAtt: Logs.Arn\n",
			"Fn::GetAtt at /Resources/App/Properties/Target"},
		{"Fn::GetAtt of a dotted attribute", "    Properties:\n      Target: !GetAtt Logs.Endpoint.Address\n",
			"Fn::GetAtt at /Resources/App/Properties/Target"},
		{"Fn::GetAtt in an output", "Outputs:\n  LogsArn:\n    Value: !GetAtt [Logs, Arn]\n",
			"Fn::GetAtt at /Outputs/LogsArn/Value"},
		{"DependsOn as a string", "    DependsOn: Logs\n",
			"DependsOn at /Resources/App/DependsOn"},
		{"DependsOn as a list", "    DependsOn: [Spare, Logs]\n",
			"DependsOn at /Resources/App/DependsOn"},
		{"Fn::Sub string form", "    Properties:\n      Target: !Sub \"arn:aws:s3:::${Logs}/*\"\n",
			"Fn::Sub at /Resources/App/Properties/Target"},
		{"Fn::Sub attribute variable", "    Properties:\n      Target: !Sub \"${Logs.Arn}\"\n",
			"Fn::Sub at /Resources/App/Properties/Target"},
		{"Fn::Sub list form", "    Properties:\n      Target: !Sub [\"${Logs}-${Suffix}\", {Suffix: x}]\n",
			"Fn::Sub at /Resources/App/Properties/Target"},
		{"Fn::Sub in an output", "Outputs:\n  Url:\n    Value: !Sub \"https://${Logs.DomainName}\"\n",
			"Fn::Sub at /Outputs/Url/Value"},
		{"Ref in a Fn::Sub variable's value", "    Properties:\n      Target: !Sub [\"${Name}\", {Name: !Ref Logs}]\n",
			"Ref at /Resources/App/Properties/Target/Fn::Sub/1/Name"},
		{"Ref in a Fn::If branch", "    Properties:\n      Target: !If [IsProd, !Ref Logs, x]\n",
			"Ref at /Resources/App/Properties/Target/Fn::If/1"},
		{"Ref in a Fn::Join list", "    Properties:\n      Target: !Join [\"\", [\"arn:\", !Ref Logs]]\n",
			"Ref at /Resources/App/Properties/Target/Fn::Join/1/1"},
		{"Fn::Sub in a resource's metadata", "    Metadata:\n      AWS::CloudFormation::Init:\n        config:\n          files:\n            /etc/app.conf:\n              content: !Sub \"bucket=${Logs}\"\n",
			"Fn::Sub at /Resources/App/Metadata/AWS::CloudFormation::Init/config/files/~1etc~1app.conf/content"},
		{"Ref in an update policy", "    UpdatePolicy:\n      Policy:\n        Target: !Ref Logs\n",
			"Ref at /Resources/App/UpdatePolicy/Policy/Target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := oneTemplate(t, omitsReferring+tc.extra)
			if _, _, err := LeaveOutSourced(files, false, resourceOmits("AWS::SQS::Queue/Spare"), "app"); err != nil &&
				!strings.Contains(tc.extra, "Spare") {
				t.Fatalf("the control leaving the unreferenced queue out: %v", err)
			}
			_, counts, err := LeaveOutSourced(files, false, resourceOmits("AWS::S3::Bucket/Logs"), "app")
			if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("got %v (counts %v), want the halt on the remaining reference", err, counts)
			}
			for _, want := range []string{
				"cloudformation-provider: what an omits entry left out is still named by the source's templates: ",
				named + tc.finding,
				"CloudFormation refuses a template naming a resource it does not declare: leave out what names it as well, or remove the entry",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not carry\n%s\ngot:\n%s", want, err.Error())
				}
			}
		})
	}
}

// TestLeaveOutSourced_WhatNamesNoResourceIsNotAReference leaves Logs out of templates
// that spell its logical ID in every place the template reference gives that
// spelling some other meaning — a condition, a mapping, another stack's export, a
// region, literal text, a variable a map defines, a section CloudFormation reads no
// reference from — and none of them halts.
func TestLeaveOutSourced_WhatNamesNoResourceIsNotAReference(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
	}{
		{"a literal Fn::Sub variable", "    Properties:\n      Target: !Sub \"${!Logs}\"\n"},
		{"a Fn::Sub variable its map defines", "    Properties:\n      Target: !Sub [\"${Logs}\", {Logs: x}]\n"},
		{"the text of a Fn::Sub string", "    Properties:\n      Target: !Sub \"cfn-init --resource Logs\"\n"},
		{"a plain string", "    Properties:\n      Target: Logs\n"},
		{"a condition attribute", "    Condition: Logs\nConditions:\n  Logs: !Equals [a, a]\n"},
		{"a condition in Fn::If", "    Properties:\n      Target: !If [Logs, a, b]\n"},
		{"a mapping", "    Properties:\n      Target: !FindInMap [Logs, a, b]\n"},
		{"another stack's export", "    Properties:\n      Target: !ImportValue Logs\n"},
		{"a region", "    Properties:\n      Target: !GetAZs Logs\n"},
		{"a Ref whose name is an intrinsic", "    Properties:\n      Target:\n        Ref: !Join [\"\", [Lo, gs]]\n"},
		{"the Conditions section", "Conditions:\n  Always: !Equals [!Ref Logs, x]\n"},
		{"the template's Metadata section", "Metadata:\n  Notes:\n    Ref: Logs\n"},
		{"the Rules section", "Rules:\n  Check:\n    Assertions:\n      - Assert: !Equals [!Ref Logs, x]\n"},
		{"a Ref to a pseudo parameter", "    Properties:\n      Target: !Ref AWS::Region\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, counts, err := LeaveOutSourced(oneTemplate(t, omitsReferring+tc.extra), false, resourceOmits("AWS::S3::Bucket/Logs"), "app")
			if err != nil {
				t.Fatalf("leaving Logs out of a template naming it as no resource halted: %v", err)
			}
			if !slices.Equal(counts, []int{1}) {
				t.Errorf("counts %v, want [1]", counts)
			}
			if got := resourceIDs(t, out[0].Content); !slices.Equal(got, []string{"App", "Spare"}) {
				t.Errorf("the template declares %v, want [App Spare]", got)
			}
		})
	}
}

// TestLeaveOutSourced_AnIDAnotherTemplateStillDeclaresIsNotDangling leaves a bucket
// out of one template of a directory source while another template of the same
// source declares a resource under that logical ID: the templates assemble into one
// stack, so a reference to the ID still meets a resource there and nothing halts.
func TestLeaveOutSourced_AnIDAnotherTemplateStillDeclaresIsNotDangling(t *testing.T) {
	files := normalizedFiles(t, true,
		SourcedFile{Path: "a.yaml", Content: []byte(omitsReferring + "    Properties:\n      Target: !Ref Logs\n")},
		SourcedFile{Path: "b.yaml", Content: []byte("Resources:\n  Logs:\n    Type: AWS::Logs::LogGroup\n")},
	)
	_, counts, err := LeaveOutSourced(files, true, resourceOmits("AWS::S3::Bucket/Logs"), "app")
	if err != nil {
		t.Fatalf("a reference to a logical ID the source still declares halted: %v", err)
	}
	if !slices.Equal(counts, []int{1}) {
		t.Errorf("counts %v, want [1]", counts)
	}
}

// TestLeaveOutSourced_EveryRemainingReferenceIsReportedInOneOrder names every
// remaining reference in one message: template by template in the order the source
// carries them, each template's resources and then its outputs in key order.
func TestLeaveOutSourced_EveryRemainingReferenceIsReportedInOneOrder(t *testing.T) {
	files := normalizedFiles(t, true,
		SourcedFile{Path: "b.yaml", Content: []byte("Resources:\n  Logs:\n    Type: AWS::S3::Bucket\n  Zed:\n    Type: AWS::SQS::Queue\n    DependsOn: Logs\nOutputs:\n  Arn:\n    Value: !GetAtt Logs.Arn\n")},
		SourcedFile{Path: "a.yaml", Content: []byte("Resources:\n  Alpha:\n    Type: AWS::SQS::Queue\n    Properties:\n      Name: !Sub \"${Logs}\"\n")},
	)
	_, _, err := LeaveOutSourced(files, true, resourceOmits("AWS::S3::Bucket/Logs"), "app")
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("got %v, want the halt", err)
	}
	const res = `resource "Logs" (type "AWS::S3::Bucket")`
	want := `template "b.yaml" names ` + res + ` by DependsOn at /Resources/Zed/DependsOn; ` +
		`template "b.yaml" names ` + res + ` by Fn::GetAtt at /Outputs/Arn/Value; ` +
		`template "a.yaml" names ` + res + ` by Fn::Sub at /Resources/Alpha/Properties/Name; ` +
		`CloudFormation refuses`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the halt does not report\n%s\ngot:\n%s", want, err.Error())
	}
}

// TestLeaveOutSourced_AWithheldFieldIsNeverQuoted holds the reports on an entry an
// author marked as produced from a value they declared sensitive: neither the
// namespace refusal nor the reference halt quotes a withheld field — the reference
// halt not even where the withheld name is also the spelling of a key on the path to
// the reference — and each shows the placeholder in its place.
func TestLeaveOutSourced_AWithheldFieldIsNeverQuoted(t *testing.T) {
	const secret = "Logs"
	withheld := func(namespace string) []rendercore.ResolvedOmit {
		entries := resourceOmits("AWS::S3::Bucket/" + secret)
		entries[0].Entry.Namespace = namespace
		entries[0].Withheld = rendercore.OmitFieldsWithheld{Kind: true, Name: true, Namespace: true}
		return entries
	}

	t.Run("a namespace", func(t *testing.T) {
		_, _, err := LeaveOutSourced(oneTemplate(t, omitsTemplate), false, withheld("prod"), "app")
		if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
			t.Fatalf("got %v, want the namespace refusal", err)
		}
		placeholder := redact.Placeholder()
		want := "(kind " + placeholder + ", name " + placeholder + ") names namespace " + placeholder
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "prod") || !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal quotes a withheld field or lacks the placeholder:\n%s", err.Error())
		}
	})
	t.Run("a reference", func(t *testing.T) {
		files := oneTemplate(t, omitsReferring+
			"Outputs:\n  Out:\n    Value: !Sub [\"${Logs}\", {Logs: !Ref Logs}]\n")
		_, _, err := LeaveOutSourced(files, false, withheld(""), "app")
		if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
			t.Fatalf("got %v, want the reference halt", err)
		}
		placeholder := redact.Placeholder()
		want := `template "template.yaml" names resource ` + placeholder + ` (type ` + placeholder + `) by Ref at /Outputs/Out/Value/Fn::Sub/1/` + placeholder
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "AWS::S3::Bucket") || !strings.Contains(err.Error(), want) {
			t.Errorf("the halt quotes a withheld field or lacks the placeholder:\nwant %s\ngot:\n%s", want, err.Error())
		}
	})
}

// TestLeaveOutSourced_LeavingOutChangesNoInputOfTheRender holds the render to its
// inputs: the files it is handed read the same after it has left resources out of
// them.
func TestLeaveOutSourced_LeavingOutChangesNoInputOfTheRender(t *testing.T) {
	files := oneTemplate(t, omitsTemplate)
	before := bytes.Clone(files[0].Content)
	if _, _, err := LeaveOutSourced(files, false, resourceOmits("AWS::S3::Bucket/Logs"), "app"); err != nil {
		t.Fatalf("leave out: %v", err)
	}
	if !bytes.Equal(files[0].Content, before) {
		t.Errorf("the input template changed under the render")
	}
}

// TestLeaveOutSourced_ALeftOutResourceIsNeitherStampedNorDeclared composes the
// sourced path in the order the guest runs it — normalize, leave out, stamp, declare
// — and reads the names the node declares for the duplicate-name check: the
// left-out resource is not among them, and nothing stamped it.
func TestLeaveOutSourced_ALeftOutResourceIsNeitherStampedNorDeclared(t *testing.T) {
	files, _, err := LeaveOutSourced(oneTemplate(t, omitsTemplate), false, resourceOmits("AWS::S3::Bucket/Logs"), "app")
	if err != nil {
		t.Fatalf("leave out: %v", err)
	}
	stamped, _, err := StampSourcedProvenance(files, false, map[string]string{"aggrega.ai/component-id": "app"})
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if bytes.Contains(stamped[0].Content, []byte(`"Logs"`)) {
		t.Errorf("the stamped template still carries Logs:\n%s", stamped[0].Content)
	}
	resources, _, err := DeclaredNamesSourced("app", stamped, false)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if !slices.Equal(resources, []string{"Queue", "Store"}) {
		t.Errorf("the node declares %v, want [Queue Store]", resources)
	}
}
