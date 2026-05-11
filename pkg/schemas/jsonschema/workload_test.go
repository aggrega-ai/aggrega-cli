// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/cognitio/aggrega-cli/pkg/validator/jsonschema"
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
