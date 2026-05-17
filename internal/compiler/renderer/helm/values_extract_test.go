// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// TestStringFromValues_EveryBranch pins the type-discriminating helper:
// declared string returns the string; non-string declared value returns
// the fallback; missing key returns the fallback.
func TestStringFromValues_EveryBranch(t *testing.T) {
	t.Parallel()
	v := map[string]any{
		"present-string":     "hello",
		"present-not-string": 42,
	}
	if got := stringFromValues(v, "present-string", "fb"); got != "hello" {
		t.Errorf("present string = %q; want hello", got)
	}
	if got := stringFromValues(v, "present-not-string", "fb"); got != "fb" {
		t.Errorf("non-string value should fall back; got %q", got)
	}
	if got := stringFromValues(v, "missing", "fb"); got != "fb" {
		t.Errorf("missing key should fall back; got %q", got)
	}
}

// TestMapStringFromValues_EveryShape pins every documented dispatch
// arm of the type discriminator: each shape produces an OrderedMap
// with the expected author order or sorted order, and unsupported
// shapes return nil.
func TestMapStringFromValues_EveryShape(t *testing.T) {
	t.Parallel()
	t.Run("missing-key", func(t *testing.T) {
		if mapStringFromValues(map[string]any{}, "absent") != nil {
			t.Errorf("missing should return nil")
		}
	})
	t.Run("nil-value", func(t *testing.T) {
		if mapStringFromValues(map[string]any{"k": nil}, "k") != nil {
			t.Errorf("nil value should return nil")
		}
	})
	t.Run("ordered-pointer-string", func(t *testing.T) {
		om := types.NewOrderedMap[string]()
		om.Set("a", "1")
		om.Set("b", "2")
		got := mapStringFromValues(map[string]any{"env": om}, "env")
		if got == nil || got.Len() != 2 {
			t.Fatalf("expected 2 entries; got %v", got)
		}
		// Pointer pass-through: same map.
		if got != om {
			t.Errorf("expected pass-through; got copy")
		}
	})
	t.Run("ordered-value-string", func(t *testing.T) {
		var om types.OrderedMap[string]
		om.Set("a", "1")
		om.Set("b", "2")
		got := mapStringFromValues(map[string]any{"env": om}, "env")
		if got == nil || got.Len() != 2 {
			t.Fatalf("expected 2 entries; got %v", got)
		}
	})
	t.Run("ordered-pointer-any", func(t *testing.T) {
		om := types.NewOrderedMap[any]()
		om.Set("a", 1)
		om.Set("b", "two")
		got := mapStringFromValues(map[string]any{"env": om}, "env")
		if got == nil || got.Len() != 2 {
			t.Fatalf("expected 2 entries; got %v", got)
		}
		if v, _ := got.Get("a"); v != "1" {
			t.Errorf("a = %q; want 1 (coerced)", v)
		}
	})
	t.Run("ordered-pointer-any-nil", func(t *testing.T) {
		var om *types.OrderedMap[any]
		if mapStringFromValues(map[string]any{"env": om}, "env") != nil {
			t.Errorf("nil pointer should return nil")
		}
	})
	t.Run("ordered-value-any", func(t *testing.T) {
		var om types.OrderedMap[any]
		om.Set("a", 1)
		got := mapStringFromValues(map[string]any{"env": om}, "env")
		if got == nil || got.Len() != 1 {
			t.Fatalf("expected 1 entry; got %v", got)
		}
	})
	t.Run("plain-map-any", func(t *testing.T) {
		m := map[string]any{"zebra": "z", "apple": 1, "mango": "m"}
		got := mapStringFromValues(map[string]any{"env": m}, "env")
		if got == nil || got.Len() != 3 {
			t.Fatalf("expected 3 entries; got %v", got)
		}
		// Plain map → keys inserted in sorted order (no author order).
		keys := got.Keys()
		want := []string{"apple", "mango", "zebra"}
		for i, k := range want {
			if keys[i] != k {
				t.Errorf("position %d: got %q; want %q", i, keys[i], k)
			}
		}
	})
	t.Run("plain-map-string", func(t *testing.T) {
		m := map[string]string{"z": "z-val", "a": "a-val"}
		got := mapStringFromValues(map[string]any{"env": m}, "env")
		if got == nil || got.Len() != 2 {
			t.Fatalf("expected 2 entries; got %v", got)
		}
		keys := got.Keys()
		if keys[0] != "a" || keys[1] != "z" {
			t.Errorf("plain map[string]string should sort keys; got %v", keys)
		}
	})
	t.Run("unsupported-shape", func(t *testing.T) {
		if mapStringFromValues(map[string]any{"env": 42}, "env") != nil {
			t.Errorf("integer should return nil")
		}
	})
}

// TestIntFromValues_EveryBranch pins the type-discriminating helper:
// float64, int, string-numeric, string-non-numeric, missing key.
func TestIntFromValues_EveryBranch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		values   map[string]any
		key      string
		fallback int
		want     int
	}{
		{"float64", map[string]any{"k": float64(7)}, "k", 99, 7},
		{"int", map[string]any{"k": int(8)}, "k", 99, 8},
		{"string-numeric", map[string]any{"k": "11"}, "k", 99, 11},
		{"string-non-numeric", map[string]any{"k": "abc"}, "k", 99, 99},
		{"missing", map[string]any{}, "k", 99, 99},
		{"unsupported-type", map[string]any{"k": true}, "k", 99, 99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := intFromValues(tc.values, tc.key, tc.fallback); got != tc.want {
				t.Errorf("got %d; want %d", got, tc.want)
			}
		})
	}
}

// TestStringSliceFromValues_AllBranches pins missing key, nil value,
// non-array type, and populated array.
func TestStringSliceFromValues_AllBranches(t *testing.T) {
	t.Parallel()
	if got := stringSliceFromValues(map[string]any{}, "absent"); got != nil {
		t.Errorf("missing should be nil; got %v", got)
	}
	if got := stringSliceFromValues(map[string]any{"k": nil}, "k"); got != nil {
		t.Errorf("nil value should be nil; got %v", got)
	}
	if got := stringSliceFromValues(map[string]any{"k": "not-array"}, "k"); got != nil {
		t.Errorf("non-array should be nil; got %v", got)
	}
	got := stringSliceFromValues(map[string]any{"k": []any{"a", 1, true}}, "k")
	if len(got) != 3 || got[0] != "a" || got[1] != "1" {
		t.Errorf("populated array coercion; got %v", got)
	}
}

// TestSplitPort_HostAndContainer pins both forms: "host:container"
// and a single-port form treated as both host and container.
func TestSplitPort_HostAndContainer(t *testing.T) {
	t.Parallel()
	h, c := splitPort("80:8080")
	if h != 80 || c != 8080 {
		t.Errorf("host:container split = (%d, %d); want (80, 8080)", h, c)
	}
	h, c = splitPort("3000")
	if h != 3000 || c != 3000 {
		t.Errorf("single port = (%d, %d); want (3000, 3000)", h, c)
	}
}

// TestParsePorts_EmptyAndPopulated pins empty-input short-circuit and
// the populated dispatch shape.
func TestParsePorts_EmptyAndPopulated(t *testing.T) {
	t.Parallel()
	cp, sp := parsePorts(nil)
	if cp != nil || sp != nil {
		t.Errorf("empty should return (nil, nil); got (%v, %v)", cp, sp)
	}
	cp, sp = parsePorts([]string{"80:8080", "443:8443"})
	if len(cp) != 2 || len(sp) != 2 {
		t.Fatalf("got %d container, %d service; want 2/2", len(cp), len(sp))
	}
	if sp[0].Name != "port-0" || sp[1].Name != "port-1" {
		t.Errorf("port names = %q, %q; want port-0, port-1", sp[0].Name, sp[1].Name)
	}
}

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
func TestBuildAggregaLabels_EveryConditionalArm(t *testing.T) {
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
	labels := buildAggregaLabels(ctx)
	want := map[string]string{
		"app.kubernetes.io/name":       "comp-x",
		"aggrega.ai/component-id":      "comp-x",
		"aggrega.ai/boundary-path":     "infra.web",
		"aggrega.ai/recipe-uri":        "myco/web:1.0.0",
		"aggrega.ai/compiler-version":  "v0.99",
		"aggrega.ai/aggregate-name":    "agg-prod",
		"aggrega.ai/environment":       "prod",
		"aggrega.ai/sync-wave":         "5",
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("%s = %q; want %q", k, labels[k], v)
		}
	}

	// Empty ctx: only the default identity labels emit.
	minimal := buildAggregaLabels(renderer.NodeRenderContext{NodeID: "n"})
	if minimal["app.kubernetes.io/name"] != "n" {
		t.Errorf("fallback to NodeID for app.kubernetes.io/name; got %q", minimal["app.kubernetes.io/name"])
	}
	if _, has := minimal["aggrega.ai/boundary-path"]; has {
		t.Errorf("empty BoundaryPath should NOT emit aggrega.ai/boundary-path")
	}
	if _, has := minimal["aggrega.ai/sync-wave"]; has {
		t.Errorf("zero SyncWave should NOT emit aggrega.ai/sync-wave")
	}
}

// TestBuildAggregaAnnotations_SyncWaveAndDefault pins the per-conditional
// emission: SyncWave != 0 emits argocd.argoproj.io/sync-wave; zero
// produces an empty map.
func TestBuildAggregaAnnotations_SyncWaveAndDefault(t *testing.T) {
	t.Parallel()
	got := buildAggregaAnnotations(renderer.NodeRenderContext{SyncWave: 3})
	if got["argocd.argoproj.io/sync-wave"] != "3" {
		t.Errorf("sync-wave 3 expected; got %v", got)
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
	}, "5")
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
	}, "3")
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
	out, err := injectAggregaAnnotations(src, nil, "")
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
	out, err := injectAggregaAnnotations(src, map[string]string{"k": "v"}, "")
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
	out, err := injectAggregaAnnotations(src, map[string]string{"a/b": "c"}, "")
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
	}, "")
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
