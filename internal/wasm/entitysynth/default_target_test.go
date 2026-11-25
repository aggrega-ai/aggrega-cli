// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package entitysynth_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestImporterDefaultTargetRecordedOnEntity holds the generation of a Plugin
// entity to recording the default target an importer's describe() declares, and
// to writing no key for an importer that declares none. The record is held to the
// load's own comparison: mapped through the claim the load gate builds, it agrees
// with the descriptor it came from.
func TestImporterDefaultTargetRecordedOnEntity(t *testing.T) {
	t.Parallel()
	descriptor := func(target string) wasm.PluginDescriptor {
		d := wasm.PluginDescriptor{
			AbiVersion:   wasm.ABIVersion,
			PluginType:   string(wasm.PluginTypeImporter),
			Capabilities: []string{"import:arm"},
			Exports:      []wasm.PluginExport{{Name: wasm.ImportExport.Name()}},
			Metadata:     &wasm.PluginDescriptorMeta{Version: "1.2.3"},
		}
		if target != "" {
			d.Importer = &wasm.ImporterContract{DefaultTarget: target}
		}
		return d
	}
	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "import-arm"}}

	entity, err := entitysynth.MergeForPublish(descriptor("arm"), "", nil, overlay, "importer/import-arm")
	if err != nil {
		t.Fatalf("MergeForPublish: %v", err)
	}
	if entity.Spec.Importer == nil || entity.Spec.Importer.DefaultTarget != "arm" {
		t.Fatalf("the generated record is %+v; want defaultTarget arm", entity.Spec.Importer)
	}
	claim := resolver.WasmContractClaim(resolver.PluginContractClaim(entity))
	if claim == nil {
		t.Fatal("an entity recording a default target yields no claim")
	}
	if err := wasm.CrossCheckContract("importer/import-arm", *claim, descriptor("arm")); err != nil {
		t.Errorf("the generated record disagrees with its own descriptor: %v", err)
	}

	none, err := entitysynth.MergeForPublish(descriptor(""), "", nil, overlay, "importer/import-arm")
	if err != nil {
		t.Fatalf("MergeForPublish: %v", err)
	}
	if none.Spec.Importer != nil && none.Spec.Importer.DefaultTarget != "" {
		t.Errorf("an importer declaring no default target recorded %q", none.Spec.Importer.DefaultTarget)
	}
}
