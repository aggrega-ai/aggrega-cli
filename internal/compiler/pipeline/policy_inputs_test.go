// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// policyInputRef is an attachment of the plugin at uri, for the naming the files
// take from it.
func policyInputRef(uri string) workspaceconfig.PluginRef {
	return workspaceconfig.PluginRef{URI: uri}
}

// TestPolicyInputFilesNameEachRoleAndAttachment drives the collector through the
// shapes a run records — nothing attached, attachments handed the same payload,
// attachments handed different ones, a role the run stopped inside and a role it
// never reached — and asserts the file names and the account of what is missing.
func TestPolicyInputFilesNameEachRoleAndAttachment(t *testing.T) {
	same := map[string]string{"k": "v"}
	tests := []struct {
		name        string
		record      func(p *PolicyInputs)
		wantFiles   []string
		wantMissing []plugintype.Role
	}{
		{
			name: "nothing attached writes one file per role reached",
			record: func(p *PolicyInputs) {
				for _, role := range plugintype.GovernanceRoles() {
					p.reach(role, 0)
					p.record(role, -1, workspaceconfig.PluginRef{}, same)
				}
			},
			wantFiles: []string{"annotation-transform.json", "ir-verdict.json", "label-transform.json", "render-verdict.json"},
		},
		{
			name: "attachments handed identical payloads share one file",
			record: func(p *PolicyInputs) {
				p.reach(plugintype.RoleIRVerdict, 2)
				p.record(plugintype.RoleIRVerdict, 0, policyInputRef("governance/rego:1.0.0"), same)
				p.record(plugintype.RoleIRVerdict, 1, policyInputRef("governance/cel:1.0.0"), same)
			},
			wantFiles:   []string{"ir-verdict.json"},
			wantMissing: []plugintype.Role{plugintype.RoleAnnotationTransform, plugintype.RoleLabelTransform, plugintype.RoleRenderVerdict},
		},
		{
			name: "attachments handed different payloads get a file each",
			record: func(p *PolicyInputs) {
				p.reach(plugintype.RoleRenderVerdict, 2)
				p.record(plugintype.RoleRenderVerdict, 0, policyInputRef("hub.aggrega.ai/governance/rego:0.8.5"), map[string]string{"a": "1"})
				p.record(plugintype.RoleRenderVerdict, 1, policyInputRef("./plugins/my policy.wasm"), map[string]string{"a": "2"})
			},
			wantFiles:   []string{"render-verdict.1.rego.json", "render-verdict.2." + pathsegment.Encode("my policy.wasm") + ".json"},
			wantMissing: []plugintype.Role{plugintype.RoleAnnotationTransform, plugintype.RoleLabelTransform, plugintype.RoleIRVerdict},
		},
		{
			name: "a role the run stopped inside is written per attachment and named",
			record: func(p *PolicyInputs) {
				p.reach(plugintype.RoleLabelTransform, 2)
				p.record(plugintype.RoleLabelTransform, 0, policyInputRef("governance/rego:1.0.0"), same)
			},
			wantFiles:   []string{"label-transform.1.rego.json"},
			wantMissing: []plugintype.Role{plugintype.RoleAnnotationTransform, plugintype.RoleLabelTransform, plugintype.RoleIRVerdict, plugintype.RoleRenderVerdict},
		},
		{
			name: "a payload that could not be produced is named and not written",
			record: func(p *PolicyInputs) {
				p.reach(plugintype.RoleRenderVerdict, 0)
				p.fail(plugintype.RoleRenderVerdict, json.Unmarshal([]byte("{"), new(any)))
			},
			wantMissing: []plugintype.Role{plugintype.RoleAnnotationTransform, plugintype.RoleLabelTransform, plugintype.RoleIRVerdict, plugintype.RoleRenderVerdict},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPolicyInputs()
			tc.record(p)
			got := p.Files()

			var names []string
			for name := range got.Files {
				names = append(names, name)
				if !IsPolicyInputFileName(name) {
					t.Errorf("%q is written but not recognised as a file the export writes", name)
				}
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, tc.wantFiles) && (len(names) != 0 || len(tc.wantFiles) != 0) {
				t.Errorf("files = %v, want %v", names, tc.wantFiles)
			}

			var missing []plugintype.Role
			for _, gap := range got.Missing {
				missing = append(missing, gap.Role)
				if gap.Reason == "" {
					t.Errorf("the gap for %s carries no reason", gap.Role)
				}
			}
			if !reflect.DeepEqual(missing, tc.wantMissing) && (len(missing) != 0 || len(tc.wantMissing) != 0) {
				t.Errorf("missing = %v, want %v", missing, tc.wantMissing)
			}
		})
	}
}

// TestPolicyInputsUnrequestedRecordsNothing proves a run handed no collector
// records nothing and answers as asking for none: the nil collector is the
// state every run that did not ask is in.
func TestPolicyInputsUnrequestedRecordsNothing(t *testing.T) {
	var p *PolicyInputs
	if p.wanted() {
		t.Fatal("a nil collector reports the payloads as wanted")
	}
	p.reach(plugintype.RoleIRVerdict, 1)
	p.record(plugintype.RoleIRVerdict, 0, policyInputRef("governance/rego:1.0.0"), map[string]string{})
	p.fail(plugintype.RoleIRVerdict, nil)
	p.reset()
	if got := p.Files(); len(got.Files) != 0 || len(got.Missing) != 0 {
		t.Fatalf("a nil collector produced %+v", got)
	}
}

// TestIsPolicyInputFileNameRecognisesOnlyTheExportsNames pins the set of names a
// later export replaces, so a file an author keeps beside the payloads is never
// one of them.
func TestIsPolicyInputFileNameRecognisesOnlyTheExportsNames(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"ir-verdict.json", true},
		{"render-verdict.2.rego.json", true},
		{"label-transform.1.my+zx000020policy.wasm.json", true},
		{"annotation-transform.json", true},
		{"ir-verdict.0.rego.json", false},
		{"ir-verdict.x.rego.json", false},
		{"ir-verdict.rego", false},
		{"ir-verdict.json.bak", false},
		{"policy.rego", false},
		{"notes.json", false},
		{"verdict.json", false},
	}
	for _, tc := range tests {
		if got := IsPolicyInputFileName(tc.name); got != tc.want {
			t.Errorf("IsPolicyInputFileName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPolicyInputDocumentReadsBackToTheDispatchedBytes proves the written
// document is the payload the plugin receives: read back into the payload type,
// as `aggrega plugin run-local` reads a file, it encodes to the bytes the
// dispatch encodes. The settings carry a number, which is the leaf a text form
// is likeliest to change.
func TestPolicyInputDocumentReadsBackToTheDispatchedBytes(t *testing.T) {
	settings := map[string]any{"threshold": 3, "translation": []any{map[string]any{"key": "noted", "severity": "warn"}}}

	t.Run("render-verdict", func(t *testing.T) {
		in := analyzer.RenderPolicyInput{
			Artifacts: []analyzer.RenderedArtifact{{Path: "dist/a.yaml", Content: "kind: Service\n<&>\n", Format: "yaml"}},
			Sources:   []analyzer.PolicySource{{Path: "p.rego", Content: "package p\n"}},
			Settings:  settings,
		}
		var back analyzer.RenderPolicyInput
		assertPolicyInputRoundTrip(t, in, &back, func() ([]byte, error) { return in.EncodeCBOR() }, func() ([]byte, error) { return back.EncodeCBOR() })
	})

	t.Run("annotation-transform", func(t *testing.T) {
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		in := AnnotationTransformInput{
			BindingKeys: []string{"gate.example.com/tier"},
			Sources:     []AnnotationSource{{Path: "a.rego", Content: "package a\n"}},
			Settings:    raw,
		}
		var back AnnotationTransformInput
		assertPolicyInputRoundTrip(t, in, &back, func() ([]byte, error) { return in.EncodeCBOR() }, func() ([]byte, error) { return back.EncodeCBOR() })
	})

	t.Run("label-transform", func(t *testing.T) {
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		labels := types.OrderedMap[string]{}
		labels.Set("component.aggrega.ai/type", "server")
		labels.Set("a.example.com/first", "1")
		nodes := types.OrderedMap[topology.MutatorNode]{}
		nodes.Set("app", topology.MutatorNode{Component: "test/svc", ResolvedVersion: "1.0.0", Labels: labels})
		in := topology.MutatorInput{
			CompilationContext: topology.CompilationContext{ReleaseName: "rel", Environment: "testing"},
			Graph:              topology.MutatorGraph{Nodes: nodes},
			Sources:            []topology.MutatorSource{{Path: "l.rego", Content: "package l\n"}},
			Settings:           raw,
		}
		var back topology.MutatorInput
		assertPolicyInputRoundTrip(t, in, &back, func() ([]byte, error) { return in.EncodeCBOR() }, func() ([]byte, error) { return back.EncodeCBOR() })
	})
}

// assertPolicyInputRoundTrip writes in as the collector writes it, reads the
// document back into back, and compares the two encodings.
func assertPolicyInputRoundTrip(t *testing.T, in, back any, encodeIn, encodeBack func() ([]byte, error)) {
	t.Helper()
	doc, err := policyInputDocument(in)
	if err != nil {
		t.Fatalf("write the document: %v", err)
	}
	if err := json.Unmarshal(doc, back); err != nil {
		t.Fatalf("read the document back: %v", err)
	}
	want, err := encodeIn()
	if err != nil {
		t.Fatalf("encode the dispatched payload: %v", err)
	}
	got, err := encodeBack()
	if err != nil {
		t.Fatalf("encode the payload read from the document: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the document does not read back to the dispatched bytes:\ndocument:\n%s", doc)
	}
}
