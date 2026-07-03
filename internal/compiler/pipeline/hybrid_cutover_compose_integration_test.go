// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestHybridCutoverComposeProductionPathByteIdentity is the production-path A/B
// for the compose render+assemble cutover. It builds a hub-wired fixture — a
// Provider(compose) bound to a Plugin advertising BOTH render_node and
// assemble_boundary, whose artifact resolves from the embedded tier — twice
// through the real pipeline.RunPipeline:
//
//   - the Wasm build passes the full catalog lookup, so the in-pipeline hybrid
//     resolves the Provider→Plugin→exports chain and routes the compose nodes
//     through the wasmengine assembler adapter (per-node render AND per-group
//     assembly in Wasm);
//   - the Go build passes the same lookup with only the Provider accessor nil'd,
//     so the hybrid falls back to the built-in Go compose engine.
//
// The Provider accessor has no other consumer in the pipeline, so nil'ing it
// changes only the routing decision — every other render input is identical.
// The two builds must therefore produce byte-identical artifacts, proving the
// Wasm render+assemble path reproduces the Go engine across the production spine
// for a SOURCED compose node (a local compose file), two SYNTHESIZED compose
// nodes (workloads), AND the multi-node boundary assembly that merges all three
// into a single docker-compose.yaml with per-node networks (distinct
// aggrega.ai/namespace annotations) and depends_on edges inferred from the IR
// uses graph.
//
// The assertions below on the assembled artifact (distinct networks, depends_on)
// prove the multi-node assembly genuinely ran through Wasm rather than degrading
// to per-node output.
func TestHybridCutoverComposeProductionPathByteIdentity(t *testing.T) {
	ctx := context.Background()
	parts := loadCutoverParts(t, "testdata/hybrid-cutover-compose")

	runBuild := func(lookup resolver.CatalogLookup) map[string][]byte {
		res, err := pipeline.RunPipeline(
			ctx, parts.inputs, lookup, parts.recipeLookup, parts.subAgg,
			enginefactory.Dispatch, parts.opts,
		)
		if err != nil {
			t.Fatalf("RunPipeline: %v", err)
		}
		return res.Artifacts
	}

	// Wasm build: full lookup → hybrid resolves → wasmengine assembler adapter.
	wasmArtifacts := runBuild(parts.buildLookup)

	// Go build: identical inputs, Provider accessor nil'd → hybrid falls back.
	goLookup := parts.buildLookup
	goLookup.Provider = nil
	goArtifacts := runBuild(goLookup)

	assertByteIdentical(t, goArtifacts, wasmArtifacts)

	joined := concatArtifacts(wasmArtifacts)

	// Prove each input kind actually rendered through the Wasm build, so the A/B
	// is not silently covering only one path. The sourced compose stamps
	// "web:1.2.3"; the synthesized workloads stamp "api:2.0" and "postgres:16".
	for _, marker := range []string{"web:1.2.3", "api:2.0", "postgres:16"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("expected marker %q in the Wasm build output (input-kind coverage)", marker)
		}
	}

	// Prove the multi-node boundary assembly ran: all three per-node networks
	// (from the distinct aggrega.ai/namespace annotations) land in one assembled
	// docker-compose.yaml.
	for _, network := range []string{"web-zone", "api-zone", "db-zone"} {
		if !strings.Contains(joined, network) {
			t.Errorf("expected per-node network %q in the assembled compose output", network)
		}
	}

	// Prove depends_on was inferred from the IR uses edges (web→api, api→db) and
	// emitted into the assembled artifact.
	if !strings.Contains(joined, "depends_on") {
		t.Errorf("expected depends_on entries inferred from uses edges in the assembled compose output")
	}

	// Exactly one assembled docker-compose.yaml is written (per-node fragments are
	// intermediate and never hit disk), proving the merge collapsed the group.
	assembledCount := 0
	for path := range wasmArtifacts {
		if strings.HasSuffix(path, "/"+compose.AssembledOutputFile) || path == compose.AssembledOutputFile {
			assembledCount++
		}
	}
	if assembledCount != 1 {
		t.Errorf("expected exactly one assembled %s, got %d (paths: %v)", compose.AssembledOutputFile, assembledCount, sortedKeys(wasmArtifacts))
	}
}

// TestHybridCutoverComposeRouting proves the routing decision the byte-identity
// build relies on: with the hub Provider wired to a Plugin advertising BOTH
// render_node and assemble_boundary, the hybrid returns the ASSEMBLER Wasm
// adapter for the compose provider (not the render-only adapter, and not the Go
// engine); with the Provider accessor removed, it returns the Go engine.
// Asserting both directions makes the byte-identity airtight — the two builds
// took different engine paths yet produced identical bytes.
func TestHybridCutoverComposeRouting(t *testing.T) {
	parts := loadCutoverParts(t, "testdata/hybrid-cutover-compose")

	// Entity-fronted plugin lookup: entity URI → spec.plugin → cascade (embed
	// tier serves the embedded guest). The routing decision does not invoke it,
	// but building the real one keeps the assertion faithful.
	cascade := (pluginpath.Resolver{}).Resolve
	pluginLookup := pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginCapabilityClaim, *pluginpath.PluginRuntimeLimits, bool) {
		entity, ok := parts.buildLookup.Plugin(uri)
		if !ok {
			return "", nil, nil, false
		}
		return entity.Spec.Plugin, resolver.PluginCapabilityClaim(entity), resolver.PluginRuntimeLimits(entity), true
	}, cascade)

	sharedLoader, closeLoader := wasm.NewCachingLoader(nil)
	defer func() { _ = closeLoader() }()

	// Hub Provider wired, Plugin advertises assemble_boundary → assembler adapter.
	hybrid := enginefactory.NewHybrid(enginefactory.Dispatch, parts.buildLookup, pluginLookup, sharedLoader)
	eng, err := hybrid("compose")
	if err != nil {
		t.Fatalf("hybrid(compose): %v", err)
	}
	if _, ok := eng.(*wasmengine.AssemblerEngine); !ok {
		t.Fatalf("hub Provider compose routed to %T, want *wasmengine.AssemblerEngine", eng)
	}

	// Plain Go dispatch → in-tree compose engine.
	goEng, err := enginefactory.Dispatch("compose")
	if err != nil {
		t.Fatalf("Dispatch(compose): %v", err)
	}
	if _, ok := goEng.(*compose.ComposeEngine); !ok {
		t.Fatalf("plain Dispatch compose is %T, want *compose.ComposeEngine", goEng)
	}

	// Provider accessor removed → hybrid falls back to the Go engine.
	goLookup := parts.buildLookup
	goLookup.Provider = nil
	fallbackHybrid := enginefactory.NewHybrid(enginefactory.Dispatch, goLookup, pluginLookup, sharedLoader)
	fbEng, err := fallbackHybrid("compose")
	if err != nil {
		t.Fatalf("fallback hybrid(compose): %v", err)
	}
	if _, ok := fbEng.(*compose.ComposeEngine); !ok {
		t.Fatalf("Provider-less hybrid compose routed to %T, want *compose.ComposeEngine (fallback)", fbEng)
	}
}
