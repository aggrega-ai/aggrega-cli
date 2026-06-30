// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"sort"
)

// pluginKindHint marks a closure entry emitted for a pipeline plugin artifact
// (the .wasm reference a Plugin entity's spec.plugin names). The plugin
// descent sets it so the pinned plugin artifact carries the same provenance hint
// the workspace-config plugin axis records, distinguishing a pipeline-plugin pin
// from a catalog-entity pin during closure assembly. Like the other
// ResolvedRef.KindHint values it is an in-memory provenance hint, not a lockfile
// field; the lockfile writer copies only the wire-shape fields.
const pluginKindHint = "plugin"

// DescendRecipePluginEntities is Phase A of the two-pass pipeline-plugin descent:
// it discovers every Plugin ENTITY a closure's Recipes / FeatureRecipes
// reference through their spec.pipeline[].plugin URIs (plus any workspace-socket
// plugin entity ComputeClosure already pinned on the entity axis) and pins each
// registry-resolved entity onto the catalog-entity axis (c.Entities) through the
// same ResolveCatalogEntity path the provider axis uses. It does NOT load any
// entity body and does NOT resolve any .wasm artifact — that is Phase B
// (DescendRecipePluginArtifacts).
//
// The split exists because a freshly-pulled plugin entity is pinned here but is
// not yet visible to the entity-front the artifact-load phase reads: the caller
// must re-fold the lazy index from the now-augmented closure between the two
// phases so Phase B's LoadPlugin resolves the just-pinned entity. A
// single-pass descent loads the body against a pin set that predates the
// entity's own pin and misses it.
//
// Both phases are pull-side augmentations, run after ComputeClosure (and the
// sub-aggregate / library / provider discovery passes) at lock time — never
// inside ComputeClosure, which also runs on the hermetic build and serve paths
// where no registry pull may occur. The shape mirrors DescendRecipeProviders: a
// separate pass over the resolved closure driven by an injected loader, producing
// the same Entities entry representation the other axes emit rather than a
// parallel pin.
//
// Registry-only pinning: an entity is emitted into the closure ONLY when it
// resolves above Tier-1 (the registry-backed cache / pull tiers). A Tier-1-local
// entity — one authored in a local catalog directory or addressed by a
// workspace-relative path — is left out of the closure and resolves at build
// through the local catalog walk, mirroring how a Tier-1-local provider is not
// lockfile-pinned. A reference that resolves through no tier (NotFound) is
// skipped rather than halting the pull: a local-path entity the lock-time lookup
// does not see resolves at build, and the build-time loader gate halts on a
// genuinely-absent reference.
//
// Newly-discovered entities are appended to c.Entities (deduped by URI against
// what is already present, accumulating referenced-from sites). A nil loader is
// a no-op (the caller wired no recipe-body access).
func DescendRecipePluginEntities(c *Closure, loader RecipeLoader, lookup Lookup) error {
	if c == nil || loader == nil {
		return nil
	}

	entityIndex := make(map[string]int, len(c.Entities))
	for i, e := range c.Entities {
		entityIndex[e.URI] = i
	}

	recipeRefs, entityURIs, err := discoverPluginEntities(c, loader)
	if err != nil {
		return err
	}

	for _, entityURI := range entityURIs {
		// Resolve and pin the Plugin ENTITY onto the catalog-entity axis —
		// exactly as the provider axis pins a Provider. A recipe-referenced entity
		// is pinned here with the referencing recipe URIs; a workspace-socket entity
		// was already pinned by ComputeClosure and keeps its socket referenced-from.
		recipeFromSet := recipeRefs[entityURI]
		entRes, err := lookup.ResolveCatalogEntity(entityURI, pluginKindHint)
		if err != nil {
			return fmt.Errorf("closure: resolve wasm plugin entity %q: %w", entityURI, err)
		}
		// A plugin entity referenced by a recipe but resolvable through no tier is not
		// a closure gap here: it may resolve at build through a local catalog the
		// lock-time lookup does not see, and the build-time loader gate halts on a
		// genuinely-absent entity. Skip an unresolved entity rather than halt the pull.
		// (A workspace-socket entity already passed ComputeClosure's fatal resolve.)
		if entRes.NotFound {
			continue
		}
		// Registry-only pinning: a Tier-1-local entity is not pinned (it resolves at
		// build through the local catalog walk).
		if entRes.ResolutionPath == tier1CatalogPath {
			continue
		}
		if len(recipeFromSet) > 0 {
			refs := make([]string, 0, len(recipeFromSet))
			for r := range recipeFromSet {
				refs = append(refs, r)
			}
			entRef := ResolvedRef{
				URI:             entityURI,
				ResolvedVersion: entRes.ResolvedVersion,
				Digest:          entRes.Digest,
				ResolutionPath:  entRes.ResolutionPath,
				ReferencedFrom:  refs,
				KindHint:        pluginKindHint,
			}
			if idx, dup := entityIndex[entityURI]; dup {
				c.Entities[idx].ReferencedFrom = mergeReferencedFrom(c.Entities[idx].ReferencedFrom, entRef.ReferencedFrom)
			} else {
				c.Entities = append(c.Entities, entRef)
				entityIndex[entityURI] = len(c.Entities) - 1
			}
		}
	}

	canonicaliseClosure(c)
	return nil
}

// DescendRecipePluginArtifacts is Phase B of the two-pass pipeline-plugin descent:
// it rediscovers the Plugin entities Phase A pinned (by walking c.Entities for
// KindHint==pluginKindHint), loads each entity body to read spec.plugin (the .wasm
// artifact reference), resolves that reference through ResolvePlugin, and pins
// the artifact onto the plugin axis (c.Plugins). It runs AFTER the caller has
// re-folded the lazy index from the Phase-A-augmented closure, so LoadPlugin
// resolves a just-pinned plugin entity that Phase A discovered.
//
// Rediscovery is from c.Entities rather than from a re-walk of the recipes: Phase A
// already pinned every registry-resolved plugin entity (recipe-referenced and
// workspace-socket) with KindHint==pluginKindHint, so the entity axis is the
// authoritative set of artifacts to descend, and a Tier-1-local entity Phase A
// declined to pin is correctly absent (its artifact is not lockfile-pinned either).
//
// The artifact is referenced from its Plugin entity. Registry-only pinning and
// NotFound-skip mirror Phase A. Newly-discovered artifacts are appended to
// c.Plugins (deduped by URI, accumulating referenced-from sites); the closure
// is re-canonicalised before return, so the pinned set — and the lockfile written
// from it — is byte-identical across runs regardless of descent order. A nil loader
// is a no-op.
func DescendRecipePluginArtifacts(c *Closure, loader RecipeLoader, lookup Lookup) error {
	if c == nil || loader == nil {
		return nil
	}

	pluginIndex := make(map[string]int, len(c.Plugins))
	for i, e := range c.Plugins {
		pluginIndex[e.URI] = i
	}

	// Rediscover the plugin entities Phase A pinned. Collect first (sorted) so the
	// artifact-descent order is deterministic and independent of c.Entities order.
	var entityURIs []string
	for _, ent := range c.Entities {
		if ent.KindHint == pluginKindHint {
			entityURIs = append(entityURIs, ent.URI)
		}
	}
	sort.Strings(entityURIs)

	for _, entityURI := range entityURIs {
		// Load the entity body to read spec.plugin (the .wasm artifact reference),
		// then resolve and pin the ARTIFACT onto the plugin axis. The entity →
		// spec.plugin step here is a plain field read; it is deliberately NOT factored
		// into a shared func with the load-time entity-front (a different catalog-access
		// path serving the same trivial field), so leave the two independent.
		wp, err := loader.LoadPlugin(entityURI)
		if err != nil {
			return fmt.Errorf("closure: load wasm plugin entity %q for artifact descent: %w", entityURI, err)
		}
		artifactURI := wp.Spec.Plugin
		if artifactURI == "" {
			continue
		}
		artRes, err := lookup.ResolvePlugin(artifactURI)
		if err != nil {
			return fmt.Errorf("closure: resolve pipeline plugin artifact %q: %w", artifactURI, err)
		}
		if artRes.NotFound {
			continue
		}
		if artRes.ResolutionPath == tier1CatalogPath {
			continue
		}
		artRef := ResolvedRef{
			URI:             artifactURI,
			ResolvedVersion: artRes.ResolvedVersion,
			Digest:          artRes.Digest,
			ResolutionPath:  artRes.ResolutionPath,
			ReferencedFrom:  []string{entityURI},
			KindHint:        pluginKindHint,
		}
		if idx, dup := pluginIndex[artifactURI]; dup {
			c.Plugins[idx].ReferencedFrom = mergeReferencedFrom(c.Plugins[idx].ReferencedFrom, artRef.ReferencedFrom)
		} else {
			c.Plugins = append(c.Plugins, artRef)
			pluginIndex[artifactURI] = len(c.Plugins) - 1
		}
	}

	canonicaliseClosure(c)
	return nil
}

// discoverPluginEntities walks the closure's Recipe / FeatureRecipe entities,
// reading each spec.pipeline[].plugin URI, and returns the deduped set of plugin
// entity URIs to descend plus, per entity, the set of Recipe / FeatureRecipe URIs
// that reference it. A Plugin entity already on the entity axis (a workspace
// plugin socket ComputeClosure pinned) is added as its own descent target with no
// recipe-ref. The returned entity-URI slice is sorted, so the descent is
// order-independent and each entity is descended once.
func discoverPluginEntities(c *Closure, loader RecipeLoader) (map[string]map[string]struct{}, []string, error) {
	recipeRefs := map[string]map[string]struct{}{}
	var entityURIs []string
	seen := map[string]struct{}{}
	addEntityURI := func(entityURI string) {
		if entityURI == "" {
			return
		}
		if _, ok := seen[entityURI]; !ok {
			seen[entityURI] = struct{}{}
			entityURIs = append(entityURIs, entityURI)
		}
	}
	recordRecipeRef := func(entityURI, recipeURI string) {
		addEntityURI(entityURI)
		if recipeRefs[entityURI] == nil {
			recipeRefs[entityURI] = map[string]struct{}{}
		}
		recipeRefs[entityURI][recipeURI] = struct{}{}
	}

	for _, ent := range c.Entities {
		entityKind, ok := ent.KindHint, ent.KindHint != ""
		if !ok {
			entityKind, ok = classifyEntityKind(ent.URI)
		}
		if !ok {
			continue
		}
		switch entityKind {
		case "recipe":
			r, err := loader.LoadRecipe(ent.URI)
			if err != nil {
				return nil, nil, fmt.Errorf("closure: load recipe %q for plugin descent: %w", ent.URI, err)
			}
			for _, step := range r.Spec.Pipeline {
				recordRecipeRef(step.Plugin, ent.URI)
			}
		case "feature_recipe":
			fr, err := loader.LoadFeatureRecipe(ent.URI)
			if err != nil {
				return nil, nil, fmt.Errorf("closure: load feature recipe %q for plugin descent: %w", ent.URI, err)
			}
			for _, step := range fr.Spec.Pipeline {
				recordRecipeRef(step.Plugin, ent.URI)
			}
		case pluginKindHint:
			// A Plugin entity pinned on the entity axis directly — a workspace
			// plugin socket. It is its own descent target; ComputeClosure already
			// recorded its socket referenced-from, so no recipe-ref is added.
			addEntityURI(ent.URI)
		}
	}

	sort.Strings(entityURIs)
	return recipeRefs, entityURIs, nil
}
