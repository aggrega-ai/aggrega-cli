// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"fmt"
	"sort"
)

// pluginKindHint marks a closure entry emitted for a pipeline plugin URI
// (a Recipe / FeatureRecipe spec.pipeline[].plugin reference). DescendRecipePlugins
// sets it so the pinned plugin carries the same provenance hint the workspace-config
// plugin axis records, distinguishing a pipeline-plugin pin from a catalog-entity pin
// in the lockfile. It is serialised to the lockfile like the other ResolvedRef.KindHint
// values.
const pluginKindHint = "wasm_plugin"

// DescendRecipePlugins extends an already-computed Closure with the pipeline-plugin
// axis: every Recipe / FeatureRecipe reachable in the closure may declare
// spec.pipeline steps, each referencing a Wasm plugin by URI (spec.pipeline[].plugin).
// A registry-resolved plugin is resolved through the same ResolveWasmPlugin path the
// walker's workspace-config plugin axis uses and content-digest-pinned into the
// closure's WasmPlugins set, so a registry-published pipeline plugin participates in
// the deterministic lockfile pin like any other plugin. This pins the artifact; the
// build-time per-step loader reads the pin and loads the bytes.
//
// It is a pull-side augmentation, run after ComputeClosure (and the sub-aggregate /
// library / provider discovery passes) at lock time — never inside ComputeClosure,
// which also runs on the hermetic build and serve paths where no registry pull may
// occur. The shape mirrors DescendRecipeProviders: a separate pass over the resolved
// closure driven by an injected loader, producing the same WasmPlugins entry
// representation the walker's plugin axis emits rather than a parallel pin.
//
// Registry-only pinning: a plugin is emitted into the closure ONLY when it resolves
// above Tier-1 (the registry-backed cache / pull tiers). A Tier-1-local plugin — one
// addressed by a workspace-relative path — is left out of the closure and resolves at
// build through the local path, mirroring how a Tier-1-local provider is not
// lockfile-pinned. A plugin that resolves through no tier (NotFound) is skipped rather
// than halting the pull: a local-path plugin the lock-time lookup does not see resolves
// at build, and the build-time loader gate halts on a genuinely-absent plugin.
//
// Newly-discovered plugins are appended to c.WasmPlugins (deduped by URI against what
// is already present, accumulating referenced-from sites); the closure is
// re-canonicalised before return, so the pinned set — and the lockfile written from
// it — is byte-identical across runs regardless of descent order. A nil loader is a
// no-op (the caller wired no recipe-body access).
func DescendRecipePlugins(c *Closure, loader RecipeLoader, lookup Lookup) error {
	if c == nil || loader == nil {
		return nil
	}

	index := make(map[string]int, len(c.WasmPlugins))
	for i, e := range c.WasmPlugins {
		index[e.URI] = i
	}

	// Collect each distinct plugin URI with the deduped set of recipe URIs that
	// reference it, so the referenced-from set is order-independent and the resolve
	// happens once per plugin URI.
	referencedFrom := map[string]map[string]struct{}{}
	var pluginURIs []string
	recordPlugin := func(uri, fromURI string) {
		if uri == "" {
			return
		}
		if referencedFrom[uri] == nil {
			referencedFrom[uri] = map[string]struct{}{}
			pluginURIs = append(pluginURIs, uri)
		}
		referencedFrom[uri][fromURI] = struct{}{}
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
				recordPlugin(step.Plugin, ent.URI)
			}
		case "feature_recipe":
			fr, err := loader.LoadFeatureRecipe(ent.URI)
			if err != nil {
				return fmt.Errorf("closure: load feature recipe %q for plugin descent: %w", ent.URI, err)
			}
			for _, step := range fr.Spec.Pipeline {
				recordPlugin(step.Plugin, ent.URI)
			}
		}
	}

	sort.Strings(pluginURIs)
	for _, uri := range pluginURIs {
		res, err := lookup.ResolveWasmPlugin(uri)
		if err != nil {
			return fmt.Errorf("closure: resolve pipeline plugin %q: %w", uri, err)
		}
		// A plugin referenced by a recipe but resolvable through no tier is not a
		// closure gap here: a workspace-relative plugin may resolve at build through a
		// local path the lock-time lookup does not see, and the build-time loader gate
		// halts on a genuinely-absent plugin. Skip an unresolved plugin rather than
		// halt the pull.
		if res.NotFound {
			continue
		}
		// Registry-only pinning: a Tier-1-local plugin is not pinned (it resolves at
		// build through its local path). Only a plugin resolved above Tier-1
		// participates in the closure + lockfile pin.
		if res.ResolutionPath == tier1CatalogPath {
			continue
		}

		refs := make([]string, 0, len(referencedFrom[uri]))
		for r := range referencedFrom[uri] {
			refs = append(refs, r)
		}
		ref := ResolvedRef{
			URI:             uri,
			ResolvedVersion: res.ResolvedVersion,
			Digest:          res.Digest,
			ResolutionPath:  res.ResolutionPath,
			ReferencedFrom:  refs,
			KindHint:        pluginKindHint,
		}
		if idx, dup := index[uri]; dup {
			c.WasmPlugins[idx].ReferencedFrom = mergeReferencedFrom(c.WasmPlugins[idx].ReferencedFrom, ref.ReferencedFrom)
		} else {
			c.WasmPlugins = append(c.WasmPlugins, ref)
			index[uri] = len(c.WasmPlugins) - 1
		}
	}

	canonicaliseClosure(c)
	return nil
}
