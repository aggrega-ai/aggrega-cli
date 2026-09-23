// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
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
// under the condition a build halts with, because one finding reported under two
// identities depending on which command found it is two answers to one question.
//
//   - An imported-file pin is compared with the digest of that file as it is on
//     disk now, computed by the function the pull records it with. That is the
//     same digest a build takes of the same bytes, so the two commands agree on
//     every file.
//   - A source pin is compared with the digest the lockfile records for that
//     source, which is the pair a build compares before it reads the cache.
//
// A pin the lock does not carry is not reported: a build has nothing to compare
// there either, so reporting it would refuse a workspace its build accepts. For a
// source this compares two records with each other, which is what a build's pin
// check does; whether the cached archive still digests to what the lockfile
// records is a separate check a build makes when it reads the cache, and it is not
// what this reports.
//
// # The identity a source pin is looked up under
//
// A source pin is found under the identity the lockfile row records, which is the
// identity the pull wrote it under. For every source whose resolver pins the
// authored version verbatim — every row carrying no authored reference — that is
// the identity a build looks the pin up under as well. For a row carrying an
// authored reference the pinned version is the commit the reference resolved to,
// which is not what the entity names, so this check compares a pin a build's own
// lookup does not reach. A disagreement there is still a lock a pull would
// rewrite, so it is reported rather than passed over.
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
					artifactPinDisagreement(locked.path, now.Name, entry.Import, now.Digest, pin.Digest))
			}
		}

		for _, row := range lf.SourceArtifacts {
			if !referencesEntity(row, ent.URI) {
				continue
			}
			pin, pinned := recipepin.MatchSourcePin(pins.SourceDigests, row.Kind, row.URL, row.Chart, row.ModuleSubdir, row.PinnedVersion)
			if !pinned || (pin.Digest == row.Digest && pin.ManifestDigest == row.ManifestDigest) {
				continue
			}
			report(locked.path+"\x00source\x00"+row.Kind+"\x00"+row.URL+"\x00"+row.Chart+"\x00"+row.ModuleSubdir+"\x00"+row.PinnedVersion,
				sourcePinDisagreement(recipepin.LockPath(locked.path), row, pin))
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
// It resolves through the loader and the path accessors the pull's own freeze uses,
// so the set of entities checked here is the set whose locks that freeze writes.
func lockedEntityFor(loader closure.RecipeLoader, lazyView *catalog.LazyLookup, uri string) (lockedEntity, bool) {
	if r, err := loader.LoadRecipe(uri); err == nil {
		path, ok := lazyView.LocalRecipePath(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version)
		if !ok {
			return lockedEntity{}, false
		}
		return lockedEntity{path: path, kind: "Recipe", name: r.Metadata.Name, artifacts: r.Spec.Artifacts}, true
	}
	if fr, err := loader.LoadFeatureRecipe(uri); err == nil {
		path, ok := lazyView.LocalFeatureRecipePath(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version)
		if !ok {
			return lockedEntity{}, false
		}
		return lockedEntity{path: path, kind: "FeatureRecipe", name: fr.Metadata.Name, artifacts: fr.Spec.Artifacts}, true
	}
	return lockedEntity{}, false
}

// referencesEntity reports whether a lockfile source row was resolved for the
// entity at uri. The join is on the back-pointer's URI because that is the join
// the pull uses when it decides which rows become which entity's source pins.
func referencesEntity(row lockfile.SourceArtifact, uri string) bool {
	for _, ref := range row.ReferencedBy {
		if ref.URI == uri {
			return true
		}
	}
	return false
}

// artifactPinDisagreement reports an imported file whose content no longer
// digests to the pin the lock beside its entity records.
//
// It is positioned where a build positions the same finding — at the artifacts
// block of the document that authors the import — and it names the imported file
// and both digests, which are the three things a reader needs in order to tell a
// changed file from a changed pin.
func artifactPinDisagreement(documentPath, artifact, importedPath, current, recorded string) error {
	condition := renderer.ErrArtifactContentMismatch()
	return cerrors.Wrap(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: documentPath, Locator: ".spec.artifacts"},
		cerrors.ComposeMessage(condition, []string{
			fmt.Sprintf("%q", artifact),
			fmt.Sprintf("imported file %q", importedPath),
			fmt.Sprintf("its content digests to %q", current),
			fmt.Sprintf("the recorded pin is %q", recorded),
		}, "If the file was changed on purpose, run `aggrega pull --release <release>` to record its new "+
			"digest; otherwise restore the file"))
}

// sourcePinDisagreement reports a source pin in a lock that disagrees with the
// digest the lockfile records for the same source.
//
// It is positioned at the lock, because both remedies act on the lock: restoring
// the committed file, or a pull rewriting it from the same resolution the lockfile
// records. It names both values of whichever digest
// disagrees — the content digest, or the manifest digest where the content digests
// agree — since either file may be the one that moved, and two equal values would
// tell a reader nothing.
func sourcePinDisagreement(lockPath string, row lockfile.SourceArtifact, pin types.SourceDigest) error {
	which, pinned, recorded := "digest", pin.Digest, row.Digest
	if pin.Digest == row.Digest {
		which, pinned, recorded = "manifest digest", pin.ManifestDigest, row.ManifestDigest
	}
	condition := oci.ErrFetchIntegrity()
	return cerrors.Wrap(condition, cerrors.FetchError,
		cerrors.SourceLocation{File: lockPath},
		cerrors.ComposeMessage(condition, []string{
			fmt.Sprintf("source-artifact %s@%s", row.URL, row.PinnedVersion),
			fmt.Sprintf("the source pin recorded in %s has %s %s", recipepin.LockFileName, which, pinned),
			fmt.Sprintf("the lockfile records %s", recorded),
		}, "Restore "+recipepin.LockFileName+", or run `aggrega pull --release <release>` to rewrite it"))
}
