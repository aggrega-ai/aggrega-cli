// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command compose-importer is the Compose importer Wasm guest. It reads the
// services and the models a Compose application declares — compose.yaml or
// docker-compose.yml, with its override file — and drafts an Aggregate holding
// one instance per service, of the catalog Component its image matches or of
// core/compute/server running its image, connected as depends_on says, one
// drafted model Component per model, and a Release beside it carrying what each
// service states for its operation.
//
// The guest holds what the host reads off it: this descriptor, the signature a
// Compose file is recognised by, and the export wiring. What it does with a
// source is the composeimport package (plugins/internal/composeimport), which
// builds on the plugin kit's importer package,
// github.com/aggrega-ai/aggrega-pdk-go/pkg/importer, for the decision a service is
// drafted by, the settings, the credential decision and the drafts' shared
// conventions. The image table it matches images through is generated from the
// hub's Compose Recipes by the tool in plugins/internal/composeimport/imagetable
// and compiled in; the descriptor names the hub revision it was read from.
//
// It obtains each source's tree through the host rather than parsing the source
// with a grammar of its own: the import export hands each Compose file to the
// aggrega_parse_source host function for the YAML syntactic format. So it
// declares the aggrega_parse_source host function on its import export, and the
// load-time cross-check of the descriptor's declared host functions against the
// binary's imports covers it.
//
// The import payload is decoded and the result encoded through the generated ABI
// codec (github.com/aggrega-ai/aggrega-abi-go/pkg/abi, re-exported by the plugin
// kit), in the canonical CBOR the importer socket carries in both directions.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags "-X github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega.Version=<version>" -o compose.wasm ./plugins/compose-importer
package main

import (
	"encoding/json"
	"fmt"
	"slices"

	pdk "github.com/extism/go-pdk"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/composeimport"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// descriptor is the static describe() self-report: a v1alpha1 importer plugin
// serving the compose format, whose import export imports the
// aggrega_parse_source host function it uses to obtain each source's tree, and
// the signature a Compose file is recognised by. The host reads this once at
// load, validates it, and cross-checks the declared host functions against the
// binary's imports and the signature against the one the importer's Plugin
// entity records.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "importer",
		Description: "Import a Compose application (compose.yaml, docker-compose.yml and their override files) into a " +
			"draft Aggregate holding one instance per service — of the catalog Component its image matches, or of " +
			"core/compute/server running its image — connected as depends_on says, a drafted model Component per " +
			"model, and a draft Release carrying each service's image, ports, volumes and environment. Images are " +
			"matched through a table generated from the Compose Recipes and Components of aggrega-hub at " +
			composeimport.TableRevision + ".",
		Capabilities: []string{"import:" + composeimport.Format},
		Exports: []aggrega.PluginExport{{
			Name:          "import",
			HostFunctions: []string{"aggrega_parse_source"},
		}},
		Importer: &aggrega.ImporterContract{Detect: detect},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
}

// detect is how a Compose file is recognised when an import names no format,
// declared in describe() and recorded on the importer's Plugin entity: a YAML
// source named as Compose reads a Compose file or its override file by default,
// holding an object at services. A Compose file under any other name is imported
// naming its format. The host evaluates the signature without knowing what
// Compose is.
var detect = []aggrega.DetectSignature{{
	Syntax: composeimport.Syntax,
	Files:  slices.Concat(composeimport.BaseFileNames, composeimport.OverrideFileNames),
	Match:  []aggrega.PathMatch{{Path: "services", Is: "object"}},
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
		pdk.SetError(fmt.Errorf("compose importer: decode ImportInput: %w", err))
		return 1
	}

	// The parse callback is the guest's only channel to a parser: it asks the
	// host to run the parser for the syntactic format, which refuses a source
	// that is not YAML with the parser's reason.
	out := composeimport.Import(in, func(syntax string, source []byte) (json.RawMessage, error) {
		return aggrega.ParseSource(syntax, source, nil)
	})

	raw, err := out.EncodeCBOR()
	if err != nil {
		pdk.SetError(fmt.Errorf("compose importer: encode ImportOutput: %w", err))
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
