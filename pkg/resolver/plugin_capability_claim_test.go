// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPluginContractClaim proves the single-source entity → neutral-claim
// mapping: authored exports map to the per-export operates/scope claim (selector
// dropped). The host-function surface is machine-derived and stamped, not
// authored, so it never enters the load-time claim.
func TestPluginContractClaim(t *testing.T) {
	t.Parallel()

	t.Run("maps exports", func(t *testing.T) {
		t.Parallel()
		entity := types.Plugin{}
		entity.Spec.Exports = []types.PluginExport{
			{Name: "step", Operates: "patch", Scope: "own", Selector: "dropped"},
			{Name: "render_node", Operates: "emit", Scope: "host"},
		}

		got := PluginContractClaim(entity)
		if got == nil {
			t.Fatal("expected a non-nil claim")
		}
		wantExports := []pluginpath.PluginExportClaim{
			{Name: "step", Operates: "patch", Scope: "own"},
			{Name: "render_node", Operates: "emit", Scope: "host"},
		}
		if !reflect.DeepEqual(got.Exports, wantExports) {
			t.Errorf("exports = %+v; want %+v (selector excluded)", got.Exports, wantExports)
		}
	})

	t.Run("a stamped host-function record does not, on its own, yield a claim", func(t *testing.T) {
		t.Parallel()
		entity := types.Plugin{}
		entity.Spec.Runtime = &types.PluginRuntime{
			HostFunctions: []string{"aggrega_log"},
		}
		if got := PluginContractClaim(entity); got != nil {
			t.Errorf("the machine-derived host-function record is not an authored claim; an entity with no exports should yield nil, got %+v", got)
		}
	})

	t.Run("no authored exports yields nil", func(t *testing.T) {
		t.Parallel()
		if got := PluginContractClaim(types.Plugin{}); got != nil {
			t.Errorf("an entity with no authored exports should yield nil; got %+v", got)
		}
	})
}

// TestWasmContractClaimCarriesEveryCapabilityField pins the single-source
// entity → neutral → wasm capability mapping the load gate and the publish
// cross-check both run (PluginContractClaim then WasmContractClaim). Every
// authored plugin-level capability field must survive both hops; a field dropped
// on either hop would let an authored claim reach the describe() cross-check
// understated — the divergence that once compared an authored reusableInstance:
// true as false and blocked publishing every reusable provider.
func TestWasmContractClaimCarriesEveryCapabilityField(t *testing.T) {
	t.Parallel()

	entity := types.Plugin{}
	entity.Spec.Exports = []types.PluginExport{{Name: "render_node", Operates: "emit", Scope: "own"}}
	entity.Spec.Provider = types.PluginProvider{
		Contract: types.ProviderContract{
			InputKinds: types.PluginInputKinds{
				Sourced:             true,
				Synthesized:         true,
				ArtifactsStandalone: true,
			},
			BoundaryAssembly:       "merge",
			ArtifactDelimiters:     &types.PluginArtifactDelimiters{Left: "[[", Right: "]]"},
			AssemblyNameAnnotation: &types.PluginAssemblyNameAnnotation{Annotation: "aggrega.ai/assembly", RequireUnique: true},
			ReusableInstance:       true,
			CrossBoundaryLinking:   true,
			Omits:                  true,
		},
	}

	got := WasmContractClaim(PluginContractClaim(entity))
	if got == nil || got.Contract == nil {
		t.Fatal("expected a non-nil claim carrying a non-nil contract block")
	}
	want := &wasm.ProviderContract{
		InputKinds:             wasm.InputKinds{Sourced: true, Synthesized: true, ArtifactsStandalone: true},
		BoundaryAssembly:       "merge",
		ArtifactDelimiters:     &wasm.ArtifactDelimiters{Left: "[[", Right: "]]"},
		AssemblyNameAnnotation: &wasm.AssemblyNameAnnotation{Annotation: "aggrega.ai/assembly", RequireUnique: true},
		ReusableInstance:       true,
		CrossBoundaryLinking:   true,
		Omits:                  true,
	}
	if !reflect.DeepEqual(got.Contract, want) {
		t.Errorf("mapped contract = %+v; want %+v", got.Contract, want)
	}
}
