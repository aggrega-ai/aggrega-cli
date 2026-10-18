// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestComputeClosureIsBlindToProviderSocket proves ComputeClosure records NO
// provider Plugin entity for a URI-bound plugins.providers binding: the provider
// socket is resolved by a separate pull-time pass, not inside the hermetic,
// config-invariant closure walk. Build / serve reconstruct the provider entity
// from the lockfile, so the entity axis a build computes is invariant to a
// workspace-config edit made after pull.
func TestComputeClosureIsBlindToProviderSocket(t *testing.T) {
	t.Parallel()
	const providerEntity = "provider/helm:1.0.0"

	cfg := workspaceconfig.Default()
	cfg.Plugins.Providers = []workspaceconfig.PluginRef{
		{Capability: "helm", URI: providerEntity},
	}

	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{
			providerEntity: resolutionOK("1.0.0", "sha256:helm", "tier3-registry"),
		},
	}

	c, err := closure.ComputeClosure(context.Background(), &types.Release{}, nil, types.NormalizedAggregate{}, cfg, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if len(c.Entities) != 0 {
		t.Errorf("ComputeClosure recorded %d entities for a provider socket binding; want 0 (config-agnostic): %+v",
			len(c.Entities), c.Entities)
	}
	if len(c.Plugins) != 0 {
		t.Errorf("wasm plugin axis: got %d; want 0", len(c.Plugins))
	}
}

// TestResolveProviderSocketEntities_URIBoundPinnedOnEntityAxis proves the
// pull-time provider-socket pass records a URI-bound plugins.providers entry on
// the catalog-entity axis: the provider Plugin entity lands in c.Entities with
// the plugin kind hint and an authoring-site path naming the providers socket,
// resolved via ResolveCatalogEntity.
func TestResolveProviderSocketEntities_URIBoundPinnedOnEntityAxis(t *testing.T) {
	t.Parallel()
	const providerEntity = "provider/helm:1.0.0"

	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{
			providerEntity: resolutionOK("1.0.0", "sha256:helm", "tier3-registry"),
		},
	}

	c := closure.Closure{}
	refs := []workspaceconfig.PluginRef{{Capability: "helm", URI: providerEntity}}
	if err := closure.ResolveProviderSocketEntities(context.Background(), &c, refs, lookup); err != nil {
		t.Fatalf("ResolveProviderSocketEntities: %v", err)
	}

	var found *closure.ResolvedRef
	for i := range c.Entities {
		if c.Entities[i].URI == providerEntity {
			found = &c.Entities[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("provider Plugin entity %q not recorded on the catalog-entity axis; got %+v",
			providerEntity, c.Entities)
	}
	if len(c.Plugins) != 0 {
		t.Errorf("wasm plugin axis: got %d; want 0 (the provider socket rides the entity axis)", len(c.Plugins))
	}
	if found.KindHint != "plugin" {
		t.Errorf("KindHint: got %q; want %q", found.KindHint, "plugin")
	}
	if found.ResolvedVersion != "1.0.0" || found.Digest != "sha256:helm" {
		t.Errorf("resolution not carried through: got version %q digest %q", found.ResolvedVersion, found.Digest)
	}
	const wantPath = ".aggrega-config.yaml.plugins.providers"
	if !containsString(found.ReferencedFrom, wantPath) {
		t.Errorf("ReferencedFrom: got %v; want to contain %q", found.ReferencedFrom, wantPath)
	}
}

// TestResolveProviderSocketEntities_CapabilityOnlyBindingNotPinned proves a
// capability-only plugins.providers entry (URI == "", the binding that names a
// target but lets the render-engine capability resolve its serving plugin)
// produces no catalog-entity pin and does not halt the pass.
func TestResolveProviderSocketEntities_CapabilityOnlyBindingNotPinned(t *testing.T) {
	t.Parallel()

	// An empty lookup: any attempt to resolve an unexpected URI would surface
	// NotFound and halt with ErrUnresolvableURI.
	lookup := &fakeLookup{entities: map[string]closure.Resolution{}}

	c := closure.Closure{}
	refs := []workspaceconfig.PluginRef{{Capability: "helm"}} // capability-only: no URI
	if err := closure.ResolveProviderSocketEntities(context.Background(), &c, refs, lookup); err != nil {
		t.Fatalf("capability-only binding must not halt; got %v", err)
	}
	if len(c.Entities) != 0 {
		t.Errorf("capability-only binding produced %d entity pins; want 0: %+v", len(c.Entities), c.Entities)
	}
}

// TestResolveProviderSocketEntities_Tier1LocalNotPinned proves a URI-bound
// binding whose entity resolves at Tier-1 (a local catalog plugin) is NOT pinned:
// the build reconstructs the binding from the lockfile and resolves the serving
// plugin through the local catalog walk, so a Tier-1 pin would strand a lockfile
// entry the hermetic build cannot reconstruct. The pass does not halt.
func TestResolveProviderSocketEntities_Tier1LocalNotPinned(t *testing.T) {
	t.Parallel()
	const providerEntity = "provider/helm"

	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{
			providerEntity: resolutionOK("1.0.0", "sha256:helm", "tier-1-catalog"),
		},
	}

	c := closure.Closure{}
	refs := []workspaceconfig.PluginRef{{Capability: "helm", URI: providerEntity}}
	if err := closure.ResolveProviderSocketEntities(context.Background(), &c, refs, lookup); err != nil {
		t.Fatalf("Tier-1 provider must not halt; got %v", err)
	}
	if len(c.Entities) != 0 {
		t.Errorf("Tier-1-local provider produced %d entity pins; want 0 (registry-only): %+v",
			len(c.Entities), c.Entities)
	}
}

// TestResolveProviderSocketEntities_UnresolvableHaltsLoudly proves a URI-bound
// binding whose entity resolves to NotFound halts the pass with ErrUnresolvableURI
// rather than silently dropping the pin.
func TestResolveProviderSocketEntities_UnresolvableHaltsLoudly(t *testing.T) {
	t.Parallel()

	lookup := &fakeLookup{entities: map[string]closure.Resolution{}}

	c := closure.Closure{}
	refs := []workspaceconfig.PluginRef{{Capability: "helm", URI: "provider/helm:1.0.0"}}
	err := closure.ResolveProviderSocketEntities(context.Background(), &c, refs, lookup)
	if !errors.Is(err, closure.ErrUnresolvableURI()) {
		t.Fatalf("want ErrUnresolvableURI; got %v", err)
	}
}

// TestResolveProviderSocketEntities_NonRegressionCleanPaths proves the pull-time
// provider-socket pass introduces no fresh halt on the no-binding,
// capability-only, and mixed capability-only + URI-bound paths, and pins exactly
// the URI-bound entities on the mixed path.
func TestResolveProviderSocketEntities_NonRegressionCleanPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		providers    []workspaceconfig.PluginRef
		wantEntity   string
		wantEntities int
	}{
		{"no-provider-binding", nil, "", 0},
		{"capability-only-binding", []workspaceconfig.PluginRef{{Capability: "helm"}}, "", 0},
		{"mixed-capability-only-and-uri-bound", []workspaceconfig.PluginRef{
			{Capability: "compose"},
			{Capability: "helm", URI: "provider/helm:1.0.0"},
		}, "provider/helm:1.0.0", 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lookup := &fakeLookup{
				entities: map[string]closure.Resolution{
					"provider/helm:1.0.0": resolutionOK("1.0.0", "sha256:helm", "tier3-registry"),
				},
			}
			c := closure.Closure{}
			if err := closure.ResolveProviderSocketEntities(context.Background(), &c, tc.providers, lookup); err != nil {
				t.Fatalf("expected clean resolution; got %v", err)
			}
			if len(c.Entities) != tc.wantEntities {
				t.Fatalf("entity count: got %d; want %d: %+v", len(c.Entities), tc.wantEntities, c.Entities)
			}
			if tc.wantEntity != "" && (len(c.Entities) == 0 || c.Entities[0].URI != tc.wantEntity) {
				t.Errorf("want entity %q pinned; got %+v", tc.wantEntity, c.Entities)
			}
		})
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
