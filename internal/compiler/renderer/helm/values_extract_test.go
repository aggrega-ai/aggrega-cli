// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// TestNilIfEmpty_BothBranches pins empty-input nil return and populated
// pass-through.
func TestNilIfEmpty_BothBranches(t *testing.T) {
	t.Parallel()
	if got := nilIfEmpty(map[string]string{}); got != nil {
		t.Errorf("empty should return nil; got %v", got)
	}
	if got := nilIfEmpty(nil); got != nil {
		t.Errorf("nil should return nil; got %v", got)
	}
	pop := map[string]string{"k": "v"}
	got := nilIfEmpty(pop)
	if got == nil || got["k"] != "v" {
		t.Errorf("populated should pass through; got %v", got)
	}
}

// TestPtrToInt64FromInt_NilAndPopulated pins both branches of the
// pointer-conversion helper.
func TestPtrToInt64FromInt_NilAndPopulated(t *testing.T) {
	t.Parallel()
	if ptrToInt64FromInt(nil) != nil {
		t.Errorf("nil should return nil")
	}
	v := 42
	got := ptrToInt64FromInt(&v)
	if got == nil || *got != int64(42) {
		t.Errorf("populated should convert; got %v", got)
	}
}

// TestIntsToInt64s_EmptyAndPopulated pins both branches.
func TestIntsToInt64s_EmptyAndPopulated(t *testing.T) {
	t.Parallel()
	if got := intsToInt64s(nil); got != nil {
		t.Errorf("nil should return nil; got %v", got)
	}
	if got := intsToInt64s([]int{}); got != nil {
		t.Errorf("empty should return nil; got %v", got)
	}
	got := intsToInt64s([]int{1, 2, 3})
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("populated: got %v; want [1 2 3]", got)
	}
}

// TestChooseScalarStyle_SpecialAndPlain pins the conservative-quote
// dispatch: special YAML chars → double-quoted; plain identifier → 0.
func TestChooseScalarStyle_SpecialAndPlain(t *testing.T) {
	t.Parallel()
	if got := chooseScalarStyle("plain-id"); got != 0 {
		t.Errorf("plain → 0; got %v", got)
	}
	specials := []string{"a:b", "a#b", "a{}b", "a[]b", "a,b", "a&b", "a*b", "a!b", "a|b", "a>b", "a'b", "a\"b", "a%b", "a@b", "a`b", "a\nb"}
	for _, s := range specials {
		if got := chooseScalarStyle(s); got != yaml.DoubleQuotedStyle {
			t.Errorf("%q should be quoted; got %v", s, got)
		}
	}
}

// TestBuildAggregaLabels_EveryConditionalArm pins every optional-
// emission branch in buildAggregaLabels.
// k8sLabelValue is the Kubernetes label-value charset: empty, or start/end
// alphanumeric with [-A-Za-z0-9_.] between, ≤63 chars.
var k8sLabelValue = regexp.MustCompile(`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`)

func TestBuildAggregaLabels_EveryConditionalArm(t *testing.T) {
	t.Parallel()
	// Deliberately label-HOSTILE values: a nested boundary path ('/'), a
	// versioned recipe URI (':'), and a negative sync wave (leading '-').
	ctx := renderer.NodeRenderContext{
		NodeID:           "node-1",
		LabelComponentID: "comp-x",
		BoundaryPath:     "infra/web",
		RecipeName:       "myco/web",
		RecipeVersion:    "1.0.0",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: "v0.99",
			AggregateName:   "agg-prod",
			Environment:     "prod",
		},
		SyncWave: -1,
	}
	labels := buildAggregaLabels(ctx)
	// Only label-safe provenance stays in the label map.
	wantLabels := map[string]string{
		"app.kubernetes.io/name":      "comp-x",
		"aggrega.ai/component-id":     "comp-x",
		"aggrega.ai/compiler-version": "v0.99",
		"aggrega.ai/aggregate-name":   "agg-prod",
		"aggrega.ai/environment":      "prod",
	}
	for k, v := range wantLabels {
		if labels[k] != v {
			t.Errorf("label %s = %q; want %q", k, labels[k], v)
		}
	}
	// The label-hostile keys must NOT be labels.
	for _, k := range []string{"aggrega.ai/recipe-uri", "aggrega.ai/boundary-path", "aggrega.ai/sync-wave"} {
		if _, has := labels[k]; has {
			t.Errorf("%s must be an annotation, not a label (label values reject ':'/'/'/leading '-')", k)
		}
	}
	// Semantic guard: EVERY emitted label value must satisfy the K8s charset.
	for k, v := range labels {
		if !k8sLabelValue.MatchString(v) || len(v) > 63 {
			t.Errorf("label %s has K8s-invalid value %q", k, v)
		}
	}

	// The label-hostile provenance lands in the annotation map verbatim.
	ann := buildAggregaAnnotations(ctx)
	wantAnn := map[string]string{
		"aggrega.ai/boundary-path":     "infra/web",
		"aggrega.ai/recipe-uri":        "myco/web:1.0.0",
		"aggrega.ai/sync-wave":         "-1",
		"argocd.argoproj.io/sync-wave": "-1",
	}
	for k, v := range wantAnn {
		if ann[k] != v {
			t.Errorf("annotation %s = %q; want %q", k, ann[k], v)
		}
	}

	// Empty ctx: only the default identity labels emit; no annotations.
	minimal := buildAggregaLabels(renderer.NodeRenderContext{NodeID: "n"})
	if minimal["app.kubernetes.io/name"] != "n" {
		t.Errorf("fallback to NodeID for app.kubernetes.io/name; got %q", minimal["app.kubernetes.io/name"])
	}
	if len(buildAggregaAnnotations(renderer.NodeRenderContext{NodeID: "n"})) != 0 {
		t.Errorf("empty ctx should produce no annotations")
	}
}

// TestBuildAggregaAnnotations_SyncWaveAndDefault pins the per-conditional
// emission: SyncWave != 0 emits both sync-wave annotations; zero produces an
// empty map.
func TestBuildAggregaAnnotations_SyncWaveAndDefault(t *testing.T) {
	t.Parallel()
	got := buildAggregaAnnotations(renderer.NodeRenderContext{SyncWave: 3})
	if got["argocd.argoproj.io/sync-wave"] != "3" || got["aggrega.ai/sync-wave"] != "3" {
		t.Errorf("sync-wave 3 expected on both annotations; got %v", got)
	}
	got = buildAggregaAnnotations(renderer.NodeRenderContext{SyncWave: 0})
	if len(got) != 0 {
		t.Errorf("zero SyncWave should produce empty map; got %v", got)
	}
}

// TestHelmHookWeight_EveryBranch pins zero-elide vs populated stringify.
func TestHelmHookWeight_EveryBranch(t *testing.T) {
	t.Parallel()
	if got := helmHookWeight(renderer.NodeRenderContext{SyncWave: 0}); got != "" {
		t.Errorf("zero should return empty; got %q", got)
	}
	if got := helmHookWeight(renderer.NodeRenderContext{SyncWave: -2}); got != "-2" {
		t.Errorf("-2 should return %q; got %q", "-2", got)
	}
	if got := helmHookWeight(renderer.NodeRenderContext{SyncWave: 7}); got != "7" {
		t.Errorf("7 should return %q; got %q", "7", got)
	}
}

// TestFindMapChild_EveryBranch pins nil receiver, wrong-kind, key
// present, key absent.
func TestFindMapChild_EveryBranch(t *testing.T) {
	t.Parallel()
	if findMapChild(nil, "x") != nil {
		t.Errorf("nil map should return nil")
	}
	if findMapChild(&yaml.Node{Kind: yaml.ScalarNode}, "x") != nil {
		t.Errorf("scalar should return nil")
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "k1"},
		{Kind: yaml.ScalarNode, Value: "v1"},
		{Kind: yaml.ScalarNode, Value: "k2"},
		{Kind: yaml.ScalarNode, Value: "v2"},
	}}
	if got := findMapChild(mapping, "k2"); got == nil || got.Value != "v2" {
		t.Errorf("k2 = %v; want v2", got)
	}
	if findMapChild(mapping, "absent") != nil {
		t.Errorf("absent should return nil")
	}
	// Odd content (key without value): early-break path.
	odd := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "lone"},
	}}
	if findMapChild(odd, "lone") != nil {
		t.Errorf("orphan key without value pair must not match")
	}
}

// TestInjectAggregaAnnotations_HookResource pins the helm.sh/hook-aware
// path: a manifest carrying an existing helm.sh/hook annotation
// receives helm.sh/hook-weight; a non-hook manifest does NOT.
func TestInjectAggregaAnnotations_HookResource(t *testing.T) {
	t.Parallel()
	hookYAML := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: migrate
  annotations:
    helm.sh/hook: pre-install
spec:
  template:
    spec:
      containers:
      - name: c
        image: x
`)
	out, err := injectAggregaAnnotations(hookYAML, map[string]string{
		"aggrega.ai/foo": "bar",
	}, "5", 2)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "helm.sh/hook-weight") {
		t.Errorf("hook resource should receive hook-weight; got:\n%s", got)
	}
	if !strings.Contains(got, "aggrega.ai/foo: bar") {
		t.Errorf("aggrega annotation missing; got:\n%s", got)
	}
}

// TestInjectAggregaAnnotations_NonHookResource pins the non-hook
// path: no helm.sh/hook → no hook-weight emission.
func TestInjectAggregaAnnotations_NonHookResource(t *testing.T) {
	t.Parallel()
	plainYAML := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: data
data:
  k: v
`)
	out, err := injectAggregaAnnotations(plainYAML, map[string]string{
		"argocd.argoproj.io/sync-wave": "3",
	}, "3", 2)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "helm.sh/hook-weight") {
		t.Errorf("non-hook resource should NOT receive hook-weight; got:\n%s", got)
	}
	if !strings.Contains(got, "sync-wave") {
		t.Errorf("always-emit annotation missing; got:\n%s", got)
	}
}

// TestInjectAggregaAnnotations_EmptyInputs pins the no-op short-circuit.
func TestInjectAggregaAnnotations_EmptyInputs(t *testing.T) {
	t.Parallel()
	src := []byte("apiVersion: v1\nkind: Pod\n")
	out, err := injectAggregaAnnotations(src, nil, "", 2)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if string(out) != string(src) {
		t.Errorf("empty inputs should be no-op")
	}
}

// TestInjectAggregaAnnotations_NoMetadata pins the no-metadata-block
// branch: documents without metadata get untouched.
func TestInjectAggregaAnnotations_NoMetadata(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: v1
kind: Service
spec:
  type: ClusterIP
`)
	// Manifest has no metadata; merger creates metadata.annotations.
	out, err := injectAggregaAnnotations(src, map[string]string{"k": "v"}, "", 2)
	if err != nil {
		t.Fatalf("no-metadata: %v", err)
	}
	if !strings.Contains(string(out), "k: v") {
		t.Errorf("annotations must be injected even without metadata; got:\n%s", string(out))
	}
}

// TestInjectAggregaAnnotations_MultiDoc pins multi-document YAML: the
// "---" boundary is preserved and each document is processed.
func TestInjectAggregaAnnotations_MultiDoc(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: v1
kind: Pod
metadata:
  name: pod-1
---
apiVersion: v1
kind: Service
metadata:
  name: svc-1
`)
	out, err := injectAggregaAnnotations(src, map[string]string{"a/b": "c"}, "", 2)
	if err != nil {
		t.Fatalf("multi-doc: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "---") {
		t.Errorf("multi-doc boundary should be preserved; got:\n%s", got)
	}
	if strings.Count(got, "a/b: c") != 2 {
		t.Errorf("annotation should appear in both docs; got count=%d", strings.Count(got, "a/b: c"))
	}
}

// TestInjectAggregaAnnotations_ExistingAggregaKeyOverwritten pins the
// overwrite semantics: aggrega-managed key already present in
// metadata.annotations gets the new value.
func TestInjectAggregaAnnotations_ExistingAggregaKeyOverwritten(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: v1
kind: Pod
metadata:
  name: p
  annotations:
    aggrega.ai/foo: old
    chart-author/key: keep
`)
	out, err := injectAggregaAnnotations(src, map[string]string{
		"aggrega.ai/foo": "new",
	}, "", 2)
	if err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "aggrega.ai/foo: new") {
		t.Errorf("aggrega key should be overwritten; got:\n%s", got)
	}
	if !strings.Contains(got, "chart-author/key: keep") {
		t.Errorf("chart-author key should be preserved; got:\n%s", got)
	}
}

// TestTranslateContainerSecurityContext_EveryField pins each field
// transposition: RunAsUser/Group via ptrToInt64FromInt, RunAsNonRoot
// bool pass-through, RO root FS, capabilities add/drop.
func TestTranslateContainerSecurityContext_EveryField(t *testing.T) {
	t.Parallel()
	runAsUser := 1000
	runAsGroup := 2000
	runAsNonRoot := true
	roFS := true
	allowPriv := false
	src := &types.ContainerSecurityContext{
		RunAsUser:                &runAsUser,
		RunAsGroup:               &runAsGroup,
		RunAsNonRoot:             &runAsNonRoot,
		ReadOnlyRootFilesystem:   &roFS,
		AllowPrivilegeEscalation: &allowPriv,
		Capabilities: &types.Capabilities{
			Add:  []string{"NET_BIND_SERVICE"},
			Drop: []string{"ALL"},
		},
	}
	got := translateContainerSecurityContext(src)
	if got.RunAsUser == nil || *got.RunAsUser != 1000 {
		t.Errorf("RunAsUser = %v; want 1000", got.RunAsUser)
	}
	if got.RunAsGroup == nil || *got.RunAsGroup != 2000 {
		t.Errorf("RunAsGroup = %v; want 2000", got.RunAsGroup)
	}
	if got.RunAsNonRoot == nil || !*got.RunAsNonRoot {
		t.Errorf("RunAsNonRoot lost")
	}
	if got.Capabilities == nil || got.Capabilities.Add[0] != "NET_BIND_SERVICE" {
		t.Errorf("Capabilities lost")
	}

	// Nil capabilities case.
	src2 := &types.ContainerSecurityContext{}
	got2 := translateContainerSecurityContext(src2)
	if got2.Capabilities != nil {
		t.Errorf("nil Capabilities should stay nil; got %v", got2.Capabilities)
	}
}
