// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
)

// dispatchableProviders is the set of provider names Dispatch routes to a real
// engine. Kept in lockstep with enginefactory.Dispatch.
var dispatchableProviders = []string{"helm", "helm-chart", "compose"}

// TestCapabilities_SynthesizedMatchesWorkloadSynthesizer pins the synthesis
// consistency invariant: an engine self-reports Capabilities().Synthesized if
// and only if it implements renderer.WorkloadSynthesizer. The central input-kind
// gate trusts Capabilities() to decide whether a workload: Recipe may proceed to
// Phase B.4; Phase B.4 then type-asserts WorkloadSynthesizer. If those two
// disagree, a build either gates open and panics at B.4 (claims Synthesized,
// lacks the method) or gates shut on a capable engine (implements the method,
// reports false). This test makes the two sides provably co-vary.
func TestCapabilities_SynthesizedMatchesWorkloadSynthesizer(t *testing.T) {
	t.Parallel()
	for _, name := range dispatchableProviders {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			engine, err := enginefactory.Dispatch(name)
			if err != nil {
				t.Fatalf("Dispatch(%q): %v", name, err)
			}
			reports := engine.Capabilities().Synthesized
			_, implements := engine.(renderer.WorkloadSynthesizer)
			if reports != implements {
				t.Errorf("provider %q: Capabilities().Synthesized=%v but implements WorkloadSynthesizer=%v; the two must agree", name, reports, implements)
			}
		})
	}
}

// TestCapabilities_ProviderMatrix pins each shipped provider's declared
// input-kind support. The matrix is the contract the central gate enforces:
// helm supports every kind; helm-chart is sourced-only (it wraps an external
// chart, so it neither synthesises a workload nor stands up artifacts on its
// own); compose synthesises and sources but does not stand up artifacts alone.
func TestCapabilities_ProviderMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		want     renderer.ProviderCapabilities
	}{
		{"helm", renderer.ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: true}},
		{"helm-chart", renderer.ProviderCapabilities{Sourced: true, Synthesized: false, ArtifactsStandalone: false}},
		{"compose", renderer.ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: false}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			engine, err := enginefactory.Dispatch(tc.provider)
			if err != nil {
				t.Fatalf("Dispatch(%q): %v", tc.provider, err)
			}
			if got := engine.Capabilities(); got != tc.want {
				t.Errorf("provider %q capabilities = %+v; want %+v", tc.provider, got, tc.want)
			}
		})
	}
}
