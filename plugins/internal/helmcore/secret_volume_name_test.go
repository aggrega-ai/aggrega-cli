// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// fileEntry is one container attachments entry delivered `as: file`, in the
// form the synthesis receives it: a slot read through a connection arrives
// already folded into the name the compiler composes for it.
type fileEntry struct {
	key, slot, path string
}

// secretVolume is one pod volume the synthesis emits for a file-delivered
// slot: the volume's name and the Secret it mounts.
type secretVolume struct {
	name, secret string
}

// TestSynthesizeWorkload_SecretFileVolumeNameWritesTheSlotOut drives the helm
// synthesis with file-delivered attachments and reads the emitted Deployment
// back: the pod volume each slot is mounted from, the Secret it mounts, and the
// name every container and init container mounts it by.
//
// A pod volume name is an RFC 1123 label, so every name the Deployment carries
// for a volume or a mount is also held to that grammar, whatever the slot was
// called. A slot made of lowercase letters, digits and hyphens keeps the name
// "<basis>-secret-<slot>" as written — the first row is the control for that.
// Every other character is written out: an underscore, a capital, and the dot a
// connection's slot arrives with. The last row is the reason the encoding is
// applied to every slot rather than only to one holding such a character: an own
// slot spelled the way a connection's slot is written out must still reach a
// volume of its own.
func TestSynthesizeWorkload_SecretFileVolumeNameWritesTheSlotOut(t *testing.T) {
	t.Parallel()

	provides := map[string]types.OrderedMap[string]{
		"tls-cert":           providesBag("tls-bundle", "tls.crt"),
		"admin_key":          providesBag("router-admin-key", "token"),
		"server_certificate": providesBag("router-server-certificate", "tls.crt"),
		"cache.server_ca":    providesBag("router-cache.server_ca", "ca.crt"),
		"tlsCert":            providesBag("router-tls-cert", "tls.crt"),
		"gpt.credential":     providesBag("router-gpt.credential", "api-key"),
		"a-zdb":              providesBag("own-a-zdb", "key"),
		"a.b":                providesBag("conn-a-b", "key"),
	}

	cases := []struct {
		name           string
		main           []fileEntry
		init           []fileEntry
		wantVolumes    []secretVolume
		wantMainMounts []string
		wantInitMounts []string
	}{
		{
			name:           "an own slot is named as written",
			main:           []fileEntry{{"CERT", "tls-cert", "/etc/tls"}},
			wantVolumes:    []secretVolume{{"router-secret-tls-cert", "tls-bundle"}},
			wantMainMounts: []string{"router-secret-tls-cert"},
		},
		{
			name:           "an own slot carrying an underscore has the underscore written out",
			main:           []fileEntry{{"ADMIN", "admin_key", "/run/admin"}},
			wantVolumes:    []secretVolume{{"router-secret-admin-zukey", "router-admin-key"}},
			wantMainMounts: []string{"router-secret-admin-zukey"},
		},
		{
			name:           "a server certificate slot has its underscore written out",
			main:           []fileEntry{{"TLS", "server_certificate", "/etc/tls"}},
			wantVolumes:    []secretVolume{{"router-secret-server-zucertificate", "router-server-certificate"}},
			wantMainMounts: []string{"router-secret-server-zucertificate"},
		},
		{
			name:           "a connection's server CA slot has its dot and its underscore written out",
			main:           []fileEntry{{"CACHE_CA", "cache.server_ca", "/etc/cache-ca"}},
			wantVolumes:    []secretVolume{{"router-secret-cache-zdserver-zuca", "router-cache.server_ca"}},
			wantMainMounts: []string{"router-secret-cache-zdserver-zuca"},
		},
		{
			name:           "an own slot holding a capital has the capital written out",
			main:           []fileEntry{{"CERT", "tlsCert", "/etc/tls"}},
			wantVolumes:    []secretVolume{{"router-secret-tls-zx000043ert", "router-tls-cert"}},
			wantMainMounts: []string{"router-secret-tls-zx000043ert"},
		},
		{
			name:           "a connection's slot reaches the volume name with its dot written out",
			main:           []fileEntry{{"GPT_KEY", "gpt.credential", "/run/gpt"}},
			wantVolumes:    []secretVolume{{"router-secret-gpt-zdcredential", "router-gpt.credential"}},
			wantMainMounts: []string{"router-secret-gpt-zdcredential"},
		},
		{
			name:           "an init container's connection slot is written out the same way",
			init:           []fileEntry{{"GPT_KEY", "gpt.credential", "/run/gpt"}},
			wantVolumes:    []secretVolume{{"router-secret-gpt-zdcredential", "router-gpt.credential"}},
			wantInitMounts: []string{"router-secret-gpt-zdcredential"},
		},
		{
			name:           "a container and an init container reading one connection slot share its volume",
			main:           []fileEntry{{"GPT_KEY", "gpt.credential", "/run/gpt"}},
			init:           []fileEntry{{"GPT_KEY", "gpt.credential", "/run/gpt"}},
			wantVolumes:    []secretVolume{{"router-secret-gpt-zdcredential", "router-gpt.credential"}},
			wantMainMounts: []string{"router-secret-gpt-zdcredential"},
			wantInitMounts: []string{"router-secret-gpt-zdcredential"},
		},
		{
			name: "an own slot spelled like a written-out connection slot keeps a volume of its own",
			main: []fileEntry{{"OWN", "a-zdb", "/run/own"}, {"CONN", "a.b", "/run/conn"}},
			wantVolumes: []secretVolume{
				{"router-secret-a-zhzdb", "own-a-zdb"},
				{"router-secret-a-zdb", "conn-a-b"},
			},
			wantMainMounts: []string{"router-secret-a-zhzdb", "router-secret-a-zdb"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The expected names are held to the grammar too, so a row cannot
			// pin a name the API server would refuse.
			for _, v := range tc.wantVolumes {
				if !isLabelName(v.name, rfc1123Label) {
					t.Fatalf("the expected volume %q is not a name a pod volume may carry", v.name)
				}
			}

			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			app := types.ContainerSpec{Image: "router:1.0"}
			for _, e := range tc.main {
				app.Attachments.Set(e.key, types.AttachmentRef{From: e.slot, As: "file", Path: e.path})
			}
			w.Containers.Set("main", app)
			if len(tc.init) > 0 {
				prepare := types.ContainerSpec{Name: "prepare", Image: "router-init:1.0"}
				for _, e := range tc.init {
					prepare.Attachments.Set(e.key, types.AttachmentRef{From: e.slot, As: "file", Path: e.path})
				}
				w.InitContainers = []types.ContainerSpec{prepare}
			}

			files, err := SynthesizeWorkloadCore(w, provides, nodeCtxFor("router"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			var body []byte
			for _, f := range files {
				if f.Name == "router-deployment.yaml" {
					body = f.Body
				}
			}
			if body == nil {
				t.Fatalf("no router-deployment.yaml among the emitted files")
			}
			var emitted Deployment
			if err := yaml.Unmarshal(body, &emitted); err != nil {
				t.Fatalf("decode the emitted Deployment: %v\n%s", err, body)
			}
			pod := emitted.Spec.Template.Spec

			var gotVolumes []secretVolume
			for _, v := range pod.Volumes {
				if !isLabelName(v.Name, rfc1123Label) {
					t.Errorf("pod volume %q is not a name a pod volume may carry", v.Name)
				}
				if v.Secret != nil {
					gotVolumes = append(gotVolumes, secretVolume{v.Name, v.Secret.SecretName})
				}
			}
			if !equalSecretVolumes(gotVolumes, tc.wantVolumes) {
				t.Errorf("pod secret volumes = %v, want %v\n%s", gotVolumes, tc.wantVolumes, body)
			}

			if len(pod.Containers) != 1 {
				t.Fatalf("containers = %d, want 1", len(pod.Containers))
			}
			if got := mountNames(pod.Containers[0]); !equalStrings(got, tc.wantMainMounts) {
				t.Errorf("container mounts = %v, want %v", got, tc.wantMainMounts)
			}
			var gotInit []string
			if len(pod.InitContainers) == 1 {
				gotInit = mountNames(pod.InitContainers[0])
			}
			if !equalStrings(gotInit, tc.wantInitMounts) {
				t.Errorf("init container mounts = %v, want %v", gotInit, tc.wantInitMounts)
			}
			for _, name := range append(mountNames(pod.Containers[0]), gotInit...) {
				if !isLabelName(name, rfc1123Label) {
					t.Errorf("a container mounts %q, which is not a name a pod volume may carry", name)
				}
			}
		})
	}
}

// mountNames returns the volume name of every mount a container declares, in
// the order it declares them.
func mountNames(c Container) []string {
	var names []string
	for _, vm := range c.VolumeMounts {
		names = append(names, vm.Name)
	}
	return names
}

func equalSecretVolumes(a, b []secretVolume) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
