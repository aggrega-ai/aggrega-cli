// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// hostNetworkWorkload builds a one-container workload with a published port,
// optionally declaring that it runs on the node's own network. A port is
// included because the two are what a caller of this field pairs in practice,
// and their independence is part of what the assertions pin.
func hostNetworkWorkload(hostNetwork bool) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload", HostNetwork: hostNetwork}
	c := types.ContainerSpec{Image: "media:1.0"}
	c.Ports.Set("rtp", types.PortSpec{Port: 7881, Protocol: "UDP"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "LoadBalancer"}
	svc.Ports.Set("rtp", 7881)
	w.Services.Set("default", svc)
	return w
}

// TestSynthesizeWorkload_HostNetworkReachesTheEmittedBytes is the invocation
// proof: the declaration is read off the rendered Deployment rather than off the
// parsed value, because a key the schema accepts and synthesis drops is exactly
// the silent loss this field exists to remove — the manifest would come up in
// its own network namespace with every gate green.
//
// The undeclared row is the additive proof. omitempty elides a false bool, so a
// workload that says nothing must emit no hostNetwork key at all; an emitted
// `hostNetwork: false` would move bytes in every artifact rendered before this
// field existed.
func TestSynthesizeWorkload_HostNetworkReachesTheEmittedBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		hostNetwork bool
		wantKey     bool
	}{
		{"undeclared emits no key at all", false, false},
		{"declared reaches the pod spec", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, err := SynthesizeWorkloadCore(hostNetworkWorkload(tc.hostNetwork),
				renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("media"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			if len(files) != 2 {
				t.Fatalf("expected a Deployment and a Service, got %d files", len(files))
			}
			deployment := string(files[0].Body)

			if !tc.wantKey {
				if strings.Contains(deployment, "hostNetwork") {
					t.Errorf("a workload declaring nothing emitted a hostNetwork key:\n%s", deployment)
				}
				return
			}
			// Asserted against the emitted value, not merely the key: a
			// `hostNetwork: false` would satisfy a key-only check while
			// declaring the opposite of what was authored.
			if !strings.Contains(deployment, "hostNetwork: true") {
				t.Errorf("pod spec does not carry hostNetwork: true:\n%s", deployment)
			}
		})
	}
}

// TestSynthesizeWorkload_HostNetworkTouchesNothingElse pins the bound. The
// declaration chooses a network namespace and nothing else: it must not add,
// remove or rewrite a port, and it must not reach the Service. Without this the
// field could quietly acquire port-shaping behaviour that no clause asks for.
func TestSynthesizeWorkload_HostNetworkTouchesNothingElse(t *testing.T) {
	t.Parallel()
	plain, err := SynthesizeWorkloadCore(hostNetworkWorkload(false),
		renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("media"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (undeclared): %v", err)
	}
	hosted, err := SynthesizeWorkloadCore(hostNetworkWorkload(true),
		renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("media"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore (declared): %v", err)
	}

	// The Service is byte-identical across the two: nothing about which
	// namespace the pod runs in belongs to a Service.
	if string(plain[1].Body) != string(hosted[1].Body) {
		t.Errorf("declaring hostNetwork moved the Service bytes:\nwithout:\n%s\nwith:\n%s",
			plain[1].Body, hosted[1].Body)
	}

	// The Deployment differs by the single added line and nothing else.
	withoutLines := strings.Split(strings.TrimRight(string(plain[0].Body), "\n"), "\n")
	withLines := strings.Split(strings.TrimRight(string(hosted[0].Body), "\n"), "\n")
	if len(withLines) != len(withoutLines)+1 {
		t.Fatalf("expected exactly one added line, got %d vs %d:\n%s",
			len(withLines), len(withoutLines), hosted[0].Body)
	}
	var added []string
	for _, line := range withLines {
		if !containsLine(withoutLines, line) {
			added = append(added, line)
		}
	}
	if len(added) != 1 || strings.TrimSpace(added[0]) != "hostNetwork: true" {
		t.Errorf("the only difference should be the hostNetwork line, got %q", added)
	}

	// The port survives untouched on both sides.
	for _, body := range []string{string(plain[0].Body), string(hosted[0].Body)} {
		if !strings.Contains(body, "containerPort: 7881") {
			t.Errorf("container port lost:\n%s", body)
		}
	}
}

// containsLine reports whether lines holds an exact match for line. Used to
// diff two rendered manifests by content rather than by position.
func containsLine(lines []string, line string) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}
	return false
}
