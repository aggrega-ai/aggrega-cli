// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// NewHybrid wraps a fallback EngineFactory with catalog-driven Wasm routing: a
// provider whose name resolves to a provider-rendering Plugin — the Plugin that
// lists the provider in its compatibleProviders — advertising a render_node
// export renders through the wasmengine adapter; every other provider renders
// through the fallback engine exactly as before.
//
// The Wasm route has two tiers, selected by the Plugin entity's advertised
// exports. A Plugin advertising only render_node routes to the render-only
// adapter (per-node render, host-side Go assembly for a flat-output provider). A
// Plugin advertising render_node AND assemble_boundary routes to the assembler
// adapter, which renders per-node AND merges the dispatch group's fragments
// through the guest — so a full provider's per-group assembly (an umbrella chart,
// a merged compose file) runs in Wasm too. The assembler adapter embeds the
// render adapter, so the render behavior is identical across the two tiers.
//
// The fallback is authoritative for which providers exist and for engine
// capabilities: a provider the fallback rejects (unknown name) surfaces that
// error unchanged, and a routed Wasm engine reports the fallback engine's own
// input-kind capabilities, so the render-time input-kind gate stays Go-decided.
// The Wasm engine loads through sharedLoader (one compilation cache warmed once
// per build) and resolves its bytes through pluginLookup (the entity-fronted
// artifact cascade), keyed by the resolved Plugin's entity identity.
//
// The routing decision falls back to the fallback engine on EVERY miss in the
// resolve chain — a nil catalog accessor (catalog-less paths leave the
// CatalogLookup fields nil), a provider name no provider-Plugin serves, or a
// Plugin that does not advertise the render_node export. This is the
// no-regression mechanism: a catalog that does not serve a provider through a
// render_node-advertising Wasm plugin renders through the same Go engine it
// always has, so wrapping the dispatch changes nothing until a provider-Plugin
// declaring that provider in its compatibleProviders is present.
// NewHybrid also returns a [renderer.ProviderVersionResolver] alongside the
// factory: it reports the resolved provider-rendering plugin version behind a
// binding (the version the routed engine renders through), or "" for a binding
// that renders through the fallback engine. Both share one plugin-resolution
// pass so the version reported and the engine returned for a binding always
// agree. The orchestrator stamps that version on the aggrega.ai/provider-version
// label and uses it to pick a mixed-version dispatch group's boundary assembler.
func NewHybrid(
	fallback renderer.EngineFactory,
	catalogLookup resolver.CatalogLookup,
	pluginLookup pluginpath.Lookup,
	sharedLoader *wasm.Loader,
) (renderer.EngineFactory, renderer.ProviderVersionResolver) {
	// Memo keyed on the RESOLVED engine identity so two version-distinct bindings
	// of one provider (helm:1.0.0 and helm:1.0.1) coexist as two entries rather
	// than collapsing under a shared bare name, while two bindings that resolve to
	// the same plugin (helm:^1.0 and helm:1.0.5, when 1.0.5 is highest-in-range)
	// share one entry. A Wasm-routed provider keys on the resolved Plugin identity
	// (metadata.name:version); a fallback provider keys on its bare engine kind.
	//
	// RenderAll drives a single sequential per-node loop (plus its pre-pass and
	// post-assembly phases) with no goroutines, and each build constructs its own
	// hybrid over its own loader, so this map is touched by exactly one goroutine
	// over the hybrid's life — a plain map needs no mutex. Should node rendering
	// ever go concurrent, this memo (and the shared loader's per-call load) would
	// need a guard.
	memo := map[string]renderer.Engine{}
	factory := func(providerRef string) (renderer.Engine, error) {
		bare := renderer.ProviderEngineKind(providerRef)
		engine, memoKey, err := routeEngine(providerRef, bare, fallback, catalogLookup, pluginLookup, sharedLoader)
		if err != nil {
			// Either a version-pinned binding that no provider plugin satisfies, or
			// an unknown engine kind surfaced by the lazy fallback construction on the
			// fallback route — halt loudly rather than memoizing a nil engine or
			// silently rendering through the versionless Go engine.
			return nil, err
		}
		if e, ok := memo[memoKey]; ok {
			return e, nil
		}
		memo[memoKey] = engine
		return engine, nil
	}
	// The version resolver shares resolvePluginIdentity with routeEngine, so a
	// binding's reported provider-version is the one behind the engine the factory
	// returns for it. A binding that routes to the fallback engine (no serving
	// render_node plugin, no catalog, or an unsatisfiable pin) is version-less.
	versionResolver := func(providerRef string) string {
		plugin, routed, err := resolvePluginIdentity(providerRef, catalogLookup)
		if err != nil || !routed {
			return ""
		}
		return plugin.Metadata.Version
	}
	return factory, versionResolver
}

// routeEngine decides whether providerRef renders through the Wasm adapter or the
// fallback Go engine, resolving the version-pinned binding → provider-Plugin (via
// the Plugin's compatibleProviders and version constraint) → render_node chain
// against the catalog. It returns the engine and the memo key that identifies it
// (the resolved Plugin identity for a Wasm route, the bare engine kind for a
// fallback). The fallback engine is constructed lazily — routeEngine calls
// fallback only on a non-version break in the chain (the fallback route, keyed on
// bare), so a Wasm-routed binding never constructs a fallback engine. The fallback
// is the safe default whenever the Wasm path is not fully wired. A non-nil error
// is a loud halt: a version pin the catalog cannot satisfy, or an unknown engine
// kind surfaced by the lazy fallback construction on the fallback route.
func routeEngine(
	providerRef, bare string,
	fallback renderer.EngineFactory,
	catalogLookup resolver.CatalogLookup,
	pluginLookup pluginpath.Lookup,
	sharedLoader *wasm.Loader,
) (renderer.Engine, string, error) {
	plugin, routed, err := resolvePluginIdentity(providerRef, catalogLookup)
	if err != nil {
		return nil, "", err
	}
	if !routed {
		// Lazy fallback construction: build the Go fallback engine only now that the
		// binding is known to render through it. An unknown engine kind surfaces the
		// fallback's error here, unchanged — the same loud halt that a genuinely
		// unknown provider produces on this route.
		goEngine, err := fallback(bare)
		if err != nil {
			return nil, "", err
		}
		return goEngine, bare, nil
	}
	// The adapter's lookup key is the resolved Plugin's entity identity
	// (metadata.name:version); the entity-fronted pluginLookup resolves that
	// entity → artifact → cascade. It is also the memo key, so version-distinct
	// resolutions memoize distinctly.
	pluginKey := plugin.Metadata.Name + ":" + plugin.Metadata.Version
	// Two-tier adapter selection. Every routed provider renders per-node through
	// the render adapter; a provider whose Plugin ALSO advertises the
	// assemble_boundary export renders AND assembles through the assembler adapter
	// (which embeds the render Engine, so it is a superset). A provider whose
	// Plugin advertises only render_node — a flat-output provider like helm —
	// stays on the render-only adapter, unchanged.
	//
	// The adapter carries the provider's host-side capability holder — resolved
	// from the provider-name registry, not from a constructed Go engine — so it
	// derives input-kind capabilities from the holder (the gate stays Go-decided)
	// and projects a sourced node's source through the holder's own ProjectSource.
	// The registry shares each provider's holder function with the in-tree engine,
	// so the Wasm route and the fallback route read one source.
	hostCaps, ok := HostCapsFor(bare)
	if !ok {
		// Unreachable in normal flow: fallback(bare) already resolved bare to a
		// shipped engine, and HostCapsFor covers the same provider set (guarded by
		// a parity test). A miss here would mean the two dispatch tables drifted.
		return nil, "", fmt.Errorf("%w: %q", renderer.ErrUnknownProvider, bare)
	}
	if advertisesAssembleBoundary(plugin) {
		return wasmengine.NewAssembler(pluginLookup, pluginKey, hostCaps, sharedLoader), pluginKey, nil
	}
	return wasmengine.New(pluginLookup, pluginKey, hostCaps, sharedLoader), pluginKey, nil
}

// resolvePluginIdentity resolves providerRef to the provider-rendering Plugin
// that serves it and reports whether the render routes through Wasm. It is the
// single resolution both routeEngine (engine selection) and the version resolver
// (the provider-version label and assembler ordering) share, so the version
// reported for a binding is the one behind the engine selected for it. routed is
// false — with a zero Plugin — whenever the binding renders through the fallback
// engine: no catalog accessor, no plugin serving the provider, or a plugin that
// does not advertise render_node. A non-nil error is a loud halt: a version pin
// the catalog cannot satisfy.
func resolvePluginIdentity(providerRef string, catalogLookup resolver.CatalogLookup) (plugin types.Plugin, routed bool, err error) {
	if catalogLookup.ProviderPlugin == nil {
		return types.Plugin{}, false, nil
	}
	p, ok, err := catalogLookup.ProviderPlugin(providerRef)
	if err != nil {
		return types.Plugin{}, false, err
	}
	if !ok || !advertisesRenderNode(p) {
		return types.Plugin{}, false, nil
	}
	return p, true, nil
}

// advertisesRenderNode reports whether the Plugin entity declares the export the
// wasmengine adapter drives. The name is sourced from the adapter itself, so the
// routing precondition and the export the adapter calls are one string.
func advertisesRenderNode(plugin types.Plugin) bool {
	return advertisesExport(plugin, wasmengine.RenderNodeExport)
}

// advertisesAssembleBoundary reports whether the Plugin entity declares the
// export the wasmengine assembler adapter drives. The name is sourced from the
// adapter itself, so the routing precondition and the export the adapter calls
// are one string — mirroring advertisesRenderNode.
func advertisesAssembleBoundary(plugin types.Plugin) bool {
	return advertisesExport(plugin, wasmengine.AssembleBoundaryExport)
}

// advertisesExport reports whether the Plugin entity declares an export of the
// given name.
func advertisesExport(plugin types.Plugin, name string) bool {
	for _, exp := range plugin.Spec.Exports {
		if exp.Name == name {
			return true
		}
	}
	return false
}
