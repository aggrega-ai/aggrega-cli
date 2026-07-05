// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	helmchart "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// This runner generalizes the fixture-based host-vs-guest byte-identity A/B
// (hybrid_cutover*_integration_test.go) to EVERY real example under
// aggrega-docs/language-reference/examples against the REAL sibling hub catalog.
// It renders each renderable (example, release) pair BOTH ways through the same
// production spine — pipeline.RunPipeline — differing only in the catalog
// Provider accessor:
//
//   - the Wasm build passes the full catalog lookup, so the in-pipeline hybrid
//     resolves the provider name → provider-Plugin → render_node chain and routes
//     the provider nodes through the wasmengine adapter;
//   - the Go build passes the same lookup with only the ProviderPlugin accessor
//     nil'd, so the hybrid falls back to the built-in Go engine.
//
// The two builds must produce byte-identical artifacts modulo the single
// aggrega.ai/provider-version label the version-less Go oracle cannot reproduce
// (stripped by stripProviderVersionLabelLine, defined in the hybrid_cutover
// harness and reused here). Every other byte — including aggrega.ai/provider —
// stays in the comparison.
//
// It runs behind the `integration` build tag because it reaches out of the
// aggrega-cli module into the sibling aggrega-docs and aggrega-hub checkouts;
// the default suite stays hermetic to the module's own testdata. Under the tag
// in the umbrella workspace it RUNS (never skips): a missing corpus is a hard
// failure, not a silent skip.

// exampleReleasesRoot is the corpus location relative to this package directory
// (the test working directory): pipeline → compiler → internal → aggrega-cli →
// aggrega-umbrella, then down into the docs checkout.
const exampleReleasesRoot = "../../../../aggrega-docs/language-reference/examples"

// expectedNonRendering names release files that demonstrate a loud build-halt
// guardrail and therefore render nothing. They are SKIPPED from the A/B but the
// skip is asserted (the build must genuinely fail) and logged with its reason —
// never a silent coverage gap.
var expectedNonRendering = map[string]bool{
	"release-negative.yaml": true,
}

// exampleParts carries the build inputs and lookups derived for one
// (example, release) pair, assembled through the same resolution the CLI build
// performs. The two builds share every field verbatim except the catalog
// Provider accessor (nil'd for the Go build), so they differ only in the routing
// decision, never in render input.
type exampleParts struct {
	inputs       pipeline.Inputs
	buildLookup  resolver.CatalogLookup
	recipeLookup renderer.RecipeLookup
	subAgg       topology.SubAggregateLoader
	opts         pipeline.Options
}

// TestExampleCorpusGoWasmByteIdentity is the DELETE-safety evidence basis: it
// proves the Wasm render path reproduces the in-tree Go engine byte-for-byte
// across the whole real example corpus. It emits a per-(example, release)
// PASS / SKIP line and a covered/skipped tally, and each byte comparison logs
// BYTE-IDENTICAL per artifact path via the shared assertByteIdentical.
func TestExampleCorpusGoWasmByteIdentity(t *testing.T) {
	corpus, err := filepath.Abs(exampleReleasesRoot)
	if err != nil {
		t.Fatalf("resolve corpus root: %v", err)
	}
	if fi, statErr := os.Stat(corpus); statErr != nil || !fi.IsDir() {
		t.Fatalf("example corpus not found at %s (this integration runner requires the sibling aggrega-docs checkout; it must RUN, not skip): %v", corpus, statErr)
	}

	pairs := enumerateReleases(t, corpus)
	if len(pairs) == 0 {
		t.Fatalf("no (example, release) pairs discovered under %s", corpus)
	}

	var covered, skipped int
	for _, p := range pairs {
		name := p.example + "/" + p.releaseFile
		ok := t.Run(name, func(t *testing.T) {
			if expectedNonRendering[p.releaseFile] {
				assertExpectedNonRendering(t, corpus, p)
				t.Logf("SKIP %s: expected-fail guardrail demonstration — the build halts and renders nothing", name)
				return
			}
			runExampleParity(t, corpus, p)
			t.Logf("PASS %s", name)
		})
		switch {
		case !ok:
			// A failing subtest already reported its diagnostic (including any
			// verbatim byte-mismatch diff from assertByteIdentical).
		case expectedNonRendering[p.releaseFile]:
			skipped++
		default:
			covered++
		}
	}

	t.Logf("corpus parity tally: covered=%d skipped=%d total=%d", covered, skipped, len(pairs))
}

// TestExampleCorpusWasmDeterminism proves the Deterministic Output Invariant on
// the corpus: one example's Wasm build is byte-identical across two independent
// pipeline runs of the same input. mariadb-adminer/release-helm-bitnami.yaml is
// chosen because it renders a rich multi-file helm output from a sourced chart.
func TestExampleCorpusWasmDeterminism(t *testing.T) {
	corpus, err := filepath.Abs(exampleReleasesRoot)
	if err != nil {
		t.Fatalf("resolve corpus root: %v", err)
	}
	p := releasePair{example: "mariadb-adminer", releaseFile: "release-helm-bitnami.yaml"}
	if _, statErr := os.Stat(filepath.Join(corpus, p.example, p.releaseFile)); statErr != nil {
		t.Fatalf("determinism example %s/%s not present: %v", p.example, p.releaseFile, statErr)
	}

	restore := chdir(t, filepath.Join(corpus, p.example))
	defer restore()

	parts, err := loadExampleParts(corpus, p)
	if err != nil {
		t.Fatalf("load parts for %s/%s: %v", p.example, p.releaseFile, err)
	}
	first, err := render(parts, parts.buildLookup)
	if err != nil {
		t.Fatalf("first Wasm render: %v", err)
	}
	second, err := render(parts, parts.buildLookup)
	if err != nil {
		t.Fatalf("second Wasm render: %v", err)
	}
	assertByteIdentical(t, first, second)
	t.Logf("DETERMINISM OK %s/%s: Wasm build byte-identical across two runs (%d artifacts)", p.example, p.releaseFile, len(first))
}

// runExampleParity renders one renderable (example, release) both ways and
// asserts byte-identity, then proves the Wasm build genuinely took the Wasm
// route for the providers the example serves (so the A/B is not a vacuous
// Go-vs-Go no-op).
func runExampleParity(t *testing.T, corpus string, p releasePair) {
	restore := chdir(t, filepath.Join(corpus, p.example))
	defer restore()

	parts, err := loadExampleParts(corpus, p)
	if err != nil {
		t.Fatalf("resolve/load %s/%s: %v", p.example, p.releaseFile, err)
	}

	// Wasm build: full lookup → hybrid resolves → wasmengine adapter.
	wasmArtifacts, err := render(parts, parts.buildLookup)
	if err != nil {
		t.Fatalf("Wasm build %s/%s: %v", p.example, p.releaseFile, err)
	}

	// Go build: identical inputs, ProviderPlugin accessor nil'd → hybrid falls back.
	goLookup := parts.buildLookup
	goLookup.ProviderPlugin = nil
	goArtifacts, err := render(parts, goLookup)
	if err != nil {
		t.Fatalf("Go build %s/%s: %v", p.example, p.releaseFile, err)
	}

	assertByteIdentical(t, goArtifacts, wasmArtifacts)
	assertWasmRoute(t, parts.buildLookup)
}

// assertWasmRoute mirrors the routing assertion in the hybrid_cutover harness
// over the example's own catalog lookup: for every known provider the lookup
// serves a provider-Plugin for, the hub-wired hybrid returns a wasmengine
// adapter, and the ProviderPlugin-nil'd hybrid falls back to the in-tree Go
// engine. It constructs the plugin lookup the way RunPipeline does internally,
// so the routing it observes is the routing the byte-identity build took. A
// failure to match any provider fails loudly — a vacuous routing proof is
// itself a defect for this evidence runner.
func assertWasmRoute(t *testing.T, buildLookup resolver.CatalogLookup) {
	t.Helper()

	cascade := (pluginpath.Resolver{}).Resolve
	pluginLookup := pluginpath.EntityLookup(func(uri string) (string, *pluginpath.PluginCapabilityClaim, *pluginpath.PluginRuntimeLimits, bool) {
		entity, ok := buildLookup.Plugin(uri)
		if !ok {
			return "", nil, nil, false
		}
		return entity.Spec.Plugin, resolver.PluginCapabilityClaim(entity), resolver.PluginRuntimeLimits(entity), true
	}, cascade)

	sharedLoader, closeLoader := wasm.NewCachingLoader(nil)
	defer func() { _ = closeLoader() }()

	goLookup := buildLookup
	goLookup.ProviderPlugin = nil

	matched := 0
	for _, provider := range []string{"helm", "compose", "helm-chart"} {
		if _, ok, provErr := buildLookup.ProviderPlugin(provider); provErr != nil || !ok {
			continue
		}
		matched++

		hybrid, _ := enginefactory.NewHybrid(enginefactory.Dispatch, buildLookup, pluginLookup, sharedLoader)
		eng, err := hybrid(provider)
		if err != nil {
			t.Fatalf("hybrid(%q): %v", provider, err)
		}
		switch eng.(type) {
		case *wasmengine.Engine, *wasmengine.AssemblerEngine:
			// Wasm route taken — as required.
		default:
			t.Fatalf("hub Provider %q routed to %T, want a *wasmengine.Engine or *wasmengine.AssemblerEngine (Wasm build did not take the Wasm route)", provider, eng)
		}

		fallbackHybrid, _ := enginefactory.NewHybrid(enginefactory.Dispatch, goLookup, pluginLookup, sharedLoader)
		fbEng, err := fallbackHybrid(provider)
		if err != nil {
			t.Fatalf("fallback hybrid(%q): %v", provider, err)
		}
		switch fbEng.(type) {
		case *helm.HelmEngine, *compose.ComposeEngine, *helmchart.HelmEngine:
			// Go fallback — as required, proving the A/B took two different paths.
		default:
			t.Fatalf("Provider-less hybrid %q routed to %T, want an in-tree Go engine (fallback)", provider, fbEng)
		}
	}

	if matched == 0 {
		t.Fatalf("example lookup serves no known provider (helm/compose/helm-chart); the Wasm-route proof would be vacuous")
	}
}

// assertExpectedNonRendering confirms an expected-fail release genuinely halts:
// either resolution or the render must error. A build that unexpectedly succeeds
// is a surprise and fails the test rather than being silently tolerated.
func assertExpectedNonRendering(t *testing.T, corpus string, p releasePair) {
	t.Helper()
	restore := chdir(t, filepath.Join(corpus, p.example))
	defer restore()

	parts, err := loadExampleParts(corpus, p)
	if err != nil {
		return // halted during resolution — the guardrail fired.
	}
	if _, err := render(parts, parts.buildLookup); err != nil {
		return // halted during render — the guardrail fired.
	}
	t.Fatalf("expected-fail release %s/%s rendered successfully; the guardrail did not fire", p.example, p.releaseFile)
}

// render runs the production pipeline once with the supplied catalog lookup and
// returns the rendered artifact map. The lookup is the only knob the A/B turns:
// the full lookup routes to Wasm; the ProviderPlugin-nil'd lookup falls back to
// the Go engine.
func render(parts exampleParts, lookup resolver.CatalogLookup) (map[string][]byte, error) {
	res, err := pipeline.RunPipeline(
		context.Background(), parts.inputs, lookup, parts.recipeLookup, parts.subAgg,
		enginefactory.Dispatch, parts.opts,
	)
	if err != nil {
		return nil, err
	}
	return res.Artifacts, nil
}

// loadExampleParts reproduces the CLI build's catalog resolution for one
// (example, release) pair. It MUST be called with the working directory set to
// the example directory (see chdir) so the workspace-config walk, the catalog
// search stack, and the lockfile/cache paths anchor exactly as the CLI's do
// under `aggrega build` run from the example directory. It resolves the catalog
// through the real front door — workspace-config resolution, the catalog-dir
// search stack (the paired catalog-<P>/ prepended when present), the lazy
// catalog view keyed to the example's lockfile pins — and assembles the same
// pipeline inputs dispatchBuild does.
func loadExampleParts(corpus string, p releasePair) (exampleParts, error) {
	releaseFile := p.releaseFile
	releaseDir := "."

	rw, err := workspaceconfig.Resolve("", "")
	if err != nil {
		return exampleParts{}, err
	}
	wsCfg := rw.Config()
	wsRoot := rw.Root()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()

	// Catalog discovery rule 1: a paired catalog-<token>/ beside release-<token>.yaml
	// is prepended to the search stack (first-win). Rules 2/3 (config catalog_dirs
	// or fallback) are carried by resolveCatalogDirs over the resolved config.
	var flagDirs []string
	if token := releaseToken(releaseFile); token != "" {
		paired := "catalog-" + token
		if fi, statErr := os.Stat(paired); statErr == nil && fi.IsDir() {
			flagDirs = []string{paired}
		}
	}
	dirs := resolveCatalogSearchStack(flagDirs, wsCfg)

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releaseFile))
	if err != nil {
		return exampleParts{}, err
	}

	lazy := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	if lf != nil {
		lazy.SetPins(catalog.PinsFromLockfile(lf))
	}

	rel, err := resolver.LoadRelease(releaseFile)
	if err != nil {
		return exampleParts{}, err
	}
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return exampleParts{}, err
	}
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		return exampleParts{}, err
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, lazy.AggregateLookup(), nil)
	if err != nil {
		return exampleParts{}, err
	}

	return exampleParts{
		inputs: pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggRes.Bytes,
			AggregatePath:     aggRes.Path,
			WorkspaceConfig:   wsCfg,
			Lockfile:          lf,
			WorkspaceRoot:     wsRoot,
			MappingLibraries:  lazy.MappingLibraries(),
			CompilerVersion:   "0.1.0",
		},
		buildLookup:  lazy.AsCatalogLookup(),
		recipeLookup: lazy.RecipeLookupWithManifest(manifest),
		subAgg:       lazy.SubAggregateLoader(),
		opts:         pipeline.Options{RecipeResolutionVerifier: lazy.VerifyRecipeResolution},
	}, nil
}

// resolveCatalogSearchStack replicates the CLI's resolveCatalogDirs: the paired
// catalog flag dirs (anchored to the working directory) PREPENDED to the
// workspace config's catalog_dirs, falling back to the working directory when
// neither is set. The list is a first-win precedence stack.
func resolveCatalogSearchStack(flagDirs []string, cfg workspaceconfig.Config) []string {
	dirs := make([]string, 0, len(flagDirs)+len(cfg.Workspace.CatalogDirs))
	for _, d := range flagDirs {
		if abs, err := filepath.Abs(d); err == nil {
			dirs = append(dirs, abs)
		} else {
			dirs = append(dirs, d)
		}
	}
	dirs = append(dirs, cfg.Workspace.CatalogDirs...)
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	return dirs
}

// releaseToken strips the release- prefix and .yaml suffix to recover the <P>
// discovery token. A bare release.yaml yields "".
func releaseToken(releaseFile string) string {
	token := strings.TrimSuffix(releaseFile, ".yaml")
	token = strings.TrimPrefix(token, "release")
	token = strings.TrimPrefix(token, "-")
	return token
}

// releasePair identifies one (example directory, release file) build target.
type releasePair struct {
	example     string
	releaseFile string
}

// enumerateReleases walks the corpus and collects every (example, release) pair
// the way build-release.sh does: for each example directory, every file matching
// release.yaml or release-*.yaml. The result is sorted for deterministic
// per-run ordering.
func enumerateReleases(t *testing.T, corpus string) []releasePair {
	t.Helper()
	entries, err := os.ReadDir(corpus)
	if err != nil {
		t.Fatalf("read corpus dir %s: %v", corpus, err)
	}
	var pairs []releasePair
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		exampleDir := filepath.Join(corpus, e.Name())
		files, err := os.ReadDir(exampleDir)
		if err != nil {
			t.Fatalf("read example dir %s: %v", exampleDir, err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			if name == "release.yaml" || (strings.HasPrefix(name, "release-") && strings.HasSuffix(name, ".yaml")) {
				pairs = append(pairs, releasePair{example: e.Name(), releaseFile: name})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].example != pairs[j].example {
			return pairs[i].example < pairs[j].example
		}
		return pairs[i].releaseFile < pairs[j].releaseFile
	})
	return pairs
}

// chdir switches the working directory to dir and returns a restore function.
// The runner is serial (no t.Parallel), so a process-global directory change is
// safe here; the restore runs on return, including on t.Fatalf via the caller's
// defer. This reproduces the CLI's execution context: `aggrega build` runs with
// the working directory set to the example directory.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd %s: %v", orig, err)
		}
	}
}
