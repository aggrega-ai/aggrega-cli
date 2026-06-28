// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	// registryPluginEntity is the WasmPlugin ENTITY URI a recipe pipeline step
	// references; registryPluginArtifact is the .wasm artifact the entity's
	// spec.plugin names.
	registryPluginEntity   = "core/frp/patch-workloads:1.0.0"
	registryPluginArtifact = "oci://hub.aggrega.ai/wasm/frp/patch-workloads@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	localPluginEntity      = "local/frp/patch-local:1.0.0"
	localPluginArtifact    = "plugins/patch-local.wasm"
)

// wasmPluginEntity builds a WasmPlugin entity body whose spec.plugin is the given
// artifact reference, for the descent's RecipeLoader.LoadWasmPlugin.
func wasmPluginEntity(t *testing.T, name, artifactURI string) *types.WasmPlugin {
	t.Helper()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: WasmPlugin\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  plugin: \"" + artifactURI + "\"\n  pluginTypes:\n    - frp_step\n  abiVersion: v1alpha1\n"
	var wp types.WasmPlugin
	if err := yaml.Unmarshal([]byte(src), &wp); err != nil {
		t.Fatalf("unmarshal wasm plugin: %v", err)
	}
	return &wp
}

// recipeWithPipelinePlugin builds a Recipe fixture declaring spec.provider plus a
// spec.pipeline step referencing the given WasmPlugin entity URI, for the descent's
// RecipeLoader.
func recipeWithPipelinePlugin(t *testing.T, name, provider, pluginEntityURI string) *types.Recipe {
	t.Helper()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  provider: " + provider + "\n  implements: core/svc\n" +
		"  pipeline:\n    - id: patch\n      plugin: " + pluginEntityURI + "\n      function: step\n"
	var r types.Recipe
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("unmarshal recipe: %v", err)
	}
	return &r
}

// featureRecipeWithPipelinePlugin builds a FeatureRecipe fixture declaring
// spec.provider plus a spec.pipeline step referencing the given WasmPlugin entity
// URI, for the descent's RecipeLoader. Mirrors recipeWithPipelinePlugin on the
// FeatureRecipe axis.
func featureRecipeWithPipelinePlugin(t *testing.T, name, provider, pluginEntityURI string) *types.FeatureRecipe {
	t.Helper()
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: FeatureRecipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  provider: " + provider + "\n  implements: core/cred\n" +
		"  pipeline:\n    - id: patch\n      plugin: " + pluginEntityURI + "\n      function: step\n"
	var fr types.FeatureRecipe
	if err := yaml.Unmarshal([]byte(src), &fr); err != nil {
		t.Fatalf("unmarshal feature recipe: %v", err)
	}
	return &fr
}

// pluginDescentLookup resolves the manifest/aggregate/component/recipe + provider
// URIs the fixture reaches, the WasmPlugin ENTITY URIs (on the catalog-entity axis),
// and the .wasm artifacts the entities' spec.plugin names (on the wasmPlugins axis
// ResolveWasmPlugin reads): a registry-resolved entity+artifact (tier-3) and a
// Tier-1-local pair.
func pluginDescentLookup() *fakeLookup {
	ents := map[string]closure.Resolution{
		"hub.aggrega.ai/aggregate/example/app:1.0.0": resolutionOK("1.0.0", "sha256:agg", "tier-3-pull"),
		"hub.aggrega.ai/manifest/example/app:1.0.0":  resolutionOK("1.0.0", "sha256:man", "tier-3-pull"),
		"hub.aggrega.ai/component/core/svc:1.0.0":    resolutionOK("1.0.0", "sha256:svc", "tier-3-pull"),
		recipeURI:            resolutionOK("1.0.0", "sha256:recipe", "tier-3-pull"),
		registryProvider:     resolutionOK("1.0.0", "sha256:compose", "tier-3-pull"),
		registryPluginEntity: resolutionOK("1.0.0", "sha256:plugin-entity", "tier-3-pull"),
		localPluginEntity:    resolutionOK("", "sha256:local-entity", "tier-1-catalog"),
	}
	plugins := map[string]closure.Resolution{
		registryPluginArtifact: resolutionOK("1.0.0", "sha256:plugin", "tier-3-pull"),
		localPluginArtifact:    resolutionOK("", "sha256:local", "tier-1-catalog"),
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

// TestPluginDescent_RegistryResolvedPinned proves a registry-resolved pipeline
// plugin entity is walked from the recipe's spec.pipeline[].plugin, the entity is
// resolved through ResolveCatalogEntity and pinned into the closure's Entities set,
// and its spec.plugin .wasm artifact is resolved through ResolveWasmPlugin and
// pinned into the closure's WasmPlugins set — the entity→.wasm round-trip.
func TestPluginDescent_RegistryResolvedPinned(t *testing.T) {
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, registryPluginEntity),
		},
		wasmPlugins: map[string]*types.WasmPlugin{
			registryPluginEntity: wasmPluginEntity(t, "patch-workloads", registryPluginArtifact),
		},
	}
	cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
	if err != nil {
		t.Fatalf("plugin descent: %v", err)
	}

	// The ENTITY is pinned on the catalog-entity axis with KindHint wasm_plugin.
	gotEnt, ok := entityByURI(cl, registryPluginEntity)
	if !ok {
		t.Fatalf("registry-resolved plugin ENTITY %q not pinned into the closure entities", registryPluginEntity)
	}
	if gotEnt.ResolvedVersion != "1.0.0" || gotEnt.Digest != "sha256:plugin-entity" {
		t.Errorf("entity pin = {%q, %q}; want {1.0.0, sha256:plugin-entity}", gotEnt.ResolvedVersion, gotEnt.Digest)
	}
	if gotEnt.KindHint != "wasm_plugin" {
		t.Errorf("entity KindHint = %q; want wasm_plugin", gotEnt.KindHint)
	}
	if len(gotEnt.ReferencedFrom) != 1 || gotEnt.ReferencedFrom[0] != recipeURI {
		t.Errorf("entity ReferencedFrom = %v; want [%s]", gotEnt.ReferencedFrom, recipeURI)
	}

	// The .wasm ARTIFACT is pinned on the plugin axis, referenced from its entity.
	gotArt, ok := wasmPluginByURI(cl, registryPluginArtifact)
	if !ok {
		t.Fatalf("registry-resolved plugin ARTIFACT %q not pinned into the closure wasm_plugins", registryPluginArtifact)
	}
	if gotArt.ResolvedVersion != "1.0.0" || gotArt.Digest != "sha256:plugin" {
		t.Errorf("artifact pin = {%q, %q}; want {1.0.0, sha256:plugin}", gotArt.ResolvedVersion, gotArt.Digest)
	}
	if gotArt.KindHint != "wasm_plugin" {
		t.Errorf("artifact KindHint = %q; want wasm_plugin", gotArt.KindHint)
	}
	if len(gotArt.ReferencedFrom) != 1 || gotArt.ReferencedFrom[0] != registryPluginEntity {
		t.Errorf("artifact ReferencedFrom = %v; want [%s]", gotArt.ReferencedFrom, registryPluginEntity)
	}
}

// TestPluginDescent_Tier1LocalNotPinned proves a Tier-1-local pipeline plugin entity
// is NOT pinned — it resolves at build through the local catalog walk, so pinning it
// would diverge a local-authoring build from its pre-change behaviour.
func TestPluginDescent_Tier1LocalNotPinned(t *testing.T) {
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, localPluginEntity),
		},
		wasmPlugins: map[string]*types.WasmPlugin{
			localPluginEntity: wasmPluginEntity(t, "patch-local", localPluginArtifact),
		},
	}
	cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
	if err != nil {
		t.Fatalf("plugin descent: %v", err)
	}
	if _, ok := entityByURI(cl, localPluginEntity); ok {
		t.Fatalf("Tier-1-local plugin entity %q must not be pinned; it resolves locally at build", localPluginEntity)
	}
	if _, ok := wasmPluginByURI(cl, localPluginArtifact); ok {
		t.Fatalf("Tier-1-local plugin artifact %q must not be pinned", localPluginArtifact)
	}
}

// TestPluginDescent_NotFoundSkipped proves a plugin entity that resolves through no
// tier is skipped rather than halting the pull (a local-path entity the lock-time
// lookup does not see resolves at build; the build-time loader gate halts a
// genuinely-absent one).
func TestPluginDescent_NotFoundSkipped(t *testing.T) {
	const unknownEntity = "core/frp/unknown:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, unknownEntity),
		},
	}
	cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
	if err != nil {
		t.Fatalf("plugin descent must not halt on NotFound: %v", err)
	}
	if _, ok := entityByURI(cl, unknownEntity); ok {
		t.Fatalf("unresolved plugin entity %q must not be pinned", unknownEntity)
	}
}

// TestPluginDescent_Deterministic descends the same Release twice and asserts the
// pinned Entities + WasmPlugins sets are byte-identical (Invariant 8): the pins sort
// deterministically regardless of descent order.
func TestPluginDescent_Deterministic(t *testing.T) {
	build := func() closure.Closure {
		loader := &fakeRecipeLoader{
			recipes: map[string]*types.Recipe{
				recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, registryPluginEntity),
			},
			wasmPlugins: map[string]*types.WasmPlugin{
				registryPluginEntity: wasmPluginEntity(t, "patch-workloads", registryPluginArtifact),
			},
		}
		cl, err := runPluginDescent(t, pluginDescentLookup(), loader)
		if err != nil {
			t.Fatalf("plugin descent: %v", err)
		}
		return cl
	}
	first := build()
	second := build()
	if !reflect.DeepEqual(first.WasmPlugins, second.WasmPlugins) {
		t.Fatalf("plugin descent is non-deterministic (wasm_plugins):\n first=%#v\nsecond=%#v", first.WasmPlugins, second.WasmPlugins)
	}
	if !reflect.DeepEqual(first.Entities, second.Entities) {
		t.Fatalf("plugin descent is non-deterministic (entities):\n first=%#v\nsecond=%#v", first.Entities, second.Entities)
	}
}

// TestPluginDescent_NilLoaderNoop confirms a nil loader leaves the closure untouched.
func TestPluginDescent_NilLoaderNoop(t *testing.T) {
	cl, err := runPluginDescent(t, pluginDescentLookup(), nil)
	if err != nil {
		t.Fatalf("nil-loader descent: %v", err)
	}
	if _, ok := wasmPluginByURI(cl, registryPluginArtifact); ok {
		t.Fatalf("nil-loader descent must not pin a plugin")
	}
}

const (
	frRecipeURI       = "hub.aggrega.ai/featurerecipe/example/cred:1.0.0"
	frPluginEntity    = "core/frp/patch-credential:1.0.0"
	frPluginArtifact  = "oci://hub.aggrega.ai/wasm/frp/patch-credential@sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// featureRecipeDescentManifest maps a Feature type to a FeatureRecipe URI so the
// closure carries a FeatureRecipe entity (via the features-mapping value) whose
// pipeline plugins the descent reads.
const featureRecipeDescentManifest = `
apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app
  version: 1.0.0
spec:
  mappings:
    features:
      type:
        hub.aggrega.ai/feature/core/cred:1.0.0: ` + frRecipeURI + `
`

// featureRecipeDescentLookup resolves the manifest/aggregate/feature/feature_recipe
// URIs the fixture reaches plus the registry-resolved FeatureRecipe pipeline plugin
// entity and its .wasm artifact.
func featureRecipeDescentLookup() *fakeLookup {
	ents := map[string]closure.Resolution{
		"hub.aggrega.ai/aggregate/example/app:1.0.0": resolutionOK("1.0.0", "sha256:agg", "tier-3-pull"),
		"hub.aggrega.ai/manifest/example/app:1.0.0":  resolutionOK("1.0.0", "sha256:man", "tier-3-pull"),
		"hub.aggrega.ai/feature/core/cred:1.0.0":     resolutionOK("1.0.0", "sha256:cred", "tier-3-pull"),
		frRecipeURI:    resolutionOK("1.0.0", "sha256:fr", "tier-3-pull"),
		frPluginEntity: resolutionOK("1.0.0", "sha256:frplugin-entity", "tier-3-pull"),
	}
	plugins := map[string]closure.Resolution{
		frPluginArtifact: resolutionOK("1.0.0", "sha256:frplugin", "tier-3-pull"),
	}
	return &fakeLookup{entities: ents, wasmPlugins: plugins}
}

// TestPluginDescent_FeatureRecipePinned proves a registry-resolved FeatureRecipe
// pipeline plugin entity is walked from the FeatureRecipe's spec.pipeline[].plugin,
// the entity is pinned on the catalog-entity axis, and its spec.plugin .wasm artifact
// is pinned on the plugin axis with FeatureRecipe referenced-from provenance.
func TestPluginDescent_FeatureRecipePinned(t *testing.T) {
	loader := &fakeRecipeLoader{
		features: map[string]*types.FeatureRecipe{
			frRecipeURI: featureRecipeWithPipelinePlugin(t, "cred", registryProvider, frPluginEntity),
		},
		wasmPlugins: map[string]*types.WasmPlugin{
			frPluginEntity: wasmPluginEntity(t, "patch-credential", frPluginArtifact),
		},
	}
	rel := loadRelease(t, providerDescentRelease)
	man := loadManifest(t, featureRecipeDescentManifest)
	lookup := featureRecipeDescentLookup()
	cl, err := closure.ComputeClosure(rel, man, nil, workspaceconfig.Config{}, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if err := closure.DescendRecipePlugins(&cl, loader, lookup); err != nil {
		t.Fatalf("plugin descent: %v", err)
	}

	gotEnt, ok := entityByURI(cl, frPluginEntity)
	if !ok {
		t.Fatalf("registry-resolved FeatureRecipe plugin ENTITY %q not pinned", frPluginEntity)
	}
	if gotEnt.KindHint != "wasm_plugin" {
		t.Errorf("entity KindHint = %q; want wasm_plugin", gotEnt.KindHint)
	}
	if len(gotEnt.ReferencedFrom) != 1 || gotEnt.ReferencedFrom[0] != frRecipeURI {
		t.Errorf("entity ReferencedFrom = %v; want [%s]", gotEnt.ReferencedFrom, frRecipeURI)
	}

	gotArt, ok := wasmPluginByURI(cl, frPluginArtifact)
	if !ok {
		t.Fatalf("registry-resolved FeatureRecipe plugin ARTIFACT %q not pinned", frPluginArtifact)
	}
	if gotArt.ResolvedVersion != "1.0.0" || gotArt.Digest != "sha256:frplugin" {
		t.Errorf("artifact pin = {%q, %q}; want {1.0.0, sha256:frplugin}", gotArt.ResolvedVersion, gotArt.Digest)
	}
	if gotArt.KindHint != "wasm_plugin" {
		t.Errorf("artifact KindHint = %q; want wasm_plugin", gotArt.KindHint)
	}
	if len(gotArt.ReferencedFrom) != 1 || gotArt.ReferencedFrom[0] != frPluginEntity {
		t.Errorf("artifact ReferencedFrom = %v; want [%s]", gotArt.ReferencedFrom, frPluginEntity)
	}
}

// TestPluginDescent_WorkspaceSocketEntityArtifactPinned proves a workspace plugin
// socket — a WasmPlugin entity ComputeClosure pinned on the entity axis — is descended
// to its spec.plugin .wasm artifact at pull time even though no Recipe references it.
func TestPluginDescent_WorkspaceSocketEntityArtifactPinned(t *testing.T) {
	const socketEntity = "core/policy-validator/baseline:1.0.0"
	const socketArtifact = "oci://hub.aggrega.ai/wasm/policy-validator/baseline@sha256:3333333333333333333333333333333333333333333333333333333333333333"

	lookup := pluginDescentLookup()
	lookup.entities[socketEntity] = resolutionOK("1.0.0", "sha256:socket-entity", "tier-3-pull")
	lookup.wasmPlugins[socketArtifact] = resolutionOK("1.0.0", "sha256:socket-artifact", "tier-3-pull")

	loader := &fakeRecipeLoader{
		// The provider-descent manifest carries this recipe (no pipeline); the
		// descent walks every recipe entity, so the loader must serve it.
		recipes: map[string]*types.Recipe{
			recipeURI: recipeWithPipelinePlugin(t, "svc", registryProvider, ""),
		},
		wasmPlugins: map[string]*types.WasmPlugin{
			socketEntity: wasmPluginEntity(t, "baseline", socketArtifact),
		},
	}

	rel := loadRelease(t, providerDescentRelease)
	man := loadManifest(t, providerDescentManifest)
	cfg := workspaceconfig.Config{}
	cfg.Plugins.Policy = []workspaceconfig.PluginRef{{URI: socketEntity}}
	cl, err := closure.ComputeClosure(rel, man, nil, cfg, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	// ComputeClosure pinned the entity (entity axis), not the artifact.
	if _, ok := entityByURI(cl, socketEntity); !ok {
		t.Fatalf("workspace socket entity %q not pinned by ComputeClosure", socketEntity)
	}
	if _, ok := wasmPluginByURI(cl, socketArtifact); ok {
		t.Fatalf("ComputeClosure must not pin the artifact; that is the pull-time descent's job")
	}

	if err := closure.DescendRecipePlugins(&cl, loader, lookup); err != nil {
		t.Fatalf("plugin descent: %v", err)
	}
	gotArt, ok := wasmPluginByURI(cl, socketArtifact)
	if !ok {
		t.Fatalf("workspace socket plugin artifact %q not pinned after descent", socketArtifact)
	}
	if len(gotArt.ReferencedFrom) != 1 || gotArt.ReferencedFrom[0] != socketEntity {
		t.Errorf("artifact ReferencedFrom = %v; want [%s]", gotArt.ReferencedFrom, socketEntity)
	}
}
