// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

func TestWorkload_MinimalRoundTrip(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:latest
`)
	var w types.Workload
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if w.APIVersion != "aggrega.ai/v1alpha1" {
		t.Errorf("apiVersion: %q", w.APIVersion)
	}
	if w.Kind != "Workload" {
		t.Errorf("kind: %q", w.Kind)
	}
	main, ok := w.Containers.Get("main")
	if !ok {
		t.Fatalf("containers.main missing")
	}
	if main.Image != "app:latest" {
		t.Errorf("main.image: %q", main.Image)
	}
}

func TestWorkload_ContainerAuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	// Deliberately non-alphabetical to verify OrderedMap propagation:
	// envoy precedes main precedes alpine; alphabetical would yield
	// alpine, envoy, main.
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  envoy:
    image: envoy:1.0
  main:
    image: main:1.0
  alpine:
    image: alpine:3
`)
	var w types.Workload
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got := w.Containers.Keys()
	want := []string{"envoy", "main", "alpine"}
	if !slices.Equal(got, want) {
		t.Errorf("container author order broken: got %v, want %v", got, want)
	}
}

func TestWorkload_VolumeVariantsParseDistinctly(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:latest
volumes:
  cache:
    ephemeral:
      sizeLimit: 100Mi
  config:
    configmap:
      source: app-config
  secret-store:
    secret:
      source: app-secret
  data:
    persistent:
      size: 10Gi
  shard:
    persistentTemplate:
      size: 1Gi
`)
	var w types.Workload
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	cache, _ := w.Volumes.Get("cache")
	if cache.Ephemeral == nil || cache.Ephemeral.SizeLimit != "100Mi" {
		t.Errorf("ephemeral parse: %+v", cache.Ephemeral)
	}
	cfg, _ := w.Volumes.Get("config")
	if cfg.ConfigMap == nil || cfg.ConfigMap.Source != "app-config" {
		t.Errorf("configmap parse: %+v", cfg.ConfigMap)
	}
	sec, _ := w.Volumes.Get("secret-store")
	if sec.Secret == nil || sec.Secret.Source != "app-secret" {
		t.Errorf("secret parse: %+v", sec.Secret)
	}
	data, _ := w.Volumes.Get("data")
	if data.Persistent == nil || data.Persistent.Size != "10Gi" {
		t.Errorf("persistent parse: %+v", data.Persistent)
	}
	shard, _ := w.Volumes.Get("shard")
	if shard.PersistentTemplate == nil || shard.PersistentTemplate.Size != "1Gi" {
		t.Errorf("persistentTemplate parse: %+v", shard.PersistentTemplate)
	}
}

func TestWorkload_ProbeShapesParseDistinctly(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  app:
    image: app:1.0
    health:
      ready:
        http: /healthz
        timeoutSeconds: 5
      live:
        tcp: 8080
      startup:
        exec: ["/bin/check.sh"]
`)
	var w types.Workload
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	app, _ := w.Containers.Get("app")
	if app.Health == nil {
		t.Fatal("health missing")
	}
	if app.Health.Ready == nil || app.Health.Ready.HTTP != "/healthz" {
		t.Errorf("ready probe: %+v", app.Health.Ready)
	}
	if app.Health.Live == nil || app.Health.Live.TCP != 8080 {
		t.Errorf("live probe: %+v", app.Health.Live)
	}
	if app.Health.Startup == nil || len(app.Health.Startup.Exec) != 1 || app.Health.Startup.Exec[0] != "/bin/check.sh" {
		t.Errorf("startup probe: %+v", app.Health.Startup)
	}
}

func TestWorkload_ServiceTypeParses(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  app:
    image: app:1.0
services:
  api:
    type: ClusterIP
    ports:
      web: 8080
      admin: 9090
`)
	var w types.Workload
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	api, ok := w.Services.Get("api")
	if !ok {
		t.Fatal("services.api missing")
	}
	if api.Type != "ClusterIP" {
		t.Errorf("type: %q", api.Type)
	}
	web, _ := api.Ports.Get("web")
	if web != 8080 {
		t.Errorf("web port: %d", web)
	}
	// Author order preservation: web before admin.
	got := api.Ports.Keys()
	if !slices.Equal(got, []string{"web", "admin"}) {
		t.Errorf("port author order broken: got %v, want [web admin]", got)
	}
}

func TestWorkload_FullSurfaceParses(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Workload
metadata:
  name: api-server
  labels:
    tier: backend
replicas: 3
initContainers:
  - image: migrator:1.0
    command: ["/migrate"]
containers:
  main:
    image: app:1.0
    command: ["/app"]
    args: ["--port", "8080"]
    variables:
      LOG_LEVEL: info
    secrets:
      DB_PASS:
        from: db-password
        as: env
    ports:
      web: 8080
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 500m
        memory: 512Mi
    health:
      ready:
        http: /healthz
    volumeMounts:
      - name: data
        path: /data
    restartPolicy: Always
    securityContext:
      runAsUser: 1000
      runAsNonRoot: true
volumes:
  data:
    persistent:
      size: 10Gi
services:
  api:
    type: ClusterIP
    ports:
      web: 8080
scheduling:
  nodeSelector:
    tier: backend
podSecurityContext:
  fsGroup: 2000
serviceAccount: app-sa
`)
	var w types.Workload
	if err := yaml.Unmarshal(src, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Spot-checks across the full surface.
	if w.Replicas == nil || *w.Replicas != 3 {
		t.Errorf("replicas: %v", w.Replicas)
	}
	if len(w.InitContainers) != 1 || w.InitContainers[0].Image != "migrator:1.0" {
		t.Errorf("initContainers: %+v", w.InitContainers)
	}
	main, _ := w.Containers.Get("main")
	if main.Resources == nil || main.Resources.Requests == nil || main.Resources.Requests.CPU != "100m" {
		t.Errorf("resources.requests.cpu: %+v", main.Resources)
	}
	if w.Scheduling == nil || w.Scheduling.NodeSelector["tier"] != "backend" {
		t.Errorf("scheduling.nodeSelector: %+v", w.Scheduling)
	}
	if w.PodSecurityContext == nil || w.PodSecurityContext.FSGroup == nil || *w.PodSecurityContext.FSGroup != 2000 {
		t.Errorf("podSecurityContext.fsGroup: %+v", w.PodSecurityContext)
	}
	if w.ServiceAccount != "app-sa" {
		t.Errorf("serviceAccount: %q", w.ServiceAccount)
	}
	if w.Metadata.Labels["tier"] != "backend" {
		t.Errorf("metadata.labels: %v", w.Metadata.Labels)
	}
}
