// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// cannedLookup returns a pluginpath.Lookup that resolves every URI to the guest
// bytes b, carrying an explicit memory cap. The standard-Go SDK guest declares a
// linear-memory minimum above the tight platform default (1024 pages); the cap
// here is high enough (4096 pages) that the guest loads rather than trapping,
// which also proves the adapter plumbs the resolved plugin's MaxMemoryPages
// through resolver.PluginSpec into the loader — a load at the default cap would
// trap before render_node runs.
func cannedLookup(b []byte) pluginpath.Lookup {
	return func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{
			Bytes:  b,
			Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096, TimeoutMs: 300000},
		}, nil
	}
}

// TestWasmEngineAdapterComposeRenderByteIdentity proves the generalized
// wasmengine.Engine adapter — constructed with the compose Go engine — reproduces
// the in-tree ComposeEngine.Render byte-for-byte when driven strictly through the
// renderer.Engine interface. This is the end-to-end proof of the adapter's
// provider-agnostic projection: the adapter type-asserts the wrapped compose
// engine to renderer.SourceProjector, projects the base compose file onto the
// render wire through ProjectComposeSource, loads the guest through the production
// describe-gated loader, invokes render_node, and folds the output back into the
// interface's path→bytes map — with no compose package import inside the adapter.
//
// Cases: the default indent and indent 4 (re-marshals the merged document
// visibly wider). The cross-case guard proves the indent carried through the
// adapter is observable rather than a no-op.
func TestWasmEngineAdapterComposeRenderByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmBytes, err := os.ReadFile(buildPluginWasm(t))
	if err != nil {
		t.Fatalf("read guest wasm: %v", err)
	}

	baseCompose := absPath(t, "../../../internal/compiler/testdata/fullstack-app/catalog/recipes/compose-sourced/sources/api.yaml")

	nodeID := "api"
	valuesOrdered := valuesFromJSON(t, `{"services":{"api":{"environment":{"PORT":"8080","LOG_LEVEL":"info"}}}}`)
	values := orderedToPlain(&valuesOrdered)

	// The candidate is driven strictly through the renderer.Engine interface
	// value — never a concrete method — so the A/B exercises exactly the seam the
	// engine dispatch selects. New wraps the compose Go engine; the adapter derives
	// caps from it and projects the source through its SourceProjector.
	var eng renderer.Engine = wasmengine.New(cannedLookup(wasmBytes), "test://compose-renderer", &compose.ComposeEngine{}, &wasm.Loader{})

	cases := []struct {
		name   string
		indent int
	}{
		{"default-indent", 0},
		{"indent-4", 4},
	}

	captured := make(map[string]map[string][]byte, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodeCtx := renderer.NodeRenderContext{NodeID: nodeID, Source: baseCompose, YAMLIndent: tc.indent}
			stampOracleProvenance(&nodeCtx)

			oracle, err := (&compose.ComposeEngine{}).Render(ctx, values, nodeCtx)
			if err != nil {
				t.Fatalf("oracle Render: %v", err)
			}

			candidate, err := eng.Render(ctx, values, nodeCtx)
			if err != nil {
				t.Fatalf("adapter Render: %v", err)
			}

			assertByteIdentical(t, oracle, candidate)
			captured[tc.name] = candidate
		})
	}

	if def, ok := captured["default-indent"]; ok {
		if wide, ok := captured["indent-4"]; ok && byteMapsEqual(def, wide) {
			t.Fatal("indent 4 produced byte-identical output to the default indent through the adapter; the indent carriage is not observable, so the A/B is a no-op")
		}
	}
}

// TestWasmEngineAdapterComposeSynthByteIdentity proves the generalized
// wasmengine.Engine adapter reproduces the in-tree ComposeEngine.SynthesizeWorkload
// byte-for-byte when driven strictly through the renderer.WorkloadSynthesizer
// interface. The adapter projects the workload onto the render wire (compose
// consumes no per-slot secret provides, so the projected Secrets are dropped by
// the guest), invokes render_node, and returns the synthesized fragment in
// emission order. Emission order is load-bearing, so the files are compared
// index-for-index.
func TestWasmEngineAdapterComposeSynthByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmBytes, err := os.ReadFile(buildPluginWasm(t))
	if err != nil {
		t.Fatalf("read guest wasm: %v", err)
	}

	var synth renderer.WorkloadSynthesizer = wasmengine.New(cannedLookup(wasmBytes), "test://compose-renderer", &compose.ComposeEngine{}, &wasm.Loader{})

	cases := []struct {
		name   string
		nodeID string
		build  func() *types.Workload
	}{
		{"minimal-single-container", "api", minimalWorkload},
		{"multi-container-main-and-sidecar", "api", multiContainerWorkload},
		{"secrets-as-env-and-file", "store", secretsWorkload},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.build()

			nodeCtx := renderer.NodeRenderContext{NodeID: tc.nodeID}
			stampOracleProvenance(&nodeCtx)

			// ORACLE — the in-tree compose engine.
			oracle, err := (&compose.ComposeEngine{}).SynthesizeWorkload(ctx, nil, w, nodeCtx)
			if err != nil {
				t.Fatalf("oracle SynthesizeWorkload: %v", err)
			}

			// CANDIDATE — the adapter through the WorkloadSynthesizer interface.
			candidate, err := synth.SynthesizeWorkload(ctx, nil, w, nodeCtx)
			if err != nil {
				t.Fatalf("adapter SynthesizeWorkload: %v", err)
			}

			assertSynthByteIdentical(t, oracle, emittedToRendered(candidate))
		})
	}
}

// emittedToRendered adapts the interface's []EmittedFile to the []RenderedFile
// shape assertSynthByteIdentical compares against, preserving order.
func emittedToRendered(files []renderer.EmittedFile) []renderer.RenderedFile {
	out := make([]renderer.RenderedFile, len(files))
	for i, f := range files {
		out[i] = renderer.RenderedFile{Path: f.Name, Content: f.Body}
	}
	return out
}
