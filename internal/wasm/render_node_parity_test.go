// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// renderNodeParityInput builds the single fixed RenderNodeInput both
// per-language render fixtures render. The node id and prefix are ASCII so
// the guests' UTF-8 encoding of the emitted file content is unambiguous and
// identical across languages.
func renderNodeParityInput(t *testing.T) []byte {
	t.Helper()
	in := renderer.RenderNodeInput{
		Node: renderer.IRNodeProjection{
			ID:             "parity-node-01",
			Type:           "core/web-service",
			ResolvedRecipe: "core/recipe/parity:1.0.0",
			Provenance: renderer.NodeProvenance{
				CompilerVersion: "test",
				AggregateName:   "parity",
				Environment:     "prod",
			},
		},
		Context: renderctx.RenderContext{},
	}
	b, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal RenderNodeInput: %v", err)
	}
	return b
}

// renderNodeFixtureOutput loads the named render fixture, exercises its
// describe() self-report through the host descriptor validator, invokes
// render_node with the shared input, and returns the decoded output.
func renderNodeFixtureOutput(t *testing.T, fixtureName string, input []byte) renderer.RenderNodeOutput {
	t.Helper()
	plugin, cleanup := loadFixturePlugin(t, fixtureName)
	defer cleanup()

	descBytes, err := plugin.Call(context.Background(), "describe", nil)
	if err != nil {
		t.Fatalf("plugin.Call describe (%s): %v", fixtureName, err)
	}
	var desc wasm.PluginDescriptor
	if err := json.Unmarshal(descBytes, &desc); err != nil {
		t.Fatalf("unmarshal PluginDescriptor (%s): %v (bytes: %s)", fixtureName, err, descBytes)
	}
	if err := desc.Validate(fixtureName); err != nil {
		t.Fatalf("descriptor Validate (%s): %v", fixtureName, err)
	}

	outBytes, err := plugin.Call(context.Background(), "render_node", input)
	if err != nil {
		t.Fatalf("plugin.Call render_node (%s): %v", fixtureName, err)
	}
	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(outBytes); err != nil {
		t.Fatalf("unmarshal RenderNodeOutput (%s): %v (bytes: %s)", fixtureName, err, outBytes)
	}
	return out
}

// TestRenderSocketParity_RustTypeScript proves the Rust and TypeScript PDK
// render sockets produce equivalent output for one fixed RenderNodeInput.
// Each fixture is loaded through the real loader, its descriptor is
// validated, and its render_node output is decoded through the host's
// generated RenderNodeOutput codec (so the comparison is over decoded
// structure, not raw guest JSON bytes). The Rust and TypeScript outputs
// must be structurally equal: cross-language render-socket parity.
func TestRenderSocketParity_RustTypeScript(t *testing.T) {
	input := renderNodeParityInput(t)

	rustOut := renderNodeFixtureOutput(t, "render-node-rust.wasm", input)
	tsOut := renderNodeFixtureOutput(t, "render-node-typescript.wasm", input)

	// Pin the per-fixture contract first: exactly one file at the fixed
	// path whose content echoes the node id. This makes a divergence report
	// which side broke the contract before the cross-language comparison.
	for _, tc := range []struct {
		name string
		out  renderer.RenderNodeOutput
	}{
		{"rust", rustOut},
		{"typescript", tsOut},
	} {
		if len(tc.out.Files) != 1 {
			t.Fatalf("%s: expected exactly one rendered file; got %d", tc.name, len(tc.out.Files))
		}
		if tc.out.Files[0].Path != "parity.txt" {
			t.Errorf("%s: unexpected file path %q", tc.name, tc.out.Files[0].Path)
		}
		if got := string(tc.out.Files[0].Content); got != "rendered-node:parity-node-01" {
			t.Errorf("%s: unexpected file content %q", tc.name, got)
		}
	}

	if !reflect.DeepEqual(rustOut, tsOut) {
		t.Fatalf("render-socket parity mismatch between Rust and TypeScript:\n rust = %#v\n ts   = %#v", rustOut, tsOut)
	}
}
