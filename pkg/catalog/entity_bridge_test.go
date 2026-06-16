// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

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
  provider: compose
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
  provider: compose
  implements: "core/secret-value"
`
)

// recipeBundle wraps an entity YAML as a tar.gz with the root `recipe.yaml`
// anchor — the on-the-wire shape Recipe / FeatureRecipe kinds publish in (and the
// cache stores), so the bridge test exercises the real unbundle path rather than
// a plain-YAML stand-in.
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

// TestMergeExtraEntities_FoldsAllCoreKinds proves the registry-entity bridge folds
// every core kind into the in-memory catalog with the loader's identity keying —
// plain-YAML Components/Features and tar.gz-bundled Recipes/FeatureRecipes,
// including the provider-scoped recipe key — so the resolver guard layer is not
// blind in a registry build. It fails if a kind is dropped or a recipe loses its
// provider scope.
func TestMergeExtraEntities_FoldsAllCoreKinds(t *testing.T) {
	c := makeEmptyCatalog()
	recipeBlob := recipeBundle(t, bridgeRecipeYAML)
	frBlob := recipeBundle(t, bridgeFeatureRecipeYAML)

	warns, err := c.MergeExtraEntities(map[string][]byte{
		"core/datastore/mariadb":      []byte(bridgeComponentYAML),
		"core/secret-value":           []byte(bridgeFeatureYAML),
		"compose/mariadb:1.0.0":       recipeBlob,
		"compose/dotenv-secret:1.0.0": frBlob,
	})
	if err != nil {
		t.Fatalf("MergeExtraEntities: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}

	if _, ok, _ := c.resolveComponentKey("core/datastore/mariadb"); !ok {
		t.Errorf("Component not folded into cat.Components")
	}
	if _, ok, _ := c.resolveFeatureKey("core/secret-value"); !ok {
		t.Errorf("Feature not folded into cat.Features")
	}
	// Provider-scoped recipe keys — a dropped provider would key as "mariadb:1.0.0"
	// and these lookups would miss.
	if _, ok := c.Recipes[recipeIdentityKey("compose", "mariadb", "1.0.0")]; !ok {
		t.Errorf("Recipe not folded at provider-scoped key compose/mariadb:1.0.0")
	}
	if _, ok := c.FeatureRecipes[recipeIdentityKey("compose", "dotenv-secret", "1.0.0")]; !ok {
		t.Errorf("FeatureRecipe not folded at provider-scoped key compose/dotenv-secret:1.0.0")
	}
	// Component entityBytes are the exact plain-YAML bytes.
	if got := c.entityBytes[entityBytesKey("Component", "core/datastore/mariadb")]; string(got) != bridgeComponentYAML {
		t.Errorf("Component entityBytes are not the exact verified bytes")
	}
	// Recipe entityBytes are the WHOLE bundle blob (lockfile-drift pin is over the
	// bundle, not the extracted recipe.yaml), not the parsed YAML.
	if got := c.entityBytes[entityBytesKey("Recipe", recipeIdentityKey("compose", "mariadb", "1.0.0"))]; !bytes.Equal(got, recipeBlob) {
		t.Errorf("Recipe entityBytes are not the exact bundle blob")
	}
}

// TestMergeExtraEntities_LocalWins proves a locally-authored entity shadows a
// lockfile-pinned one of the same identity, with a warning — matching the
// library/aggregate bridges' discipline.
func TestMergeExtraEntities_LocalWins(t *testing.T) {
	c := makeEmptyCatalog()
	if err := c.indexFile([]byte(bridgeComponentYAML), "local/component.yaml"); err != nil {
		t.Fatalf("indexFile local: %v", err)
	}

	bridged := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "2.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
`
	warns, err := c.MergeExtraEntities(map[string][]byte{"core/datastore/mariadb": []byte(bridged)})
	if err != nil {
		t.Fatalf("MergeExtraEntities: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("want 1 local-wins warning; got %v", warns)
	}
	if got := c.entityBytes[entityBytesKey("Component", "core/datastore/mariadb")]; string(got) != bridgeComponentYAML {
		t.Errorf("local definition was overwritten by the bridged entity")
	}
}
