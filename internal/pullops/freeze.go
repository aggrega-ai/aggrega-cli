// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
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
//
// # Reporting instead of writing
//
// With reportOnly set, every recipe file is left exactly as it was and the bytes
// the freeze would have put there are returned alongside the digests, one entry
// per recipe whose bytes would MOVE — a recipe the freeze would leave
// byte-identical is not reported, because a report of it would name a file
// nothing was going to write.
//
// The digests are identical either way, and that is what makes the reported run
// describe the writing one: the post-freeze content digest is the digest of the
// post-freeze bytes, and those bytes are what the splice produces whether or not
// anybody puts them on disk.
func freezeRecipePins(lazyView *catalog.LazyLookup, entities []closure.ResolvedRef, srcArts []closure.SourceArtifact, reportOnly bool) (map[string]string, []dryrunreport.File, error) {
	postFreezeRecipeDigests := map[string]string{}
	var reported []dryrunreport.File
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
	// Several entity URIs can resolve to ONE recipe file — a recipe referenced by
	// its bare name and by its provider-qualified one is two entities over one
	// document — so the reported set is deduplicated by path as well as by URI.
	// The writing path needs no such guard, and that is why the duplicate is
	// invisible there: the second freeze of a file the first one has already
	// written finds its pins equal and writes nothing. A reporting run writes
	// nothing either time, so both passes find the file unfrozen and would name it
	// twice.
	reportedPaths := map[string]bool{}
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
			d, file, ferr := freezeOneRecipe(path, r.Spec.Artifacts, "Recipe", r.Metadata.Name, srcByURI[ent.URI], reportOnly)
			if ferr != nil {
				return nil, nil, ferr
			}
			if file != nil && !reportedPaths[path] {
				reportedPaths[path] = true
				reported = append(reported, *file)
			}
			if !uriShadowedByComponentOrFeature(lazyView, ent.URI) {
				postFreezeRecipeDigests[ent.URI] = d
			}
			continue
		}
		if fr, err := loader.LoadFeatureRecipe(ent.URI); err == nil {
			path, ok := lazyView.LocalFeatureRecipePath(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)
			if !ok {
				continue
			}
			d, file, ferr := freezeOneRecipe(path, fr.Spec.Artifacts, "FeatureRecipe", fr.Metadata.Name, srcByURI[ent.URI], reportOnly)
			if ferr != nil {
				return nil, nil, ferr
			}
			if file != nil && !reportedPaths[path] {
				reportedPaths[path] = true
				reported = append(reported, *file)
			}
			if !uriShadowedByComponentOrFeature(lazyView, ent.URI) {
				postFreezeRecipeDigests[ent.URI] = d
			}
			continue
		}
	}
	return postFreezeRecipeDigests, reported, nil
}

// freezeOneRecipe freezes one writable recipe file's intrinsic content pins and
// returns the file's post-freeze content digest, plus the bytes a reporting run
// would hand back for it — nil where the file would not move, and always nil on a
// writing run.
//
// The two kinds reach it through one body. They differ in the loader that found
// them and in the word their diagnostics name them by, and in nothing the freeze
// itself does — so a second copy of this would be two places for a reporting run
// to stop agreeing with a writing one.
//
// The digest is taken from the on-disk bytes on a writing run and from the
// would-be bytes on a reporting one. Those are the same bytes: a writing run put
// exactly what the splice produced on disk a moment earlier.
//
// It is computed for every recipe it freezes, including one whose URI also
// resolves to a Component or a Feature and whose digest the caller therefore
// discards. That costs one read of a file the freeze has just touched, and it
// buys the caller being the only place that knows about the shadowing rule —
// which is where that rule belongs, since it is a fact about what the LOCKFILE
// records rather than about what the freeze does to a file.
func freezeOneRecipe(path string, artifacts types.OrderedMap[types.ArtifactEntry], entityKind, entityName string, srcPins []types.SourceDigest, reportOnly bool) (string, *dryrunreport.File, error) {
	artPins, aerr := recipepin.ArtifactPinsForRecipe(artifacts, entityKind, entityName)
	if aerr != nil {
		return "", nil, aerr
	}
	if reportOnly {
		out, rewrites, perr := recipepin.PreviewPins(path, srcPins, artPins)
		if perr != nil {
			return "", nil, perr
		}
		if !rewrites {
			return digest.ContentDigest(out), nil, nil
		}
		return digest.ContentDigest(out), &dryrunreport.File{Path: path, Content: out}, nil
	}
	if _, werr := recipepin.WritePins(path, srcPins, artPins); werr != nil {
		return "", nil, werr
	}
	d, derr := fileContentDigest(path)
	if derr != nil {
		return "", nil, derr
	}
	return d, nil, nil
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
