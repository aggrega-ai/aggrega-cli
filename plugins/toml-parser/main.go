// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command toml-parser is the embedded TOML parser Wasm guest. It turns TOML
// source text into a canonical JSON tree so a consumer can read the structure
// without carrying its own TOML parser. It self-describes as a parser plugin
// advertising the parse:toml format, and its parse export decodes a ParseInput,
// converts the source bytes through the shared parsercore conversion, and returns
// a ParseOutput. The grammar work lives in parsercore, which is exercised directly
// on the host toolchain; this guest holds only the ABI decode, the call, and the
// ABI encode.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o toml.wasm ./plugins/toml-parser
package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// descriptor is the describe() self-report: a v1alpha1 parser plugin whose parse
// export handles the parse:toml format. The host reads this once at load and
// validates it before invoking parse.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion:   aggrega.APIVersion,
		PluginType:   "parser",
		Description:  "Parse TOML source into a canonical JSON tree.",
		Capabilities: []string{"parse:toml"},
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

//go:wasmexport parse
func parse() int32 {
	var in aggrega.ParseInput
	if err := in.UnmarshalJSON(pdk.Input()); err != nil {
		pdk.SetError(err)
		return 1
	}
	raw, err := parsercore.ParseTOML(in.Bytes).MarshalJSON()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
