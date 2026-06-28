// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"sort"
)

// pluginKindHint marks a closure entry emitted for a pipeline plugin artifact
// (the .wasm reference a WasmPlugin entity's spec.plugin names). DescendRecipePlugins
// sets it so the pinned plugin artifact carries the same provenance hint the
// workspace-config plugin axis records, distinguishing a pipeline-plugin pin from a
// catalog-entity pin in the lockfile. It is serialised to the lockfile like the other
// ResolvedRef.KindHint values.
const pluginKindHint = "wasm_plugin"

// DescendRecipePlugins extends an already-computed Closure with the pipeline-plugin
// axis: every Recipe / FeatureRecipe reachable in the closure may declare
// spec.pipeline steps, each referencing a WasmPlugin ENTITY by URI
// (spec.pipeline[].plugin). A registry-resolved entity is resolved through the same
// ResolveCatalogEntity path the provider axis uses and pinned into the closure's
// Entities set; the entity body's spec.plugin (the .wasm artifact reference) is then
// resolved through ResolveWasmPlugin and content-digest-pinned into the closure's
// WasmPlugins set. So a registry-published pipeline plugin participates in the
// deterministic lockfile pin as both the entity (in entities) and its artifact (in
// wasm_plugins) — the entity rides the catalog-entity axis, the .wasm rides the
// plugin axis, no new lockfile shape. The build-time per-step loader reads the
// artifact pin and loads the bytes.
//
// It is a pull-side augmentation, run after ComputeClosure (and the sub-aggregate /
// library / provider discovery passes) at lock time — never inside ComputeClosure,
// which also runs on the hermetic build and serve paths where no registry pull may
// occur. The shape mirrors DescendRecipeProviders: a separate pass over the resolved
// closure driven by an injected loader, producing the same Entities + WasmPlugins
// entry representations the other axes emit rather than a parallel pin.
//
// Registry-only pinning: an entity or artifact is emitted into the closure ONLY when
// it resolves above Tier-1 (the registry-backed cache / pull tiers). A Tier-1-local
// entity / plugin — one authored in a local catalog directory or addressed by a
// workspace-relative path — is left out of the closure and resolves at build through
// the local catalog walk, mirroring how a Tier-1-local provider is not lockfile-pinned.
// A reference that resolves through no tier (NotFound) is skipped rather than halting
// the pull: a local-path entity the lock-time lookup does not see resolves at build,
// and the build-time loader gate halts on a genuinely-absent reference.
//
// Newly-discovered entities / plugins are appended to c.Entities / c.WasmPlugins
// (deduped by URI against what is already present, accumulating referenced-from
// sites); the closure is re-canonicalised before return, so the pinned set — and the
// lockfile written from it — is byte-identical across runs regardless of descent
// order. A nil loader is a no-op (the caller wired no recipe-body access).
func DescendRecipePlugins(c *Closure, loader RecipeLoader, lookup Lookup) error {
	if c == nil || loader == nil {
		return nil
	}

	entityIndex := make(map[string]int, len(c.Entities))
	for i, e := range c.Entities {
		entityIndex[e.URI] = i
	}
	pluginIndex := make(map[string]int, len(c.WasmPlugins))
	for i, e := range c.WasmPlugins {
		pluginIndex[e.URI] = i
	}

	// Collect each distinct WasmPlugin entity URI to descend, plus the deduped set
	// of Recipe / FeatureRecipe URIs whose pipeline steps reference it (empty for a
	// workspace-socket plugin entity, which ComputeClosure already pinned with its
	// socket referenced-from). The set is order-independent and each entity is
	// descended once.
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
				return fmt.Errorf("closure: load recipe %q for plugin descent: %w", ent.URI, err)
			}
			for _, step := range r.Spec.Pipeline {
				recordRecipeRef(step.Plugin, ent.URI)
			}
		case "feature_recipe":
			fr, err := loader.LoadFeatureRecipe(ent.URI)
			if err != nil {
				return fmt.Errorf("closure: load feature recipe %q for plugin descent: %w", ent.URI, err)
			}
			for _, step := range fr.Spec.Pipeline {
				recordRecipeRef(step.Plugin, ent.URI)
			}
		case pluginKindHint:
			// A WasmPlugin entity pinned on the entity axis directly — a workspace
			// plugin socket. It is its own descent target; ComputeClosure already
			// recorded its socket referenced-from, so no recipe-ref is added.
			addEntityURI(ent.URI)
		}
	}

	sort.Strings(entityURIs)
	for _, entityURI := range entityURIs {
		// 1. Resolve and pin the WasmPlugin ENTITY onto the catalog-entity axis —
		//    exactly as the provider axis pins a Provider. A recipe-referenced entity
		//    is pinned here with the referencing recipe URIs; a workspace-socket entity
		//    was already pinned by ComputeClosure and keeps its socket referenced-from.
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

		// 2. Load the entity body to read spec.plugin (the .wasm artifact reference),
		//    then resolve and pin the ARTIFACT onto the plugin axis. The artifact is
		//    referenced from its WasmPlugin entity. The entity → spec.plugin step here
		//    is a plain field read; it is deliberately NOT factored into a shared func
		//    with the load-time entity-front (a different catalog-access path serving
		//    the same trivial field), so leave the two independent.
		wp, err := loader.LoadWasmPlugin(entityURI)
		if err != nil {
			return fmt.Errorf("closure: load wasm plugin entity %q for artifact descent: %w", entityURI, err)
		}
		artifactURI := wp.Spec.Plugin
		if artifactURI == "" {
			continue
		}
		artRes, err := lookup.ResolveWasmPlugin(artifactURI)
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
			c.WasmPlugins[idx].ReferencedFrom = mergeReferencedFrom(c.WasmPlugins[idx].ReferencedFrom, artRef.ReferencedFrom)
		} else {
			c.WasmPlugins = append(c.WasmPlugins, artRef)
			pluginIndex[artifactURI] = len(c.WasmPlugins) - 1
		}
	}

	canonicaliseClosure(c)
	return nil
}
