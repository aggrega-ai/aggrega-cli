// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"bytes"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// withContainer sets the named container, read and written back the way
// withMainContainer does.
func withContainer(w *types.Workload, name string, mut func(c *types.ContainerSpec)) {
	c, _ := w.Containers.Get(name)
	if c.Image == "" {
		c.Image = "sidecar:1.0"
	}
	mut(&c)
	w.Containers.Set(name, c)
}

// serviceUsers reads the user each service of a rendered fragment runs as.
func serviceUsers(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var frag struct {
		Services map[string]struct {
			User string `yaml:"user"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &frag); err != nil {
		t.Fatalf("read the rendered fragment: %v\n%s", err, body)
	}
	out := make(map[string]string, len(frag.Services))
	for name, svc := range frag.Services {
		out[name] = svc.User
	}
	return out
}

// TestUnsupportedField_ContainerGroupWithoutUserOutOfBand proves a container
// runAsGroup written without a runAsUser beside it — which compose's user cannot
// carry, since it names a user before any group — is announced once and changes
// nothing compose renders, on a main container, a sidecar and an init container
// alike, and beside a pod user that the service still runs as. One fault per row.
func TestUnsupportedField_ContainerGroupWithoutUserOutOfBand(t *testing.T) {
	t.Parallel()
	group := func(c *types.ContainerSpec) {
		c.SecurityContext = &types.ContainerSecurityContext{RunAsGroup: ptrInt(3000)}
	}
	cases := []struct {
		name  string
		build func(withGroup bool) *types.Workload
	}{
		{"on the main container", func(withGroup bool) *types.Workload {
			w := baseDegradeWorkload()
			if withGroup {
				withMainContainer(w, group)
			}
			return w
		}},
		{"on a sidecar", func(withGroup bool) *types.Workload {
			w := baseDegradeWorkload()
			withContainer(w, "log", func(c *types.ContainerSpec) {
				if withGroup {
					group(c)
				}
			})
			return w
		}},
		{"on an init container", func(withGroup bool) *types.Workload {
			w := baseDegradeWorkload()
			setup := types.ContainerSpec{Name: "setup", Image: "setup:1.0"}
			if withGroup {
				group(&setup)
			}
			w.InitContainers = []types.ContainerSpec{setup}
			return w
		}},
		{"on the main container beside a pod user", func(withGroup bool) *types.Workload {
			w := baseDegradeWorkload()
			w.PodSecurityContext = &types.PodSecurityContext{RunAsUser: ptrInt(1000)}
			if withGroup {
				withMainContainer(w, group)
			}
			return w
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bareFiles, bareDiags, err := SynthesizeWorkloadCore(tc.build(false), nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth without the group: %v", err)
			}
			if len(bareDiags) != 0 {
				t.Fatalf("the workload without the group raised diagnostics: %+v", bareDiags)
			}
			files, diags, err := SynthesizeWorkloadCore(tc.build(true), nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth with the group: %v", err)
			}
			if len(diags) != 1 || diags[0].SourceRef != "runAsGroup" || diags[0].Code != rendercore.UnsupportedFieldCode {
				t.Fatalf("got %+v, want exactly the unsupported-field warning for runAsGroup", diags)
			}
			if !bytes.Equal(bareFiles[0].Body, files[0].Body) {
				t.Errorf("the dropped group changed rendered bytes (must be out-of-band)\n--- without ---\n%s\n--- with ---\n%s",
					bareFiles[0].Body, files[0].Body)
			}
		})
	}
}

// TestPodUserReachesEveryService proves the pod's user identity is carried by
// every service a workload renders to — the main one, a sidecar and an init
// container — as a pod runs each of its containers as the pod's user, and that a
// container naming its own user keeps it: with its own group it replaces the
// pod's and nothing is announced, and without one the pod's group reaches no part
// of that service and is announced.
func TestPodUserReachesEveryService(t *testing.T) {
	t.Parallel()
	workload := func(sidecar, setup func(c *types.ContainerSpec)) *types.Workload {
		w := baseDegradeWorkload()
		w.PodSecurityContext = &types.PodSecurityContext{RunAsUser: ptrInt(1000), RunAsGroup: ptrInt(2000)}
		withContainer(w, "log", sidecar)
		initC := types.ContainerSpec{Name: "setup", Image: "setup:1.0"}
		setup(&initC)
		w.InitContainers = []types.ContainerSpec{initC}
		return w
	}
	ownUser := func(c *types.ContainerSpec) {
		c.SecurityContext = &types.ContainerSecurityContext{RunAsUser: ptrInt(1500)}
	}
	none := func(*types.ContainerSpec) {}
	cases := []struct {
		name      string
		sidecar   func(c *types.ContainerSpec)
		setup     func(c *types.ContainerSpec)
		wantUsers map[string]string
		wantDiags []string
	}{
		{
			name:      "no container names a user",
			sidecar:   none,
			setup:     none,
			wantUsers: map[string]string{"api": "1000:2000", "api-log": "1000:2000", "api-setup": "1000:2000"},
		},
		{
			name: "the sidecar names its own user and group",
			sidecar: func(c *types.ContainerSpec) {
				c.SecurityContext = &types.ContainerSecurityContext{RunAsUser: ptrInt(1500), RunAsGroup: ptrInt(4000)}
			},
			setup:     none,
			wantUsers: map[string]string{"api": "1000:2000", "api-log": "1500:4000", "api-setup": "1000:2000"},
		},
		{
			name:      "the sidecar names its own user and no group",
			sidecar:   ownUser,
			setup:     none,
			wantUsers: map[string]string{"api": "1000:2000", "api-log": "1500", "api-setup": "1000:2000"},
			wantDiags: []string{"podSecurityContext.runAsGroup"},
		},
		{
			name:      "the init container names its own user and no group",
			sidecar:   none,
			setup:     ownUser,
			wantUsers: map[string]string{"api": "1000:2000", "api-log": "1000:2000", "api-setup": "1500"},
			wantDiags: []string{"podSecurityContext.runAsGroup"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, diags, err := SynthesizeWorkloadCore(workload(tc.sidecar, tc.setup), nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			got := serviceUsers(t, files[0].Body)
			for svc, want := range tc.wantUsers {
				if got[svc] != want {
					t.Errorf("service %q runs as %q, want %q\n%s", svc, got[svc], want, files[0].Body)
				}
			}
			if len(diags) != len(tc.wantDiags) {
				t.Fatalf("got %d diagnostics, want %d (all: %+v)", len(diags), len(tc.wantDiags), diags)
			}
			for i, d := range diags {
				if d.SourceRef != tc.wantDiags[i] || d.Code != rendercore.UnsupportedFieldCode {
					t.Errorf("diagnostic %d = %+v, want the unsupported-field warning for %s", i, d, tc.wantDiags[i])
				}
			}
		})
	}
}
