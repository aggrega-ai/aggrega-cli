// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestInjectAggregaAnnotations_HookWeightOverwrite pins the existing-
// hook-weight overwrite branch: a manifest that already carries a
// helm.sh/hook-weight annotation gets its value updated rather than
// duplicated.
func TestInjectAggregaAnnotations_HookWeightOverwrite(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: migrate
  annotations:
    helm.sh/hook: pre-install
    helm.sh/hook-weight: "1"
`)
	out, err := injectAggregaAnnotations(src, map[string]string{}, "9", 2)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `helm.sh/hook-weight: "9"`) {
		t.Errorf("hook-weight should be overwritten to 9; got:\n%s", got)
	}
	if strings.Count(got, "helm.sh/hook-weight") != 1 {
		t.Errorf("hook-weight should appear exactly once; got count=%d", strings.Count(got, "helm.sh/hook-weight"))
	}
}

// TestInjectAggregaAnnotations_HookWeightWithSpecialValue pins the
// new-hook-weight emission path when the weight value contains chars
// that trigger chooseScalarStyle's quoted output.
func TestInjectAggregaAnnotations_HookWeightWithSpecialValue(t *testing.T) {
	t.Parallel()
	src := []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: m
  annotations:
    helm.sh/hook: post-install
`)
	out, err := injectAggregaAnnotations(src, map[string]string{}, "5", 2)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if !strings.Contains(string(out), "helm.sh/hook-weight") {
		t.Errorf("new hook-weight should emit")
	}
}

// TestTranslatePodVolumes_AllVolumeKinds pins the four documented
// volume-source kinds: Ephemeral, ConfigMap, Secret, Persistent.
func TestTranslatePodVolumes_AllVolumeKinds(t *testing.T) {
	t.Parallel()
	w := &types.Workload{}
	size := "1Gi"
	w.Volumes.Set("data-mem", types.VolumeSpec{
		Ephemeral: &types.EphemeralVolume{SizeLimit: size},
	})
	w.Volumes.Set("data-cm", types.VolumeSpec{
		ConfigMap: &types.ConfigMapVolume{Source: "shared-cm"},
	})
	w.Volumes.Set("data-secret", types.VolumeSpec{
		Secret: &types.SecretVolume{Source: "shared-secret"},
	})
	w.Volumes.Set("data-pvc", types.VolumeSpec{
		Persistent: &types.PersistentVolume{},
	})

	vols := translatePodVolumes(w, "node-1", nil)
	if len(vols) != 4 {
		t.Fatalf("expected 4 volumes; got %d", len(vols))
	}
	// Check shapes per kind.
	saw := map[string]bool{}
	for _, v := range vols {
		switch {
		case v.EmptyDir != nil:
			saw["ephemeral"] = v.EmptyDir.SizeLimit == "1Gi"
		case v.ConfigMap != nil:
			saw["configmap"] = v.ConfigMap.Name == "shared-cm"
		case v.Secret != nil:
			saw["secret"] = v.Secret.SecretName == "shared-secret"
		case v.PersistentVolumeClaim != nil:
			saw["pvc"] = v.PersistentVolumeClaim.ClaimName == "node-1-data-pvc"
		}
	}
	for _, k := range []string{"ephemeral", "configmap", "secret", "pvc"} {
		if !saw[k] {
			t.Errorf("kind %s not emitted correctly", k)
		}
	}
}

// TestTranslatePodVolumes_SecretFileVolumesDedupedAcrossContainers
// pins the dedup contract: multiple containers referencing the same
// secret-as-file slot produce exactly one secret volume.
func TestTranslatePodVolumes_SecretFileVolumesDedupedAcrossContainers(t *testing.T) {
	t.Parallel()
	w := &types.Workload{}
	c1 := types.ContainerSpec{Image: "x"}
	c1.Attachments.Set("creds/key", types.AttachmentRef{From: "shared", As: "file", Path: "/tmp/key"})
	c2 := types.ContainerSpec{Image: "y"}
	c2.Attachments.Set("creds/key", types.AttachmentRef{From: "shared", As: "file", Path: "/tmp/key"})
	w.Containers.Set("c1", c1)
	w.Containers.Set("c2", c2)
	w.InitContainers = []types.ContainerSpec{
		{Image: "init"},
	}
	w.InitContainers[0].Attachments.Set("init-creds", types.AttachmentRef{From: "shared", As: "file", Path: "/tmp/key2"})

	vols := translatePodVolumes(w, "node-1", nil)
	// "node-1-secret-shared" should appear exactly once across all
	// containers + init containers.
	count := 0
	for _, v := range vols {
		if v.Secret != nil && v.Name == "node-1-secret-shared" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("secret-volume dedup failed; count=%d (want 1)", count)
	}
}

// TestTranslatePodVolumes_EnvSecretsIgnored pins that `as: env`
// secrets do NOT produce volume entries (they go through env-var
// dispatch instead).
func TestTranslatePodVolumes_EnvSecretsIgnored(t *testing.T) {
	t.Parallel()
	w := &types.Workload{}
	c := types.ContainerSpec{Image: "x"}
	c.Attachments.Set("API_TOKEN", types.AttachmentRef{From: "creds", As: "env"})
	w.Containers.Set("c", c)
	if len(translatePodVolumes(w, "n", nil)) != 0 {
		t.Errorf("as:env secrets should NOT produce volume entries")
	}
}

// TestResolveHTTPProbePort_NamedPortsBranches pins the success and
// failure paths: zero ports → error; ports without names → error;
// at least one named port → first name sorted.
func TestResolveHTTPProbePort_NamedPortsBranches(t *testing.T) {
	t.Parallel()
	if _, err := resolveHTTPProbePort(nil); err == nil {
		t.Errorf("empty ports should error")
	}
	// Ports declared but unnamed.
	if _, err := resolveHTTPProbePort([]ContainerPort{{ContainerPort: 80}}); err == nil {
		t.Errorf("unnamed-port should error")
	}
	// Named ports — alphabetical first wins.
	got, err := resolveHTTPProbePort([]ContainerPort{
		{Name: "metrics", ContainerPort: 9090},
		{Name: "http", ContainerPort: 80},
	})
	if err != nil {
		t.Fatalf("named: %v", err)
	}
	if got != "http" {
		t.Errorf("got %q; want http (alphabetical first)", got)
	}
}

// TestTranslateContainer_VolumeMountsAndSecretsBranches pins the
// container translation's volume-mount and secret-env+file branches.
func TestTranslateContainer_VolumeMountsAndSecretsBranches(t *testing.T) {
	t.Parallel()
	c := types.ContainerSpec{
		Image:   "myimage",
		Command: []string{"sh"},
		Args:    []string{"-c", "ok"},
	}
	c.Ports.Set("http", 8080)
	c.Variables.Set("ENV1", "v1")
	c.Attachments.Set("API_TOKEN", types.AttachmentRef{From: "creds", As: "env"})
	c.Attachments.Set("tls-key", types.AttachmentRef{From: "tls", As: "file", Path: "/tls/key"})
	c.VolumeMounts = []types.VolumeMount{
		{Name: "data", Path: "/data", ReadOnly: true},
	}

	got, err := translateContainer("c1", c, "node-1", nil, "")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(got.Env) < 2 {
		t.Errorf("expected at least Env1 + API_TOKEN env entries; got %d", len(got.Env))
	}
	if len(got.VolumeMounts) < 2 {
		t.Errorf("expected volume-mount + secret-as-file mount; got %d", len(got.VolumeMounts))
	}
	saw := false
	for _, vm := range got.VolumeMounts {
		if vm.Name == "node-1-secret-tls" && vm.MountPath == "/tls/key" && vm.ReadOnly {
			saw = true
		}
	}
	if !saw {
		t.Errorf("secret-as-file mount missing or malformed; got %v", got.VolumeMounts)
	}
}
