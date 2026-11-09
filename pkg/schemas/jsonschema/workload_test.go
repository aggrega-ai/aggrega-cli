// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// TestWorkloadJSONSchemaEmbedded verifies the embedded JSON Schema
// string is non-empty and contains the expected $schema header.
func TestWorkloadJSONSchemaEmbedded(t *testing.T) {
	t.Parallel()
	if WorkloadJSONSchema == "" {
		t.Fatal("WorkloadJSONSchema is empty; the canonical source rebound from the aggrega-schemas module carries no bytes")
	}
	if !strings.Contains(WorkloadJSONSchema, `"$schema"`) {
		t.Errorf("expected $schema header in embedded source; got first 200 chars: %s", WorkloadJSONSchema[:200])
	}
}

// TestWorkloadJSONSchemaParses verifies the embedded source
// JSON-unmarshals cleanly into a *jsonschema.Schema.
func TestWorkloadJSONSchemaParses(t *testing.T) {
	t.Parallel()
	var schema libschema.Schema
	if err := json.Unmarshal([]byte(WorkloadJSONSchema), &schema); err != nil {
		t.Fatalf("json.Unmarshal of embedded workload schema: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("expected root type=object; got %q", schema.Type)
	}
}

// TestWorkloadJSONSchemaResolvesViaAdapter verifies the embedded
// schema constructs a Validator successfully — the adapter's eager
// pre-resolution pass exercises the full schema graph including
// $defs (the workload schema's reusable definitions).
func TestWorkloadJSONSchemaResolvesViaAdapter(t *testing.T) {
	t.Parallel()
	var schema libschema.Schema
	if err := json.Unmarshal([]byte(WorkloadJSONSchema), &schema); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	v, err := jsonschema.New(&schema)
	if err != nil {
		t.Fatalf("jsonschema.New on embedded workload schema: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil Validator; got nil")
	}
}

// TestWorkloadJSONSchemaExtendedResourcesAndRuntimeClass proves the
// structural contract for GPU-expressible workloads: a resourceList
// admits vendor/domain-scoped extended-resource keys (a fully-qualified
// <domain>/<name> with a dotted domain) alongside cpu/memory, while
// bare or dotless keys stay rejected; and runtimeClassName is a
// top-level string that is forbidden on a reference workload. The schema
// is a structural contract only — it does not police which domains are
// reserved (a kubernetes.io-prefixed key is accepted structurally; the
// quantity is piped through verbatim and resolved by the cluster).
func TestWorkloadJSONSchemaExtendedResourcesAndRuntimeClass(t *testing.T) {
	t.Parallel()
	var schema libschema.Schema
	if err := json.Unmarshal([]byte(WorkloadJSONSchema), &schema); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	v, err := jsonschema.New(&schema)
	if err != nil {
		t.Fatalf("jsonschema.New: %v", err)
	}

	cases := []struct {
		name   string
		doc    string
		wantOK bool
	}{
		{
			name:   "extended gpu limit plus runtimeClassName",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"stateless",
				"runtimeClassName":"nvidia",
				"containers":{"main":{"image":"x",
					"resources":{"limits":{"cpu":"2","memory":"8Gi","nvidia.com/gpu":"1"},
					             "requests":{"nvidia.com/gpu":"1"}}}}}`,
		},
		{
			name:   "amd extended resource",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"amd.com/gpu":"2"}}}}}`,
		},
		{
			name:   "multi-dot domain accepted structurally",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"feature.node.kubernetes.io/foo":"1"}}}}}`,
		},
		{
			name:   "cpu memory only unaffected",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"cpu":"1","memory":"1Gi"}}}}}`,
		},
		{
			name:   "bare key without slash rejected",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"gpu":"1"}}}}}`,
		},
		{
			name:   "dotless domain rejected",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"nvidia/gpu":"1"}}}}}`,
		},
		{
			name:   "runtimeClassName forbidden on reference form",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"reference",
				"endpoint":"db.example.com","runtimeClassName":"nvidia"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var instance any
			if err := json.Unmarshal([]byte(tc.doc), &instance); err != nil {
				t.Fatalf("decode instance: %v", err)
			}
			report, err := v.Validate(instance, nil)
			if err != nil {
				t.Fatalf("validate engine error: %v", err)
			}
			ok := len(report.Findings) == 0
			if ok != tc.wantOK {
				t.Fatalf("wantOK=%v got ok=%v findings=%v", tc.wantOK, ok, report.Findings)
			}
		})
	}
}

// TestWorkloadJSONSchemaHostNetwork proves the structural contract for a
// workload that runs on the node's own network: hostNetwork is a top-level
// boolean, it is compute-only (forbidden on a reference workload, which has no
// containers and therefore no network namespace to choose), and the root stays
// closed so a near-miss spelling halts at authoring rather than being accepted
// and dropped.
//
// The false row is not redundant with the true row: omitempty elides false, so
// the schema must accept an explicitly authored false even though it emits
// nothing — otherwise a document round-tripped through a tool that writes
// defaults would stop validating.
func TestWorkloadJSONSchemaHostNetwork(t *testing.T) {
	t.Parallel()
	var schema libschema.Schema
	if err := json.Unmarshal([]byte(WorkloadJSONSchema), &schema); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	v, err := jsonschema.New(&schema)
	if err != nil {
		t.Fatalf("jsonschema.New: %v", err)
	}

	cases := []struct {
		name   string
		doc    string
		wantOK bool
	}{
		{
			name:   "declared true on a compute workload",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","hostNetwork":true,
				"containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "declared false on a compute workload",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","hostNetwork":false,
				"containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "absent is the shape every workload authored before it carries",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload",
				"containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "a string is rejected rather than coerced",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","hostNetwork":"true",
				"containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "forbidden on reference form",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"reference",
				"endpoint":"db.example.com","hostNetwork":true}`,
		},
		{
			name:   "a near-miss spelling halts on the closed root",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","hostNetworking":true,
				"containers":{"main":{"image":"x"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var instance any
			if err := json.Unmarshal([]byte(tc.doc), &instance); err != nil {
				t.Fatalf("decode instance: %v", err)
			}
			report, err := v.Validate(instance, nil)
			if err != nil {
				t.Fatalf("validate engine error: %v", err)
			}
			ok := len(report.Findings) == 0
			if ok != tc.wantOK {
				t.Fatalf("wantOK=%v got ok=%v findings=%v", tc.wantOK, ok, report.Findings)
			}
		})
	}
}

// TestWorkloadJSONSchemaServiceType proves the structural contract for a
// service entry's type: the five enumerated values are accepted, Loopback among
// them, and any other spelling is refused when the Workload is validated rather
// than reaching a provider as a type it would have to guess at.
//
// Every row is one document that differs from the others only in the type
// value, so the accepted rows are the positive control for the refused ones:
// a refused row fails on its type and on nothing else.
func TestWorkloadJSONSchemaServiceType(t *testing.T) {
	t.Parallel()
	var schema libschema.Schema
	if err := json.Unmarshal([]byte(WorkloadJSONSchema), &schema); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	v, err := jsonschema.New(&schema)
	if err != nil {
		t.Fatalf("jsonschema.New: %v", err)
	}

	cases := []struct {
		name        string
		serviceType string
		wantOK      bool
	}{
		{name: "ClusterIP is accepted", serviceType: "ClusterIP", wantOK: true},
		{name: "Headless is accepted", serviceType: "Headless", wantOK: true},
		{name: "NodePort is accepted", serviceType: "NodePort", wantOK: true},
		{name: "LoadBalancer is accepted", serviceType: "LoadBalancer", wantOK: true},
		{name: "Loopback is accepted", serviceType: "Loopback", wantOK: true},
		{name: "a misspelling is refused", serviceType: "Loopbak", wantOK: false},
		{name: "the value is case-sensitive", serviceType: "loopback", wantOK: false},
		{name: "a host address is not a type", serviceType: "127.0.0.1", wantOK: false},
		{name: "an empty type is refused", serviceType: "", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typeJSON, err := json.Marshal(tc.serviceType)
			if err != nil {
				t.Fatalf("encode type: %v", err)
			}
			doc := `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload",
				"containers":{"main":{"image":"x","ports":{"admin":9090}}},
				"services":{"local":{"type":` + string(typeJSON) + `,"ports":{"admin":9090}}}}`
			var instance any
			if err := json.Unmarshal([]byte(doc), &instance); err != nil {
				t.Fatalf("decode instance: %v", err)
			}
			report, err := v.Validate(instance, nil)
			if err != nil {
				t.Fatalf("validate engine error: %v", err)
			}
			ok := len(report.Findings) == 0
			if ok != tc.wantOK {
				t.Fatalf("type %q: wantOK=%v got ok=%v findings=%v", tc.serviceType, tc.wantOK, ok, report.Findings)
			}
		})
	}
}

// TestWorkloadJSONSchemaSuspend proves the structural contract for a held
// schedule: suspend is a top-level boolean the schema accepts beside a
// form: scheduled workload, refuses as a string rather than coercing it, and
// keeps the root closed so a near-miss spelling halts at authoring.
//
// The false row is not redundant with the true row: omitempty elides false, so
// the schema must accept an explicitly authored false even though it emits
// nothing. The absent row is the shape every scheduled workload authored
// without the field carries.
func TestWorkloadJSONSchemaSuspend(t *testing.T) {
	t.Parallel()
	var schema libschema.Schema
	if err := json.Unmarshal([]byte(WorkloadJSONSchema), &schema); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	v, err := jsonschema.New(&schema)
	if err != nil {
		t.Fatalf("jsonschema.New: %v", err)
	}

	cases := []struct {
		name   string
		doc    string
		wantOK bool
	}{
		{
			name:   "declared true on a scheduled workload",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"scheduled",
				"schedule":"@daily","suspend":true,"containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "declared false on a scheduled workload",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"scheduled",
				"schedule":"@daily","suspend":false,"containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "absent on a scheduled workload",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"scheduled",
				"schedule":"@daily","containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "a string is rejected rather than coerced",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"scheduled",
				"schedule":"@daily","suspend":"true","containers":{"main":{"image":"x"}}}`,
		},
		{
			name:   "a near-miss spelling halts on the closed root",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1beta1","kind":"Workload","form":"scheduled",
				"schedule":"@daily","suspended":true,"containers":{"main":{"image":"x"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var instance any
			if err := json.Unmarshal([]byte(tc.doc), &instance); err != nil {
				t.Fatalf("decode instance: %v", err)
			}
			report, err := v.Validate(instance, nil)
			if err != nil {
				t.Fatalf("validate engine error: %v", err)
			}
			ok := len(report.Findings) == 0
			if ok != tc.wantOK {
				t.Fatalf("wantOK=%v got ok=%v findings=%v", tc.wantOK, ok, report.Findings)
			}
		})
	}
}
