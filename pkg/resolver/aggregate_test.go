// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"reflect"
	"sort"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// WireAggregateAnnotations tests pin the Aggregate-side cascade walker
// against the canonical authoring-tree path format and the
// pointer-tristate cascade semantics established by the Release-side
// walkBoundary. Coverage spans the baseline (empty), top-level
// component, subsystem propagation, component override, nested
// subsystem cascade, null opt-out at two levels, and deterministic
// iteration.

func loadAggregate(t *testing.T, src string) *types.Aggregate {
	t.Helper()
	var a types.Aggregate
	if err := yaml.Unmarshal([]byte(src), &a); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return &a
}

func TestWalkAggregate_FlatComponentNoAnnotations(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
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
`)
	got := WireAggregateAnnotations(agg)
	entry, ok := got["subsystems.data.components.api"]
	if !ok {
		t.Fatalf("subsystems.data.components.api missing from result; got keys: %v", keysOfStringMap(got))
	}
	if entry.Len() != 0 {
		t.Errorf("api entry should have no annotations; got %d entries", entry.Len())
	}
}

func TestWalkAggregate_TopLevelComponentAnnotation(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
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
        feature.myco/rewrite-host: "api.example.com"
`)
	got := WireAggregateAnnotations(agg)
	entry, ok := got["components.api"]
	if !ok {
		t.Fatalf("components.api missing from result; got keys: %v", keysOfStringMap(got))
	}
	val, ok := entry.Get("feature.myco/rewrite-host")
	if !ok {
		t.Fatal("feature.myco/rewrite-host missing from api's effective annotations")
	}
	if val != "api.example.com" {
		t.Errorf("annotation value = %q, want api.example.com", val)
	}
}

func TestWalkAggregate_SubsystemAnnotationPropagates(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
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
      components:
        api:
          type: core/server:1.0.0
        worker:
          type: core/server:1.0.0
`)
	got := WireAggregateAnnotations(agg)
	for _, path := range []string{
		"subsystems.data.components.api",
		"subsystems.data.components.worker",
	} {
		entry, ok := got[path]
		if !ok {
			t.Fatalf("%s missing from result", path)
		}
		val, ok := entry.Get("feature.myco/db-pool")
		if !ok {
			t.Errorf("%s should inherit feature.myco/db-pool from data subsystem", path)
			continue
		}
		if val != "prod-db" {
			t.Errorf("%s feature.myco/db-pool = %q, want prod-db", path, val)
		}
	}
}

func TestWalkAggregate_ComponentAnnotationOverridesSubsystem(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "subsystem-default"
      components:
        api:
          type: core/server:1.0.0
          annotations:
            feature.myco/db-pool: "api-override"
        worker:
          type: core/server:1.0.0
`)
	got := WireAggregateAnnotations(agg)

	api, _ := got["subsystems.data.components.api"]
	apiVal, _ := api.Get("feature.myco/db-pool")
	if apiVal != "api-override" {
		t.Errorf("api effective annotation = %q, want api-override (closer level wins)", apiVal)
	}

	worker, _ := got["subsystems.data.components.worker"]
	workerVal, _ := worker.Get("feature.myco/db-pool")
	if workerVal != "subsystem-default" {
		t.Errorf("worker effective annotation = %q, want subsystem-default (no component override)", workerVal)
	}
}

func TestWalkAggregate_NestedSubsystemCascade(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    outer:
      annotations:
        feature.myco/db-pool: "outer-value"
      subsystems:
        middle:
          annotations:
            feature.myco/db-pool: "middle-value"
          components:
            leaf:
              type: core/server:1.0.0
`)
	got := WireAggregateAnnotations(agg)
	path := "subsystems.outer.subsystems.middle.components.leaf"
	entry, ok := got[path]
	if !ok {
		t.Fatalf("%s missing from result; got keys: %v", path, keysOfStringMap(got))
	}
	val, _ := entry.Get("feature.myco/db-pool")
	if val != "middle-value" {
		t.Errorf("leaf effective annotation = %q, want middle-value (middle overrides outer)", val)
	}
}

func TestWalkAggregate_NullOptOutAtComponentLevel(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool: "subsystem-default"
      components:
        api:
          type: core/server:1.0.0
          annotations:
            feature.myco/db-pool: null
        worker:
          type: core/server:1.0.0
`)
	got := WireAggregateAnnotations(agg)

	api, _ := got["subsystems.data.components.api"]
	if _, present := api.Get("feature.myco/db-pool"); present {
		t.Error("api should have opted out of feature.myco/db-pool (explicit null at component level removes inherited entry)")
	}

	worker, _ := got["subsystems.data.components.worker"]
	workerVal, ok := worker.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("worker should still inherit feature.myco/db-pool (sibling component did not opt out)")
	}
	if workerVal != "subsystem-default" {
		t.Errorf("worker effective annotation = %q, want subsystem-default", workerVal)
	}
}

func TestWalkAggregate_NullOptOutAtSubsystemLevel(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    outer:
      annotations:
        feature.myco/db-pool: "outer-value"
      components:
        outer-comp:
          type: core/server:1.0.0
      subsystems:
        analytics:
          annotations:
            feature.myco/db-pool: null
          components:
            inner-comp:
              type: core/server:1.0.0
`)
	got := WireAggregateAnnotations(agg)

	innerPath := "subsystems.outer.subsystems.analytics.components.inner-comp"
	inner, ok := got[innerPath]
	if !ok {
		t.Fatalf("%s missing from result", innerPath)
	}
	if _, present := inner.Get("feature.myco/db-pool"); present {
		t.Error("inner-comp should have opted out (explicit null at nested subsystem level removes inherited entry for that subtree)")
	}

	outerCompPath := "subsystems.outer.components.outer-comp"
	outerComp, ok := got[outerCompPath]
	if !ok {
		t.Fatalf("%s missing from result", outerCompPath)
	}
	outerVal, ok := outerComp.Get("feature.myco/db-pool")
	if !ok {
		t.Fatal("outer-comp should still inherit feature.myco/db-pool (the nested subsystem's opt-out does not affect siblings in the outer subsystem)")
	}
	if outerVal != "outer-value" {
		t.Errorf("outer-comp effective annotation = %q, want outer-value", outerVal)
	}
}

func TestWalkAggregate_DeterministicIteration(t *testing.T) {
	t.Parallel()
	src := `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    outer:
      annotations:
        zulu_key: "z"
        alpha_key: "a"
        mike_key: "m"
      components:
        api:
          type: core/server:1.0.0
        worker:
          type: core/server:1.0.0
`
	first := WireAggregateAnnotations(loadAggregate(t, src))
	second := WireAggregateAnnotations(loadAggregate(t, src))

	// Same component-path key set across runs.
	firstKeys := keysOfStringMap(first)
	secondKeys := keysOfStringMap(second)
	if !reflect.DeepEqual(firstKeys, secondKeys) {
		t.Errorf("component-path key set differs across runs: first=%v second=%v", firstKeys, secondKeys)
	}

	// Same author-order annotation entries within each component's
	// flattened set across runs. Iterate one path to pin the discipline;
	// OrderedMap's All() preserves author order through merge + flatten.
	apiFirst, _ := first["subsystems.outer.components.api"]
	apiSecond, _ := second["subsystems.outer.components.api"]
	firstAnnotationKeys := orderedKeys(apiFirst)
	secondAnnotationKeys := orderedKeys(apiSecond)
	if !reflect.DeepEqual(firstAnnotationKeys, secondAnnotationKeys) {
		t.Errorf("annotation key order differs across runs: first=%v second=%v", firstAnnotationKeys, secondAnnotationKeys)
	}
	wantOrder := []string{"zulu_key", "alpha_key", "mike_key"}
	if !reflect.DeepEqual(firstAnnotationKeys, wantOrder) {
		t.Errorf("annotation keys = %v, want author order %v", firstAnnotationKeys, wantOrder)
	}
}

func keysOfStringMap(m map[string]types.OrderedMap[string]) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Sort for stable comparison in tests; this is test-internal stability,
	// not a contract on the production map's iteration order (which is
	// Go-map random).
	sort.Strings(out)
	return out
}

func orderedKeys(m types.OrderedMap[string]) []string {
	out := []string{}
	for k := range m.All() {
		out = append(out, k)
	}
	return out
}
