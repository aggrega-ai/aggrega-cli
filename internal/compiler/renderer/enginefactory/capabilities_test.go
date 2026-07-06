// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
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
// holder shape: the input-kind capabilities plus the output-assembly and
// projection facets the render route reads. The matrix is the contract the
// central gate and the boundary-assembly dispatch enforce — helm renders every
// input kind and assembles nothing; helm-chart is sourced-only and assembles an
// umbrella from external artifacts through its own codec and sub-chart
// projection; compose synthesises and sources, assembles a merged file, and
// contributes boundary consumer env overrides.
func TestHostCapsFor_ProviderMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider                        string
		caps                            renderer.ProviderCapabilities
		assemblesBoundary               bool
		externalArtifacts               bool
		hasCodec                        bool
		hasProjectSource                bool
		hasProjectSubCharts             bool
		hasPathForArtifact              bool
		hasArtifactDelimiters           bool
		hasBoundaryConsumerEnvOverrides bool
	}{
		{
			provider:         "helm",
			caps:             renderer.ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: true},
			hasProjectSource: true,
		},
		{
			provider:              "helm-chart",
			caps:                  renderer.ProviderCapabilities{Sourced: true},
			assemblesBoundary:     true,
			externalArtifacts:     true,
			hasCodec:              true,
			hasProjectSource:      true,
			hasProjectSubCharts:   true,
			hasPathForArtifact:    true,
			hasArtifactDelimiters: true,
		},
		{
			provider:                        "compose",
			caps:                            renderer.ProviderCapabilities{Sourced: true, Synthesized: true},
			assemblesBoundary:               true,
			hasProjectSource:                true,
			hasBoundaryConsumerEnvOverrides: true,
		},
		{
			provider:          "terraform",
			caps:              renderer.ProviderCapabilities{Sourced: true, ArtifactsStandalone: true},
			externalArtifacts: true,
			hasProjectSource:  true,
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
			if h.Caps != tc.caps {
				t.Errorf("Caps = %+v; want %+v", h.Caps, tc.caps)
			}
			if h.AssemblesBoundary != tc.assemblesBoundary {
				t.Errorf("AssemblesBoundary = %v; want %v", h.AssemblesBoundary, tc.assemblesBoundary)
			}
			if h.ExternalArtifacts != tc.externalArtifacts {
				t.Errorf("ExternalArtifacts = %v; want %v", h.ExternalArtifacts, tc.externalArtifacts)
			}
			if (h.Codec != nil) != tc.hasCodec {
				t.Errorf("Codec present = %v; want %v", h.Codec != nil, tc.hasCodec)
			}
			if (h.ProjectSource != nil) != tc.hasProjectSource {
				t.Errorf("ProjectSource present = %v; want %v", h.ProjectSource != nil, tc.hasProjectSource)
			}
			if (h.ProjectSubCharts != nil) != tc.hasProjectSubCharts {
				t.Errorf("ProjectSubCharts present = %v; want %v", h.ProjectSubCharts != nil, tc.hasProjectSubCharts)
			}
			if (h.PathForArtifact != nil) != tc.hasPathForArtifact {
				t.Errorf("PathForArtifact present = %v; want %v", h.PathForArtifact != nil, tc.hasPathForArtifact)
			}
			if (h.ArtifactDelimiters != nil) != tc.hasArtifactDelimiters {
				t.Errorf("ArtifactDelimiters present = %v; want %v", h.ArtifactDelimiters != nil, tc.hasArtifactDelimiters)
			}
			if (h.BoundaryConsumerEnvOverrides != nil) != tc.hasBoundaryConsumerEnvOverrides {
				t.Errorf("BoundaryConsumerEnvOverrides present = %v; want %v", h.BoundaryConsumerEnvOverrides != nil, tc.hasBoundaryConsumerEnvOverrides)
			}
		})
	}
}

// TestCapabilitiesFor_MatchesHostCapsForCaps pins that the input-kind capability
// accessor the early lint detector reads resolves through the same holder the
// render-time gate reads, so the two cannot report divergent input-kind support
// for a provider. An unknown provider reports ok=false from both.
func TestCapabilitiesFor_MatchesHostCapsForCaps(t *testing.T) {
	t.Parallel()
	for _, name := range shippedProviders {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			holder, ok := enginefactory.HostCapsFor(name)
			if !ok {
				t.Fatalf("HostCapsFor(%q): ok=false", name)
			}
			caps, ok := enginefactory.CapabilitiesFor(name)
			if !ok {
				t.Fatalf("CapabilitiesFor(%q): ok=false", name)
			}
			if caps != holder.Caps {
				t.Errorf("CapabilitiesFor=%+v; HostCapsFor.Caps=%+v", caps, holder.Caps)
			}
		})
	}
	if _, ok := enginefactory.CapabilitiesFor("no-such-provider"); ok {
		t.Errorf("CapabilitiesFor(%q): ok=true; an unknown provider must report no capabilities", "no-such-provider")
	}
}
