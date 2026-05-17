// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAnnotationValue_IsEmpty_EveryBranch pins the three documented
// empty-states: nil receiver, non-nil receiver with both Scalar and
// Structured unset (the defensive UnmarshalYAML-null-branch state),
// and the negative case (populated values return false).
func TestAnnotationValue_IsEmpty_EveryBranch(t *testing.T) {
	t.Parallel()
	var nilAV *AnnotationValue
	if !nilAV.IsEmpty() {
		t.Errorf("nil receiver: IsEmpty should be true")
	}
	emptyAV := &AnnotationValue{}
	if !emptyAV.IsEmpty() {
		t.Errorf("empty AnnotationValue: IsEmpty should be true")
	}
	scalar := "x"
	scalarAV := &AnnotationValue{Scalar: &scalar}
	if scalarAV.IsEmpty() {
		t.Errorf("scalar AnnotationValue: IsEmpty should be false")
	}
	structured := OrderedMap[any]{}
	structured.Set("k", "v")
	structuredAV := &AnnotationValue{Structured: structured}
	if structuredAV.IsEmpty() {
		t.Errorf("structured AnnotationValue: IsEmpty should be false")
	}
}

// TestScalarOnly_ProjectsScalarsExcludesStructured pins the projector:
// scalar entries land in the output OrderedMap at their original keys;
// structured, empty, and nil entries are excluded; author order is
// preserved.
func TestScalarOnly_ProjectsScalarsExcludesStructured(t *testing.T) {
	t.Parallel()
	in := OrderedMap[*AnnotationValue]{}
	first := "first-value"
	in.Set("a", &AnnotationValue{Scalar: &first})
	in.Set("b", nil) // excluded
	structured := OrderedMap[any]{}
	structured.Set("k", "v")
	in.Set("c", &AnnotationValue{Structured: structured}) // excluded
	second := "second-value"
	in.Set("d", &AnnotationValue{Scalar: &second})

	out := ScalarOnly(in)
	if out.Len() != 2 {
		t.Fatalf("expected 2 scalars in projection; got %d (%v)", out.Len(), out.Keys())
	}
	if v, _ := out.Get("a"); v != "first-value" {
		t.Errorf("a: expected first-value; got %q", v)
	}
	if v, _ := out.Get("d"); v != "second-value" {
		t.Errorf("d: expected second-value; got %q", v)
	}
	// Order preserved.
	keys := out.Keys()
	if keys[0] != "a" || keys[1] != "d" {
		t.Errorf("author order broken; got %v", keys)
	}
}

// TestScalarOnly_EmptyInputProducesEmptyOutput pins the empty-input
// path: an empty input map yields an empty output map (not nil).
func TestScalarOnly_EmptyInputProducesEmptyOutput(t *testing.T) {
	t.Parallel()
	out := ScalarOnly(OrderedMap[*AnnotationValue]{})
	if out.Len() != 0 {
		t.Errorf("empty input: expected empty output; got %v", out.Keys())
	}
}

// TestAnnotationBindingMaps_LenAndLookup pins both small accessors:
// Len sums Infra and Values; Lookup walks both lists with infra-first
// tie-breaking.
func TestAnnotationBindingMaps_LenAndLookup(t *testing.T) {
	t.Parallel()
	m := AnnotationBindingMaps{
		Infra:  []string{"pool_size", "max_connections"},
		Values: []string{"db_name", "shared"},
	}
	if m.Len() != 4 {
		t.Errorf("Len = %d; want 4", m.Len())
	}
	if pillar, ok := m.Lookup("pool_size"); !ok || pillar != "infra" {
		t.Errorf("Lookup pool_size: got (%q, %v); want (infra, true)", pillar, ok)
	}
	if pillar, ok := m.Lookup("db_name"); !ok || pillar != "values" {
		t.Errorf("Lookup db_name: got (%q, %v); want (values, true)", pillar, ok)
	}
	if _, ok := m.Lookup("undeclared"); ok {
		t.Errorf("Lookup undeclared: ok should be false")
	}
	// Tie-breaking: same path in both pillars resolves to infra.
	overlap := AnnotationBindingMaps{
		Infra:  []string{"shared"},
		Values: []string{"shared"},
	}
	if pillar, ok := overlap.Lookup("shared"); !ok || pillar != "infra" {
		t.Errorf("Lookup overlap: got (%q, %v); want (infra, true) — infra-first tie-breaking", pillar, ok)
	}
}

// TestIsValidComponentType_DiscriminatesOntology pins the 7-type
// ontology discriminator. Every documented type returns true; an
// unknown type returns false.
func TestIsValidComponentType_DiscriminatesOntology(t *testing.T) {
	t.Parallel()
	for typ := range ValidComponentTypes {
		if !IsValidComponentType(typ) {
			t.Errorf("expected %q valid; got false", typ)
		}
	}
	if IsValidComponentType("not-in-ontology") {
		t.Errorf("expected not-in-ontology invalid; got true")
	}
	if IsValidComponentType("") {
		t.Errorf("expected empty type invalid; got true")
	}
}

// TestTemplateAlphabeticalMap_AuthorOrderProducesAlphabeticalMap pins
// the single-level template projection: an OrderedMap with author-
// ordered keys produces a map[string]any whose iteration is
// alphabetical-by-stdlib-map (the Go template engine's natural
// iteration order). The contract is implicit — author order is
// preserved in the OrderedMap; the projected map's range order is
// alphabetical per Helm-template convention.
func TestTemplateAlphabeticalMap_AuthorOrderProducesAlphabeticalMap(t *testing.T) {
	t.Parallel()
	om := OrderedMap[any]{}
	om.Set("zebra", "z-val")
	om.Set("apple", "a-val")
	om.Set("mango", "m-val")
	out := om.TemplateAlphabeticalMap()
	if len(out) != 3 {
		t.Fatalf("expected 3 entries; got %d", len(out))
	}
	if out["zebra"] != "z-val" || out["apple"] != "a-val" || out["mango"] != "m-val" {
		t.Errorf("template map content wrong: %v", out)
	}
}

// TestTemplateAlphabeticalMap_EmptyInput pins the empty-input branch
// of the projector.
func TestTemplateAlphabeticalMap_EmptyInput(t *testing.T) {
	t.Parallel()
	om := OrderedMap[any]{}
	out := om.TemplateAlphabeticalMap()
	if len(out) != 0 {
		t.Errorf("empty input: expected empty map; got %v", out)
	}
}

// TestAnnotationValue_UnmarshalYAML_EveryBranch pins each documented
// input shape for the YAML unmarshaler: scalar string → Scalar field;
// mapping node → Structured field; tilde/null → empty (defensive
// branch); sequence → error.
func TestAnnotationValue_UnmarshalYAML_EveryBranch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		yamlSrc      string
		wantScalar   string
		wantStructured bool
		wantEmpty    bool
		wantErr      bool
	}{
		{"scalar", `"hello"`, "hello", false, false, false},
		{"null_tilde", `~`, "", false, true, false},
		{"null_explicit", `null`, "", false, true, false},
		{"mapping", `{k1: v1, k2: v2}`, "", true, false, false},
		{"sequence_rejected", `[1, 2, 3]`, "", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var av AnnotationValue
			err := yaml.Unmarshal([]byte(c.yamlSrc), &av)
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error; got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantEmpty {
				if !av.IsEmpty() {
					t.Errorf("expected IsEmpty=true; got %+v", av)
				}
				return
			}
			if c.wantScalar != "" {
				if av.Scalar == nil || *av.Scalar != c.wantScalar {
					t.Errorf("expected Scalar=%q; got %+v", c.wantScalar, av.Scalar)
				}
			}
			if c.wantStructured {
				if av.Structured.Len() == 0 {
					t.Errorf("expected Structured populated; got %+v", av.Structured)
				}
			}
		})
	}
}

// FuzzAnnotationValueUnmarshalYAML exercises the YAML unmarshaler
// with arbitrary input bytes. The contract: the unmarshaler must
// never panic on any byte sequence (validation errors are returned
// as Go errors, not panics). Seeds: the table-driven cases plus
// boundary inputs (empty, whitespace, malformed YAML, unicode).
func FuzzAnnotationValueUnmarshalYAML(f *testing.F) {
	f.Add([]byte(``))
	f.Add([]byte(`"scalar"`))
	f.Add([]byte(`~`))
	f.Add([]byte(`{k: v}`))
	f.Add([]byte(`[1, 2]`))
	f.Add([]byte(`{nested: {deep: {deeper: x}}}`))
	f.Add([]byte(strings.Repeat("a", 1000)))
	f.Fuzz(func(t *testing.T, input []byte) {
		// Unmarshaler must not panic on arbitrary input. Error returns
		// are fine; panic is not.
		var av AnnotationValue
		_ = yaml.Unmarshal(input, &av)
	})
}

// TestUsesSocketValue_UnmarshalYAML_AllNodeKinds pins each documented
// dispatch branch: SequenceNode → Compact, MappingNode → Verbose,
// empty ScalarNode → zero value, non-empty ScalarNode → error,
// other kinds → error.
func TestUsesSocketValue_UnmarshalYAML_AllNodeKinds(t *testing.T) {
	t.Parallel()
	// SequenceNode → Compact.
	var seq UsesSocketValue
	if err := yaml.Unmarshal([]byte(`[a, b, c]`), &seq); err != nil {
		t.Fatalf("sequence: %v", err)
	}
	if len(seq.Compact) != 3 || seq.Compact[0] != "a" {
		t.Errorf("sequence → Compact: got %+v", seq.Compact)
	}
	// MappingNode → Verbose.
	var mapping UsesSocketValue
	if err := yaml.Unmarshal([]byte(`{primary: backend}`), &mapping); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if mapping.Verbose.Len() == 0 {
		t.Errorf("mapping → Verbose: empty")
	}
	// Empty scalar → zero value.
	var empty UsesSocketValue
	if err := yaml.Unmarshal([]byte(`""`), &empty); err != nil {
		t.Errorf("empty scalar: expected nil; got %v", err)
	}
	// Non-empty scalar → error.
	var bad UsesSocketValue
	if err := yaml.Unmarshal([]byte(`"not-empty"`), &bad); err == nil {
		t.Errorf("non-empty scalar: expected error; got nil")
	}
}

// TestUsesSocketValue_UnmarshalJSON_AllDispatches pins each documented
// JSON dispatch: null → zero value, [ → Compact, { → Verbose,
// anything else → error.
func TestUsesSocketValue_UnmarshalJSON_AllDispatches(t *testing.T) {
	t.Parallel()
	var nullVal UsesSocketValue
	if err := nullVal.UnmarshalJSON([]byte("null")); err != nil {
		t.Errorf("null: %v", err)
	}
	if !nullVal.IsZero() {
		t.Errorf("null should produce zero value")
	}
	var seq UsesSocketValue
	if err := seq.UnmarshalJSON([]byte(`["a", "b"]`)); err != nil {
		t.Errorf("array: %v", err)
	}
	if len(seq.Compact) != 2 {
		t.Errorf("array → Compact: got %+v", seq.Compact)
	}
	var mapping UsesSocketValue
	if err := mapping.UnmarshalJSON([]byte(`{"primary":"backend"}`)); err != nil {
		t.Errorf("object: %v", err)
	}
	if mapping.Verbose.Len() == 0 {
		t.Errorf("object → Verbose: empty")
	}
	var bad UsesSocketValue
	if err := bad.UnmarshalJSON([]byte("42")); err == nil {
		t.Errorf("scalar: expected error; got nil")
	}
}

// FuzzUsesSocketValue_UnmarshalYAML exercises the YAML unmarshaler
// with arbitrary input. Contract: never panic.
func FuzzUsesSocketValue_UnmarshalYAML(f *testing.F) {
	f.Add([]byte(`[a, b]`))
	f.Add([]byte(`{primary: backend}`))
	f.Add([]byte(`""`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{nested: {deep: x}}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		var v UsesSocketValue
		_ = yaml.Unmarshal(input, &v)
	})
}

// TestInjectPipe_UnmarshalYAML_AllMechanisms pins the per-mechanism
// dispatch: env / file / arg / all populate the typed fields;
// non-core mechanisms route to Additional preserving author order.
func TestInjectPipe_UnmarshalYAML_AllMechanisms(t *testing.T) {
	t.Parallel()
	src := `
env:
  API_HOST: backend
  API_PORT: "8080"
file:
  config.json: "/etc/config.json"
arg:
  --verbose: "true"
custom_mechanism:
  CUSTOM_KEY: "custom_value"
`
	var pipe InjectPipe
	if err := yaml.Unmarshal([]byte(src), &pipe); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v, _ := pipe.Env.Get("API_HOST"); v != "backend" {
		t.Errorf("env.API_HOST: got %q; want backend", v)
	}
	if v, _ := pipe.File.Get("config.json"); v != "/etc/config.json" {
		t.Errorf("file.config.json: got %q", v)
	}
	if v, _ := pipe.Arg.Get("--verbose"); v != "true" {
		t.Errorf("arg.--verbose: got %q", v)
	}
	custom, ok := pipe.Additional.Get("custom_mechanism")
	if !ok {
		t.Fatal("custom_mechanism should be in Additional")
	}
	if v, _ := custom.Get("CUSTOM_KEY"); v != "custom_value" {
		t.Errorf("Additional[custom_mechanism].CUSTOM_KEY: got %q", v)
	}
}

// TestInjectPipe_UnmarshalYAML_RejectsDuplicateKey pins the duplicate-
// key guard: two `env:` keys at the same level return a wrapped error
// rather than silently letting the second overwrite the first.
func TestInjectPipe_UnmarshalYAML_RejectsDuplicateKey(t *testing.T) {
	t.Parallel()
	src := `
env:
  A: 1
env:
  B: 2
`
	var pipe InjectPipe
	err := yaml.Unmarshal([]byte(src), &pipe)
	if err == nil {
		t.Fatal("expected duplicate-key error; got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate; got: %s", err.Error())
	}
}

// TestInjectPipe_UnmarshalYAML_RejectsNonMapping pins the type guard:
// a sequence or scalar at the top level returns an error rather
// than mis-decoding into typed fields.
func TestInjectPipe_UnmarshalYAML_RejectsNonMapping(t *testing.T) {
	t.Parallel()
	for _, src := range []string{`[1, 2, 3]`, `"not-a-mapping"`} {
		var pipe InjectPipe
		err := yaml.Unmarshal([]byte(src), &pipe)
		if err == nil {
			t.Errorf("input %q: expected error; got nil", src)
		}
	}
}

// TestInjectPipe_UnmarshalJSON_NullReturnsZero pins the null-input
// fast path: a literal `null` JSON document leaves the InjectPipe
// untouched (zero state) and returns nil.
func TestInjectPipe_UnmarshalJSON_NullReturnsZero(t *testing.T) {
	t.Parallel()
	var pipe InjectPipe
	if err := pipe.UnmarshalJSON([]byte("null")); err != nil {
		t.Errorf("null input: expected nil; got %v", err)
	}
	if !pipe.Env.IsZero() || !pipe.File.IsZero() || !pipe.Arg.IsZero() {
		t.Errorf("null input should leave pipe at zero; got %+v", pipe)
	}
}

// TestUsesSocketValue_IsZero_EveryBranch pins the IsZero predicate
// used by yaml.v3 omitempty / encoding/json omitzero. A zero-value
// UsesSocketValue is IsZero; populating either Compact or Verbose
// flips the result.
func TestUsesSocketValue_IsZero_EveryBranch(t *testing.T) {
	t.Parallel()
	var zero UsesSocketValue
	if !zero.IsZero() {
		t.Errorf("zero-value UsesSocketValue: expected IsZero=true")
	}
	withCompact := UsesSocketValue{Compact: []string{"a"}}
	if withCompact.IsZero() {
		t.Errorf("populated Compact: expected IsZero=false")
	}
	verbose := OrderedMap[VerboseUsesEntry]{}
	verbose.Set("primary", VerboseUsesEntry{Target: "x"})
	withVerbose := UsesSocketValue{Verbose: verbose}
	if withVerbose.IsZero() {
		t.Errorf("populated Verbose: expected IsZero=false")
	}
}

// TestVerboseUsesEntry_IsZero_EveryBranch pins the IsZero predicate
// on VerboseUsesEntry: a zero-value entry is IsZero; setting Target
// OR Inject flips the result.
func TestVerboseUsesEntry_IsZero_EveryBranch(t *testing.T) {
	t.Parallel()
	var zero VerboseUsesEntry
	if !zero.IsZero() {
		t.Errorf("zero-value VerboseUsesEntry: expected IsZero=true")
	}
	withTarget := VerboseUsesEntry{Target: "backend"}
	if withTarget.IsZero() {
		t.Errorf("populated Target: expected IsZero=false")
	}
	withInject := VerboseUsesEntry{Inject: &InjectBlock{}}
	if withInject.IsZero() {
		t.Errorf("populated Inject: expected IsZero=false")
	}
}

// TestWorkloadTemplate_IsEmpty_EveryBranch pins the IsEmpty
// discriminator on WorkloadTemplate: zero-value is empty; populating
// either Inline or Import flips the result.
func TestWorkloadTemplate_IsEmpty_EveryBranch(t *testing.T) {
	t.Parallel()
	var w WorkloadTemplate
	if !w.IsEmpty() {
		t.Errorf("zero-value WorkloadTemplate: expected IsEmpty=true")
	}
	w.Inline = "x"
	if w.IsEmpty() {
		t.Errorf("populated Inline: expected IsEmpty=false")
	}
	w2 := WorkloadTemplate{Import: "./path"}
	if w2.IsEmpty() {
		t.Errorf("populated Import: expected IsEmpty=false")
	}
}

// TestValidateTemplateBody_NilAndZeroKindReturnNil pins the early-
// return guards: nil pointer and zero-kind body both return nil.
// (The forbidden-keyword scan applies only to non-empty bodies.)
func TestValidateTemplateBody_NilAndZeroKindReturnNil(t *testing.T) {
	t.Parallel()
	if err := ValidateTemplateBody(nil); err != nil {
		t.Errorf("nil: expected nil; got %v", err)
	}
	if err := ValidateTemplateBody(&TemplateEntry{}); err != nil {
		t.Errorf("zero-kind body: expected nil; got %v", err)
	}
}

// TestCanonicalSchemaID_FormatsFullID pins the URL composer.
func TestCanonicalSchemaID_FormatsFullID(t *testing.T) {
	t.Parallel()
	want := "oci://hub.aggrega.ai/base-components@1.0.0#/schemas/PostgresPayload"
	got := CanonicalSchemaID("hub.aggrega.ai", "base-components", "1.0.0", "PostgresPayload")
	if got != want {
		t.Errorf("CanonicalSchemaID = %q; want %q", got, want)
	}
}

// TestArtifactEntry_IsEmpty pins the IsEmpty discriminator on
// ArtifactEntry: a zero-value entry is empty; populated fields make
// it non-empty.
func TestArtifactEntry_IsEmpty(t *testing.T) {
	t.Parallel()
	var a ArtifactEntry
	if !a.IsEmpty() {
		t.Errorf("zero-value ArtifactEntry: expected IsEmpty=true")
	}
	a.Inline = "non-empty"
	if a.IsEmpty() {
		t.Errorf("populated Inline: expected IsEmpty=false")
	}
	b := ArtifactEntry{Import: "./path/to/file"}
	if b.IsEmpty() {
		t.Errorf("populated Import: expected IsEmpty=false")
	}
}
