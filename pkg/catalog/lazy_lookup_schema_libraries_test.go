// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestLazyLookup_PrimeCarriesSchemaLibraries proves daemon/CLI IR parity for
// cross-library schema defaults. The daemon resolves Components/Features through
// the lazy lookup but loads an eager Catalog for RecipeLookup; it primes the lazy
// lookup from that eager Catalog. Priming must carry the eager Catalog's
// SchemaLibraries through AsCatalogLookup so the daemon's IR materializes
// cross-library defaults identically to the CLI (which uses
// Catalog.CatalogLookup directly). Without this, the daemon's /ir would serve
// content-divergent IR for cross-library Components.
func TestLazyLookup_PrimeCarriesSchemaLibraries(t *testing.T) {
	t.Parallel()
	cat := &Catalog{
		SchemaLibraries: map[string]types.SchemaLibrary{
			"BaseComponents": {Metadata: types.SchemaLibraryMetadata{Name: "BaseComponents"}},
		},
	}
	lazy := NewLazyLookup("", "", nil, nil)

	// Pre-prime: no libraries available.
	if before := lazy.AsCatalogLookup(); len(before.SchemaLibraries) != 0 {
		t.Fatalf("pre-prime lazy lookup must carry no SchemaLibraries; got %d", len(before.SchemaLibraries))
	}

	lazy.Prime(cat)

	cl := lazy.AsCatalogLookup()
	lib, ok := cl.SchemaLibraries["BaseComponents"]
	if !ok {
		t.Fatalf("primed lazy lookup must expose SchemaLibraries via AsCatalogLookup (daemon IR would otherwise omit cross-library defaults)")
	}
	if lib.Metadata.Name != "BaseComponents" {
		t.Errorf("library Metadata.Name = %q, want BaseComponents", lib.Metadata.Name)
	}
}
