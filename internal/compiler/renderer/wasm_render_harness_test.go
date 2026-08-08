// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// The orchestrator integration tests drive RenderAll through the same embedded
// provider guests a real build uses, resolved and loaded through the production
// render path (wasmengine adapters over the shared compilation cache). This is
// the Wasm-only successor to the previous in-tree-engine test factories: the
// render backend is the shipped guest, so the tests exercise real per-provider
// render output rather than a Go stand-in.

// renderLoader returns a caching loader. Under `go test` the loader hands every
// test build one shared compilation cache backed by a fixed directory under the
// system temp directory — falling back to an in-memory cache only when that
// directory cannot be created — so each provider guest compiles at most once for
// the whole run rather than once per test, with no per-caller cache setup.
func renderLoader() *wasm.Loader {
	loader := wasm.NewCachingLoader(nil)
	return loader
}

// embeddedProviderURI resolves a bare provider name to the embedded rendering
// guest's canonical URI, matched by the guest artifact's basename.
func embeddedProviderURI(t *testing.T, provider string) string {
	t.Helper()
	// The empty provider name defaults to helm, so the harness routes a
	// provider-less binding exactly as the render route does.
	leaf := map[string]string{
		"":           "helm",
		"helm":       "helm",
		"compose":    "compose",
		"helm-chart": "helm-chart",
		"terraform":  "terraform",
	}[provider]
	if leaf == "" {
		t.Fatalf("no embedded rendering guest known for provider %q", provider)
	}
	for _, uri := range embedded.URIs() {
		if strings.HasSuffix(uri, "/"+leaf) {
			return uri
		}
	}
	t.Fatalf("no embedded guest URI ends with %q; embedded URIs: %v", "/"+leaf, embedded.URIs())
	return ""
}

// embeddedLookup resolves an embedded guest URI to its decompressed bytes,
// mirroring the embed tier of the production plugin-resolution cascade.
func embeddedLookup(uri string) (pluginpath.ResolvedPlugin, error) {
	b, ok := embedded.Lookup(uri)
	if !ok {
		return pluginpath.ResolvedPlugin{}, fmt.Errorf("no embedded guest for %q", uri)
	}
	// Mirror the shipped provider Plugin entities' runtime.maxMemoryPages so the
	// guest loads under the same memory limit a real build grants it.
	return pluginpath.ResolvedPlugin{
		Bytes:  b,
		URI:    uri,
		Tier:   pluginpath.TierEmbed,
		Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096},
	}, nil
}

// wasmEngineFactory builds an EngineFactory that renders every node through the
// embedded provider guest — the same guests and the same wasmengine render path
// a real build routes through. A boundary-assembling provider (compose,
// helm-chart) routes to the assembler adapter; a flat-output provider (helm) to
// the render-only adapter, exactly as the production hybrid selects by
// host-capability.
func wasmEngineFactory(t *testing.T) renderer.EngineFactory {
	t.Helper()
	loader := renderLoader()
	// One engine per provider for the life of the test, the harness counterpart of
	// the render path's per-build memo. The render path calls the factory once per
	// node, and again for any feature-recipe dispatch or codec probe that needs an
	// engine, so an un-memoized factory builds engines in proportion to the node
	// count rather than the provider count — and each one holds its guest's
	// reusable instance until the test ends. RenderAll drives a single sequential
	// loop with no goroutines and each test builds its own factory, so this map is
	// touched by one goroutine over its life and needs no guard. Should node
	// rendering ever go concurrent, it would.
	memo := map[string]renderer.Engine{}
	return func(providerRef string) (renderer.Engine, error) {
		bare := renderer.ProviderEngineKind(providerRef)
		switch bare {
		case "", "helm", "compose", "helm-chart", "terraform":
		default:
			return nil, fmt.Errorf("%w: %q", renderer.ErrUnknownProvider(), providerRef)
		}
		// The guest URI below is derived from the bare kind alone, so the bare kind
		// is this factory's whole engine identity — the harness counterpart of the
		// render path's key on resolved plugin identity. A hit returns the same
		// instance and constructs nothing, which the factory contract permits: the
		// orchestrator makes no assumption about engine instance identity.
		if engine, ok := memo[bare]; ok {
			return engine, nil
		}
		uri := embeddedProviderURI(t, bare)
		// A boundary-assembling provider routes to the assembler adapter; a
		// flat-output provider to the render-only adapter, exactly as the production
		// hybrid selects. The assembly mode is the guest's live describe()
		// self-report, read here by probing a render-only engine's classification.
		engine := wasmengine.New(embeddedLookup, uri, loader)
		// Release each engine's reused plugin instance when the test ends, the
		// harness counterpart of the render path's release at build end: a guest
		// that declares a reusable instance keeps that instance on the engine until
		// Close, so an unclosed engine outlives its test and the instances
		// accumulate for the life of the test binary. Closing an engine that holds
		// no instance — the classification probe above — is a no-op.
		t.Cleanup(func() { _ = engine.Close(context.Background()) })
		if renderer.AssemblesBoundary(engine) {
			assembler := wasmengine.NewAssembler(embeddedLookup, uri, loader)
			t.Cleanup(func() { _ = assembler.Close(context.Background()) })
			memo[bare] = assembler
			return assembler, nil
		}
		memo[bare] = engine
		return engine, nil
	}
}
