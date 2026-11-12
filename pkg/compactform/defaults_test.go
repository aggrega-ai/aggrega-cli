// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform_test

import (
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// om builds a nested map in the value-typed form, which is the form
// SchemaDefaults produces. It is the DEFAULTS side of a merge, and using it for
// the author side would assemble a shape no authored document ever arrives as.
func om(pairs ...any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

// authored decodes YAML into an OrderedMap the way the product decodes an
// authored document, so a nested group arrives in the pointer-typed form a
// decoder actually produces rather than a hand-assembled value-typed one.
//
// The distinction is the whole point of this helper: a hand-assembled author
// fixture asserts a merge path that production never takes.
func authored(t *testing.T, src string) types.OrderedMap[any] {
	t.Helper()
	var out types.OrderedMap[any]
	if err := yaml.Unmarshal([]byte(src), &out); err != nil {
		t.Fatalf("decoding author fixture: %v", err)
	}
	return out
}

// nestedOf reads a nested node from a merged result in whichever storage form
// it carries. A merge legitimately yields both: a recursed group comes back
// value-typed, while a group kept verbatim keeps the decoder's pointer form.
func nestedOf(t *testing.T, v any, what string) types.OrderedMap[any] {
	t.Helper()
	switch n := v.(type) {
	case types.OrderedMap[any]:
		return n
	case *types.OrderedMap[any]:
		if n == nil {
			t.Fatalf("%s: nested map is a nil pointer", what)
		}
		return *n
	}
	t.Fatalf("%s: not a nested map, got %T", what, v)
	return types.OrderedMap[any]{}
}

// compiledPillarDefaults runs authored schema text through the same path the
// product uses — a `schema:` node decoded off an entity, compiled, then read for
// one pillar's declared defaults.
func compiledPillarDefaults(t *testing.T, entitySrc, pillar string) types.OrderedMap[any] {
	t.Helper()
	var entity struct {
		Schema yaml.Node `yaml:"schema"`
	}
	if err := yaml.Unmarshal([]byte(entitySrc), &entity); err != nil {
		t.Fatalf("decoding entity fixture: %v", err)
	}
	compiled, err := compactform.Compile(&entity.Schema)
	if err != nil {
		t.Fatalf("compiling schema fixture: %v", err)
	}
	return compactform.PillarDefaults(compiled, pillar)
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
//
// The author map is DECODED rather than hand-assembled, so a nested group
// arrives pointer-typed the way an authored document produces it. Assembling it
// by hand exercises a merge path no build takes.
func TestMergeSchemaDefaults_NestedThreeCases(t *testing.T) {
	t.Parallel()
	defaults := om(
		"compute", om("cpu", "250m", "memory", "512Mi"),
		"storage", om("size", "10Gi", "type", "gp3"),
		"backup", om("enabled", true, "retention_days", 7),
	)
	author := authored(t, `
compute:
  cpu: 1000m
storage: {}
`)

	got := compactform.MergeSchemaDefaults(author, defaults)

	computeRaw, _ := got.Get("compute")
	computeOM := nestedOf(t, computeRaw, "compute")
	if v, _ := computeOM.Get("cpu"); v != "1000m" {
		t.Errorf("Case 3 author child must win: compute.cpu = %v, want 1000m", v)
	}
	if v, _ := computeOM.Get("memory"); v != "512Mi" {
		t.Errorf("Case 3 omitted sibling default dropped: compute.memory = %v, want 512Mi", v)
	}

	storageRaw, _ := got.Get("storage")
	if storageOM := nestedOf(t, storageRaw, "storage"); storageOM.Len() != 0 {
		t.Errorf("Case 2 explicit-empty must stay empty: storage = %v", storageOM.Keys())
	}

	backupRaw, ok := got.Get("backup")
	if !ok {
		t.Fatal("Case 1 absent group not synthesized: backup missing")
	}
	backupOM := nestedOf(t, backupRaw, "backup")
	if v, _ := backupOM.Get("retention_days"); v != 7 {
		t.Errorf("Case 1 synthesized subtree incomplete: backup.retention_days = %v, want 7", v)
	}
}

// TestMergeSchemaDefaults_PartialGroupKeepsSiblingDefaults pins the behaviour a
// partially-authored group owes: writing one child of a defaulted group leaves
// every sibling default in place.
//
// It runs the whole path the product runs — authored schema text compiled and
// read for its declared defaults, an authored document decoded, then merged — so
// the author value arrives in the form a decoder produces rather than one the
// test chose.
func TestMergeSchemaDefaults_PartialGroupKeepsSiblingDefaults(t *testing.T) {
	t.Parallel()

	defaults := compiledPillarDefaults(t, `
schema:
  infra:
    backup:
      enabled:
        type: boolean
        default: true
      retention_days:
        type: integer
        default: 7
    ha:
      enabled:
        type: boolean
        default: false
      read_replicas:
        type: integer
        default: 0
`, "infra")

	// The operator enables backups and says nothing about retention.
	author := authored(t, `
backup:
  enabled: true
`)

	got := compactform.MergeSchemaDefaults(author, defaults)

	backup := nestedOf(t, mustGet(t, got, "backup"), "backup")
	if v, _ := backup.Get("enabled"); v != true {
		t.Errorf("author value must win: backup.enabled = %v, want true", v)
	}
	retention, ok := backup.Get("retention_days")
	if !ok {
		t.Fatal("sibling default dropped: backup.retention_days absent after merging a partially-authored group")
	}
	if fmtNumber(retention) != "7" {
		t.Errorf("backup.retention_days = %v (%T), want 7", retention, retention)
	}

	// The group the author never touched keeps its defaults, which is what scopes
	// the loss precisely to the partially-written group.
	ha := nestedOf(t, mustGet(t, got, "ha"), "ha")
	if v, _ := ha.Get("enabled"); v != false {
		t.Errorf("untouched group default lost: ha.enabled = %v, want false", v)
	}
	if v, ok := ha.Get("read_replicas"); !ok || fmtNumber(v) != "0" {
		t.Errorf("untouched group default lost: ha.read_replicas = %v (present=%v), want 0", v, ok)
	}
}

func mustGet(t *testing.T, m types.OrderedMap[any], key string) any {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("key %q absent; keys = %v", key, m.Keys())
	}
	return v
}

// fmtNumber renders a decoded numeric in a comparable form. A compiled schema's
// declared default reaches the merge as whatever the schema decoder produced, so
// the assertion compares the rendered digits rather than a Go type.
func fmtNumber(v any) string {
	switch n := v.(type) {
	case int:
		return fmt.Sprintf("%d", n)
	case int64:
		return fmt.Sprintf("%d", n)
	case float64:
		return fmt.Sprintf("%g", n)
	case fmt.Stringer:
		return n.String()
	}
	return fmt.Sprintf("%v", v)
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
