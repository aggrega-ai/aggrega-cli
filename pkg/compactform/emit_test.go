// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEmitMinMaxDispatchInteger verifies min/max on a `type: integer`
// field emit as `minimum` and `maximum`.
func TestEmitMinMaxDispatchInteger(t *testing.T) {
	t.Parallel()

	src := `port: { type: integer, min: 1, max: 65535 }`
	out := mustCompile(t, src)
	port := out["properties"].(Schema)["port"].(Schema)
	if port["minimum"] != 1 {
		t.Errorf("expected minimum=1; got %v (%T)", port["minimum"], port["minimum"])
	}
	if port["maximum"] != 65535 {
		t.Errorf("expected maximum=65535; got %v", port["maximum"])
	}
}

// TestEmitMinMaxDispatchString verifies min/max on a `type: string`
// field emit as `minLength` and `maxLength`.
func TestEmitMinMaxDispatchString(t *testing.T) {
	t.Parallel()

	src := `name: { type: string, min: 1, max: 64 }`
	out := mustCompile(t, src)
	name := out["properties"].(Schema)["name"].(Schema)
	if name["minLength"] != 1 {
		t.Errorf("expected minLength=1; got %v", name["minLength"])
	}
	if name["maxLength"] != 64 {
		t.Errorf("expected maxLength=64; got %v", name["maxLength"])
	}
}

// TestEmitMinMaxDispatchArray verifies min/max on a `type: array`
// field emit as `minItems` and `maxItems`.
func TestEmitMinMaxDispatchArray(t *testing.T) {
	t.Parallel()

	src := `
tags:
  type: array
  min: 1
  max: 10
  items: { type: string }
`
	out := mustCompile(t, src)
	tags := out["properties"].(Schema)["tags"].(Schema)
	if tags["minItems"] != 1 {
		t.Errorf("expected minItems=1; got %v", tags["minItems"])
	}
	if tags["maxItems"] != 10 {
		t.Errorf("expected maxItems=10; got %v", tags["maxItems"])
	}
}

// TestEmitValuesType verifies the `valuesType:` shorthand expands to
// `additionalProperties: { type: <X> }`.
func TestEmitValuesType(t *testing.T) {
	t.Parallel()

	src := `labels: { type: object, valuesType: string }`
	out := mustCompile(t, src)
	labels := out["properties"].(Schema)["labels"].(Schema)
	ap, ok := labels["additionalProperties"].(Schema)
	if !ok {
		t.Fatalf("additionalProperties is not a Schema; got %T", labels["additionalProperties"])
	}
	if ap["type"] != "string" {
		t.Errorf("expected additionalProperties.type=string; got %v", ap["type"])
	}
}

// TestEmitRequiredAccumulation verifies per-leaf `required: true`
// markers accumulate into the parent object's `required:` array.
func TestEmitRequiredAccumulation(t *testing.T) {
	t.Parallel()

	src := `
auth:
  username: { type: string, default: app_user }
  password: { type: string, required: true }
  database: { type: string, required: true }
`
	out := mustCompile(t, src)
	auth := out["properties"].(Schema)["auth"].(Schema)
	required, ok := auth["required"].([]string)
	if !ok {
		t.Fatalf("required is not []string; got %T", auth["required"])
	}
	if len(required) != 2 {
		t.Fatalf("expected 2 required fields; got %d", len(required))
	}
	hasPassword, hasDatabase := false, false
	for _, r := range required {
		if r == "password" {
			hasPassword = true
		}
		if r == "database" {
			hasDatabase = true
		}
	}
	if !hasPassword || !hasDatabase {
		t.Errorf("expected required=[password,database]; got %v", required)
	}
}

// TestEmitImplicitObjectClosure verifies compact-form objects are
// closed by default (`additionalProperties: false`).
func TestEmitImplicitObjectClosure(t *testing.T) {
	t.Parallel()

	src := `
spec:
  name: { type: string }
`
	out := mustCompile(t, src)
	spec := out["properties"].(Schema)["spec"].(Schema)
	if spec["additionalProperties"] != false {
		t.Errorf("expected additionalProperties=false (closure); got %v", spec["additionalProperties"])
	}
}

// TestEmitValidateExtensionKeyword verifies `validate:` + `validateMessage:`
// emit as `x-aggrega-validate:` and `x-aggrega-validateMessage:`
// extension keywords for the validator adapter to consume.
func TestEmitValidateExtensionKeyword(t *testing.T) {
	t.Parallel()

	src := `
db_size:
  type: string
  validate: '{{ regexFind "^[0-9]+" . | atoi | gt 100 }}'
  validateMessage: "db_size must be at least 100"
`
	out := mustCompile(t, src)
	dbSize := out["properties"].(Schema)["db_size"].(Schema)
	if _, ok := dbSize["x-aggrega-validate"]; !ok {
		t.Errorf("expected x-aggrega-validate extension keyword; got %v", dbSize)
	}
	if msg := dbSize["x-aggrega-validateMessage"]; msg != "db_size must be at least 100" {
		t.Errorf("expected x-aggrega-validateMessage to round-trip; got %v", msg)
	}
}

// TestEmitWhenExtensionKeyword verifies `when:` emits as
// `x-aggrega-when:` extension keyword.
func TestEmitWhenExtensionKeyword(t *testing.T) {
	t.Parallel()

	src := `
secondary:
  $ref: "#/$defs/NodePool"
  when: '{{ eq $.architecture "replication" }}'
`
	out := mustCompile(t, src)
	secondary := out["properties"].(Schema)["secondary"].(Schema)
	if _, ok := secondary["x-aggrega-when"]; !ok {
		t.Errorf("expected x-aggrega-when extension keyword; got %v", secondary)
	}
	if secondary["$ref"] != "#/$defs/NodePool" {
		t.Errorf("expected $ref to round-trip alongside when; got %v", secondary["$ref"])
	}
}

// mustCompile compiles src or fails the test. Helper for test brevity.
func mustCompile(t *testing.T, src string) Schema {
	t.Helper()
	var input yaml.Node
	if err := yaml.Unmarshal([]byte(src), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := Compile(&input)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return out
}
