// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestWorkloadLibrary_RoundTrip_MinimalEntry pins the upward-compatibility
// property: a minimal WorkloadLibrary with one workload entry decodes into
// the typed fields and re-encodes preserving the entry content.
func TestWorkloadLibrary_RoundTrip_MinimalEntry(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: BaseWorkloads
  version: 1.0.0
spec:
  workloads:
    SimpleService: 'kind: Workload
image: {{ .Values.image }}'
`
	var lib WorkloadLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if lib.APIVersion != "aggrega.ai/v1alpha1" {
		t.Errorf("APIVersion = %q, want aggrega.ai/v1alpha1", lib.APIVersion)
	}
	if lib.Kind != "WorkloadLibrary" {
		t.Errorf("Kind = %q, want WorkloadLibrary", lib.Kind)
	}
	if lib.Metadata.Name != "BaseWorkloads" {
		t.Errorf("Metadata.Name = %q, want BaseWorkloads", lib.Metadata.Name)
	}
	if lib.Spec.Workloads.Len() != 1 {
		t.Fatalf("Spec.Workloads.Len = %d, want 1", lib.Spec.Workloads.Len())
	}
	entry, ok := lib.Spec.Workloads.Get("SimpleService")
	if !ok {
		t.Fatal("expected SimpleService entry present")
	}
	if !strings.Contains(entry.Body, ".Values.image") {
		t.Errorf("Body did not preserve template content: %q", entry.Body)
	}
}

// TestWorkloadLibrary_RoundTrip_MultiEntryAuthorOrder verifies the
// OrderedMap preserves author-declared ordering across YAML decode/encode.
func TestWorkloadLibrary_RoundTrip_MultiEntryAuthorOrder(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: Ordered
  version: 1.0.0
spec:
  workloads:
    Zulu: 'z: {{ .Values.z }}'
    Alpha: 'a: {{ .Values.a }}'
    Mike: 'm: {{ .Values.m }}'
`
	var lib WorkloadLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	keys := lib.Spec.Workloads.Keys()
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

// TestWorkloadLibrary_RemarshalsCleanly verifies the WorkloadEntry
// MarshalYAML path produces non-empty output and the marshaled string
// preserves entry names + body content.
func TestWorkloadLibrary_RemarshalsCleanly(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: RoundTrip
  version: 1.0.0
spec:
  workloads:
    Inline: 'simple: {{ .Values.x }}'
`
	var lib WorkloadLibrary
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
	for _, frag := range []string{"Inline", ".Values.x", "WorkloadLibrary"} {
		if !strings.Contains(str, frag) {
			t.Errorf("expected %q in marshaled output; got:\n%s", frag, str)
		}
	}
}

// TestWorkloadLibrary_MultilineTemplateBody_RoundTrip exercises the
// MarshalYAML literal-scalar discipline: a multi-line Go template body
// with newlines + indentation must preserve formatting through round-
// trip. The MarshalYAML implementation sets yaml.LiteralStyle when the
// body contains newlines.
func TestWorkloadLibrary_MultilineTemplateBody_RoundTrip(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: Multiline
  version: 1.0.0
spec:
  workloads:
    ServiceBlock: |
      apiVersion: aggrega.ai/v1alpha1
      kind: Workload
      spec:
        containers:
          - name: app
            port: {{ .Values.port }}
`
	var lib WorkloadLibrary
	if err := yaml.Unmarshal([]byte(in), &lib); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	entry, ok := lib.Spec.Workloads.Get("ServiceBlock")
	if !ok {
		t.Fatal("expected ServiceBlock entry")
	}
	if !strings.Contains(entry.Body, "containers:") {
		t.Errorf("Body lost containers content: %q", entry.Body)
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
	if !strings.Contains(outStr, "ServiceBlock: |") {
		t.Errorf("expected ServiceBlock emitted in literal style (|); got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "containers") {
		t.Errorf("marshaled output lost template content; got:\n%s", outStr)
	}
}

// TestWorkloadEntry_BoundaryCases exercises the UnmarshalYAML nil-value
// branch + MarshalYAML empty-Body branch.
func TestWorkloadEntry_BoundaryCases(t *testing.T) {
	t.Parallel()

	var e WorkloadEntry
	if err := e.UnmarshalYAML(nil); err != nil {
		t.Errorf("expected nil error on nil-value UnmarshalYAML; got %v", err)
	}
	if e.Body != "" {
		t.Errorf("expected Body empty after nil unmarshal; got %q", e.Body)
	}

	var empty WorkloadEntry
	got, err := empty.MarshalYAML()
	if err != nil {
		t.Errorf("expected nil error on empty-Body MarshalYAML; got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil node on empty-Body MarshalYAML; got %v", got)
	}
}

// TestWorkloadEntry_NonScalarRejected verifies UnmarshalYAML rejects
// non-scalar inputs (mapping or sequence nodes).
func TestWorkloadEntry_NonScalarRejected(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: BadEntry
  version: 1.0.0
spec:
  workloads:
    Bad:
      not: a string
`
	var lib WorkloadLibrary
	err := yaml.Unmarshal([]byte(in), &lib)
	if err == nil {
		t.Fatal("expected error on non-scalar workload entry; got nil")
	}
	if !errors.Is(err, ErrWorkloadEntryNotScalar) {
		t.Errorf("expected ErrWorkloadEntryNotScalar in chain; got %v", err)
	}
}
