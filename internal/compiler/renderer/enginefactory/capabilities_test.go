// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package enginefactory_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestContractFor_ReadsEntityClaim pins that the input-kind accessor the early
// lint detector reads resolves the provider's serving Plugin entity through the
// given lookup and reports the entity's authored capabilities.input_kinds claim. A
// provider whose serving Plugin does not resolve, or whose Plugin carries no
// capabilities claim, reports ok=false so a capability-based check skips rather
// than flags.
func TestContractFor_ReadsEntityClaim(t *testing.T) {
	t.Parallel()
	lookup := func(ref string) (types.Plugin, bool, error) {
		switch ref {
		case "helm-chart":
			return types.Plugin{Spec: types.PluginSpec{
				Provider: types.PluginProvider{Contract: types.ProviderContract{InputKinds: types.PluginInputKinds{Sourced: true}}},
			}}, true, nil
		case "compose":
			return types.Plugin{Spec: types.PluginSpec{
				Provider: types.PluginProvider{Contract: types.ProviderContract{InputKinds: types.PluginInputKinds{Sourced: true, Synthesized: true}}},
			}}, true, nil
		case "no-claim":
			return types.Plugin{Spec: types.PluginSpec{}}, true, nil
		default:
			return types.Plugin{}, false, nil
		}
	}

	caps, ok := enginefactory.ContractFor(lookup, "helm-chart")
	if !ok {
		t.Fatal(`ContractFor("helm-chart"): ok=false; want the entity claim`)
	}
	if want := (renderer.ProviderContract{Sourced: true}); caps != want {
		t.Errorf("ContractFor(helm-chart) = %+v; want %+v", caps, want)
	}

	caps, ok = enginefactory.ContractFor(lookup, "compose")
	if !ok {
		t.Fatal(`ContractFor("compose"): ok=false; want the entity claim`)
	}
	if want := (renderer.ProviderContract{Sourced: true, Synthesized: true}); caps != want {
		t.Errorf("ContractFor(compose) = %+v; want %+v", caps, want)
	}

	if _, ok := enginefactory.ContractFor(lookup, "no-such-provider"); ok {
		t.Error("ContractFor(no-such-provider): ok=true; a provider with no serving Plugin must report no capabilities")
	}
	if _, ok := enginefactory.ContractFor(lookup, "no-claim"); ok {
		t.Error("ContractFor(no-claim): ok=true; a Plugin with no capabilities claim must report no capabilities")
	}
	if _, ok := enginefactory.ContractFor(nil, "helm-chart"); ok {
		t.Error("ContractFor(nil lookup): ok=true; a nil lookup must report no capabilities")
	}
}
