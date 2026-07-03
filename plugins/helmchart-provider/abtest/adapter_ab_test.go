// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
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

// TestWasmEngineAdapterHelmChartRenderByteIdentity proves the generalized
// wasmengine.Engine adapter — constructed with the helm-chart Go engine —
// reproduces the in-tree HelmEngine.Render byte-for-byte when driven strictly
// through the renderer.Engine interface. This is the end-to-end proof of the
// adapter's provider-agnostic projection: the adapter type-asserts the wrapped
// helm-chart engine to renderer.SourceProjector, projects the chart's files onto
// the render wire through ProjectSourcedChart, loads the guest through the
// production describe-gated loader, invokes render_node, and folds the output back
// into the interface's path→bytes map — with no helmchart package import inside
// the adapter.
//
// helm-chart copies chart files verbatim, so the output is invariant to values,
// indent, provenance, and annotations; the non-vacuity guard instead asserts the
// copy carried the full multi-file chart, so a byte-identical match is not a match
// over an empty or single-file set.
func TestWasmEngineAdapterHelmChartRenderByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmBytes, err := os.ReadFile(buildPluginWasm(t))
	if err != nil {
		t.Fatalf("read guest wasm: %v", err)
	}

	chartDir := chartFixtureDir(t)
	nodeID := "api"

	// The candidate is driven strictly through the renderer.Engine interface value
	// — never a concrete method — so the A/B exercises exactly the seam the engine
	// dispatch selects. New wraps the helm-chart Go engine; the adapter derives
	// caps from it and projects the source through its SourceProjector.
	var eng renderer.Engine = wasmengine.New(cannedLookup(wasmBytes), "test://helm-chart-renderer", &helmchart.HelmEngine{}, &wasm.Loader{})

	nodeCtx := renderer.NodeRenderContext{NodeID: nodeID, Source: chartDir}

	oracle, err := (&helmchart.HelmEngine{}).Render(ctx, nil, nodeCtx)
	if err != nil {
		t.Fatalf("oracle Render: %v", err)
	}
	if len(oracle) < 2 {
		t.Fatalf("fixture chart produced %d files; want at least Chart.yaml plus a template so the multi-file copy is exercised", len(oracle))
	}

	candidate, err := eng.Render(ctx, nil, nodeCtx)
	if err != nil {
		t.Fatalf("adapter Render: %v", err)
	}

	assertByteIdentical(t, oracle, candidate)
}
