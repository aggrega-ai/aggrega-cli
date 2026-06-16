// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	pinnedAggregateYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: example/app
  version: "1.0.0"
spec: {}
`
	pinnedMappingLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
`
	pinnedProviderYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: compose
  version: "1.0.0"
spec:
  baseSchema: |
    type: object
    additionalProperties: true
  workloadSchema: |
    type: object
    additionalProperties: true
`
)

// pinCacheCountingPuller is an OCIPuller that records every Pull. The determinism
// proof uses it to assert Tier-3 is never reached: a hermetic build's lazy lookup
// resolves the whole pinned closure from the Tier-2 cache, so the puller's call
// count must stay zero.
type pinCacheCountingPuller struct{ calls int }

func (p *pinCacheCountingPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	p.calls++
	return nil, oci.ErrFetchNotFound
}

// writePinnedSlot writes one entity's bytes to the kind-appropriate Tier-2 cache
// slot (library.yaml for libraries, blob for everything else) and returns its pin
// digest, mirroring exactly what `aggrega pull` lays down.
func writePinnedSlot(t *testing.T, ws, cacheDir, uri, mediaType string, blob []byte) string {
	t.Helper()
	_, version, _ := splitVersionedURI(uri)
	if err := writeEntityToCacheSlot(ws, cacheDir, uri, version, &oci.PullResult{MediaType: mediaType, Bytes: blob}); err != nil {
		t.Fatalf("write cache slot %s: %v", uri, err)
	}
	return contentDigest(blob)
}

// TestLazyLookup_PinnedCacheEnumeration_BridgeParity proves that a
// determinism-wired lazy lookup (nil puller + SetPins) resolves EVERY kind the
// cache→catalog bridge folds, from the pinned Tier-2 cache, byte-identically to the
// bridge. The set-level entityBytes comparison is the omission guard: a subject that
// silently drops a pinned recipe / aggregate / library the bridge folds fails it.
func TestLazyLookup_PinnedCacheEnumeration_BridgeParity(t *testing.T) {
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"

	recipeBlob := recipeBundle(t, bridgeRecipeYAML)
	frBlob := recipeBundle(t, bridgeFeatureRecipeYAML)

	// Versioned URIs: the Tier-2 slot read requires a version on the URI.
	const (
		compURI    = "core/datastore/mariadb:1.0.0"
		featURI    = "core/secret-value:1.0.0"
		recipeURI  = "compose/mariadb:1.0.0"
		frURI      = "compose/dotenv-secret:1.0.0"
		aggURI     = "example/app:1.0.0"
		schemaURI  = "DbSizing:1.0.0"
		mappingURI = "BaseMappings:1.0.0"
		provURI    = "compose:1.0.0" // pinned in cache but NOT folded by the bridge
	)

	pins := map[string]string{}
	pins[compURI] = writePinnedSlot(t, ws, cacheDir, compURI, oci.MediaTypeComponent, []byte(bridgeComponentYAML))
	pins[featURI] = writePinnedSlot(t, ws, cacheDir, featURI, oci.MediaTypeFeature, []byte(bridgeFeatureYAML))
	pins[recipeURI] = writePinnedSlot(t, ws, cacheDir, recipeURI, oci.MediaTypeRecipe, recipeBlob)
	pins[frURI] = writePinnedSlot(t, ws, cacheDir, frURI, oci.MediaTypeFeatureRecipe, frBlob)
	pins[aggURI] = writePinnedSlot(t, ws, cacheDir, aggURI, oci.MediaTypeAggregate, []byte(pinnedAggregateYAML))
	pins[schemaURI] = writePinnedSlot(t, ws, cacheDir, schemaURI, oci.MediaTypeSchemaLibrary, []byte(dbSizingLibYAML))
	pins[mappingURI] = writePinnedSlot(t, ws, cacheDir, mappingURI, oci.MediaTypeMappingLibrary, []byte(pinnedMappingLibYAML))
	pins[provURI] = writePinnedSlot(t, ws, cacheDir, provURI, oci.MediaTypeProvider, []byte(pinnedProviderYAML))

	// --- Bridge reference view (today's build path) ---
	bridge := makeEmptyCatalog()
	entityExtra := map[string][]byte{
		compURI:   []byte(bridgeComponentYAML),
		featURI:   []byte(bridgeFeatureYAML),
		recipeURI: recipeBlob,
		frURI:     frBlob,
		aggURI:    []byte(pinnedAggregateYAML),
		provURI:   []byte(pinnedProviderYAML),
	}
	if _, err := bridge.MergeExtraEntities(entityExtra); err != nil {
		t.Fatalf("bridge MergeExtraEntities: %v", err)
	}
	if _, err := bridge.MergeExtraAggregates(entityExtra); err != nil {
		t.Fatalf("bridge MergeExtraAggregates: %v", err)
	}
	// Libraries are keyed in the bridge by their lockfile URI, which is the bare
	// name (libraries are $ref'd by bare name) == metadata.name == the key the lazy
	// backing folds under. The cache slot + pin stay versioned (Tier-2 read needs
	// the version); the bridge map key is the bare name.
	if _, err := bridge.MergeExtraLibraries(
		map[string][]byte{"DbSizing": []byte(dbSizingLibYAML)},
		map[string][]byte{"BaseMappings": []byte(pinnedMappingLibYAML)},
	); err != nil {
		t.Fatalf("bridge MergeExtraLibraries: %v", err)
	}

	// --- Subject: determinism-wired lazy lookup (nil puller + pins, no catalog dirs) ---
	subject := NewLazyLookup(ws, cacheDir, nil, nil)
	subject.SetPins(pins)
	subject.buildRecipeIndex()     // Tier-1 (empty) + pin-driven Tier-2 for the recipe-shell kinds
	subject.buildSchemaLibraries() // Tier-1 (empty) + pin-driven Tier-2 for schema libraries
	if _, ok := subject.Component(compURI); !ok {
		t.Fatalf("subject did not resolve pinned Component %s from the cache", compURI)
	}
	if _, ok := subject.Feature(featURI); !ok {
		t.Fatalf("subject did not resolve pinned Feature %s from the cache", featURI)
	}

	// --- Set-level parity + byte-identity over entityBytes (the omission guard) ---
	// entityBytes is keyed entityBytesKey(kind, name) in BOTH views for every kind,
	// immune to the entity-map key-scheme differences (aggregates: name vs name:version).
	wantKeys := []string{
		entityBytesKey("Component", "core/datastore/mariadb"),
		entityBytesKey("Feature", "core/secret-value"),
		entityBytesKey("Recipe", recipeIdentityKey("compose", "mariadb", "1.0.0")),
		entityBytesKey("FeatureRecipe", recipeIdentityKey("compose", "dotenv-secret", "1.0.0")),
		entityBytesKey("Aggregate", "example/app"),
		entityBytesKey("SchemaLibrary", "DbSizing"),
		entityBytesKey("MappingLibrary", "BaseMappings"),
	}
	for _, k := range wantKeys {
		b, bok := bridge.entityBytes[k]
		s, sok := subject.entityBytes[k]
		if !bok {
			t.Fatalf("bridge reference is missing entityBytes %q (fixture/bridge wiring bug)", k)
		}
		if !sok {
			t.Errorf("subject OMITS %q the bridge folds — pinned-enumeration completeness gap", k)
			continue
		}
		if !bytes.Equal(b, s) {
			t.Errorf("entityBytes %q differ between bridge and subject — byte-identity gap", k)
		}
	}

	// --- Provider exclusion: the bridge folds no Provider, so neither must the subject ---
	// (a cache-pinned Provider is present in the cache + pins above, yet absent from both views).
	if _, ok := bridge.entityBytes[entityBytesKey("Provider", "compose")]; ok {
		t.Fatalf("bridge unexpectedly folded a Provider — premise changed")
	}
	if _, ok := subject.entityBytes[entityBytesKey("Provider", "compose")]; ok {
		t.Errorf("subject folded a cache-pinned Provider the bridge does not — divergence from the bridge surface")
	}
	if len(subject.providers) != 0 {
		t.Errorf("subject.providers must be empty (Provider is local-only, never bridged); got %v", subject.providers)
	}

	// --- Value parity on the enumerated entity maps (same key scheme in both views) ---
	if !reflect.DeepEqual(subject.recipes, bridge.Recipes) {
		t.Errorf("recipe maps differ:\n subject=%v\n bridge=%v", subject.recipes, bridge.Recipes)
	}
	if !reflect.DeepEqual(subject.featureRecipes, bridge.FeatureRecipes) {
		t.Errorf("feature-recipe maps differ")
	}
	if !reflect.DeepEqual(subject.mappingLibraries, bridge.MappingLibraries) {
		t.Errorf("mapping-library maps differ")
	}
	if !reflect.DeepEqual(subject.schemaLibraries, bridge.SchemaLibraries) {
		t.Errorf("schema-library maps differ")
	}
	// Aggregates: the lazy backing keys by metadata.name, the eager catalog by
	// name:version — normalise to name and compare values.
	bridgeAggsByName := map[string]types.Aggregate{}
	for _, a := range bridge.Aggregates {
		bridgeAggsByName[a.Metadata.Name] = a
	}
	if !reflect.DeepEqual(subject.aggregates, bridgeAggsByName) {
		t.Errorf("aggregate maps differ (compared by name)")
	}
}

// TestLazyLookup_PinnedCacheEnumeration_PullerNeverCalled proves the determinism
// contract directly: with every pinned entity present in the Tier-2 cache, the lazy
// lookup resolves the whole closure WITHOUT a single Tier-3 pull — no build-time
// network. A counting puller (not nil) makes a stray Tier-3 fetch observable.
func TestLazyLookup_PinnedCacheEnumeration_PullerNeverCalled(t *testing.T) {
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	recipeBlob := recipeBundle(t, bridgeRecipeYAML)

	const (
		compURI   = "core/datastore/mariadb:1.0.0"
		recipeURI = "compose/mariadb:1.0.0"
		schemaURI = "DbSizing:1.0.0"
	)
	pins := map[string]string{
		compURI:   writePinnedSlot(t, ws, cacheDir, compURI, oci.MediaTypeComponent, []byte(bridgeComponentYAML)),
		recipeURI: writePinnedSlot(t, ws, cacheDir, recipeURI, oci.MediaTypeRecipe, recipeBlob),
		schemaURI: writePinnedSlot(t, ws, cacheDir, schemaURI, oci.MediaTypeSchemaLibrary, []byte(dbSizingLibYAML)),
	}

	puller := &pinCacheCountingPuller{}
	subject := NewLazyLookup(ws, cacheDir, nil, puller)
	subject.SetPins(pins)
	subject.buildRecipeIndex()
	subject.buildSchemaLibraries()
	if _, ok := subject.Component(compURI); !ok {
		t.Fatalf("Component %s not resolved from cache", compURI)
	}

	if puller.calls != 0 {
		t.Errorf("Tier-3 puller was called %d time(s); a hermetic build resolves the pinned closure from cache only", puller.calls)
	}
}

// TestLazyLookup_NilPuller_CacheMissCleanMiss proves nil-puller safety: a pinned
// entity absent from the cache resolves to a clean miss (the build's loud-halt
// path), never a panic / nil-deref. This is the determinism wiring's failure mode
// that must NOT crash.
func TestLazyLookup_NilPuller_CacheMissCleanMiss(t *testing.T) {
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const absentURI = "core/never-cached:1.0.0"

	subject := NewLazyLookup(ws, cacheDir, nil, nil)
	subject.SetPins(map[string]string{absentURI: "sha256:" + "00000000000000000000000000000000000000000000000000000000000000"})

	if _, ok := subject.Component(absentURI); ok {
		t.Fatalf("a pinned-but-uncached Component must be a clean miss; got a hit")
	}
}
