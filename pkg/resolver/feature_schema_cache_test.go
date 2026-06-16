// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// brokenSchemaFeature returns a Feature whose spec.schema body
// triggers a compactform.Compile rejection (constraint applied to
// the wrong declared type — `min:` on `type: object`). Used to
// exercise the cache's negative-result path.
func brokenSchemaFeature(t *testing.T, name string) types.Feature {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(`values:
  port: { type: object, min: 5 }
`), &node); err != nil {
		t.Fatalf("broken schema unmarshal: %v", err)
	}
	doc := &node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	return types.Feature{
		Metadata: types.FeatureMetadata{Name: name, Version: "1.0.0"},
		Spec:     types.FeatureSpec{Schema: *doc},
	}
}

func TestFeatureSchemaCache_FirstCallCompilesOnceAndCachesMiss(t *testing.T) {
	t.Parallel()
	cache := newFeatureSchemaCache(nil)
	feature := featureWithSchema(t, "core/db-pool", `
values:
  db_name:
    type: string
`)
	_, ok := cache.schemaFor(&feature)
	if !ok {
		t.Fatalf("schemaFor: expected successful compile; got ok=false")
	}
	if cache.misses != 1 {
		t.Errorf("misses: want 1, got %d", cache.misses)
	}
	if cache.hits != 0 {
		t.Errorf("hits: want 0, got %d", cache.hits)
	}
}

func TestFeatureSchemaCache_SecondCallOnSameFeatureHits(t *testing.T) {
	t.Parallel()
	cache := newFeatureSchemaCache(nil)
	feature := featureWithSchema(t, "core/db-pool", `
values:
  db_name:
    type: string
`)
	if _, ok := cache.schemaFor(&feature); !ok {
		t.Fatalf("schemaFor (first): expected successful compile")
	}
	if _, ok := cache.schemaFor(&feature); !ok {
		t.Fatalf("schemaFor (second): expected cached success")
	}
	if cache.misses != 1 {
		t.Errorf("misses: want 1, got %d", cache.misses)
	}
	if cache.hits != 1 {
		t.Errorf("hits: want 1, got %d", cache.hits)
	}
}

func TestFeatureSchemaCache_DistinctVersionedURIsCompileSeparately(t *testing.T) {
	t.Parallel()
	cache := newFeatureSchemaCache(nil)
	featureA := featureWithSchema(t, "core/db-pool", `
values:
  db_name:
    type: string
`)
	featureB := featureWithSchema(t, "core/cache", `
values:
  size:
    type: integer
`)
	// Same Metadata.Name as featureA but different Version — the
	// cache must treat the versioned URI as a distinct key so a
	// future multi-version coexistence in the catalog does not
	// silently conflate compile output across versions.
	featureAprime := featureWithSchema(t, "core/db-pool", `
values:
  db_name:
    type: string
  read_replicas:
    type: integer
`)
	featureAprime.Metadata.Version = "2.0.0"

	cache.schemaFor(&featureA)
	cache.schemaFor(&featureB)
	cache.schemaFor(&featureAprime)

	if cache.misses != 3 {
		t.Errorf("misses: want 3 (distinct versioned URIs), got %d", cache.misses)
	}
	if cache.hits != 0 {
		t.Errorf("hits: want 0, got %d", cache.hits)
	}
}

func TestFeatureSchemaCache_CompileErrorCachesNegativeResult(t *testing.T) {
	t.Parallel()
	cache := newFeatureSchemaCache(nil)
	feature := brokenSchemaFeature(t, "core/broken")

	_, ok := cache.schemaFor(&feature)
	if ok {
		t.Fatalf("schemaFor (first): expected ok=false on broken schema; got ok=true")
	}
	_, ok = cache.schemaFor(&feature)
	if ok {
		t.Fatalf("schemaFor (second): expected cached ok=false; got ok=true")
	}
	if cache.misses != 1 {
		t.Errorf("misses: want 1 (one compile attempt cached negatively), got %d", cache.misses)
	}
	if cache.hits != 1 {
		t.Errorf("hits: want 1 (second call hits the negative cache), got %d", cache.hits)
	}
}

func TestFeatureSchemaCache_NilCacheIsCompileOnEveryCall(t *testing.T) {
	t.Parallel()
	var cache *featureSchemaCache // nil
	feature := featureWithSchema(t, "core/db-pool", `
values:
  db_name:
    type: string
`)
	// Nil receiver falls through to direct compactform.Compile;
	// no counters available but the call must succeed.
	if _, ok := cache.schemaFor(&feature); !ok {
		t.Errorf("schemaFor on nil cache: expected successful compile; got ok=false")
	}
	if _, ok := cache.schemaFor(&feature); !ok {
		t.Errorf("schemaFor on nil cache (second call): expected successful compile; got ok=false")
	}
}

func TestFeatureSchemaCache_NilFeatureReturnsNegativeNoCounterChange(t *testing.T) {
	t.Parallel()
	cache := newFeatureSchemaCache(nil)
	if _, ok := cache.schemaFor(nil); ok {
		t.Errorf("schemaFor(nil): expected ok=false; got ok=true")
	}
	if cache.misses != 0 || cache.hits != 0 {
		t.Errorf("nil feature should not touch counters; got misses=%d hits=%d", cache.misses, cache.hits)
	}
}
