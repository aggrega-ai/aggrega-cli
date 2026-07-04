// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestLazyLookup_BuildPathSurfaces_FromPinnedEnumeration proves the build-path
// catalog surfaces the cutover needs resolve over the determinism-wired lazy
// lookup's enumeration — a cache-pinned aggregate / recipe through the pinned
// Tier-2 pass, a Tier-1-local provider through the catalog-dir walk — with the
// Tier-3 puller never called. These are the surfaces a later build cutover repoints
// from the eager catalog; proving them in isolation keeps that cutover a mechanical
// swap.
func TestLazyLookup_BuildPathSurfaces_FromPinnedEnumeration(t *testing.T) {
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	recipeBlob := recipeBundle(t, bridgeRecipeYAML) // compose/mariadb, implements core/datastore/mariadb

	const (
		aggSlot   = "example/app:1.0.0"
		recipeURI = "compose/mariadb:1.0.0"
	)
	pins := map[string]Pin{
		recipeURI: writePinnedSlot(t, ws, cacheDir, recipeURI, oci.MediaTypeRecipe, recipeBlob),
	}
	// The aggregate is pinned BARE (referenced by bare name via AggregateLookup),
	// with a separate ResolvedVersion and a versioned slot — the real lockfile form.
	pins["example/app"] = writePinnedSlot(t, ws, cacheDir, aggSlot, oci.MediaTypeAggregate, []byte(pinnedAggregateYAML))

	// An empty local catalog dir (Tier-1) — the build-path surfaces below resolve
	// from the pinned Tier-2 cache.
	catalogDir := filepath.Join(ws, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	puller := &pinCacheCountingPuller{}
	lazy := NewLazyLookup(ws, cacheDir, []string{catalogDir}, puller)
	lazy.SetPins(pins)

	// 1. AggregateLookup resolves the cache-pinned (Tier-2) aggregate — reads the
	//    pinned enumeration, not a re-resolve (puller asserted zero below).
	agg, ok := lazy.AggregateLookup()("example/app")
	if !ok {
		t.Fatalf("AggregateLookup did not resolve the pinned aggregate example/app")
	}
	if agg.Aggregate == nil || agg.Aggregate.Metadata.Name != "example/app" {
		t.Errorf("AggregateLookup resolved the wrong aggregate: %+v", agg.Aggregate)
	}

	// 2. AssertManifestImplementsConsistency passes when the mapped type matches the
	//    pinned recipe's implements, and fails when it does not.
	consistent := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: func() *types.MappingTier {
		m := &types.MappingTier{}
		m.Type.Set("core/datastore/mariadb", "compose/mariadb:1.0.0")
		return m
	}()}}}
	if err := lazy.AssertManifestImplementsConsistency(consistent, "manifest.yaml"); err != nil {
		t.Errorf("consistency must pass (recipe resolved from the pinned enumeration, implements matches): %v", err)
	}
	inconsistent := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: func() *types.MappingTier {
		m := &types.MappingTier{}
		m.Type.Set("core/datastore/WRONG", "compose/mariadb:1.0.0")
		return m
	}()}}}
	if err := lazy.AssertManifestImplementsConsistency(inconsistent, "manifest.yaml"); err == nil {
		t.Errorf("consistency must fail when the mapped type does not match the recipe's implements")
	}

	// 3. VerifyRecipeResolution wires to the enumeration's completeness producer —
	//    an empty graph has no instances, so it is a clean nil.
	if err := lazy.VerifyRecipeResolution(consistent, &types.IRGraph{}); err != nil {
		t.Errorf("VerifyRecipeResolution over an empty graph must be nil; got %v", err)
	}

	// Every read came from Tier-1 (catalog dir) or pinned Tier-2 (cache) — never a
	// Tier-3 fetch. A determinism-wired build must not network.
	if puller.calls != 0 {
		t.Errorf("Tier-3 puller called %d time(s); build-path surfaces must read the enumeration, not re-resolve", puller.calls)
	}
}
