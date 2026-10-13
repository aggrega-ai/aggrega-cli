// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitysynth_test

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestMergeForPublishRecordsTheImportersSignatures holds the generation of a
// Plugin entity to recording the recognition signatures an importer's describe()
// declares, in declared order and field for field, beside the formats it
// advertises — and to recording nothing for an importer that declares none, so
// the generated entity makes no claim the load would then compare.
//
// The record it writes is held to the load's own comparison too: mapped through
// the claim the load gate builds, it agrees with the descriptor it came from.
func TestMergeForPublishRecordsTheImportersSignatures(t *testing.T) {
	t.Parallel()
	detect := []wasm.DetectSignature{
		{Syntax: "json", Files: []string{"*.json"}, Match: []wasm.PathMatch{
			{Path: "data.nodes", Is: "array"},
			{Path: "kind", Is: "string", Prefix: "Fl"},
		}},
		{Syntax: "yaml", Match: []wasm.PathMatch{{Path: "kind", Is: "string", Equals: "Flow"}}},
	}
	descriptor := func(detect []wasm.DetectSignature) wasm.PluginDescriptor {
		d := wasm.PluginDescriptor{
			AbiVersion:   wasm.ABIVersionV1Alpha1,
			PluginType:   string(wasm.PluginTypeImporter),
			Capabilities: []string{"import:langflow"},
			Exports:      []wasm.PluginExport{{Name: wasm.ImportExport.Name()}},
			Metadata:     &wasm.PluginDescriptorMeta{Version: "1.2.3"},
		}
		if detect != nil {
			d.Importer = &wasm.ImporterContract{Detect: detect}
		}
		return d
	}
	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "import-langflow"}}

	entity, err := entitysynth.MergeForPublish(descriptor(detect), "", nil, overlay, "importer/import-langflow")
	if err != nil {
		t.Fatalf("MergeForPublish: %v", err)
	}
	want := []types.PluginDetectSignature{
		{Syntax: "json", Files: []string{"*.json"}, Match: []types.PluginPathMatch{
			{Path: "data.nodes", Is: types.IsArray},
			{Path: "kind", Is: types.IsString, Prefix: "Fl"},
		}},
		{Syntax: "yaml", Match: []types.PluginPathMatch{{Path: "kind", Is: types.IsString, Equals: "Flow"}}},
	}
	if entity.Spec.Importer == nil || !reflect.DeepEqual(entity.Spec.Importer.Detect, want) {
		t.Fatalf("the generated record is %+v; want %+v", entity.Spec.Importer, want)
	}
	if got := entity.DeclaredCapabilities(); !reflect.DeepEqual(got, []string{"import:langflow"}) {
		t.Errorf("the generated entity advertises %v; want the descriptor's formats beside the record", got)
	}
	claim := resolver.WasmContractClaim(resolver.PluginContractClaim(entity))
	if claim == nil || claim.Detect == nil {
		t.Fatal("the load gate's claim for the generated entity carries no recognition record")
	}
	if err := wasm.CrossCheckContract("importer/import-langflow:1.2.3", *claim, descriptor(detect)); err != nil {
		t.Errorf("the generated record disagrees with the descriptor it came from: %v", err)
	}

	bare, err := entitysynth.MergeForPublish(descriptor(nil), "", nil, overlay, "importer/import-langflow")
	if err != nil {
		t.Fatalf("MergeForPublish: %v", err)
	}
	if bare.Spec.Importer == nil || bare.Spec.Importer.Detect != nil {
		t.Errorf("an importer declaring no signature generated the record %+v; want none", bare.Spec.Importer)
	}
}
