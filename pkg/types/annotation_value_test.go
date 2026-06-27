// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// AnnotationValue coverage: scalar round-trip, structured round-trip,
// null handling (whole-value opt-out via nil pointer at the parent
// map level), sequence-node rejection with the operator-facing error
// message, empty-string scalar, JSON serialization shape, and the
// load-bearing per-subkey nil preservation that the cascade-merge
// step depends on.

func TestAnnotationValue_UnmarshalScalar(t *testing.T) {
	t.Parallel()
	var av AnnotationValue
	if err := yaml.Unmarshal([]byte(`"api.example.com"`), &av); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !av.IsScalar() {
		t.Fatal("IsScalar() = false; want true for scalar input")
	}
	if av.IsStructured() {
		t.Error("IsStructured() = true; want false for scalar input")
	}
	if *av.Scalar != "api.example.com" {
		t.Errorf("Scalar = %q, want api.example.com", *av.Scalar)
	}
}

func TestAnnotationValue_UnmarshalStructured(t *testing.T) {
	t.Parallel()
	src := `
pool_size: 20
max_connections: 100
`
	var av AnnotationValue
	if err := yaml.Unmarshal([]byte(src), &av); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if av.IsScalar() {
		t.Error("IsScalar() = true; want false for structured input")
	}
	if !av.IsStructured() {
		t.Fatal("IsStructured() = false; want true for structured input")
	}
	poolSize, ok := av.Structured.Get("pool_size")
	if !ok {
		t.Fatal("Structured.pool_size missing")
	}
	if poolSize != 20 {
		t.Errorf("pool_size = %v, want 20", poolSize)
	}
	maxConns, ok := av.Structured.Get("max_connections")
	if !ok {
		t.Fatal("Structured.max_connections missing")
	}
	if maxConns != 100 {
		t.Errorf("max_connections = %v, want 100", maxConns)
	}
}

func TestAnnotationValue_StructuredWithNilSubkey_PreservesEntry(t *testing.T) {
	t.Parallel()
	// Load-bearing test for the cascade-merge step's per-subkey
	// null opt-out. A structured value with a null subkey must
	// unmarshal to a Structured OrderedMap that carries the
	// subkey with a nil entry — not a missing key. The merge step
	// inspects these nil entries to fire per-subkey opt-outs.
	src := `
pool_size: 20
max_connections: null
`
	var av AnnotationValue
	if err := yaml.Unmarshal([]byte(src), &av); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !av.IsStructured() {
		t.Fatal("IsStructured() = false; want true")
	}
	maxConns, present := av.Structured.Get("max_connections")
	if !present {
		t.Fatal("max_connections subkey MUST be present (with nil value) for the cascade-merge per-subkey opt-out signal to survive")
	}
	if maxConns != nil {
		t.Errorf("max_connections = %v, want nil (per-subkey opt-out signal)", maxConns)
	}
	// Sanity: the non-null subkey survives unaffected.
	poolSize, _ := av.Structured.Get("pool_size")
	if poolSize != 20 {
		t.Errorf("pool_size = %v, want 20", poolSize)
	}
}

func TestAnnotationValue_NullProducesNilPointer_AtParentMapLevel(t *testing.T) {
	t.Parallel()
	// yaml.v3 stores nil for **T when the YAML value is null; the
	// parent map's OrderedMap[*AnnotationValue].Get returns a nil
	// pointer for explicit-null entries. The cascade-merge step
	// interprets the nil pointer as a whole-value opt-out signal.
	src := `
feature.myco/db-pool: null
feature.myco/other: "value"
`
	var m OrderedMap[*AnnotationValue]
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	dbPool, present := m.Get("feature.myco/db-pool")
	if !present {
		t.Fatal("feature.myco/db-pool key missing from OrderedMap; null entries should still be present as keys")
	}
	if dbPool != nil {
		t.Errorf("feature.myco/db-pool pointer = %+v, want nil (whole-value opt-out)", dbPool)
	}
	other, _ := m.Get("feature.myco/other")
	if other == nil || other.Scalar == nil || *other.Scalar != "value" {
		t.Errorf("feature.myco/other = %+v, want non-nil pointer to AnnotationValue with Scalar=value", other)
	}
}

func TestAnnotationValue_SequenceNodeRejected(t *testing.T) {
	t.Parallel()
	// Operator authoring mistake: list literal where an object or
	// scalar was expected. The unmarshaller rejects sequence nodes
	// with an error naming all three accepted shapes so the operator
	// can correct the authoring without consulting the spec.
	src := `[a, b, c]`
	var av AnnotationValue
	err := yaml.Unmarshal([]byte(src), &av)
	if err == nil {
		t.Fatal("expected sequence node to be rejected; got nil error")
	}
	msg := err.Error()
	for _, want := range []string{
		"ANNOTATION_PARSE_ERROR",
		"scalar string",
		"object",
		"null",
		"sequence",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull: %s", want, msg)
		}
	}
}

func TestAnnotationValue_EmptyScalarPreservesEmptyString(t *testing.T) {
	t.Parallel()
	// An explicit empty-quoted string is deliberately-empty (distinct
	// from null opt-out). The Scalar pointer points to "" — IsScalar
	// returns true; IsStructured returns false.
	src := `""`
	var av AnnotationValue
	if err := yaml.Unmarshal([]byte(src), &av); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !av.IsScalar() {
		t.Fatal("IsScalar() = false; want true for explicit empty-quoted scalar")
	}
	if *av.Scalar != "" {
		t.Errorf("Scalar = %q, want empty string", *av.Scalar)
	}
}

func TestAnnotationValue_RoundTripScalar(t *testing.T) {
	t.Parallel()
	src := `api.example.com`
	var av AnnotationValue
	if err := yaml.Unmarshal([]byte(src), &av); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&av)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "api.example.com") {
		t.Errorf("round-trip output missing scalar value; got: %s", out)
	}
}

func TestAnnotationValue_RoundTripStructured(t *testing.T) {
	t.Parallel()
	src := `
pool_size: 20
max_connections: 100
`
	var av AnnotationValue
	if err := yaml.Unmarshal([]byte(src), &av); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&av)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "pool_size: 20") {
		t.Errorf("round-trip missing pool_size; got: %s", got)
	}
	if !strings.Contains(got, "max_connections: 100") {
		t.Errorf("round-trip missing max_connections; got: %s", got)
	}
}

func TestAnnotationValue_JSONShape(t *testing.T) {
	t.Parallel()
	// JSON serialization follows the field tags. Scalar populates
	// "scalar"; Structured populates "structured". Both fields are
	// omitempty so an empty AnnotationValue emits {}.
	s := "shared-pool"
	av := AnnotationValue{Scalar: &s}
	b, err := json.Marshal(av)
	if err != nil {
		t.Fatalf("json.Marshal scalar: %v", err)
	}
	if !strings.Contains(string(b), `"scalar":"shared-pool"`) {
		t.Errorf("JSON scalar shape missing; got: %s", b)
	}

	structured := OrderedMap[any]{}
	structured.Set("pool_size", 20)
	av2 := AnnotationValue{Structured: structured}
	b, err = json.Marshal(av2)
	if err != nil {
		t.Fatalf("json.Marshal structured: %v", err)
	}
	if !strings.Contains(string(b), `"structured"`) {
		t.Errorf("JSON structured shape missing; got: %s", b)
	}
	if !strings.Contains(string(b), `"pool_size":20`) {
		t.Errorf("JSON structured contents missing; got: %s", b)
	}
}
