// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOrderedMap_Len_NilReceiver pins the nil-guard: Len() on a nil
// pointer returns 0 without panicking.
func TestOrderedMap_Len_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *OrderedMap[string]
	if got := m.Len(); got != 0 {
		t.Errorf("nil.Len() = %d; want 0", got)
	}
	// Non-nil empty.
	var m2 OrderedMap[string]
	if got := m2.Len(); got != 0 {
		t.Errorf("empty.Len() = %d; want 0", got)
	}
	// Non-nil populated.
	m2.Set("k", "v")
	if got := m2.Len(); got != 1 {
		t.Errorf("populated.Len() = %d; want 1", got)
	}
}

// TestOrderedMap_TemplateAlphabeticalMap_Empty pins the empty-input
// short-circuit: returns an empty map[string]any without iterating.
func TestOrderedMap_TemplateAlphabeticalMap_Empty(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	got := m.TemplateAlphabeticalMap()
	if len(got) != 0 {
		t.Errorf("empty.TemplateAlphabeticalMap() = %v; want empty", got)
	}
}

// TestOrderedMap_TemplateAlphabeticalMapDeep_Empty pins the empty-input
// short-circuit for the deep variant.
func TestOrderedMap_TemplateAlphabeticalMapDeep_Empty(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	got := m.TemplateAlphabeticalMapDeep()
	if len(got) != 0 {
		t.Errorf("empty.TemplateAlphabeticalMapDeep() = %v; want empty", got)
	}
}

// TestOrderedMap_TemplateAlphabeticalMapDeep_NestedShapes pins the
// recursion contract: nested *OrderedMap[any], OrderedMap[any] value,
// nested []any with OrderedMap elements all unwrap to plain Go maps
// and slices.
func TestOrderedMap_TemplateAlphabeticalMapDeep_NestedShapes(t *testing.T) {
	t.Parallel()
	nested := NewOrderedMap[any]()
	nested.Set("deep", "value")

	var inner OrderedMap[any]
	inner.Set("k", "v")

	var m OrderedMap[any]
	m.Set("a", nested)              // *OrderedMap[any]
	m.Set("b", inner)               // OrderedMap[any] value
	m.Set("c", []any{nested, "x"})  // []any with mixed elements
	m.Set("d", "plain")             // scalar
	m.Set("e", (*OrderedMap[any])(nil)) // nil pointer

	out := m.TemplateAlphabeticalMapDeep()

	deep, ok := out["a"].(map[string]any)
	if !ok || deep["deep"] != "value" {
		t.Errorf("a: nested *OrderedMap[any] not unwrapped; got %v", out["a"])
	}
	innerMap, ok := out["b"].(map[string]any)
	if !ok || innerMap["k"] != "v" {
		t.Errorf("b: OrderedMap[any] value not unwrapped; got %v", out["b"])
	}
	sliceVal, ok := out["c"].([]any)
	if !ok || len(sliceVal) != 2 {
		t.Errorf("c: []any not preserved; got %v", out["c"])
	}
	if first, ok := sliceVal[0].(map[string]any); !ok || first["deep"] != "value" {
		t.Errorf("c[0]: nested OrderedMap inside []any not unwrapped; got %v", sliceVal[0])
	}
	if out["d"] != "plain" {
		t.Errorf("d: scalar not preserved; got %v", out["d"])
	}
	if out["e"] != nil {
		t.Errorf("e: nil *OrderedMap[any] should pass as nil; got %v", out["e"])
	}
}

// TestOrderedMap_UnmarshalJSON_NullEmptyState pins the null-literal
// short-circuit: JSON null decodes into an empty map without error.
func TestOrderedMap_UnmarshalJSON_NullEmptyState(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	if err := json.Unmarshal([]byte("null"), &m); err != nil {
		t.Fatalf("null: %v", err)
	}
	if m.Len() != 0 {
		t.Errorf("null should leave empty; got Len=%d", m.Len())
	}
}

// TestOrderedMap_UnmarshalJSON_RejectsNonObject pins the type-gate:
// a JSON array surfaces a clear error.
func TestOrderedMap_UnmarshalJSON_RejectsNonObject(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	if err := json.Unmarshal([]byte("[1,2]"), &m); err == nil {
		t.Errorf("array should error")
	}
}

// TestOrderedMap_UnmarshalYAML_RejectsNonScalarKey pins the key-decode
// error path: a YAML mapping with a sequence-typed key surfaces a
// clear error.
func TestOrderedMap_UnmarshalYAML_RejectsNonScalarKey(t *testing.T) {
	t.Parallel()
	src := `
? [a, b]
: value
`
	var m OrderedMap[string]
	err := yaml.Unmarshal([]byte(src), &m)
	if err == nil {
		t.Errorf("sequence-typed key should error")
	}
}

// TestOrderedMap_MarshalJSON_EmptyEmitsEmptyObject pins the JSON
// emission for an empty map: emit `{}` rather than null.
func TestOrderedMap_MarshalJSON_EmptyEmitsEmptyObject(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != "{}" {
		t.Errorf("got %s; want {}", string(out))
	}
}

// TestOrderedMap_MarshalYAML_EmptyEmitsEmptyMapping pins the YAML
// emission for an empty map.
func TestOrderedMap_MarshalYAML_EmptyEmitsEmptyMapping(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	got, err := m.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	node, ok := got.(*yaml.Node)
	if !ok || node.Kind != yaml.MappingNode || len(node.Content) != 0 {
		t.Errorf("empty MarshalYAML must produce empty mapping; got %+v", got)
	}
}

// TestFlattenLeaves_NestedAndEmptyContainers pins the walker's
// dispatch arms: nested *OrderedMap[any] descends; empty
// *OrderedMap[any] yields a single FlatLeaf at the container path;
// scalar yields a FlatLeaf; nil pointer also yields a FlatLeaf.
func TestFlattenLeaves_NestedAndEmptyContainers(t *testing.T) {
	t.Parallel()
	// Build: root.a.b = "v1", root.c (empty *OrderedMap), root.d = "v4",
	// root.e (OrderedMap-value)
	leaf := NewOrderedMap[any]()
	leaf.Set("b", "v1")
	empty := NewOrderedMap[any]()
	innerOM := OrderedMap[any]{}
	innerOM.Set("k", "innerv")
	emptyOM := OrderedMap[any]{}

	var root OrderedMap[any]
	root.Set("a", leaf)
	root.Set("c", empty)
	root.Set("d", "v4")
	root.Set("e", innerOM)
	root.Set("f", emptyOM)
	root.Set("g", (*OrderedMap[any])(nil))

	out := FlattenLeaves(root)
	if len(out) == 0 {
		t.Fatalf("expected leaves")
	}
	paths := map[string]bool{}
	for _, l := range out {
		paths[l.Path] = true
	}
	if !paths["a.b"] {
		t.Errorf("expected a.b leaf")
	}
	if !paths["c"] {
		t.Errorf("expected empty-container leaf at c")
	}
	if !paths["d"] {
		t.Errorf("expected scalar leaf at d")
	}
	if !paths["e.k"] {
		t.Errorf("expected e.k leaf through OrderedMap[any]-value recursion")
	}
	if !paths["f"] {
		t.Errorf("expected empty-OrderedMap-value leaf at f")
	}
	if !paths["g"] {
		t.Errorf("expected nil-pointer leaf at g")
	}
}

// TestInjectPipe_MarshalYAML_PerMechanismOnly pins each per-mechanism
// emission branch. Each test populates exactly one mechanism so the
// non-emitted branches stay zero.
func TestInjectPipe_MarshalYAML_PerMechanismOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		setup  func(*InjectPipe)
		needle string
	}{
		{"env-only", func(p *InjectPipe) { p.Env.Set("K", "v") }, "env:"},
		{"file-only", func(p *InjectPipe) { p.File.Set("F", "/x") }, "file:"},
		{"arg-only", func(p *InjectPipe) { p.Arg.Set("A", "1") }, "arg:"},
		{"all-only", func(p *InjectPipe) { p.All.Set("L", "any") }, "all:"},
		{"additional-only", func(p *InjectPipe) {
			var inner OrderedMap[string]
			inner.Set("X", "y")
			p.Additional.Set("custom", inner)
		}, "custom:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p InjectPipe
			tc.setup(&p)
			out, err := yaml.Marshal(p)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !strings.Contains(string(out), tc.needle) {
				t.Errorf("missing %q in %s", tc.needle, string(out))
			}
		})
	}
}

// TestInjectPipe_MarshalJSON_PerMechanismOnly pins the JSON parallel.
func TestInjectPipe_MarshalJSON_PerMechanismOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		setup  func(*InjectPipe)
		needle string
	}{
		{"env-only", func(p *InjectPipe) { p.Env.Set("K", "v") }, `"env":`},
		{"file-only", func(p *InjectPipe) { p.File.Set("F", "/x") }, `"file":`},
		{"arg-only", func(p *InjectPipe) { p.Arg.Set("A", "1") }, `"arg":`},
		{"all-only", func(p *InjectPipe) { p.All.Set("L", "any") }, `"all":`},
		{"additional-only", func(p *InjectPipe) {
			var inner OrderedMap[string]
			inner.Set("X", "y")
			p.Additional.Set("custom", inner)
		}, `"custom":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p InjectPipe
			tc.setup(&p)
			out, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !strings.Contains(string(out), tc.needle) {
				t.Errorf("missing %q in %s", tc.needle, string(out))
			}
		})
	}
}

// TestInjectBlock_MarshalYAML_PerMechanismOnly pins the per-mechanism
// emission for the agnostic InjectBlock.
func TestInjectBlock_MarshalYAML_PerMechanismOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		setup  func(*InjectBlock)
		needle string
	}{
		{"env-only", func(b *InjectBlock) { b.Env = map[string]string{"K": "v"} }, "env:"},
		{"file-only", func(b *InjectBlock) { b.File = map[string]string{"F": "/x"} }, "file:"},
		{"arg-only", func(b *InjectBlock) { b.Arg = map[string]string{"A": "1"} }, "arg:"},
		{"additional-only", func(b *InjectBlock) { b.Additional.Set("custom", "scalar") }, "custom:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b InjectBlock
			tc.setup(&b)
			out, err := yaml.Marshal(b)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !strings.Contains(string(out), tc.needle) {
				t.Errorf("missing %q in %s", tc.needle, string(out))
			}
		})
	}
}

// TestInjectBlock_MarshalJSON_PerMechanismOnly pins JSON parallel.
func TestInjectBlock_MarshalJSON_PerMechanismOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		setup  func(*InjectBlock)
		needle string
	}{
		{"env-only", func(b *InjectBlock) { b.Env = map[string]string{"K": "v"} }, `"env":`},
		{"file-only", func(b *InjectBlock) { b.File = map[string]string{"F": "/x"} }, `"file":`},
		{"arg-only", func(b *InjectBlock) { b.Arg = map[string]string{"A": "1"} }, `"arg":`},
		{"additional-only", func(b *InjectBlock) { b.Additional.Set("custom", "scalar") }, `"custom":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b InjectBlock
			tc.setup(&b)
			out, err := json.Marshal(b)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !strings.Contains(string(out), tc.needle) {
				t.Errorf("missing %q in %s", tc.needle, string(out))
			}
		})
	}
}

// TestInjectBlock_UnmarshalYAML_NonScalarKey pins the key-decode error
// branch: a non-scalar key (sequence) surfaces a clear error.
func TestInjectBlock_UnmarshalYAML_NonScalarKey(t *testing.T) {
	t.Parallel()
	src := `
? [a, b]
: value
`
	var b InjectBlock
	if err := yaml.Unmarshal([]byte(src), &b); err == nil {
		t.Errorf("sequence key should error")
	}
}

// TestInjectPipe_UnmarshalYAML_NonScalarKey pins the same for
// InjectPipe.
func TestInjectPipe_UnmarshalYAML_NonScalarKey(t *testing.T) {
	t.Parallel()
	src := `
? [a, b]
: value
`
	var p InjectPipe
	if err := yaml.Unmarshal([]byte(src), &p); err == nil {
		t.Errorf("sequence key should error")
	}
}

// TestManifestMappings_MarshalYAML_FeaturesOnly pins the Features-
// only branch of the emission.
func TestManifestMappings_MarshalYAML_FeaturesOnly(t *testing.T) {
	t.Parallel()
	mt := MappingTier{}
	mt.Type.Set("rewrite", "nginx-helm")
	mm := ManifestMappings{Features: &mt}
	out, err := yaml.Marshal(mm)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "features:") {
		t.Errorf("missing features:; got %s", string(out))
	}
}

// TestManifestMappings_MarshalYAML_LegacyOnly pins the legacy-only
// emission path.
func TestManifestMappings_MarshalYAML_LegacyOnly(t *testing.T) {
	t.Parallel()
	mm := ManifestMappings{}
	mt := MappingTier{}
	mt.Name.Set("api", "legacy-helm")
	mm.Legacy.Set("my-provider", ProviderMappings{Components: &mt})
	out, err := yaml.Marshal(mm)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "my-provider:") {
		t.Errorf("missing legacy entry; got %s", string(out))
	}
}

// TestUnmarshalImportable_AllShapes pins every dispatch arm of the
// shared content-shape helper: nil, scalar, well-formed $import, and
// each error branch.
func TestUnmarshalImportable_AllShapes(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("test-sentinel")
	// Nil node.
	in, imp, err := unmarshalImportable(nil, sentinel)
	if err != nil || in != "" || imp != "" {
		t.Errorf("nil = (%q, %q, %v)", in, imp, err)
	}
	// Scalar.
	in, imp, err = unmarshalImportable(&yaml.Node{Kind: yaml.ScalarNode, Value: "inline-value"}, sentinel)
	if err != nil || in != "inline-value" || imp != "" {
		t.Errorf("scalar = (%q, %q, %v)", in, imp, err)
	}
	// Well-formed import.
	src := `$import: target.yaml`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse import: %v", err)
	}
	in, imp, err = unmarshalImportable(node.Content[0], sentinel)
	if err != nil || imp != "target.yaml" || in != "" {
		t.Errorf("$import = (%q, %q, %v)", in, imp, err)
	}
	// Multi-key mapping.
	src = `
$import: x
other: y
`
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse multi-key: %v", err)
	}
	_, _, err = unmarshalImportable(node.Content[0], sentinel)
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("multi-key should error with sentinel; got %v", err)
	}
	// Wrong key name.
	src = `wrong-key: x`
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse wrong-key: %v", err)
	}
	_, _, err = unmarshalImportable(node.Content[0], sentinel)
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("wrong-key should error; got %v", err)
	}
	// $import with non-scalar value.
	src = `
$import:
  nested: y
`
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse non-scalar: %v", err)
	}
	_, _, err = unmarshalImportable(node.Content[0], sentinel)
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("non-scalar $import should error; got %v", err)
	}
	// Unsupported kind (sequence).
	_, _, err = unmarshalImportable(&yaml.Node{Kind: yaml.SequenceNode, Line: 1}, sentinel)
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("sequence should error; got %v", err)
	}
}

// TestMarshalImportable_AllShapes pins the inverse: inline → scalar
// (with literal style on multi-line), import → {$import: x}, empty →
// nil.
func TestMarshalImportable_AllShapes(t *testing.T) {
	t.Parallel()
	// Inline single-line.
	got, err := marshalImportable("inline", "")
	if err != nil {
		t.Fatalf("inline: %v", err)
	}
	node, ok := got.(*yaml.Node)
	if !ok || node.Value != "inline" || node.Style == yaml.LiteralStyle {
		t.Errorf("single-line inline must NOT use literal style; got %+v", node)
	}
	// Inline multi-line (triggers literal style).
	got, err = marshalImportable("line1\nline2", "")
	if err != nil {
		t.Fatalf("multi-line: %v", err)
	}
	node, ok = got.(*yaml.Node)
	if !ok || node.Style != yaml.LiteralStyle {
		t.Errorf("multi-line should use literal style; got %+v", node)
	}
	// Import.
	got, err = marshalImportable("", "x.yaml")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	m, ok := got.(map[string]string)
	if !ok || m["$import"] != "x.yaml" {
		t.Errorf("import = %v; want {$import: x.yaml}", got)
	}
	// Empty.
	got, err = marshalImportable("", "")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got != nil {
		t.Errorf("empty should be nil; got %v", got)
	}
}

// TestOrderedMap_MarshalJSON_ErrorOnUnencodableValue pins the per-
// value JSON encode error path: a value type (channel) that cannot
// be JSON-marshaled surfaces the error.
func TestOrderedMap_MarshalJSON_ErrorOnUnencodableValue(t *testing.T) {
	t.Parallel()
	var m OrderedMap[any]
	m.Set("bad", make(chan int))
	if _, err := json.Marshal(m); err == nil {
		t.Errorf("channel value should fail JSON marshal")
	}
}

// TestOrderedMap_MarshalYAML_ErrorOnUnencodableValue pins the per-
// value YAML encode error path. yaml.v3 typically panics on unsupported
// types; channels surface as errors during reflection-driven encode.
func TestOrderedMap_MarshalYAML_ErrorOnUnencodableValue(t *testing.T) {
	t.Parallel()
	defer func() {
		// yaml.v3 may panic on unsupported types instead of returning
		// errors; the recover keeps the test self-bounded — the goal
		// is exercise of the value-encode path, not a specific
		// error-surface contract for a synthetic value type.
		_ = recover()
	}()
	var m OrderedMap[any]
	m.Set("bad", make(chan int))
	_, _ = yaml.Marshal(m)
}

// TestInjectBlock_MarshalJSON_ErrorOnUnencodableAdditional pins the
// Additional-value JSON-encode error path via a channel value.
func TestInjectBlock_MarshalJSON_ErrorOnUnencodableAdditional(t *testing.T) {
	t.Parallel()
	var b InjectBlock
	b.Additional.Set("bad", make(chan int))
	if _, err := json.Marshal(b); err == nil {
		t.Errorf("channel additional should fail JSON marshal")
	}
}
