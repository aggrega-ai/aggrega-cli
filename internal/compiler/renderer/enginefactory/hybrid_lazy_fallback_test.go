// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// spyFallback wraps an EngineFactory and records every provider name it is asked
// to construct, so a test can assert whether the hybrid constructed a Go fallback
// engine for a given binding.
type spyFallback struct {
	inner renderer.EngineFactory
	calls []string
}

func (s *spyFallback) build(providerName string) (renderer.Engine, error) {
	s.calls = append(s.calls, providerName)
	return s.inner(providerName)
}

// TestHybridLazyFallbackConstruction proves the Go fallback engine is constructed
// lazily: a Wasm-routed binding constructs NO fallback engine (the spy is never
// called), a fallback-routed binding DOES construct one (the spy is called), and
// an unknown provider still halts loudly on BOTH routes — via HostCapsFor on the
// Wasm route (spy un-called) and via the lazy fallback construction on the
// fallback route (spy called).
func TestHybridLazyFallbackConstruction(t *testing.T) {
	t.Parallel()

	t.Run("wasm route constructs no fallback engine", func(t *testing.T) {
		t.Parallel()
		spy := &spyFallback{inner: enginefactory.Dispatch}
		cat := &catalog.Catalog{
			Plugins: map[string]types.Plugin{
				"helm-renderer:1.0.0": providerRenderPlugin("helm-renderer", "1.0.0", "helm"),
			},
		}
		lookup := resolver.CatalogLookup{ProviderPlugin: cat.ProviderPluginByName}
		factory, _ := enginefactory.NewHybrid(spy.build, lookup, nil, nil)

		engine, err := factory("helm")
		if err != nil || engine == nil {
			t.Fatalf("factory(helm): got (%v, %v); want a non-nil Wasm-routed engine, no error", engine, err)
		}
		if len(spy.calls) != 0 {
			t.Errorf("Wasm-routed helm constructed a fallback engine: spy called for %v; want zero calls", spy.calls)
		}
	})

	t.Run("fallback route constructs the fallback engine", func(t *testing.T) {
		t.Parallel()
		spy := &spyFallback{inner: enginefactory.Dispatch}
		// A nil ProviderPlugin accessor routes every binding to the fallback.
		lookup := resolver.CatalogLookup{}
		factory, _ := enginefactory.NewHybrid(spy.build, lookup, nil, nil)

		engine, err := factory("helm")
		if err != nil || engine == nil {
			t.Fatalf("factory(helm): got (%v, %v); want the fallback engine, no error", engine, err)
		}
		if len(spy.calls) != 1 || spy.calls[0] != "helm" {
			t.Errorf("fallback-routed helm did not construct the fallback engine exactly once: spy called for %v; want [helm]", spy.calls)
		}
	})

	t.Run("unknown provider halts loudly on the fallback route", func(t *testing.T) {
		t.Parallel()
		spy := &spyFallback{inner: enginefactory.Dispatch}
		lookup := resolver.CatalogLookup{} // no accessor → fallback route
		factory, _ := enginefactory.NewHybrid(spy.build, lookup, nil, nil)

		if _, err := factory("bogus"); !errors.Is(err, renderer.ErrUnknownProvider) {
			t.Errorf("factory(bogus) on the fallback route: got err %v; want ErrUnknownProvider", err)
		}
		if len(spy.calls) != 1 || spy.calls[0] != "bogus" {
			t.Errorf("fallback-route unknown provider did not go through the lazy fallback: spy called for %v; want [bogus]", spy.calls)
		}
	})

	t.Run("unknown provider halts loudly on the wasm route", func(t *testing.T) {
		t.Parallel()
		spy := &spyFallback{inner: enginefactory.Dispatch}
		// A render_node plugin serves the unknown provider name, so resolution
		// routes to the Wasm path; HostCapsFor rejects the unknown bare name.
		cat := &catalog.Catalog{
			Plugins: map[string]types.Plugin{
				"bogus-renderer:1.0.0": providerRenderPlugin("bogus-renderer", "1.0.0", "bogus"),
			},
		}
		lookup := resolver.CatalogLookup{ProviderPlugin: cat.ProviderPluginByName}
		factory, _ := enginefactory.NewHybrid(spy.build, lookup, nil, nil)

		if _, err := factory("bogus"); !errors.Is(err, renderer.ErrUnknownProvider) {
			t.Errorf("factory(bogus) on the wasm route: got err %v; want ErrUnknownProvider", err)
		}
		if len(spy.calls) != 0 {
			t.Errorf("wasm-route unknown provider constructed a fallback engine: spy called for %v; want zero calls", spy.calls)
		}
	})
}
