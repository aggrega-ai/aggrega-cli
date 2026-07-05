// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stubEngine is a minimal renderer.Engine standing in for an in-tree Go engine
// so the hybrid resolves without any real Wasm artifact. Identity (pointer) is
// what the coexistence assertions compare, so each fallback call returns a fresh
// instance.
type stubEngine struct{ kind string }

func (stubEngine) Render(context.Context, map[string]any, renderer.NodeRenderContext) (map[string][]byte, error) {
	return nil, nil
}

func (stubEngine) Capabilities() renderer.ProviderCapabilities {
	return renderer.ProviderCapabilities{}
}

func stubFallback(name string) (renderer.Engine, error) {
	switch name {
	case "helm", "eval":
		return &stubEngine{kind: name}, nil
	default:
		return nil, fmt.Errorf("stub fallback: unknown engine kind %q", name)
	}
}

func providerRenderPlugin(name, version, provider string) types.Plugin {
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: name, Version: version},
		Spec: types.PluginSpec{
			PluginType:          "provider",
			CompatibleProviders: []string{provider},
			Exports:             []types.PluginExport{{Name: wasmengine.RenderNodeExport}},
		},
	}
}

// TestHybridVersionPinnedProvidersCoexist proves the version-pinned provider
// contract end-to-end through the hybrid engine factory against synthetic
// provider-rendering Plugins (no real Wasm): two recipes pinning distinct plugin
// versions of one engine resolve to distinct coexisting engines in a single
// build (the memo holds two entries, not one collapsed entry); a bare binding
// resolves to the highest version; a bare engine kind with no serving plugin
// falls back to the in-tree engine; and a version pin nothing satisfies halts
// loudly with the provider-version sentinel.
func TestHybridVersionPinnedProvidersCoexist(t *testing.T) {
	t.Parallel()

	cat := &catalog.Catalog{
		Plugins: map[string]types.Plugin{
			"helm-renderer:1.0.0": providerRenderPlugin("helm-renderer", "1.0.0", "helm"),
			"helm-renderer:1.0.1": providerRenderPlugin("helm-renderer", "1.0.1", "helm"),
		},
	}
	lookup := resolver.CatalogLookup{ProviderPlugin: cat.ProviderPluginByName}
	factory, providerVersion := enginefactory.NewHybrid(stubFallback, lookup, nil, nil)

	// Same pin, twice — the memo returns the identical instance.
	e100a, err := factory("helm:1.0.0")
	if err != nil || e100a == nil {
		t.Fatalf("factory(helm:1.0.0): got (%v, %v); want a non-nil engine, no error", e100a, err)
	}
	e100b, err := factory("helm:1.0.0")
	if err != nil {
		t.Fatalf("factory(helm:1.0.0) second call errored: %v", err)
	}
	if e100a != e100b {
		t.Errorf("memo returned distinct instances for the same pin helm:1.0.0; the memo is not caching by resolved identity")
	}

	// Distinct pins of one engine coexist — NOT collapsed under a shared name.
	e101, err := factory("helm:1.0.1")
	if err != nil || e101 == nil {
		t.Fatalf("factory(helm:1.0.1): got (%v, %v); want a non-nil engine, no error", e101, err)
	}
	if e100a == e101 {
		t.Errorf("helm:1.0.0 and helm:1.0.1 collapsed to one engine; version-distinct plugins must coexist as two memo entries")
	}

	// Bare binding resolves to the highest available version (1.0.1), i.e. the
	// same resolved plugin identity as the explicit helm:1.0.1 pin.
	eHighest, err := factory("helm")
	if err != nil || eHighest == nil {
		t.Fatalf("factory(helm): got (%v, %v); want a non-nil engine, no error", eHighest, err)
	}
	if eHighest != e101 {
		t.Errorf("bare helm did not resolve to the highest-version engine (helm:1.0.1)")
	}

	// A bare engine kind no provider plugin serves falls back to the in-tree
	// engine — the no-regression path, unchanged.
	eEval, err := factory("eval")
	if err != nil || eEval == nil {
		t.Fatalf("factory(eval): got (%v, %v); want the fallback engine, no error", eEval, err)
	}
	if eEval == e100a || eEval == e101 || eEval == eHighest {
		t.Errorf("eval routed to a helm provider engine; a served-by-no-plugin kind must fall back to its own in-tree engine")
	}

	// A version pin nothing satisfies halts loudly with the provider-version
	// sentinel, rather than silently falling back to the versionless engine.
	if _, err := factory("helm:^9.0"); !errors.Is(err, catalog.ErrProviderVersionUnsatisfiable) {
		t.Errorf("factory(helm:^9.0): got err %v; want ErrProviderVersionUnsatisfiable", err)
	}

	// The version resolver reports the resolved plugin version behind each
	// binding — the one the routed engine renders through — and "" for a binding
	// served by no render_node plugin (the fallback route). It shares the
	// factory's resolution, so its answers track the engines selected above.
	for _, tc := range []struct {
		ref  string
		want string
	}{
		{"helm:1.0.0", "1.0.0"}, // exact pin → that plugin version
		{"helm:1.0.1", "1.0.1"},
		{"helm", "1.0.1"}, // bare → highest available
		{"eval", ""},      // served by no render_node plugin → version-less
	} {
		if got := providerVersion(tc.ref); got != tc.want {
			t.Errorf("providerVersion(%q) = %q; want %q", tc.ref, got, tc.want)
		}
	}
}
