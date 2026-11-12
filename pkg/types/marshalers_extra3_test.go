// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestOrderedMap_UnmarshalJSON_EmptyData_OpeningTokenError pins the
// opening-token error path in unmarshalJSONWithDepth: empty data
// returns an io.EOF token error wrapped with descriptive context.
func TestOrderedMap_UnmarshalJSON_EmptyData_OpeningTokenError(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	err := m.UnmarshalJSON([]byte(""))
	if err == nil {
		t.Errorf("empty data should error")
	}
}

// TestInjectBlock_UnmarshalJSON_EmptyData_OpeningTokenError pins the
// opening-token error path in InjectBlock.UnmarshalJSON.
func TestInjectBlock_UnmarshalJSON_EmptyData_OpeningTokenError(t *testing.T) {
	t.Parallel()
	var b InjectBlock
	if err := b.UnmarshalJSON([]byte("")); err == nil {
		t.Errorf("empty data should error")
	}
}

// TestInjectPipe_UnmarshalJSON_EmptyData_OpeningTokenError pins the
// opening-token error path in InjectPipe.UnmarshalJSON.
func TestInjectPipe_UnmarshalJSON_EmptyData_OpeningTokenError(t *testing.T) {
	t.Parallel()
	var p InjectPipe
	if err := p.UnmarshalJSON([]byte("")); err == nil {
		t.Errorf("empty data should error")
	}
}

// TestOrderedMap_DepthExceeded_YAMLAndJSON pins the depth-bound
// guards in unmarshalYAMLWithDepth and unmarshalJSONWithDepth: a
// direct call past orderedMapDecodeMaxDepth returns
// ErrOrderedMapDecodeDepthExceeded.
func TestOrderedMap_DepthExceeded_YAMLAndJSON(t *testing.T) {
	t.Parallel()
	// YAML path.
	var m OrderedMap[any]
	mapNode := &yaml.Node{Kind: yaml.MappingNode}
	err := m.unmarshalYAMLWithDepth(mapNode, orderedMapDecodeMaxDepth+1)
	if err == nil || !errors.Is(err, ErrOrderedMapDecodeDepthExceeded()) {
		t.Errorf("YAML depth-exceeded should error ErrOrderedMapDecodeDepthExceeded; got %v", err)
	}

	// JSON path.
	var m2 OrderedMap[any]
	err = m2.unmarshalJSONWithDepth([]byte(`{}`), orderedMapDecodeMaxDepth+1)
	if err == nil || !errors.Is(err, ErrOrderedMapDecodeDepthExceeded()) {
		t.Errorf("JSON depth-exceeded should error ErrOrderedMapDecodeDepthExceeded; got %v", err)
	}
}

// TestDecodeJSONValue_DepthExceeded pins decodeJSONValue's depth guard
// via a direct white-box call past the max depth.
func TestDecodeJSONValue_DepthExceeded(t *testing.T) {
	t.Parallel()
	_, err := decodeJSONValue[any]([]byte(`{}`), orderedMapDecodeMaxDepth+1)
	if err == nil || !errors.Is(err, ErrOrderedMapDecodeDepthExceeded()) {
		t.Errorf("depth-exceeded should error; got %v", err)
	}
}

// TestDecodeYAMLValue_DepthExceeded pins decodeYAMLValue's depth guard.
func TestDecodeYAMLValue_DepthExceeded(t *testing.T) {
	t.Parallel()
	_, err := decodeYAMLValue[any](&yaml.Node{Kind: yaml.MappingNode}, orderedMapDecodeMaxDepth+1)
	if err == nil || !errors.Is(err, ErrOrderedMapDecodeDepthExceeded()) {
		t.Errorf("depth-exceeded should error; got %v", err)
	}
}

// TestDecodeJSONValue_EmptyJSON pins decodeJSONValue's empty-input
// branch: an empty raw message at the any V path returns a clear
// error.
func TestDecodeJSONValue_EmptyJSON(t *testing.T) {
	t.Parallel()
	_, err := decodeJSONValue[any]([]byte(""), 1)
	if err == nil {
		t.Errorf("empty raw should error")
	}
}

// TestDecodeJSONValue_NumberPath pins the json.Number decode arm: a
// numeric raw (default case in the leading-byte switch) decodes via
// json.NewDecoder with UseNumber for precision preservation.
func TestDecodeJSONValue_NumberPath(t *testing.T) {
	t.Parallel()
	v, err := decodeJSONValue[any]([]byte("12345678901234567890"), 1)
	if err != nil {
		t.Fatalf("number: %v", err)
	}
	num, ok := v.(json.Number)
	if !ok {
		t.Errorf("expected json.Number; got %T %v", v, v)
	}
	if string(num) != "12345678901234567890" {
		t.Errorf("number string = %q; want full precision", num)
	}
}

// TestDecodeJSONValue_ArrayWithNestedObject pins the array branch:
// each element walks recursively, so an array of objects produces
// nested *OrderedMap[any] elements.
func TestDecodeJSONValue_ArrayWithNestedObject(t *testing.T) {
	t.Parallel()
	v, err := decodeJSONValue[any]([]byte(`[{"k":1},"text",42]`), 1)
	if err != nil {
		t.Fatalf("array: %v", err)
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("expected []any of len 3; got %T %v", v, v)
	}
	if _, ok := arr[0].(*OrderedMap[any]); !ok {
		t.Errorf("first element should be *OrderedMap[any]; got %T", arr[0])
	}
}

// TestDecodeYAMLValue_SequenceWithNestedMapping pins the sequence
// branch's recursion: an element that is a YAML mapping produces a
// nested *OrderedMap[any] in the result.
func TestDecodeYAMLValue_SequenceWithNestedMapping(t *testing.T) {
	t.Parallel()
	src := `
- k: v
- "text"
- 42
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	seq := node.Content[0]
	v, err := decodeYAMLValue[any](seq, 1)
	if err != nil {
		t.Fatalf("seq: %v", err)
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("expected []any of len 3; got %T %v", v, v)
	}
	if _, ok := arr[0].(*OrderedMap[any]); !ok {
		t.Errorf("first element should be *OrderedMap[any]; got %T", arr[0])
	}
}

// TestDecodeTypedContract_Direct pins the typed-contract decode
// happy path with realistic contract field set.
func TestDecodeTypedContract_Direct(t *testing.T) {
	t.Parallel()
	m := map[string]any{
		"type":        "string",
		"description": "test desc",
		"pattern":     "^x",
		"format":      "uri",
		"required":    true,
	}
	pc, err := decodeTypedContract(m)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pc.Type != "string" {
		t.Errorf("Type = %q; want string", pc.Type)
	}
	if pc.Pattern != "^x" {
		t.Errorf("Pattern = %q; want ^x", pc.Pattern)
	}
	if !pc.Required {
		t.Errorf("Required = false; want true")
	}
}

// TestSchemaLibrary_TemplateEntry_RoundTrip pins the round-trip for
// TemplateEntry through SchemaLibrary's templates map.
func TestSchemaLibrary_TemplateEntry_RoundTrip(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: TestLib
  version: 1.0.0
spec:
  schemas:
    Probe:
      type: object
      properties:
        port:
          type: integer
  templates:
    SizedString:
      $params:
        size:
          type: integer
          default: 10
      type: string
      maxLength: 100
`
	var lib SchemaLibrary
	if err := yaml.Unmarshal([]byte(src), &lib); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(lib)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "Probe") {
		t.Errorf("emission missing Probe; got %s", string(out))
	}
	if !strings.Contains(string(out), "SizedString") {
		t.Errorf("emission missing SizedString; got %s", string(out))
	}
}

// TestOrderedMap_Delete_AllPaths pins the Delete contract: nil map,
// non-existent key, existing key (mid-position), existing key (last
// position).
func TestOrderedMap_Delete_AllPaths(t *testing.T) {
	t.Parallel()
	// Nil map.
	var nilMap *OrderedMap[string]
	if got := nilMap.Delete("x"); got {
		t.Errorf("nil.Delete = true; want false")
	}
	// Non-existent key.
	var m OrderedMap[string]
	m.Set("a", "1")
	if got := m.Delete("missing"); got {
		t.Errorf("missing.Delete = true; want false")
	}
	// Mid-position delete.
	m.Set("b", "2")
	m.Set("c", "3")
	if !m.Delete("b") {
		t.Errorf("Delete b should return true")
	}
	if got := m.Keys(); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("keys after delete = %v; want [a c]", got)
	}
	// Last-position delete.
	if !m.Delete("c") {
		t.Errorf("Delete c should return true")
	}
}
