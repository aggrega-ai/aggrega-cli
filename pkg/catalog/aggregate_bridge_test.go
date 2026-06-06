// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func aggregateYAML(name string) []byte {
	return []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec: {}\n")
}

func emptyBridgeCatalog(ws string) *Catalog {
	return &Catalog{
		workspaceRoot:    ws,
		Aggregates:       map[string]types.Aggregate{},
		aggregateSources: map[string]string{},
		entityBytes:      map[string][]byte{},
	}
}

// TestMergeExtraAggregatesFolds proves a cache-bridged aggregate lands in
// cat.Aggregates + entityBytes (exact bytes), and resolves through BOTH the
// triad AggregateLookup and the topology SubAggregateLoader — with an honestly
// EMPTY source Path (decision 2: a cache-bridged aggregate has no workspace
// file; the resolve must produce valid output, not panic).
func TestMergeExtraAggregatesFolds(t *testing.T) {
	c := emptyBridgeCatalog("/ws")
	body := aggregateYAML("app")

	warns, err := c.MergeExtraAggregates(map[string][]byte{"hub.aggrega.ai/aggregate/example/app:1.0.0": body})
	if err != nil {
		t.Fatalf("MergeExtraAggregates: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if _, ok := c.Aggregates["app"]; !ok {
		t.Fatalf("aggregate not folded into cat.Aggregates")
	}
	if got := c.entityBytes[entityBytesKey("Aggregate", "app")]; string(got) != string(body) {
		t.Fatalf("entityBytes not the exact verified bytes")
	}

	// Triad AggregateLookup resolves it, Path honestly empty, Bytes carried.
	ra, ok := c.AggregateLookup()("app")
	if !ok {
		t.Fatalf("AggregateLookup miss for bridged aggregate")
	}
	if ra.Path != "" {
		t.Errorf("bridged aggregate Path should be empty (no workspace file); got %q", ra.Path)
	}
	if string(ra.Bytes) != string(body) {
		t.Errorf("AggregateLookup bytes mismatch")
	}

	// SubAggregateLoader resolves it with empty provenance — no panic, honest empty.
	agg, path, serr := c.SubAggregateLoader()("app")
	if serr != nil {
		t.Fatalf("SubAggregateLoader: %v", serr)
	}
	if agg.Metadata.Name != "app" {
		t.Errorf("SubAggregateLoader wrong aggregate: %q", agg.Metadata.Name)
	}
	if path != "" {
		t.Errorf("SubAggregateLoader provenance should be empty; got %q", path)
	}
}

// TestMergeExtraAggregatesSkipsNonAggregate confirms the kind dispatch leaves a
// pulled non-aggregate (a Component, and a tar.gz whose bytes are not YAML)
// untouched — the pre-existing gap stays out of scope.
func TestMergeExtraAggregatesSkipsNonAggregate(t *testing.T) {
	c := emptyBridgeCatalog("")
	component := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: api\n")
	tarish := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00} // gzip magic — not YAML

	warns, err := c.MergeExtraAggregates(map[string][]byte{
		"api:1.0.0":    component,
		"recipe:1.0.0": tarish,
	})
	if err != nil {
		t.Fatalf("MergeExtraAggregates: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(c.Aggregates) != 0 {
		t.Fatalf("non-aggregate kinds must not be folded; got %v", c.Aggregates)
	}
}

// TestMergeExtraAggregatesLocalWins confirms a locally-authored aggregate is not
// overwritten by a lockfile-pinned one of the same name; a warning is emitted.
func TestMergeExtraAggregatesLocalWins(t *testing.T) {
	c := emptyBridgeCatalog("")
	c.Aggregates["app"] = types.Aggregate{Metadata: types.AggregateMetadata{Name: "app", Version: "9.9.9"}}

	warns, err := c.MergeExtraAggregates(map[string][]byte{"app:1.0.0": aggregateYAML("app")})
	if err != nil {
		t.Fatalf("MergeExtraAggregates: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("expected one local-wins warning, got %v", warns)
	}
	if c.Aggregates["app"].Metadata.Version != "9.9.9" {
		t.Fatalf("local aggregate was overwritten by the bridged one")
	}
}

// TestReadPinnedEntity pins the verified cache read: a hit returns the bytes, a
// miss is ErrFetchNotFound (the caller's skip signal), a digest mismatch is
// ErrFetchIntegrity (loud). The slot is written through the same primitive the
// real puller uses, keyed `<uri>:<version>`.
func TestReadPinnedEntity(t *testing.T) {
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const versionedURI = "app:1.0.0"
	body := aggregateYAML("app")
	if err := writeEntityToCacheSlot(ws, cacheDir, versionedURI, "1.0.0", &oci.PullResult{
		MediaType: oci.MediaTypeAggregate,
		Bytes:     body,
	}); err != nil {
		t.Fatalf("writeEntityToCacheSlot: %v", err)
	}
	digest := contentDigest(body)

	got, err := ReadPinnedEntity(ws, cacheDir, "app", "1.0.0", digest)
	if err != nil {
		t.Fatalf("ReadPinnedEntity hit: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("ReadPinnedEntity returned wrong bytes")
	}

	if _, err := ReadPinnedEntity(ws, cacheDir, "absent", "1.0.0", digest); !errors.Is(err, oci.ErrFetchNotFound) {
		t.Fatalf("cache miss should be ErrFetchNotFound; got %v", err)
	}
	if _, err := ReadPinnedEntity(ws, cacheDir, "app", "1.0.0", "sha256:deadbeef"); !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Fatalf("digest mismatch should be ErrFetchIntegrity; got %v", err)
	}
}
