// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"sort"
)

// providerKindHint marks a closure entity emitted for a Recipe / FeatureRecipe's
// declared provider (its bare spec.provider name). DescendRecipeProviders sets it
// so the provider entity rides the catalog-entity resolution cascade with the
// "provider" KindHint, binding a bare provider identity to its OCI location at the
// registry tier exactly as a Component / Feature reference does. The hint is
// serialised to the lockfile like the other ResolvedRef.KindHint values that carry
// provenance; source-descent's kind switch routes it to its default no-op arm.
const providerKindHint = "provider"

// tier1CatalogPath is the ResolutionPath a Lookup reports for an entity resolved
// from a local catalog directory (Tier-1). DescendRecipeProviders pins a provider
// only when it resolves above Tier-1 (the registry-backed cache / pull tiers, or an
// inline external seed); a Tier-1-local provider stays catalog-resident and is
// resolved at build through the same local catalog walk, never the lockfile pin.
const tier1CatalogPath = "tier-1-catalog"

// DescendRecipeProviders extends an already-computed Closure with the provider axis:
// every Recipe / FeatureRecipe reachable in the closure declares a provider by bare
// name (spec.provider), and a registry-resolved provider is loaded, resolved, and
// content-digest-pinned into the closure so a registry-published provider
// participates in the deterministic lockfile pin like any other catalog entity. A
// migration that consumes the pinned provider (binding a Wasm guest behind it) is a
// future follow-on; this pass establishes the closure-participation capability the
// migration rides.
//
// It is a pull-side augmentation, run after ComputeClosure (and the sub-aggregate /
// library discovery passes) at lock time — never inside ComputeClosure, which also
// runs on the hermetic build and serve paths where no registry pull may occur. The
// shape mirrors DescendSubAggregates and DescendRecipeSources: a separate pass over
// the resolved closure driven by an injected loader.
//
// Registry-only pinning: a provider is emitted into the closure ONLY when it
// resolves above Tier-1 (the registry-backed cache / pull tiers, or an inline
// external seed). A Tier-1-local provider — one authored in a local catalog
// directory — is left out of the closure and resolves at build through the local
// catalog walk exactly as before, mirroring how a Component or Recipe authored
// locally is not lockfile-pinned. This keeps the local-authoring path byte-identical
// while a registry-resolved provider gains a pin.
//
// Newly-discovered providers are appended to c.Entities (deduped by URI against what
// is already present, accumulating referenced-from sites); the closure is
// re-canonicalised before return, so the pinned set — and the lockfile written from
// it — is byte-identical across runs regardless of descent order. A nil loader is a
// no-op (the caller wired no recipe-body access).
func DescendRecipeProviders(c *Closure, loader RecipeLoader, lookup Lookup) error {
	if c == nil || loader == nil {
		return nil
	}

	index := make(map[string]int, len(c.Entities))
	for i, e := range c.Entities {
		index[e.URI] = i
	}

	// Collect each distinct provider name with the deduped set of recipe URIs that
	// declare it, so the referenced-from set is order-independent and the resolve
	// happens once per provider name.
	referencedFrom := map[string]map[string]struct{}{}
	var providerNames []string
	recordProvider := func(name, fromURI string) {
		if name == "" {
			return
		}
		if referencedFrom[name] == nil {
			referencedFrom[name] = map[string]struct{}{}
			providerNames = append(providerNames, name)
		}
		referencedFrom[name][fromURI] = struct{}{}
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
				return fmt.Errorf("closure: load recipe %q for provider descent: %w", ent.URI, err)
			}
			recordProvider(r.Spec.Provider, ent.URI)
		case "feature_recipe":
			fr, err := loader.LoadFeatureRecipe(ent.URI)
			if err != nil {
				return fmt.Errorf("closure: load feature recipe %q for provider descent: %w", ent.URI, err)
			}
			recordProvider(fr.Spec.Provider, ent.URI)
		}
	}

	sort.Strings(providerNames)
	for _, name := range providerNames {
		res, err := lookup.ResolveCatalogEntity(name, providerKindHint)
		if err != nil {
			return fmt.Errorf("closure: resolve provider %q: %w", name, err)
		}
		// A provider declared by a recipe but resolvable through no tier is not a
		// closure gap here: the provider may resolve at build through a local catalog
		// the lock-time lookup does not see, and the recipe-resolution gate already
		// halts on a genuinely-absent provider. Skip an unresolved provider rather
		// than halt the pull.
		if res.NotFound {
			continue
		}
		// Registry-only pinning: a Tier-1-local provider is not pinned (it resolves at
		// build through the local catalog walk). Only a provider resolved above Tier-1
		// participates in the closure + lockfile pin.
		if res.ResolutionPath == tier1CatalogPath {
			continue
		}

		refs := make([]string, 0, len(referencedFrom[name]))
		for r := range referencedFrom[name] {
			refs = append(refs, r)
		}
		ref := ResolvedRef{
			URI:             name,
			ResolvedVersion: res.ResolvedVersion,
			Digest:          res.Digest,
			ResolutionPath:  res.ResolutionPath,
			ReferencedFrom:  refs,
			KindHint:        providerKindHint,
		}
		if idx, dup := index[name]; dup {
			c.Entities[idx].ReferencedFrom = mergeReferencedFrom(c.Entities[idx].ReferencedFrom, ref.ReferencedFrom)
		} else {
			c.Entities = append(c.Entities, ref)
			index[name] = len(c.Entities) - 1
		}
	}

	canonicaliseClosure(c)
	return nil
}
