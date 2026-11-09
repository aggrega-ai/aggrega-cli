// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// terraformRouteCatalog builds a CatalogLookup whose ProviderPlugin accessor
// serves the terraform-renderer Plugin for the "terraform" provider name and
// nothing for any other name — the resolution the hybrid router follows to select
// a provider's Wasm rendering guest. The Plugin advertises the render_node export,
// the export the render adapter drives.
func terraformRouteCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		ProviderPlugin: func(ref string) (types.Plugin, bool, error) {
			if renderer.ProviderEngineKind(ref) != "terraform" {
				return types.Plugin{}, false, nil
			}
			return types.Plugin{
				APIVersion: "aggrega.ai/v1beta1",
				Kind:       "Plugin",
				Metadata:   types.PluginMetadata{Name: "terraform", Version: "1.0.0", PluginType: "provider"},
				Spec: types.PluginSpec{
					AbiVersion: "v1beta1",
					Provider:   types.PluginProvider{Capabilities: []string{"terraform"}},
					Exports:    []types.PluginExport{{Name: "render_node"}},
				},
			}, true, nil
		},
	}
}

// TestNewHybrid_RoutesTerraform proves that target: terraform now routes to the
// terraform-renderer Wasm rendering guest rather than halting: the hybrid factory
// resolves the provider through the ProviderPlugin accessor and the host-side
// capability holder, returns a non-nil engine, and reports the resolved provider
// version. A provider name no Plugin serves still halts loudly with
// renderer.ErrUnknownProvider — the flat-output route stays closed for a bogus
// name.
func TestNewHybrid_RoutesTerraform(t *testing.T) {
	t.Parallel()

	lookup := terraformRouteCatalog()
	// The plugin artifact cascade and shared loader are constructor inputs the
	// factory captures but does not exercise until a node actually renders; route
	// selection touches neither. A cascade that always errors makes that explicit —
	// if route selection ever reached it, the test would fail loudly.
	cascade := pluginpath.Lookup(func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{}, errors.New("cascade must not be consulted at route selection")
	})
	loader := &wasm.Loader{}

	factory, versionResolver, _ := enginefactory.NewHybrid(lookup, cascade, loader)

	engine, err := factory("terraform")
	if err != nil {
		t.Fatalf("factory(\"terraform\") errored: %v; expected a routed Wasm engine, not a halt", err)
	}
	if engine == nil {
		t.Fatal("factory(\"terraform\") returned a nil engine")
	}
	if got := versionResolver("terraform"); got != "1.0.0" {
		t.Fatalf("versionResolver(\"terraform\") = %q, want %q (the resolved Plugin version)", got, "1.0.0")
	}

	_, err = factory("no-such-provider")
	if err == nil {
		t.Fatal("factory(\"no-such-provider\") succeeded; expected a loud ErrUnknownProvider halt")
	}
	if !errors.Is(err, renderer.ErrUnknownProvider()) {
		t.Fatalf("factory(\"no-such-provider\") error = %v, want wrapped renderer.ErrUnknownProvider", err)
	}
}
