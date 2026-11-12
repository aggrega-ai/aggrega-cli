// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// TestTopLevelSectionsAreAccountedFor holds the section accounting to the template
// language's top-level sections: every section is stamped, merged, carried or
// named out of band, and no section is in two of those groups.
//
// It is the guard that makes the accounting worth writing down. A section added to
// [topLevelSections] without a disposition fails here rather than being dropped by
// the assembler in silence, and a section given two dispositions fails here rather
// than being both carried and reported.
//
// What it cannot check is whether [topLevelSections] is itself complete: the
// template language is not ours, so a section the language gains and nobody adds
// to that list is invisible to this test. The list is written in the language's own
// order so it can be read against the language's own documentation, which is the
// only thing that can settle that question.
func TestTopLevelSectionsAreAccountedFor(t *testing.T) {
	t.Parallel()
	disposition := map[string][]string{}
	for _, group := range []struct {
		name     string
		sections []string
	}{
		{"stamped", stampedSections},
		{"merged", mergedSections},
		{"carried", carriedSections},
		{"named", namedSections},
		{"unioned", unionedSections},
		{"carried with its transform", transformCarriedSections},
	} {
		for _, section := range group.sections {
			disposition[section] = append(disposition[section], group.name)
		}
	}

	for _, section := range topLevelSections {
		switch got := disposition[section]; len(got) {
		case 0:
			t.Errorf("section %q has no disposition: it would be dropped in silence", section)
		case 1:
			// exactly one, which is the contract
		default:
			sort.Strings(got)
			t.Errorf("section %q has %d dispositions (%v); it must have exactly one", section, len(got), got)
		}
		delete(disposition, section)
	}
	for section, groups := range disposition {
		t.Errorf("section %q is given the disposition(s) %v but is not a top-level section of the template language", section, groups)
	}
}

// TestAssembleStackCarriesTheNameKeyedSections is the positive proof: a fragment
// declaring all five carried sections reaches the assembled stack with every one
// of them, under exactly the names the fragment authored, so a resource or output
// referencing one of those names meets its declaration.
func TestAssembleStackCarriesTheNameKeyedSections(t *testing.T) {
	t.Parallel()
	fragment := []byte(`
Parameters:
  DbInstanceClass:
    Type: String
    Default: db.t3.micro
Mappings:
  RegionCidr:
    us-east-1:
      Primary: 10.0.0.0/16
Conditions:
  IsPrimaryRegion:
    Fn::Equals: [{Ref: "AWS::Region"}, us-east-1]
Metadata:
  StoreDocumentation:
    Purpose: the bucket the application reads
Rules:
  InstanceClassIsOffered:
    Assertions:
      - Assert:
          Fn::Contains: [[db.t3.micro], {Ref: DbInstanceClass}]
Resources:
  NetworkVpc:
    Type: AWS::EC2::VPC
    Condition: IsPrimaryRegion
    Properties:
      CidrBlock: !FindInMap [RegionCidr, us-east-1, Primary]
Outputs:
  instance_class:
    Value: !Ref DbInstanceClass
`)
	out, err := AssembleStack([]NodeFragment{{NodeID: "network", Files: [][]byte{fragment}}})
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	var stack map[string]any
	if err := json.Unmarshal(out, &stack); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}

	for _, section := range carriedSections {
		if _, present := stack[section]; !present {
			t.Errorf("the assembled stack carries no %s section\n--- stack ---\n%s", section, out)
		}
	}

	// The names inside a carried section are the author's own, unmodified — which is
	// what makes a reference to one resolve after the merge.
	params, ok := stack[sectionParameters].(map[string]any)
	if !ok {
		t.Fatalf("Parameters is not an object; got %#v", stack[sectionParameters])
	}
	if _, ok := params["DbInstanceClass"]; !ok {
		t.Errorf("the carried Parameters section does not declare DbInstanceClass; got keys %v", sortedKeys(params))
	}
	mappings, ok := stack[sectionMappings].(map[string]any)
	if !ok {
		t.Fatalf("Mappings is not an object; got %#v", stack[sectionMappings])
	}
	if _, ok := mappings["RegionCidr"]; !ok {
		t.Errorf("the carried Mappings section does not declare RegionCidr; got keys %v", sortedKeys(mappings))
	}
	conditions, ok := stack[sectionConditions].(map[string]any)
	if !ok {
		t.Fatalf("Conditions is not an object; got %#v", stack[sectionConditions])
	}
	if _, ok := conditions["IsPrimaryRegion"]; !ok {
		t.Errorf("the carried Conditions section does not declare IsPrimaryRegion; got keys %v", sortedKeys(conditions))
	}

	// And the two that cannot be folded stay out of the emitted bytes.
	for _, section := range namedSections {
		if _, present := stack[section]; present {
			t.Errorf("the assembled stack carries a %s section, which no merge can fold\n--- stack ---\n%s", section, out)
		}
	}
}

// TestAssembleStackDropsTheSectionsNoMergeCanFold pins the other half: a fragment
// declaring a template-wide description, and a Hooks section no carried transform
// reads, assembles without either.
func TestAssembleStackDropsTheSectionsNoMergeCanFold(t *testing.T) {
	t.Parallel()
	fragment := []byte(`
Description: The application data store.
Hooks:
  BlueGreen:
    Type: AWS::CodeDeploy::BlueGreen
Resources:
  StoreBucket:
    Type: AWS::S3::Bucket
`)
	out, err := AssembleStack([]NodeFragment{{NodeID: "store", Files: [][]byte{fragment}}})
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	var stack map[string]any
	if err := json.Unmarshal(out, &stack); err != nil {
		t.Fatalf("assembled stack is not valid JSON: %v", err)
	}
	for _, section := range namedSections {
		if _, present := stack[section]; present {
			t.Errorf("the assembled stack carries a %s section\n--- stack ---\n%s", section, out)
		}
	}
	if _, present := stack[sectionResources]; !present {
		t.Errorf("the assembled stack lost its Resources\n--- stack ---\n%s", out)
	}
}

// TestAssembleStackIsUnchangedForAFragmentDeclaringNoCarriedSection is the
// byte-identity leg at the core's own level: a fragment declaring only Resources
// assembles to exactly the three-key document it assembled before any section was
// carried.
//
// 🔴 The expected bytes are written out rather than compared against a second run
// of the same code, because a comparison of one implementation against itself
// passes whatever the implementation does.
func TestAssembleStackIsUnchangedForAFragmentDeclaringNoCarriedSection(t *testing.T) {
	t.Parallel()
	fragment := []byte(`
Resources:
  StoreBucket:
    Type: AWS::S3::Bucket
`)
	out, err := AssembleStack([]NodeFragment{{NodeID: "store", Files: [][]byte{fragment}}})
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	want := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "StoreBucket": {
      "Type": "AWS::S3::Bucket"
    }
  }
}
`
	if string(out) != want {
		t.Errorf("a fragment declaring no carried section changed the assembled bytes\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestAssembleStackReconcilesACarriedSectionInBothDirections proves the collision
// policy per key space, both ways: two nodes declaring one parameter name with the
// SAME body merge into one entry, and with a DIFFERENT body the assembly halts
// naming the section, the name and both contributors.
//
// 🔴 Both rows are required. A merge with no demonstrated refusal is a silent
// last-writer-wins, which is exactly what carrying a section would otherwise
// introduce.
func TestAssembleStackReconcilesACarriedSectionInBothDirections(t *testing.T) {
	t.Parallel()
	tier := []byte(`
Parameters:
  SharedTier:
    Type: String
    Default: standard
Resources:
  TierQueue:
    Type: AWS::SQS::Queue
`)
	agreeing := []byte(`
Parameters:
  SharedTier:
    Type: String
    Default: standard
Resources:
  TierTopic:
    Type: AWS::SNS::Topic
`)
	conflicting := []byte(`
Parameters:
  SharedTier:
    Type: String
    Default: premium
Resources:
  TierTopic:
    Type: AWS::SNS::Topic
`)

	t.Run("the same body merges", func(t *testing.T) {
		t.Parallel()
		out, err := AssembleStack([]NodeFragment{
			{NodeID: "tier", Files: [][]byte{tier}},
			{NodeID: "tier-twin", Files: [][]byte{agreeing}},
		})
		if err != nil {
			t.Fatalf("two nodes restating one parameter identically must merge; got %v", err)
		}
		var stack map[string]any
		if err := json.Unmarshal(out, &stack); err != nil {
			t.Fatalf("assembled stack is not valid JSON: %v", err)
		}
		params, ok := stack[sectionParameters].(map[string]any)
		if !ok {
			t.Fatalf("Parameters is not an object; got %#v", stack[sectionParameters])
		}
		if got := sortedKeys(params); len(got) != 1 || got[0] != "SharedTier" {
			t.Errorf("the merged Parameters section carries %v, want exactly [SharedTier]\n--- stack ---\n%s", got, out)
		}
		entry, ok := params["SharedTier"].(map[string]any)
		if !ok {
			t.Fatalf("SharedTier is not an object; got %#v", params["SharedTier"])
		}
		if entry["Default"] != "standard" {
			t.Errorf("the merged SharedTier default is %#v, want %q", entry["Default"], "standard")
		}
	})

	t.Run("a different body halts", func(t *testing.T) {
		t.Parallel()
		_, err := AssembleStack([]NodeFragment{
			{NodeID: "tier", Files: [][]byte{tier}},
			{NodeID: "tier-rival", Files: [][]byte{conflicting}},
		})
		if err == nil {
			t.Fatal("two nodes declaring one parameter with different bodies must halt; one of the two defaults would be lost in silence")
		}
		msg := err.Error()
		for _, want := range []string{sectionParameters, "SharedTier", "tier", "tier-rival"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the halt does not name %q; got %q", want, msg)
			}
		}
	})
}

// TestSectionNoticesNamesTheSectionsNoMergeCanFold proves the out-of-band report:
// a node authoring a description and a Hooks section gets one warn-severity
// diagnostic per section, under the shared unsupported-input code, naming the
// section as the author wrote it. Its Transform raises nothing: the assembly carries
// it or refuses it by name.
func TestSectionNoticesNamesTheSectionsNoMergeCanFold(t *testing.T) {
	t.Parallel()
	fragment := []byte(`
Description: The application data store.
Transform: AWS::LanguageExtensions
Hooks:
  BlueGreen:
    Type: AWS::CodeDeploy::BlueGreen
Resources:
  StoreBucket:
    Type: AWS::S3::Bucket
`)
	diags, err := SectionNotices([][]byte{fragment})
	if err != nil {
		t.Fatalf("SectionNotices: %v", err)
	}
	if len(diags) != len(namedSections) {
		t.Fatalf("got %d notices, want %d: %+v", len(diags), len(namedSections), diags)
	}
	for i, d := range diags {
		if d.Severity != abi.SeverityWarn {
			t.Errorf("notice %d severity is %q, want %q", i, d.Severity, abi.SeverityWarn)
		}
		if d.Code != rendercore.UnsupportedFieldCode {
			t.Errorf("notice %d code is %q, want the shared unsupported-input code %q", i, d.Code, rendercore.UnsupportedFieldCode)
		}
		if d.SourceRef != namedSections[i] {
			t.Errorf("notice %d names %q, want the section %q", i, d.SourceRef, namedSections[i])
		}
		if !strings.Contains(d.Message, namedSections[i]) {
			t.Errorf("notice %d message does not name the section %q; got %q", i, namedSections[i], d.Message)
		}
		if !strings.Contains(d.Message, cloudformationTargetName) {
			t.Errorf("notice %d message does not name the target %q; got %q", i, cloudformationTargetName, d.Message)
		}
	}
}

// TestSectionNoticesIsSilentForACarriedOrAbsentSection is the negative control:
// the notices report the two sections that cannot be folded and nothing else, so a
// node authoring only carried sections gets none — a warning about a section that
// DID reach the stack would invite deleting content that is being emitted.
func TestSectionNoticesIsSilentForACarriedOrAbsentSection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		fragment string
	}{
		{"only resources", "Resources:\n  StoreBucket:\n    Type: AWS::S3::Bucket\n"},
		{"every carried section", "Parameters:\n  P:\n    Type: String\nMappings:\n  M:\n    a:\n      b: c\nConditions:\n  C:\n    Fn::Equals: [a, a]\nMetadata:\n  Doc:\n    k: v\nRules:\n  R:\n    Assertions: []\nResources:\n  StoreBucket:\n    Type: AWS::S3::Bucket\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags, err := SectionNotices([][]byte{[]byte(tc.fragment)})
			if err != nil {
				t.Fatalf("SectionNotices: %v", err)
			}
			if len(diags) != 0 {
				t.Errorf("got %d notices, want none: %+v", len(diags), diags)
			}
		})
	}
}

// TestSectionNoticesReportsOneNoticePerSectionPerNode pins the multiplicity: a
// node whose two fragments each author a description is told once, because the
// fact reported is that this node's description did not reach the stack — one fact
// however many times it was written.
func TestSectionNoticesReportsOneNoticePerSectionPerNode(t *testing.T) {
	t.Parallel()
	first := []byte("Description: the first half\nResources:\n  A:\n    Type: AWS::S3::Bucket\n")
	second := []byte("Description: the second half\nResources:\n  B:\n    Type: AWS::S3::Bucket\n")
	diags, err := SectionNotices([][]byte{first, second})
	if err != nil {
		t.Fatalf("SectionNotices: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(diags), diags)
	}
	if diags[0].SourceRef != sectionDescription {
		t.Errorf("the notice names %q, want %q", diags[0].SourceRef, sectionDescription)
	}
}

// TestSectionNoticesSourcedSkipsNonTemplateContent proves the sourced path reads
// the same files the sourced declaration reads: a directory source's non-template
// file is vendored verbatim and declares no section, so a description-shaped line
// in a README raises nothing, while the same content in a template file does.
func TestSectionNoticesSourcedSkipsNonTemplateContent(t *testing.T) {
	t.Parallel()
	readme := SourcedFile{Path: "README", Content: []byte("Description: not a template\n")}
	template := SourcedFile{Path: "stack.yaml", Content: []byte("Description: a template\nResources:\n  A:\n    Type: AWS::S3::Bucket\n")}

	diags, err := SectionNoticesSourced([]SourcedFile{readme}, true)
	if err != nil {
		t.Fatalf("SectionNoticesSourced(readme): %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("a directory source's non-template file raised %d notices, want none: %+v", len(diags), diags)
	}

	// The positive control: the same content in a template file IS reported, so the
	// zero above measures the skip rather than a notice path that never fires.
	diags, err = SectionNoticesSourced([]SourcedFile{template}, true)
	if err != nil {
		t.Fatalf("SectionNoticesSourced(template): %v", err)
	}
	if len(diags) != 1 || diags[0].SourceRef != sectionDescription {
		t.Errorf("a template file's Description raised %+v, want one notice naming %q", diags, sectionDescription)
	}
}
