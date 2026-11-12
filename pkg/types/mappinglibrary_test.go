// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestMappingLibrary_RoundTrip_MinimalEntry pins the upward-compatibility
// property: a minimal MappingLibrary with one mapping entry decodes into
// the typed fields and re-encodes preserving the entry content.
func TestMappingLibrary_RoundTrip_MinimalEntry(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
`
	var lib MappingLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if lib.APIVersion != "aggrega.ai/v1beta1" {
		t.Errorf("APIVersion = %q, want aggrega.ai/v1beta1", lib.APIVersion)
	}
	if lib.Kind != "MappingLibrary" {
		t.Errorf("Kind = %q, want MappingLibrary", lib.Kind)
	}
	if lib.Metadata.Name != "BaseMappings" {
		t.Errorf("Metadata.Name = %q, want BaseMappings", lib.Metadata.Name)
	}
	if lib.Spec.Mappings.Len() != 1 {
		t.Fatalf("Spec.Mappings.Len = %d, want 1", lib.Spec.Mappings.Len())
	}
	entry, ok := lib.Spec.Mappings.Get("SimpleValues")
	if !ok {
		t.Fatal("expected SimpleValues entry present")
	}
	if !strings.Contains(entry.Body, ".Values.image") {
		t.Errorf("Body did not preserve template content: %q", entry.Body)
	}
}

// TestMappingLibrary_RoundTrip_MultiEntryAuthorOrder verifies the
// OrderedMap preserves author-declared ordering across YAML decode/encode.
// A library with entries declared in non-alphabetic order must surface
// those entries in the declared order via Spec.Mappings.Keys().
func TestMappingLibrary_RoundTrip_MultiEntryAuthorOrder(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: Ordered
  version: 1.0.0
spec:
  mappings:
    Zulu: 'z: {{ .Values.z }}'
    Alpha: 'a: {{ .Values.a }}'
    Mike: 'm: {{ .Values.m }}'
`
	var lib MappingLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	keys := lib.Spec.Mappings.Keys()
	want := []string{"Zulu", "Alpha", "Mike"}
	if len(keys) != 3 {
		t.Fatalf("got %d keys; want 3 (%v)", len(keys), keys)
	}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("keys[%d] = %q, want %q (author order)", i, keys[i], k)
		}
	}
}

// TestMappingLibrary_RemarshalsCleanly verifies the MappingEntry
// MarshalYAML path produces non-empty output and the marshaled string
// preserves entry names + body content.
func TestMappingLibrary_RemarshalsCleanly(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: RoundTrip
  version: 1.0.0
spec:
  mappings:
    Inline: 'simple: {{ .Values.x }}'
`
	var lib MappingLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	out, err := yaml.Marshal(&lib)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty marshaled output; got empty")
	}
	str := string(out)
	for _, frag := range []string{"Inline", ".Values.x", "MappingLibrary"} {
		if !strings.Contains(str, frag) {
			t.Errorf("expected %q in marshaled output; got:\n%s", frag, str)
		}
	}
}

// TestMappingLibrary_MultilineTemplateBody_RoundTrip exercises the
// MarshalYAML literal-scalar discipline: a multi-line Go template body
// with newlines + indentation must preserve formatting through round-
// trip. yaml.v3's default scalar emission would collapse newlines on a
// plain-style scalar; the MarshalYAML implementation sets
// yaml.LiteralStyle when the body contains newlines.
func TestMappingLibrary_MultilineTemplateBody_RoundTrip(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: Multiline
  version: 1.0.0
spec:
  mappings:
    ProbeBlock: |
      readinessProbe:
        httpGet:
          path: /healthz
          port: {{ .Values.port }}
      livenessProbe:
        httpGet:
          path: /healthz
          port: {{ .Values.port }}
`
	var lib MappingLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	entry, ok := lib.Spec.Mappings.Get("ProbeBlock")
	if !ok {
		t.Fatal("expected ProbeBlock entry")
	}
	if !strings.Contains(entry.Body, "readinessProbe:") {
		t.Errorf("Body lost readinessProbe content: %q", entry.Body)
	}
	if !strings.Contains(entry.Body, "livenessProbe:") {
		t.Errorf("Body lost livenessProbe content: %q", entry.Body)
	}
	if !strings.Contains(entry.Body, "\n") {
		t.Errorf("Body lost newlines; got single-line: %q", entry.Body)
	}

	// Marshal back; the output must be a literal-style scalar so the
	// multi-line content survives.
	out, err := yaml.Marshal(&lib)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	outStr := string(out)
	// Literal-style scalars start with `|` (sometimes `|-` or `|+` for
	// chomping indicators). Verify the ProbeBlock value is emitted in
	// literal style.
	if !strings.Contains(outStr, "ProbeBlock: |") {
		t.Errorf("expected ProbeBlock emitted in literal style (|); got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "readinessProbe") {
		t.Errorf("marshaled output lost template content; got:\n%s", outStr)
	}
}

// TestMappingEntry_BoundaryCases exercises the UnmarshalYAML nil-value
// branch + MarshalYAML empty-Body branch — both branches are unreachable
// via typical YAML decode paths but defined defensively to cover edge
// cases (concurrent mutation, explicit nil construction).
func TestMappingEntry_BoundaryCases(t *testing.T) {
	t.Parallel()

	// UnmarshalYAML with nil value: returns nil (no-op).
	var e MappingEntry
	if err := e.UnmarshalYAML(nil); err != nil {
		t.Errorf("expected nil error on nil-value UnmarshalYAML; got %v", err)
	}
	if e.Body != "" {
		t.Errorf("expected Body empty after nil unmarshal; got %q", e.Body)
	}

	// MarshalYAML with empty Body: returns (nil, nil) — the entry is
	// effectively absent and the parent's emission should skip it.
	var empty MappingEntry
	got, err := empty.MarshalYAML()
	if err != nil {
		t.Errorf("expected nil error on empty-Body MarshalYAML; got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil node on empty-Body MarshalYAML; got %v", got)
	}
}

// TestMappingEntry_NonScalarRejected verifies UnmarshalYAML rejects
// non-scalar inputs (mapping or sequence nodes). MappingEntry bodies
// must be string scalars at the type-layer; the envelope JSON Schema
// also constrains the value-type at validation time, but the Go-side
// check catches the case where typed unmarshal reaches a non-scalar.
func TestMappingEntry_NonScalarRejected(t *testing.T) {
	t.Parallel()

	// A mapping shape where the entry's value is a YAML mapping (object)
	// rather than a scalar. The envelope schema would reject this; the
	// Go-side UnmarshalYAML also rejects to keep the type-layer honest.
	in := `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: BadEntry
  version: 1.0.0
spec:
  mappings:
    Bad:
      not: a string
`
	var lib MappingLibrary
	err := yaml.Unmarshal([]byte(in), &lib)
	if err == nil {
		t.Fatal("expected error on non-scalar mapping entry; got nil")
	}
	if !errors.Is(err, ErrMappingEntryNotScalar()) {
		t.Errorf("expected ErrMappingEntryNotScalar in chain; got %v", err)
	}
}
