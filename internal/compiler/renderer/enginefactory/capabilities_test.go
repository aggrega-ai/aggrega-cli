// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// shippedProviders is the set of provider names the Wasm render route resolves to
// a host-capability holder.
var shippedProviders = []string{"helm", "helm-chart", "compose", "terraform"}

// TestHostCapsFor_ResolvesEveryShippedProvider pins the provider-name host-
// capability registry: every shipped provider resolves a non-empty holder, and an
// unknown provider name resolves nothing (ok=false) rather than a defaulted
// holder. This is the single source the Wasm render route reads a provider's
// host-side capabilities from; a provider silently missing here would leave the
// render route unable to resolve its projections and assembly behaviour.
func TestHostCapsFor_ResolvesEveryShippedProvider(t *testing.T) {
	t.Parallel()
	for _, name := range shippedProviders {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, ok := enginefactory.HostCapsFor(name); !ok {
				t.Fatalf("HostCapsFor(%q): ok=false; a shipped provider must resolve a host-capability holder", name)
			}
		})
	}
	if _, ok := enginefactory.HostCapsFor("no-such-provider"); ok {
		t.Errorf("HostCapsFor(%q): ok=true; an unknown provider must resolve no holder", "no-such-provider")
	}
}

// TestHostCapsFor_ProviderMatrix pins each shipped provider's host-capability
// holder shape: the residual host-side facets the render route reads. The input
// kinds, boundary-assembly mode, and artifact-template delimiters are not held
// here — they are the guest's own describe() self-report — and artifact placement
// (format, provenance, path) is the guest's concern, so no artifact facet is
// held. The matrix pins what remains: helm and terraform hold nothing; helm-chart
// holds its codec and sub-chart projection; compose contributes boundary consumer
// env overrides.
func TestHostCapsFor_ProviderMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider                        string
		hasCodec                        bool
		hasProjectSubCharts             bool
		hasBoundaryConsumerEnvOverrides bool
	}{
		{
			provider: "helm",
		},
		{
			provider:            "helm-chart",
			hasCodec:            true,
			hasProjectSubCharts: true,
		},
		{
			provider:                        "compose",
			hasBoundaryConsumerEnvOverrides: true,
		},
		{
			provider: "terraform",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			h, ok := enginefactory.HostCapsFor(tc.provider)
			if !ok {
				t.Fatalf("HostCapsFor(%q): ok=false", tc.provider)
			}
			if (h.Codec != nil) != tc.hasCodec {
				t.Errorf("Codec present = %v; want %v", h.Codec != nil, tc.hasCodec)
			}
			if (h.ProjectSubCharts != nil) != tc.hasProjectSubCharts {
				t.Errorf("ProjectSubCharts present = %v; want %v", h.ProjectSubCharts != nil, tc.hasProjectSubCharts)
			}
			if (h.BoundaryConsumerEnvOverrides != nil) != tc.hasBoundaryConsumerEnvOverrides {
				t.Errorf("BoundaryConsumerEnvOverrides present = %v; want %v", h.BoundaryConsumerEnvOverrides != nil, tc.hasBoundaryConsumerEnvOverrides)
			}
		})
	}
}

// TestCapabilitiesFor_ReadsEntityClaim pins that the input-kind accessor the early
// lint detector reads resolves the provider's serving Plugin entity through the
// given lookup and reports the entity's authored capabilities.input_kinds claim. A
// provider whose serving Plugin does not resolve, or whose Plugin carries no
// capabilities claim, reports ok=false so a capability-based check skips rather
// than flags.
func TestCapabilitiesFor_ReadsEntityClaim(t *testing.T) {
	t.Parallel()
	lookup := func(ref string) (types.Plugin, bool, error) {
		switch ref {
		case "helm-chart":
			return types.Plugin{Spec: types.PluginSpec{
				Capabilities: &types.PluginCapabilities{InputKinds: types.PluginInputKinds{Sourced: true}},
			}}, true, nil
		case "compose":
			return types.Plugin{Spec: types.PluginSpec{
				Capabilities: &types.PluginCapabilities{InputKinds: types.PluginInputKinds{Sourced: true, Synthesized: true}},
			}}, true, nil
		case "no-claim":
			return types.Plugin{Spec: types.PluginSpec{}}, true, nil
		default:
			return types.Plugin{}, false, nil
		}
	}

	caps, ok := enginefactory.CapabilitiesFor(lookup, "helm-chart")
	if !ok {
		t.Fatal(`CapabilitiesFor("helm-chart"): ok=false; want the entity claim`)
	}
	if want := (renderer.ProviderCapabilities{Sourced: true}); caps != want {
		t.Errorf("CapabilitiesFor(helm-chart) = %+v; want %+v", caps, want)
	}

	caps, ok = enginefactory.CapabilitiesFor(lookup, "compose")
	if !ok {
		t.Fatal(`CapabilitiesFor("compose"): ok=false; want the entity claim`)
	}
	if want := (renderer.ProviderCapabilities{Sourced: true, Synthesized: true}); caps != want {
		t.Errorf("CapabilitiesFor(compose) = %+v; want %+v", caps, want)
	}

	if _, ok := enginefactory.CapabilitiesFor(lookup, "no-such-provider"); ok {
		t.Error("CapabilitiesFor(no-such-provider): ok=true; a provider with no serving Plugin must report no capabilities")
	}
	if _, ok := enginefactory.CapabilitiesFor(lookup, "no-claim"); ok {
		t.Error("CapabilitiesFor(no-claim): ok=true; a Plugin with no capabilities claim must report no capabilities")
	}
	if _, ok := enginefactory.CapabilitiesFor(nil, "helm-chart"); ok {
		t.Error("CapabilitiesFor(nil lookup): ok=true; a nil lookup must report no capabilities")
	}
}
