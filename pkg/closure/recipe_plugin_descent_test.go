// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	registryPlugin = "hub.aggrega.ai/wasm/frp/patch-workloads:1.0.0"
	localPlugin    = "plugins/patch-local.wasm"
)

// recipeWithPipelinePlugin builds a Recipe fixture declaring spec.provider plus a
// spec.pipeline step referencing the given plugin URI, for the descent's RecipeLoader.
func recipeWithPipelinePlugin(t *testing.T, name, provider, pluginURI string) *types.Recipe {
	t.Helper()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  provider: " + provider + "\n  implements: core/svc\n" +
		"  pipeline:\n    - id: patch\n      plugin: " + pluginURI + "\n      function: step\n"
	var r types.Recipe
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal recipe: %v", err)
	}
	return &r
}

// pluginDescentLookup resolves the manifest/aggregate/component/recipe + provider
// URIs the fixture reaches, and the two plugins: a registry-resolved plugin (tier-3)
// and a Tier-1-local plugin, on the wasmPlugins axis ResolveWasmPlugin reads.
func pluginDescentLookup() *fakeLookup {
	ents := map[string]closure.Resolution{
		"hub.aggrega.ai/aggregate/example/app:1.0.0": resolutionOK("1.0.0", "sha256:agg", "tier-3-pull"),
		"hub.aggrega.ai/manifest/example/app:1.0.0":  resolutionOK("1.0.0", "sha256:man", "tier-3-pull"),
		"hub.aggrega.ai/component/core/svc:1.0.0":     resolutionOK("1.0.0", "sha256:svc", "tier-3-pull"),
		recipeURI:                                     resolutionOK("1.0.0", "sha256:recipe", "tier-3-pull"),
		registryProvider:                              resolutionOK("1.0.0", "sha256:compose", "tier-3-pull"),
	}
	plugins := map[string]closure.Resolution{
		registryPlugin: resolutionOK("1.0.0", "sha256:plugin", "tier-3-pull"),
		localPlugin:    resolutionOK("", "sha256:local", "tier-1-catalog"),
	}
	return &fakeLookup{entities: ents, wasmPlugins: plugins}
}

// runPluginDescent computes the base closure for the manifest-bearing Release, then
// runs the pipeline-plugin descent against the supplied loader and lookup, returning
// the augmented closure (or the error).
func runPluginDescent(t *testing.T, lookup *fakeLookup, loader closure.RecipeLoader) (closure.Closure, error) {
	t.Helper()
	rel := loadRelease(t, providerDescentRelease)
	man := loadManifest(t, providerDescentManifest)
	cl, err := closure.ComputeClosure(rel, man, nil, workspaceconfig.Config{}, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	derr := closure.DescendRecipePlugins(&cl, loader, lookup)
	return cl, derr
}

func wasmPluginByURI(cl closure.Closure, uri string) (closure.ResolvedRef, bool) {
	for _, p := range cl.WasmPlugins {
		if p.URI == uri {
			return p, true
		}
	}
	return closure.ResolvedRef{}, false
}

// TestPluginDescent_RegistryResolvedPinned proves a registry-resolved pipeline plugin
// is walked from the recipe's spec.pipeline[].plugin, resolved through the
// ResolveWasmPlugin path, and pinned into the closure's WasmPlugins set with its
// version + digest + plugin hint.
func TestPluginDescent_RegistryResolvedPinned(t *testing.T) {
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{
		recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, registryPlugin),
	}}
	cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
	if err != nil {
		t.Fatalf("plugin descent: %v", err)
	}

	got, ok := wasmPluginByURI(cl, registryPlugin)
	if !ok {
		t.Fatalf("registry-resolved pipeline plugin %q not pinned into the closure", registryPlugin)
	}
	if got.ResolvedVersion != "1.0.0" || got.Digest != "sha256:plugin" {
		t.Errorf("plugin pin = {%q, %q}; want {1.0.0, sha256:plugin}", got.ResolvedVersion, got.Digest)
	}
	if got.KindHint != "wasm_plugin" {
		t.Errorf("plugin KindHint = %q; want wasm_plugin", got.KindHint)
	}
	if len(got.ReferencedFrom) != 1 || got.ReferencedFrom[0] != recipeURI {
		t.Errorf("plugin ReferencedFrom = %v; want [%s]", got.ReferencedFrom, recipeURI)
	}
}

// TestPluginDescent_Tier1LocalNotPinned proves a Tier-1-local pipeline plugin is NOT
// pinned — it resolves at build through its local path, so pinning it would diverge a
// local-authoring build from its pre-change behaviour.
func TestPluginDescent_Tier1LocalNotPinned(t *testing.T) {
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{
		recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, localPlugin),
	}}
	cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
	if err != nil {
		t.Fatalf("plugin descent: %v", err)
	}
	if _, ok := wasmPluginByURI(cl, localPlugin); ok {
		t.Fatalf("Tier-1-local plugin %q must not be pinned; it resolves locally at build", localPlugin)
	}
}

// TestPluginDescent_NotFoundSkipped proves a plugin that resolves through no tier is
// skipped rather than halting the pull (a local-path plugin the lock-time lookup does
// not see resolves at build; the build-time loader gate halts a genuinely-absent one).
func TestPluginDescent_NotFoundSkipped(t *testing.T) {
	const unknownPlugin = "hub.aggrega.ai/wasm/frp/unknown:1.0.0"
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{
		recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, unknownPlugin),
	}}
	cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
	if err != nil {
		t.Fatalf("plugin descent must not halt on NotFound: %v", err)
	}
	if _, ok := wasmPluginByURI(cl, unknownPlugin); ok {
		t.Fatalf("unresolved plugin %q must not be pinned", unknownPlugin)
	}
}

// TestPluginDescent_Deterministic descends the same Release twice and asserts the
// pinned WasmPlugins set is byte-identical (Invariant 8): the plugin pin sorts
// deterministically regardless of descent order.
func TestPluginDescent_Deterministic(t *testing.T) {
	build := func() closure.Closure {
		loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, registryPlugin),
		}}
		cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
		if err != nil {
			t.Fatalf("plugin descent: %v", err)
		}
		return cl
	}
	first := build()
	second := build()
	if !reflect.DeepEqual(first.WasmPlugins, second.WasmPlugins) {
		t.Fatalf("plugin descent is non-deterministic:\n first=%#v\nsecond=%#v", first.WasmPlugins, second.WasmPlugins)
	}
}

// TestPluginDescent_NilLoaderNoop confirms a nil loader leaves the closure untouched.
func TestPluginDescent_NilLoaderNoop(t *testing.T) {
	cl, err := runPluginDescent(t, pluginDescentLookup(), nil)
	if err != nil {
		t.Fatalf("nil-loader descent: %v", err)
	}
	if _, ok := wasmPluginByURI(cl, registryPlugin); ok {
		t.Fatalf("nil-loader descent must not pin a plugin")
	}
}
