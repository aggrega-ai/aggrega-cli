// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuildProvenanceLabels_EveryConditional pins each optional-emit
// arm of the artifacts-emission label builder.
func TestBuildProvenanceLabels_EveryConditional(t *testing.T) {
	t.Parallel()
	state := &PhaseBState{
		RecipeName:    "myco/web",
		RecipeVersion: "1.0.0",
	}
	ctx := NodeRenderContext{
		NodeID:       "node-1",
		BoundaryPath: "infra/web",
		Provenance: ProvenanceContext{
			CompilerVersion: "v0.99",
			AggregateName:   "agg-prod",
			Environment:     "prod",
		},
		SyncWave: 5,
	}
	got := buildProvenanceLabels(state, ctx)
	want := map[string]string{
		"app.kubernetes.io/name":      "node-1",
		"aggrega.ai/component-id":     "node-1",
		"aggrega.ai/compiler-version": "v0.99",
		"aggrega.ai/aggregate-name":   "agg-prod",
		"aggrega.ai/environment":      "prod",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("label %s = %q; want %q", k, got[k], v)
		}
	}
	// The charset-unsafe provenance lives in annotations, never labels.
	for _, k := range []string{"aggrega.ai/recipe-uri", "aggrega.ai/boundary-path", "aggrega.ai/sync-wave"} {
		if _, has := got[k]; has {
			t.Errorf("label %s present; a charset-unsafe key must be an annotation, not a label", k)
		}
	}
	gotAnn := buildProvenanceAnnotations(state, ctx)
	wantAnn := map[string]string{
		"aggrega.ai/boundary-path":     "infra/web",
		"aggrega.ai/recipe-uri":        "myco/web:1.0.0",
		"aggrega.ai/sync-wave":         "5",
		"argocd.argoproj.io/sync-wave": "5",
	}
	for k, v := range wantAnn {
		if gotAnn[k] != v {
			t.Errorf("annotation %s = %q; want %q", k, gotAnn[k], v)
		}
	}

	// Minimal context: only identity labels emit; no charset-unsafe annotation.
	minimal := buildProvenanceLabels(nil, NodeRenderContext{NodeID: "n"})
	if minimal["aggrega.ai/component-id"] != "n" {
		t.Errorf("identity fallback; got %q", minimal["aggrega.ai/component-id"])
	}
	minimalAnn := buildProvenanceAnnotations(nil, NodeRenderContext{NodeID: "n"})
	if _, has := minimalAnn["aggrega.ai/sync-wave"]; has {
		t.Errorf("zero SyncWave should not emit")
	}
	if _, has := minimalAnn["aggrega.ai/recipe-uri"]; has {
		t.Errorf("nil state should not emit recipe-uri")
	}
}

// TestProvenanceComponentID_PrefersSanitizedLabelComponentID pins the shared
// component-identity contract every emitting surface (helm, helm-chart, the
// artifacts emitter) now derives from one helper: when LabelComponentID is set
// — the case for FR-emitted resources whose internal NodeID carries marker
// syntax — it is the component identity, so a charset-invalid synthetic NodeID
// never reaches a label value. When LabelComponentID is empty (the ordinary
// component node) the already-label-valid NodeID stands.
func TestProvenanceComponentID_PrefersSanitizedLabelComponentID(t *testing.T) {
	t.Parallel()

	// Synthetic boundary-FR node: raw NodeID is charset-invalid (":" / "@" /
	// "<>"); the sanitised LabelComponentID is what labels must carry.
	syn := NodeRenderContext{
		NodeID:           "<boundary-fr>:bridge@cluster",
		LabelComponentID: "boundary-fr-bridge-cluster",
	}
	if got := ProvenanceComponentID(syn); got != "boundary-fr-bridge-cluster" {
		t.Errorf("ProvenanceComponentID = %q; want the sanitised LabelComponentID", got)
	}
	labels := BuildAggregaProvenanceLabels(syn)
	for _, k := range []string{"aggrega.ai/component-id", "app.kubernetes.io/name"} {
		if labels[k] != "boundary-fr-bridge-cluster" {
			t.Errorf("%s = %q; want sanitised id (raw synthetic NodeID must not reach a label value)", k, labels[k])
		}
	}

	// Ordinary component node: no LabelComponentID, the label-valid NodeID stands.
	if got := ProvenanceComponentID(NodeRenderContext{NodeID: "data.main-db"}); got != "data.main-db" {
		t.Errorf("ProvenanceComponentID fallback = %q; want the NodeID", got)
	}
}

// TestBuildProvenanceAnnotations_SyncWaveAndDefault pins the
// SyncWave-driven and zero cases.
func TestBuildProvenanceAnnotations_SyncWaveAndDefault(t *testing.T) {
	t.Parallel()
	got := buildProvenanceAnnotations(nil, NodeRenderContext{SyncWave: 3})
	if got["argocd.argoproj.io/sync-wave"] != "3" {
		t.Errorf("got %v; want sync-wave=3", got)
	}
	got = buildProvenanceAnnotations(nil, NodeRenderContext{SyncWave: 0})
	if len(got) != 0 {
		t.Errorf("zero SyncWave should produce empty; got %v", got)
	}
}

// TestRecipeURI_EveryBranch pins nil-state, missing-name, missing-
// version, and complete arms.
func TestRecipeURI_EveryBranch(t *testing.T) {
	t.Parallel()
	if got := recipeURI(nil); got != "" {
		t.Errorf("nil state → empty; got %q", got)
	}
	if got := recipeURI(&PhaseBState{RecipeName: "n"}); got != "" {
		t.Errorf("missing version → empty; got %q", got)
	}
	if got := recipeURI(&PhaseBState{RecipeVersion: "v"}); got != "" {
		t.Errorf("missing name → empty; got %q", got)
	}
	if got := recipeURI(&PhaseBState{RecipeName: "n", RecipeVersion: "v"}); got != "n:v" {
		t.Errorf("complete → n:v; got %q", got)
	}
}

// TestExtractNodeBaseContext_AllBranches pins the base-context projector:
// graph-present and graph-nil paths.
func TestExtractNodeBaseContext_AllBranches(t *testing.T) {
	t.Parallel()
	// Nil graph → zero output.
	got := ExtractNodeBaseContext("n", nil)
	if got.NodeID != "n" {
		t.Errorf("NodeID should be set even with nil graph; got %q", got.NodeID)
	}

	// Populated graph.
	graph := &types.IRGraph{}
	graph.Metadata.AggregateName = "agg"
	graph.Spec.Provenance.ReleaseVersion = "1.2.3"
	graph.Spec.Provenance.AggregateVersion = "0.1.0"
	node := types.IRNode{
		ResolvedVersion: "9.0",
		Component:       "comp/x",
		BoundaryPath:    "infra/web",
	}
	graph.Spec.Nodes.Set("n1", node)
	graph.Spec.ValuesMapping.Set("n1", []byte(`{"k":"v"}`))
	graph.Spec.InfraMapping.Set("n1", []byte(`{"size":"small"}`))
	graph.Spec.Edges = []types.IREdge{
		{Source: "n1", Target: "tgt", Socket: "http", Alias: "a", OriginFeature: "f"},
		{Source: "other", Target: "x", Socket: "y"}, // skipped: wrong source
		{Source: "n1", Target: "noskt"},             // skipped: empty socket
	}

	got = ExtractNodeBaseContext("n1", graph)
	if got.NodeID != "n1" {
		t.Errorf("NodeID = %q; want n1", got.NodeID)
	}
	if v, _ := got.Values["k"]; v != "v" {
		t.Errorf("Values lost; got %v", got.Values)
	}
	if v, _ := got.Infra["size"]; v != "small" {
		t.Errorf("Infra lost; got %v", got.Infra)
	}
	httpEntry, ok := got.Uses["http"].(map[string]any)
	if !ok || httpEntry["target"] != "tgt" || httpEntry["alias"] != "a" || httpEntry["origin_feature"] != "f" {
		t.Errorf("Uses entry malformed; got %v", got.Uses)
	}
	// Wrong-source + empty-socket edges should not appear.
	if _, has := got.Uses["y"]; has {
		t.Errorf("wrong-source edge should be skipped")
	}
	if got.Metadata["aggregate_name"] != "agg" {
		t.Errorf("metadata aggregate_name lost; got %v", got.Metadata)
	}
	if got.Metadata["component"] != "comp/x" {
		t.Errorf("metadata component lost; got %v", got.Metadata)
	}

	// Missing node — metadata.component absent.
	got = ExtractNodeBaseContext("absent", graph)
	if _, has := got.Metadata["component"]; has {
		t.Errorf("absent node should not populate component")
	}
}

// TestWalkSlice_StructElements pins the slice-walker dispatch through
// the public walkForTemplate entry point.
func TestWalkSlice_StructElements(t *testing.T) {
	t.Parallel()
	type s struct {
		V int `json:"v"`
	}
	src := []s{{V: 1}, {V: 2}}
	got := walkForTemplate(reflect.ValueOf(src))
	arr, ok := got.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("slice of structs → []any of len 2; got %v", got)
	}
	first, ok := arr[0].(map[string]any)
	if !ok || first["v"] != 1 {
		t.Errorf("first struct → map; got %v", arr[0])
	}
}

// TestWalkMap_NonStringKeys pins the map-walker dispatch for a non-
// string-keyed map: the walker uses Go's stringer semantics via
// fmt.Sprintf for the map keys.
func TestWalkMap_NonStringKeys(t *testing.T) {
	t.Parallel()
	m := map[int]string{1: "a", 2: "b"}
	got := walkForTemplate(reflect.ValueOf(m))
	// Outcome doesn't matter as long as the walk doesn't panic.
	_ = got
}

// TestWalkForTemplate_NestedInterfaceUnwrapping pins the interface-
// dereference path: an interface wrapping a struct passes through to
// walkStruct.
func TestWalkForTemplate_NestedInterfaceUnwrapping(t *testing.T) {
	t.Parallel()
	type s struct {
		V string `json:"v"`
	}
	var holder any = s{V: "x"}
	got := walkForTemplate(reflect.ValueOf(&holder).Elem())
	m, ok := got.(map[string]any)
	if !ok || m["v"] != "x" {
		t.Errorf("interface-wrapped struct → map; got %v", got)
	}
}
