// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// freezeRecipePins is the resolution-phase freeze: for every writable (Tier-1)
// Recipe and FeatureRecipe the closure reaches, it writes the recipe's intrinsic
// content pins — sourceDigests (from the resolved remote sources) and
// artifactDigests (from the $import artifact files) — into the recipe file,
// modelling the go.mod/go.sum discipline where resolution records the pins the
// author commits and the build verifies them.
//
// Source pins come from the source-descent result (already fetched + digested);
// artifact pins are computed here from the $import files on disk. The write is
// surgical and idempotent — a recipe whose declared sources and $import set are
// unchanged is left byte-identical. A recipe living in a content-addressed cache
// slot (registry-resolved) has no writable path and is skipped: it arrives
// pre-pinned from its own publish.
//
// Returns a map from recipe entity URI to the recipe file's POST-freeze content
// digest, for every writable recipe entity processed (recomputed even when the
// freeze left the file byte-identical, since the on-disk bytes are what the
// build re-hashes). The pull flow overlays this onto the matching lockfile entity
// so the recorded digest reflects the file as it stands after the freeze, not the
// resolution-time bytes captured before it. The overlay is keyed strictly by URI
// so a lockfile entity of another kind that happens to share the recipe's bare
// name is never touched.
func freezeRecipePins(lazyView *catalog.LazyLookup, entities []closure.ResolvedRef, srcArts []closure.SourceArtifact) (map[string]string, error) {
	postFreezeRecipeDigests := map[string]string{}
	srcByURI := map[string][]types.SourceDigest{}
	for _, sa := range srcArts {
		sd := types.SourceDigest{
			Kind:           sa.Kind,
			URL:            sa.URL,
			Chart:          sa.Chart,
			ModuleSubdir:   sa.ModuleSubdir,
			Version:        sa.PinnedVersion,
			Digest:         sa.Digest,
			ManifestDigest: sa.ManifestDigest,
		}
		for _, ref := range sa.ReferencedBy {
			srcByURI[ref.URI] = append(srcByURI[ref.URI], sd)
		}
	}

	loader := lazyView.ClosureRecipeLoader()
	seen := map[string]bool{}
	for _, ent := range entities {
		if seen[ent.URI] {
			continue
		}
		seen[ent.URI] = true

		if r, err := loader.LoadRecipe(ent.URI); err == nil {
			path, ok := lazyView.LocalRecipePath(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version)
			if !ok {
				continue
			}
			artPins, aerr := recipepin.ArtifactPinsForRecipe(r.Spec.Artifacts, "Recipe", r.Metadata.Name)
			if aerr != nil {
				return nil, aerr
			}
			if _, werr := recipepin.WritePins(path, srcByURI[ent.URI], artPins); werr != nil {
				return nil, werr
			}
			if !uriShadowedByComponentOrFeature(lazyView, ent.URI) {
				d, derr := fileContentDigest(path)
				if derr != nil {
					return nil, derr
				}
				postFreezeRecipeDigests[ent.URI] = d
			}
			continue
		}
		if fr, err := loader.LoadFeatureRecipe(ent.URI); err == nil {
			path, ok := lazyView.LocalFeatureRecipePath(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)
			if !ok {
				continue
			}
			artPins, aerr := recipepin.ArtifactPinsForRecipe(fr.Spec.Artifacts, "FeatureRecipe", fr.Metadata.Name)
			if aerr != nil {
				return nil, aerr
			}
			if _, werr := recipepin.WritePins(path, srcByURI[ent.URI], artPins); werr != nil {
				return nil, werr
			}
			if !uriShadowedByComponentOrFeature(lazyView, ent.URI) {
				d, derr := fileContentDigest(path)
				if derr != nil {
					return nil, derr
				}
				postFreezeRecipeDigests[ent.URI] = d
			}
			continue
		}
	}
	return postFreezeRecipeDigests, nil
}

// uriShadowedByComponentOrFeature reports whether a recipe URI also resolves to a
// Component or Feature. The closure resolves an entity URI component-first, then
// feature, then recipe — so when a bare recipe name is shared with a Component or
// Feature of the same identity, the lockfile entity for that URI tracks the
// shadowing Component/Feature bytes, not the recipe's. The freeze never edits a
// Component or Feature file, so a shadowed entity's recorded digest is already
// stable across the freeze and must NOT be overlaid with the recipe file's
// post-freeze digest — doing so would fabricate drift the build would then flag.
// Only a URI that resolves solely to the recipe carries the recipe's bytes in the
// lockfile, and only that entity needs the post-freeze overlay.
func uriShadowedByComponentOrFeature(lazyView *catalog.LazyLookup, uri string) bool {
	if _, ok := lazyView.Component(uri); ok {
		return true
	}
	if _, ok := lazyView.Feature(uri); ok {
		return true
	}
	return false
}

// fileContentDigest hashes a file's on-disk bytes with the same content-address
// spelling the closure and build-time drift verify use, so a post-freeze recipe
// digest computed here is byte-comparable with the digest the build recomputes
// from the same file.
func fileContentDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digest.ContentDigest(data), nil
}
