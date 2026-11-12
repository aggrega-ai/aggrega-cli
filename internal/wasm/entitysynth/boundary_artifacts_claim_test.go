// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package entitysynth_test

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAContractClaimingOnlyBoundaryArtifactsIsNotAnAbsentContract holds both
// answers to "does this provider author a contract" to the shared-files claim —
// the describe() side's, which lists the fields it reads, and the entity side's,
// which compares against the zero value — and reads the claim back off the
// generated entity, on the value and on its serialized form under the key an
// author writes, spec.provider.contract.boundaryArtifacts.
func TestAContractClaimingOnlyBoundaryArtifactsIsNotAnAbsentContract(t *testing.T) {
	if entitysynth.IsZeroContract(wasm.ProviderContract{BoundaryArtifacts: true}) {
		t.Error("a describe() contract claiming only boundary artifacts reads as absent")
	}
	if (types.ProviderContract{BoundaryArtifacts: true}).IsZero() {
		t.Error("an entity contract claiming only boundary artifacts reads as absent")
	}

	desc := wasm.PluginDescriptor{
		AbiVersion: wasm.ABIVersion,
		PluginType: string(wasm.PluginTypeProvider),
		Exports:    []wasm.PluginExport{{Name: "render_node"}},
		Contract:   &wasm.ProviderContract{BoundaryArtifacts: true},
		Metadata:   &wasm.PluginDescriptorMeta{Version: "1.0.0"},
	}
	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "shared-files-only"}}
	entity, err := entitysynth.MergeForPublish(desc, "", nil, overlay, "provider/shared-files-only:1.0.0")
	if err != nil {
		t.Fatalf("generate the entity: %v", err)
	}
	if !entity.Spec.Provider.Contract.BoundaryArtifacts {
		t.Fatalf("the generated entity dropped a contract whose only claim is boundary artifacts: %+v", entity.Spec.Provider.Contract)
	}
	raw, err := json.Marshal(entity.Spec.Provider.Contract)
	if err != nil {
		t.Fatalf("marshal the generated contract: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("read the generated contract back: %v", err)
	}
	if fields["boundaryArtifacts"] != true {
		t.Errorf("the serialized entity contract does not carry boundaryArtifacts: true: %s", raw)
	}
}
