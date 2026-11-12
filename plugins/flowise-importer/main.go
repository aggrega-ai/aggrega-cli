// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command flowise-importer is the Flowise importer Wasm guest. It reads Flowise
// flow JSON — chatflows and agentflows — and drafts an Aggregate. By default the
// draft is the Flowise platform, core/ai-agent/flowise, carrying the flow as
// written with its credential values emptied, and each node of the flow the
// workspace's catalog matches as a model, a tool or a store drafted as a
// Component of its own, wired to the platform's socket that takes it; with the
// layout setting nodes, every node of the flow is drafted as a Component,
// referencing a Component of the catalog where it matches one with high
// confidence and drafting a new one where none does.
//
// The guest holds only what is particular to Flowise: this descriptor and the
// export wiring, the walker lifting a flow's nodes and edges and locating its
// credential values (walk.go), how the flow reaches each part wired to the
// platform (wire.go), and the format's mapping arm, syntactic format and
// platform (format.go). The rest — the
// confidence scorer, the decisions, the drafts, the renderers and the settings —
// is the plugin kit's importer package,
// github.com/aggrega-ai/aggrega-pdk-go/pkg/importer, which every importer built on
// the kit shares. All of it runs in this guest: the host mediates the pipeline and
// stays agnostic to what a node means. The scorer weighs each node against the
// Components of the workspace's own catalog, which the host hands the guest on its
// input as a view of their names, versions, labels, key names and sockets; the
// mapping arm is carried in the guest, and operators extend or override it through
// the import attachment's settings.
//
// It obtains each source's structured tree through the host rather than parsing
// the source itself: the import export hands the source bytes to the
// aggrega_parse_source host function for the JSON syntactic format and walks the
// canonical tree the host returns. So it declares the aggrega_parse_source host
// function on its import export, and the load-time cross-check of the descriptor's
// declared host functions against the binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit) rather than hand-declared wire types, so the wire shape stays single-sourced
// from the schema. The importer socket carries canonical CBOR in both directions:
// the input is read with DecodeCBOR and the output is written with EncodeCBOR.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o flowise.wasm ./plugins/flowise-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// descriptor is the static describe() self-report: an importer plugin
// serving the flowise agent-flow format, whose import export imports the
// aggrega_parse_source host function it uses to obtain each source's tree, and
// the signature a Flowise source is recognised by (format.go). The host reads
// this once at load, validates it, and cross-checks the declared host functions
// against the binary's imports and the signatures against the ones the
// importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "importer",
		Description:  "Import Flowise agent-flow JSON into a draft Aggregate: by default the Flowise platform carrying the flow, with the flow's models, tools and stores that match the workspace's catalog wired to it as Components; with the layout setting nodes, every node drafted as a Component of its own.",
		Capabilities: []string{"import:" + flowise.Name},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

//go:wasmexport describe
func describe() int32 {
	out, err := aggrega.MarshalDescriptor(descriptor())
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(out)
	return 0
}

// importArtifact is the import export. The Go function is named differently from
// the export because "import" is a Go keyword; the //go:wasmexport directive
// carries the export name the host dispatches.
//
//go:wasmexport import
func importArtifact() int32 {
	var in aggrega.ImportInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		pdk.SetError(fmt.Errorf("flowise importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a source tree: it asks the
	// host to run a parser for the syntactic format and hands back the canonical
	// tree. The guest never parses the source with a grammar of its own.
	out := importer.Import(in, flowise, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("flowise importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
