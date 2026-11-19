// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestReadSettingsReadsNamesAndObjects(t *testing.T) {
	raw := json.RawMessage(`{"other": 3, "target_namespace": "acme",
		"resource_types": ["Microsoft.Storage/storageAccounts@2025-06-01", "aws_sqs_queue",
		{"type": "AWS::SQS::Queue", "component": "queue"}, {"type": "x@v1"}]}`)
	got, d := ReadSettings(raw)
	if d != nil {
		t.Fatalf("unexpected diagnostic %+v", d)
	}
	want := []Request{
		{Type: "Microsoft.Storage/storageAccounts", APIVersion: "2025-06-01"},
		{Type: "aws_sqs_queue"},
		{Type: "AWS::SQS::Queue", Component: "queue"},
		{Type: "x", APIVersion: "v1"},
	}
	if !reflect.DeepEqual(got.ResourceTypes, want) {
		t.Fatalf("requests = %+v, want %+v", got.ResourceTypes, want)
	}
	if got.TargetNamespace != "acme" {
		t.Fatalf("target namespace = %q", got.TargetNamespace)
	}
}

func TestReadSettingsAbsentIsNone(t *testing.T) {
	for _, raw := range []string{``, `{}`, `[1]`, `{"layout": 4}`} {
		got, d := ReadSettings(json.RawMessage(raw))
		if d != nil || len(got.ResourceTypes) != 0 {
			t.Fatalf("%q: got %+v, %+v", raw, got, d)
		}
	}
}

func TestReadSettingsRefusesEachUnreadableShape(t *testing.T) {
	for name, raw := range map[string]string{
		"a string":             `{"resource_types": "aws_sqs_queue"}`,
		"an object":            `{"resource_types": {"type": "a"}}`,
		"null":                 `{"resource_types": null}`,
		"a number entry":       `{"resource_types": ["a", 3]}`,
		"no type":              `{"resource_types": [{"component": "c"}]}`,
		"an unknown member":    `{"resource_types": [{"type": "a", "provider": "p"}]}`,
		"a non-text type":      `{"resource_types": [{"type": 4}]}`,
		"a non-text component": `{"resource_types": [{"type": "a", "component": 4}]}`,
		"an empty version":     `{"resource_types": ["a@"]}`,
		"an empty type":        `{"resource_types": ["@v1"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, d := ReadSettings(json.RawMessage(raw))
			if d == nil || d.Code != codeResourceTypesUnreadable || d.Severity != "halt" {
				t.Fatalf("diagnostic = %+v", d)
			}
			if len(got.ResourceTypes) != 0 {
				t.Fatalf("requests read despite the halt: %+v", got.ResourceTypes)
			}
		})
	}
}

// TestReadSettingsRefusesAComponentThatIsNoName reads an entry whose component
// is empty, which names no Component a catalog can hold: the import halts under
// resource-types-unreadable naming the entry, rather than reading the entry as
// one with no component.
func TestReadSettingsRefusesAComponentThatIsNoName(t *testing.T) {
	const entry = `{"type": "AWS::SQS::Queue", "component": ""}`
	got, d := ReadSettings(json.RawMessage(`{"resource_types": ["aws_sqs_queue", ` + entry + `]}`))
	if d == nil || d.Code != codeResourceTypesUnreadable || d.Severity != "halt" {
		t.Fatalf("diagnostic = %+v", d)
	}
	if !strings.Contains(d.Message, entry) || !strings.Contains(d.Message, "component") {
		t.Errorf("the halt does not name the entry and its component: %s", d.Message)
	}
	if len(got.ResourceTypes) != 0 {
		t.Fatalf("requests read despite the halt: %+v", got.ResourceTypes)
	}
}
