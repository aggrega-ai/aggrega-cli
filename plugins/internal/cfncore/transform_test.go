// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// Fragments the Transform rows assemble. Each contributes a resource under its own
// logical ID, so two of them always assemble.
const (
	samFragment = `Transform: AWS::Serverless-2016-10-31
Resources:
  Handler:
    Type: AWS::Serverless::Function
    Properties:
      Runtime: python3.12
      Handler: index.handler
      InlineCode: "def handler(event, context): return 'ok'"
`
	plainFragment = `Resources:
  Store:
    Type: AWS::S3::Bucket
`
	languageExtensionsFragment = `Transform: AWS::LanguageExtensions
Resources:
  Queue:
    Type: AWS::SQS::Queue
`
	secretsFragment = `Transform: AWS::SecretsManager-2024-09-16
Resources:
  Secret:
    Type: AWS::SecretsManager::Secret
`
)

// assembledTransform assembles one fragment per contributor and returns the stack.
func assembledStack(t *testing.T, fragments ...string) (map[string]any, error) {
	t.Helper()
	nodes := make([]NodeFragment, 0, len(fragments))
	for i, f := range fragments {
		nodes = append(nodes, NodeFragment{NodeID: []string{"alpha", "beta", "gamma"}[i], Files: [][]byte{[]byte(f)}})
	}
	out, err := AssembleStack(nodes)
	if err != nil {
		return nil, err
	}
	var stack map[string]any
	if err := json.Unmarshal(out, &stack); err != nil {
		t.Fatalf("assembled stack is not JSON: %v\n%s", err, out)
	}
	return stack, nil
}

// TestAssembleStackTransform is the Transform union, row by row: which contributors
// declare what, and the Transform the assembled stack carries or the condition its
// refusal reports under.
func TestAssembleStackTransform(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		fragments []string
		// want is the assembled Transform, nil for none; ignored for a halting row.
		want     any
		wantErr  error
		wantText []string
	}{
		{
			name:      "no contributor declares a transform, and the stack carries none",
			fragments: []string{plainFragment, "Resources:\n  Other:\n    Type: AWS::SNS::Topic\n"},
			want:      nil,
		},
		{
			name:      "a sole contributor keeps its transform as written",
			fragments: []string{"Transform: [AWS::Serverless-2016-10-31]\n" + strings.TrimPrefix(samFragment, "Transform: AWS::Serverless-2016-10-31\n")},
			want:      []any{"AWS::Serverless-2016-10-31"},
		},
		{
			name:      "SAM beside a plain fragment carries one transform as a string",
			fragments: []string{samFragment, plainFragment},
			want:      "AWS::Serverless-2016-10-31",
		},
		{
			name:      "two hosted transforms, in the order the contributors first declare them",
			fragments: []string{samFragment, secretsFragment},
			want:      []any{"AWS::Serverless-2016-10-31", "AWS::SecretsManager-2024-09-16"},
		},
		{
			name:      "the same two hosted transforms, contributors the other way round",
			fragments: []string{secretsFragment, samFragment},
			want:      []any{"AWS::SecretsManager-2024-09-16", "AWS::Serverless-2016-10-31"},
		},
		{
			name: "SAM written before language extensions contradicts the documented order and halts",
			fragments: []string{"Transform: [AWS::Serverless-2016-10-31, AWS::LanguageExtensions]\n" +
				strings.TrimPrefix(samFragment, "Transform: AWS::Serverless-2016-10-31\n"), plainFragment},
			wantErr:  wasm.ErrPluginRequiredInputUnresolved(),
			wantText: []string{"AWS::LanguageExtensions", "AWS::Serverless-2016-10-31", "documented order"},
		},
		{
			name: "language extensions declared before SAM by the same contributor assembles",
			fragments: []string{"Transform: [AWS::LanguageExtensions, AWS::Serverless-2016-10-31]\n" +
				strings.TrimPrefix(samFragment, "Transform: AWS::Serverless-2016-10-31\n"), plainFragment},
			want: []any{"AWS::LanguageExtensions", "AWS::Serverless-2016-10-31"},
		},
		{
			name:      "language extensions beside another contributor's SAM is refused",
			fragments: []string{samFragment, plainFragment, languageExtensionsFragment},
			wantErr:   wasm.ErrPluginCannotExpressInput(),
			wantText:  []string{"AWS::LanguageExtensions", `component "gamma"`, `component "alpha"`},
		},
		{
			name: "two contributors ordering one pair oppositely halts",
			fragments: []string{
				"Transform: [AWS::SecretsManager-2024-09-16, AWS::Serverless-2016-10-31]\nResources:\n  A:\n    Type: AWS::SNS::Topic\n",
				"Transform: [AWS::Serverless-2016-10-31, AWS::SecretsManager-2024-09-16]\nResources:\n  B:\n    Type: AWS::SNS::Topic\n",
			},
			wantErr:  wasm.ErrPluginRequiredInputUnresolved(),
			wantText: []string{`component "alpha"`, `component "beta"`, "AWS::SecretsManager-2024-09-16", "AWS::Serverless-2016-10-31"},
		},
		{
			name:      "a custom macro alone is kept as written",
			fragments: []string{"Transform: AccountMacro\n" + plainFragment},
			want:      "AccountMacro",
		},
		{
			name:      "a custom macro beside another contributor is refused",
			fragments: []string{"Transform: AccountMacro\n" + plainFragment, "Resources:\n  Other:\n    Type: AWS::SNS::Topic\n"},
			wantErr:   wasm.ErrPluginCannotExpressInput(),
			wantText:  []string{`"AccountMacro"`, `component "alpha"`, `component "beta"`},
		},
		{
			name:      "AWS::CodeDeployBlueGreen is refused even alone",
			fragments: []string{"Transform: [AWS::CodeDeployBlueGreen]\n" + plainFragment},
			wantErr:   wasm.ErrPluginCannotExpressInput(),
			wantText:  []string{"AWS::CodeDeployBlueGreen", "Outputs"},
		},
		{
			name:      "AWS::Include, written as a mapping, is refused even alone",
			fragments: []string{"Transform:\n  Name: AWS::Include\n  Parameters:\n    Location: s3://bucket/snippet.yaml\n" + plainFragment},
			wantErr:   wasm.ErrPluginCannotExpressInput(),
			wantText:  []string{"AWS::Include"},
		},
		{
			name: "SAM Globals beside another contributor's serverless resources are refused",
			fragments: []string{
				samFragment + "Globals:\n  Function:\n    Timeout: 30\n",
				"Resources:\n  Other:\n    Type: AWS::Serverless::Function\n    Properties:\n      Runtime: python3.12\n      Handler: index.handler\n      InlineCode: x\n",
			},
			wantErr:  wasm.ErrPluginCannotExpressInput(),
			wantText: []string{"Globals", `component "alpha"`, `component "beta"`},
		},
		{
			name: "a SAM event naming another contributor's resource is refused",
			fragments: []string{
				samFragment + "        \n" + "      Events:\n        Upload:\n          Type: S3\n          Properties:\n            Bucket: !Ref Store\n            Events: s3:ObjectCreated:*\n",
				plainFragment,
			},
			wantErr:  wasm.ErrPluginCannotExpressInput(),
			wantText: []string{`"Upload"`, `"Store"`, `component "beta"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stack, err := assembledStack(t, tc.fragments...)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("assembly succeeded; want a halt under %v", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("assembly halted with %v; want %v", err, tc.wantErr)
				}
				for _, want := range tc.wantText {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the halt does not name %s: %v", want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("AssembleStack: %v", err)
			}
			got, present := stack[sectionTransform]
			if tc.want == nil {
				if present {
					t.Errorf("the stack carries a Transform %#v; no contributor declared one", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Transform = %#v, want %#v", got, tc.want)
			}
			resources, _ := stack[sectionResources].(map[string]any)
			if len(resources) != len(tc.fragments) {
				t.Errorf("the stack carries %d resources, want one per contributor: %v", len(resources), sortedKeys(resources))
			}
		})
	}
}

// TestAssembleStackCarriesGlobalsWithTheServerlessTransform proves the Globals leg:
// a contributor declaring the AWS::Serverless transform has its Globals carried into
// the stack, beside a plain fragment that declares no serverless resource.
func TestAssembleStackCarriesGlobalsWithTheServerlessTransform(t *testing.T) {
	t.Parallel()
	stack, err := assembledStack(t, samFragment+"Globals:\n  Function:\n    Timeout: 30\n", plainFragment)
	if err != nil {
		t.Fatalf("AssembleStack: %v", err)
	}
	globals, ok := stack[sectionGlobals].(map[string]any)
	if !ok {
		t.Fatalf("the stack carries no Globals; got %#v", stack[sectionGlobals])
	}
	if _, ok := globals["Function"]; !ok {
		t.Errorf("the carried Globals lost the Function entry: %#v", globals)
	}
}
