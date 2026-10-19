// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command google-adk-importer is the Google ADK importer Wasm guest. It reads the
// agents declared in the Agent Config files of an application built on Google's
// Agent Development Kit (ADK) — root_agent.yaml and the configurations its
// sub-agents name, where the attachment's with.sources hands them — and drafts
// an Aggregate holding one instance of core/ai-agent/google-adk per agent tree,
// wired to the Gemini model the tree names, and a Release beside it. The
// application's code is never read, nor the environment file ADK's scaffold
// writes beside root_agent.yaml.
//
// The guest holds what the host reads off it: this descriptor, the signatures a
// configuration is recognised by, and the export wiring. What it does with a
// source is the adkimport package (plugins/internal/adkimport), which builds on
// the plugin kit's importer package,
// github.com/aggrega-ai/aggrega-pdk-go/pkg/importer, for the settings, the
// credential decision and the drafts' shared conventions.
//
// It obtains each configuration's tree through the host rather than parsing the
// source with a grammar of its own: the import export hands each configuration
// to the aggrega_parse_source host function, for the YAML syntactic format. So
// it declares the aggrega_parse_source host function on its import export, and
// the load-time cross-check of the descriptor's declared host functions against
// the binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o google-adk.wasm ./plugins/google-adk-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/adkimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the static describe() self-report: a v1alpha1 importer plugin
// serving the google-adk format, whose import export imports the
// aggrega_parse_source host function it uses to obtain each configuration's
// tree, and the signatures a configuration is recognised by. The host reads this
// once at load, validates it, and cross-checks the declared host functions
// against the binary's imports and the signatures against the ones the
// importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import the agents declared in the Agent Config files of an application built on Google ADK (" +
			adkimport.RootFile + " and the configurations its sub-agents name) into a draft Aggregate holding one " +
			adkimport.Component + " instance per agent tree, wired to the Gemini model the tree names, and a draft " +
			"Release beside it; the application's code is not read, nor its environment file.",
		Capabilities: []string{"import:" + adkimport.Format},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how an ADK application's configurations are recognised when an
// import names no format, declared in describe() and recorded on the importer's
// Plugin entity. A root agent's configuration is read as YAML under the name ADK
// loads an application from, root_agent.yaml: an LlmAgent's states its name and
// its instruction, and any other agent's its class and its name. The patterns are
// also what selects the files of a directory the importer is handed, so a
// directory hands it its root_agent.yaml files and no other file: the
// application's code and its environment file are never handed, and neither is a
// sub-agent's configuration, which reaches the importer only where the
// attachment's with.sources hands it. No pattern names every YAML file, since a
// sub-agent's configuration may be called anything and recognition reads every
// file any importer's patterns select, so such a pattern would have every
// directory import of every format open each YAML file it holds. The host
// evaluates the signatures without knowing what ADK is.
var detect = []aggrega.DetectSignature{
	{Syntax: adkimport.Syntax, Files: []string{adkimport.RootFile},
		Match: []aggrega.PathMatch{{Path: "name", Is: "present"}, {Path: "instruction", Is: "present"}}},
	{Syntax: adkimport.Syntax, Files: []string{adkimport.RootFile},
		Match: []aggrega.PathMatch{{Path: "agent_class", Is: "string"}, {Path: "name", Is: "present"}}},
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
		pdk.SetError(fmt.Errorf("google-adk importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not in that syntax with the parser's reason.
	out := adkimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("google-adk importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
