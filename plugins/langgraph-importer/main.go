// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command langgraph-importer is the LangGraph importer Wasm guest. It reads the
// graphs a LangGraph application declares in its configuration, langgraph.json,
// and drafts an Aggregate holding one instance of core/ai-agent/langgraph per
// graph, wired to the datastore its checkpointer keeps state in, and a Release
// beside it carrying the environment the configuration states. The application's
// code is never read, and of an environment file the configuration names only
// the variables' names are, where the importer's with.sources hands it.
//
// The guest holds what the host reads off it: this descriptor, the signatures a
// configuration is recognised by, and the export wiring. What it does with a source is the langgraphimport package
// (plugins/internal/langgraphimport), which builds on the plugin kit's importer
// package, github.com/aggrega-ai/aggrega-pdk-go/pkg/importer, for the settings,
// the credential decision and the drafts' shared conventions.
//
// It obtains a configuration's tree through the host rather than parsing it with
// a grammar of its own: the import export hands each configuration to the
// aggrega_parse_source host function, for the JSON syntactic format. So it
// declares the aggrega_parse_source host function on its import export, and the
// load-time cross-check of the descriptor's declared host functions against the
// binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o langgraph.wasm ./plugins/langgraph-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/langgraphimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the static describe() self-report: an importer plugin
// serving the langgraph format, whose import export imports the
// aggrega_parse_source host function it uses to obtain each configuration's
// tree, and the signatures a configuration is recognised by. The host reads this once at load, validates it, and
// cross-checks the declared host functions against the binary's imports and the
// signatures against the ones the importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import the graphs a LangGraph project declares in " + langgraphimport.ConfigFile +
			" into a draft Aggregate holding one " + langgraphimport.Component + " instance per graph, wired to the " +
			"datastore its checkpointer keeps state in, and a draft Release carrying the environment the " +
			"configuration states; the project's code is not read, nor any value of an environment file.",
		Capabilities: []string{"import:" + langgraphimport.Format},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a LangGraph configuration is recognised when an import names no
// format, declared in describe() and recorded on the importer's Plugin entity.
// A configuration is read as JSON with an object at graphs: under its default
// name, langgraph.json, or, under any name, where its $schema names the
// configuration's schema. The patterns are also what selects the files of a
// directory the importer is handed, so a directory hands it langgraph.json and no
// other file: the application's code is never handed, and neither is an
// environment file. No pattern names an environment file, since recognition
// reads every file any importer's patterns select, so one naming *.env would
// have every directory import of every format open each environment file it
// holds; an environment file reaches the importer only where the attachment's
// with.sources hands it. The host evaluates the signatures without knowing what
// LangGraph is.
var detect = []aggrega.DetectSignature{
	{Syntax: langgraphimport.Syntax, Files: []string{langgraphimport.ConfigFile},
		Match: []aggrega.PathMatch{{Path: "graphs", Is: "object"}}},
	{Syntax: langgraphimport.Syntax,
		Match: []aggrega.PathMatch{{Path: "$schema", Is: "string", Equals: langgraphimport.SchemaURL},
			{Path: "graphs", Is: "object"}}},
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
		pdk.SetError(fmt.Errorf("langgraph importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not in that syntax with the parser's reason.
	out := langgraphimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("langgraph importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
