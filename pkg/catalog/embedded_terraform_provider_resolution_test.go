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

// terraformArtifactURI is the bare identity (<socket>/<name>) that is the
// terraform provider plugin's identity — the value the terraform-renderer Plugin
// entity's metadata.name carries and the key the embed tier registers its bytes
// under. No digest literal is frozen here: the reference digest is derived from
// the embed tier's own bytes at test time (embeddedReferenceDigest), so the
// resolution assertion is a self-consistency check against the shipped bytes.
const (
	terraformArtifactURI    = "provider/terraform"
	terraformPluginEntity   = terraformArtifactURI + ":" + testCorePluginVersion
	terraformProviderMemPag = 4096
)

// terraformChainCatalog builds an eager Catalog carrying the terraform-renderer
// provider-rendering Plugin entity with the same field values the shipped hub YAML
// declares, so its CatalogLookup drives the real resolution chain against the
// artifact embedded in the binary. The Plugin serves the "terraform" provider
// name through its targets list and advertises the render_node export
// only — terraform is a flat-output provider with no boundary assembly.
func terraformChainCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Plugins: map[string]types.Plugin{
			terraformPluginEntity: {
				APIVersion: "aggrega.ai/v1alpha1",
				Kind:       "Plugin",
				Metadata:   types.PluginMetadata{Name: "terraform", Version: testCorePluginVersion, PluginType: "provider"},
				Spec: types.PluginSpec{
					AbiVersion: "v1alpha1",
					Provider:   &types.PluginProvider{Capabilities: []string{"terraform"}},
					Exports:    []types.PluginExport{{Name: "render_node"}},
					Runtime:    &types.PluginRuntime{MaxMemoryPages: terraformProviderMemPag},
				},
			},
		},
	}
}

// TestTerraformProviderEmbeddedResolutionChain proves the full
// provider→plugin→artifact resolution chain end-to-end through the real embed
// tier for the terraform provider: the ProviderPlugin accessor (provider name →
// provider-rendering Plugin via targets), the Plugin accessor, the
// entity-fronted cascade, the pinned-digest integrity check (both arms), and the
// entity's runtime limits flowing to the loader spec. It does not re-prove render
// byte-identity — that is the A/B harness's job; this capstone is resolution +
// digest + limits, plus a describe() smoke asserting the render_node export.
func TestTerraformProviderEmbeddedResolutionChain(t *testing.T) {
	cat := terraformChainCatalog()
	lookup := cat.CatalogLookup()

	// Level 1: provider name → provider-rendering Plugin entity, via the
	// ProviderPlugin accessor (targets match).
	if lookup.ProviderPlugin == nil {
		t.Fatal("CatalogLookup.ProviderPlugin accessor is nil; the eager constructor did not wire it")
	}
	pplug, ok, err := lookup.ProviderPlugin("terraform")
	if err != nil {
		t.Fatalf("ProviderPlugin(\"terraform\") errored: %v", err)
	}
	if !ok {
		t.Fatal("ProviderPlugin(\"terraform\") not found")
	}
	if got := pplug.ArtifactReference(); got != terraformArtifactURI {
		t.Fatalf("provider-plugin artifact reference = %q, want %q", got, terraformArtifactURI)
	}

	// Level 2: Plugin-entity URI → Plugin entity → artifact URI. The entity-fronted
	// cascade below resolves the Plugin by this URI.
	if lookup.Plugin == nil {
		t.Fatal("CatalogLookup.Plugin accessor is nil")
	}
	plugin, ok := lookup.Plugin(terraformPluginEntity)
	if !ok {
		t.Fatalf("Plugin(%q) not found", terraformPluginEntity)
	}
	if got := plugin.ArtifactReference(); got != terraformArtifactURI {
		t.Fatalf("plugin artifact reference = %q, want %q", got, terraformArtifactURI)
	}

	t.Run("CleanResolveThroughEmbedTier", func(t *testing.T) {
		refDigest := embeddedReferenceDigest(t, terraformArtifactURI)
		res := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureLockfile, pluginpath.PinMaps{Pins: map[string]string{terraformArtifactURI: refDigest}})
		front := entityFront(lookup, res.Resolve)

		resolved, err := front(terraformPluginEntity)
		if err != nil {
			t.Fatalf("clean resolve halted: %v", err)
		}
		if resolved.Tier != pluginpath.TierEmbed {
			t.Fatalf("resolved from tier %q, want %q — the artifact must resolve through the real embed tier",
				resolved.Tier, pluginpath.TierEmbed)
		}
		if got := digest.ContentDigest(resolved.Bytes); got != refDigest {
			t.Fatalf("resolved bytes digest = %q, want the embed tier's own reference digest %q", got, refDigest)
		}

		// Limits flow end-to-end from the Plugin entity's runtime.maxMemoryPages
		// through the neutral limits (attached by the entity front) into the
		// loader spec. Driven from the entity, never hardcoded on the resolver.
		if resolved.Limits == nil {
			t.Fatal("resolved.Limits is nil; the entity front did not attach the entity's runtime limits")
		}
		spec := resolver.PluginSpec("terraform", resolved)
		if spec.MaxMemoryPages != terraformProviderMemPag {
			t.Fatalf("loader spec MaxMemoryPages = %d, want %d (from Plugin entity runtime)",
				spec.MaxMemoryPages, terraformProviderMemPag)
		}
	})

	t.Run("WrongPinHaltsWithFetchIntegrity", func(t *testing.T) {
		wrongPin := "sha256:" + strings.Repeat("0", 64)
		res := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureLockfile, pluginpath.PinMaps{Pins: map[string]string{terraformArtifactURI: wrongPin}})
		front := entityFront(lookup, res.Resolve)

		_, err := front(terraformPluginEntity)
		if err == nil {
			t.Fatal("resolve with a wrong pin succeeded; expected an integrity halt")
		}
		if !errors.Is(err, oci.ErrFetchIntegrity()) {
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
		res := pluginpath.NewResolver(pluginpath.ResolutionContext{}, pluginpath.PinPostureLockfile, pluginpath.PinMaps{Pins: map[string]string{terraformArtifactURI: embeddedReferenceDigest(t, terraformArtifactURI)}})
		front := entityFront(lookup, res.Resolve)
		resolved, err := front(terraformPluginEntity)
		if err != nil {
			t.Fatalf("resolve for smoke load: %v", err)
		}

		// LoadAndDescribe runs the capability cross-check of the entity's authored
		// exports against the guest's describe() self-report, so a clean load also
		// proves the Plugin entity's authored claims (pluginType/abiVersion/
		// exports) are coherent with the embedded guest.
		spec := resolver.PluginSpec("terraform", resolved)
		loader := &wasm.Loader{}
		ctx := context.Background()
		plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
		if err != nil {
			t.Fatalf("LoadAndDescribe of the embedded guest: %v", err)
		}
		t.Cleanup(func() { _ = plugin.Close(ctx) })

		if desc.PluginType != string(wasm.PluginTypeProvider) {
			t.Fatalf("describe() plugin_type = %q, want %q", desc.PluginType, wasm.PluginTypeProvider)
		}
		if _, ok := findExport(desc, "render_node"); !ok {
			t.Fatal("describe() does not declare the render_node export")
		}
	})
}
