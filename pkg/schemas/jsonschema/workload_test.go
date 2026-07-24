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
		t.Fatal("WorkloadJSONSchema is empty; the //go:embed directive did not pick up workload.json")
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
			doc: `{"apiVersion":"aggrega.ai/v1alpha1","kind":"Workload","form":"stateless",
				"runtimeClassName":"nvidia",
				"containers":{"main":{"image":"x",
					"resources":{"limits":{"cpu":"2","memory":"8Gi","nvidia.com/gpu":"1"},
					             "requests":{"nvidia.com/gpu":"1"}}}}}`,
		},
		{
			name:   "amd extended resource",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1alpha1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"amd.com/gpu":"2"}}}}}`,
		},
		{
			name:   "multi-dot domain accepted structurally",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1alpha1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"feature.node.kubernetes.io/foo":"1"}}}}}`,
		},
		{
			name:   "cpu memory only unaffected",
			wantOK: true,
			doc: `{"apiVersion":"aggrega.ai/v1alpha1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"cpu":"1","memory":"1Gi"}}}}}`,
		},
		{
			name:   "bare key without slash rejected",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1alpha1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"gpu":"1"}}}}}`,
		},
		{
			name:   "dotless domain rejected",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1alpha1","kind":"Workload",
				"containers":{"main":{"image":"x","resources":{"limits":{"nvidia/gpu":"1"}}}}}`,
		},
		{
			name:   "runtimeClassName forbidden on reference form",
			wantOK: false,
			doc: `{"apiVersion":"aggrega.ai/v1alpha1","kind":"Workload","form":"reference",
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
