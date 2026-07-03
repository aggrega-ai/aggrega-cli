// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestHybridCutoverHelmChartProductionPathByteIdentity is the production-path A/B
// for the helm-chart render+assemble cutover. It builds a hub-wired fixture — a
// Provider(helm-chart) bound to a Plugin advertising BOTH render_node and
// assemble_boundary, whose artifact resolves from the embedded tier — twice
// through the real pipeline.RunPipeline:
//
//   - the Wasm build passes the full catalog lookup, so the in-pipeline hybrid
//     resolves the provider name → provider-Plugin (via compatibleProviders) →
//     exports chain and routes the helm-chart nodes through the wasmengine
//     assembler adapter (per-node sub-chart render AND per-group umbrella
//     assembly in Wasm);
//   - the Go build passes the same lookup with only the ProviderPlugin accessor
//     nil'd, so the hybrid falls back to the built-in Go helm-chart engine.
//
// The ProviderPlugin accessor has no other consumer in the pipeline, so nil'ing
// it changes only the routing decision — every other render input is identical.
// The two builds must therefore produce byte-identical artifacts, proving the
// Wasm render+assemble path reproduces the Go engine across the production spine
// for THREE SOURCED helm-chart nodes (local charts copied verbatim into
// sub-chart file sets) AND the per-dispatch-group umbrella assembly that lists
// every sub-chart as a dependency (drawing each dependency's name and version
// from the vendored chart's Chart.yaml) and scopes per-instance values under
// each node's alias in one values.yaml.
//
// The assertions below on the assembled umbrella (all three dependency aliases,
// exactly one umbrella Chart.yaml) prove the multi-node assembly genuinely ran
// through Wasm rather than degrading to per-node output.
func TestHybridCutoverHelmChartProductionPathByteIdentity(t *testing.T) {
	ctx := context.Background()
	parts := loadCutoverParts(t, "testdata/hybrid-cutover-helmchart")

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

	// Go build: identical inputs, ProviderPlugin accessor nil'd → hybrid falls back.
	goLookup := parts.buildLookup
	goLookup.ProviderPlugin = nil
	goArtifacts := runBuild(goLookup)

	assertByteIdentical(t, goArtifacts, wasmArtifacts)

	joined := concatArtifacts(wasmArtifacts)

	// Prove each sourced node actually rendered through the Wasm build, so the A/B
	// is not silently covering only one path. Each recipe's valuesMapping stamps a
	// distinct image into the umbrella values.yaml, and each vendored chart's
	// values.yaml carries the same marker.
	for _, marker := range []string{"web:1.2.3", "api:2.0", "postgres:16"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("expected marker %q in the Wasm build output (per-node render coverage)", marker)
		}
	}

	// Locate the umbrella Chart.yaml (the boundary-root Chart.yaml, not a
	// sub-chart's under charts/). Exactly one exists — the single dispatch group
	// collapses to one umbrella.
	umbrella := ""
	umbrellaCount := 0
	for path, body := range wasmArtifacts {
		if strings.HasSuffix(path, "/Chart.yaml") && !strings.Contains(path, "/charts/") {
			umbrellaCount++
			umbrella = string(body)
		}
	}
	if umbrellaCount != 1 {
		t.Fatalf("expected exactly one umbrella Chart.yaml, got %d (paths: %v)", umbrellaCount, sortedKeys(wasmArtifacts))
	}

	// Prove the per-group assembly ran: the umbrella lists all three sub-charts as
	// dependencies, each drawing its name/version from the vendored Chart.yaml and
	// aliased to the node's instance id.
	for _, dep := range []string{"name: web-app", "name: api-app", "name: db-app"} {
		if !strings.Contains(umbrella, dep) {
			t.Errorf("expected dependency %q in the assembled umbrella Chart.yaml:\n%s", dep, umbrella)
		}
	}
	for _, alias := range []string{"alias: web", "alias: api", "alias: db"} {
		if !strings.Contains(umbrella, alias) {
			t.Errorf("expected %q in the assembled umbrella Chart.yaml:\n%s", alias, umbrella)
		}
	}

	// Prove the sub-charts nested under the umbrella (render output landed on disk
	// alongside the assembled umbrella).
	for _, sub := range []string{"/charts/web/Chart.yaml", "/charts/api/Chart.yaml", "/charts/db/Chart.yaml"} {
		found := false
		for path := range wasmArtifacts {
			if strings.HasSuffix(path, sub) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected nested sub-chart %q in the Wasm build output (paths: %v)", sub, sortedKeys(wasmArtifacts))
		}
	}
}

// TestHybridCutoverHelmChartRouting proves the routing decision the byte-identity
// build relies on: with the hub provider-Plugin advertising BOTH render_node and
// assemble_boundary, the hybrid returns the ASSEMBLER Wasm adapter for the
// helm-chart provider (not the render-only adapter, and not the Go engine); with
// the ProviderPlugin accessor removed, it returns the Go engine. Asserting both
// directions makes the byte-identity airtight — the two builds took different
// engine paths yet produced identical bytes.
func TestHybridCutoverHelmChartRouting(t *testing.T) {
	parts := loadCutoverParts(t, "testdata/hybrid-cutover-helmchart")

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

	// Hub provider-Plugin wired, advertises assemble_boundary → assembler adapter.
	hybrid := enginefactory.NewHybrid(enginefactory.Dispatch, parts.buildLookup, pluginLookup, sharedLoader)
	eng, err := hybrid("helm-chart")
	if err != nil {
		t.Fatalf("hybrid(helm-chart): %v", err)
	}
	if _, ok := eng.(*wasmengine.AssemblerEngine); !ok {
		t.Fatalf("hub Provider helm-chart routed to %T, want *wasmengine.AssemblerEngine", eng)
	}

	// Plain Go dispatch → in-tree helm-chart engine.
	goEng, err := enginefactory.Dispatch("helm-chart")
	if err != nil {
		t.Fatalf("Dispatch(helm-chart): %v", err)
	}
	if _, ok := goEng.(*helmchart.HelmEngine); !ok {
		t.Fatalf("plain Dispatch helm-chart is %T, want *helmchart.HelmEngine", goEng)
	}

	// ProviderPlugin accessor removed → hybrid falls back to the Go engine.
	goLookup := parts.buildLookup
	goLookup.ProviderPlugin = nil
	fallbackHybrid := enginefactory.NewHybrid(enginefactory.Dispatch, goLookup, pluginLookup, sharedLoader)
	fbEng, err := fallbackHybrid("helm-chart")
	if err != nil {
		t.Fatalf("fallback hybrid(helm-chart): %v", err)
	}
	if _, ok := fbEng.(*helmchart.HelmEngine); !ok {
		t.Fatalf("Provider-less hybrid helm-chart routed to %T, want *helmchart.HelmEngine (fallback)", fbEng)
	}
}
