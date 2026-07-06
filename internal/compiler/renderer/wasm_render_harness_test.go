// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
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

// renderLoader returns a caching loader. Under `go test` the loader's
// compilation cache is the process-wide in-memory singleton the loader package
// hands every test build, so each provider guest compiles once for the whole
// test process rather than once per test, with no per-caller cache setup.
func renderLoader() *wasm.Loader {
	loader := wasm.NewCachingLoader(nil)
	return loader
}

// embeddedProviderURI resolves a bare provider name to the embedded rendering
// guest's canonical URI, matched by the guest artifact's basename.
func embeddedProviderURI(t *testing.T, provider string) string {
	t.Helper()
	// The empty provider name defaults to helm, mirroring HostCapsFor's own
	// empty-string default so the harness routes a provider-less binding exactly
	// as the render route does.
	basename := map[string]string{
		"":           "helm-provider",
		"helm":       "helm-provider",
		"compose":    "compose-provider",
		"helm-chart": "helmchart-provider",
	}[provider]
	if basename == "" {
		t.Fatalf("no embedded rendering guest known for provider %q", provider)
	}
	for _, uri := range embedded.URIs() {
		if strings.Contains(uri, basename) {
			return uri
		}
	}
	t.Fatalf("no embedded guest URI carries %q; embedded URIs: %v", basename, embedded.URIs())
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
	return func(providerRef string) (renderer.Engine, error) {
		bare := renderer.ProviderEngineKind(providerRef)
		hostCaps, ok := enginefactory.HostCapsFor(bare)
		if !ok {
			return nil, fmt.Errorf("%w: %q", renderer.ErrUnknownProvider, providerRef)
		}
		uri := embeddedProviderURI(t, bare)
		if hostCaps.AssemblesBoundary {
			return wasmengine.NewAssembler(embeddedLookup, uri, hostCaps, loader), nil
		}
		return wasmengine.New(embeddedLookup, uri, hostCaps, loader), nil
	}
}
