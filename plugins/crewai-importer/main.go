// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command crewai-importer is the CrewAI importer Wasm guest. It reads the agents
// and the tasks a CrewAI project declares — a classic project's
// config/agents.yaml and config/tasks.yaml, or the crew.jsonc and
// agents/<name>.jsonc a project in the JSON-first layout declares them in, or
// their strict-JSON spellings crew.json and agents/<name>.json — and
// drafts an Aggregate holding one instance of core/ai-agent/crewai per agent,
// wired to the model its configuration names, and a Release beside it. The
// crew's program is never read.
//
// The guest holds what the host reads off it: this descriptor, the signatures a
// project's files are recognised by, and the export wiring. What it does with a
// source is the crewaiimport package (plugins/internal/crewaiimport), which
// builds on the plugin kit's importer package,
// github.com/aggrega-ai/aggrega-pdk-go/pkg/importer, for the settings, the
// credential decision and the drafts' shared conventions.
//
// It obtains each source's tree through the host rather than parsing the source
// with a grammar of its own: the import export hands each declaration to the
// aggrega_parse_source host function, for the YAML syntactic format or for JSON
// with comments. So it declares the aggrega_parse_source host function on its
// import export, and the load-time cross-check of the descriptor's declared host
// functions against the binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o crewai.wasm ./plugins/crewai-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/crewaiimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the static describe() self-report: an importer plugin
// serving the crewai format, whose import export imports the aggrega_parse_source
// host function it uses to obtain each source's tree, and the signatures a
// project's files are recognised by. The host reads this once at load, validates
// it, and cross-checks the declared host functions against the binary's imports
// and the signatures against the ones the importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import the agents and tasks a CrewAI project declares (" + crewaiimport.AgentsFile + " and " +
			crewaiimport.TasksFile + ", or " + crewaiimport.CrewFile + " and " + crewaiimport.AgentFiles + ", or their " +
			".json spellings) into a " +
			"draft Aggregate holding one " + crewaiimport.Component + " instance per agent, wired to the model its " +
			"llm names, and a draft Release beside it; the crew's program is not read.",
		Capabilities: []string{"import:" + crewaiimport.Format},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a CrewAI project's files are recognised when an import names no
// format, declared in describe() and recorded on the importer's Plugin entity.
// Every signature names its file, so the patterns are also what selects the files
// of a project directory the importer is handed, and no other file — the crew's
// program included — ever is. A classic project's agents and tasks are keyed by
// names their author chooses, so their files are told by their names alone; a
// crew.jsonc lists its agents at agents, and an agent's file states its role —
// each read as JSON with comments whether it is spelled .jsonc or, in strict
// JSON, .json. The host evaluates the signatures without knowing what CrewAI is.
var detect = []aggrega.DetectSignature{
	{Syntax: crewaiimport.SyntaxYAML, Files: []string{crewaiimport.AgentsFile}},
	{Syntax: crewaiimport.SyntaxYAML, Files: []string{crewaiimport.TasksFile}},
	{Syntax: crewaiimport.SyntaxJSONC, Files: []string{crewaiimport.CrewFile, crewaiimport.CrewJSONFile},
		Match: []aggrega.PathMatch{{Path: "agents", Is: "array"}}},
	{Syntax: crewaiimport.SyntaxJSONC, Files: []string{crewaiimport.AgentFiles, crewaiimport.AgentJSONFiles},
		Match: []aggrega.PathMatch{{Path: "role", Is: "present"}}},
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
		pdk.SetError(fmt.Errorf("crewai importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not in that syntax with the parser's reason.
	out := crewaiimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("crewai importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
