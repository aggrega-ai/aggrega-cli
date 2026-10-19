// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Command jsonc-parser is the embedded parser Wasm guest for JSON with comments,
// the syntax Visual Studio Code and CrewAI write their configuration in. It turns
// such a document into the canonical JSON tree — object keys sorted and integers
// kept exact — that the JSON parser yields for the same document without its
// comments and trailing commas. It self-describes as a parser plugin advertising
// the parse:jsonc format, and its parse export decodes a ParseInput, converts the
// source bytes through the shared parsercore conversion, and returns a
// ParseOutput. The work lives in parsercore, which is exercised directly on the
// host toolchain; this guest holds only the ABI decode, the call, and the ABI
// encode.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o jsonc.wasm ./plugins/jsonc-parser
package main

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	pdk "github.com/extism/go-pdk"
)

// descriptor is the describe() self-report: a v1alpha1 parser plugin whose parse
// export handles the parse:jsonc format. The host reads this once at load and
// validates it before invoking parse.
func descriptor() aggrega.PluginDescriptor {
	return aggrega.PluginDescriptor{
		AbiVersion: aggrega.APIVersion,
		PluginType: "parser",
		Description: "Parse JSON with comments — line and block comments and trailing commas — into the canonical " +
			"JSON tree the JSON parser yields for the same document without them.",
		Capabilities: []string{"parse:jsonc"},
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
	raw, err := parsercore.ParseJSONC(in.Bytes).EncodeCBOR()
	if err != nil {
		pdk.SetError(err)
		return 1
	}
	pdk.Output(raw)
	return 0
}

func main() {}
