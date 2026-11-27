// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command cloudformation-importer is the CloudFormation importer Wasm guest. It
// reads CloudFormation templates, AWS SAM templates among them, in YAML with
// short forms or in JSON, and the parameters file beside them, and drafts an
// Aggregate holding one instance per resource, of the catalog Component its
// type matches, connected as the template's references and dependencies say, a
// Release beside it carrying what the template states for its stack and the
// parameters file supplies, and the catalog entities a resource no Component
// covers is drafted as. An import naming no target drafts its Manifest for the
// cloudformation target, where a catalog Recipe binds to it.
//
// The guest holds what the host reads off it: this descriptor, the signatures a
// template and a parameters file are recognised by, and the export wiring. What
// it does with a source is the cfnimport package (plugins/internal/cfnimport),
// which builds on the shared cloud importer core. The resource table it maps
// types through is generated from the hub's cloudformation Recipes by the tool
// in that package and compiled in; the descriptor names the hub revision it was
// read from.
//
// It obtains a parameters file's tree through the host rather than parsing it
// with a grammar of its own: the import export hands the file to the
// aggrega_parse_source host function, and the descriptor declares that host
// function on its import export. A template is read by the template reader the
// package carries, as CloudFormation reads it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi/go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega.Version=<version>" -o cloudformation.wasm ./plugins/cloudformation-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfnimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// descriptor is the static describe() self-report: an importer plugin serving
// the cloudformation format, whose import export imports the aggrega_parse_source
// host function it uses to obtain a parameters file's tree, the signatures a
// template and a parameters file are recognised by, and the target an import
// names none for. The host reads this once at load, validates it, and
// cross-checks the declared host functions against the binary's imports and the
// signatures against the ones the importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import CloudFormation templates, AWS SAM templates among them, in YAML or JSON, and their " +
			"parameters files into a draft Aggregate holding one instance per resource, of the catalog Component its " +
			"type matches, connected as its references and dependencies say, a draft Release carrying what the " +
			"template states for its stack, and the catalog entities a resource no Component covers is drafted as. " +
			"An import that names no target drafts for cloudformation, where a catalog Recipe binds to it. " +
			"Resource types are matched through a table generated from the cloudformation Recipes of aggrega-hub at " +
			cfnimport.TableRevision + ".",
		Capabilities: []string{"import:" + cfnimport.Format.Name},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect, DefaultTarget: cfnimport.Format.Target},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a template and a parameters file are recognised when an import
// names no format, declared in describe() and recorded on the importer's Plugin
// entity: a file stating AWSTemplateFormatVersion, or holding resources under a
// Transform, is a template; a JSON file whose first entry is a list of
// ParameterKey entries, a list of Key=Value text, or an object holding
// parameters and no resources is a parameters file. The version line is read as
// a string whether or not it is quoted. The host evaluates the signatures
// without knowing what CloudFormation is.
var detect = []aggrega.DetectSignature{
	{Syntax: "yaml", Files: []string{"*.yaml", "*.yml", "*.template"},
		Match: []aggrega.PathMatch{{Path: "AWSTemplateFormatVersion", Is: "string"}}},
	{Syntax: "yaml", Files: []string{"*.yaml", "*.yml", "*.template"},
		Match: []aggrega.PathMatch{{Path: "Resources", Is: "object"}, {Path: "Transform", Is: "present"}}},
	{Syntax: "json", Files: []string{"*.json", "*.template"},
		Match: []aggrega.PathMatch{{Path: "AWSTemplateFormatVersion", Is: "string"}}},
	{Syntax: "json", Files: []string{"*.json"},
		Match: []aggrega.PathMatch{{Path: "Resources", Is: "object"}, {Path: "Transform", Is: "present"}}},
	{Syntax: "json", Files: []string{"*.json"},
		Match: []aggrega.PathMatch{{Path: "0.ParameterKey", Is: "string"}}},
	{Syntax: "json", Files: []string{"*.json"},
		Match: []aggrega.PathMatch{{Path: "0", Is: "string"}}},
	{Syntax: "json", Files: []string{"*.json"},
		Match: []aggrega.PathMatch{{Path: "Parameters", Is: "object"}, {Path: "Resources", Is: "absent"}}},
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
		pdk.SetError(fmt.Errorf("cloudformation importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not in it with the parser's reason.
	out := cfnimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("cloudformation importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
