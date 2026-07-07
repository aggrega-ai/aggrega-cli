// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package enginefactory_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
