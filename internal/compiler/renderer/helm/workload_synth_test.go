// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/types"
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
	engine := &HelmEngine{}
	files, err := engine.SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
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

// TestSynthesizeWorkload_NilWorkload verifies the no-op case for the
// dispatcher's nil-safe behaviour.
func TestSynthesizeWorkload_NilWorkload(t *testing.T) {
	t.Parallel()
	engine := &HelmEngine{}
	files, err := engine.SynthesizeWorkload(context.Background(), nil, nil, nodeCtxFor("ignored"))
	if err != nil {
		t.Errorf("nil Workload should produce no error, got %v", err)
	}
	if len(files) != 0 {
		t.Errorf("nil Workload should produce no files, got %d", len(files))
	}
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

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
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

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("data-server"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "all-volume-variants", concatBodies(files))
}

// ----- Category 4 — persistentTemplate triggers StatefulSet -----

func TestSynthesizeWorkload_PersistentTemplateTriggersStatefulSet(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "db:1.0"}
	c.Ports.Set("db", 5432)
	w.Containers.Set("main", c)

	w.Volumes.Set("data", types.VolumeSpec{
		PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi", Class: "gp3"},
	})

	svc := types.ServiceSpec{Type: "Headless"}
	svc.Ports.Set("db", 5432)
	w.Services.Set("db", svc)

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("postgres"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}

	// Must emit StatefulSet, NOT Deployment.
	if files[0].Name != "postgres-statefulset.yaml" {
		t.Errorf("expected statefulset.yaml; got %q", files[0].Name)
	}
	goldenCheck(t, "persistent-template-statefulset", concatBodies(files))
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

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
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

	_, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
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

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
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

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "init-containers", concatBodies(files))
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

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "secrets-env-and-file", concatBodies(files))
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

	files, err := (&HelmEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("gateway"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "loadbalancer-service", concatBodies(files))
}

// TestSynthesizeWorkload_ByteIdenticalAcrossRuns verifies the
// byte-for-byte determinism contract: the same Workload input produces
// the same output on every compile. Catches non-deterministic iteration
// that might creep in via Go map ranges.
func TestSynthesizeWorkload_ByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	nodeCtx := nodeCtxFor("api-server")
	engine := &HelmEngine{}

	files1, err := engine.SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	for i := 0; i < 5; i++ {
		filesN, err := engine.SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
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
