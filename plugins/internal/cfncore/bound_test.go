// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// boundSource is the template a sourced node's binding left behind: every
// reference to Stage replaced by its value and Stage's declaration removed.
const boundSource = "Resources:\n  TopicA:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: dev\n"

// boundNode is the sourced contributor whose mapped values bound Stage.
func boundNode() NodeFragment {
	return NodeFragment{NodeID: "streams", Files: [][]byte{[]byte(boundSource)}, Paths: []string{"a.yaml"}, Bound: []string{"Stage"}}
}

// otherNode is a second contributor carrying one fragment under one file name.
func otherNode(fragment string) NodeFragment {
	return NodeFragment{NodeID: "other", Files: [][]byte{[]byte(fragment)}, Paths: []string{"template.yaml"}}
}

// TestAssembleStackRefusesReferencesToBoundParameters is the assembly's half of
// binding a sourced template's parameters, row by row: each row assembles a stack
// beside a contributor whose binding filled Stage, and states whether the stack
// assembles or which contributor, file and parameter its halt names. One fault per
// halting row; each passing row is the control for the halting row above it.
func TestAssembleStackRefusesReferencesToBoundParameters(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		nodes []NodeFragment
		// wantText are words the halt names; nil for a row that assembles.
		wantText []string
		// absentText are words the halt must not carry.
		absentText []string
	}{
		{
			name:     "another contributor's Ref to the bound parameter halts naming the contributor, the template, the parameter and the binder",
			nodes:    []NodeFragment{boundNode(), otherNode("Resources:\n  OtherTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: {Ref: Stage}\n")},
			wantText: []string{`component "other"`, `template "template.yaml"`, `parameter "Stage"`, `valuesMapping of a Recipe or FeatureRecipe rendering for component "streams"`},
		},
		{
			name: "an attached Feature's binding is named by the identity its content is addressed by, and its host's own reference halts",
			nodes: []NodeFragment{
				{NodeID: "<attachment>:frhost@aux", NameBasis: "frhost", Files: [][]byte{[]byte(boundSource)}, Paths: []string{"a.yaml"}, Bound: []string{"Stage"}},
				{NodeID: "frhost", Files: [][]byte{[]byte("Resources:\n  HostTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      DisplayName: {Ref: Stage}\n")}, Paths: []string{"host.yaml"}},
			},
			wantText:   []string{`component "frhost"'s template "host.yaml" references parameter "Stage"`, `rendering for component "frhost"`},
			absentText: []string{"<attachment>"},
		},
		{
			name:     "another contributor naming the bound parameter as an Fn::Sub variable halts",
			nodes:    []NodeFragment{boundNode(), otherNode("Resources:\n  OtherTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: {Fn::Sub: \"${Stage}-other\"}\n")},
			wantText: []string{`component "other"`, `parameter "Stage"`},
		},
		{
			name:     "another contributor's rule reading the bound parameter through Fn::ValueOf halts",
			nodes:    []NodeFragment{boundNode(), otherNode("Rules:\n  Tagged:\n    Assertions:\n      - Assert: {Fn::Equals: [{Fn::ValueOf: [Stage, Tags.Env]}, prod]}\nResources:\n  OtherTopic:\n    Type: AWS::SNS::Topic\n")},
			wantText: []string{`component "other"`, `parameter "Stage"`},
		},
		{
			name: "every reference is named, in the order the contributors are given",
			nodes: []NodeFragment{
				otherNode("Resources:\n  FirstTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: {Ref: Stage}\n"),
				boundNode(),
				{NodeID: "third", Files: [][]byte{[]byte("Resources:\n  ThirdTopic:\n    Type: AWS::SNS::Topic\nOutputs:\n  Shown:\n    Value: {Ref: Stage}\n")}, Paths: []string{"third.yaml"}},
			},
			wantText: []string{`component "other"'s template "template.yaml"`, `component "third"'s template "third.yaml"`},
		},
		{
			name:  "a reference the stack still resolves to another contributor's own declaration of the parameter assembles",
			nodes: []NodeFragment{boundNode(), otherNode("Parameters:\n  Stage:\n    Type: String\nResources:\n  OtherTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: {Ref: Stage}\n")},
		},
		{
			name:  "a Ref naming a resource whose logical ID equals the bound parameter's name assembles",
			nodes: []NodeFragment{boundNode(), otherNode("Resources:\n  Stage:\n    Type: AWS::SNS::Topic\n  OtherTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: {Ref: Stage}\n")},
		},
		{
			name:  "the same reference beside a contributor that bound nothing assembles",
			nodes: []NodeFragment{{NodeID: "streams", Files: [][]byte{[]byte(boundSource)}, Paths: []string{"a.yaml"}}, otherNode("Resources:\n  OtherTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: {Ref: Stage}\n")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := AssembleStack(tc.nodes)
			if tc.wantText == nil {
				if err != nil {
					t.Fatalf("AssembleStack: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the stack assembled; want a halt under %v", wasm.ErrPluginRequiredInputUnresolved())
			}
			if !errors.Is(err, wasm.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("the assembly halted under %v; want %v", err, wasm.ErrPluginRequiredInputUnresolved())
			}
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not name %s: %v", want, err)
				}
			}
			for _, absent := range tc.absentText {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("the halt carries %s: %v", absent, err)
				}
			}
		})
	}
}

// TestAssembleStackBoundNamesChangeNoBytes is the byte-identity leg: a stack whose
// contributors reference no bound parameter assembles the same bytes whether or not
// a contributor carries bound names, since the check only reads.
func TestAssembleStackBoundNamesChangeNoBytes(t *testing.T) {
	t.Parallel()
	other := otherNode("Resources:\n  OtherTopic:\n    Type: AWS::SNS::Topic\n    Properties:\n      TopicName: fixed\n")
	unbound := boundNode()
	unbound.Bound = nil
	want, err := AssembleStack([]NodeFragment{unbound, other})
	if err != nil {
		t.Fatalf("AssembleStack without bound names: %v", err)
	}
	got, err := AssembleStack([]NodeFragment{boundNode(), other})
	if err != nil {
		t.Fatalf("AssembleStack with bound names: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the bound names moved the assembled bytes:\n--- with ---\n%s\n--- without ---\n%s", got, want)
	}
}
