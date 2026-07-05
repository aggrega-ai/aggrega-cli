// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cutoverParts carries everything needed to drive RunPipeline against the
// hybrid-cutover fixture. The build inputs, lookups, and options are shared
// verbatim between the Wasm build and the Go build so the two differ only in the
// catalog Provider accessor (nil'd for the Go build), never in render input.
type cutoverParts struct {
	inputs       pipeline.Inputs
	buildLookup  resolver.CatalogLookup
	recipeLookup renderer.RecipeLookup
	subAgg       topology.SubAggregateLoader
	opts         pipeline.Options
	cat          *catalog.Catalog
}

// loadCutoverParts loads a hybrid-cutover fixture under baseDir and assembles the
// build inputs exactly as the CLI build does (the triad derivation the
// pipelinetest harness performs), so the two builds below run the real production
// spine. baseDir is parametrized so the helm and compose cutover A/Bs share one
// loader — the fixtures differ only in their catalog content, never in how the
// build inputs are derived.
func loadCutoverParts(t *testing.T, baseDir string) cutoverParts {
	t.Helper()
	releasePath := filepath.Join(baseDir, "release.yaml")
	catalogPath := filepath.Join(baseDir, "catalog")

	cat, err := catalog.LoadAll("", catalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	releaseDir := filepath.Dir(releasePath)
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		t.Fatalf("compose manifest extends: %v", err)
	}
	if err := cat.AssertManifestImplementsConsistency(manifest, manRes.Path); err != nil {
		t.Fatalf("manifest consistency: %v", err)
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, cat.AggregateLookup(), nil)
	if err != nil {
		t.Fatalf("load aggregate: %v", err)
	}

	return cutoverParts{
		inputs: pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggRes.Bytes,
			AggregatePath:     aggRes.Path,
			MappingLibraries:  cat.MappingLibraries,
			CompilerVersion:   "0.1.0",
			WorkspaceConfig:   workspaceconfig.Default(),
		},
		buildLookup:  cat.CatalogLookup(),
		recipeLookup: cat.RecipeLookupWithManifest(manifest),
		subAgg:       cat.SubAggregateLoader(),
		opts:         pipeline.Options{RecipeResolutionVerifier: cat.VerifyRecipeResolution},
		cat:          cat,
	}
}

// TestHybridCutoverProductionPathByteIdentity is the production-path A/B for the
// helm render cutover. It builds a hub-wired fixture — a Provider(helm) bound to
// a render_node-advertising Plugin whose artifact resolves from the embedded
// tier — twice through the real pipeline.RunPipeline:
//
//   - the Wasm build passes the full catalog lookup, so the in-pipeline hybrid
//     resolves the provider name → provider-Plugin (via compatibleProviders) →
//     render_node chain and routes the helm nodes through the wasmengine adapter;
//   - the Go build passes the same lookup with only the ProviderPlugin accessor
//     nil'd, so the hybrid falls back to the built-in Go helm engine.
//
// The ProviderPlugin accessor has no other consumer in the pipeline, so nil'ing
// it changes only the routing decision — every other render input is identical.
// The two builds must therefore produce byte-identical artifacts, proving the
// Wasm render path reproduces the Go engine across the production spine for both
// a SOURCED helm node (a local chart) and a SYNTHESIZED helm node (a workload
// carrying numeric replicas/port, the int→float64 wire-fidelity case).
//
// The routing sub-test proves the Wasm build genuinely exercised the Wasm path
// (not a silent Go fallback that would make the byte-identity a Go-vs-Go no-op):
// the hybrid returns *wasmengine.Engine for the hub Provider, and the Go-forced
// hybrid returns *helm.HelmEngine.
func TestHybridCutoverProductionPathByteIdentity(t *testing.T) {
	ctx := context.Background()
	parts := loadCutoverParts(t, "testdata/hybrid-cutover")

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

	// Wasm build: full lookup → hybrid resolves → wasmengine adapter.
	wasmArtifacts := runBuild(parts.buildLookup)

	// Go build: identical inputs, ProviderPlugin accessor nil'd → hybrid falls back.
	goLookup := parts.buildLookup
	goLookup.ProviderPlugin = nil
	goArtifacts := runBuild(goLookup)

	assertByteIdentical(t, goArtifacts, wasmArtifacts)

	// Prove both input kinds actually rendered through the Wasm build, so the
	// A/B is not silently covering only one path. The sourced chart stamps
	// "websvc:1.2.3"; the synthesized workload stamps "postgres:16".
	joined := concatArtifacts(wasmArtifacts)
	if !strings.Contains(joined, "websvc:1.2.3") {
		t.Errorf("sourced helm node did not render through the Wasm build (no %q in output)", "websvc:1.2.3")
	}
	if !strings.Contains(joined, "postgres:16") {
		t.Errorf("synthesized helm node did not render through the Wasm build (no %q in output)", "postgres:16")
	}
}

// TestHybridCutoverRouting proves the routing decision the byte-identity build
// relies on: with the hub provider-Plugin wired, the hybrid returns the Wasm
// adapter for the helm provider; with the ProviderPlugin accessor removed, it
// returns the Go engine. Asserting both directions makes the byte-identity
// airtight — the two builds took different engine paths yet produced identical bytes.
func TestHybridCutoverRouting(t *testing.T) {
	parts := loadCutoverParts(t, "testdata/hybrid-cutover")

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

	// Hub provider-Plugin wired → Wasm adapter.
	hybrid, _ := enginefactory.NewHybrid(enginefactory.Dispatch, parts.buildLookup, pluginLookup, sharedLoader)
	eng, err := hybrid("helm")
	if err != nil {
		t.Fatalf("hybrid(helm): %v", err)
	}
	if _, ok := eng.(*wasmengine.Engine); !ok {
		t.Fatalf("hub Provider helm routed to %T, want *wasmengine.Engine", eng)
	}

	// Plain Go dispatch → in-tree helm engine.
	goEng, err := enginefactory.Dispatch("helm")
	if err != nil {
		t.Fatalf("Dispatch(helm): %v", err)
	}
	if _, ok := goEng.(*helm.HelmEngine); !ok {
		t.Fatalf("plain Dispatch helm is %T, want *helm.HelmEngine", goEng)
	}

	// ProviderPlugin accessor removed → hybrid falls back to the Go engine.
	goLookup := parts.buildLookup
	goLookup.ProviderPlugin = nil
	fallbackHybrid, _ := enginefactory.NewHybrid(enginefactory.Dispatch, goLookup, pluginLookup, sharedLoader)
	fbEng, err := fallbackHybrid("helm")
	if err != nil {
		t.Fatalf("fallback hybrid(helm): %v", err)
	}
	if _, ok := fbEng.(*helm.HelmEngine); !ok {
		t.Fatalf("Provider-less hybrid helm routed to %T, want *helm.HelmEngine (fallback)", fbEng)
	}
}

// assertByteIdentical fails the test unless oracle and candidate carry the same
// path set and byte-identical content for every path.
func assertByteIdentical(t *testing.T, oracle, candidate map[string][]byte) {
	t.Helper()
	if len(oracle) == 0 {
		t.Fatal("build produced no artifacts; A/B would be vacuous")
	}
	oKeys, cKeys := sortedKeys(oracle), sortedKeys(candidate)
	if strings.Join(oKeys, ",") != strings.Join(cKeys, ",") {
		t.Fatalf("artifact path sets differ:\n go:   %v\n wasm: %v", oKeys, cKeys)
	}
	for _, k := range oKeys {
		// The in-tree Go oracle renders version-less by design: the A/B nil's the
		// candidate provider plugin, which zeroes the provider-version resolver, so
		// the oracle structurally cannot reproduce a resolved plugin version. The
		// Wasm-routed candidate resolves a real pinned plugin and stamps that
		// version as the aggrega.ai/provider-version label. Excluding only that one
		// label line from both sides keeps the comparison a full raw byte-identity
		// check on every other byte — including the aggrega.ai/provider label, which
		// both paths emit — while tolerating the single label the oracle cannot
		// produce. Any other difference is a real wiring defect, not normalized away.
		o := stripProviderVersionLabelLine(oracle[k])
		c := stripProviderVersionLabelLine(candidate[k])
		if string(o) != string(c) {
			t.Errorf("BYTE MISMATCH for %q:\n--- go ---\n%s\n--- wasm ---\n%s", k, o, c)
		} else {
			t.Logf("BYTE-IDENTICAL %q (%d bytes)", k, len(o))
		}
	}
}

// stripProviderVersionLabelLine removes the single aggrega.ai/provider-version
// label line from a rendered manifest so the A/B tolerates the one label the
// in-tree Go oracle cannot reproduce (it renders version-less by design). It
// targets that label alone: the aggrega.ai/provider label and every other byte
// stay in the comparison.
func stripProviderVersionLabelLine(b []byte) []byte {
	lines := bytes.Split(b, []byte("\n"))
	kept := make([][]byte, 0, len(lines))
	for _, ln := range lines {
		if bytes.Contains(ln, []byte("aggrega.ai/provider-version:")) {
			continue
		}
		kept = append(kept, ln)
	}
	return bytes.Join(kept, []byte("\n"))
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func concatArtifacts(m map[string][]byte) string {
	var b strings.Builder
	for _, k := range sortedKeys(m) {
		b.Write(m[k])
		b.WriteByte('\n')
	}
	return b.String()
}
