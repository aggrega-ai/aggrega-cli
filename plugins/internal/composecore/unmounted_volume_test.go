// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// volumeDiags filters a diagnostic set down to the per-volume advisories keyed by
// the authored volumes.<name> path, so a case can assert on them without matching
// the container-level or pod-level entries.
func volumeDiags(diags []renderctx.Diagnostic, volume string) []renderctx.Diagnostic {
	var out []renderctx.Diagnostic
	for _, d := range diags {
		if d.SourceRef == "volumes."+volume {
			out = append(out, d)
		}
	}
	return out
}

// TestUnmountedVolume_PersistentDiagnosed proves a persistent volume no container
// mounts is reported, naming the volume and the consequence: the named volume
// still reaches the output, unused.
func TestUnmountedVolume_PersistentDiagnosed(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "store:1.0"}
	w.Containers.Set("main", c) // deliberately no volumeMounts
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi"}})

	files, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("store"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}

	got := volumeDiags(diags, "data")
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics for volumes.data, want 1 (all: %+v)", len(got), diags)
	}
	d := got[0]
	if d.Severity != abi.SeverityWarn {
		t.Errorf("severity = %q, want warn", d.Severity)
	}
	if d.Code != rendercore.UnsupportedFieldCode {
		t.Errorf("code = %q, want %q", d.Code, rendercore.UnsupportedFieldCode)
	}
	if !strings.Contains(d.Message, "volumes.data") {
		t.Errorf("message does not name the volume: %q", d.Message)
	}
	if !strings.Contains(d.Message, "no container in this workload mounts it") {
		t.Errorf("message does not state the reason: %q", d.Message)
	}
	if !strings.Contains(d.Message, "named volume is created and nothing uses it") {
		t.Errorf("message does not state the persistent consequence: %q", d.Message)
	}

	// The orphan volume is kept in the output rather than pruned: it is inert in
	// compose, and removing it would be a translation change rather than a
	// diagnostic.
	if !strings.Contains(string(files[0].Body), "store--data") {
		t.Errorf("expected the unmounted persistent volume to remain in the output; got:\n%s", files[0].Body)
	}
}

// TestUnmountedVolume_ConfigMapDiagnosed proves a configmap volume no container
// mounts is reported. This is the sharper shape: the volume reaches the output
// only through a mount point, so with nothing mounting it the declaration leaves
// no trace at all — which is exactly why it needs a diagnostic.
func TestUnmountedVolume_ConfigMapDiagnosed(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	w.Containers.Set("main", c) // deliberately no volumeMounts
	w.Volumes.Set("settings", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}})

	files, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("app"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}

	got := volumeDiags(diags, "settings")
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics for volumes.settings, want 1 (all: %+v)", len(got), diags)
	}
	d := got[0]
	if d.Code != rendercore.UnsupportedFieldCode {
		t.Errorf("code = %q, want %q", d.Code, rendercore.UnsupportedFieldCode)
	}
	if !strings.Contains(d.Message, "volumes.settings") {
		t.Errorf("message does not name the volume: %q", d.Message)
	}
	if !strings.Contains(d.Message, "configmap") {
		t.Errorf("message does not name the authored variant: %q", d.Message)
	}
	if !strings.Contains(d.Message, "nothing is emitted for it") {
		t.Errorf("message does not state the vanishing consequence: %q", d.Message)
	}

	// The premise the diagnostic exists for: without it, this declaration is
	// silent. Nothing in the rendered bytes names the volume or its source.
	body := string(files[0].Body)
	if strings.Contains(body, "settings") || strings.Contains(body, "app-config") {
		t.Errorf("expected the unmounted configmap volume to leave no trace in the output; got:\n%s", body)
	}
}

// TestUnmountedVolume_MountedVolumesSilent is the negative case: a volume some
// container mounts produces no unmounted advisory. It covers both mounting
// containers — a long-running container and an init container — because an init
// container's mounts are translated, so a volume mounted only there is wired and
// must not be reported.
//
// Each case names the volume it declares so the assertion can match the unmounted
// advisory's exact coordinate. Matching every source ref merely *beginning*
// volumes. would be a looser test rather than a stricter one: other volume-scoped
// advisories share that prefix and sit deeper in the authored path — a dropped
// ephemeral sizeLimit among them — and catching one of those here would report a
// failure of the mounting rule that has not happened.
func TestUnmountedVolume_MountedVolumesSilent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		volume string
		build  func() *types.Workload
	}{
		{"mounted by the main container", "data", func() *types.Workload {
			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			c := types.ContainerSpec{Image: "app:1.0"}
			c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/var/data"}}
			w.Containers.Set("main", c)
			w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "1Gi"}})
			return w
		}},
		{"mounted only by an init container", "settings", func() *types.Workload {
			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
			initC := types.ContainerSpec{Name: "seed", Image: "seeder:1.0"}
			initC.VolumeMounts = []types.VolumeMount{{Name: "settings", Path: "/etc/settings"}}
			w.InitContainers = []types.ContainerSpec{initC}
			w.Volumes.Set("settings", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}})
			return w
		}},
		{"mounted by a sidecar", "scratch", func() *types.Workload {
			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
			side := types.ContainerSpec{Image: "logger:1.0"}
			side.VolumeMounts = []types.VolumeMount{{Name: "scratch", Path: "/scratch"}}
			w.Containers.Set("logger", side)
			w.Volumes.Set("scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "100Mi"}})
			return w
		}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := tc.build()
			_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("app"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			if got := volumeDiags(diags, tc.volume); len(got) != 0 {
				t.Errorf("a mounted volume produced %d unmounted advisory(ies): %+v", len(got), got)
			}
		})
	}
}

// TestUnmountedVolume_PersistentTemplateReportsTwoDistinctFacts proves the
// per-replica-templating advisory and the unmounted advisory read as two
// different facts about one volume rather than the same sentence twice. One
// cause can have two consequences; a surface that restates one of them is noise.
func TestUnmountedVolume_PersistentTemplateReportsTwoDistinctFacts(t *testing.T) {
	t.Parallel()
	// persistentTemplate is a companion of form: stateful.
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload", Form: "stateful"}
	w.Containers.Set("main", types.ContainerSpec{Image: "db:1.0"}) // no volumeMounts
	w.Volumes.Set("state", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}})

	_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("db"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}

	var templating, unmounted *renderctx.Diagnostic
	for i := range diags {
		switch {
		case diags[i].SourceRef == "persistentTemplate":
			templating = &diags[i]
		case diags[i].SourceRef == "volumes.state":
			unmounted = &diags[i]
		}
	}
	if templating == nil {
		t.Fatalf("expected the per-replica templating advisory; got %+v", diags)
	}
	if unmounted == nil {
		t.Fatalf("expected the unmounted advisory; got %+v", diags)
	}
	if templating.Message == unmounted.Message {
		t.Fatalf("the two advisories are the same sentence twice: %q", templating.Message)
	}
	if !strings.Contains(templating.Message, "per-replica") {
		t.Errorf("templating advisory lost its subject: %q", templating.Message)
	}
	if !strings.Contains(unmounted.Message, "no container in this workload mounts it") {
		t.Errorf("unmounted advisory lost its subject: %q", unmounted.Message)
	}
	if strings.Contains(unmounted.Message, "per-replica") {
		t.Errorf("unmounted advisory restates the templating fact: %q", unmounted.Message)
	}
}

// TestUnmountedVolume_EveryVariantReported proves the rule is one rule over the
// declared volumes rather than a per-variant special case: every one of the five
// authored variants is reported when nothing mounts it. The two persistent kinds
// leave a visible unused entry and the other three leave no trace, so a check
// wired to the output rather than to the declaration would see only the first
// two.
func TestUnmountedVolume_EveryVariantReported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		volume string
		spec   types.VolumeSpec
		// stateful is required for the persistentTemplate companion rule.
		stateful bool
	}{
		{"scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "100Mi"}}, false},
		{"settings", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}}, false},
		{"creds", types.VolumeSpec{Secret: &types.SecretVolume{Source: "app-creds"}}, false},
		{"data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "1Gi"}}, false},
		{"state", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.volume, func(t *testing.T) {
			t.Parallel()
			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			if tc.stateful {
				w.Form = "stateful"
			}
			w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
			w.Volumes.Set(tc.volume, tc.spec)

			_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("app"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			got := volumeDiags(diags, tc.volume)
			if len(got) != 1 {
				t.Fatalf("got %d unmounted advisories for %s, want 1 (all: %+v)", len(got), tc.volume, diags)
			}
			if !strings.Contains(got[0].Message, "volumes."+tc.volume) {
				t.Errorf("message does not name the volume: %q", got[0].Message)
			}
		})
	}
}
