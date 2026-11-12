// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package recipepin_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// writeImport writes one imported file and returns its absolute path, which is the
// form the catalog hands an entry after load.
func writeImport(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write import: %v", err)
	}
	return p
}

// TestABoundaryImportIsPinnedApartFromAnArtifactsImportOfTheSameName pins a Recipe
// whose artifacts block and boundary block each import a different file under the
// same key. The lock records two pins — one under the key, one under the boundary
// pin name — each carrying the digest of its own block's file, and each is found
// again by the one lookup a comparison uses. An inline entry in either block carries
// no pin.
func TestABoundaryImportIsPinnedApartFromAnArtifactsImportOfTheSameName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const ownBody = "# the component's own provider file\n"
	const sharedBody = "provider \"azurerm\" {\n  features {}\n}\n"
	ownPath := writeImport(t, dir, "own.tf", ownBody)
	sharedPath := writeImport(t, dir, "shared.tf", sharedBody)

	r := &types.Recipe{Metadata: types.RecipeMetadata{Name: "gateway", Version: "1.0.0"}}
	r.Spec.Artifacts.Set("providers.tf", types.ArtifactEntry{Import: ownPath})
	r.Spec.Artifacts.Set("main.tf", types.ArtifactEntry{Inline: "# inline\n"})
	r.Spec.Boundary.Artifacts.Set("providers.tf", types.ArtifactEntry{Import: sharedPath})
	r.Spec.Boundary.Artifacts.Set("versions.tf", types.ArtifactEntry{Inline: "# inline\n"})

	pins, err := recipepin.ArtifactPinsForRecipe(recipepin.PinnedArtifactEntries(r), "Recipe", r.Metadata.Name)
	if err != nil {
		t.Fatalf("ArtifactPinsForRecipe: %v", err)
	}
	if len(pins) != 2 {
		t.Fatalf("want one pin per imported file, two in all; got %+v", pins)
	}
	boundaryName := recipepin.BoundaryArtifactPinName("providers.tf")
	for _, want := range []struct{ name, body string }{
		{"providers.tf", ownBody},
		{boundaryName, sharedBody},
	} {
		pin, ok := recipepin.MatchArtifactPin(pins, want.name)
		if !ok {
			t.Errorf("no pin is recorded under %q: %+v", want.name, pins)
			continue
		}
		if pin.Digest != digest.ContentDigest([]byte(want.body)) {
			t.Errorf("the pin under %q carries %s, not the digest of its own block's file", want.name, pin.Digest)
		}
	}
	if boundaryName != "boundary/providers.tf" {
		t.Errorf("the boundary pin name is %q; the entity lock records it as boundary/providers.tf", boundaryName)
	}
	if key, ok := recipepin.BoundaryArtifactKeyOf(boundaryName); !ok || key != "providers.tf" {
		t.Errorf("BoundaryArtifactKeyOf(%q) = %q, %v; want the key it was composed from", boundaryName, key, ok)
	}
	if _, ok := recipepin.BoundaryArtifactKeyOf("providers.tf"); ok {
		t.Error("an artifacts key is read as a boundary pin name")
	}
}

// TestARecipeWithNoBoundaryBlockPinsItsArtifactsAsBefore holds the other cell: a
// Recipe declaring no boundary block presents exactly its own artifacts, so its lock
// records no name it did not record already.
func TestARecipeWithNoBoundaryBlockPinsItsArtifactsAsBefore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := &types.Recipe{Metadata: types.RecipeMetadata{Name: "server", Version: "1.0.0"}}
	r.Spec.Artifacts.Set("extra.yaml", types.ArtifactEntry{Import: writeImport(t, dir, "extra.yaml", "a: b\n")})

	got := recipepin.PinnedArtifactEntries(r)
	if keys := got.Keys(); len(keys) != 1 || keys[0] != "extra.yaml" {
		t.Errorf("a Recipe with no boundary block presents %v; want its own artifacts alone", keys)
	}
	if empty := recipepin.PinnedArtifactEntries(nil); empty.Len() != 0 {
		t.Errorf("no Recipe presents %d entries; want none", empty.Len())
	}
}
