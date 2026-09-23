// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// verifyEntityLocks is the verify path's check over the entity.lock files beside
// the Recipes and FeatureRecipes this Release reaches: it reports every pin such a
// lock records that a build of the Release would refuse.
//
// # Why the lockfile comparison does not already cover it
//
// The lockfile records an entity by the digest of the entity's own document, and
// the pins live in a separate file beside it. So a pin edited in the lock, or an
// imported file edited after the pull that pinned it, moves nothing the lockfile
// comparison reads — while a build reads both and halts on either. A verify that
// stopped at the lockfile would report a match over a workspace whose build fails.
//
// # What each pin is compared against, and under which condition
//
// Both classes are read through the record a build reads — the pins the catalog
// load filed under the entity's document — and each disagreement is reported
// under the condition a build halts with, in the words a build uses, because one
// finding reported two ways depending on which command found it is two answers to
// one question. Neither diagnostic is composed here: each comes from the one home
// the build's own comparison uses.
//
//   - An imported-file pin is compared with the digest of that file as it is on
//     disk now, computed by the function the pull records it with. That is the
//     same digest a build takes of the same bytes, so the two commands agree on
//     every file. The finding is renderer.ArtifactContentPinMismatch's.
//   - A source pin is compared with the digest the lockfile records for that
//     source by recipepin.CheckSourcePin, which is the comparison a build makes
//     before it reads the cache.
//
// A pin the lock does not carry is not reported: a build has nothing to compare
// there either, so reporting it would refuse a workspace its build accepts. For a
// source this compares two records with each other, which is what a build's pin
// check does; whether the cached archive still digests to what the lockfile
// records is a separate check a build makes when it reads the cache, and it is not
// what this reports.
//
// # The coordinates a source pin is looked up under
//
// A build looks a source pin up under the coordinates the entity authors —
// crucially, the version it writes — and a pull records the pin under the version
// the resolution pinned. The two are the same version exactly when the resolution
// pinned what the author wrote, and a lockfile back-pointer says which case it
// is: one recording no authored reference was resolved by pinning the authored
// version as written, so the row's pinned version is the version the entity
// names; one recording an authored reference was resolved to the commit that
// reference named, which the entity does not name.
//
// So each row is asked about through each of this entity's back-pointers, and
// only a back-pointer recording no authored reference hands the comparison the
// row's coordinates. One recording a reference is passed over, because a build of
// the same entity looks its pin up under a version that is not that commit and
// compares nothing there — reporting it would refuse a workspace its build
// accepts.
//
// Only an entity read from a document in a workspace catalog directory has a lock,
// so an entity resolved from a registry is passed over here exactly as the pull
// that writes locks passes it over.
func verifyEntityLocks(lf *lockfile.Lockfile, lazyView *catalog.LazyLookup, entities []closure.ResolvedRef) error {
	recorded := lazyView.RecordedPins()
	loader := lazyView.ClosureRecipeLoader()

	var findings []error
	// Several entity URIs can name one document — a Recipe referenced by its bare
	// name and by its provider-qualified one — and each carries its own lockfile
	// back-pointers, so every URI is examined while each finding is reported once.
	reported := map[string]bool{}
	report := func(key string, err error) {
		if reported[key] {
			return
		}
		reported[key] = true
		findings = append(findings, err)
	}

	seen := map[string]bool{}
	for _, ent := range entities {
		if seen[ent.URI] {
			continue
		}
		seen[ent.URI] = true

		locked, ok := lockedEntityFor(loader, lazyView, ent.URI)
		if !ok {
			continue
		}
		pins := recorded.For(locked.path)
		if pins.Empty() {
			continue
		}

		if len(pins.ArtifactDigests) > 0 {
			current, err := recipepin.ArtifactPinsForRecipe(locked.artifacts, locked.kind, locked.name)
			if err != nil {
				return err
			}
			for _, now := range current {
				pin, pinned := recipepin.MatchArtifactPin(pins.ArtifactDigests, now.Name)
				if !pinned || pin.Digest == now.Digest {
					continue
				}
				entry, _ := locked.artifacts.Get(now.Name)
				report(locked.path+"\x00artifact\x00"+now.Name,
					renderer.ArtifactContentPinMismatch(locked.path, now.Name, entry.Import, now.Digest, pin.Digest))
			}
		}

		for _, row := range lf.SourceArtifacts {
			if !reachedAtItsAuthoredVersion(row, ent.URI) {
				continue
			}
			if err := recipepin.CheckSourcePin(pins.SourceDigests, lf, recipepin.LockPath(locked.path),
				row.Kind, row.URL, row.Chart, row.ModuleSubdir, row.PinnedVersion, ""); err != nil {
				report(locked.path+"\x00source\x00"+row.Kind+"\x00"+row.URL+"\x00"+row.Chart+"\x00"+row.ModuleSubdir+"\x00"+row.PinnedVersion, err)
			}
		}
	}
	return errors.Join(findings...)
}

// lockedEntity is one entity a lock can sit beside: the document it was read
// from, and what the pin comparison needs to know about it.
type lockedEntity struct {
	path      string
	kind      string
	name      string
	artifacts types.OrderedMap[types.ArtifactEntry]
}

// lockedEntityFor resolves a closure URI to the Recipe or FeatureRecipe document
// the pull writes a lock beside, and reports false for anything else — another
// kind, or an entity with no document in a workspace catalog directory.
//
// It resolves through the loader and the writable-home predicate the pin step uses,
// so the set of entities checked here is the set whose locks that step writes.
func lockedEntityFor(loader closure.RecipeLoader, lazyView *catalog.LazyLookup, uri string) (lockedEntity, bool) {
	if r, err := loader.LoadRecipe(uri); err == nil {
		path, ok := entitypin.WritableHome(lazyView, "Recipe", r.Spec.Capability, r.Metadata.Name, r.Metadata.Version)
		if !ok {
			return lockedEntity{}, false
		}
		return lockedEntity{path: path, kind: "Recipe", name: r.Metadata.Name, artifacts: r.Spec.Artifacts}, true
	}
	if fr, err := loader.LoadFeatureRecipe(uri); err == nil {
		path, ok := entitypin.WritableHome(lazyView, "FeatureRecipe", fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)
		if !ok {
			return lockedEntity{}, false
		}
		return lockedEntity{path: path, kind: "FeatureRecipe", name: fr.Metadata.Name, artifacts: fr.Spec.Artifacts}, true
	}
	return lockedEntity{}, false
}

// reachedAtItsAuthoredVersion reports whether a lockfile source row was resolved
// for the entity at uri through a back-pointer that records no authored reference
// — the one case in which the row's pinned version is the version the entity
// itself names, and so the case in which a build of the entity reaches the pin
// recorded under it.
//
// The join is on the back-pointer's URI because that is the join the pull uses
// when it decides which rows become which entity's source pins. A row this entity
// reached only through an authored reference answers false: its pinned version is
// the commit that reference resolved to, and the version the entity names is not
// that commit.
func reachedAtItsAuthoredVersion(row lockfile.SourceArtifact, uri string) bool {
	for _, ref := range row.ReferencedBy {
		if ref.URI == uri && ref.RequestedRef == "" {
			return true
		}
	}
	return false
}
