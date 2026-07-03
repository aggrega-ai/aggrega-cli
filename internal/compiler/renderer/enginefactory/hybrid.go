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
// provider whose catalog Provider resolves, through its named Plugin entity, to
// a Wasm rendering plugin advertising a render_node export renders through the
// wasmengine adapter; every other provider renders through the fallback engine
// exactly as before.
//
// The fallback is authoritative for which providers exist and for engine
// capabilities: a provider the fallback rejects (unknown name) surfaces that
// error unchanged, and a routed Wasm engine reports the fallback engine's own
// input-kind capabilities, so the render-time input-kind gate stays Go-decided.
// The Wasm engine loads through sharedLoader (one compilation cache warmed once
// per build) and resolves its bytes through pluginLookup (the entity-fronted
// artifact cascade), keyed by the Provider's Plugin-entity URI.
//
// The routing decision falls back to the fallback engine on EVERY miss in the
// resolve chain — a nil catalog accessor (catalog-less paths leave the
// CatalogLookup fields nil), an unresolved Provider or Plugin, a Provider with
// no Plugin binding, or a Plugin that does not advertise the render_node export.
// This is the no-regression mechanism: a catalog that does not wire a provider
// to a render_node-advertising Wasm plugin renders through the same Go engine it
// always has, so wrapping the dispatch changes nothing until a Provider is
// explicitly bound to such a plugin.
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
// the fallback Go engine, resolving the Provider → Plugin → render_node chain
// against the catalog. Any break in the chain returns goEngine, so the fallback
// is the safe default whenever the Wasm path is not fully wired.
func routeEngine(
	providerName string,
	goEngine renderer.Engine,
	catalogLookup resolver.CatalogLookup,
	pluginLookup pluginpath.Lookup,
	sharedLoader *wasm.Loader,
) renderer.Engine {
	if catalogLookup.Provider == nil {
		return goEngine
	}
	prov, ok := catalogLookup.Provider(providerName)
	if !ok || prov.Spec.Plugin == "" {
		return goEngine
	}
	if catalogLookup.Plugin == nil {
		return goEngine
	}
	plugin, ok := catalogLookup.Plugin(prov.Spec.Plugin)
	if !ok || !advertisesRenderNode(plugin) {
		return goEngine
	}
	// Pass the Provider's Plugin-entity URI as the adapter's lookup key: the
	// entity-fronted pluginLookup resolves entity → artifact → cascade. Wrap the
	// Go engine so the adapter derives input-kind capabilities from it (the gate
	// stays Go-decided) and projects a sourced node's source through its own
	// SourceProjector.
	return wasmengine.New(pluginLookup, prov.Spec.Plugin, goEngine, sharedLoader)
}

// advertisesRenderNode reports whether the Plugin entity declares the export the
// wasmengine adapter drives. The name is sourced from the adapter itself, so the
// routing precondition and the export the adapter calls are one string.
func advertisesRenderNode(plugin types.Plugin) bool {
	for _, exp := range plugin.Spec.Exports {
		if exp.Name == wasmengine.RenderNodeExport {
			return true
		}
	}
	return false
}
