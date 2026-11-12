// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
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
// Every class is read through the record a build reads — the pins the catalog
// load filed under the entity's document — and each disagreement is reported
// under the condition a build halts with, in the words a build uses, because one
// finding reported two ways depending on which command found it is two answers to
// one question. No diagnostic is composed here: each comes from the one home
// the build's own comparison uses.
//
//   - An imported-file pin is compared with the digest of that file as it is on
//     disk now, computed by the function the pull records it with. That is the
//     same digest a build takes of the same bytes, so the two commands agree on
//     every file. The finding is renderer.ArtifactContentPinMismatch's.
//   - A local source's pins are compared with the pins its files digest to now,
//     read through the catalog's declared set as the pin step and a build read
//     them, by recipepin.CheckLocalSourcePins, the comparison a build makes before
//     a provider reads the source. They are compared only where the entity
//     declares a local source — catalog.LocalSource.RecordedPinsInForce, which
//     the pull holding the lock to the pin step reads too — because a build
//     compares them only over a source it reads.
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

	// Each entity is examined as the kind it was collected as: a Recipe and a
	// FeatureRecipe of one name are two entities, each held to its own lock.
	seen := map[closure.EntityKey]bool{}
	for _, ent := range entities {
		key := ent.Key()
		if seen[key] {
			continue
		}
		seen[key] = true

		locked, ok := entitypin.LocalEntityFor(lazyView, loader, key)
		if !ok {
			continue
		}
		pins := recorded.For(locked.Path)
		if pins.Empty() {
			continue
		}

		if len(pins.ArtifactDigests) > 0 {
			current, err := recipepin.ArtifactPinsForRecipe(locked.Artifacts, locked.Kind, locked.Name)
			if err != nil {
				return err
			}
			for _, now := range current {
				pin, pinned := recipepin.MatchArtifactPin(pins.ArtifactDigests, now.Name)
				if !pinned || pin.Digest == now.Digest {
					continue
				}
				entry, _ := locked.Artifacts.Get(now.Name)
				report(locked.Path+"\x00artifact\x00"+now.Name,
					renderer.ArtifactContentPinMismatch(locked.Path, now.Name, entry.Import, now.Digest, pin.Digest))
			}
		}

		if len(pins.LocalSourceDigests) > 0 {
			local, err := catalog.LocalSourceAt(locked.Path)
			if err != nil {
				return err
			}
			if err := recipepin.CheckLocalSourcePins(locked.Path, local.RecordedPinsInForce(pins.LocalSourceDigests), local.Pins); err != nil {
				report(locked.Path+"\x00local-source", err)
			}
		}

		for _, row := range lf.SourceArtifacts {
			if !reachedAtItsAuthoredVersion(row, key) {
				continue
			}
			if err := recipepin.CheckSourcePin(pins.SourceDigests, lf, recipepin.LockPath(locked.Path),
				row.Kind, row.URL, row.Chart, row.ModuleSubdir, row.PinnedVersion, ""); err != nil {
				report(locked.Path+"\x00source\x00"+row.Kind+"\x00"+row.URL+"\x00"+row.Chart+"\x00"+row.ModuleSubdir+"\x00"+row.PinnedVersion, err)
			}
		}
	}
	return errors.Join(findings...)
}

// reachedAtItsAuthoredVersion reports whether a lockfile source row was resolved
// for the entity key names through a back-pointer that records no authored
// reference — the one case in which the row's pinned version is the version the
// entity itself names, and so the case in which a build of the entity reaches the
// pin recorded under it.
//
// The join is on the back-pointer's entity kind and URI, the identity the pin step
// routes a source to the entity it belongs beside by, so a source a Recipe reached
// is never held to the lock of a FeatureRecipe of the same name. A row this entity
// reached only through an authored reference answers false: its pinned version is
// the commit that reference resolved to, and the version the entity names is not
// that commit.
func reachedAtItsAuthoredVersion(row lockfile.SourceArtifact, key closure.EntityKey) bool {
	for _, ref := range row.ReferencedBy {
		if ref.Entity == key.Kind && ref.URI == key.URI && ref.RequestedRef == "" {
			return true
		}
	}
	return false
}
