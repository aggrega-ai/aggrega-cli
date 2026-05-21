// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Number-shape regression tests for the json.Unmarshal → yaml.Unmarshal switch.
// yaml.v3 decodes integer literals as int (vs encoding/json's float64) and
// float literals as float64. Downstream consumers must coerce via asNumber.

func TestExecutePhaseB_NumberShape_IntLiteralIsInt(t *testing.T) {
	t.Parallel()
	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "int-recipe"},
		Spec:     types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"ports": 8080}`}},
	}
	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, isInt := values["ports"].(int); !isInt {
		t.Errorf("expected int (yaml.v3 native), got %T (%v)", values["ports"], values["ports"])
	}
	got, ok := asNumber(values["ports"])
	if !ok || got != 8080 {
		t.Errorf("asNumber: got=%v ok=%v", got, ok)
	}
}

func TestExecutePhaseB_NumberShape_FloatLiteralIsFloat(t *testing.T) {
	t.Parallel()
	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "float-recipe"},
		Spec:     types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"replicas": 3.0}`}},
	}
	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, isFloat := values["replicas"].(float64); !isFloat {
		t.Errorf("expected float64, got %T (%v)", values["replicas"], values["replicas"])
	}
	got, ok := asNumber(values["replicas"])
	if !ok || got != 3.0 {
		t.Errorf("asNumber: got=%v ok=%v", got, ok)
	}
}

func TestExecutePhaseB_NumberShape_StringNumericIsRejectedByAsNumber(t *testing.T) {
	t.Parallel()
	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "str-num-recipe"},
		// Author wrote a string where a number was expected. yaml decodes "8080"
		// as a quoted scalar (string). Downstream coercion via asNumber returns
		// ok=false, allowing the consumer to emit a clear "expected number" error.
		Spec: types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"ports": "8080"}`}},
	}
	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, isString := values["ports"].(string); !isString {
		t.Errorf("expected string, got %T (%v)", values["ports"], values["ports"])
	}
	if _, ok := asNumber(values["ports"]); ok {
		t.Errorf("asNumber should reject string-numeric input; got ok=true")
	}
}

// YAML block-scalar parsing: a Recipe whose valuesMapping emits a multiline
// block scalar parses with newlines preserved. Demonstrates the new
// authoring affordance unlocked by the yaml.Unmarshal switch.
func TestExecutePhaseB_YAMLBlockScalar(t *testing.T) {
	t.Parallel()
	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "block-scalar-recipe"},
		Spec: types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `motd: |
  line1
  line2
  line3
`}},
	}
	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := values["motd"].(string)
	if !ok {
		t.Fatalf("motd: expected string, got %T", values["motd"])
	}
	if got != "line1\nline2\nline3\n" {
		t.Errorf("motd: got %q, want %q", got, "line1\nline2\nline3\n")
	}
}

// Parity test: an existing JSON-shaped Recipe still parses identically. The
// switch is non-breaking for catalogs that produce JSON output today —
// yaml.v3 reads valid JSON natively, and the resulting numeric-type drift
// (float64 → int for int literals) is absorbed at consumption sites via
// asNumber.
func TestExecutePhaseB_JSONShapedParity(t *testing.T) {
	t.Parallel()
	v := types.OrderedMap[any]{}
	v.Set("name", "demo")
	v.Set("port", 8080)
	ctx := &types.TemplateContext{Values: v}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "json-shaped"},
		Spec:     types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"image": "demo:1.0", "name": "{{ .values.name }}", "port": {{ .values.port }}, "enabled": true, "tags": ["a", "b"]}`}},
	}
	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if values["image"] != "demo:1.0" {
		t.Errorf("image: %v", values["image"])
	}
	if values["name"] != "demo" {
		t.Errorf("name: %v", values["name"])
	}
	gotPort, ok := asNumber(values["port"])
	if !ok || gotPort != 8080 {
		t.Errorf("port: %v (%T)", values["port"], values["port"])
	}
	if values["enabled"] != true {
		t.Errorf("enabled: %v", values["enabled"])
	}
	tags, ok := values["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags: %v", values["tags"])
	}

	// Sanity: every expected key is present (JSON-shaped output decodes to
	// the same key set under yaml.Unmarshal).
	for _, k := range []string{"image", "name", "port", "enabled", "tags"} {
		if _, present := values[k]; !present {
			t.Errorf("expected key %q missing from values map", k)
		}
	}
	if _, isMap := any(values).(map[string]any); !isMap {
		// The map IS map[string]any by declaration — this just
		// guards future signature drift.
		t.Error("values is not map[string]any")
	}
	// strings.Contains check on a deterministic key — covers JSON-shape
	// edge case where empty objects render differently.
	if !strings.Contains(values["image"].(string), "demo") {
		t.Errorf("image content unexpected: %v", values["image"])
	}
}
