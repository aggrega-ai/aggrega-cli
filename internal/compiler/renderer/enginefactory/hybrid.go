// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory

import (
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
func NewHybrid(
	fallback renderer.EngineFactory,
	catalogLookup resolver.CatalogLookup,
	pluginLookup pluginpath.Lookup,
	sharedLoader *wasm.Loader,
) renderer.EngineFactory {
	// Per-provider-name memo. RenderAll drives a single sequential per-node
	// loop (plus its pre-pass and post-assembly phases) with no goroutines, and
	// each build constructs its own hybrid over its own loader, so this map is
	// touched by exactly one goroutine over the hybrid's life — a plain map
	// needs no mutex. Should node rendering ever go concurrent, this memo (and
	// the shared loader's per-call load) would need a guard.
	memo := map[string]renderer.Engine{}
	return func(providerName string) (renderer.Engine, error) {
		if e, ok := memo[providerName]; ok {
			return e, nil
		}
		goEngine, err := fallback(providerName)
		if err != nil {
			// Unknown provider — surface the fallback's error exactly as today,
			// without memoizing a nil engine.
			return nil, err
		}
		engine := routeEngine(providerName, goEngine, catalogLookup, pluginLookup, sharedLoader)
		memo[providerName] = engine
		return engine, nil
	}
}

// routeEngine decides whether providerName renders through the Wasm adapter or
// the fallback Go engine, resolving the provider name → provider-Plugin (via the
// Plugin's compatibleProviders) → render_node chain against the catalog. Any
// break in the chain returns goEngine, so the fallback is the safe default
// whenever the Wasm path is not fully wired.
func routeEngine(
	providerName string,
	goEngine renderer.Engine,
	catalogLookup resolver.CatalogLookup,
	pluginLookup pluginpath.Lookup,
	sharedLoader *wasm.Loader,
) renderer.Engine {
	if catalogLookup.ProviderPlugin == nil {
		return goEngine
	}
	plugin, ok := catalogLookup.ProviderPlugin(providerName)
	if !ok || !advertisesRenderNode(plugin) {
		return goEngine
	}
	// The adapter's lookup key is the resolved Plugin's entity identity
	// (metadata.name:version); the entity-fronted pluginLookup resolves that
	// entity → artifact → cascade.
	pluginKey := plugin.Metadata.Name + ":" + plugin.Metadata.Version
	// Two-tier adapter selection. Every routed provider renders per-node through
	// the render adapter; a provider whose Plugin ALSO advertises the
	// assemble_boundary export renders AND assembles through the assembler adapter
	// (which embeds the render Engine, so it is a superset). A provider whose
	// Plugin advertises only render_node — a flat-output provider like helm —
	// stays on the render-only adapter, unchanged.
	//
	// Wrap the Go engine so the adapter derives input-kind capabilities from it
	// (the gate stays Go-decided) and projects a sourced node's source through its
	// own SourceProjector.
	if advertisesAssembleBoundary(plugin) {
		return wasmengine.NewAssembler(pluginLookup, pluginKey, goEngine, sharedLoader)
	}
	return wasmengine.New(pluginLookup, pluginKey, goEngine, sharedLoader)
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
