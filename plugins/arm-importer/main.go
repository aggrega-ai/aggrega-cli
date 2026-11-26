// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command arm-importer is the Azure Resource Manager importer Wasm guest. It
// reads the templates written for Azure Resource Manager — language version 1.0
// and 2.0, with comments or without — and the parameters file beside them, and
// drafts an Aggregate holding one instance per resource, of the catalog
// Component its type matches, connected as the template's references and
// dependencies say, a Release beside it carrying what the template states for
// its operation and the parameters file supplies, and the catalog entities a
// resource no Component covers is drafted as. Its Manifest binds the arm
// target, which is the target an import names none for.
//
// The guest holds what the host reads off it: this descriptor, the signatures a
// template and a parameters file are recognised by, and the export wiring. What
// it does with a source is the armimport package (plugins/internal/armimport),
// which builds on the shared cloud importer core. The resource table it maps
// types through is generated from the hub's arm Recipes by the tool in that
// package and compiled in; the descriptor names the hub revision it was read
// from and the Azure Verified Modules index it recognises modules by.
//
// It obtains each source's tree through the host rather than parsing the source
// with a grammar of its own: the import export hands each file to the
// aggrega_parse_source host function for the JSON-with-comments syntactic
// format, and the descriptor declares that host function on its import export.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi/go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega.Version=<version>" -o arm.wasm ./plugins/arm-importer
package main

import (
	"encoding/json"
	"fmt"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// descriptor is the static describe() self-report: an importer plugin serving
// the arm format, whose import export imports the aggrega_parse_source host
// function it uses to obtain each source's tree, the signatures a template and
// a parameters file are recognised by, and the target an import names none for.
// The host reads this once at load, validates it, and cross-checks the declared
// host functions against the binary's imports and the signatures against the
// ones the importer's Plugin entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import Azure Resource Manager templates (language version 1.0 and 2.0, with comments or " +
			"without) and their parameters files into a draft Aggregate holding one instance per resource, of the " +
			"catalog Component its type matches, connected as its references and dependencies say, a draft Release " +
			"carrying what the template states for its operation, and the catalog entities a resource no Component " +
			"covers is drafted as. An import that names no target drafts for arm. Resource types are matched through " +
			"a table generated from the arm Recipes of aggrega-hub at " + armimport.TableRevision +
			"; Azure Verified Modules modules are recognised through the module index of commit " +
			armimport.AVMIndexCommit + ".",
		Capabilities: []string{"import:" + armimport.Format.Name},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect, DefaultTarget: armimport.Format.Target},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a template and a parameters file are recognised when an import
// names no format, declared in describe() and recorded on the importer's Plugin
// entity: a JSON file whose $schema begins with the Azure Resource Manager
// schema address, and which holds resources (a template) or an object at
// parameters (a parameters file). The host evaluates the signatures without
// knowing what Azure Resource Manager is.
var detect = []aggrega.DetectSignature{
	{Syntax: armimport.Format.Syntax, Files: []string{"*.json"},
		Match: []aggrega.PathMatch{
			{Path: "$schema", Is: "string", Prefix: armimport.SchemaPrefix},
			{Path: "resources", Is: "present"},
		}},
	{Syntax: armimport.Format.Syntax, Files: []string{"*.json"},
		Match: []aggrega.PathMatch{
			{Path: "$schema", Is: "string", Prefix: armimport.SchemaPrefix},
			{Path: "parameters", Is: "object"},
		}},
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
		pdk.SetError(fmt.Errorf("arm importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not JSON with comments with the parser's reason.
	out := armimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("arm importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
