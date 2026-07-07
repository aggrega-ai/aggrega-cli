// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These constants pin the embedded helm provider artifact. The digest is the
// uncompressed content digest of the artifact compiled into the binary and
// registered by internal/wasm/embedded; it MUST equal the digest in that
// package's registration URI and in the helm-renderer Plugin entity's
// spec.plugin. build-embedded-helm-provider.sh recomputes it when the guest
// source changes.
const (
	helmArtifactDigest = "sha256:b05aebcaef785d6fa488b3c932072f79b5ed9c871c476685fc34852a05559061"
	helmArtifactURI    = "oci://hub.aggrega.ai/plugins/helm-provider@" + helmArtifactDigest
	helmPluginEntity   = "helm-renderer:1.0.0"
	helmProviderMemPag = 4096
)

// helmChainCatalog builds an eager Catalog carrying the helm-renderer
// provider-rendering Plugin entity with the same field values the shipped hub YAML
// declares, so its CatalogLookup drives the real resolution chain against the
// artifact embedded in the binary. The Plugin serves the "helm" provider name
// through its compatibleProviders list.
func helmChainCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Plugins: map[string]types.Plugin{
			"helm-renderer:1.0.0": {
				APIVersion: "aggrega.ai/v1alpha1",
				Kind:       "Plugin",
				Metadata:   types.PluginMetadata{Name: "helm-renderer", Version: "1.0.0"},
				Spec: types.PluginSpec{
					Plugin:              helmArtifactURI,
					PluginType:          "provider",
					AbiVersion:          "v1alpha1",
					CompatibleProviders: []string{"helm"},
					Exports:             []types.PluginExport{{Name: "render_node"}},
					Runtime:             &types.PluginRuntime{MaxMemoryPages: helmProviderMemPag},
				},
			},
		},
	}
}

// entityFront builds the entity-fronted plugin Lookup over lookup's Plugin
// accessor and cascade — the same two-step (Plugin entity URI → spec.plugin →
// cascade) the build-load pipeline binds, so the test exercises the production
// resolution seam rather than a bypass.
func entityFront(lookup resolver.CatalogLookup, cascade pluginpath.Lookup) pluginpath.Lookup {
	return pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginCapabilityClaim, *pluginpath.PluginRuntimeLimits, bool) {
		p, ok := lookup.Plugin(uri)
		if !ok {
			return "", nil, nil, false
		}
		return p.Spec.Plugin, resolver.PluginCapabilityClaim(p), resolver.PluginRuntimeLimits(p), true
	}, cascade)
}

// TestHelmProviderEmbeddedResolutionChain proves the full provider→plugin→artifact
// resolution chain end-to-end through the real embed tier: the ProviderPlugin
// accessor on resolver.CatalogLookup (provider name → provider-rendering Plugin
// via compatibleProviders), the Plugin accessor, the entity-fronted cascade, the
// pinned-digest integrity check (both arms), and the entity's runtime limits
// flowing to the loader spec. It does not re-prove render byte-identity — that is
// the A/B harness's job; this capstone is resolution + digest + limits.
func TestHelmProviderEmbeddedResolutionChain(t *testing.T) {
	cat := helmChainCatalog()
	lookup := cat.CatalogLookup()

	// Level 1: provider name → provider-rendering Plugin entity, via the
	// ProviderPlugin accessor wired into the eager CatalogLookup constructor.
	// Render dispatch resolves a provider name to the Plugin serving it through
	// the Plugin's compatibleProviders list.
	if lookup.ProviderPlugin == nil {
		t.Fatal("CatalogLookup.ProviderPlugin accessor is nil; the eager constructor did not wire it")
	}
	pplug, ok, err := lookup.ProviderPlugin("helm")
	if err != nil {
		t.Fatalf("ProviderPlugin(\"helm\") errored: %v", err)
	}
	if !ok {
		t.Fatal("ProviderPlugin(\"helm\") not found")
	}
	if pplug.Spec.Plugin != helmArtifactURI {
		t.Fatalf("provider-plugin spec.plugin = %q, want %q", pplug.Spec.Plugin, helmArtifactURI)
	}

	// Level 2: Plugin-entity URI → Plugin entity → artifact URI. The entity-fronted
	// cascade below resolves the Plugin by this URI.
	if lookup.Plugin == nil {
		t.Fatal("CatalogLookup.Plugin accessor is nil")
	}
	plugin, ok := lookup.Plugin(helmPluginEntity)
	if !ok {
		t.Fatalf("Plugin(%q) not found", helmPluginEntity)
	}
	if plugin.Spec.Plugin != helmArtifactURI {
		t.Fatalf("plugin spec.plugin = %q, want %q", plugin.Spec.Plugin, helmArtifactURI)
	}

	t.Run("CleanResolveThroughEmbedTier", func(t *testing.T) {
		res := pluginpath.Resolver{Pins: map[string]string{helmArtifactURI: helmArtifactDigest}}
		front := entityFront(lookup, res.Resolve)

		resolved, err := front(helmPluginEntity)
		if err != nil {
			t.Fatalf("clean resolve halted: %v", err)
		}
		if resolved.Tier != pluginpath.TierEmbed {
			t.Fatalf("resolved from tier %q, want %q — the artifact must resolve through the real embed tier",
				resolved.Tier, pluginpath.TierEmbed)
		}
		if got := digest.ContentDigest(resolved.Bytes); got != helmArtifactDigest {
			t.Fatalf("resolved bytes digest = %q, want %q", got, helmArtifactDigest)
		}

		// Limits flow end-to-end from the Plugin entity's runtime.maxMemoryPages
		// through the neutral limits (attached by the entity front) into the
		// loader spec. Driven from the entity, never hardcoded on the resolver.
		if resolved.Limits == nil {
			t.Fatal("resolved.Limits is nil; the entity front did not attach the entity's runtime limits")
		}
		spec := resolver.PluginSpec("helm", resolved)
		if spec.MaxMemoryPages != helmProviderMemPag {
			t.Fatalf("loader spec MaxMemoryPages = %d, want %d (from Plugin entity runtime)",
				spec.MaxMemoryPages, helmProviderMemPag)
		}
	})

	t.Run("WrongPinHaltsWithFetchIntegrity", func(t *testing.T) {
		wrongPin := "sha256:" + strings.Repeat("0", 64)
		res := pluginpath.Resolver{Pins: map[string]string{helmArtifactURI: wrongPin}}
		front := entityFront(lookup, res.Resolve)

		_, err := front(helmPluginEntity)
		if err == nil {
			t.Fatal("resolve with a wrong pin succeeded; expected an integrity halt")
		}
		if !errors.Is(err, oci.ErrFetchIntegrity) {
			t.Fatalf("wrong-pin resolve error = %v, want wrapped oci.ErrFetchIntegrity", err)
		}
	})

	t.Run("LoadAndDescribeSmoke", func(t *testing.T) {
		if testing.Short() {
			// Compiling the multi-MB embedded guest through the Wasm runtime is
			// seconds-scale; the resolution + digest + limits proof above is the
			// fast core. This subtest is the optional loadability + describe()
			// coherence bonus, skipped under -short.
			t.Skip("skipping embedded-guest load under -short")
		}
		res := pluginpath.Resolver{Pins: map[string]string{helmArtifactURI: helmArtifactDigest}}
		front := entityFront(lookup, res.Resolve)
		resolved, err := front(helmPluginEntity)
		if err != nil {
			t.Fatalf("resolve for smoke load: %v", err)
		}

		// LoadAndDescribe runs the capability cross-check of the entity's authored
		// exports against the guest's describe() self-report, so a clean load also
		// proves the Plugin entity's authored claims (pluginType/abiVersion/
		// exports) are coherent with the embedded guest.
		spec := resolver.PluginSpec("helm", resolved)
		loader := &wasm.Loader{}
		ctx := context.Background()
		plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
		if err != nil {
			t.Fatalf("LoadAndDescribe of the embedded guest: %v", err)
		}
		t.Cleanup(func() { _ = plugin.Close(ctx) })

		if desc.PluginType != wasm.PluginTypeProvider {
			t.Fatalf("describe() plugin_type = %q, want %q", desc.PluginType, wasm.PluginTypeProvider)
		}
		if _, ok := findExport(desc, "render_node"); !ok {
			t.Fatal("describe() does not declare the render_node export")
		}
	})
}

// findExport reports whether the descriptor declares an export of the given name.
func findExport(desc wasm.PluginDescriptor, name string) (wasm.PluginExport, bool) {
	for _, e := range desc.Exports {
		if e.Name == name {
			return e, true
		}
	}
	return wasm.PluginExport{}, false
}
