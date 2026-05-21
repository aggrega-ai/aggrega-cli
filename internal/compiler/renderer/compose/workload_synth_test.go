// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// goldenCheck compares actual against testdata/workload/<name>.golden.yaml.
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

// nodeCtxFor returns a minimal NodeRenderContext for compose tests.
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

// minimalWorkload — single container, ClusterIP service. Smoke test.
func minimalWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)
	return w
}

func TestSynthesizeWorkload_MinimalSingleContainer(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	nodeCtx := nodeCtxFor("api-server")
	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 fragment file, got %d", len(files))
	}
	if files[0].Name != "api-server.compose-fragment.yaml" {
		t.Errorf("fragment name: got %q", files[0].Name)
	}
	goldenCheck(t, "minimal-single-container", files[0].Body)
}

func TestSynthesizeWorkload_NilWorkload_ReturnsNil(t *testing.T) {
	t.Parallel()
	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, nil, nodeCtxFor("x"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if files != nil {
		t.Errorf("expected nil files for nil workload, got %v", files)
	}
}

// TestSynthesizeWorkload_MultiContainerMainAndSidecar — main + sidecar.
// Main is named "main"; sidecar shares the main's network namespace via
// network_mode: "service:<main>".
func TestSynthesizeWorkload_MultiContainerMainAndSidecar(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	main := types.ContainerSpec{Image: "api:2.0"}
	main.Ports.Set("http", 8080)
	w.Containers.Set("main", main)

	sidecar := types.ContainerSpec{Image: "logger:1.0"}
	w.Containers.Set("logger", sidecar)

	pubSvc := types.ServiceSpec{Type: "NodePort"}
	pubSvc.Ports.Set("http", 8080)
	w.Services.Set("public", pubSvc)

	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "multi-container-main-and-sidecar", files[0].Body)
}

// TestSynthesizeWorkload_AllVolumeVariants — exercises ephemeral, configmap,
// secret, persistent, persistentTemplate. Verifies the degraded translations
// per the package godoc.
func TestSynthesizeWorkload_AllVolumeVariants(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "store:1.0"}
	c.Ports.Set("api", 9000)
	c.VolumeMounts = []types.VolumeMount{
		{Name: "scratch", Path: "/tmp/scratch"},
		{Name: "config", Path: "/etc/config"},
		{Name: "creds", Path: "/run/secrets/creds"},
		{Name: "data", Path: "/var/data"},
		{Name: "session", Path: "/var/session"},
	}
	w.Containers.Set("main", c)

	w.Volumes.Set("scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "100Mi"}})
	w.Volumes.Set("config", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}})
	w.Volumes.Set("creds", types.VolumeSpec{Secret: &types.SecretVolume{Source: "app-creds"}})
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi"}})
	w.Volumes.Set("session", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}})

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("api", 9000)
	w.Services.Set("internal", svc)

	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("store"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "all-volume-variants", files[0].Body)
}

// TestSynthesizeWorkload_AllProbeShapes — exercises http, tcp, exec probe
// translations. Each in a separate container so all three shapes appear in
// one golden.
func TestSynthesizeWorkload_AllProbeShapes(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	timeoutSecs := 3
	intervalSecs := 10
	retries := 5
	startSecs := 30

	httpC := types.ContainerSpec{Image: "http-probe:1.0"}
	httpC.Ports.Set("http", 8080)
	httpC.Health = &types.HealthSpec{
		Live: &types.ProbeSpec{
			HTTP:                "/healthz",
			TimeoutSeconds:      &timeoutSecs,
			PeriodSeconds:       &intervalSecs,
			FailureThreshold:    &retries,
			InitialDelaySeconds: &startSecs,
		},
	}
	w.Containers.Set("main", httpC)

	tcpC := types.ContainerSpec{Image: "tcp-probe:1.0"}
	tcpC.Ports.Set("rpc", 50051)
	tcpC.Health = &types.HealthSpec{Live: &types.ProbeSpec{TCP: 50051}}
	w.Containers.Set("tcp-side", tcpC)

	execC := types.ContainerSpec{Image: "exec-probe:1.0"}
	execC.Health = &types.HealthSpec{Live: &types.ProbeSpec{Exec: []string{"check.sh", "--ready"}}}
	w.Containers.Set("exec-side", execC)

	pubSvc := types.ServiceSpec{Type: "ClusterIP"}
	pubSvc.Ports.Set("http", 8080)
	w.Services.Set("public", pubSvc)

	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("multi"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "all-probe-shapes", files[0].Body)
}

// TestSynthesizeWorkload_HTTPProbeNoPort_Errors — http probe needs a port.
// Container with no ports + http probe should TRANSLATION_ERROR.
func TestSynthesizeWorkload_HTTPProbeNoPort_Errors(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{
		Image: "noports:1.0",
		Health: &types.HealthSpec{
			Live: &types.ProbeSpec{HTTP: "/health"},
		},
	}
	w.Containers.Set("main", c)

	_, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("x"))
	if err == nil {
		t.Fatal("expected TRANSLATION_ERROR for http probe with no ports")
	}
	if !errors.Is(err, ErrTranslationProbeNoPort) {
		t.Errorf("expected ErrTranslationProbeNoPort, got %v", err)
	}
}

// TestSynthesizeWorkload_SecretsAsEnvAndFile — both secret reference modes
// in one Workload. as: env produces an environment entry; as: file produces
// a bind-mount.
func TestSynthesizeWorkload_SecretsAsEnvAndFile(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", 8080)
	c.Secrets.Set("DB_PASSWORD", types.SecretRef{From: "db-credentials", As: "env"})
	c.Secrets.Set("tls-cert", types.SecretRef{From: "tls-cert", As: "file", Path: "/etc/tls/server.crt"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "secrets-env-and-file", files[0].Body)
}

// TestSynthesizeWorkload_ServiceTypeNodePort_PublishesPorts — NodePort triggers
// host:container port publishing on the main service.
func TestSynthesizeWorkload_ServiceTypeNodePort_PublishesPorts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "web:1.0"}
	c.Ports.Set("http", 8080)
	c.Ports.Set("metrics", 9090)
	w.Containers.Set("main", c)

	pub := types.ServiceSpec{Type: "NodePort"}
	pub.Ports.Set("http", 8080)
	w.Services.Set("frontend", pub)

	internal := types.ServiceSpec{Type: "ClusterIP"}
	internal.Ports.Set("metrics", 9090)
	w.Services.Set("metrics-only", internal)

	files, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtxFor("web"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "service-type-nodeport-publishes-ports", files[0].Body)
}

// TestSynthesizeWorkload_ByteIdenticalAcrossRuns — determinism check.
// Synthesizing the same Workload twice must produce byte-identical output.
func TestSynthesizeWorkload_ByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	nodeCtx := nodeCtxFor("api-server")
	files1, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	files2, err := (&ComposeEngine{}).SynthesizeWorkload(context.Background(), nil, w, nodeCtx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if string(files1[0].Body) != string(files2[0].Body) {
		t.Errorf("byte-identical-across-runs guard failed:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", files1[0].Body, files2[0].Body)
	}
}
