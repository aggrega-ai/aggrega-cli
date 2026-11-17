// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

func refsOf(names ...string) []TypeRef {
	var out []TypeRef
	for _, n := range names {
		out = append(out, TypeRef{Name: n, APIVersion: "2025-06-01", Source: "f.json"})
	}
	return out
}

func onlyCode(t *testing.T, ds []aggrega.Diagnostic, code string) aggrega.Diagnostic {
	t.Helper()
	if len(ds) != 1 || ds[0].Code != code || ds[0].Severity != "halt" {
		t.Fatalf("diagnostics = %+v, want one halt %s", ds, code)
	}
	return ds[0]
}

func TestSelectDraftsTheOneType(t *testing.T) {
	got, ds := Select(refsOf("aws_sqs_queue"), nil, false, true, nil)
	if len(ds) != 0 || len(got) != 1 || got[0].Ref.Name != "aws_sqs_queue" {
		t.Fatalf("got %+v, %+v", got, ds)
	}
}

func TestSelectHaltsWhenSeveralAndNoneNamed(t *testing.T) {
	refs := refsOf("A::B::C", "A::B::D")
	got, ds := Select(refs, nil, false, false, nil)
	d := onlyCode(t, ds, codeResourceTypeNotNamed)
	if got != nil || !strings.Contains(d.Message, "A::B::C, A::B::D") {
		t.Fatalf("got %+v, message %q", got, d.Message)
	}
	_, ds = Select(refs, nil, false, true, nil)
	d = onlyCode(t, ds, codeResourceTypeNotNamed)
	if strings.Contains(d.Message, "A::B::C") || !strings.Contains(d.Message, "2 resource types") ||
		!strings.Contains(d.Message, "resource_types") {
		t.Fatalf("count-only message %q", d.Message)
	}
}

func TestSelectNamesNearestForAnUnknownType(t *testing.T) {
	refs := refsOf("t_a", "t_b", "t_c", "t_d", "t_e", "t_f", "zzzzzzzz")
	got, ds := Select(refs, []Request{{Type: "t_x"}}, false, false, nil)
	d := onlyCode(t, ds, codeResourceTypeNotFound)
	if got != nil || !strings.Contains(d.Message, `"t_x"`) ||
		!strings.Contains(d.Message, "t_a, t_b, t_c, t_d, t_e") || strings.Contains(d.Message, "t_f") ||
		strings.Contains(d.Message, "zzzzzzzz") {
		t.Fatalf("message %q", d.Message)
	}
}

func TestSelectFoldsCaseForArm(t *testing.T) {
	arm := refsOf("Microsoft.Storage/storageAccounts", "Microsoft.Storage/blobServices")
	got, ds := Select(arm, []Request{{Type: "microsoft.storage/STORAGEACCOUNTS", Component: "sa"}}, true, false, nil)
	if len(ds) != 0 || len(got) != 1 || got[0].Ref.Name != "Microsoft.Storage/storageAccounts" || got[0].Component != "sa" {
		t.Fatalf("got %+v, %+v", got, ds)
	}
	cfn := refsOf("AWS::SNS::Topic", "AWS::SQS::Queue", "AWS::S3::Bucket")
	_, ds = Select(cfn, []Request{{Type: "aws::sqs::queue"}}, false, false, nil)
	d := onlyCode(t, ds, codeResourceTypeNotFound)
	if !strings.Contains(d.Message, "nearest names are AWS::SQS::Queue,") {
		t.Fatalf("message %q", d.Message)
	}
}

func TestSelectRefusesAnotherApiVersion(t *testing.T) {
	arm := refsOf("Microsoft.Storage/storageAccounts")
	_, ds := Select(arm, []Request{{Type: "Microsoft.Storage/storageAccounts", APIVersion: "2020-01-01"}}, true, false, nil)
	d := onlyCode(t, ds, codeResourceTypeNotFound)
	if !strings.Contains(d.Message, "2025-06-01") || !strings.Contains(d.Message, "2020-01-01") {
		t.Fatalf("message %q", d.Message)
	}
	got, ds := Select(arm, []Request{{Type: "Microsoft.Storage/storageAccounts", APIVersion: "2025-06-01"}}, true, false, nil)
	if len(ds) != 0 || len(got) != 1 {
		t.Fatalf("got %+v, %+v", got, ds)
	}
}

func TestSelectKeepsRequestOrderAndCallsResolve(t *testing.T) {
	refs := []TypeRef{{Name: "a", Source: "1"}, {Name: "b", Source: "1"}, {Name: "a", Source: "2"}}
	var called string
	resolve := func(name string, rs []TypeRef) (TypeRef, []aggrega.Diagnostic) {
		called = name
		return rs[1], nil
	}
	got, ds := Select(refs, []Request{{Type: "b"}, {Type: "a"}}, false, false, resolve)
	if len(ds) != 0 || len(got) != 2 || got[0].Ref.Name != "b" || got[1].Ref.Source != "2" || called != "a" {
		t.Fatalf("got %+v, %+v, called %q", got, ds, called)
	}
	halt := func(string, []TypeRef) (TypeRef, []aggrega.Diagnostic) {
		return TypeRef{}, []aggrega.Diagnostic{{Severity: "halt", Code: "x", Message: "m"}}
	}
	got, ds = Select(refs, []Request{{Type: "b"}, {Type: "a"}}, false, false, halt)
	if got != nil || len(ds) != 1 || ds[0].Code != "x" {
		t.Fatalf("got %+v, %+v", got, ds)
	}
}
