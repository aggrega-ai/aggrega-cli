// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
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
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)
	return w
}

// TestSynthesizeWorkload_GPUFieldsDegrade proves the compose unsupported-field
// contract for the two GPU-only Workload surfaces: cpu/memory still translate to
// deploy.resources, while extended-resource keys (nvidia.com/gpu) and
// runtimeClassName have no compose analog and are omitted from the rendered
// bytes. The omission is out-of-band-warned (asserted in the unsupported-field tests),
// not silent, and never halts. This test locks the rendered bytes.
func TestSynthesizeWorkload_GPUFieldsDegrade(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	w.RuntimeClassName = "nvidia"
	c := types.ContainerSpec{Image: "vllm:latest"}
	c.Ports.Set("http", types.PortSpec{Port: 8000})
	limits := &types.ResourceList{CPU: "4", Memory: "16Gi"}
	limits.Extended.Set("nvidia.com/gpu", "1")
	requests := &types.ResourceList{}
	requests.Extended.Set("nvidia.com/gpu", "1")
	c.Resources = &types.ResourceSpec{Requests: requests, Limits: limits}
	w.Containers.Set("main", c)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("inference"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	body := string(files[0].Body)
	if strings.Contains(body, "nvidia.com/gpu") {
		t.Errorf("extended-resource key must be omitted on compose; got:\n%s", body)
	}
	if strings.Contains(body, "runtimeClassName") || strings.Contains(body, "nvidia") {
		t.Errorf("runtimeClassName must be omitted on compose; got:\n%s", body)
	}
	if !strings.Contains(body, "cpus:") || !strings.Contains(body, "memory:") {
		t.Errorf("cpu/memory must still translate to deploy.resources; got:\n%s", body)
	}
	goldenCheck(t, "gpu-fields-degrade", files[0].Body)
}

// TestSynthesizeWorkload_ImageRegistryAnnotation proves the compose engine honors
// aggrega.ai/image-registry, rewriting the service image under the prefix, and
// leaves the image untouched when the annotation is absent.
func TestSynthesizeWorkload_ImageRegistryAnnotation(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "docker.io/library/postgres:16"}
	c.Ports.Set("db", types.PortSpec{Port: 5432})
	w.Containers.Set("main", c)

	plainFiles, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("db"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (plain): %v", err)
	}
	if plain := string(plainFiles[0].Body); !strings.Contains(plain, "image: docker.io/library/postgres:16") {
		t.Errorf("absent annotation should leave image unchanged; got:\n%s", plain)
	}

	ctx := nodeCtxFor("db")
	ctx.Annotations = map[string]string{rendercore.AnnotationImageRegistry: "reg.internal:5000"}
	regFiles, _, err := SynthesizeWorkloadCore(w, ctx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (registry): %v", err)
	}
	if reg := string(regFiles[0].Body); !strings.Contains(reg, "image: reg.internal:5000/library/postgres:16") {
		t.Errorf("image not rewritten under prefix; got:\n%s", reg)
	}
}

func TestSynthesizeWorkload_MinimalSingleContainer(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	nodeCtx := nodeCtxFor("api-server")
	files, _, err := SynthesizeWorkloadCore(w, nodeCtx)
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

// TestSynthesizeWorkload_MultiContainerMainAndSidecar — main + sidecar.
// Main is named "main"; sidecar shares the main's network namespace via
// network_mode: "service:<main>".
func TestSynthesizeWorkload_MultiContainerMainAndSidecar(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	main := types.ContainerSpec{Image: "api:2.0"}
	main.Ports.Set("http", types.PortSpec{Port: 8080})
	w.Containers.Set("main", main)

	sidecar := types.ContainerSpec{Image: "logger:1.0"}
	w.Containers.Set("logger", sidecar)

	pubSvc := types.ServiceSpec{Type: "NodePort"}
	pubSvc.Ports.Set("http", 8080)
	w.Services.Set("public", pubSvc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
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
	// persistentTemplate is a companion of form: stateful.
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "stateful"}

	c := types.ContainerSpec{Image: "store:1.0"}
	c.Ports.Set("api", types.PortSpec{Port: 9000})
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

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("store"))
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
	httpC.Ports.Set("http", types.PortSpec{Port: 8080})
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
	tcpC.Ports.Set("rpc", types.PortSpec{Port: 50051})
	tcpC.Health = &types.HealthSpec{Live: &types.ProbeSpec{TCP: 50051}}
	w.Containers.Set("tcp-side", tcpC)

	execC := types.ContainerSpec{Image: "exec-probe:1.0"}
	execC.Health = &types.HealthSpec{Live: &types.ProbeSpec{Exec: []string{"check.sh", "--ready"}}}
	w.Containers.Set("exec-side", execC)

	pubSvc := types.ServiceSpec{Type: "ClusterIP"}
	pubSvc.Ports.Set("http", 8080)
	w.Services.Set("public", pubSvc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("multi"))
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

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("x"))
	if err == nil {
		t.Fatal("expected TRANSLATION_ERROR for http probe with no ports")
	}
	// The condition names the KIND of failure, and the sentence names which
	// input compose could not resolve. Both are asserted: the condition alone is
	// shared with every other unresolved-input halt this core raises, so a green
	// on it would no longer tell this failure from those.
	if !errors.Is(err, wasm.ErrPluginRequiredInputUnresolved()) {
		t.Errorf("expected the unresolved-required-input condition, got %v", err)
	}
	if want := "probe http: requires at least one container port; container declares none"; !strings.Contains(errorChainText(err), want) {
		t.Errorf("the halt no longer says which input could not be resolved.\n  want: %s\n  got:  %v", want, err)
	}
}

// TestSynthesizeWorkload_SecretsAsEnvAndFile — both secret reference modes
// in one Workload. as: env produces an environment entry; as: file produces
// a bind-mount.
func TestSynthesizeWorkload_SecretsAsEnvAndFile(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	c.Attachments.Set("DB_PASSWORD", types.AttachmentRef{From: "db-credentials", As: "env"})
	c.Attachments.Set("tls-cert", types.AttachmentRef{From: "tls-cert", As: "file", Path: "/etc/tls/server.crt"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "secrets-env-and-file", files[0].Body)
}

// TestSynthesizeWorkload_EnvAuthorOrder — the rendered environment: block keeps
// the container's author order, not alphabetical. Variables are declared
// ZEBRA, ALPHA, MIDDLE and a secret env entry NESTED appends last; the golden
// must list them in that sequence, proving the Invariant-8 author-order clause
// (no map-sort substituted for the OrderedMap).
func TestSynthesizeWorkload_EnvAuthorOrder(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Variables.Set("ZEBRA", "z")
	c.Variables.Set("ALPHA", "a")
	c.Variables.Set("MIDDLE", "m")
	c.Attachments.Set("NESTED", types.AttachmentRef{From: "db-credentials", As: "env"})
	w.Containers.Set("main", c)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "env-author-order", files[0].Body)
}

// TestSynthesizeWorkload_CommandAndArgs — a container's command and args map to
// compose entrypoint and command respectively. K8s command (entrypoint
// override) → compose entrypoint; K8s args (entrypoint arguments) → compose
// command. Each renders as a YAML list.
func TestSynthesizeWorkload_CommandAndArgs(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Command = []string{"/bin/server", "--config"}
	c.Args = []string{"/etc/app/config.yaml", "--verbose"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "command-and-args", files[0].Body)

	// Round-trip the rendered fragment and assert the list forms survive: the
	// k8s command lands on compose entrypoint, the k8s args on compose command.
	var rt ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &rt); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	got, ok := rt.Services["api"]
	if !ok {
		t.Fatalf("service %q missing from rendered fragment", "api")
	}
	wantEntrypoint := []string{"/bin/server", "--config"}
	wantCommand := []string{"/etc/app/config.yaml", "--verbose"}
	if !reflect.DeepEqual(got.Entrypoint, wantEntrypoint) {
		t.Errorf("entrypoint: got %#v, want %#v", got.Entrypoint, wantEntrypoint)
	}
	if !reflect.DeepEqual(got.Command, wantCommand) {
		t.Errorf("command: got %#v, want %#v", got.Command, wantCommand)
	}
}

// TestSynthesizeWorkload_ServiceTypeNodePort_PublishesPorts — NodePort triggers
// host:container port publishing on the main service.
func TestSynthesizeWorkload_ServiceTypeNodePort_PublishesPorts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "web:1.0"}
	c.Ports.Set("http", types.PortSpec{Port: 8080})
	c.Ports.Set("metrics", types.PortSpec{Port: 9090})
	w.Containers.Set("main", c)

	pub := types.ServiceSpec{Type: "NodePort"}
	pub.Ports.Set("http", 8080)
	w.Services.Set("frontend", pub)

	internal := types.ServiceSpec{Type: "ClusterIP"}
	internal.Ports.Set("metrics", 9090)
	w.Services.Set("metrics-only", internal)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("web"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "service-type-nodeport-publishes-ports", files[0].Body)
}

// TestSynthesizeWorkload_FormStatefulRendersService — form: stateful renders a
// service plus the named volume, byte-identical to the pre-form stateful path.
func TestSynthesizeWorkload_FormStatefulRendersService(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "stateful"}
	c := types.ContainerSpec{Image: "db:1.0"}
	c.Ports.Set("db", types.PortSpec{Port: 5432})
	c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/var/lib/db"}}
	w.Containers.Set("main", c)
	w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi"}})

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("db", 5432)
	w.Services.Set("db", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("db-server"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "form-stateful", files[0].Body)
}

// TestSynthesizeWorkload_UnsupportedFormsHalt — daemon, scheduled, and task have
// no clean compose mapping and halt loudly.
func TestSynthesizeWorkload_UnsupportedFormsHalt(t *testing.T) {
	t.Parallel()
	for _, form := range []string{"daemon", "scheduled", "task"} {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form}
		// form: scheduled requires a schedule to clear the shared companion
		// validation that runs before the compose form switch; give it a valid
		// one so this case reaches compose's own unsupported-form halt.
		if form == "scheduled" {
			w.Schedule = "@daily"
		}
		c := types.ContainerSpec{Image: "app:1.0"}
		w.Containers.Set("main", c)

		_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("x"))
		if !errors.Is(err, wasm.ErrPluginCannotExpressInput()) {
			t.Errorf("form %q: expected the cannot-express condition; got %v", form, err)
		}
		if want := "compose cannot express this workload form"; !strings.Contains(err.Error(), want) {
			t.Errorf("form %q: the halt no longer says which construct has no expression.\n  want: %s\n  got:  %v", form, want, err)
		}
	}
}

// TestSynthesizeWorkload_PersistentTemplateRequiresStateful — a persistentTemplate
// volume on a non-stateful form halts on the shared companion validation.
func TestSynthesizeWorkload_PersistentTemplateRequiresStateful(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"} // empty form → stateless
	c := types.ContainerSpec{Image: "db:1.0"}
	w.Containers.Set("main", c)
	w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "10Gi"}})

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("db"))
	if !errors.Is(err, rendercore.ErrPersistentTemplateRequiresStatefulForm()) {
		t.Fatalf("expected ErrPersistentTemplateRequiresStatefulForm; got %v", err)
	}
}

// TestSynthesizeWorkload_ContainerResources — container resource requests and
// limits render to the compose deploy.resources block: requests become
// reservations, limits become limits, cpu millicpu converts to fractional
// cores, memory converts to an exact byte count.
func TestSynthesizeWorkload_ContainerResources(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	c.Resources = &types.ResourceSpec{
		Requests: &types.ResourceList{CPU: "250m", Memory: "128Mi"},
		Limits:   &types.ResourceList{CPU: "1", Memory: "256Mi"},
	}
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "container-resources", files[0].Body)
}

// TestSynthesizeWorkload_LifecycleHalts — a container declaring lifecycle hooks
// halts loudly on a compose target (compose has no lifecycle-hook primitive).
func TestSynthesizeWorkload_LifecycleHalts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Lifecycle = &types.LifecycleSpec{
		PreStop: &types.LifecycleHookSpec{Exec: []string{"/bin/drain"}},
	}
	w.Containers.Set("main", c)

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("x"))
	if !errors.Is(err, wasm.ErrPluginCannotExpressInput()) {
		t.Errorf("expected the cannot-express condition, got %v", err)
	}
	if want := "compose has no lifecycle-hook primitive; container declares lifecycle hooks"; !strings.Contains(errorChainText(err), want) {
		t.Errorf("the halt no longer says which construct has no expression.\n  want: %s\n  got:  %v", want, err)
	}
}

// TestSynthesizeWorkload_UnconvertibleResourceQuantityHalts — a memory quantity
// that is not a whole number of bytes has no exact compose byte count and halts
// rather than round, and the halt names the container's authored path.
func TestSynthesizeWorkload_UnconvertibleResourceQuantityHalts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Resources = &types.ResourceSpec{Limits: &types.ResourceList{Memory: "1500m"}}
	w.Containers.Set("main", c)

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("x"))
	if !errors.Is(err, wasm.ErrPluginValueNotRepresentable()) {
		t.Errorf("expected the value-not-representable condition, got %v", err)
	}
	for _, want := range []string{
		"resources quantity cannot be converted to a compose deploy.resources value",
		"containers.main.resources.limits.memory",
		"a quantity compose cannot represent exactly",
	} {
		if !strings.Contains(errorChainText(err), want) {
			t.Errorf("the halt no longer says which value has no representation.\n  want: %s\n  got:  %v", want, err)
		}
	}
}

// TestSynthesizeWorkload_InitContainerQuantityHaltNamesItsPosition — an init
// container is located by its list position, so a refusal of one of its
// quantities names that position rather than a container name.
func TestSynthesizeWorkload_InitContainerQuantityHaltNamesItsPosition(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	w.InitContainers = []types.ContainerSpec{{
		Image:     "migrate:1.0",
		Resources: &types.ResourceSpec{Requests: &types.ResourceList{CPU: "lots"}},
	}}

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("x"))
	if !errors.Is(err, wasm.ErrPluginValueNotRepresentable()) {
		t.Fatalf("expected the value-not-representable condition, got %v", err)
	}
	if want := "initContainers[0].resources.requests.cpu"; !strings.Contains(errorChainText(err), want) {
		t.Errorf("the halt does not name the init container's position.\n  want: %s\n  got:  %v", want, err)
	}
}

// TestSynthesizeWorkload_ByteIdenticalAcrossRuns — determinism check.
// Synthesizing the same Workload twice must produce byte-identical output.
func TestSynthesizeWorkload_ByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	nodeCtx := nodeCtxFor("api-server")
	files1, _, err := SynthesizeWorkloadCore(w, nodeCtx)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	files2, _, err := SynthesizeWorkloadCore(w, nodeCtx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if string(files1[0].Body) != string(files2[0].Body) {
		t.Errorf("byte-identical-across-runs guard failed:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", files1[0].Body, files2[0].Body)
	}
}

// TestSynthesizeWorkload_ImagePullPolicy — imagePullPolicy maps to the compose
// service pull_policy (IfNotPresent → if_not_present).
func TestSynthesizeWorkload_ImagePullPolicy(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0", ImagePullPolicy: "IfNotPresent"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "imagepullpolicy", files[0].Body)
}

// TestSynthesizeWorkload_EnvFromHalts — envFrom has no compose env-from-object
// primitive and halts loudly.
func TestSynthesizeWorkload_EnvFromHalts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.EnvFrom = []types.EnvFromSource{{ConfigMap: "app-config"}}
	w.Containers.Set("main", c)

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("x"))
	if !errors.Is(err, wasm.ErrPluginCannotExpressInput()) {
		t.Errorf("expected the cannot-express condition, got %v", err)
	}
	if want := "compose has no env-from-object primitive; container declares envFrom"; !strings.Contains(errorChainText(err), want) {
		t.Errorf("the halt no longer says which construct has no expression.\n  want: %s\n  got:  %v", want, err)
	}
}

// TestSynthesizeWorkload_PodLevelFields — terminationGracePeriodSeconds maps to
// stop_grace_period, dnsConfig to dns/dns_search, and hostAliases to extra_hosts;
// imagePullSecrets/priorityClassName/dnsPolicy are helm-only and omitted.
func TestSynthesizeWorkload_PodLevelFields(t *testing.T) {
	t.Parallel()
	grace := 45
	w := &types.Workload{
		APIVersion:                    "aggrega.ai/v1alpha1",
		Kind:                          "Workload",
		ImagePullSecrets:              []string{"regcred"}, // helm-only; omitted on compose
		PriorityClassName:             "high-priority",     // helm-only; omitted on compose
		DNSPolicy:                     "None",              // helm-only; omitted on compose
		TerminationGracePeriodSeconds: &grace,
		DNSConfig: &types.DNSConfig{
			Nameservers: []string{"1.1.1.1", "8.8.8.8"},
			Searches:    []string{"svc.cluster.local"},
		},
		HostAliases: []types.HostAlias{
			{IP: "10.0.0.1", Hostnames: []string{"db.internal", "cache.internal"}},
		},
	}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	w.Containers.Set("main", c)
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("api", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	goldenCheck(t, "pod-level-fields", files[0].Body)
}

// TestSynthesizeWorkload_ReferenceEmitsNoFragment — a form: reference workload
// has no compose analog; compose emits no fragment (the endpoint reaches
// consumers through the recipe provides).
func TestSynthesizeWorkload_ReferenceEmitsNoFragment(t *testing.T) {
	t.Parallel()
	w := &types.Workload{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Workload",
		Form:       "reference",
		Endpoint:   "https://api.anthropic.com",
	}
	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("anthropic"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no fragment for a reference workload, got %d files", len(files))
	}
}

// TestSynthesizeWorkload_EndpointRequiresReferenceForm — an endpoint on a
// non-reference form halts on the shared companion validation.
func TestSynthesizeWorkload_EndpointRequiresReferenceForm(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Endpoint: "api.example.com"}
	c := types.ContainerSpec{Image: "app:1.0"}
	w.Containers.Set("main", c)
	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("x"))
	if !errors.Is(err, rendercore.ErrEndpointRequiresReferenceForm()) {
		t.Errorf("expected ErrEndpointRequiresReferenceForm, got %v", err)
	}
}

// TestSynthesizeWorkload_ServiceAccountAnnotationNoOp proves the K8s-only
// aggrega.ai/service-account annotation is a silent no-op on compose: a compose
// render of a Workload carrying the annotation is byte-identical to the same
// Workload without it. Compose has no ServiceAccount concept.
func TestSynthesizeWorkload_ServiceAccountAnnotationNoOp(t *testing.T) {
	t.Parallel()
	base, _, err := SynthesizeWorkloadCore(minimalWorkload(), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (no annotation): %v", err)
	}
	ctx := nodeCtxFor("api")
	ctx.Annotations = map[string]string{rendercore.AnnotationServiceAccount: "op-sa"}
	withAnn, _, err := SynthesizeWorkloadCore(minimalWorkload(), ctx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (with annotation): %v", err)
	}
	if string(base[0].Body) != string(withAnn[0].Body) {
		t.Errorf("compose output changed with the service-account annotation; must be a no-op\n--- without ---\n%s\n--- with ---\n%s", base[0].Body, withAnn[0].Body)
	}
	if strings.Contains(string(withAnn[0].Body), "op-sa") {
		t.Errorf("compose output leaked the service-account value:\n%s", withAnn[0].Body)
	}
}
