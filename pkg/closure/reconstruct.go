// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"sort"
)

// ReconstructClosureAxes rebuilds the sub-aggregate and pipeline-plugin axes of an
// already-computed closure from the pinned cache alone, with no registry fetch —
// the hermetic counterpart to the lock-time descent. A consumer that verifies a
// closure against an existing lockfile runs it so the closure it diffs carries the
// same sub-aggregate and plugin entries the lock-time closure pinned, giving the
// drift check an honest, non-empty expected set rather than the empty plugin and
// transitive-sub-aggregate axes ComputeClosure leaves.
//
// It runs the same primitives the lock-time descent orchestrates, in the same
// order: transitive sub-aggregate descent, then the two-pass pipeline-plugin
// descent. The entity descent (DescendRecipePluginEntities) pins each plugin
// ENTITY on the catalog-entity axis; the caller then re-folds the loader's pin
// set from the now-augmented closure via refold so the just-pinned entities are
// visible; the artifact descent
// (DescendRecipePluginArtifacts) loads each entity to pin its .wasm ARTIFACT on the
// plugin axis. The refold is caller-supplied because re-deriving a loader's pin set
// is a catalog-side operation this package does not reach; a nil refold reuses the
// same loader for both phases.
//
// providerResolver resolves each Recipe / FeatureRecipe spec.capability binding to
// its serving provider-rendering Plugin. A verifier passes a resolver that reads
// the lockfile's recorded bindings, so reconstruction stays invariant to a later
// workspace-config change. Every loader is nil-safe — a nil loader makes its pass
// a no-op — so a caller with no registry tier reconstructs nothing.
//
// The lock-time descent does not route through this wrapper: its descent is
// interleaved with lock-time-only passes (provider-binding writes, host-function
// verification, and source descent) that must run between the same primitives, so
// it orchestrates them inline. This function is the reconstruction path for the
// hermetic verifiers only; keep the two in sync deliberately rather than folding
// the lock-time path into it.
func ReconstructClosureAxes(
	cl *Closure,
	aggLoader AggregateLoader,
	recipeLoader RecipeLoader,
	lookup Lookup,
	providerResolver ProviderPluginResolver,
	refold func() (RecipeLoader, error),
) error {
	if err := DescendSubAggregates(cl, aggLoader, lookup); err != nil {
		return fmt.Errorf("sub-aggregate descent: %w", err)
	}
	if err := DescendRecipePluginEntities(cl, recipeLoader, lookup, providerResolver); err != nil {
		return fmt.Errorf("pipeline-plugin entity descent: %w", err)
	}
	artifactLoader := recipeLoader
	if refold != nil {
		loader2, err := refold()
		if err != nil {
			return fmt.Errorf("re-fold plugin pins: %w", err)
		}
		artifactLoader = loader2
	}
	if err := DescendRecipePluginArtifacts(cl, artifactLoader, lookup); err != nil {
		return fmt.Errorf("pipeline-plugin artifact descent: %w", err)
	}
	return nil
}

// ReconstructWorkspaceSocketEntities re-adds the output-shaping workspace-socket
// plugin ENTITIES and their .wasm ARTIFACTS a lockfile pinned but a hermetic
// closure walk cannot re-derive from config. The provider-rendering plugins bound
// through the provider selections and the mutator plugins attached through the
// mutator selections are chosen at pull and recorded ONLY in the lockfile, so a
// config-invariant build (and serve render) reconstructs their entity + plugin
// axes from the lockfile-known entity URIs rather than from live config — the same
// footing on which the render resolver reads the provider bindings from the
// lockfile.
//
// entityURIs is the set of socket plugin ENTITY URIs (<socket>/<name>:<version>)
// the lockfile pins. Each is resolved through the pinned cache (the Lookup carries
// a nil puller, so no registry fetch), pinned onto the catalog-entity axis with
// the plugin kind hint, then descended to its .wasm artifact on the plugin axis.
// A URI already present (a mutator entity ComputeClosure recorded on the entity
// axis) is not duplicated — its referenced-from unions and its kind hint is kept
// so the artifact descent still reaches it.
//
// Unlike DescendRecipePluginEntities, a Tier-1-resolved socket entity is NOT
// filtered out: a lockfile-pinned socket plugin folded into the hermetic view's
// local enumeration resolves as tier-1-catalog, and dropping it is precisely the
// reconstruction gap that surfaces as spurious "no longer used" drift. Integrity
// is unchanged: the entity bytes are re-hashed here and its .wasm content digest is
// re-read from the content-addressed cache slot the lockfile pin names, so a
// drifted cache slot or a tampered lockfile pin still surfaces (a missing slot
// leaves the axis short and drift fires; changed bytes fail the pinned-integrity
// gate or the render read).
//
// A NotFound entity or artifact is skipped rather than halting: the honest frozen
// build resolves both from the cache, and a genuinely-absent one surfaces as drift
// against the lockfile. A nil loader is a no-op.
func ReconstructWorkspaceSocketEntities(c *Closure, entityURIs []string, loader RecipeLoader, lookup Lookup) error {
	if c == nil || loader == nil || len(entityURIs) == 0 {
		return nil
	}

	entityIndex := make(map[string]int, len(c.Entities))
	for i, e := range c.Entities {
		entityIndex[e.URI] = i
	}

	sorted := append([]string(nil), entityURIs...)
	sort.Strings(sorted)

	const refPath = "release plugin socket"
	for _, entityURI := range sorted {
		res, err := lookup.ResolveCatalogEntity(entityURI, pluginKindHint)
		if err != nil {
			return fmt.Errorf("resolve: resolve workspace-socket plugin entity %q: %w", entityURI, err)
		}
		// A socket entity the pinned cache cannot serve is left off the reconstructed
		// axis; the drift verify then reports it against the lockfile rather than the
		// reconstruction masking the gap.
		if res.NotFound {
			continue
		}
		if idx, dup := entityIndex[entityURI]; dup {
			c.Entities[idx].ReferencedFrom = unionReferencedFrom(c.Entities[idx].ReferencedFrom, refPath)
			if c.Entities[idx].KindHint == "" {
				c.Entities[idx].KindHint = pluginKindHint
			}
			continue
		}
		c.Entities = append(c.Entities, ResolvedRef{
			URI:             entityURI,
			ResolvedVersion: res.ResolvedVersion,
			Digest:          res.Digest,
			ManifestDigest:  res.ManifestDigest,
			ResolutionPath:  res.ResolutionPath,
			ReferencedFrom:  []string{refPath},
			KindHint:        pluginKindHint,
		})
		entityIndex[entityURI] = len(c.Entities) - 1
	}
	canonicaliseClosure(c)

	pluginIndex := make(map[string]int, len(c.Plugins))
	for i, p := range c.Plugins {
		pluginIndex[p.URI] = i
	}
	for _, entityURI := range sorted {
		// A lockfile-pinned socket .wasm can resolve as locally-present when its pinned
		// cache slot is folded into the hermetic view's local enumeration, so the
		// registry-only rule is disabled — the artifact is pinned regardless of tier.
		if err := pinPluginArtifact(c, loader, lookup, entityURI, pluginIndex, false /* registryOnly */); err != nil {
			return err
		}
	}
	canonicaliseClosure(c)
	return nil
}
