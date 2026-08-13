// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuildAggregaLabels_Compose_EveryConditional pins each optional-
// emission arm for the compose engine's label builder.
func TestBuildAggregaLabels_Compose_EveryConditional(t *testing.T) {
	t.Parallel()
	ctx := renderer.NodeRenderContext{
		NodeID:           "node-1",
		LabelComponentID: "comp-x",
		BoundaryPath:     "infra.web",
		RecipeName:       "myco/web",
		RecipeVersion:    "1.0.0",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: "v0.99",
			AggregateName:   "agg-prod",
			Environment:     "prod",
		},
		SyncWave: 5,
	}
	got := buildAggregaLabels(ctx)
	want := map[string]string{
		"aggrega.ai/component-id":     "comp-x",
		"aggrega.ai/boundary-path":    "infra.web",
		"aggrega.ai/recipe-uri":       "myco/web:1.0.0",
		"aggrega.ai/compiler-version": "v0.99",
		"aggrega.ai/aggregate-name":   "agg-prod",
		"aggrega.ai/environment":      "prod",
		"aggrega.ai/sync-wave":        "5",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q; want %q", k, got[k], v)
		}
	}
	// LabelComponentID empty → falls back to NodeID.
	got = buildAggregaLabels(renderer.NodeRenderContext{NodeID: "n"})
	if got["aggrega.ai/component-id"] != "n" {
		t.Errorf("fallback to NodeID; got %q", got["aggrega.ai/component-id"])
	}
}

// TestAsStringMap_AllDispatches pins the three dispatch arms: native
// map[string]any pass-through; map[any]any coercion; unsupported value
// returns false.
func TestAsStringMap_AllDispatches(t *testing.T) {
	t.Parallel()
	m, ok := asStringMap(map[string]any{"a": 1})
	if !ok || m["a"] != 1 {
		t.Errorf("native map: ok=%v m=%v", ok, m)
	}
	m, ok = asStringMap(map[any]any{"a": 1, "b": "x"})
	if !ok || m["a"] != 1 || m["b"] != "x" {
		t.Errorf("any-keyed coercion: ok=%v m=%v", ok, m)
	}
	// Non-string key in map[any]any → false.
	_, ok = asStringMap(map[any]any{1: "v"})
	if ok {
		t.Errorf("non-string key should fail coercion")
	}
	// Unsupported scalar.
	_, ok = asStringMap("not a map")
	if ok {
		t.Errorf("scalar should fail coercion")
	}
	_, ok = asStringMap(nil)
	if ok {
		t.Errorf("nil should fail coercion")
	}
}

// TestBuildSingleOriginMap_EmptyAndPopulated pins the helper's
// guard branches and the populated path.
func TestBuildSingleOriginMap_EmptyAndPopulated(t *testing.T) {
	t.Parallel()
	ctx := renderer.NodeRenderContext{NodeID: "n"}
	// Nil doc → empty origins.
	if got := buildSingleOriginMap(nil, ctx); len(got) != 0 {
		t.Errorf("nil doc → empty; got %v", got)
	}
	// No services key → empty.
	if got := buildSingleOriginMap(map[string]any{"version": "3"}, ctx); len(got) != 0 {
		t.Errorf("no services → empty; got %v", got)
	}
	// services is not a map → empty.
	bad := map[string]any{"services": []any{1, 2}}
	if got := buildSingleOriginMap(bad, ctx); len(got) != 0 {
		t.Errorf("non-map services → empty; got %v", got)
	}
	// Populated.
	doc := map[string]any{"services": map[string]any{"web": map[string]any{}, "db": map[string]any{}}}
	got := buildSingleOriginMap(doc, ctx)
	if len(got) != 2 || got["web"].NodeID != "n" || got["db"].NodeID != "n" {
		t.Errorf("populated: %v", got)
	}
}

// TestInjectAggregaLabelsIntoServices_EveryArm pins every dispatch arm
// of the per-service label-injection helper.
func TestInjectAggregaLabelsIntoServices_EveryArm(t *testing.T) {
	t.Parallel()
	ctx := renderer.NodeRenderContext{NodeID: "n", LabelComponentID: "comp"}

	// nil doc → no-op (no panic).
	injectAggregaLabelsIntoServices(nil, map[string]renderer.NodeRenderContext{"web": ctx})

	// Empty origins → no-op.
	doc := map[string]any{"services": map[string]any{"web": map[string]any{}}}
	injectAggregaLabelsIntoServices(doc, nil)
	if _, has := doc["services"].(map[string]any)["web"].(map[string]any)["labels"]; has {
		t.Errorf("empty origins should leave services untouched")
	}

	// No services key → no-op.
	noSvc := map[string]any{"version": "3"}
	injectAggregaLabelsIntoServices(noSvc, map[string]renderer.NodeRenderContext{"web": ctx})

	// services is not a map → no-op.
	badShape := map[string]any{"services": []any{}}
	injectAggregaLabelsIntoServices(badShape, map[string]renderer.NodeRenderContext{"web": ctx})

	// Service not in origins → preserved untouched.
	docMixed := map[string]any{
		"services": map[string]any{
			"web":        map[string]any{},
			"author-svc": map[string]any{"image": "x"},
		},
	}
	injectAggregaLabelsIntoServices(docMixed, map[string]renderer.NodeRenderContext{
		"web": ctx,
	})
	svc := docMixed["services"].(map[string]any)
	if _, has := svc["author-svc"].(map[string]any)["labels"]; has {
		t.Errorf("author-managed service should not get labels")
	}
	webSvc := svc["web"].(map[string]any)
	labels, hasLabels := webSvc["labels"].(map[string]string)
	if !hasLabels || labels["aggrega.ai/component-id"] != "comp" {
		t.Errorf("web should receive aggrega labels; got %v", webSvc["labels"])
	}

	// Service value is not a map → continue (no panic).
	badSvc := map[string]any{"services": map[string]any{"weird": 42}}
	injectAggregaLabelsIntoServices(badSvc, map[string]renderer.NodeRenderContext{"weird": ctx})

	// Existing labels (map[string]any with string vals) merged.
	docWithLabels := map[string]any{
		"services": map[string]any{
			"web": map[string]any{
				"labels": map[string]any{"tier": "backend"},
			},
		},
	}
	injectAggregaLabelsIntoServices(docWithLabels, map[string]renderer.NodeRenderContext{"web": ctx})
	webSvc = docWithLabels["services"].(map[string]any)["web"].(map[string]any)
	mergedLabels := webSvc["labels"].(map[string]string)
	if mergedLabels["tier"] != "backend" {
		t.Errorf("existing label lost; got %v", mergedLabels)
	}
	if mergedLabels["aggrega.ai/component-id"] != "comp" {
		t.Errorf("aggrega label missing; got %v", mergedLabels)
	}
}

// TestApplyContainerSecurityContext_EveryField pins each documented
// field translation: read-only root FS, capabilities add/drop, user
// formatting with and without group.
func TestApplyContainerSecurityContext_EveryField(t *testing.T) {
	t.Parallel()
	roFS := true
	runAsUser := 1000
	runAsGroup := 2000
	sc := &types.ContainerSecurityContext{
		ReadOnlyRootFilesystem: &roFS,
		RunAsUser:              &runAsUser,
		RunAsGroup:             &runAsGroup,
		Capabilities: &types.Capabilities{
			Add:  []string{"NET_BIND_SERVICE"},
			Drop: []string{"ALL"},
		},
	}
	var svc Service
	applyContainerSecurityContext(&svc, sc)
	if !svc.ReadOnly {
		t.Errorf("read-only not applied")
	}
	if svc.User != "1000:2000" {
		t.Errorf("user = %q; want 1000:2000", svc.User)
	}
	if len(svc.CapAdd) != 1 || svc.CapAdd[0] != "NET_BIND_SERVICE" {
		t.Errorf("cap_add: %v", svc.CapAdd)
	}
	if len(svc.CapDrop) != 1 || svc.CapDrop[0] != "ALL" {
		t.Errorf("cap_drop: %v", svc.CapDrop)
	}

	// User-only (no group).
	var svc2 Service
	applyContainerSecurityContext(&svc2, &types.ContainerSecurityContext{
		RunAsUser: &runAsUser,
	})
	if svc2.User != "1000" {
		t.Errorf("user-only = %q; want 1000", svc2.User)
	}

	// Empty SC.
	var svc3 Service
	applyContainerSecurityContext(&svc3, &types.ContainerSecurityContext{})
	if svc3.User != "" || svc3.ReadOnly {
		t.Errorf("empty SC should leave defaults; got %+v", svc3)
	}
}

// TestApplyPodLevelFieldsToService_EveryBranch pins the precedence:
// container-level User wins; pod-level RunAsUser fills when container-
// level absent; nil-PSC → no-op.
func TestApplyPodLevelFieldsToService_EveryBranch(t *testing.T) {
	t.Parallel()
	runAsUser := 1000
	runAsGroup := 2000

	// Container-level User wins (early return).
	svc := Service{User: "preset:override"}
	w := &types.Workload{
		PodSecurityContext: &types.PodSecurityContext{RunAsUser: &runAsUser},
	}
	applyPodLevelFieldsToService(&svc, w)
	if svc.User != "preset:override" {
		t.Errorf("container-level User should win; got %q", svc.User)
	}

	// Nil PodSecurityContext → no-op.
	svc = Service{}
	w = &types.Workload{}
	applyPodLevelFieldsToService(&svc, w)
	if svc.User != "" {
		t.Errorf("nil PSC should not set User; got %q", svc.User)
	}

	// Nil RunAsUser inside PSC → no-op.
	svc = Service{}
	w = &types.Workload{PodSecurityContext: &types.PodSecurityContext{}}
	applyPodLevelFieldsToService(&svc, w)
	if svc.User != "" {
		t.Errorf("nil RunAsUser should not set User; got %q", svc.User)
	}

	// Populated PSC with group → uid:gid format.
	svc = Service{}
	w = &types.Workload{
		PodSecurityContext: &types.PodSecurityContext{
			RunAsUser:  &runAsUser,
			RunAsGroup: &runAsGroup,
		},
	}
	applyPodLevelFieldsToService(&svc, w)
	if svc.User != "1000:2000" {
		t.Errorf("PSC with group = %q; want 1000:2000", svc.User)
	}

	// Populated PSC without group → uid only.
	svc = Service{}
	w = &types.Workload{
		PodSecurityContext: &types.PodSecurityContext{RunAsUser: &runAsUser},
	}
	applyPodLevelFieldsToService(&svc, w)
	if svc.User != "1000" {
		t.Errorf("PSC without group = %q; want 1000", svc.User)
	}
}

// TestDeepMergeOverlay_AllBranches pins every dispatch path: nil base,
// nested-map recursion, scalar overlay replaces, list overlay replaces,
// new key added.
func TestDeepMergeOverlay_AllBranches(t *testing.T) {
	t.Parallel()
	// Nil base.
	out := deepMergeOverlay(nil, map[string]any{"k": "v"})
	if out["k"] != "v" {
		t.Errorf("nil base: %v", out)
	}

	// New key added.
	out = deepMergeOverlay(map[string]any{"a": 1}, map[string]any{"b": 2})
	if out["a"] != 1 || out["b"] != 2 {
		t.Errorf("new key: %v", out)
	}

	// Nested-map recursion.
	base := map[string]any{
		"services": map[string]any{
			"web": map[string]any{"image": "old", "env": map[string]any{"K": "v"}},
		},
	}
	overlay := map[string]any{
		"services": map[string]any{
			"web": map[string]any{"image": "new"},
			"db":  map[string]any{"image": "postgres"},
		},
	}
	out = deepMergeOverlay(base, overlay)
	services := out["services"].(map[string]any)
	web := services["web"].(map[string]any)
	if web["image"] != "new" {
		t.Errorf("scalar overlay should win; got %v", web["image"])
	}
	// env preserved through recursion.
	envM := web["env"].(map[string]any)
	if envM["K"] != "v" {
		t.Errorf("nested env should preserve; got %v", web["env"])
	}
	if services["db"] == nil {
		t.Errorf("new service should be added")
	}

	// List overlay replaces (not merges).
	base = map[string]any{"ports": []any{"80"}}
	overlay = map[string]any{"ports": []any{"443"}}
	out = deepMergeOverlay(base, overlay)
	ports := out["ports"].([]any)
	if len(ports) != 1 || ports[0] != "443" {
		t.Errorf("list overlay should replace; got %v", ports)
	}
}
