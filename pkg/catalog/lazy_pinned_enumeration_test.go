// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// Cache-fixture entities for the lazy-enumeration tests. The library-referencing
// component carries a schema $ref; recipes/FRs publish as tar.gz bundles.
const (
	bridgeComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
`
	bridgeFeatureYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/secret-value
  version: "1.0.0"
  labels:
    feature.aggrega.ai/kind: secret
spec: {}
`
	bridgeRecipeYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mariadb
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/datastore/mariadb"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: demo:1
`
	bridgeFeatureRecipeYAML = `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: dotenv-secret
  version: "1.0.0"
spec:
  capability: compose
  implements: "core/secret-value"
`
	dbSizingLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`
)

// recipeBundle wraps an entity YAML as a tar.gz with the root recipe.yaml anchor —
// the on-the-wire shape Recipe / FeatureRecipe kinds publish in (and the cache
// stores), so the pinned-Tier-2 read exercises the real unbundle path.
func recipeBundle(t *testing.T, yaml string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "recipe.yaml", Size: int64(len(yaml)), Mode: 0o644}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(yaml)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

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
	pinnedWorkloadLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: BaseWorkloads
  version: 1.0.0
spec:
  workloads:
    SimpleService: 'kind: Deployment'
`
)

// pinCacheCountingPuller is an OCIPuller that records every Pull. The determinism
// proof uses it to assert Tier-3 is never reached: a hermetic build's lazy lookup
// resolves the whole pinned closure from the Tier-2 cache, so the puller's call
// count must stay zero.
type pinCacheCountingPuller struct{ calls int }

func (p *pinCacheCountingPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	p.calls++
	return nil, oci.ErrFetchNotFound()
}

// writePinnedSlot writes one entity's bytes to the kind-appropriate Tier-2 cache
// slot (library.yaml for libraries, blob for everything else), keyed by the
// VERSIONED slotURI exactly as `aggrega pull` lays it down, and returns the
// version-aware Pin. The caller decides the lockfile pin KEY (bare for an entity
// referenced by bare name — a sub-aggregate / library — versioned otherwise); the
// slot itself is always versioned, so a bare-URI pin must combine its URI with the
// returned ResolvedVersion to address it.
func writePinnedSlot(t *testing.T, ws, cacheDir, slotURI, mediaType string, blob []byte) Pin {
	t.Helper()
	_, version, _ := resolver.SplitVersionedURI(slotURI)
	mb, md := manifestBytesFor(t, mediaType, blob)
	if err := writeEntityToCacheSlot(ws, cacheDir, slotURI, version, &oci.PullResult{MediaType: mediaType, Bytes: blob, ManifestBytes: mb, Digest: md}); err != nil {
		t.Fatalf("write cache slot %s: %v", slotURI, err)
	}
	return Pin{ResolvedVersion: version, Digest: rowDigestOf(blob), ManifestDigest: md}
}

// rowDigestOf returns the digest a lockfile row records for published bytes, taken
// through the product's own rule (PinDigest) so a fixture's pin cannot fall out of
// step with it: for an entity, the digest of the entries the publication carries;
// for a library or an executable, the digest of the bytes.
//
// Bytes no entity can be read out of — an archive that does not unpack, or one
// whose root holds no entity document or several — have no row digest, and no
// pull could record one for them. A fixture seeding such a slot still needs a pin
// to hold it to, so it pins the bytes' own digest: the verified read refuses the
// slot while taking the row digest, before any pin is compared, so the value is
// never read.
func rowDigestOf(blob []byte) string {
	if d, err := PinDigest(blob); err == nil {
		return d
	}
	return contentDigest(blob)
}

// TestLazyLookup_PinnedCacheEnumeration_Resolves proves that a determinism-wired
// lazy lookup (nil puller + SetPins) resolves EVERY bridged kind from the pinned
// Tier-2 cache, byte-identically to the directly-constructed fixture bytes. The
// entityBytes comparison is the omission guard: a subject that silently drops a
// pinned recipe / aggregate / library fails it. The Aggregate + SchemaLibrary are
// pinned BARE (versioned slot, separate ResolvedVersion) — the bare-URI arm.
func TestLazyLookup_PinnedCacheEnumeration_Resolves(t *testing.T) {
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"

	recipeBlob := recipeBundle(t, bridgeRecipeYAML)
	frBlob := recipeBundle(t, bridgeFeatureRecipeYAML)

	// Cache slots are always keyed by the VERSIONED URI. The lockfile pin KEY,
	// however, mirrors the reference form: a component / recipe referenced
	// versioned pins versioned; an aggregate / library referenced by bare name
	// pins BARE with a separate ResolvedVersion (the real lockfile form — the
	// case that strands a version-less pin against a versioned slot).
	const (
		compSlot     = "core/datastore/mariadb:1.0.0"
		featSlot     = "core/secret-value:1.0.0"
		recipeSlot   = "compose/mariadb:1.0.0"
		frSlot       = "compose/dotenv-secret:1.0.0"
		aggSlot      = "example/app:1.0.0"
		schemaSlot   = "DbSizing:1.0.0"
		mappingSlot  = "BaseMappings:1.0.0"
		workloadSlot = "BaseWorkloads:1.0.0"
	)

	pins := map[string]Pin{}
	pins[compSlot] = writePinnedSlot(t, ws, cacheDir, compSlot, oci.MediaTypeComponent, []byte(bridgeComponentYAML))
	pins[featSlot] = writePinnedSlot(t, ws, cacheDir, featSlot, oci.MediaTypeFeature, []byte(bridgeFeatureYAML))
	pins[recipeSlot] = writePinnedSlot(t, ws, cacheDir, recipeSlot, oci.MediaTypeRecipe, recipeBlob)
	pins[frSlot] = writePinnedSlot(t, ws, cacheDir, frSlot, oci.MediaTypeFeatureRecipe, frBlob)
	// BARE-URI pins (slot stays versioned) — the real lockfile form for a
	// bare-`type` aggregate reference and a bare-name library $ref.
	pins["example/app"] = writePinnedSlot(t, ws, cacheDir, aggSlot, oci.MediaTypeAggregate, []byte(pinnedAggregateYAML))
	pins["DbSizing"] = writePinnedSlot(t, ws, cacheDir, schemaSlot, oci.MediaTypeSchemaLibrary, []byte(dbSizingLibYAML))
	pins[mappingSlot] = writePinnedSlot(t, ws, cacheDir, mappingSlot, oci.MediaTypeMappingLibrary, []byte(pinnedMappingLibYAML))
	// BARE-URI workload-library pin (slot versioned) — the B→A bridge: a
	// lockfile-pinned remote WorkloadLibrary folds into the enumerated set.
	pins["BaseWorkloads"] = writePinnedSlot(t, ws, cacheDir, workloadSlot, oci.MediaTypeWorkloadLibrary, []byte(pinnedWorkloadLibYAML))

	// --- Subject: determinism-wired lazy lookup (nil puller + pins, no catalog dirs) ---
	subject := NewLazyLookup(ws, cacheDir, nil, nil)
	subject.SetPins(pins)
	subject.buildRecipeIndex()     // Tier-1 (empty) + pin-driven Tier-2 for the recipe-shell kinds
	subject.buildSchemaLibraries() // Tier-1 (empty) + pin-driven Tier-2 for schema libraries
	if _, ok := subject.Component(compSlot); !ok {
		t.Fatalf("subject did not resolve pinned Component %s from the cache", compSlot)
	}
	if _, ok := subject.Feature(featSlot); !ok {
		t.Fatalf("subject did not resolve pinned Feature %s from the cache", featSlot)
	}

	// --- Byte-identity against the DIRECT fixture bytes (the omission guard,
	// bridge-free). entityBytes is keyed entityBytesKey(kind, index key): a subject
	// that drops a pinned recipe / aggregate / library — or retains drifted bytes —
	// fails here. The Aggregate + SchemaLibrary are pinned BARE (versioned slot,
	// separate ResolvedVersion), so their presence is the bare-URI determinism arm
	// (the case the versionedSlotURI fix exists for). ---
	wantBytes := map[string][]byte{
		entityBytesKey("Component", entityIndexKey("core/datastore/mariadb", "1.0.0")):          []byte(bridgeComponentYAML),
		entityBytesKey("Feature", entityIndexKey("core/secret-value", "1.0.0")):                 []byte(bridgeFeatureYAML),
		entityBytesKey("Recipe", recipeIdentityKey("compose", "mariadb", "1.0.0")):              recipeBlob,
		entityBytesKey("FeatureRecipe", recipeIdentityKey("compose", "dotenv-secret", "1.0.0")): frBlob,
		entityBytesKey("Aggregate", entityIndexKey("example/app", "1.0.0")):                     []byte(pinnedAggregateYAML),
		entityBytesKey("SchemaLibrary", "DbSizing"):                                             []byte(dbSizingLibYAML),
		entityBytesKey("MappingLibrary", "BaseMappings"):                                        []byte(pinnedMappingLibYAML),
		entityBytesKey("WorkloadLibrary", "BaseWorkloads"):                                      []byte(pinnedWorkloadLibYAML),
	}
	for k, want := range wantBytes {
		got, ok := subject.entityBytes[k]
		if !ok {
			t.Errorf("subject OMITS %q from the pinned closure — enumeration completeness gap", k)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("entityBytes %q is not the pinned fixture bytes — byte-identity gap", k)
		}
	}

	// The bare-pinned Aggregate + SchemaLibrary resolved into the enumerated maps —
	// a version-less pin correctly addressing the versioned slot.
	if _, ok := subject.aggregates[entityIndexKey("example/app", "1.0.0")]; !ok {
		t.Errorf("bare-URI-pinned Aggregate example/app not folded into the enumerated set")
	}
	if _, ok := subject.schemaLibraries["DbSizing"]; !ok {
		t.Errorf("bare-URI-pinned SchemaLibrary DbSizing not folded into the enumerated set")
	}
	// The bare-pinned remote WorkloadLibrary folded into the enumerated set the
	// renderer's workload `$import` resolution consumes — the B→A bridge.
	if _, ok := subject.workloadLibraries["BaseWorkloads"]; !ok {
		t.Errorf("bare-URI-pinned WorkloadLibrary BaseWorkloads not folded into the enumerated set (B→A bridge)")
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
	pins := map[string]Pin{
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
	subject.SetPins(map[string]Pin{absentURI: {ResolvedVersion: "1.0.0", Digest: "sha256:" + "00000000000000000000000000000000000000000000000000000000000000"}})

	if _, ok := subject.Component(absentURI); ok {
		t.Fatalf("a pinned-but-uncached Component must be a clean miss; got a hit")
	}
}
