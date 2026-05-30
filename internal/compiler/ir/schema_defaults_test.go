// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

func om(pairs ...any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

// TestMergeSchemaDefaults_FlatFillAndAuthorWins covers Case 1 (omitted key
// filled) and author precedence (supplied key untouched).
func TestMergeSchemaDefaults_FlatFillAndAuthorWins(t *testing.T) {
	t.Parallel()
	author := om("database", "appdb", "charset", "latin1")
	defaults := om("charset", "utf8mb4", "collation", "utf8mb4_general_ci")

	got := mergeSchemaDefaults(author, defaults)

	if v, _ := got.Get("charset"); v != "latin1" {
		t.Errorf("author value must win: charset = %v, want latin1", v)
	}
	if v, _ := got.Get("collation"); v != "utf8mb4_general_ci" {
		t.Errorf("omitted default not filled: collation = %v", v)
	}
	if v, _ := got.Get("database"); v != "appdb" {
		t.Errorf("author key dropped: database = %v", v)
	}
	// Order: author keys in author order, then omitted defaults in sorted order.
	want := []string{"database", "charset", "collation"}
	if got.Len() != 3 {
		t.Fatalf("len = %d, want 3 (keys=%v)", got.Len(), got.Keys())
	}
	for i, k := range got.Keys() {
		if k != want[i] {
			t.Errorf("key order[%d] = %q, want %q (full=%v)", i, k, want[i], got.Keys())
		}
	}
}

// TestMergeSchemaDefaults_NestedThreeCases covers Case 1 absent-parent
// synthesis, Case 2 explicit-empty suppression, and Case 3 partial recurse.
func TestMergeSchemaDefaults_NestedThreeCases(t *testing.T) {
	t.Parallel()
	defaults := om(
		"compute", om("cpu", "250m", "memory", "512Mi"),
		"storage", om("size", "10Gi", "type", "gp3"),
	)

	// Case 1: parent absent → synthesized from child defaults.
	// Case 3: parent present non-empty → fill omitted children, author wins.
	// Case 2: parent present empty → suppress.
	author := om(
		"compute", om("cpu", "1000m"), // partial: cpu set, memory omitted
		"storage", om(),               // explicit empty: suppress defaults
	)

	got := mergeSchemaDefaults(author, defaults)

	compute, _ := got.Get("compute")
	computeOM := compute.(types.OrderedMap[any])
	if v, _ := computeOM.Get("cpu"); v != "1000m" {
		t.Errorf("Case 3 author child must win: compute.cpu = %v, want 1000m", v)
	}
	if v, _ := computeOM.Get("memory"); v != "512Mi" {
		t.Errorf("Case 3 omitted child not filled: compute.memory = %v", v)
	}
	storage, _ := got.Get("storage")
	if storageOM := storage.(types.OrderedMap[any]); storageOM.Len() != 0 {
		t.Errorf("Case 2 explicit-empty must stay empty: storage = %v", storageOM.Keys())
	}
}

// TestSchemaDefaults_ExtractFlatNestedAndSkipRef pins the compiled-schema
// extraction: flat default, nested object subtree (sorted), $ref skipped.
func TestSchemaDefaults_ExtractFlatNestedAndSkipRef(t *testing.T) {
	t.Parallel()
	props := map[string]any{
		"charset": map[string]any{"type": "string", "default": "utf8mb4"},
		"compute": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cpu":    map[string]any{"type": "string", "default": "250m"},
				"memory": map[string]any{"type": "string", "default": "512Mi"},
			},
		},
		"resources": map[string]any{"$ref": "#/$defs/Resources"}, // skipped
		"nodefault": map[string]any{"type": "string"},            // no default, no props → omitted
	}

	got := schemaDefaults(props)

	if v, _ := got.Get("charset"); v != "utf8mb4" {
		t.Errorf("flat default = %v", v)
	}
	if _, present := got.Get("resources"); present {
		t.Error("$ref property must be skipped (defaults behind $ref not yet materialized)")
	}
	if _, present := got.Get("nodefault"); present {
		t.Error("property with neither default nor nested defaults must be omitted")
	}
	compute, ok := got.Get("compute")
	if !ok {
		t.Fatal("nested object default subtree missing")
	}
	computeOM := compute.(types.OrderedMap[any])
	if v, _ := computeOM.Get("cpu"); v != "250m" {
		t.Errorf("nested default compute.cpu = %v", v)
	}
	// Nested keys emitted in sorted order.
	if k := computeOM.Keys(); len(k) != 2 || k[0] != "cpu" || k[1] != "memory" {
		t.Errorf("nested key order = %v, want [cpu memory]", k)
	}
}

// TestComponentPillarDefaults_RealSchema is the end-to-end extraction: a
// MariaDB-shaped Component schema (flat values defaults + nested infra) is
// compiled and its declared defaults surface per pillar. This is the
// materialization a rendered template reads when the Aggregate omits the key.
func TestComponentPillarDefaults_RealSchema(t *testing.T) {
	t.Parallel()
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/database/mariadb
  version: "1.0.0"
spec:
  schema:
    values:
      database:
        type: string
        required: true
      charset:
        type: string
        default: utf8mb4
      collation:
        type: string
        default: utf8mb4_general_ci
    infra:
      port:
        type: integer
        default: 3306
      compute:
        cpu:
          type: string
          default: 250m
        memory:
          type: string
          default: 512Mi
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	values, infra := componentPillarDefaults(comp)

	if v, _ := values.Get("charset"); v != "utf8mb4" {
		t.Errorf("values default charset = %v, want utf8mb4", v)
	}
	if v, _ := values.Get("collation"); v != "utf8mb4_general_ci" {
		t.Errorf("values default collation = %v", v)
	}
	if _, present := values.Get("database"); present {
		t.Error("a required field with no default must not be materialized")
	}
	if _, present := infra.Get("port"); !present {
		t.Error("infra default port not extracted")
	}
	compute, ok := infra.Get("compute")
	if !ok {
		t.Fatal("nested infra default compute missing")
	}
	computeOM := compute.(types.OrderedMap[any])
	if v, _ := computeOM.Get("cpu"); v != "250m" {
		t.Errorf("nested infra default compute.cpu = %v, want 250m", v)
	}
}

// TestComponentPillarDefaults_WholePillarRefYieldsNothing documents the current
// limit: when a pillar is authored as a whole-pillar $ref (the standard-library
// naked-base pattern), the compiled pillar has no direct `properties` map, so no
// defaults are materialized today — even though the referenced $defs carries
// them. Composition-aware resolution (resolving $ref/allOf before extraction)
// is a deferred follow-on.
func TestComponentPillarDefaults_WholePillarRefYieldsNothing(t *testing.T) {
	t.Parallel()
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server/base
  version: "1.0.0"
spec:
  schema:
    $defs:
      DbParams:
        charset:
          type: string
          default: utf8mb4
    values:
      $ref: "#/$defs/DbParams"
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	values, _ := componentPillarDefaults(comp)
	if values.Len() != 0 {
		t.Errorf("whole-pillar $ref must yield no inline defaults today; got %v", values.Keys())
	}
}

// TestComponentPillarDefaults_NoSchema returns empty without error.
func TestComponentPillarDefaults_NoSchema(t *testing.T) {
	t.Parallel()
	values, infra := componentPillarDefaults(types.Component{})
	if values.Len() != 0 || infra.Len() != 0 {
		t.Errorf("component with no schema must yield empty defaults; got values=%v infra=%v", values.Keys(), infra.Keys())
	}
}
