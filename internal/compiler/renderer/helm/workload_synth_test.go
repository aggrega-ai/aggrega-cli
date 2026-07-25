// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// goldenCheck compares actual against the golden file at testdata/workload/<name>.golden.yaml.
// Set UPDATE_GOLDEN=1 to overwrite the golden with actual.
func goldenCheck(t *testing.T, name string, actual []byte) {
	t.Helper()
	path := filepath.Join("testdata", "workload", name+".golden.yaml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with UPDATE_GOLDEN=1 to create)", path, err)
	}
	if string(actual) != string(want) {
		t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, actual, want)
	}
}

// providesBag builds a resolved secret-slot provides bag with the
// referenceable-secret contract keys (name, key) the synthesizer reads.
func providesBag(name, key string) types.OrderedMap[string] {
	p := types.OrderedMap[string]{}
	p.Set("name", name)
	p.Set("key", key)
	return p
}

// nodeCtxFor returns a minimal NodeRenderContext. Tests vary just the
// fields they exercise.
func nodeCtxFor(nodeID string) renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:       nodeID,
		BoundaryPath: "default",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion:  "test",
			AggregateName:    "test-agg",
			ReleaseName:      "test-rel",
			Environment:      "test-env",
			AggregateVersion: "1.0.0",
		},
	}
}

// minimalWorkload constructs a single-container Workload exposing one
// ClusterIP service — the smoke-test smallest end-to-end case.
func minimalWorkload() *types.Workload {
	w := &types.Workload{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Workload",
	}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)
	return w
}

func TestSynthesizeWorkload_MinimalDeploymentClusterIP(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	nodeCtx := nodeCtxFor("api-server")
	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtx)
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files (Deployment + Service), got %d", len(files))
	}

	// File names follow the helm provider's naming convention.
	if files[0].Name != "api-server-deployment.yaml" {
		t.Errorf("file[0] name: %q", files[0].Name)
	}
	if files[1].Name != "api-server-api-service.yaml" {
		t.Errorf("file[1] name: %q", files[1].Name)
	}

	// Concatenated YAML body is what the test gold-checks.
	concat := append(append([]byte{}, files[0].Body...), files[1].Body...)
	goldenCheck(t, "minimal-deployment-clusterip", concat)
}

// ----- GPU — extended-resource limit + runtimeClassName -----

func TestSynthesizeWorkload_GPUExtendedResourceAndRuntimeClass(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	w.RuntimeClassName = "nvidia"

	c := types.ContainerSpec{Image: "vllm:latest"}
	c.Ports.Set("http", 8000)
	limits := &types.ResourceList{CPU: "4", Memory: "16Gi"}
	limits.Extended.Set("nvidia.com/gpu", "1")
	requests := &types.ResourceList{}
	requests.Extended.Set("nvidia.com/gpu", "1")
	c.Resources = &types.ResourceSpec{Requests: requests, Limits: limits}
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("http", 8000)
	w.Services.Set("api", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("inference"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	body := string(files[0].Body)
	if !strings.Contains(body, "nvidia.com/gpu:") {
		t.Errorf("Deployment must carry the extended-resource key; got:\n%s", body)
	}
	if !strings.Contains(body, "runtimeClassName: nvidia") {
		t.Errorf("pod spec must carry runtimeClassName: nvidia; got:\n%s", body)
	}
	goldenCheck(t, "gpu-extended-resource-runtimeclass", concatBodies(files))
}

// concatBodies concatenates EmittedFile bodies in their author order.
// Tests gold-check the full concatenation so a single golden file covers
// every emitted resource for a given Workload.
func concatBodies(files []renderer.EmittedFile) []byte {
	var out []byte
	for _, f := range files {
		out = append(out, f.Body...)
	}
	return out
}

// nodeCtxWithAnnotations returns a minimal NodeRenderContext carrying the given
// merged per-node annotations — the same map the provider plugin fills from the
// IR's aggregate/release/catalog buckets.
func nodeCtxWithAnnotations(nodeID string, ann map[string]string) renderer.NodeRenderContext {
	ctx := nodeCtxFor(nodeID)
	ctx.Annotations = ann
	return ctx
}

// saWorkload builds a single-container Workload exposing one ClusterIP service,
// with Workload.ServiceAccount set to field (empty leaves it unset).
func saWorkload(field string) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)
	if field != "" {
		w.ServiceAccount = field
	}
	return w
}

// TestSynthesizeWorkload_ServiceAccountPrecedence proves the four combinations
// of the recipe-layer workload.serviceAccount field and the operator-boundary
// aggrega.ai/service-account annotation. The annotation wins when both are set.
func TestSynthesizeWorkload_ServiceAccountPrecedence(t *testing.T) {
	t.Parallel()
	const annKey = rendercore.AnnotationServiceAccount
	cases := []struct {
		name   string
		field  string
		annVal string
		wantSA string // "" means serviceAccountName must be absent
	}{
		{name: "neither", field: "", annVal: "", wantSA: ""},
		{name: "field-only", field: "recipe-sa", annVal: "", wantSA: "recipe-sa"},
		{name: "annotation-only", field: "", annVal: "op-sa", wantSA: "op-sa"},
		{name: "both-annotation-wins", field: "recipe-sa", annVal: "op-sa", wantSA: "op-sa"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ann map[string]string
			if tc.annVal != "" {
				ann = map[string]string{annKey: tc.annVal}
			}
			files, err := SynthesizeWorkloadCore(
				saWorkload(tc.field),
				renderer.SecretProvidesFromContext(nil),
				nodeCtxWithAnnotations("api", ann),
			)
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			got := string(concatBodies(files))
			if tc.wantSA == "" {
				if strings.Contains(got, "serviceAccountName") {
					t.Errorf("expected no serviceAccountName, got:\n%s", got)
				}
				return
			}
			want := "serviceAccountName: " + tc.wantSA
			if !strings.Contains(got, want) {
				t.Errorf("expected %q in output, got:\n%s", want, got)
			}
		})
	}
}

func TestServiceAccountFromAnnotations(t *testing.T) {
	t.Parallel()
	if got := rendercore.ServiceAccountFromAnnotations(nil); got != "" {
		t.Errorf("nil map: want %q, got %q", "", got)
	}
	if got := rendercore.ServiceAccountFromAnnotations(map[string]string{}); got != "" {
		t.Errorf("empty map: want %q, got %q", "", got)
	}
	if got := rendercore.ServiceAccountFromAnnotations(map[string]string{rendercore.AnnotationServiceAccount: "op-sa"}); got != "op-sa" {
		t.Errorf("present: want %q, got %q", "op-sa", got)
	}
}

// TestSynthesizeWorkload_ServiceAccountAnnotation gold-checks the full rendered
// Deployment when only the operator-boundary aggrega.ai/service-account
// annotation is set (no recipe workload.serviceAccount).
func TestSynthesizeWorkload_ServiceAccountAnnotation(t *testing.T) {
	t.Parallel()
	ctx := nodeCtxWithAnnotations("api", map[string]string{rendercore.AnnotationServiceAccount: "op-sa"})
	files, err := SynthesizeWorkloadCore(saWorkload(""), renderer.SecretProvidesFromContext(nil), ctx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	goldenCheck(t, "service-account-annotation", concatBodies(files))
}

// TestSynthesizeWorkload_ServiceAccountAnnotationWinsOverField gold-checks that
// when both the annotation and the recipe field are set, the rendered
// serviceAccountName is the annotation value (precedence, proven at render).
func TestSynthesizeWorkload_ServiceAccountAnnotationWinsOverField(t *testing.T) {
	t.Parallel()
	ctx := nodeCtxWithAnnotations("api", map[string]string{rendercore.AnnotationServiceAccount: "op-sa"})
	files, err := SynthesizeWorkloadCore(saWorkload("recipe-sa"), renderer.SecretProvidesFromContext(nil), ctx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	goldenCheck(t, "service-account-annotation-wins", concatBodies(files))
}

// TestSynthesizeWorkload_ImageRegistryAnnotation proves the operator-boundary
// aggrega.ai/image-registry annotation rewrites every container image ref —
// regular AND init — under the single prefix, and that its absence leaves images
// untouched.
func TestSynthesizeWorkload_ImageRegistryAnnotation(t *testing.T) {
	t.Parallel()
	build := func() *types.Workload {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
		main := types.ContainerSpec{Image: "docker.io/library/postgres:16"}
		main.Ports.Set("db", 5432)
		w.Containers.Set("main", main)
		w.InitContainers = []types.ContainerSpec{{Name: "setup", Image: "busybox:1"}}
		svc := types.ServiceSpec{Type: "ClusterIP"}
		svc.Ports.Set("db", 5432)
		w.Services.Set("db", svc)
		return w
	}

	// Absent annotation → images unchanged.
	filesPlain, err := SynthesizeWorkloadCore(build(), renderer.SecretProvidesFromContext(nil), nodeCtxFor("db"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (plain): %v", err)
	}
	plain := string(concatBodies(filesPlain))
	if !strings.Contains(plain, "image: docker.io/library/postgres:16") {
		t.Errorf("absent annotation should leave the regular image unchanged; got:\n%s", plain)
	}
	if !strings.Contains(plain, "image: busybox:1") {
		t.Errorf("absent annotation should leave the init image unchanged; got:\n%s", plain)
	}

	// Present annotation → both images rewritten under the prefix.
	ctx := nodeCtxWithAnnotations("db", map[string]string{rendercore.AnnotationImageRegistry: "reg.internal:5000"})
	filesReg, err := SynthesizeWorkloadCore(build(), renderer.SecretProvidesFromContext(nil), ctx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (registry): %v", err)
	}
	reg := string(concatBodies(filesReg))
	if !strings.Contains(reg, "image: reg.internal:5000/library/postgres:16") {
		t.Errorf("regular image not rewritten; got:\n%s", reg)
	}
	if !strings.Contains(reg, "image: reg.internal:5000/busybox:1") {
		t.Errorf("init image not rewritten (single-site claim); got:\n%s", reg)
	}
}

// TestSynthesizeWorkload_ImagePullSecretAnnotation proves the list-union rule for
// aggrega.ai/image-pull-secret: the operator-boundary annotation appends to the
// recipe-layer workload.imagePullSecrets (author order preserved), coexisting
// with recipe entries, and deduping against an existing field entry.
func TestSynthesizeWorkload_ImagePullSecretAnnotation(t *testing.T) {
	t.Parallel()
	build := func(field []string) *types.Workload {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
		c := types.ContainerSpec{Image: "app:1.0"}
		c.Ports.Set("web", 8080)
		w.Containers.Set("main", c)
		svc := types.ServiceSpec{Type: "ClusterIP"}
		svc.Ports.Set("web", 8080)
		w.Services.Set("api", svc)
		w.ImagePullSecrets = field
		return w
	}
	render := func(t *testing.T, field []string, annotation string) string {
		t.Helper()
		ann := map[string]string{}
		if annotation != "" {
			ann[rendercore.AnnotationImagePullSecret] = annotation
		}
		files, err := SynthesizeWorkloadCore(build(field), renderer.SecretProvidesFromContext(nil), nodeCtxWithAnnotations("api", ann))
		if err != nil {
			t.Fatalf("SynthesizeWorkloadCore: %v", err)
		}
		return string(concatBodies(files))
	}

	// 1. neither field nor annotation → no imagePullSecrets block.
	if body := render(t, nil, ""); strings.Contains(body, "imagePullSecrets") {
		t.Errorf("neither set: imagePullSecrets should be absent; got:\n%s", body)
	}
	// 2. field only → field entries in order, no annotation.
	if body := render(t, []string{"rc-a", "rc-b"}, ""); strings.Index(body, "- name: rc-a") < 0 ||
		strings.Index(body, "- name: rc-a") > strings.Index(body, "- name: rc-b") {
		t.Errorf("field only: expected [rc-a, rc-b] in order; got:\n%s", body)
	}
	// 3. annotation only → single annotation entry.
	body := render(t, nil, "op-x")
	if !strings.Contains(body, "- name: op-x") {
		t.Errorf("annotation only: expected op-x; got:\n%s", body)
	}
	// 4. both → union, field entry before appended annotation.
	body = render(t, []string{"rc-a"}, "op-x")
	if i, j := strings.Index(body, "- name: rc-a"), strings.Index(body, "- name: op-x"); i < 0 || j < 0 || i > j {
		t.Errorf("both: expected union [rc-a, op-x] in order; got:\n%s", body)
	}
	// 5. annotation duplicates a field entry → deduped (exactly one).
	body = render(t, []string{"rc-a"}, "rc-a")
	if n := strings.Count(body, "- name: rc-a"); n != 1 {
		t.Errorf("dedup: expected exactly one rc-a, got %d; body:\n%s", n, body)
	}
}

// TestSynthesizeWorkload_ImageAnnotationsCombined gold-checks the full rendered
// Deployment when both operator-boundary annotations are set together: the
// image-registry prefix rewrites the regular and init images, and the
// image-pull-secret unions after the recipe-declared pull secret.
func TestSynthesizeWorkload_ImageAnnotationsCombined(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	main := types.ContainerSpec{Image: "docker.io/library/postgres:16"}
	main.Ports.Set("db", 5432)
	w.Containers.Set("main", main)
	w.InitContainers = []types.ContainerSpec{{Name: "setup", Image: "busybox:1"}}
	w.ImagePullSecrets = []string{"recipe-pull"}
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("db", 5432)
	w.Services.Set("db", svc)

	ctx := nodeCtxWithAnnotations("db", map[string]string{
		rendercore.AnnotationImageRegistry:   "reg.internal:5000",
		rendercore.AnnotationImagePullSecret: "op-pull",
	})
	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), ctx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	goldenCheck(t, "image-annotations-combined", concatBodies(files))
}

// ----- Category 2 — Multi-container (main + sidecar) -----

func TestSynthesizeWorkload_MultiContainer(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	main := types.ContainerSpec{Image: "main:1.0"}
	main.Ports.Set("web", 8080)
	w.Containers.Set("main", main)

	sidecar := types.ContainerSpec{Image: "envoy:1.0"}
	sidecar.Ports.Set("admin", 9901)
	w.Containers.Set("envoy", sidecar)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "multi-container", concatBodies(files))
}

// ----- Category 3 — Each volume variant -----

func TestSynthesizeWorkload_AllVolumeVariants(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)

	w.Volumes.Set("cache", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "100Mi"}})
	w.Volumes.Set("config", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}})
	w.Volumes.Set("secret-store", types.VolumeSpec{Secret: &types.SecretVolume{Source: "app-secret"}})
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi", Class: "gp3"}})

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("data-server"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "all-volume-variants", concatBodies(files))
}

// ----- Category 4 — persistentTemplate triggers StatefulSet -----

func TestSynthesizeWorkload_PersistentTemplateTriggersStatefulSet(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "stateful"}
	c := types.ContainerSpec{Image: "db:1.0"}
	c.Ports.Set("db", 5432)
	w.Containers.Set("main", c)

	w.Volumes.Set("data", types.VolumeSpec{
		PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi", Class: "gp3"},
	})

	svc := types.ServiceSpec{Type: "Headless"}
	svc.Ports.Set("db", 5432)
	w.Services.Set("db", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("postgres"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}

	// Must emit StatefulSet, NOT Deployment.
	if files[0].Name != "postgres-statefulset.yaml" {
		t.Errorf("expected statefulset.yaml; got %q", files[0].Name)
	}
	// serviceName must be set, and must equal the rendered headless Service's
	// name (postgres-db) — the StatefulSet governs an emitted Service.
	if !strings.Contains(string(files[0].Body), "serviceName: postgres-db") {
		t.Errorf("StatefulSet must set serviceName to the headless Service name (postgres-db); got:\n%s", files[0].Body)
	}
	goldenCheck(t, "persistent-template-statefulset", concatBodies(files))
}

// TestSynthesizeWorkload_StatefulSetNoHeadless_Halts: a persistentTemplate
// Workload with no headless Service can't name a governing Service → halt.
func TestSynthesizeWorkload_StatefulSetNoHeadless_Halts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "stateful"}
	c := types.ContainerSpec{Image: "db:1.0"}
	c.Ports.Set("db", 5432)
	w.Containers.Set("main", c)
	w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi"}})
	// A ClusterIP service is not headless — no governing Service.
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("db", 5432)
	w.Services.Set("db", svc)

	_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("postgres"))
	if !errors.Is(err, ErrStatefulSetNoHeadlessService) {
		t.Fatalf("expected ErrStatefulSetNoHeadlessService; got %v", err)
	}
}

// TestSynthesizeWorkload_StatefulSetMultipleHeadless_Halts: a persistentTemplate
// Workload with two headless Services has an ambiguous governing Service → halt.
func TestSynthesizeWorkload_StatefulSetMultipleHeadless_Halts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "stateful"}
	c := types.ContainerSpec{Image: "db:1.0"}
	c.Ports.Set("db", 5432)
	w.Containers.Set("main", c)
	w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi"}})
	h1 := types.ServiceSpec{Type: "Headless"}
	h1.Ports.Set("db", 5432)
	w.Services.Set("db", h1)
	h2 := types.ServiceSpec{Type: "Headless"}
	h2.Ports.Set("db", 5432)
	w.Services.Set("db2", h2)

	_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("postgres"))
	if !errors.Is(err, ErrStatefulSetAmbiguousHeadlessService) {
		t.Fatalf("expected ErrStatefulSetAmbiguousHeadlessService; got %v", err)
	}
}

// ----- Category 5 — Each probe shape -----

func TestSynthesizeWorkload_AllProbeShapes(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	timeout := 5
	c.Health = &types.HealthSpec{
		Ready:   &types.ProbeSpec{HTTP: "/healthz", TimeoutSeconds: &timeout},
		Live:    &types.ProbeSpec{TCP: 8080},
		Startup: &types.ProbeSpec{Exec: []string{"/bin/check.sh"}},
	}
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "all-probe-shapes", concatBodies(files))
}

// TestSynthesizeWorkload_HTTPProbeNoNamedPort_TranslationError verifies that
// an http: probe on a container with no named ports fails with
// TRANSLATION_ERROR.
func TestSynthesizeWorkload_HTTPProbeNoNamedPort_TranslationError(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	// No ports declared.
	c.Health = &types.HealthSpec{Ready: &types.ProbeSpec{HTTP: "/healthz"}}
	w.Containers.Set("main", c)

	_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err == nil {
		t.Fatal("expected TRANSLATION_ERROR for http probe with no container ports")
	}
}

// ----- Category 6 — Scheduling + podSecurityContext + serviceAccount -----

func TestSynthesizeWorkload_SchedulingAndPodSecurity(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	w.Scheduling = &types.Scheduling{
		NodeSelector: map[string]string{"tier": "backend"},
	}
	uid, gid, fsg := 1000, 2000, 3000
	w.PodSecurityContext = &types.PodSecurityContext{
		RunAsUser:  &uid,
		RunAsGroup: &gid,
		FSGroup:    &fsg,
	}
	w.ServiceAccount = "app-sa"

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "scheduling-and-pod-security", concatBodies(files))
}

// ----- Category 7 — initContainers -----

func TestSynthesizeWorkload_InitContainers(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	w.InitContainers = []types.ContainerSpec{
		{Image: "migrator:1.0", Command: []string{"/migrate"}},
	}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "init-containers", concatBodies(files))
}

// TestSynthesizeWorkload_InitContainersNamed verifies that an authored init
// container name is emitted verbatim, while an unnamed entry falls back to
// "init-<index>" — where the index is the entry's POSITION in the list, not a
// count of unnamed entries. The first entry carries an authored name; the
// second is unnamed and lands at position 1, so it renders as init-1.
func TestSynthesizeWorkload_InitContainersNamed(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	w.InitContainers = []types.ContainerSpec{
		{Name: "prepare", Image: "prep:1.0", Command: []string{"/prepare"}},
		{Image: "migrator:1.0", Command: []string{"/migrate"}},
	}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	body := string(concatBodies(files))

	// The authored name is emitted verbatim, NOT init-0.
	if !strings.Contains(body, "name: prepare") {
		t.Errorf("authored init container name must be emitted; got:\n%s", body)
	}
	if strings.Contains(body, "name: init-0") {
		t.Errorf("named init container must not fall back to init-0; got:\n%s", body)
	}
	// The unnamed entry falls back to its positional index (init-1).
	if !strings.Contains(body, "name: init-1") {
		t.Errorf("unnamed init container must fall back to positional init-1; got:\n%s", body)
	}

	// Determinism: a second synthesis of the same input is byte-identical.
	files2, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload (second run): %v", err)
	}
	if string(concatBodies(files2)) != body {
		t.Errorf("synthesis is not byte-identical across runs")
	}

	goldenCheck(t, "init-containers-named", concatBodies(files))
}

// TestSynthesizeWorkload_NamedInitContainerParseValidateSynth drives the real
// parse → validate → synthesize path: a Workload YAML carrying a named init
// container is unmarshalled into the typed shape, validated against the
// embedded Workload JSON Schema, then synthesized. It proves the schema
// accepts the new name field (additionalProperties: false would reject an
// unknown key) and that the authored name flows through to the emitted output
// rather than being silently dropped.
func TestSynthesizeWorkload_NamedInitContainerParseValidateSynth(t *testing.T) {
	t.Parallel()
	const src = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
initContainers:
  - name: prepare
    image: busybox:1.36
    command: ["/prep"]
containers:
  main:
    image: app:1.0
    ports:
      web: 8080
`
	var w types.Workload
	if err := yaml.Unmarshal([]byte(src), &w); err != nil {
		t.Fatalf("unmarshal Workload: %v", err)
	}

	// The authored name must survive unmarshalling into the typed shape.
	if len(w.InitContainers) != 1 || w.InitContainers[0].Name != "prepare" {
		t.Fatalf("parsed init container name = %q (want %q)", func() string {
			if len(w.InitContainers) == 0 {
				return "<none>"
			}
			return w.InitContainers[0].Name
		}(), "prepare")
	}

	// Real schema validation: accepts the name field (proves the schema edit
	// propagated through the embedded schema; additionalProperties: false would
	// otherwise reject it).
	if err := validator.ValidateWorkload(&w, "", "", ""); err != nil {
		t.Fatalf("ValidateWorkload rejected a named init container: %v", err)
	}

	files, err := SynthesizeWorkloadCore(&w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if !strings.Contains(string(concatBodies(files)), "name: prepare") {
		t.Errorf("authored init container name must reach emitted output; got:\n%s", concatBodies(files))
	}
}

// ----- Category 7b — VolumeMount subPath -----

// TestSynthesizeWorkload_VolumeMountSubPathParseValidateSynth drives the real
// parse → validate → synthesize path for a container volumeMount carrying a
// subPath: a Workload YAML mounting a single file from a configmap volume is
// unmarshalled into the typed shape, validated against the embedded Workload
// JSON Schema, then synthesized. It proves the schema accepts the subPath field
// (additionalProperties: false would reject an unknown key) and that the value
// flows through to the emitted container volumeMounts[] entry rather than being
// silently dropped.
func TestSynthesizeWorkload_VolumeMountSubPathParseValidateSynth(t *testing.T) {
	t.Parallel()
	const src = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
volumes:
  config:
    configmap:
      source: app-config
containers:
  main:
    image: app:1.0
    ports:
      web: 8080
    volumeMounts:
      - name: config
        path: /usr/local/apisix/conf/config.yaml
        subPath: config.yaml
services:
  api:
    type: ClusterIP
    ports:
      web: 8080
`
	var w types.Workload
	if err := yaml.Unmarshal([]byte(src), &w); err != nil {
		t.Fatalf("unmarshal Workload: %v", err)
	}

	// The authored subPath must survive unmarshalling into the typed shape.
	main, ok := w.Containers.Get("main")
	if !ok || len(main.VolumeMounts) != 1 {
		t.Fatalf("expected one volumeMount on container main; got %+v", main.VolumeMounts)
	}
	if main.VolumeMounts[0].SubPath != "config.yaml" {
		t.Fatalf("parsed volumeMount subPath = %q (want %q)", main.VolumeMounts[0].SubPath, "config.yaml")
	}

	// Real schema validation: accepts the subPath field (proves the schema edit
	// propagated through the embedded schema; additionalProperties: false would
	// otherwise reject it).
	if err := validator.ValidateWorkload(&w, "", "", ""); err != nil {
		t.Fatalf("ValidateWorkload rejected a subPath volumeMount: %v", err)
	}

	files, err := SynthesizeWorkloadCore(&w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	body := string(concatBodies(files))
	if !strings.Contains(body, "subPath: config.yaml") {
		t.Errorf("authored subPath must reach the emitted container volumeMounts entry; got:\n%s", body)
	}

	// Determinism: a second synthesis of the same input is byte-identical.
	files2, err := SynthesizeWorkloadCore(&w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload (second run): %v", err)
	}
	if string(concatBodies(files2)) != body {
		t.Errorf("synthesis is not byte-identical across runs")
	}
}

// TestSynthesizeWorkload_VolumeMountNoSubPathOmitted verifies that a volumeMount
// with no subPath emits no subPath: key — the omitempty tag keeps output for
// existing Workloads byte-identical to a build without the field.
func TestSynthesizeWorkload_VolumeMountNoSubPathOmitted(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	c.VolumeMounts = []types.VolumeMount{{Name: "config", Path: "/etc/app"}}
	w.Containers.Set("main", c)
	w.Volumes.Set("config", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}})

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if strings.Contains(string(concatBodies(files)), "subPath") {
		t.Errorf("no subPath was authored; emitted output must carry no subPath key; got:\n%s", concatBodies(files))
	}
}

// ----- Category 8 — Secrets via env and file -----

func TestSynthesizeWorkload_SecretsEnvAndFile(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	c.Secrets.Set("DB_PASSWORD", types.SecretRef{From: "db-password", As: "env"})
	c.Secrets.Set("TLS_CERT", types.SecretRef{From: "tls-cert", As: "file", Path: "/etc/tls/cert.pem"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	// The synthesizer resolves each secret reference from the slot's provides:
	// `from` names the slot; the generated Secret's name and data key come from
	// the slot's resolved provides bag.
	tmplCtx := &types.TemplateContext{}
	tmplCtx.Secrets.Set("db-password", types.TemplateAttachment{Provides: providesBag("db-creds", "password")})
	tmplCtx.Secrets.Set("tls-cert", types.TemplateAttachment{Provides: providesBag("tls-bundle", "tls.crt")})

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(tmplCtx), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "secrets-env-and-file", concatBodies(files))
}

// TestSynthesizeWorkload_SecretsMultiKeyOneSlot verifies that two env secret
// entries referencing the same slot with different explicit `key` values each
// render one secretKeyRef — same secret name (the slot's single published
// locator), one data key per entry, author order preserved. This is the
// multi-key consumption shape: a slot filled by a referenceable-secret whose
// provides publishes only `name` (no `key`), with each entry supplying the
// data key it reads.
func TestSynthesizeWorkload_SecretsMultiKeyOneSlot(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	c.Secrets.Set("APP_DB_USER", types.SecretRef{From: "app-credentials", As: "env", Key: "db_username"})
	c.Secrets.Set("APP_DB_PASSWORD", types.SecretRef{From: "app-credentials", As: "env", Key: "db_password"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	// The slot's provides publishes only the locator (name) — a multi-key
	// referenceable secret carries several data keys the consumer already
	// knows, so it exposes no single `key`. Each entry supplies its own key.
	provides := types.OrderedMap[string]{}
	provides.Set("name", "app-creds")
	tmplCtx := &types.TemplateContext{}
	tmplCtx.Secrets.Set("app-credentials", types.TemplateAttachment{Provides: provides})

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(tmplCtx), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "secrets-multikey-one-slot", concatBodies(files))
}

// ----- Category 9 — LoadBalancer service -----

func TestSynthesizeWorkload_LoadBalancerService(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "gateway:1.0"}
	c.Ports.Set("https", 8443)
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "LoadBalancer"}
	svc.Ports.Set("https", 443)
	w.Services.Set("public", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("gateway"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "loadbalancer-service", concatBodies(files))
}

// ----- Category 10 — controller-shape form -----

// TestSynthesizeWorkload_FormDaemon verifies form: daemon → DaemonSet (apps/v1,
// no replicas).
func TestSynthesizeWorkload_FormDaemon(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "daemon"}
	c := types.ContainerSpec{Image: "agent:1.0"}
	c.Ports.Set("metrics", 9100)
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("metrics", 9100)
	w.Services.Set("metrics", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("node-agent"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if files[0].Name != "node-agent-daemonset.yaml" {
		t.Errorf("expected daemonset.yaml; got %q", files[0].Name)
	}
	goldenCheck(t, "form-daemon", concatBodies(files))
}

// TestSynthesizeWorkload_FormTask verifies form: task → Job (batch/v1) with a
// Job-valid pod restartPolicy.
func TestSynthesizeWorkload_FormTask(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "task"}
	c := types.ContainerSpec{Image: "migrator:1.0", Command: []string{"/migrate"}}
	w.Containers.Set("main", c)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("db-migrate"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if files[0].Name != "db-migrate-job.yaml" {
		t.Errorf("expected job.yaml; got %q", files[0].Name)
	}
	if !strings.Contains(string(files[0].Body), "restartPolicy: Never") {
		t.Errorf("Job pod template must pin a Job-valid restartPolicy; got:\n%s", files[0].Body)
	}
	goldenCheck(t, "form-task", concatBodies(files))
}

// TestSynthesizeWorkload_FormTaskOnFailurePreserved verifies an author-set
// OnFailure restartPolicy survives into the Job pod template.
func TestSynthesizeWorkload_FormTaskOnFailurePreserved(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "task"}
	c := types.ContainerSpec{Image: "worker:1.0", RestartPolicy: "OnFailure"}
	w.Containers.Set("main", c)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("worker"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if !strings.Contains(string(files[0].Body), "restartPolicy: OnFailure") {
		t.Errorf("author-set OnFailure must be preserved; got:\n%s", files[0].Body)
	}
}

// TestSynthesizeWorkload_FormScheduledHalts verifies form: scheduled with no
// schedule halts on the shared companion validation — a scheduled workload
// needs a schedule to emit a CronJob.
func TestSynthesizeWorkload_FormScheduledHalts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "scheduled"}
	c := types.ContainerSpec{Image: "reporter:1.0"}
	w.Containers.Set("main", c)

	_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("nightly"))
	if !errors.Is(err, rendercore.ErrScheduledFormRequiresSchedule) {
		t.Fatalf("expected ErrScheduledFormRequiresSchedule; got %v", err)
	}
}

// TestSynthesizeWorkload_FormScheduled verifies form: scheduled with an @-macro
// schedule → CronJob (batch/v1) whose spec.schedule carries the schedule
// verbatim.
func TestSynthesizeWorkload_FormScheduled(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "scheduled", Schedule: "@daily"}
	c := types.ContainerSpec{Image: "reporter:1.0", Command: []string{"/report"}}
	w.Containers.Set("main", c)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("nightly"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if files[0].Name != "nightly-cronjob.yaml" {
		t.Errorf("expected cronjob.yaml; got %q", files[0].Name)
	}
	if !strings.Contains(string(files[0].Body), "kind: CronJob") {
		t.Errorf("CronJob resource expected; got:\n%s", files[0].Body)
	}
	if !strings.Contains(string(files[0].Body), "schedule: '@daily'") {
		t.Errorf("spec.schedule must carry the workload schedule verbatim; got:\n%s", files[0].Body)
	}
	goldenCheck(t, "form-scheduled", concatBodies(files))
}

// TestSynthesizeWorkload_FormScheduledFiveField verifies the 5-field crontab
// path renders into spec.schedule.
func TestSynthesizeWorkload_FormScheduledFiveField(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "scheduled", Schedule: "0 0 * * *"}
	c := types.ContainerSpec{Image: "reporter:1.0"}
	w.Containers.Set("main", c)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("nightly"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if !strings.Contains(string(files[0].Body), "schedule: 0 0 * * *") {
		t.Errorf("spec.schedule must carry the 5-field cron expression; got:\n%s", files[0].Body)
	}
	goldenCheck(t, "form-scheduled-fivefield", concatBodies(files))
}

// TestSynthesizeWorkload_FormScheduledMalformedHalts verifies a malformed
// schedule on form: scheduled halts on the shared cron structural check.
func TestSynthesizeWorkload_FormScheduledMalformedHalts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "scheduled", Schedule: "0 0 * * * *"}
	c := types.ContainerSpec{Image: "reporter:1.0"}
	w.Containers.Set("main", c)

	_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("nightly"))
	if !errors.Is(err, rendercore.ErrInvalidCronSchedule) {
		t.Fatalf("expected ErrInvalidCronSchedule; got %v", err)
	}
}

// TestSynthesizeWorkload_ScheduleOnNonScheduledFormHalts verifies a schedule on
// a non-scheduled form halts on the reverse companion rule.
func TestSynthesizeWorkload_ScheduleOnNonScheduledFormHalts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "stateless", Schedule: "@daily"}
	c := types.ContainerSpec{Image: "app:1.0"}
	w.Containers.Set("main", c)

	_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("api"))
	if !errors.Is(err, rendercore.ErrScheduleRequiresScheduledForm) {
		t.Fatalf("expected ErrScheduleRequiresScheduledForm; got %v", err)
	}
}

// TestSynthesizeWorkload_PersistentTemplateRequiresStateful verifies the
// companion constraint fires when a persistentTemplate volume rides a
// non-stateful (here empty and daemon) form.
func TestSynthesizeWorkload_PersistentTemplateRequiresStateful(t *testing.T) {
	t.Parallel()
	for _, form := range []string{"", "daemon"} {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form}
		c := types.ContainerSpec{Image: "db:1.0"}
		w.Containers.Set("main", c)
		w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi"}})

		_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("db"))
		if !errors.Is(err, rendercore.ErrPersistentTemplateRequiresStatefulForm) {
			t.Errorf("form %q with persistentTemplate: expected ErrPersistentTemplateRequiresStatefulForm; got %v", form, err)
		}
	}
}

// TestSynthesizeWorkload_ByteIdenticalAcrossRuns verifies the
// byte-for-byte determinism contract: the same Workload input produces
// the same output on every compile. Catches non-deterministic iteration
// that might creep in via Go map ranges.
func TestSynthesizeWorkload_ByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	nodeCtx := nodeCtxFor("api-server")

	files1, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtx)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	for i := 0; i < 5; i++ {
		filesN, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtx)
		if err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
		if len(filesN) != len(files1) {
			t.Errorf("run %d file count differs: got %d, want %d", i+1, len(filesN), len(files1))
			continue
		}
		for j, f := range files1 {
			if filesN[j].Name != f.Name {
				t.Errorf("run %d file[%d] name differs: got %q, want %q", i+1, j, filesN[j].Name, f.Name)
			}
			if string(filesN[j].Body) != string(f.Body) {
				t.Errorf("run %d file[%d] body byte-mismatch", i+1, j)
			}
		}
	}
}

// TestSynthesizeWorkload_ImagePullPolicyAndEnvFrom — imagePullPolicy renders on
// the container; envFrom imports a ConfigMap source (configMapRef by name) and
// a Secret source (secretRef whose name resolves from the slot provides, with a
// per-source prefix).
func TestSynthesizeWorkload_ImagePullPolicyAndEnvFrom(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0", ImagePullPolicy: "IfNotPresent"}
	c.Ports.Set("web", 8080)
	c.EnvFrom = []types.EnvFromSource{
		{ConfigMap: "app-config"},
		{Secret: "db-credentials", Prefix: "DB_"},
	}
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	secretProvides := map[string]types.OrderedMap[string]{
		"db-credentials": providesBag("db-creds-secret", "password"),
	}
	files, err := SynthesizeWorkloadCore(w, secretProvides, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "imagepullpolicy-and-envfrom", files[0].Body)
}

// TestSynthesizeWorkload_PodLevelFieldsAndAccessModes — the new pod-level fields
// render on the PodSpec (imagePullSecrets, priorityClassName,
// terminationGracePeriodSeconds, dnsPolicy, dnsConfig, hostAliases) and an
// author-supplied PVC accessModes overrides the ReadWriteOnce default.
func TestSynthesizeWorkload_PodLevelFieldsAndAccessModes(t *testing.T) {
	t.Parallel()
	grace := 45
	w := &types.Workload{
		APIVersion:                    "aggrega.ai/v1alpha1",
		Kind:                          "Workload",
		Form:                          "stateful",
		ImagePullSecrets:              []string{"regcred", "regcred-2"},
		PriorityClassName:             "high-priority",
		TerminationGracePeriodSeconds: &grace,
		DNSPolicy:                     "None",
		DNSConfig: &types.DNSConfig{
			Nameservers: []string{"1.1.1.1", "8.8.8.8"},
			Searches:    []string{"svc.cluster.local"},
		},
		HostAliases: []types.HostAlias{
			{IP: "10.0.0.1", Hostnames: []string{"db.internal", "cache.internal"}},
		},
	}
	c := types.ContainerSpec{Image: "db:1.0"}
	c.Ports.Set("db", 5432)
	c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/var/lib/db"}}
	w.Containers.Set("main", c)
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi", AccessModes: []string{"ReadWriteMany"}}})

	svc := types.ServiceSpec{Type: "Headless"}
	svc.Ports.Set("db", 5432)
	w.Services.Set("db", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("db"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	// Golden the full multi-file emission (statefulset + pvc + service).
	var all []byte
	for _, f := range files {
		all = append(all, []byte("--- "+f.Name+" ---\n")...)
		all = append(all, f.Body...)
	}
	goldenCheck(t, "pod-fields-and-accessmodes", all)
}

// TestSynthesizeWorkload_ReferenceExternalName — a form: reference workload emits
// a single ExternalName Service (no controller, no PVC, no Secret). The endpoint
// host is extracted from a scheme/port-bearing URL; the optional port renders.
func TestSynthesizeWorkload_ReferenceExternalName(t *testing.T) {
	t.Parallel()
	w := &types.Workload{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Workload",
		Form:       "reference",
		Endpoint:   "https://api.anthropic.com:443/v1",
		Port:       443,
		Credential: &types.Credential{From: "api_key", Env: "ANTHROPIC_API_KEY"},
	}
	files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("anthropic"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly 1 emitted file (ExternalName Service), got %d", len(files))
	}
	goldenCheck(t, "reference-externalname", files[0].Body)
}

// TestSynthesizeWorkload_ReferenceRequiresEndpoint — a reference workload with no
// endpoint halts on the shared companion validation.
func TestSynthesizeWorkload_ReferenceRequiresEndpoint(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "reference"}
	_, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("x"))
	if !errors.Is(err, rendercore.ErrReferenceFormRequiresEndpoint) {
		t.Errorf("expected ErrReferenceFormRequiresEndpoint, got %v", err)
	}
}

// TestExternalNameHost — host extraction strips scheme, path, and port.
func TestExternalNameHost(t *testing.T) {
	cases := map[string]string{
		"https://api.anthropic.com:443/v1": "api.anthropic.com",
		"api.anthropic.com":                "api.anthropic.com",
		"http://db.internal":               "db.internal",
		"db.internal:5432":                 "db.internal",
	}
	for in, want := range cases {
		if got := externalNameHost(in); got != want {
			t.Errorf("externalNameHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSynthesizeWorkload_ConditionalFormBranches — a persistence-driven
// conditional form (form: "{{ if .values.persistence.enabled }}stateful{{ else
// }}stateless{{ end }}") resolves upstream to a literal form before synthesis;
// this regression confirms both branches that conditional can resolve to render
// the expected controller AND pass Workload validation, so a component may drive
// form from a persistence value without changing component.
func TestSynthesizeWorkload_ConditionalFormBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		form     string
		wantKind string
	}{
		{"stateful", "StatefulSet"},
		{"stateless", "Deployment"},
	}
	for _, tc := range cases {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: tc.form}
		c := types.ContainerSpec{Image: "db:1.0"}
		c.Ports.Set("db", 5432)
		if tc.form == "stateful" {
			c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/var/lib/db"}}
			w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi"}})
		}
		w.Containers.Set("main", c)
		svc := types.ServiceSpec{Type: "Headless"}
		svc.Ports.Set("db", 5432)
		w.Services.Set("db", svc)

		if err := validator.ValidateWorkload(w, "", "", ""); err != nil {
			t.Fatalf("form %q: ValidateWorkload: %v", tc.form, err)
		}
		files, err := SynthesizeWorkloadCore(w, renderer.SecretProvidesFromContext(nil), nodeCtxFor("db"))
		if err != nil {
			t.Fatalf("form %q: SynthesizeWorkload: %v", tc.form, err)
		}
		if !strings.Contains(string(files[0].Body), "kind: "+tc.wantKind) {
			t.Errorf("form %q: expected kind %q in first emitted file, got:\n%s", tc.form, tc.wantKind, files[0].Body)
		}
	}
}
