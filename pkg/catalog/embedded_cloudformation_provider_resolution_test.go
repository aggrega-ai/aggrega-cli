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

// cloudformationArtifactURI is the digest-free repository reference that is the
// cloudformation provider plugin's identity — the value the cloudformation-renderer
// Plugin entity's spec.plugin carries and the key the embed tier registers its
// bytes under. No digest literal is frozen here: the reference digest is derived
// from the embed tier's own bytes at test time (embeddedReferenceDigest), so the
// resolution assertion is a self-consistency check against the shipped bytes.
const (
	cloudformationArtifactURI    = "oci://hub.aggrega.ai/plugins/core/cloudformation-provider"
	cloudformationPluginEntity   = "cloudformation-renderer:0.4.1"
	cloudformationProviderMemPag = 4096
)

// cloudformationChainCatalog builds an eager Catalog carrying the
// cloudformation-renderer provider-rendering Plugin entity with the same field
// values the shipped hub YAML declares, so its CatalogLookup drives the real
// resolution chain against the artifact embedded in the binary. The Plugin serves
// the "cloudformation" provider name through its targets list and advertises the
// render_node, validate_node_input, assemble_boundary, link_publish, and
// link_resolve exports — the merge-plus-cross-boundary-linking signal the guest
// self-reports.
func cloudformationChainCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Plugins: map[string]types.Plugin{
			"cloudformation-renderer:0.4.1": {
				APIVersion: "aggrega.ai/v1alpha1",
				Kind:       "Plugin",
				Metadata:   types.PluginMetadata{Name: "cloudformation-renderer", Version: "0.4.1"},
				Spec: types.PluginSpec{
					Plugin:     cloudformationArtifactURI,
					PluginType: "provider",
					AbiVersion: "v1alpha1",
					Targets:    []string{"cloudformation"},
					Exports: []types.PluginExport{
						{Name: "render_node"},
						{Name: "validate_node_input"},
						{Name: "assemble_boundary"},
						{Name: "link_publish"},
						{Name: "link_resolve"},
					},
					Runtime: &types.PluginRuntime{MaxMemoryPages: cloudformationProviderMemPag},
				},
			},
		},
	}
}

// TestCloudFormationProviderEmbeddedResolutionChain proves the full
// provider→plugin→artifact resolution chain end-to-end through the real embed tier
// for the cloudformation provider: the ProviderPlugin accessor (provider name →
// provider-rendering Plugin via targets), the Plugin accessor, the entity-fronted
// cascade, the pinned-digest integrity check (both arms), and the entity's runtime
// limits flowing to the loader spec. It does not re-prove render byte-identity;
// this capstone is resolution + digest + limits, plus a describe() smoke asserting
// the five exports AND the cross-boundary-linking capability the guest declares.
func TestCloudFormationProviderEmbeddedResolutionChain(t *testing.T) {
	cat := cloudformationChainCatalog()
	lookup := cat.CatalogLookup()

	// Level 1: provider name → provider-rendering Plugin entity, via the
	// ProviderPlugin accessor (targets match).
	if lookup.ProviderPlugin == nil {
		t.Fatal("CatalogLookup.ProviderPlugin accessor is nil; the eager constructor did not wire it")
	}
	pplug, ok, err := lookup.ProviderPlugin("cloudformation")
	if err != nil {
		t.Fatalf("ProviderPlugin(\"cloudformation\") errored: %v", err)
	}
	if !ok {
		t.Fatal("ProviderPlugin(\"cloudformation\") not found")
	}
	if pplug.Spec.Plugin != cloudformationArtifactURI {
		t.Fatalf("provider-plugin spec.plugin = %q, want %q", pplug.Spec.Plugin, cloudformationArtifactURI)
	}

	// Level 2: Plugin-entity URI → Plugin entity → artifact URI. The entity-fronted
	// cascade below resolves the Plugin by this URI.
	if lookup.Plugin == nil {
		t.Fatal("CatalogLookup.Plugin accessor is nil")
	}
	plugin, ok := lookup.Plugin(cloudformationPluginEntity)
	if !ok {
		t.Fatalf("Plugin(%q) not found", cloudformationPluginEntity)
	}
	if plugin.Spec.Plugin != cloudformationArtifactURI {
		t.Fatalf("plugin spec.plugin = %q, want %q", plugin.Spec.Plugin, cloudformationArtifactURI)
	}

	t.Run("CleanResolveThroughEmbedTier", func(t *testing.T) {
		refDigest := embeddedReferenceDigest(t, cloudformationArtifactURI)
		res := pluginpath.Resolver{Pins: map[string]string{cloudformationArtifactURI: refDigest}}
		front := entityFront(lookup, res.Resolve)

		resolved, err := front(cloudformationPluginEntity)
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
		spec := resolver.PluginSpec("cloudformation", resolved)
		if spec.MaxMemoryPages != cloudformationProviderMemPag {
			t.Fatalf("loader spec MaxMemoryPages = %d, want %d (from Plugin entity runtime)",
				spec.MaxMemoryPages, cloudformationProviderMemPag)
		}
	})

	t.Run("WrongPinHaltsWithFetchIntegrity", func(t *testing.T) {
		wrongPin := "sha256:" + strings.Repeat("0", 64)
		res := pluginpath.Resolver{Pins: map[string]string{cloudformationArtifactURI: wrongPin}}
		front := entityFront(lookup, res.Resolve)

		_, err := front(cloudformationPluginEntity)
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
		res := pluginpath.Resolver{Pins: map[string]string{cloudformationArtifactURI: embeddedReferenceDigest(t, cloudformationArtifactURI)}}
		front := entityFront(lookup, res.Resolve)
		resolved, err := front(cloudformationPluginEntity)
		if err != nil {
			t.Fatalf("resolve for smoke load: %v", err)
		}

		// LoadAndDescribe runs the capability cross-check of the entity's authored
		// exports against the guest's describe() self-report, so a clean load also
		// proves the Plugin entity's authored claims (pluginType/abiVersion/
		// exports) are coherent with the embedded guest.
		spec := resolver.PluginSpec("cloudformation", resolved)
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
		// All five exports must be present: render_node drives per-node render,
		// validate_node_input structurally checks a sourced template ahead of render,
		// assemble_boundary drives the per-group merge, and link_publish +
		// link_resolve are the cross-boundary-linking pair.
		for _, name := range []string{"render_node", "validate_node_input", "assemble_boundary", "link_publish", "link_resolve"} {
			if _, ok := findExport(desc, name); !ok {
				t.Fatalf("describe() does not declare the %q export", name)
			}
		}
		// The cross-boundary-linking capability is the plugin-level claim the load
		// gate's coherence check binds to the link_publish/link_resolve pair.
		if !desc.Capabilities.CrossBoundaryLinking {
			t.Fatal("describe() does not declare the cross_boundary_linking capability")
		}
		if desc.Capabilities.BoundaryAssembly != wasm.BoundaryAssemblyMerge {
			t.Fatalf("describe() boundary_assembly = %q, want %q", desc.Capabilities.BoundaryAssembly, wasm.BoundaryAssemblyMerge)
		}
	})
}
