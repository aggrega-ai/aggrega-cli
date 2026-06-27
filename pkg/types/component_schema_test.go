// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestComponent_SchemaField_RoundTripInlineContent pins the upward-
// compatibility property of the schema authoring surface: a Component
// with inline `schema:` content (the most common authoring mode per
// chapter 05) decodes into Spec.Schema and re-encodes preserving the
// pillars (values / infra / provides) and their nested field shapes.
func TestComponent_SchemaField_RoundTripInlineContent(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, minimum: 1024, maximum: 65535 }
      auth:
        username: { type: string, pattern: "^[a-zA-Z][a-zA-Z0-9_]*$" }
    infra:
      cpu_limit: { type: string, pattern: "^[0-9]+m?$" }
    provides:
      host: { type: string }
      port: { type: integer }
`
	var c Component
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if c.Spec.Schema.Kind == 0 {
		t.Fatal("Spec.Schema not populated from `schema:` field")
	}

	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, frag := range []string{"values", "infra", "provides", "65535", "cpu_limit"} {
		if !strings.Contains(outStr, frag) {
			t.Errorf("round-trip lost fragment %q; output:\n%s", frag, outStr)
		}
	}
}

// TestComponent_SchemaField_OmitemptyEludesZeroValue verifies that a
// Component authored without `schema:` does not emit a spurious
// `schema: null` line. Probe 4 confirmed yaml.v3's omitempty correctly
// elides zero-Kind yaml.Node fields; this test pins that behavior at
// the Component type level.
func TestComponent_SchemaField_OmitemptyEludesZeroValue(t *testing.T) {
	t.Parallel()

	c := Component{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Component",
		Metadata: ComponentMetadata{
			Name:    "core/compute/api",
			Version: "1.0.0",
		},
	}
	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	if strings.Contains(outStr, "schema:") {
		t.Errorf("expected no schema field in output (zero-value omitempty); got:\n%s", outStr)
	}
}

// TestComponent_SchemaField_AcceptsImportShape verifies the second of
// three authoring modes per chapter 05: a `schema:` value containing
// only `$import:` is captured into Spec.Schema verbatim. The
// resolution of the import is downstream of the type-layer; the type
// layer's job is to preserve the authored shape.
func TestComponent_SchemaField_AcceptsImportShape(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    $import: ./schemas/api-server.schema.yaml
`
	var c Component
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "$import") {
		t.Errorf("round-trip lost $import directive; output:\n%s", string(out))
	}
}

// TestComponent_SchemaField_AcceptsRefShape verifies the third of three
// authoring modes per chapter 05: a `schema:` value containing only
// `$ref:` is captured verbatim. The OCI URI form is opaque to the
// type layer.
func TestComponent_SchemaField_AcceptsRefShape(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    $ref: oci://hub.aggrega.ai/aggrega/datastores@1.0.0#/schemas/MariaDBPayload
`
	var c Component
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "hub.aggrega.ai/aggrega/datastores") {
		t.Errorf("round-trip lost $ref URI; output:\n%s", string(out))
	}
}

// TestUsesSchemaEntry_ValueInfraSchema_RoundTrip pins the per-edge socket
// contract fields: a usesSchema socket declaring inline valueSchema and
// infraSchema round-trips through YAML, populating both yaml.Node fields,
// alongside the socket's selector.
func TestUsesSchemaEntry_ValueInfraSchema_RoundTrip(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    databases:
      selector: "component.aggrega.ai/type=stateful-server"
      valueSchema:
        pool_size: { type: integer, minimum: 1, maximum: 100 }
      infraSchema:
        storage_class: { type: string }
`
	var c Component
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entry, ok := c.Spec.UsesSchema.Get("databases")
	if !ok {
		t.Fatal("usesSchema socket `databases` not populated")
	}
	if entry.ValueSchema.Kind == 0 {
		t.Error("ValueSchema not populated from `valueSchema:` field")
	}
	if entry.InfraSchema.Kind == 0 {
		t.Error("InfraSchema not populated from `infraSchema:` field")
	}

	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, frag := range []string{"valueSchema", "pool_size", "infraSchema", "storage_class"} {
		if !strings.Contains(outStr, frag) {
			t.Errorf("round-trip lost fragment %q; output:\n%s", frag, outStr)
		}
	}
}

// TestUsesSchemaEntry_SchemaFields_OmitemptyEludeZeroValue verifies a socket
// authored without valueSchema/infraSchema emits no spurious null lines.
func TestUsesSchemaEntry_SchemaFields_OmitemptyEludeZeroValue(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    databases:
      selector: "component.aggrega.ai/type=stateful-server"
`
	var c Component
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, frag := range []string{"valueSchema", "infraSchema"} {
		if strings.Contains(outStr, frag) {
			t.Errorf("unexpected %q in output for socket without per-edge schema:\n%s", frag, outStr)
		}
	}
}
