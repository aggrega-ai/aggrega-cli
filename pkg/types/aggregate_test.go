// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// Aggregate annotation parse tests pin the pointer-tristate semantic on
// Subsystem.Annotations and ComponentInstance.Annotations: a scalar
// annotation value parses as a non-nil pointer entry; explicit YAML null
// parses as a non-nil entry with a nil pointer; an absent key produces
// no entry. The three states are distinguishable downstream by the
// annotation cascade so opt-out (nil pointer) and inheritance (absent
// key) carry different semantics.

func TestAggregateSubsystem_AnnotationsParse(t *testing.T) {
	t.Parallel()
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "prod-db"
        feature.myco/observability: "datadog"
`)
	var a Aggregate
	if err := yaml.Unmarshal(src, &a); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	sub, ok := a.Spec.Subsystems.Get("data")
	if !ok {
		t.Fatalf("subsystem data missing")
	}
	if sub.Annotations.Len() != 2 {
		t.Fatalf("Annotations.Len() = %d, want 2", sub.Annotations.Len())
	}
	got, ok := sub.Annotations.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("feature.myco/db-pool entry missing")
	}
	if got == nil {
		t.Fatal("feature.myco/db-pool pointer is nil; scalar value should parse as non-nil pointer")
	}
	if !got.IsScalar() || *got.Scalar != "prod-db" {
		t.Errorf("Annotations.Get(feature.myco/db-pool) Scalar = %v, want prod-db", got.Scalar)
	}
	got2, _ := sub.Annotations.Get("feature.myco/observability")
	if got2 == nil || !got2.IsScalar() || *got2.Scalar != "datadog" {
		t.Errorf("feature.myco/observability did not round-trip: got %+v", got2)
	}
}

func TestAggregateSubsystem_NullAnnotationParsesAsNilPointer(t *testing.T) {
	t.Parallel()
	// The three states must be distinguishable:
	//   - has-value (scalar) → non-nil pointer
	//   - opted-out (null) → nil pointer entry
	//   - absent → no entry at all
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        has-value: "v"
        opted-out: null
`)
	var a Aggregate
	if err := yaml.Unmarshal(src, &a); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	sub, _ := a.Spec.Subsystems.Get("data")

	hv, ok := sub.Annotations.Get("has-value")
	if !ok {
		t.Fatal("has-value entry missing")
	}
	if hv == nil {
		t.Fatal("has-value pointer is nil; want non-nil pointer to \"v\"")
	}
	if !hv.IsScalar() || *hv.Scalar != "v" {
		t.Errorf("has-value Scalar = %v, want v", hv.Scalar)
	}

	oo, ok := sub.Annotations.Get("opted-out")
	if !ok {
		t.Fatal("opted-out entry missing; explicit YAML null should produce a non-nil entry with nil pointer (distinct from absent key)")
	}
	if oo != nil {
		t.Errorf("opted-out pointer = %+v, want nil pointer (explicit YAML null)", oo)
	}

	if _, present := sub.Annotations.Get("absent-key"); present {
		t.Error("absent-key should produce no entry")
	}
}

func TestAggregateComponentInstance_AnnotationsParse(t *testing.T) {
	t.Parallel()
	// Annotations on a ComponentInstance — both top-level Aggregate
	// components and Subsystem-nested components carry the same struct
	// type, so the test covers both surfaces through the nested case.
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      components:
        api:
          type: core/server:1.0.0
          annotations:
            feature.myco/rewrite-host: "api.example.com"
            feature.myco/observability: null
        analytics:
          type: core/server:1.0.0
`)
	var a Aggregate
	if err := yaml.Unmarshal(src, &a); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	sub, _ := a.Spec.Subsystems.Get("data")

	api, ok := sub.Components.Get("api")
	if !ok {
		t.Fatalf("component api missing")
	}
	if api.Annotations.Len() != 2 {
		t.Fatalf("api.Annotations.Len() = %d, want 2", api.Annotations.Len())
	}
	hv, _ := api.Annotations.Get("feature.myco/rewrite-host")
	if hv == nil || !hv.IsScalar() || *hv.Scalar != "api.example.com" {
		t.Errorf("api.Annotations[feature.myco/rewrite-host] = %+v, want non-nil scalar pointing at api.example.com", hv)
	}
	oo, ok := api.Annotations.Get("feature.myco/observability")
	if !ok {
		t.Fatal("feature.myco/observability entry missing on api")
	}
	if oo != nil {
		t.Errorf("feature.myco/observability pointer = %+v, want nil (explicit YAML null)", oo)
	}

	analytics, ok := sub.Components.Get("analytics")
	if !ok {
		t.Fatalf("component analytics missing")
	}
	if analytics.Annotations.Len() != 0 {
		t.Errorf("analytics.Annotations.Len() = %d, want 0 (no annotations declared)", analytics.Annotations.Len())
	}
}

func TestAggregateTopLevelComponent_AnnotationsParse(t *testing.T) {
	t.Parallel()
	// AggregateSpec.Components carries ComponentInstance values directly
	// (no enclosing subsystem). The Annotations field surfaces at this
	// top-level scope by virtue of sharing the ComponentInstance struct.
	src := []byte(`
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server:1.0.0
      annotations:
        feature.myco/rewrite-host: "top-level.example.com"
`)
	var a Aggregate
	if err := yaml.Unmarshal(src, &a); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	api, ok := a.Spec.Components.Get("api")
	if !ok {
		t.Fatalf("top-level component api missing")
	}
	got, ok := api.Annotations.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatal("top-level api annotation missing")
	}
	if got == nil || !got.IsScalar() || *got.Scalar != "top-level.example.com" {
		t.Errorf("top-level api annotation = %+v, want non-nil scalar pointing at top-level.example.com", got)
	}
}
