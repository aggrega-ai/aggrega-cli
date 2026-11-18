// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command arm-schema-importer is the Azure Resource Manager importer Wasm guest. It reads a Bicep types.json file — the type definitions of one Azure
// resource provider namespace at one API version — and drafts a Component and a
// Recipe for each resource type the request names.
//
// The guest holds what the host reads off it: this descriptor, the signatures a
// source is recognised by, and the export wiring. What it does with a source is
// the schemadraft package (plugins/internal/schemadraft) and the format's
// package beneath it, which build on the plugin kit's importer package,
// github.com/aggrega-ai/aggrega-pdk/go/pkg/importer, for the settings, the
// credential decision and the drafts' shared conventions.
//
// It obtains each source's tree through the host rather than parsing the source
// with a grammar of its own: the import export hands each source to the
// aggrega_parse_source host function for the JSON syntactic format. So it
// declares the aggrega_parse_source host function on its import export, and the
// load-time cross-check of the descriptor's declared host functions against the
// binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi/go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega.Version=<version>" -o arm-schema.wasm ./plugins/arm-schema-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/arm"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// descriptor is the static describe() self-report: an importer plugin serving
// the arm-schema format, whose import export imports the aggrega_parse_source host
// function it uses to obtain each source's tree, and the signatures a source is
// recognised by. The host reads this once at load, validates it, and
// cross-checks the declared host functions against the binary's imports and the
// signatures against the ones the importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import the resource types of a Bicep types.json file (from Azure/bicep-types-az, one resource provider " +
			"namespace at one API version) into a draft Component and a draft Recipe for each type the settings' " +
			"resource_types names, targeting the arm provider.",
		Capabilities: []string{"import:" + arm.Format.Name},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a Bicep types file is recognised when an import names no format,
// declared in describe() and recorded on the importer's Plugin entity: a JSON
// file named types.json whose first entry is a type, by $type at index 0 as a
// string. The host evaluates the signature without knowing what Azure Resource
// Manager is.
var detect = []aggrega.DetectSignature{
	{Syntax: "json", Files: []string{"types.json"},
		Match: []aggrega.PathMatch{{Path: "0.$type", Is: "string"}}},
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
		pdk.SetError(fmt.Errorf("arm-schema importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not of that format with the parser's reason.
	out := schemadraft.Import(in, arm.Format, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("arm-schema importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
