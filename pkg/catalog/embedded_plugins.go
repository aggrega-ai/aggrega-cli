// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// embeddedPluginCache holds the parsed embedded Plugin entities, assembled once
// from the binary's embedded entity set. The embedded set is process-global (it
// is baked into the binary), so it is parsed a single time and shared across
// every Catalog view rather than re-parsed per resolution.
var embeddedPluginCache struct {
	once    sync.Once
	plugins []types.Plugin
	coords  map[string]struct{}
}

// embeddedPluginEntities returns the parsed embedded Plugin entities and the set
// of coordinates they occupy. The entities are the complete generated catalog
// entities (machine-derived describe() contract merged with the authored
// overlay, runtime block included) embedded beside each guest. The slice is in
// sorted-URI order (embedded.EntityURIs is sorted), so any consumer that folds
// it into a resolution set stays deterministic.
//
// A light binary embeds no entities, so both returns are empty and every caller
// degrades to the catalog/registry tiers with no behavior change. A malformed
// embedded entity — a build-of-binary defect — is skipped rather than aborting:
// the missing coordinate then surfaces loudly at the point of use (a provider
// that cannot be routed halts with the unknown-provider diagnostic) rather than
// panicking deep in resolution.
func embeddedPluginEntities() ([]types.Plugin, map[string]struct{}) {
	embeddedPluginCache.once.Do(func() {
		embeddedPluginCache.coords = map[string]struct{}{}
		for _, uri := range embedded.EntityURIs() {
			raw, ok := embedded.LookupEntity(uri)
			if !ok {
				continue
			}
			var p types.Plugin
			if err := yaml.Unmarshal(raw, &p); err != nil {
				continue
			}
			embeddedPluginCache.plugins = append(embeddedPluginCache.plugins, p)
			embeddedPluginCache.coords[p.Coordinate()] = struct{}{}
		}
	})
	return embeddedPluginCache.plugins, embeddedPluginCache.coords
}

// providerResolutionPlugins returns the plugin set render-time provider
// resolution consults: the loaded catalog plugins overlaid with the binary's
// embedded core-provider entities, embedded-wins on a shared coordinate. An
// embedded core provider (compose, helm, terraform, …) overrides a local catalog
// entity of the same coordinate — a full compiler renders with its own embedded
// core providers, so a dev (non-tag) build uses its dev plugins — while a local
// third-party provider of a different coordinate is untouched and still competes
// on capability. An explicit plugins.providers binding still selects among the
// survivors downstream, so a bound custom provider overrides the embedded
// default.
//
// The returned map is keyed exactly like Catalog.Plugins (coordinate:version),
// so the shared capability-resolution and by-URI matchers run over it unchanged.
// It is deliberately SEPARATE from Catalog.Plugins, and it applies to ANY view
// that enables embedded resolution (resolveEmbeddedProviders) rather than to a
// render-time one alone. The lazy backing that serves the production build and
// pull paths enables it, so a lock-time provider descent resolving a capability
// through this overlay can select an embedded core provider. A bare Catalog
// literal enables nothing and resolves from its own Plugins alone.
//
// An embedded core provider is still never pinned as a pulled artifact, and that
// follows from the pull-side pin decision rather than from this overlay going
// unread: a resolution served from the binary reports its own resolution path,
// and the pin predicates leave a core provider carrying that path out of the
// closure, because the build selects that provider's rendering engine from the
// same binary. It is that path that answers for an embedded provider — not the
// local-catalog one sitting beside it in the same predicate. The two legs are
// disjoint and neither stands in for the other. When no entities are embedded (a
// light binary, or a build before the entities were generated) the loaded set is
// returned as-is.
func (c *Catalog) providerResolutionPlugins() map[string]types.Plugin {
	if !c.resolveEmbeddedProviders {
		return c.Plugins
	}
	embeds, coords := embeddedPluginEntities()
	if len(embeds) == 0 {
		return c.Plugins
	}
	out := make(map[string]types.Plugin, len(c.Plugins)+len(embeds))
	for k, p := range c.Plugins {
		// Embedded-wins: a local entity sharing an embedded core coordinate is
		// shadowed by the embedded one.
		if _, shadowed := coords[p.Coordinate()]; shadowed {
			continue
		}
		out[k] = p
	}
	for _, p := range embeds {
		out[entityIndexKey(p.Coordinate(), p.Metadata.Version)] = p
	}
	return out
}

// UseEmbeddedProviderResolution marks this catalog view as a production render
// path that resolves core providers from the binary's embedded entity set
// (embedded-wins). Once enabled, render-time provider resolution — capability
// matching (providerResolutionPlugins) and by-URI fallback (PluginByURI) —
// overlays the embedded core providers on the loaded set, so a no-local-catalog
// render resolves a core provider (compose, helm, terraform, …) offline and a
// dev build renders with its own embedded core over a same-coordinate entry.
//
// It is set explicitly by the callers that render through the eager whole-catalog
// view and want the same embedded-wins behavior the lazy build/pull backing has.
// It must NOT be set on a view backing a controlled, self-contained catalog (the
// build-faithful render harness, the resolver guards, direct-constructed test
// fixtures) that must render exactly the providers it declares.
func (c *Catalog) UseEmbeddedProviderResolution() {
	c.resolveEmbeddedProviders = true
}
