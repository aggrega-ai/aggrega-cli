// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nodeMappingKeyManifest is a Manifest whose components tier carries the type
// entries given, in order, and one name entry for the instance named "named".
func nodeMappingKeyManifest(typeEntries ...[2]string) *types.Manifest {
	tier := &types.MappingTier{}
	for _, e := range typeEntries {
		tier.Type.Set(e[0], e[1])
	}
	tier.Name.Set("named", "db-two:1.0.0")
	return &types.Manifest{
		Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: tier}},
	}
}

// TestTheInstanceResolutionCarriesTheKeyAndTheResolvedType holds the two
// coordinates the instance resolution gained to what the lookup actually did, on
// both catalog views a build reads, over a catalog holding app/db at 1.0.0 and
// 2.0.0:
//
//   - the type-axis key the lookup settled on, verbatim, whether it matched the
//     instance's type exactly or by its base, and no key for an instance a
//     name-axis entry reached or nothing reached;
//   - the instance's type at the version the build compiles it as, a bare type
//     resolved to 2.0.0, the highest version held, and a pinned type as written.
//
// 🔴 PLANTED DEFECT: in versionTolerantTypeGet (pkg/catalog/manifest_lookup.go),
// return lookupType as the key on the base-match arm: the "a key matched by its
// base" row reddens on both views, naming the instance's type instead of the key.
func TestTheInstanceResolutionCarriesTheKeyAndTheResolvedType(t *testing.T) {
	t.Parallel()
	dir := implementsRequestCatalog(t)
	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lazy := NewLazyLookup(dir, "", []string{dir}, nil)

	for _, view := range []struct {
		name    string
		resolve func(m *types.Manifest, instance, componentType string) resolver.NodeRecipeResolution
	}{
		{name: "the eager catalog", resolve: eager.CatalogLookup().NodeRecipeResolution},
		{name: "the lazy lookup", resolve: lazy.NodeRecipeResolution},
	} {
		for _, tc := range []struct {
			name          string
			manifest      *types.Manifest
			instance      string
			componentType string
			wantMapped    bool
			wantByType    bool
			wantKey       string
			wantResolved  string
		}{
			{
				name:          "an unversioned key reaching a pinned instance",
				manifest:      nodeMappingKeyManifest([2]string{"app/db", "db-one:1.0.0"}),
				instance:      "db",
				componentType: "app/db:2.0.0",
				wantMapped:    true, wantByType: true, wantKey: "app/db", wantResolved: "app/db:2.0.0",
			},
			{
				name:          "an unversioned key reaching a bare instance",
				manifest:      nodeMappingKeyManifest([2]string{"app/db", "db-one:1.0.0"}),
				instance:      "db",
				componentType: "app/db",
				wantMapped:    true, wantByType: true, wantKey: "app/db", wantResolved: "app/db:2.0.0",
			},
			{
				name:          "a key matched by its base",
				manifest:      nodeMappingKeyManifest([2]string{"app/db:1.0.0", "db-one:1.0.0"}),
				instance:      "db",
				componentType: "app/db:2.0.0",
				wantMapped:    true, wantByType: true, wantKey: "app/db:1.0.0", wantResolved: "app/db:2.0.0",
			},
			{
				name:          "a name-axis entry",
				manifest:      nodeMappingKeyManifest([2]string{"app/db", "db-one:1.0.0"}),
				instance:      "named",
				componentType: "app/db:2.0.0",
				wantMapped:    true, wantByType: false, wantKey: "", wantResolved: "app/db:2.0.0",
			},
			{
				name:          "an instance nothing maps",
				manifest:      nodeMappingKeyManifest([2]string{"app/other", "db-one:1.0.0"}),
				instance:      "db",
				componentType: "app/db",
				wantMapped:    false, wantByType: false, wantKey: "", wantResolved: "app/db:2.0.0",
			},
		} {
			t.Run(view.name+"/"+tc.name, func(t *testing.T) {
				got := view.resolve(tc.manifest, tc.instance, tc.componentType)
				if got.Mapped != tc.wantMapped || got.MappedByType != tc.wantByType || got.TypeKey != tc.wantKey {
					t.Errorf("mapped=%t byType=%t key=%q; want mapped=%t byType=%t key=%q",
						got.Mapped, got.MappedByType, got.TypeKey, tc.wantMapped, tc.wantByType, tc.wantKey)
				}
				if got.ResolvedType != tc.wantResolved {
					t.Errorf("resolved type = %q, want %q", got.ResolvedType, tc.wantResolved)
				}
			})
		}
	}
}

// TestTheBuildLookupsResolveAComponentsAncestry holds both catalog views' build
// lookups to supplying the extends ancestry the mapping-consistency gate walks,
// so a rule asked at a stage of the compilation judges a Recipe written against a
// base as that gate does. A lookup without one answers no ancestry at all, and the
// rule over an instance would then refuse every instance a base's Recipe serves.
//
// 🔴 PLANTED DEFECT: drop the ComponentAncestry field from LazyLookup's
// AsCatalogLookupContext (pkg/catalog/lazy_lookup.go): the lazy row reddens with
// no resolver at all.
func TestTheBuildLookupsResolveAComponentsAncestry(t *testing.T) {
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "base.yaml", ancestryBaseCompYAML)
	writeCatalogFile(t, cd, "mid.yaml", ancestryMidCompYAML)
	writeCatalogFile(t, cd, "leaf.yaml", ancestryLeafCompYAML)

	eager, err := LoadAll("", cd)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lazy := NewLazyLookup(ws, ".aggrega/cache", []string{cd}, nil)
	want, werr := lazy.componentAncestry("enterprise/ancestry/leaf:1.0.0")
	if werr != nil || len(want) != 2 {
		t.Fatalf("calibration: the gate's own walk must see the whole chain (bases=%v err=%v)", want, werr)
	}

	for _, tc := range []struct {
		name   string
		lookup resolver.CatalogLookup
	}{
		{name: "the eager catalog", lookup: eager.CatalogLookup()},
		{name: "the lazy lookup", lookup: lazy.AsCatalogLookup()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.lookup.ComponentAncestry == nil {
				t.Fatal("the build lookup supplies no ancestry resolver")
			}
			got, err := tc.lookup.ComponentAncestry("enterprise/ancestry/leaf:1.0.0")
			if err != nil {
				t.Fatalf("ancestry: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("ancestry = %v, want the gate's own %v", got, want)
			}
		})
	}
}
