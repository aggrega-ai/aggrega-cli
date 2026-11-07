// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"context"
	"fmt"
	"sort"
)

// DescendPinnedAxes fills in the pinned axes a closure walk leaves empty — the
// transitive sub-aggregates, the pipeline and provider plugin entities the closure's
// Recipes and FeatureRecipes reach, and those plugins' .wasm artifacts. It is the one
// orchestration of that descent: a pull runs it to compute the axes it pins, and a
// build or a serve drift check runs it to compute the axes it holds a lockfile to, so
// the two sides reach their expected closures through the same sequence rather than
// through two copies of it kept in step by hand.
//
// It runs, back to back and in this order: the transitive sub-aggregate descent
// (DescendSubAggregates); the plugin entity descent (DescendRecipePluginEntities),
// which pins each plugin ENTITY on the catalog-entity axis; the re-fold, which
// re-derives the recipe loader over a pin set that includes the entities just pinned,
// so they are visible to the next pass; and the artifact descent
// (DescendRecipePluginArtifacts), which loads each entity to pin its .wasm ARTIFACT
// on the plugin axis. No step of either caller has to run between two of these, so
// every pass a pull runs only at lock time — attaching the transform-role governance
// plugins, caching the verdict-role ones, verifying host functions, and descending
// sources — runs after this returns.
//
// Its parameters are exactly what differs between the two callers, and nothing else
// does:
//
//   - lookup serves the sub-aggregate descent. A pull passes its reconciling lookup,
//     which carries a puller and honours its prior pins; a build passes the
//     resolution's own lookup, which carries the Release's external documents and no
//     puller, because a build never touches the network.
//   - pluginLookup serves both plugin descents. A pull passes the same lookup it
//     walked; a build passes one carrying the lockfile's pins, because a pinned .wasm
//     sits in a cache slot keyed by the content digest only its pin names, and a
//     lookup without pins resolves it as absent. A nil pluginLookup uses lookup.
//   - aggLoader and recipeLoader read the bodies each descent walks; each caller
//     takes them from its own lookup or view. A nil loader makes its pass a no-op.
//   - providerResolver resolves each Recipe / FeatureRecipe spec.capability binding
//     to its serving provider-rendering Plugin: from the workspace configuration at
//     pull, recording what it resolves, and from the lockfile's recorded bindings at
//     build, so a build is invariant to a configuration edit made after the pull.
//   - refold re-derives the recipe loader after the entity descent; re-deriving a
//     view's pin set is a catalog-side operation this package does not reach, so each
//     caller supplies it over its own view. A nil refold reuses recipeLoader.
//   - entityLocalArtifact and artifactLocalArtifact are the question each plugin
//     descent asks about the artifact of a Plugin entity authored in a local catalog
//     directory (see LocalPluginArtifact). A pull asks it at both: the entity descent
//     reads only its refusal, so an artifact no local source holds halts the pull
//     before the lockfile is written, and the artifact descent pins a transform-role
//     attachment authored there from the local bytes. A build asks it at the artifact
//     descent only, so the attachment reconstructs to the pin the pull recorded, and
//     asks the entity descent nothing: a build loads each plugin through the cascade
//     that raises the same condition where it loads it, so it refuses nothing at that
//     step that its render would not.
func DescendPinnedAxes(
	ctx context.Context,
	cl *Closure,
	aggLoader AggregateLoader,
	recipeLoader RecipeLoader,
	lookup Lookup,
	pluginLookup Lookup,
	providerResolver ProviderPluginResolver,
	refold func() (RecipeLoader, error),
	entityLocalArtifact LocalPluginArtifact,
	artifactLocalArtifact LocalPluginArtifact,
) error {
	if pluginLookup == nil {
		pluginLookup = lookup
	}
	// Each pass's error is prefixed with what it was resolving, in the terms a reader
	// of the Release knows, because the prefix is the first thing a refusal prints.
	if err := DescendSubAggregates(ctx, cl, aggLoader, lookup); err != nil {
		return fmt.Errorf("resolving the sub-aggregates the Aggregate imports: %w", err)
	}
	if err := DescendRecipePluginEntities(ctx, cl, recipeLoader, pluginLookup, providerResolver, entityLocalArtifact); err != nil {
		return fmt.Errorf("resolving the plugins the Release depends on: %w", err)
	}
	artifactLoader := recipeLoader
	if refold != nil {
		loader2, err := refold()
		if err != nil {
			return fmt.Errorf("reading the plugins just resolved: %w", err)
		}
		artifactLoader = loader2
	}
	if err := DescendRecipePluginArtifacts(ctx, cl, artifactLoader, pluginLookup, artifactLocalArtifact); err != nil {
		return fmt.Errorf("resolving the .wasm of each plugin the Release depends on: %w", err)
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
// the lockfile pins. Each is resolved through ResolvePinnedSocketEntity — the
// resolution the closure walk gave the same attachment — over the pinned cache (the
// Lookup carries a nil puller, so no registry fetch), pinned onto the catalog-entity axis with
// the plugin kind hint, then descended to its .wasm artifact on the plugin axis.
// A URI already present (a mutator entity ComputeClosure recorded on the entity
// axis) is not duplicated — its referenced-from unions and its kind hint is kept
// so the artifact descent still reaches it.
//
// Unlike DescendRecipePluginEntities, a socket ENTITY is not filtered by where it
// resolved from, and entityNeedsNoPin is deliberately not applied to it: every URI
// here is a row the lockfile records, and one of those rows resolves from a local
// catalog directory by design — a transform-role governance attachment authored
// there, which the closure walk keeps on the entity axis wherever it resolved from.
// The registry-only rule would drop that row, and with it the resolution path that
// sends its artifact to localArtifact, which is the reconstruction gap that surfaces
// as spurious "no longer used" drift. A socket entity read from a pinned cache slot
// resolves the cache tier, which the rule keeps anyway.
//
// A socket ARTIFACT is pinned by the one pin rule every artifact pin site applies
// (artifactNeedsNoPin), to the resolution path this pass reads, and the pull's
// artifact descent applied the same rule to the same entity, whose artifact resolved
// the same way, when it wrote the row. So this pass reconstructs exactly the
// artifact rows the pull wrote: a core provider whose .wasm the binary carries is
// left out on both sides, because the build loads its rendering engine from the same
// binary, and every other output-shaping socket plugin is pinned on both.
//
// Integrity is unchanged: the entity bytes are re-hashed here and its .wasm content
// digest is re-read from the content-addressed cache slot the lockfile pin names, so
// a drifted cache slot or a tampered lockfile pin still surfaces (a missing slot
// leaves the axis short and drift fires; changed bytes fail the pinned-integrity
// gate or the render read).
//
// A NotFound entity or artifact is skipped rather than halting: the honest frozen
// build resolves both from the cache, and a genuinely-absent one surfaces as drift
// against the lockfile. A nil loader is a no-op.
//
// A socket entity authored in a local catalog directory has its artifact resolved
// through localArtifact, the same question the lock-time artifact descent asks, and
// never through lookup; see DescendRecipePluginArtifacts.
func ReconstructWorkspaceSocketEntities(ctx context.Context, c *Closure, entityURIs []string, loader RecipeLoader, lookup Lookup, localArtifact LocalPluginArtifact) error {
	if c == nil || loader == nil || len(entityURIs) == 0 {
		return nil
	}

	entityIndex := entityIndexOf(c)

	sorted := append([]string(nil), entityURIs...)
	sort.Strings(sorted)

	const refPath = "release plugin socket"
	entityPaths := make(map[string]string, len(sorted))
	for _, entityURI := range sorted {
		res, err := ResolvePinnedSocketEntity(ctx, lookup, entityURI)
		if err != nil {
			return fmt.Errorf("resolve: resolve workspace-socket plugin entity %q: %w", entityURI, err)
		}
		// A socket entity the pinned cache cannot serve is left off the reconstructed
		// axis; the drift verify then reports it against the lockfile rather than the
		// reconstruction masking the gap.
		if res.NotFound {
			continue
		}
		entityPaths[entityURI] = res.ResolutionPath
		mergeEntity(c, entityIndex, ResolvedRef{
			URI:             entityURI,
			ResolvedVersion: res.ResolvedVersion,
			Digest:          res.Digest,
			ManifestDigest:  res.ManifestDigest,
			ResolutionPath:  res.ResolutionPath,
			ReferencedFrom:  []string{refPath},
			KindHint:        KindPlugin,
		})
	}
	canonicaliseClosure(c)

	pluginIndex := make(map[string]int, len(c.Plugins))
	for i, p := range c.Plugins {
		pluginIndex[p.URI] = i
	}
	for _, entityURI := range sorted {
		if err := pinPluginArtifact(ctx, c, loader, lookup, entityURI, entityPaths[entityURI], pluginIndex, localArtifact); err != nil {
			return err
		}
	}
	canonicaliseClosure(c)
	return nil
}
