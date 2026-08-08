// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory

import (
	"context"
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// engineCloser is the optional lifecycle interface a routed engine implements to
// release a reused plugin instance at build end. The wasmengine adapters
// implement it (Close); the closer NewHybrid returns walks the memoized engines
// and invokes it on each.
type engineCloser interface {
	Close(ctx context.Context) error
}

// NewHybrid builds a catalog-driven Wasm routing EngineFactory: a provider whose
// name resolves to a provider-rendering Plugin — the Plugin that lists the
// provider in its targets — advertising a render_node export renders
// through the wasmengine adapter. A provider that resolves to no such Plugin is a
// loud halt (renderer.ErrUnknownProvider); rendering is Wasm-only, so there is no
// in-tree engine to fall back to.
//
// The Wasm route has two tiers, selected by the Plugin entity's advertised
// exports. A Plugin advertising only render_node routes to the render-only
// adapter (per-node render, host-side assembly for a flat-output provider). A
// Plugin advertising render_node AND assemble_boundary routes to the assembler
// adapter, which renders per-node AND merges the dispatch group's fragments
// through the guest — so a full provider's per-group assembly (an umbrella chart,
// a merged compose file) runs in Wasm too. The assembler adapter embeds the
// render adapter, so the render behavior is identical across the two tiers.
//
// A routed Wasm engine reports its behaviour — input kinds, boundary-assembly
// mode, codec, and optional exports (source resolution, sub-chart assembly,
// boundary env overrides) — from its own describe() self-report, so the compiler
// carries no per-provider host holder for a standard provider. The Wasm engine
// loads through sharedLoader (one compilation cache warmed once per build) and
// resolves its bytes through pluginLookup (the entity-fronted artifact cascade),
// keyed by the resolved Plugin's entity identity.
//
// NewHybrid also returns a [renderer.ProviderVersionResolver] alongside the
// factory: it reports the resolved provider-rendering plugin version behind a
// binding (the version the routed engine renders through), or "" for a binding
// that resolves to no serving plugin. Both share one plugin-resolution pass so
// the version reported and the engine returned for a binding always agree. The
// orchestrator stamps that version on the aggrega.ai/provider-version label and
// uses it to pick a mixed-version dispatch group's boundary assembler.
//
// The third return is a build-end closer: it walks the memoized engines and
// releases any reused plugin instance each holds (a guest that declares
// reusable_instance is loaded once per build and its single instance lives on the
// per-build engine). The caller defers it after the render completes. A guest that
// makes no reuse claim holds no instance, so its close is a no-op. The closer is
// idempotent and safe to call once per build.
func NewHybrid(
	catalogLookup resolver.CatalogLookup,
	pluginLookup pluginpath.Lookup,
	sharedLoader *wasm.Loader,
) (renderer.EngineFactory, renderer.ProviderVersionResolver, func(context.Context) error) {
	// Memo keyed on the RESOLVED plugin identity so two version-distinct bindings
	// of one provider (helm:1.0.0 and helm:1.0.1) coexist as two entries rather
	// than collapsing under a shared bare name, while two bindings that resolve to
	// the same plugin (helm:^1.0 and helm:1.0.5, when 1.0.5 is highest-in-range)
	// share one entry.
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
		engine, memoKey, err := routeEngine(providerRef, bare, catalogLookup, pluginLookup, sharedLoader)
		if err != nil {
			// A version-pinned binding no provider plugin satisfies, or a provider
			// name no serving render_node plugin resolves — halt loudly rather than
			// memoizing a nil engine.
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
	// returns for it. A binding that resolves to no serving render_node plugin (no
	// catalog, or an unsatisfiable pin) is version-less.
	versionResolver := func(providerRef string) string {
		plugin, routed, err := resolvePluginIdentity(providerRef, catalogLookup)
		if err != nil || !routed {
			return ""
		}
		return plugin.Metadata.Version
	}
	// The build-end closer releases each memoized engine's reused instance. It
	// reads the same memo the factory populates; the render loop is a single
	// sequential goroutine and the closer runs after it, so the map is touched by
	// one goroutine over its life. Iteration order does not matter — closing
	// produces no rendered output — so a plain map range is deterministic-safe here.
	closeEngines := func(ctx context.Context) error {
		var errs []error
		for _, engine := range memo {
			if c, ok := engine.(engineCloser); ok {
				if err := c.Close(ctx); err != nil {
					errs = append(errs, err)
				}
			}
		}
		return errors.Join(errs...)
	}
	return factory, versionResolver, closeEngines
}

// routeEngine resolves providerRef to its provider-rendering Wasm adapter,
// following the version-pinned binding → provider-Plugin (via the Plugin's
// targets and version constraint) → render_node chain against the
// catalog. It returns the engine and the memo key that identifies it (the
// resolved Plugin identity). A provider that resolves to no serving render_node
// plugin is a loud halt (renderer.ErrUnknownProvider): rendering is Wasm-only, so
// there is no in-tree engine to fall back to. A non-nil error is also a loud halt
// for a version pin the catalog cannot satisfy.
func routeEngine(
	providerRef, bare string,
	catalogLookup resolver.CatalogLookup,
	pluginLookup pluginpath.Lookup,
	sharedLoader *wasm.Loader,
) (renderer.Engine, string, error) {
	plugin, routed, err := resolvePluginIdentity(providerRef, catalogLookup)
	if err != nil {
		return nil, "", err
	}
	if !routed {
		// No serving render_node plugin (a nil catalog accessor, a provider name no
		// provider-Plugin serves, or a Plugin that does not advertise render_node).
		// Rendering is Wasm-only, so this halts loudly rather than falling back.
		return nil, "", fmt.Errorf("%w: %q", renderer.ErrUnknownProvider(), bare)
	}
	// The adapter's lookup key is the resolved Plugin's entity identity coordinate
	// (<pluginType>/<name>:version); the entity-fronted pluginLookup resolves that
	// entity → artifact → cascade. It is also the memo key, so version-distinct
	// resolutions memoize distinctly.
	pluginKey := plugin.Coordinate() + ":" + plugin.Metadata.Version
	// Two-tier adapter selection. Every routed provider renders per-node through
	// the render adapter; a provider whose Plugin ALSO advertises the
	// assemble_boundary export renders AND assembles through the assembler adapter
	// (which embeds the render Engine, so it is a superset). A provider whose
	// Plugin advertises only render_node — a flat-output provider like helm —
	// stays on the render-only adapter, unchanged.
	//
	// A sourced node's source is handed to the guest as a name-only listing the
	// guest resolves and packs itself, not through any host-side projection, and
	// the guest self-reports its input kinds, boundary-assembly mode, codec, and
	// optional exports through describe() — so the adapter carries no per-provider
	// host holder. The unknown-provider halt lives once, above, on the
	// catalog-resolution check (!routed): a provider with no serving render_node
	// plugin halts there.
	if advertisesAssembleBoundary(plugin) {
		return wasmengine.NewAssembler(pluginLookup, pluginKey, sharedLoader), pluginKey, nil
	}
	return wasmengine.New(pluginLookup, pluginKey, sharedLoader), pluginKey, nil
}

// resolvePluginIdentity resolves providerRef to the provider-rendering Plugin
// that serves it and reports whether the render routes through Wasm. It is the
// single resolution both routeEngine (engine selection) and the version resolver
// (the provider-version label and assembler ordering) share, so the version
// reported for a binding is the one behind the engine selected for it. routed is
// false — with a zero Plugin — whenever no serving plugin resolves: no catalog
// accessor, no plugin serving the provider, or a plugin that does not advertise
// render_node. A non-nil error is a loud halt: a version pin the catalog cannot
// satisfy.
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
