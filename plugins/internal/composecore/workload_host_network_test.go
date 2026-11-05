// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// hostNetworkWorkload builds a one-container workload publishing one port,
// optionally declaring that it runs on the node's own network.
func hostNetworkWorkload(hostNetwork bool) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", HostNetwork: hostNetwork}
	c := types.ContainerSpec{Image: "media:1.0"}
	c.Ports.Set("rtp", types.PortSpec{Port: 7881, Protocol: "UDP"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "LoadBalancer"}
	svc.Ports.Set("rtp", 7881)
	w.Services.Set("default", svc)
	return w
}

// TestSynthesizeWorkload_HostNetworkReachesTheEmittedBytes reads the declaration
// off the rendered compose fragment rather than off the parsed value. compose
// spells the choice of namespace as network_mode, and a declaration accepted by
// validation and dropped at synthesis would leave the service in its own
// namespace with every gate green — the silent loss this field exists to remove.
func TestSynthesizeWorkload_HostNetworkReachesTheEmittedBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		hostNetwork bool
		wantMode    bool
	}{
		{"undeclared emits no network_mode at all", false, false},
		{"declared emits network_mode: host", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, _, err := SynthesizeWorkloadCore(hostNetworkWorkload(tc.hostNetwork), nodeCtxFor("media"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			body := string(files[0].Body)

			if !tc.wantMode {
				if strings.Contains(body, "network_mode") {
					t.Errorf("a workload declaring nothing emitted a network_mode key:\n%s", body)
				}
				return
			}
			// Asserted against the value, not the key: the sidecar form
			// "service:<name>" is also a network_mode, so a key-only check would
			// pass on the wrong namespace entirely.
			if !strings.Contains(body, "network_mode: host") {
				t.Errorf("the workload's service does not carry network_mode: host:\n%s", body)
			}
		})
	}
}

// TestAssembleBoundary_HostNetworkServiceLeavesTheBoundaryNetwork is the
// interaction proof, and it is the reason this field could not be assumed to be
// a one-line synthesis change. compose refuses a service declaring both
// networks: and network_mode:, so a declaration that reached only the fragment
// would produce a boundary file compose will not start. The assembler already
// leaves a network_mode-bearing service off the network it attaches; this pins
// that a pod-level declaration reaches that path rather than merely the
// fragment, and that peers on the same boundary are unaffected.
func TestAssembleBoundary_HostNetworkServiceLeavesTheBoundaryNetwork(t *testing.T) {
	t.Parallel()

	peer := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	pc := types.ContainerSpec{Image: "peer:1.0"}
	pc.Ports.Set("http", types.PortSpec{Port: 8080})
	peer.Containers.Set("main", pc)
	peerSvc := types.ServiceSpec{Type: "ClusterIP"}
	peerSvc.Ports.Set("http", 8080)
	peer.Services.Set("api", peerSvc)

	files, _, err := AssembleBoundaryCore(
		map[string][]rendercore.EmittedFile{
			"media": makeFragment(t, "media", hostNetworkWorkload(true), nil),
			"peer":  makeFragment(t, "peer", peer, nil),
		},
		map[string]map[string]string{},
		nil,
		nil,
		"default",
		0,
	)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	body := string(files[AssembledOutputFile])

	media := serviceBlock(t, body, "media")
	if !strings.Contains(media, "network_mode: host") {
		t.Errorf("the host-networked service lost its network_mode through assembly:\n%s", media)
	}
	if strings.Contains(media, "networks:") {
		t.Errorf("the host-networked service was also attached to a network; compose refuses a service declaring both:\n%s", media)
	}

	// A peer on the same boundary is untouched — the declaration is scoped to
	// the workload that made it.
	peerBlock := serviceBlock(t, body, "peer")
	if !strings.Contains(peerBlock, "networks:") {
		t.Errorf("a peer that declared nothing lost its boundary network:\n%s", peerBlock)
	}
	if strings.Contains(peerBlock, "network_mode") {
		t.Errorf("a peer that declared nothing acquired a network_mode:\n%s", peerBlock)
	}
}

// TestSynthesizeWorkload_HostNetworkSidecarsFollowTheMain pins the composition
// with the sidecar model. A sidecar takes the main service's namespace by
// naming it, so when the main service moves to the node's network the sidecar
// follows it there without a second declaration — and must not be rewritten to
// "host" itself, which would put it in the node's namespace independently of
// the service it is meant to accompany.
func TestSynthesizeWorkload_HostNetworkSidecarsFollowTheMain(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", HostNetwork: true}
	main := types.ContainerSpec{Image: "media:1.0"}
	main.Ports.Set("rtp", types.PortSpec{Port: 7881})
	w.Containers.Set("main", main)
	w.Containers.Set("exporter", types.ContainerSpec{Image: "exporter:1.0"})
	svc := types.ServiceSpec{Type: "LoadBalancer"}
	svc.Ports.Set("rtp", 7881)
	w.Services.Set("default", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("media"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	body := string(files[0].Body)

	mainBlock := serviceBlock(t, body, "media")
	if !strings.Contains(mainBlock, "network_mode: host") {
		t.Errorf("the main service does not carry network_mode: host:\n%s", mainBlock)
	}
	sidecar := serviceBlock(t, body, "media--exporter")
	if !strings.Contains(sidecar, "network_mode: service:media") {
		t.Errorf("the sidecar must keep naming the main service rather than declaring the node's network itself:\n%s", sidecar)
	}
}

// TestSynthesizeWorkload_HostNetworkInitContainersAreReported pins the
// disclosed bound. An init container becomes its own service that joins the
// boundary network in its own right — sharing the main service's namespace
// would make the main service's wait on it circular — so the workload's own
// service runs on the node's network while its init services do not. That is
// narrower than the pod shape it models, and it is reported rather than left to
// be discovered at run time.
//
// The two negative rows are what make the positive one mean something: the
// advisory must be absent when there is no difference to report.
func TestSynthesizeWorkload_HostNetworkInitContainersAreReported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		hostNetwork   bool
		initContainer bool
		wantAdvisory  bool
	}{
		{"neither", false, false, false},
		{"init containers but no host network", false, true, false},
		{"host network but no init containers", true, false, false},
		{"both", true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := hostNetworkWorkload(tc.hostNetwork)
			if tc.initContainer {
				w.InitContainers = []types.ContainerSpec{{Name: "seed", Image: "seed:1.0"}}
			}
			_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("media"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}

			var found int
			for _, d := range diags {
				if d.SourceRef != "hostNetwork" {
					continue
				}
				found++
				if d.Severity != abi.SeverityWarn {
					t.Errorf("severity = %q, want warn", d.Severity)
				}
				if d.Code != rendercore.UnsupportedFieldCode {
					t.Errorf("code = %q, want %q", d.Code, rendercore.UnsupportedFieldCode)
				}
				if !strings.Contains(d.Message, "compose") {
					t.Errorf("message %q does not name the target", d.Message)
				}
				if !strings.Contains(d.Message, "init container") {
					t.Errorf("message %q does not say what is narrower", d.Message)
				}
			}
			want := 0
			if tc.wantAdvisory {
				want = 1
			}
			if found != want {
				t.Errorf("got %d hostNetwork advisories, want %d (all: %+v)", found, want, diags)
			}
		})
	}
}
