// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

	got := compactform.MergeSchemaDefaults(author, defaults)

	if v, _ := got.Get("charset"); v != "latin1" {
		t.Errorf("author value must win: charset = %v, want latin1", v)
	}
	if v, _ := got.Get("collation"); v != "utf8mb4_general_ci" {
		t.Errorf("omitted default not filled: collation = %v", v)
	}
	if v, _ := got.Get("database"); v != "appdb" {
		t.Errorf("author key dropped: database = %v", v)
	}
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
	author := om(
		"compute", om("cpu", "1000m"), // partial: cpu set, memory omitted
		"storage", om(), // explicit empty: suppress defaults
	)

	got := compactform.MergeSchemaDefaults(author, defaults)

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

	got := compactform.SchemaDefaults(props)

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
	if k := computeOM.Keys(); len(k) != 2 || k[0] != "cpu" || k[1] != "memory" {
		t.Errorf("nested key order = %v, want [cpu memory]", k)
	}
}

// TestPillarDefaults_NavigatesProperties confirms PillarDefaults walks
// compiled.properties.<pillar>.properties.* and yields that pillar's defaults.
func TestPillarDefaults_NavigatesProperties(t *testing.T) {
	t.Parallel()
	compiled := compactform.Schema{
		"properties": map[string]any{
			"values": map[string]any{
				"properties": map[string]any{
					"charset": map[string]any{"type": "string", "default": "utf8mb4"},
				},
			},
			"infra": map[string]any{
				"properties": map[string]any{
					"port": map[string]any{"type": "integer", "default": 3306},
				},
			},
		},
	}
	vals := compactform.PillarDefaults(compiled, "values")
	if v, _ := vals.Get("charset"); v != "utf8mb4" {
		t.Errorf("values pillar default charset = %v, want utf8mb4", v)
	}
	infra := compactform.PillarDefaults(compiled, "infra")
	if v, _ := infra.Get("port"); v != 3306 {
		t.Errorf("infra pillar default port = %v, want 3306", v)
	}
	provides := compactform.PillarDefaults(compiled, "provides")
	if provides.Len() != 0 {
		t.Error("absent pillar must yield empty defaults")
	}
}
