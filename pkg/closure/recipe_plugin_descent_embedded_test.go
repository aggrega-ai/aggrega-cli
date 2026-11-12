// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The plugin the compiler binary carries, and the one a registry publishes — the
// contrast every test below is built on. registryOnlyProviderEntity is a real
// provider identity the binary does not carry, so it stands for the case that must
// keep its pin.
const (
	binaryPluginName        = "shipped-in-binary"
	binaryPluginArtifact    = "provider/" + binaryPluginName
	binaryPluginEntityURI   = binaryPluginArtifact + ":1.0.0"
	registryOnlyProviderURI = "provider/render-custom:1.0.0"
)

// The output-shaping plugin the compiler binary carries: a governance guest bound at
// a transform role. Its .wasm resolves from the binary exactly as a core provider's
// does, so it is the case that separates "resolved from the binary" from "needs no
// pin" — it shapes the rendered output bytes, and the lockfile pin is what holds
// those bytes still until the next pull.
const (
	binaryMutatorName      = "shipped-mutator"
	binaryMutatorArtifact  = "governance/" + binaryMutatorName
	binaryMutatorEntityURI = binaryMutatorArtifact + ":1.0.0"
)

// withBinaryResolvedPlugin adds a plugin the binary carries — entity and artifact
// both — to the shared descent fixture's lookup.
func withBinaryResolvedPlugin(l *fakeLookup) *fakeLookup {
	l.entities[binaryPluginEntityURI] = resolutionOK("1.0.0", "sha256:binary-entity", closure.ResolutionPathEmbedded)
	l.plugins[binaryPluginArtifact] = resolutionOK("1.0.0", "sha256:binary-artifact", closure.ResolutionPathEmbedded)
	return l
}

// TestPluginDescent_BinaryResolvedNotPinned proves a core PROVIDER resolved from the
// compiler binary is recorded on NEITHER axis: it is not a pulled artifact, and the
// build selects its rendering engine from the same binary, so the pull records nothing
// for it. Pinning it would write a digest the pull never fetched, and one that moves
// with whichever binary produced the closure.
//
// The decline is provider-scoped, which is what
// TestPluginDescent_BinaryResolvedOutputShapingPluginStillPinned holds the other side
// of: a governance guest whose .wasm comes out of the same binary IS pinned.
func TestPluginDescent_BinaryResolvedNotPinned(t *testing.T) {
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, binaryPluginEntityURI),
		},
		plugins: map[string]*types.Plugin{
			binaryPluginEntityURI: pluginEntity(t, binaryPluginName, "provider"),
		},
	}
	cl, err := runPluginDescent(t, withBinaryResolvedPlugin(pluginDescentLookup()), loader)
	if err != nil {
		t.Fatalf("plugin descent: %v", err)
	}
	if ref, ok := entityByURI(cl, binaryPluginEntityURI); ok {
		t.Errorf("plugin entity %q resolved from the binary must not be recorded; got %+v", binaryPluginEntityURI, ref)
	}
	if ref, ok := pluginByURI(cl, binaryPluginArtifact); ok {
		t.Errorf("plugin artifact %q resolved from the binary must not be recorded; got %+v", binaryPluginArtifact, ref)
	}
}

// TestPluginDescent_BinaryResolvedOutputShapingPluginStillPinned is the assertion whose
// absence let an over-broad decline-to-pin ship: an output-shaping plugin whose .wasm
// resolves from the compiler binary is STILL recorded on the plugin axis. It shapes the
// rendered output bytes, so the lockfile pin is what keeps a build reproducible — an
// unpinned one is adopted the moment it changes rather than at the next pull.
//
// The provider that also resolves from the binary is asserted absent in the SAME
// descent, so the test cannot pass by pinning everything, and the registry-published
// plugin's presence means it cannot pass by declining everything either. All three
// verdicts come out of one call.
func TestPluginDescent_BinaryResolvedOutputShapingPluginStillPinned(t *testing.T) {
	lookup := withBinaryResolvedPlugin(pluginDescentLookup())
	// The mutator entity is authored in a local catalog — the real shape, where the
	// workspace supplies the entity and the binary supplies only the .wasm. An entity
	// authored there has its .wasm resolved from the machine's own sources rather
	// than through the lookup, so the binary's copy is what that question answers.
	lookup.entities[binaryMutatorEntityURI] = resolutionOK("1.0.0", "sha256:mutator-entity", "tier-1-catalog")
	local := func(_, artifactRef string) (closure.Resolution, error) {
		if artifactRef != binaryMutatorArtifact {
			t.Errorf("the machine's own sources were asked about %q; only the catalog-authored mutator's artifact belongs there", artifactRef)
		}
		return resolutionOK("1.0.0", "sha256:mutator-artifact", closure.ResolutionPathEmbedded), nil
	}

	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, registryPluginEntity),
		},
		plugins: map[string]*types.Plugin{
			binaryMutatorEntityURI: pluginEntity(t, binaryMutatorName, string(plugintype.Governance)),
			binaryPluginEntityURI:  pluginEntity(t, binaryPluginName, string(plugintype.Provider)),
			registryPluginEntity:   pluginEntity(t, registryPluginName, "pipeline-step"),
		},
	}

	rel := loadRelease(t, providerDescentRelease)
	man := loadManifest(t, providerDescentManifest)
	cfg := workspaceconfig.Config{}
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{
		{URI: binaryMutatorEntityURI, Role: plugintype.RoleLabelTransform},
	}
	// A provider the binary carries, bound at the provider socket in the same run, is
	// the negative half of the contrast.
	cfg.Plugins.Providers = []workspaceconfig.PluginRef{
		{Capability: "in-binary", URI: binaryPluginEntityURI},
	}
	cl, err := closure.ComputeClosure(context.Background(), rel, man, types.NormalizedAggregate{}, cfg, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if err := closure.ResolveProviderSocketEntities(context.Background(), &cl, cfg.Plugins.Providers, lookup); err != nil {
		t.Fatalf("provider socket pass: %v", err)
	}
	if err := descendPluginsWith(&cl, loader, lookup, nil, local); err != nil {
		t.Fatalf("plugin descent: %v", err)
	}

	art, ok := pluginByURI(cl, binaryMutatorArtifact)
	if !ok {
		t.Fatalf("output-shaping plugin artifact %q resolved from the binary must still be pinned; plugins=%+v",
			binaryMutatorArtifact, cl.Plugins)
	}
	if art.Digest != "sha256:mutator-artifact" || art.ResolutionPath != closure.ResolutionPathEmbedded {
		t.Errorf("artifact pin = {%q, %q}; want the digest and path the binary reported",
			art.Digest, art.ResolutionPath)
	}
	if _, ok := pluginByURI(cl, binaryPluginArtifact); ok {
		t.Errorf("core provider artifact %q resolved from the binary must NOT be pinned; plugins=%+v",
			binaryPluginArtifact, cl.Plugins)
	}
	if _, ok := entityByURI(cl, binaryPluginEntityURI); ok {
		t.Errorf("core provider entity %q resolved from the binary must NOT be pinned; entities=%+v",
			binaryPluginEntityURI, cl.Entities)
	}
	if _, ok := pluginByURI(cl, registryPluginArtifact); !ok {
		t.Errorf("registry-published plugin artifact %q must still be pinned; plugins=%+v",
			registryPluginArtifact, cl.Plugins)
	}
}

// TestPluginDescent_RegistryPluginStillPinnedAlongsideBinaryOne is the guard proven
// by contrast rather than by absence: the same descent that declines the
// binary-resolved plugin still records the registry-published one, on both axes. A
// skip that had swallowed everything would pass the test above and fail this one.
func TestPluginDescent_RegistryPluginStillPinnedAlongsideBinaryOne(t *testing.T) {
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, registryPluginEntity),
		},
		plugins: map[string]*types.Plugin{
			registryPluginEntity: pluginEntity(t, registryPluginName, "pipeline-step"),
		},
	}
	cl, err := runPluginDescent(t, withBinaryResolvedPlugin(pluginDescentLookup()), loader)
	if err != nil {
		t.Fatalf("plugin descent: %v", err)
	}
	ent, ok := entityByURI(cl, registryPluginEntity)
	if !ok {
		t.Fatalf("registry-published plugin entity %q must still be recorded; entities=%+v", registryPluginEntity, cl.Entities)
	}
	if ent.Digest != "sha256:plugin-entity" {
		t.Errorf("entity Digest: got %q; want the resolved registry digest", ent.Digest)
	}
	if _, ok := pluginByURI(cl, registryPluginArtifact); !ok {
		t.Fatalf("registry-published plugin artifact %q must still be recorded; plugins=%+v", registryPluginArtifact, cl.Plugins)
	}
}

// TestPluginDescent_NearMissResolutionPathStillPinned proves the decline-to-pin test
// is an exact match on the resolution path, in BOTH directions of near miss: a
// prefix-truncation of a declining path, and an extension of one. A comparison
// written as "starts with" would wrongly accept one of the two and silently drop a
// pin the closure needs; an equality test rejects both, so the entity is recorded.
//
// The needles are truncations and extensions of REAL values rather than invented
// strings, because an invented string is rejected by any comparison at all and
// therefore measures nothing.
func TestPluginDescent_NearMissResolutionPathStillPinned(t *testing.T) {
	const embedded = closure.ResolutionPathEmbedded
	nearMisses := map[string]string{
		"truncated-binary-path":    embedded[:len(embedded)-3],
		"extended-binary-path":     embedded + "ded",
		"truncated-local-path":     "tier-1-catalo",
		"extended-local-path":      "tier-1-catalog-mirror",
		"binary-path-as-substring": "not-" + embedded,
	}
	for name, path := range nearMisses {
		name, path := name, path
		t.Run(name, func(t *testing.T) {
			lookup := pluginDescentLookup()
			lookup.entities[binaryPluginEntityURI] = resolutionOK("1.0.0", "sha256:near-miss-entity", path)
			lookup.plugins[binaryPluginArtifact] = resolutionOK("1.0.0", "sha256:near-miss-artifact", path)
			loader := &fakeRecipeLoader{
				recipes: map[string]*types.Recipe{
					recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, binaryPluginEntityURI),
				},
				plugins: map[string]*types.Plugin{
					binaryPluginEntityURI: pluginEntity(t, binaryPluginName, "provider"),
				},
			}
			cl, err := runPluginDescent(t, lookup, loader)
			if err != nil {
				t.Fatalf("plugin descent: %v", err)
			}
			if _, ok := entityByURI(cl, binaryPluginEntityURI); !ok {
				t.Errorf("resolution path %q is not a declining path, so entity %q must be recorded; entities=%+v",
					path, binaryPluginEntityURI, cl.Entities)
			}
			if _, ok := pluginByURI(cl, binaryPluginArtifact); !ok {
				t.Errorf("resolution path %q is not a declining path, so artifact %q must be recorded; plugins=%+v",
					path, binaryPluginArtifact, cl.Plugins)
			}
		})
	}
}

// TestPluginDescent_NearMissPluginTypeStillPinned proves the provider test guarding the
// from-the-binary decline is an exact match on the declared plugin type, in BOTH
// directions of near miss: a prefix-truncation of the real type, an extension of it, and
// it as a substring. A comparison written as "starts with" or "contains" would wrongly
// accept one of those and silently drop a pin the closure needs.
//
// The needles are truncations and extensions of the REAL type string rather than
// invented words, because a truncated real value is precisely what a sloppy comparison
// accepts while an obviously invented one is rejected by anything. The exact type is
// carried in the same table as the positive control, so a comparison that accepted
// nothing would fail that row instead of passing the whole table.
func TestPluginDescent_NearMissPluginTypeStillPinned(t *testing.T) {
	const real = string(plugintype.Provider)
	cases := map[string]struct {
		declaredType string
		wantPinned   bool
	}{
		"exact-type-declines":         {real, false},
		"truncated-type":              {real[:len(real)-2], true},
		"extended-type":               {real + "s", true},
		"type-as-substring":           {"not-" + real, true},
		"an-output-shaping-type-pins": {string(plugintype.Governance), true},
	}
	for name, tc := range cases {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			const pluginName = "engine"
			artifactURI := tc.declaredType + "/" + pluginName
			entityURI := artifactURI + ":1.0.0"

			lookup := pluginDescentLookup()
			lookup.entities[entityURI] = resolutionOK("1.0.0", "sha256:near-miss-type-entity", "tier-3-pull")
			lookup.plugins[artifactURI] = resolutionOK("1.0.0", "sha256:near-miss-type-artifact", closure.ResolutionPathEmbedded)
			loader := &fakeRecipeLoader{
				recipes: map[string]*types.Recipe{
					recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, entityURI),
				},
				plugins: map[string]*types.Plugin{
					entityURI: pluginEntity(t, pluginName, tc.declaredType),
				},
			}
			cl, err := runPluginDescent(t, lookup, loader)
			if err != nil {
				t.Fatalf("plugin descent: %v", err)
			}
			_, pinned := pluginByURI(cl, artifactURI)
			if pinned != tc.wantPinned {
				t.Errorf("declared plugin type %q resolved from the binary: artifact pinned = %v; want %v; plugins=%+v",
					tc.declaredType, pinned, tc.wantPinned, cl.Plugins)
			}
		})
	}
}

// TestPluginDescent_BinaryResolvedDeterministic descends the same closure twice with
// a binary-resolved plugin present and asserts both recorded axes are identical: the
// declining branch must not make the recorded set depend on descent order.
func TestPluginDescent_BinaryResolvedDeterministic(t *testing.T) {
	build := func() closure.Closure {
		loader := &fakeRecipeLoader{
			recipes: map[string]*types.Recipe{
				recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, binaryPluginEntityURI),
			},
			plugins: map[string]*types.Plugin{
				binaryPluginEntityURI: pluginEntity(t, binaryPluginName, "provider"),
				registryPluginEntity:  pluginEntity(t, registryPluginName, "pipeline-step"),
			},
		}
		cl, err := runPluginDescent(t, withBinaryResolvedPlugin(pluginDescentLookup()), loader)
		if err != nil {
			t.Fatalf("plugin descent: %v", err)
		}
		return cl
	}
	first, second := build(), build()
	if !reflect.DeepEqual(first.Entities, second.Entities) {
		t.Errorf("entity axis is not byte-identical across runs:\n first=%#v\nsecond=%#v", first.Entities, second.Entities)
	}
	if !reflect.DeepEqual(first.Plugins, second.Plugins) {
		t.Errorf("plugin axis is not byte-identical across runs:\n first=%#v\nsecond=%#v", first.Plugins, second.Plugins)
	}
}

// TestResolveProviderSocketEntities_BinaryResolvedNotPinnedAndDoesNotHalt proves the
// workspace provider socket accepts a provider the binary carries — it does not halt
// as it would on an unresolvable reference — and records no pin for it, while a
// registry-published provider bound in the same pass is still recorded. Both
// assertions run over one call so the skip cannot pass by swallowing everything.
func TestResolveProviderSocketEntities_BinaryResolvedNotPinnedAndDoesNotHalt(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{
			binaryPluginEntityURI:   resolutionOK("1.0.0", "sha256:binary-entity", closure.ResolutionPathEmbedded),
			registryOnlyProviderURI: resolutionOK("1.0.0", "sha256:render-custom", "tier-3-pull"),
		},
	}

	c := closure.Closure{}
	refs := []workspaceconfig.PluginRef{
		{Capability: "in-binary", URI: binaryPluginEntityURI},
		{Capability: "custom", URI: registryOnlyProviderURI},
	}
	if err := closure.ResolveProviderSocketEntities(context.Background(), &c, refs, lookup); err != nil {
		t.Fatalf("a provider the binary carries must not halt the socket pass; got %v", err)
	}

	var sawBinary, sawRegistry bool
	for _, e := range c.Entities {
		switch e.URI {
		case binaryPluginEntityURI:
			sawBinary = true
		case registryOnlyProviderURI:
			sawRegistry = true
		}
	}
	if sawBinary {
		t.Errorf("provider %q resolved from the binary must not be recorded; entities=%+v", binaryPluginEntityURI, c.Entities)
	}
	if !sawRegistry {
		t.Errorf("registry-published provider %q must still be recorded; entities=%+v", registryOnlyProviderURI, c.Entities)
	}
}
