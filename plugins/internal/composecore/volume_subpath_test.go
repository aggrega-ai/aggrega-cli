// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// synthesizedMainVolumes synthesizes w for node and returns the volumes: entries
// of the node's main service, read back from the emitted fragment.
func synthesizedMainVolumes(t *testing.T, w *types.Workload, node string) ([]ServiceVolume, string) {
	t.Helper()
	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor(node))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("emitted fragment does not decode: %v\n%s", err, files[0].Body)
	}
	svc, ok := frag.Services[node]
	if !ok {
		t.Fatalf("no service %q in the emitted fragment:\n%s", node, files[0].Body)
	}
	return svc.Volumes, string(files[0].Body)
}

// TestComposeSubPathMountsThePartOfTheVolumeItNames proves a volume mount's
// subPath reaches the compose entry as the long form's volume.subpath, for both
// variants compose delivers as a named volume: one volume mounted twice with two
// subPaths gives two entries naming the one volume and two different subpaths, so
// the two mount points see two parts of it rather than one shared root. A mount
// of the same volume with no subPath emits no volume: block at all, which is what
// keeps every mount written without subPath byte-identical.
func TestComposeSubPathMountsThePartOfTheVolumeItNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		spec     types.VolumeSpec
		stateful bool
	}{
		{"persistent", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "1Gi"}}, false},
		{"persistentTemplate", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}}, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			if tc.stateful {
				w.Form = "stateful"
			}
			c := types.ContainerSpec{Image: "app:1.0"}
			c.VolumeMounts = []types.VolumeMount{
				{Name: "data", Path: "/var/lib/app/db", SubPath: "db"},
				{Name: "data", Path: "/var/lib/app/uploads", SubPath: "uploads", ReadOnly: true},
				{Name: "data", Path: "/var/lib/app/all"},
			}
			w.Containers.Set("main", c)
			w.Volumes.Set("data", tc.spec)

			got, body := synthesizedMainVolumes(t, w, "app")
			want := []ServiceVolume{
				{Type: "volume", Source: "app--data", Target: "/var/lib/app/db", Volume: &ServiceVolumeOptions{Subpath: "db"}},
				{Type: "volume", Source: "app--data", Target: "/var/lib/app/uploads", ReadOnly: true, Volume: &ServiceVolumeOptions{Subpath: "uploads"}},
				{Type: "volume", Source: "app--data", Target: "/var/lib/app/all"},
			}
			if len(got) != len(want) {
				t.Fatalf("got %d volumes: entries, want %d:\n%s", len(got), len(want), body)
			}
			for i := range want {
				g, wv := got[i], want[i]
				if g.Type != wv.Type || g.Source != wv.Source || g.Target != wv.Target || g.ReadOnly != wv.ReadOnly {
					t.Errorf("entry %d = %+v, want %+v", i, g, wv)
				}
				switch {
				case wv.Volume == nil && g.Volume != nil:
					t.Errorf("entry %d carries volume options %+v for a mount written without subPath", i, *g.Volume)
				case wv.Volume != nil && (g.Volume == nil || g.Volume.Subpath != wv.Volume.Subpath):
					t.Errorf("entry %d volume options = %+v, want subpath %q:\n%s", i, g.Volume, wv.Volume.Subpath, body)
				}
			}
			if n := strings.Count(body, "subpath:"); n != 2 {
				t.Errorf("emitted fragment holds %d subpath members, want 2:\n%s", n, body)
			}
		})
	}
}

// TestComposeSubPathOnAMountWithNoSubpathFormIsRefused proves a subPath on a
// mount compose delivers through a primitive with no subpath form halts the build,
// naming the node, the mount's authored path, the volume, the subPath and the
// primitive, rather than mounting the whole volume at the path. One fault per row:
// each workload is valid but for the one subPath.
func TestComposeSubPathOnAMountWithNoSubpathFormIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		spec      types.VolumeSpec
		primitive string
	}{
		{"ephemeral", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{}}, "a tmpfs mount"},
		{"configmap", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}}, "a configs: reference"},
		{"secret", types.VolumeSpec{Secret: &types.SecretVolume{Source: "app-creds"}}, "a bind mount"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			c := types.ContainerSpec{Image: "app:1.0"}
			c.VolumeMounts = []types.VolumeMount{{Name: "part", Path: "/etc/app/app.conf", SubPath: "app.conf"}}
			w.Containers.Set("main", c)
			w.Volumes.Set("part", tc.spec)

			files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("app"))
			if err == nil {
				t.Fatalf("a subPath on a %s volume was accepted:\n%s", tc.name, files[0].Body)
			}
			if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
				t.Errorf("refusal is not the cannot-express condition: %v", err)
			}
			text := fullText(err)
			for _, want := range []string{
				`node "app"`,
				"containers.main.volumeMounts[0]",
				`volume "part"`,
				`subPath "app.conf"`,
				tc.primitive,
			} {
				if !strings.Contains(text, want) {
					t.Errorf("refusal does not say %q:\n%s", want, text)
				}
			}
		})
	}

	// Positive control: the same mounts written without subPath synthesize.
	for _, tc := range cases {
		w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
		c := types.ContainerSpec{Image: "app:1.0"}
		c.VolumeMounts = []types.VolumeMount{{Name: "part", Path: "/etc/app/app.conf"}}
		w.Containers.Set("main", c)
		w.Volumes.Set("part", tc.spec)
		if _, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("app")); err != nil {
			t.Errorf("%s: the mount without subPath was refused: %v", tc.name, err)
		}
	}
}
