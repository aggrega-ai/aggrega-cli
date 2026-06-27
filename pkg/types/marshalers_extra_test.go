// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestInjectPipe_UnmarshalJSON_AllMechanisms pins the JSON-decode
// dispatch for every documented mechanism plus a custom additional
// entry. Author-order observation requires the streaming Token() path.
func TestInjectPipe_UnmarshalJSON_AllMechanisms(t *testing.T) {
	t.Parallel()
	src := `{
		"env":  {"K1": "v1"},
		"file": {"F1": "/tmp/x"},
		"arg":  {"A1": "1"},
		"all":  {"L1": "everywhere"},
		"custom-key": {"X1": "y"}
	}`
	var p InjectPipe
	if err := json.Unmarshal([]byte(src), &p); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if v, _ := p.Env.Get("K1"); v != "v1" {
		t.Errorf("Env.K1 = %q; want v1", v)
	}
	if v, _ := p.File.Get("F1"); v != "/tmp/x" {
		t.Errorf("File.F1 = %q; want /tmp/x", v)
	}
	if v, _ := p.Arg.Get("A1"); v != "1" {
		t.Errorf("Arg.A1 = %q; want 1", v)
	}
	if v, _ := p.All.Get("L1"); v != "everywhere" {
		t.Errorf("All.L1 = %q; want everywhere", v)
	}
	add, ok := p.Additional.Get("custom-key")
	if !ok {
		t.Fatalf("Additional.custom-key missing")
	}
	if v, _ := add.Get("X1"); v != "y" {
		t.Errorf("Additional.custom-key.X1 = %q; want y", v)
	}
}

// TestInjectPipe_UnmarshalJSON_RejectsDuplicateKey pins the duplicate-
// key guard on the JSON path.
func TestInjectPipe_UnmarshalJSON_RejectsDuplicateKey(t *testing.T) {
	t.Parallel()
	src := `{"env": {"A": "1"}, "env": {"B": "2"}}`
	var p InjectPipe
	err := json.Unmarshal([]byte(src), &p)
	if err == nil {
		t.Fatalf("expected duplicate-key error; got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error must mention duplicate; got %v", err)
	}
}

// TestInjectPipe_UnmarshalJSON_RejectsNonObject pins the type-gate on
// the JSON path: a bare array surfaces a clear error.
func TestInjectPipe_UnmarshalJSON_RejectsNonObject(t *testing.T) {
	t.Parallel()
	var p InjectPipe
	err := json.Unmarshal([]byte(`[1,2,3]`), &p)
	if err == nil {
		t.Fatalf("expected error; got nil")
	}
}

// TestInjectPipe_MarshalYAML_EmitsAuthorOrder pins the emission order:
// env / file / arg / all in struct-field order, then Additional in
// author order from the OrderedMap.
func TestInjectPipe_MarshalYAML_EmitsAuthorOrder(t *testing.T) {
	t.Parallel()
	src := `
env:
  E1: v1
file:
  F1: /tmp/x
arg:
  A1: "1"
all:
  L1: any
zzz-custom:
  Z1: "z"
aaa-custom:
  A2: "a"
`
	var p InjectPipe
	if err := yaml.Unmarshal([]byte(src), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	// env / file / arg / all appear in that struct order first.
	idxEnv := strings.Index(got, "env:")
	idxFile := strings.Index(got, "file:")
	idxArg := strings.Index(got, "arg:")
	idxAll := strings.Index(got, "all:")
	idxZzz := strings.Index(got, "zzz-custom:")
	idxAaa := strings.Index(got, "aaa-custom:")
	if !(idxEnv < idxFile && idxFile < idxArg && idxArg < idxAll) {
		t.Errorf("struct-field order violated; got:\n%s", got)
	}
	// Additional entries preserve author order: zzz before aaa.
	if !(idxAll < idxZzz && idxZzz < idxAaa) {
		t.Errorf("Additional author-order violated; got:\n%s", got)
	}
}

// TestInjectPipe_MarshalJSON_EmitsAuthorOrder pins the JSON emission
// order: same as MarshalYAML.
func TestInjectPipe_MarshalJSON_EmitsAuthorOrder(t *testing.T) {
	t.Parallel()
	src := `{
		"env":  {"E1": "v1"},
		"file": {"F1": "/tmp"},
		"arg":  {"A1": "1"},
		"all":  {"L1": "any"},
		"zzz":  {"Z1": "z"},
		"aaa":  {"A2": "a"}
	}`
	var p InjectPipe
	if err := json.Unmarshal([]byte(src), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	idxEnv := strings.Index(got, `"env":`)
	idxFile := strings.Index(got, `"file":`)
	idxArg := strings.Index(got, `"arg":`)
	idxAll := strings.Index(got, `"all":`)
	idxZzz := strings.Index(got, `"zzz":`)
	idxAaa := strings.Index(got, `"aaa":`)
	if !(idxEnv < idxFile && idxFile < idxArg && idxArg < idxAll && idxAll < idxZzz && idxZzz < idxAaa) {
		t.Errorf("emission order violated; got:\n%s", got)
	}
}

// TestInjectBlock_UnmarshalYAML_EveryMechanism pins the YAML-decode
// dispatch for InjectBlock's three core mechanisms plus an Additional
// entry. Mirrors InjectPipe's contract on the agnostic InjectBlock.
func TestInjectBlock_UnmarshalYAML_EveryMechanism(t *testing.T) {
	t.Parallel()
	src := `
env:
  K1: v1
file:
  F1: /tmp/x
arg:
  A1: "1"
custom-key:
  inner: ok
`
	var b InjectBlock
	if err := yaml.Unmarshal([]byte(src), &b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if b.Env == nil {
		t.Errorf("Env not populated")
	}
	if b.File == nil {
		t.Errorf("File not populated")
	}
	if b.Arg == nil {
		t.Errorf("Arg not populated")
	}
	if _, ok := b.Additional.Get("custom-key"); !ok {
		t.Errorf("Additional.custom-key not populated")
	}
}

// TestInjectBlock_UnmarshalYAML_RejectsNonMapping pins the type-gate:
// a sequence node surfaces a clear error rather than silently no-op.
func TestInjectBlock_UnmarshalYAML_RejectsNonMapping(t *testing.T) {
	t.Parallel()
	var b InjectBlock
	err := yaml.Unmarshal([]byte(`- 1\n- 2\n`), &b)
	if err == nil {
		t.Fatalf("expected error; got nil")
	}
}

// TestInjectBlock_UnmarshalYAML_RejectsDuplicateKey pins the
// duplicate-key guard on the YAML path.
func TestInjectBlock_UnmarshalYAML_RejectsDuplicateKey(t *testing.T) {
	t.Parallel()
	src := `
env:
  K1: v1
env:
  K2: v2
`
	var b InjectBlock
	err := yaml.Unmarshal([]byte(src), &b)
	if err == nil {
		t.Fatalf("expected duplicate-key error; got nil")
	}
}

// TestInjectBlock_UnmarshalJSON_AllMechanisms pins the JSON-decode
// dispatch including a custom additional entry.
func TestInjectBlock_UnmarshalJSON_AllMechanisms(t *testing.T) {
	t.Parallel()
	src := `{
		"env":  {"E1": "v1"},
		"file": {"F1": "/tmp/x"},
		"arg":  {"A1": "1"},
		"custom-x": "scalar-value"
	}`
	var b InjectBlock
	if err := json.Unmarshal([]byte(src), &b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if b.Env == nil || b.File == nil || b.Arg == nil {
		t.Errorf("core fields not populated; got %+v", b)
	}
	if _, ok := b.Additional.Get("custom-x"); !ok {
		t.Errorf("Additional.custom-x not populated")
	}
}

// TestInjectBlock_UnmarshalJSON_RejectsDuplicateKey pins the
// duplicate-key guard on the JSON path.
func TestInjectBlock_UnmarshalJSON_RejectsDuplicateKey(t *testing.T) {
	t.Parallel()
	src := `{"file": {"F": "/a"}, "file": {"G": "/b"}}`
	var b InjectBlock
	if err := json.Unmarshal([]byte(src), &b); err == nil {
		t.Fatalf("expected duplicate-key error; got nil")
	}
}

// TestInjectBlock_UnmarshalJSON_NullReturnsZero pins the null short-
// circuit: a JSON null input leaves the receiver in its zero state.
func TestInjectBlock_UnmarshalJSON_NullReturnsZero(t *testing.T) {
	t.Parallel()
	var b InjectBlock
	if err := json.Unmarshal([]byte("null"), &b); err != nil {
		t.Fatalf("Unmarshal null: %v", err)
	}
	if b.Env != nil || b.File != nil || b.Arg != nil || b.Additional.Len() != 0 {
		t.Errorf("null should leave InjectBlock zero; got %+v", b)
	}
}

// TestInjectBlock_UnmarshalJSON_RejectsNonObject pins the type-gate
// for an unexpected JSON value (array or scalar).
func TestInjectBlock_UnmarshalJSON_RejectsNonObject(t *testing.T) {
	t.Parallel()
	var b InjectBlock
	if err := json.Unmarshal([]byte("[1,2]"), &b); err == nil {
		t.Fatalf("array should error; got nil")
	}
}

// TestInjectBlock_MarshalYAML_EmitsAuthorOrder pins the emission
// discipline: env / file / arg first, then Additional in author order.
func TestInjectBlock_MarshalYAML_EmitsAuthorOrder(t *testing.T) {
	t.Parallel()
	var b InjectBlock
	b.Env = map[string]string{"K1": "v1"}
	b.File = map[string]string{"F1": "/tmp"}
	b.Arg = map[string]string{"A1": "1"}
	b.Additional.Set("zzz", "z")
	b.Additional.Set("aaa", "a")
	out, err := yaml.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	idxEnv := strings.Index(got, "env:")
	idxFile := strings.Index(got, "file:")
	idxArg := strings.Index(got, "arg:")
	idxZzz := strings.Index(got, "zzz:")
	idxAaa := strings.Index(got, "aaa:")
	if !(idxEnv < idxFile && idxFile < idxArg) {
		t.Errorf("struct-field order violated; got:\n%s", got)
	}
	if !(idxArg < idxZzz && idxZzz < idxAaa) {
		t.Errorf("Additional author-order violated; got:\n%s", got)
	}
}

// TestInjectBlock_MarshalJSON_EmitsAuthorOrder pins JSON emission
// order: env / file / arg, then Additional in author order.
func TestInjectBlock_MarshalJSON_EmitsAuthorOrder(t *testing.T) {
	t.Parallel()
	var b InjectBlock
	b.Env = map[string]string{"K1": "v1"}
	b.File = map[string]string{"F1": "/tmp"}
	b.Arg = map[string]string{"A1": "1"}
	b.Additional.Set("zzz", "z")
	b.Additional.Set("aaa", "a")
	out, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	idxEnv := strings.Index(got, `"env":`)
	idxFile := strings.Index(got, `"file":`)
	idxArg := strings.Index(got, `"arg":`)
	idxZzz := strings.Index(got, `"zzz":`)
	idxAaa := strings.Index(got, `"aaa":`)
	if !(idxEnv < idxFile && idxFile < idxArg && idxArg < idxZzz && idxZzz < idxAaa) {
		t.Errorf("emission order violated; got:\n%s", got)
	}
}

// TestUsesSocketValue_MarshalYAML_EveryShape pins the three emission
// shapes: compact (string slice) → YAML sequence; verbose (non-empty
// OrderedMap) → mapping; both empty → null.
func TestUsesSocketValue_MarshalYAML_EveryShape(t *testing.T) {
	t.Parallel()
	// Compact form.
	v := UsesSocketValue{Compact: []string{"a", "b"}}
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal compact: %v", err)
	}
	if !strings.Contains(string(out), "- a") {
		t.Errorf("compact emission missing sequence; got %s", string(out))
	}
	// Verbose form.
	var verbose OrderedMap[VerboseUsesEntry]
	verbose.Set("alias", VerboseUsesEntry{Target: "dest"})
	v = UsesSocketValue{Verbose: verbose}
	out, err = yaml.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal verbose: %v", err)
	}
	if !strings.Contains(string(out), "alias:") {
		t.Errorf("verbose emission missing alias; got %s", string(out))
	}
	// Empty.
	v = UsesSocketValue{}
	out, err = yaml.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	if strings.TrimSpace(string(out)) != "null" {
		t.Errorf("empty should emit null; got %s", string(out))
	}
}

// TestUsesSocketValue_MarshalJSON_EveryShape pins the JSON emission:
// compact → array, verbose → object, empty → null literal.
func TestUsesSocketValue_MarshalJSON_EveryShape(t *testing.T) {
	t.Parallel()
	v := UsesSocketValue{Compact: []string{"a", "b"}}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal compact: %v", err)
	}
	if string(out) != `["a","b"]` {
		t.Errorf("compact emission = %s; want [\"a\",\"b\"]", string(out))
	}
	var verbose OrderedMap[VerboseUsesEntry]
	verbose.Set("alias", VerboseUsesEntry{Target: "dest"})
	v = UsesSocketValue{Verbose: verbose}
	out, err = json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal verbose: %v", err)
	}
	if !strings.Contains(string(out), `"alias"`) {
		t.Errorf("verbose missing alias; got %s", string(out))
	}
	v = UsesSocketValue{}
	out, err = json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	if string(out) != "null" {
		t.Errorf("empty JSON = %s; want null", string(out))
	}
}

// TestUsesSocketValue_UnmarshalJSON_RejectsEmptyAndInvalid pins the
// error branches of the JSON dispatch: empty input, leading character
// outside the documented set.
func TestUsesSocketValue_UnmarshalJSON_RejectsEmptyAndInvalid(t *testing.T) {
	t.Parallel()
	var v UsesSocketValue
	if err := v.UnmarshalJSON([]byte("")); err == nil {
		t.Errorf("empty input should error")
	}
	if err := v.UnmarshalJSON([]byte("   ")); err == nil {
		t.Errorf("whitespace-only should error")
	}
	if err := v.UnmarshalJSON([]byte("42")); err == nil {
		t.Errorf("integer leading byte should error")
	}
	if err := v.UnmarshalJSON([]byte(`"bad"`)); err == nil {
		t.Errorf("string leading byte should error")
	}
	// 'n' that is not "null"
	if err := v.UnmarshalJSON([]byte("nope")); err == nil {
		t.Errorf("non-null n-prefix should error")
	}
}

// TestVerboseUsesEntry_UnmarshalJSON_AllDispatches pins the JSON
// dispatch for VerboseUsesEntry: string scalar → rejected (single-form
// alias contract); object → struct form; empty / other → error.
func TestVerboseUsesEntry_UnmarshalJSON_AllDispatches(t *testing.T) {
	t.Parallel()
	// Bare-string target: rejected.
	var e VerboseUsesEntry
	if err := e.UnmarshalJSON([]byte(`"target-x"`)); err == nil {
		t.Fatal("string target must be rejected (single-form alias contract)")
	}
	// Object form.
	var e2 VerboseUsesEntry
	if err := e2.UnmarshalJSON([]byte(`{"target": "t", "inject": {"env": {"K": "v"}}}`)); err != nil {
		t.Fatalf("object: %v", err)
	}
	if e2.Target != "t" {
		t.Errorf("Target = %q; want t", e2.Target)
	}
	if e2.Inject == nil {
		t.Errorf("Inject not populated")
	}
	// Empty.
	var e3 VerboseUsesEntry
	if err := e3.UnmarshalJSON([]byte("")); err == nil {
		t.Errorf("empty should error")
	}
	// Other leading byte.
	var e4 VerboseUsesEntry
	if err := e4.UnmarshalJSON([]byte("42")); err == nil {
		t.Errorf("integer should error")
	}
	// Long preview.
	var e5 VerboseUsesEntry
	if err := e5.UnmarshalJSON([]byte(`xxxxxxxxxxxxxxxxxxxxxxxxxxxxx`)); err == nil {
		t.Errorf("garbage should error")
	}
}

// TestVerboseUsesEntry_UnmarshalYAML_RejectsBadKind pins the kind-
// rejection branch: a sequence node surfaces a clear error.
func TestVerboseUsesEntry_UnmarshalYAML_RejectsBadKind(t *testing.T) {
	t.Parallel()
	var e VerboseUsesEntry
	node := &yaml.Node{Kind: yaml.SequenceNode, Line: 7}
	if err := e.UnmarshalYAML(node); err == nil {
		t.Errorf("sequence kind should error")
	}
}

// TestVerboseUsesEntry_MarshalShape pins the canonical-form emission:
// MarshalYAML and MarshalJSON always emit the map shape regardless of
// the authoring shorthand used at unmarshal.
func TestVerboseUsesEntry_MarshalShape(t *testing.T) {
	t.Parallel()
	e := VerboseUsesEntry{Target: "x"}
	out, err := yaml.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(out), "target: x") {
		t.Errorf("YAML missing target; got %s", string(out))
	}
	jb, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if !strings.Contains(string(jb), `"target":"x"`) {
		t.Errorf("JSON missing target; got %s", string(jb))
	}
}

// TestVerboseUsesEntry_IsZero_Cases pins the IsZero predicate.
func TestVerboseUsesEntry_IsZero_Cases(t *testing.T) {
	t.Parallel()
	if !(VerboseUsesEntry{}).IsZero() {
		t.Errorf("empty is zero")
	}
	if (VerboseUsesEntry{Target: "x"}).IsZero() {
		t.Errorf("Target set is not zero")
	}
	if (VerboseUsesEntry{Inject: &InjectBlock{}}).IsZero() {
		t.Errorf("Inject set is not zero")
	}
}

// TestAnnotationValue_UnmarshalYAML_RejectsSequence pins the explicit-
// rejection branch for a sequence-typed authoring shape.
func TestAnnotationValue_UnmarshalYAML_RejectsSequence(t *testing.T) {
	t.Parallel()
	src := `[1, 2, 3]`
	var av AnnotationValue
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Unwrap the document node to get the sequence node directly.
	seq := node.Content[0]
	err := av.UnmarshalYAML(seq)
	if err == nil {
		t.Fatalf("expected ANNOTATION_PARSE_ERROR; got nil")
	}
	if !strings.Contains(err.Error(), "ANNOTATION_PARSE_ERROR") {
		t.Errorf("error must name ANNOTATION_PARSE_ERROR; got %v", err)
	}
}

// TestAnnotationValue_UnmarshalYAML_RejectsUnsupportedKind pins the
// default branch for unsupported YAML node kinds.
func TestAnnotationValue_UnmarshalYAML_RejectsUnsupportedKind(t *testing.T) {
	t.Parallel()
	// Alias node (Kind = AliasNode) is uncommon but valid as a "default"
	// case for the dispatch.
	var av AnnotationValue
	node := &yaml.Node{Kind: yaml.AliasNode, Line: 3}
	err := av.UnmarshalYAML(node)
	if err == nil {
		t.Fatalf("expected unsupported-kind error; got nil")
	}
}

// TestAnnotationValue_UnmarshalYAML_NilNode pins the nil-node early
// return: a nil yaml.Node leaves the receiver untouched without error.
func TestAnnotationValue_UnmarshalYAML_NilNode(t *testing.T) {
	t.Parallel()
	var av AnnotationValue
	if err := av.UnmarshalYAML(nil); err != nil {
		t.Errorf("nil node should not error; got %v", err)
	}
	if av.Scalar != nil || av.Structured.Len() != 0 {
		t.Errorf("nil node should leave receiver zero; got %+v", av)
	}
}

// TestAnnotationValue_MarshalYAML_EveryBranch pins emission for the
// three populated states + the empty case.
func TestAnnotationValue_MarshalYAML_EveryBranch(t *testing.T) {
	t.Parallel()
	s := "hello"
	av := AnnotationValue{Scalar: &s}
	out, err := yaml.Marshal(av)
	if err != nil {
		t.Fatalf("scalar marshal: %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("scalar emission missing value; got %s", string(out))
	}
	// Structured.
	var st OrderedMap[any]
	st.Set("k", "v")
	av = AnnotationValue{Structured: st}
	out, err = yaml.Marshal(av)
	if err != nil {
		t.Fatalf("structured marshal: %v", err)
	}
	if !strings.Contains(string(out), "k: v") {
		t.Errorf("structured emission missing key; got %s", string(out))
	}
	// Empty.
	av = AnnotationValue{}
	out, err = yaml.Marshal(av)
	if err != nil {
		t.Fatalf("empty marshal: %v", err)
	}
	if strings.TrimSpace(string(out)) != "null" {
		t.Errorf("empty should emit null; got %s", string(out))
	}
}

// TestIsYAMLNull_EveryBranch pins each null-detection arm: nil node,
// explicit tag, "null" / "~" sentinels, untagged empty, non-null
// scalar.
func TestIsYAMLNull_EveryBranch(t *testing.T) {
	t.Parallel()
	if !isYAMLNull(nil) {
		t.Errorf("nil node should be null")
	}
	if !isYAMLNull(&yaml.Node{Tag: "!!null"}) {
		t.Errorf("!!null tag should be null")
	}
	if !isYAMLNull(&yaml.Node{Value: "null"}) {
		t.Errorf("null sentinel should be null")
	}
	if !isYAMLNull(&yaml.Node{Value: "~"}) {
		t.Errorf("~ sentinel should be null")
	}
	if !isYAMLNull(&yaml.Node{Value: "", Style: 0}) {
		t.Errorf("untagged empty should be null")
	}
	if isYAMLNull(&yaml.Node{Value: "", Style: yaml.DoubleQuotedStyle}) {
		t.Errorf("quoted empty must NOT be null")
	}
	if isYAMLNull(&yaml.Node{Value: "x"}) {
		t.Errorf("scalar 'x' must NOT be null")
	}
}

// TestParametersBlock_UnmarshalYAML_EveryShape pins inline, $import,
// and the mixed-error / malformed-$import paths.
func TestParametersBlock_UnmarshalYAML_EveryShape(t *testing.T) {
	t.Parallel()
	// Inline mode.
	src := `
a: 1
b: two
`
	var p ParametersBlock
	if err := yaml.Unmarshal([]byte(src), &p); err != nil {
		t.Fatalf("inline: %v", err)
	}
	if p.Inline.Len() != 2 {
		t.Errorf("Inline.Len = %d; want 2", p.Inline.Len())
	}

	// Import mode.
	src = `$import: ../params/base.yaml`
	var p2 ParametersBlock
	if err := yaml.Unmarshal([]byte(src), &p2); err != nil {
		t.Fatalf("import: %v", err)
	}
	if p2.Import != "../params/base.yaml" {
		t.Errorf("Import = %q; want ../params/base.yaml", p2.Import)
	}

	// $import with sibling key — rejected.
	src = `
$import: x.yaml
sibling: 1
`
	var p3 ParametersBlock
	err := yaml.Unmarshal([]byte(src), &p3)
	if err == nil || !errors.Is(err, ErrParametersShape) {
		t.Errorf("mixed $import/sibling should error ErrParametersShape; got %v", err)
	}

	// $import with non-scalar value — rejected.
	src = `
$import:
  nested: x
`
	var p4 ParametersBlock
	err = yaml.Unmarshal([]byte(src), &p4)
	if err == nil {
		t.Errorf("non-scalar $import should error")
	}

	// Empty / nil node.
	var p5 ParametersBlock
	if err := p5.UnmarshalYAML(nil); err != nil {
		t.Errorf("nil node should not error; got %v", err)
	}

	// Non-mapping (sequence) — rejected.
	src = `- 1`
	var p6 ParametersBlock
	err = yaml.Unmarshal([]byte(src), &p6)
	if err == nil || !errors.Is(err, ErrParametersShape) {
		t.Errorf("sequence should error ErrParametersShape; got %v", err)
	}
}

// TestParametersBlock_MarshalYAML_EveryShape pins inline, $import,
// and empty emission cases.
func TestParametersBlock_MarshalYAML_EveryShape(t *testing.T) {
	t.Parallel()
	var inline OrderedMap[any]
	inline.Set("k", "v")
	p := ParametersBlock{Inline: inline}
	out, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("inline: %v", err)
	}
	if !strings.Contains(string(out), "k: v") {
		t.Errorf("inline emission missing k:v; got %s", string(out))
	}

	p = ParametersBlock{Import: "x.yaml"}
	out, err = yaml.Marshal(p)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(string(out), "$import: x.yaml") {
		t.Errorf("import emission missing $import:; got %s", string(out))
	}

	p = ParametersBlock{}
	out, err = yaml.Marshal(p)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if strings.TrimSpace(string(out)) != "null" {
		t.Errorf("empty should emit null; got %s", string(out))
	}
}

// TestParametersBlock_IsEmpty pins the predicate's truth table.
func TestParametersBlock_IsEmpty(t *testing.T) {
	t.Parallel()
	if !(ParametersBlock{}).IsEmpty() {
		t.Errorf("zero should be empty")
	}
	var inline OrderedMap[any]
	inline.Set("k", "v")
	if (ParametersBlock{Inline: inline}).IsEmpty() {
		t.Errorf("inline-set should not be empty")
	}
	if (ParametersBlock{Import: "x"}).IsEmpty() {
		t.Errorf("import-set should not be empty")
	}
}

// TestDecodeParameterContract_EveryMapEntryShape pins the dispatch in
// DecodeParameterContract for typed-form map entries: map[string]any,
// *OrderedMap[any], and value-form OrderedMap[any] all decode to a
// populated ParameterContract. Scalar entries are rejected (see
// TestDecodeParameterContract_RejectsShorthand in parameter_contract_test.go).
func TestDecodeParameterContract_EveryMapEntryShape(t *testing.T) {
	t.Parallel()

	// map[string]any typed entry.
	mEntry := map[string]any{
		"type":        "integer",
		"description": "port number",
		"min":         1,
		"max":         65535,
	}
	pc, err := DecodeParameterContract(mEntry)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if pc.Type != "integer" {
		t.Errorf("typed Type = %q; want integer", pc.Type)
	}
	if pc.Description != "port number" {
		t.Errorf("Description = %q; want 'port number'", pc.Description)
	}

	// *OrderedMap[any] typed entry.
	var om OrderedMap[any]
	om.Set("type", "string")
	om.Set("default", "abc")
	pc, err = DecodeParameterContract(&om)
	if err != nil {
		t.Fatalf("ordered-pointer: %v", err)
	}
	if pc.Type != "string" {
		t.Errorf("ordered-pointer Type = %q; want string", pc.Type)
	}

	// OrderedMap[any] value-form typed entry.
	pc, err = DecodeParameterContract(om)
	if err != nil {
		t.Fatalf("ordered-value: %v", err)
	}
	if pc.Type != "string" {
		t.Errorf("ordered-value Type = %q; want string", pc.Type)
	}
}

// TestSchemaEntry_UnmarshalYAML_NilNode pins the nil-node early return
// for SchemaEntry's yaml hook.
func TestSchemaEntry_UnmarshalYAML_NilNode(t *testing.T) {
	t.Parallel()
	var e SchemaEntry
	if err := e.UnmarshalYAML(nil); err != nil {
		t.Errorf("nil node should not error; got %v", err)
	}
	if e.Content.Kind != 0 {
		t.Errorf("Content should stay zero; got Kind=%v", e.Content.Kind)
	}
}

// TestSchemaEntry_MarshalYAML_ZeroKindEmitsNil pins the zero-Kind
// short-circuit: an unpopulated SchemaEntry marshals to nil rather
// than producing an empty-mapping node.
func TestSchemaEntry_MarshalYAML_ZeroKindEmitsNil(t *testing.T) {
	t.Parallel()
	e := SchemaEntry{}
	out, err := e.MarshalYAML()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if out != nil {
		t.Errorf("zero-Kind should marshal nil; got %v", out)
	}
}

// TestSchemaEntry_RoundTrip pins the captured-node fidelity: unmarshal
// captures the raw node; marshal re-emits it.
func TestSchemaEntry_RoundTrip(t *testing.T) {
	t.Parallel()
	src := `
type: object
properties:
  name:
    type: string
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var e SchemaEntry
	if err := e.UnmarshalYAML(node.Content[0]); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Content.Kind == 0 {
		t.Errorf("Content not captured")
	}
	out, err := yaml.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "type: object") {
		t.Errorf("emission missing type: object; got %s", string(out))
	}
}

// TestTemplateEntry_UnmarshalYAML_SplitsParamsAndBody pins the split:
// $params lifts into the typed Params field; everything else lands in
// the Body node.
func TestTemplateEntry_UnmarshalYAML_SplitsParamsAndBody(t *testing.T) {
	t.Parallel()
	src := `
$params:
  size:
    type: integer
    default: 10
type: object
properties:
  port:
    type: integer
`
	var te TemplateEntry
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := te.UnmarshalYAML(node.Content[0]); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if te.Params.Len() != 1 {
		t.Errorf("Params.Len = %d; want 1", te.Params.Len())
	}
	if _, ok := te.Params.Get("size"); !ok {
		t.Errorf("Params.size missing")
	}
	if te.Body.Kind == 0 {
		t.Errorf("Body not captured")
	}
}

// TestTemplateEntry_UnmarshalYAML_NonMapping pins the early-return:
// a sequence-kind or scalar-kind node leaves the receiver untouched.
func TestTemplateEntry_UnmarshalYAML_NonMapping(t *testing.T) {
	t.Parallel()
	var te TemplateEntry
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Value: "x"}
	if err := te.UnmarshalYAML(scalar); err != nil {
		t.Errorf("scalar should silent no-op; got %v", err)
	}
	if te.Params.Len() != 0 || te.Body.Kind != 0 {
		t.Errorf("scalar should leave receiver zero; got %+v", te)
	}
	if err := te.UnmarshalYAML(nil); err != nil {
		t.Errorf("nil node should silent no-op; got %v", err)
	}
}

// TestTemplateEntry_MarshalYAML_ParamsFirst pins the canonical emission
// order: $params first, then the body's content in author order.
func TestTemplateEntry_MarshalYAML_ParamsFirst(t *testing.T) {
	t.Parallel()
	src := `
$params:
  size:
    type: integer
    default: 10
type: object
`
	var te TemplateEntry
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := te.UnmarshalYAML(node.Content[0]); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(te)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "$params:") {
		t.Errorf("emission missing $params; got %s", got)
	}
	idxParams := strings.Index(got, "$params:")
	idxType := strings.Index(got, "type:")
	if idxParams < 0 || idxType < 0 || idxParams >= idxType {
		t.Errorf("$params must precede body; got %s", got)
	}
}

// TestManifestMappings_UnmarshalYAML_EveryDispatch pins the flat-shape
// vs legacy-provider discriminator: reserved keys (components, features)
// populate the typed fields; other keys fall into Legacy.
func TestManifestMappings_UnmarshalYAML_EveryDispatch(t *testing.T) {
	t.Parallel()
	src := `
components:
  type:
    server: helm-chart
features:
  type:
    rewrite: nginx-helm
my-provider:
  components:
    name:
      api: legacy-helm
`
	var mm ManifestMappings
	if err := yaml.Unmarshal([]byte(src), &mm); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if mm.Components == nil {
		t.Errorf("Components not populated")
	}
	if mm.Features == nil {
		t.Errorf("Features not populated")
	}
	if _, ok := mm.Legacy.Get("my-provider"); !ok {
		t.Errorf("Legacy.my-provider not populated")
	}
}

// TestManifestMappings_UnmarshalYAML_NilNode_RejectsNonMap pins the
// early-return on nil and the type-gate on a non-mapping node.
func TestManifestMappings_UnmarshalYAML_NilNode_RejectsNonMap(t *testing.T) {
	t.Parallel()
	var mm ManifestMappings
	if err := mm.UnmarshalYAML(nil); err != nil {
		t.Errorf("nil node should not error; got %v", err)
	}
	// Non-mapping kind.
	mm = ManifestMappings{}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Line: 1}
	if err := mm.UnmarshalYAML(seq); err == nil {
		t.Errorf("sequence-kind should error")
	}
}

// TestManifestMappings_MarshalYAML_PopulatedAndEmpty pins emission:
// populated emits a mapping with components / features / legacy in
// author order; empty emits nil.
func TestManifestMappings_MarshalYAML_PopulatedAndEmpty(t *testing.T) {
	t.Parallel()
	// Empty.
	mm := ManifestMappings{}
	out, err := mm.MarshalYAML()
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if out != nil {
		t.Errorf("empty should marshal nil; got %v", out)
	}
	// Populated.
	mt := MappingTier{}
	mt.Type.Set("server", "helm-chart")
	mm = ManifestMappings{Components: &mt}
	mm.Legacy.Set("custom-provider", ProviderMappings{})
	out, err = mm.MarshalYAML()
	if err != nil {
		t.Fatalf("populated: %v", err)
	}
	if out == nil {
		t.Errorf("populated should not marshal nil")
	}
}

// TestOrderedMap_Keys_NilReceiver pins the nil-safe Keys path: a nil
// pointer must produce an empty slice.
func TestOrderedMap_Keys_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *OrderedMap[string]
	keys := m.Keys()
	if len(keys) != 0 {
		t.Errorf("nil.Keys() = %v; want []", keys)
	}
}

// TestOrderedMap_All_NilReceiver pins the nil-safe All iterator: the
// returned iter.Seq2 yields zero entries without panicking.
func TestOrderedMap_All_NilReceiver(t *testing.T) {
	t.Parallel()
	var m *OrderedMap[string]
	count := 0
	for range m.All() {
		count++
	}
	if count != 0 {
		t.Errorf("nil.All iter count = %d; want 0", count)
	}
}

// TestOrderedMap_All_EarlyStop pins the yield-false early-stop branch
// of the iterator: a consumer returning false stops the iteration.
func TestOrderedMap_All_EarlyStop(t *testing.T) {
	t.Parallel()
	var m OrderedMap[string]
	m.Set("a", "1")
	m.Set("b", "2")
	m.Set("c", "3")
	count := 0
	for k := range m.All() {
		count++
		_ = k
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("early-stop count = %d; want 2", count)
	}
}

// TestOrderedMap_UnmarshalYAML_NonMapping pins the type-gate: a
// sequence node surfaces a clear error.
func TestOrderedMap_UnmarshalYAML_NonMapping(t *testing.T) {
	t.Parallel()
	var m OrderedMap[string]
	seq := &yaml.Node{Kind: yaml.SequenceNode, Line: 1}
	if err := m.UnmarshalYAML(seq); err == nil {
		t.Errorf("sequence node should error")
	}
}

// TestOrderedMap_UnmarshalYAML_RejectsDuplicateKey pins the duplicate-
// key guard at YAML decode.
func TestOrderedMap_UnmarshalYAML_RejectsDuplicateKey(t *testing.T) {
	t.Parallel()
	src := `
a: 1
a: 2
`
	var m OrderedMap[string]
	if err := yaml.Unmarshal([]byte(src), &m); err == nil {
		t.Errorf("duplicate key should error")
	}
}

// TestOrderedMap_UnmarshalJSON_RejectsDuplicateKey pins the duplicate-
// key guard at JSON decode.
func TestOrderedMap_UnmarshalJSON_RejectsDuplicateKey(t *testing.T) {
	t.Parallel()
	src := `{"a": 1, "a": 2}`
	var m OrderedMap[any]
	if err := json.Unmarshal([]byte(src), &m); err == nil {
		t.Errorf("duplicate key should error")
	}
}

// TestValidateTemplateBody_PopulatedBody pins the populated-body path:
// a TemplateEntry whose Body carries a valid mapping returns nil; a
// body containing a forbidden keyword returns an error.
func TestValidateTemplateBody_PopulatedBody(t *testing.T) {
	t.Parallel()
	src := `
$params:
  size:
    type: integer
type: object
properties:
  port:
    type: integer
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var te TemplateEntry
	if err := te.UnmarshalYAML(node.Content[0]); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := ValidateTemplateBody(&te); err != nil {
		t.Errorf("valid body should pass; got %v", err)
	}

	// Forbidden keyword $dynamicAnchor.
	src = `
type: object
$dynamicAnchor: forbidden
`
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	te = TemplateEntry{}
	if err := te.UnmarshalYAML(node.Content[0]); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := ValidateTemplateBody(&te); err == nil {
		t.Errorf("body with $dynamicAnchor should fail")
	}
}
