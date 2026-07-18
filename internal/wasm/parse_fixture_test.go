// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestParserFixtureDescribe exercises the parser-plugin scaffold in each of the
// three PDK languages end-to-end at the describe() boundary: it builds the
// guest, loads it, reads describe(), and asserts the host reads the descriptor
// as a parser plugin advertising the parse:demo capability and the parse
// export. It then dispatches parse with a ParseInput payload and asserts the
// guest returns a non-empty ParseOutput — proving both codec directions run
// inside a live guest. Skips cleanly only when a language's build toolchain is
// absent from PATH.
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
			if !desc.hasExport(ParseExport) {
				t.Fatalf("descriptor does not declare the %q export", ParseExport)
			}

			// Dispatch the declared parse export with a ParseInput and assert a
			// non-empty ParseOutput comes back — the guest ran both codec
			// directions (decode the input, encode the output).
			out, err := InvokeExport(ctx, plugin, desc, ParseExport,
				[]byte(`{"format":"demo","bytes":"aGVsbG8="}`))
			if err != nil {
				t.Fatalf("InvokeExport(parse): %v", err)
			}
			if len(out) == 0 {
				t.Fatal("parse returned empty output")
			}
		})
	}
}
