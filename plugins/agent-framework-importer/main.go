// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command agent-framework-importer is the Agent Framework importer Wasm guest.
// It reads a Microsoft Agent Framework declarative agent — the YAML document of
// kind Prompt an application built on Agent Framework loads an agent from — and
// drafts an Aggregate holding one instance of core/ai-agent/agent-framework per
// declarative agent, wired to the OpenAI model it names, with each MCP server it
// names written in a comment, and a Release beside it. The application's code is
// never read.
//
// The guest holds what the host reads off it: this descriptor, the signature a
// declarative agent is recognised by, and the export wiring. What it does with a
// source is the agentframeworkimport package
// (plugins/internal/agentframeworkimport), which builds on the plugin kit's
// importer package, github.com/aggrega-ai/aggrega-pdk-go/pkg/importer, for the
// settings, the credential decision and the drafts' shared conventions.
//
// It obtains each declarative agent's tree through the host rather than parsing
// the source with a grammar of its own: the import export hands each source to
// the aggrega_parse_source host function, for the YAML syntactic format. So it
// declares the aggrega_parse_source host function on its import export, and the
// load-time cross-check of the descriptor's declared host functions against the
// binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o agent-framework.wasm ./plugins/agent-framework-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/agentframeworkimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the static describe() self-report: an importer plugin
// serving the agent-framework format, whose import export imports the
// aggrega_parse_source host function it uses to obtain each declarative agent's
// tree, and the signature a declarative agent is recognised by. The host reads
// this once at load, validates it, and cross-checks the declared host functions
// against the binary's imports and the signature against the one the importer's
// Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import a Microsoft Agent Framework declarative agent (a YAML document of kind Prompt) into a " +
			"draft Aggregate holding one " + agentframeworkimport.Component + " instance per declarative agent, wired " +
			"to the OpenAI model it names, with each MCP server it names written in a comment, since the model's " +
			"provider reaches it, and a draft Release beside it; the application's code is not read.",
		Capabilities: []string{"import:" + agentframeworkimport.Format},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a declarative agent is recognised when an import names no
// format, declared in describe() and recorded on the importer's Plugin entity: a
// file read as YAML whose kind is Prompt and whose model is an object, under any
// name, since Agent Framework loads a declarative agent from whatever file its
// application names. The signature names no file, so it applies to a source that
// is one file and to no file of a directory: a directory is never handed to the
// importer, and no directory import of any format opens a YAML file for it. The
// host evaluates the signature without knowing what Agent Framework is.
var detect = []aggrega.DetectSignature{
	{Syntax: agentframeworkimport.Syntax,
		Match: []aggrega.PathMatch{{Path: "kind", Is: "string", Equals: agentframeworkimport.Kind},
			{Path: "model", Is: "object"}}},
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
		pdk.SetError(fmt.Errorf("agent-framework importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not in that syntax with the parser's reason.
	out := agentframeworkimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("agent-framework importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
