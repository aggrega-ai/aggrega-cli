// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestParserFixtureDescribe exercises the parser-plugin scaffold in each of the
// three PDK languages end-to-end at the describe() boundary: it builds the
// guest, loads it, reads describe(), and asserts the host reads the descriptor
// as a parser plugin advertising the parse:demo capability and the parse
// export. It then dispatches parse with a ParseInput in the socket's canonical
// CBOR and reads the answer through the strict binary decode the host uses —
// proving both codec directions run inside a live guest, on the binary
// encoding, in every language: a guest still reading or writing JSON fails one
// of the two. Skips cleanly only when a language's build toolchain is absent
// from PATH.
func TestParserFixtureDescribe(t *testing.T) {
	t.Parallel()

	for _, artifact := range []string{"parse-go.wasm", "parse-rust.wasm", "parse-typescript.wasm"} {
		artifact := artifact
		t.Run(artifact, func(t *testing.T) {
			t.Parallel()
			pluginPath := wasmtest.EnsureFixtureBuilt(t, artifact)

			loader := &Loader{}
			ctx := context.Background()

			plugin, desc, err := loader.LoadAndDescribe(ctx, PluginSpec{
				Name: artifact,
				Path: pluginPath,
			})
			if err != nil {
				t.Fatalf("LoadAndDescribe: %v", err)
			}
			defer plugin.Close(ctx)

			if desc.PluginType != string(PluginTypeParser) {
				t.Fatalf("plugin type: got %q want %q", desc.PluginType, PluginTypeParser)
			}
			if !slices.Contains(desc.Capabilities, "parse:demo") {
				t.Fatalf("descriptor capabilities %v do not advertise parse:demo", desc.Capabilities)
			}
			if !desc.hasExport(ParseExport.Name()) {
				t.Fatalf("descriptor does not declare the %q export", ParseExport)
			}

			// Dispatch the declared parse export with a ParseInput and read the
			// ParseOutput back — the guest ran both codec directions (decode the
			// input, encode the output). Each scaffold echoes the format into its
			// tree, so the tree carrying it shows the guest read what it was given.
			payload, err := abi.ParseInput{Format: "demo", Bytes: []byte("hello")}.EncodeCBOR()
			if err != nil {
				t.Fatalf("encode the parse input: %v", err)
			}
			out, err := InvokeExport(ctx, plugin, desc, ParseExport, payload)
			if err != nil {
				t.Fatalf("InvokeExport(parse): %v", err)
			}
			var po abi.ParseOutput
			if err := DecodeGuestOutputCBOR(ParseExport.Name(), out, &po); err != nil {
				t.Fatalf("the answer is not a ParseOutput in canonical CBOR: %v (bytes %q)", err, out)
			}
			if !strings.Contains(string(po.Tree), `"format":"demo"`) {
				t.Fatalf("tree = %s; want the scaffold's echo of the format it was handed", po.Tree)
			}
		})
	}
}
