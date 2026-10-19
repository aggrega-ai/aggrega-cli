// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command mcp-importer is the MCP importer Wasm guest. It reads the MCP servers
// an MCP client's configuration lists — Claude Code's .mcp.json, Claude
// Desktop's claude_desktop_config.json, Visual Studio Code's .vscode/mcp.json and
// Cursor's .cursor/mcp.json — or the one server an MCP registry server
// description, server.json, describes, and drafts an Aggregate holding one
// instance of core/ai-tool/mcp per server and a Release beside it carrying what
// each server runs and the environment it is started with.
//
// The guest holds what the host reads off it: this descriptor, the signatures
// the files are recognised by, and the export wiring. What it does with a source
// is the mcpimport package (plugins/internal/mcpimport), which builds on the
// plugin kit's importer package, github.com/aggrega-ai/aggrega-pdk-go/pkg/importer,
// for the settings, the credential decision and the drafts' shared conventions.
//
// It obtains each source's tree through the host rather than parsing the source
// with a grammar of its own: the import export hands the source, its comments and
// trailing commas written as spaces, to the aggrega_parse_source host function
// for the JSON syntactic format. So it declares the aggrega_parse_source host
// function on its import export, and the load-time cross-check of the
// descriptor's declared host functions against the binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o mcp.wasm ./plugins/mcp-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the static describe() self-report: a v1alpha1 importer plugin
// serving the mcp format, whose import export imports the aggrega_parse_source
// host function it uses to obtain each source's tree, and the signatures an MCP
// file is recognised by. The host reads this once at load, validates it, and
// cross-checks the declared host functions against the binary's imports and the
// signatures against the ones the importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "importer",
		Description:  "Import an MCP client configuration (.mcp.json, claude_desktop_config.json, .vscode/mcp.json, .cursor/mcp.json) or an MCP registry server description (server.json) into a draft Aggregate holding one core/ai-tool/mcp instance per server, and a draft Release beside it carrying each server's command line, address or image and its environment.",
		Capabilities: []string{"import:" + mcpimport.Format},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how an MCP file is recognised when an import names no format,
// declared in describe() and recorded on the importer's Plugin entity. The
// three client configurations writing their servers under mcpServers are told
// apart from one another only by their names, so each signature names its file
// and asks for an object at mcpServers; Visual Studio Code's names its file and
// asks for an object at servers. A registry server description is told by its
// content alone, its $schema opening with the address the registry publishes
// its schema under, so its signature names no file and applies to any file
// imported on its own. A file holding comments is not recognised, since the
// host reads a source as JSON to try a signature: it is imported naming its
// format. The host evaluates the signatures without knowing what MCP is.
var detect = []aggrega.DetectSignature{
	{Syntax: mcpimport.Syntax, Files: []string{".mcp.json"}, Match: []aggrega.PathMatch{{Path: "mcpServers", Is: "object"}}},
	{Syntax: mcpimport.Syntax, Files: []string{"claude_desktop_config.json"},
		Match: []aggrega.PathMatch{{Path: "mcpServers", Is: "object"}}},
	{Syntax: mcpimport.Syntax, Files: []string{".cursor/mcp.json"},
		Match: []aggrega.PathMatch{{Path: "mcpServers", Is: "object"}}},
	{Syntax: mcpimport.Syntax, Files: []string{".vscode/mcp.json"}, Match: []aggrega.PathMatch{{Path: "servers", Is: "object"}}},
	{Syntax: mcpimport.Syntax, Match: []aggrega.PathMatch{{Path: "$schema", Is: "string", Prefix: mcpimport.SchemaPrefix}}},
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
		pdk.SetError(fmt.Errorf("mcp importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not JSON with the parser's reason.
	out := mcpimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("mcp importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
