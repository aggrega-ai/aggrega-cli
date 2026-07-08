// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
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
func freezeRecipePins(lazyView *catalog.LazyLookup, entities []closure.ResolvedRef, srcArts []closure.SourceArtifact) error {
	srcByURI := map[string][]types.SourceDigest{}
	for _, sa := range srcArts {
		sd := types.SourceDigest{
			Kind:           sa.Kind,
			URL:            sa.URL,
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
			path, ok := lazyView.LocalRecipePath(r.Spec.Target, r.Metadata.Name, r.Metadata.Version)
			if !ok {
				continue
			}
			artPins, aerr := recipepin.ArtifactPinsForRecipe(r.Spec.Artifacts)
			if aerr != nil {
				return aerr
			}
			if _, werr := recipepin.WritePins(path, srcByURI[ent.URI], artPins); werr != nil {
				return werr
			}
			continue
		}
		if fr, err := loader.LoadFeatureRecipe(ent.URI); err == nil {
			path, ok := lazyView.LocalFeatureRecipePath(fr.Spec.Target, fr.Metadata.Name, fr.Metadata.Version)
			if !ok {
				continue
			}
			artPins, aerr := recipepin.ArtifactPinsForRecipe(fr.Spec.Artifacts)
			if aerr != nil {
				return aerr
			}
			if _, werr := recipepin.WritePins(path, srcByURI[ent.URI], artPins); werr != nil {
				return werr
			}
			continue
		}
	}
	return nil
}
