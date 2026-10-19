// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command dify-importer is the Dify importer Wasm guest. It reads an application
// Dify exports in its DSL and drafts an Aggregate composing the standard
// library's Dify installation, core/dify, carrying the application in the
// installation's app_definition parameter, with every model, tool, knowledge
// base and plugin the application names reported as configured inside Dify.
//
// The guest holds what the host reads off it: this descriptor, the signature a
// Dify DSL is recognised by, and the export wiring. What it does with a source
// is the difyimport package (plugins/internal/difyimport), which builds on the
// plugin kit's importer package, github.com/aggrega-ai/aggrega-pdk-go/pkg/importer,
// for the draft, the settings, the credential decision and the drafts' shared
// conventions.
//
// It obtains each source's tree through the host rather than parsing the source
// with a grammar of its own: the import export hands each DSL to the
// aggrega_parse_source host function for the YAML syntactic format. So it
// declares the aggrega_parse_source host function on its import export, and the
// load-time cross-check of the descriptor's declared host functions against the
// binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o dify.wasm ./plugins/dify-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/difyimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the static describe() self-report: a v1alpha1 importer plugin
// serving the dify format, whose import export imports the aggrega_parse_source
// host function it uses to obtain each source's tree, and the signature a Dify
// DSL is recognised by. The host reads this once at load, validates it, and
// cross-checks the declared host functions against the binary's imports and the
// signature against the one the importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import a Dify application, in the YAML DSL Dify exports, into a draft Aggregate composing the " +
			"Dify installation " + difyimport.Module + " with the application carried in its " +
			difyimport.DefinitionParameter + " parameter; the models, tools, knowledge bases and plugins the " +
			"application names are reported as configured inside Dify.",
		Capabilities: []string{"import:" + difyimport.Format},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a Dify DSL is recognised when an import names no format,
// declared in describe() and recorded on the importer's Plugin entity: a source
// read as YAML whose kind is app and which holds the application's mode at
// app.mode and the DSL's version at version, as every DSL Dify exports does. A
// DSL is exported under a name of its author's choosing, so the signature names
// no file. The host evaluates the signature without knowing what Dify is.
var detect = []aggrega.DetectSignature{{
	Syntax: difyimport.Syntax,
	Match: []aggrega.PathMatch{
		{Path: "kind", Is: "string", Equals: "app"},
		{Path: "app.mode", Is: "present"},
		{Path: "version", Is: "present"},
	},
}}

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
		pdk.SetError(fmt.Errorf("dify importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not YAML with the parser's reason.
	out := difyimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("dify importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
