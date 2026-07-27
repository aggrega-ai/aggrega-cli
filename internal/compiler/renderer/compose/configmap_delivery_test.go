// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// configMapVolumeWorkload is a single-container workload mounting one configmap
// volume at /etc/app and one persistent volume, so the emitted output carries a
// configs: reference alongside a volumes: entry.
func configMapVolumeWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("http", 8080)
	c.VolumeMounts = []types.VolumeMount{
		{Name: "settings", Path: "/etc/app", ReadOnly: true},
		{Name: "state", Path: "/var/lib/app"},
	}
	w.Containers.Set("main", c)
	w.Volumes.Set("settings", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-settings"}})
	w.Volumes.Set("state", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "1Gi"}})
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("http", 8080)
	w.Services.Set("api", svc)
	return w
}

// artifactConfigFragment is the compose fragment an `artifacts:` entry
// contributes for a configmap: a top-level configs: declaration with inline
// content, carried into boundary assembly as an intermediate fragment exactly as
// the artifacts input kind emits it for this provider.
func artifactConfigFragment(name, content string) renderer.EmittedFile {
	body := "configs:\n  " + name + ":\n    content: |\n      " + strings.ReplaceAll(content, "\n", "\n      ") + "\n"
	return renderer.EmittedFile{
		Name:         name + ".yaml",
		Origin:       renderer.EmitOriginArtifactsInline,
		Body:         []byte(body),
		Intermediate: true,
	}
}

// TestComposeAssemble_ConfigMapVolumeMissingArtifactHalts proves the guard is
// reachable: a configmap volume whose config nothing declares halts at boundary
// assembly rather than emitting a reference that resolves to nothing at run
// time. The message names the service, the config, and the mount path so the
// author can find the volume that produced it.
func TestComposeAssemble_ConfigMapVolumeMissingArtifactHalts(t *testing.T) {
	t.Parallel()

	frag := makeFragment(t, "api", configMapVolumeWorkload(), nil)

	_, err := AssembleBoundaryCore(
		map[string][]renderer.EmittedFile{"api": frag},
		map[string]map[string]string{"api": {}},
		nil, "default", 2,
	)
	if err == nil {
		t.Fatal("assemble accepted a configmap volume with no declared config; want a halt")
	}
	if !errors.Is(err, ErrTranslationConfigMapMissingArtifact) {
		t.Fatalf("error = %v, want it to carry ErrTranslationConfigMapMissingArtifact", err)
	}
	for _, want := range []string{`service "api"`, `config "app-settings"`, `"/etc/app"`, "artifacts:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err.Error(), want)
		}
	}
}

// TestComposeAssemble_ConfigMapVolumeWithArtifactRenders is the happy path over
// real emitted bytes: with the artifact fragment declaring the config, assembly
// succeeds and the assembled file carries both halves — the service's configs:
// reference and the top-level configs: entry it resolves to.
func TestComposeAssemble_ConfigMapVolumeWithArtifactRenders(t *testing.T) {
	t.Parallel()

	frag := makeFragment(t, "api", configMapVolumeWorkload(), nil)
	frag = append(frag, artifactConfigFragment("app-settings", "mode: production\nregion: eu-west-1"))

	files, err := AssembleBoundaryCore(
		map[string][]renderer.EmittedFile{"api": frag},
		map[string]map[string]string{"api": {}},
		nil, "default", 2,
	)
	if err != nil {
		t.Fatalf("AssembleBoundary: %v", err)
	}
	goldenCheckAssembled(t, "configmap-volume-via-configs", files[AssembledOutputFile])
}

// TestComposeAssemble_ConfigDeclaredByAnotherNodeResolves pins why the check
// belongs to the assembled boundary rather than to a node: one node mounts the
// configmap and a different node in the same dispatch group declares it, which a
// per-node check would reject.
func TestComposeAssemble_ConfigDeclaredByAnotherNodeResolves(t *testing.T) {
	t.Parallel()

	consumer := makeFragment(t, "api", configMapVolumeWorkload(), nil)

	producerW := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	producerW.Containers.Set("main", types.ContainerSpec{Image: "seeder:1.0"})
	producer := makeFragment(t, "seeder", producerW, nil)
	producer = append(producer, artifactConfigFragment("app-settings", "mode: production"))

	files, err := AssembleBoundaryCore(
		map[string][]renderer.EmittedFile{"api": consumer, "seeder": producer},
		map[string]map[string]string{"api": {}, "seeder": {}},
		nil, "default", 2,
	)
	if err != nil {
		t.Fatalf("AssembleBoundary rejected a config declared by a sibling node: %v", err)
	}
	body := string(files[AssembledOutputFile])
	if !strings.Contains(body, "source: app-settings") {
		t.Errorf("assembled output carries no configs: reference:\n%s", body)
	}
}

// TestComposeAssemble_UnresolvedConfigsReportedDeterministically asserts every
// unresolved reference is named, in an order that does not depend on map
// iteration.
func TestComposeAssemble_UnresolvedConfigsReportedDeterministically(t *testing.T) {
	t.Parallel()

	build := func(nodeID, configName, path string) []renderer.EmittedFile {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
		c := types.ContainerSpec{Image: "app:1.0"}
		c.VolumeMounts = []types.VolumeMount{{Name: "settings", Path: path}}
		w.Containers.Set("main", c)
		w.Volumes.Set("settings", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: configName}})
		return makeFragment(t, nodeID, w, nil)
	}

	intermediates := map[string][]renderer.EmittedFile{
		"zeta":  build("zeta", "zeta-config", "/etc/zeta"),
		"alpha": build("alpha", "alpha-config", "/etc/alpha"),
	}
	annotations := map[string]map[string]string{"zeta": {}, "alpha": {}}

	var first string
	for range 8 {
		_, err := AssembleBoundaryCore(intermediates, annotations, nil, "default", 2)
		if err == nil {
			t.Fatal("assemble accepted two unresolved config references; want a halt")
		}
		if first == "" {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("halt message is not stable across runs:\n first: %s\nlater: %s", first, err.Error())
		}
	}
	alpha := strings.Index(first, "alpha-config")
	zeta := strings.Index(first, "zeta-config")
	if alpha < 0 || zeta < 0 {
		t.Fatalf("halt names only some unresolved references: %s", first)
	}
	if alpha > zeta {
		t.Errorf("unresolved references are not reported in service order: %s", first)
	}
}

// TestComposeAssemble_ServiceLevelConfigsFromSourceFragmentResolves covers the
// shape a sourced fragment contributes: a hand-authored service-level configs:
// reference beside its own top-level declaration, which is how the cross-network
// gateway delivers its generated config. Both halves ride one fragment, so the
// guard must accept it.
func TestComposeAssemble_ServiceLevelConfigsFromSourceFragmentResolves(t *testing.T) {
	t.Parallel()

	fragment := renderer.EmittedFile{
		Name:   "gateway.compose-fragment.yaml",
		Origin: renderer.EmitOriginSource,
		Body: []byte(`services:
  gateway:
    image: nginx:alpine
    configs:
      - source: gateway-conf
        target: /etc/nginx/nginx.conf
configs:
  gateway-conf:
    content: |
      stream { }
`),
		Intermediate: true,
	}

	if _, err := AssembleBoundaryCore(
		map[string][]renderer.EmittedFile{"gateway": {fragment}},
		map[string]map[string]string{"gateway": {}},
		nil, "default", 2,
	); err != nil {
		t.Fatalf("AssembleBoundary rejected a fragment declaring both halves: %v", err)
	}
}

// TestSynthesizeWorkload_SecretFileVolumeStaysBindMount pins the deliberate
// divergence between the two variants: a configmap volume becomes a configs:
// reference, while a secret volume stays a host-path bind mount, because
// delivering it as a config would write the secret's value into the emitted
// compose file in plaintext.
func TestSynthesizeWorkload_SecretFileVolumeStaysBindMount(t *testing.T) {
	t.Parallel()

	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.VolumeMounts = []types.VolumeMount{
		{Name: "settings", Path: "/etc/app"},
		{Name: "creds", Path: "/run/secrets/creds"},
	}
	w.Containers.Set("main", c)
	w.Volumes.Set("settings", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-settings"}})
	w.Volumes.Set("creds", types.VolumeSpec{Secret: &types.SecretVolume{Source: "app-creds"}})

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	body := string(files[0].Body)

	if strings.Contains(body, "source: app-creds\n        target: /run/secrets/creds") == false {
		t.Errorf("secret volume did not stay a bind mount:\n%s", body)
	}
	if strings.Contains(body, "configs:\n      - source: app-creds") {
		t.Errorf("secret volume became a configs: reference, which would materialise it in the emitted file:\n%s", body)
	}
	if !strings.Contains(body, "configs:\n      - source: app-settings") {
		t.Errorf("configmap volume did not become a configs: reference:\n%s", body)
	}
}
