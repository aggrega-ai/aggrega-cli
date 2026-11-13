// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Command hcl-parser is the embedded HCL parser Wasm guest. It turns HCL
// (HashiCorp Configuration Language) source text into a canonical JSON tree so a
// consumer can read the structure without carrying its own HCL parser. It
// self-describes as a parser plugin advertising the parse:hcl format, and its
// parse export decodes a ParseInput, converts the source bytes through the shared
// parsercore conversion, and returns a ParseOutput. The grammar work lives in
// parsercore, which is exercised directly on the host toolchain; this guest holds
// only the ABI decode, the call, and the ABI encode.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o hcl.wasm ./plugins/hcl-parser
package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// descriptor is the describe() self-report: a parser plugin whose parse
// export handles the parse:hcl format. The host reads this once at load and
// validates it before invoking parse.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "parser",
		Description:  "Parse HCL (HashiCorp Configuration Language) source into a canonical JSON tree.",
		Capabilities: []string{"parse:hcl"},
		Exports: []aggrega.PluginExport{{
			Name: "parse",
		}},
		Metadata: &aggrega.PluginDescriptorMeta{Author: "aggrega", License: "Apache-2.0"},
	}
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

// parse reads the ParseInput, converts its source bytes through parsercore, and
// answers with the ParseOutput. The input and the answer both travel as canonical
// CBOR: this socket carries the binary encoding in both directions, so the source
// bytes arrive as a raw byte string and the tree goes back as a byte string
// holding its JSON document verbatim.
//
//go:wasmexport parse
func parse() int32 {
	var in aggrega.ParseInput
	if err := in.DecodeCBOR(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}
	raw, err := parsercore.ParseHCL(in.Bytes).EncodeCBOR()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
