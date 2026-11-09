// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// portProtocolWorkload builds a one-container workload exposing one port
// through one LoadBalancer service, with the port declared under the given
// transport. An empty protocol declares none, which is the shape every port
// authored before the transport existed carries.
func portProtocolWorkload(protocol string) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "resolver:1.0"}
	c.Ports.Set("dns", types.PortSpec{Port: 53, Protocol: protocol})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "LoadBalancer"}
	svc.Ports.Set("dns", 53)
	w.Services.Set("default", svc)
	return w
}

// TestSynthesizeWorkload_PortProtocolReachesTheEmittedBytes is the invocation
// proof for the transport a port declares: it is read off the rendered
// manifests rather than off the parsed value, because a key the schema accepts
// and synthesis drops is exactly the silent loss the field exists to remove.
//
// It covers both emission sites in one pass — the container port and the
// Service port derived from it — because the two are declared once and must not
// be able to disagree.
func TestSynthesizeWorkload_PortProtocolReachesTheEmittedBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		protocol         string
		wantContainerFrg string
		wantServiceFrg   string
	}{
		{
			name:             "no transport declared emits no protocol key",
			protocol:         "",
			wantContainerFrg: "",
			wantServiceFrg:   "",
		},
		{
			name:             "an explicitly declared stream transport is emitted explicitly",
			protocol:         "TCP",
			wantContainerFrg: "protocol: TCP",
			wantServiceFrg:   "protocol: TCP",
		},
		{
			name:             "a datagram transport reaches both the container port and its service",
			protocol:         "UDP",
			wantContainerFrg: "protocol: UDP",
			wantServiceFrg:   "protocol: UDP",
		},
		{
			name:             "a message-oriented transport reaches both",
			protocol:         "SCTP",
			wantContainerFrg: "protocol: SCTP",
			wantServiceFrg:   "protocol: SCTP",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, err := SynthesizeWorkloadCore(portProtocolWorkload(tc.protocol),
				renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("resolver"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			if len(files) != 2 {
				t.Fatalf("expected a Deployment and a Service, got %d files", len(files))
			}
			deployment, service := string(files[0].Body), string(files[1].Body)

			// The deployment carries the container port; the service carries the
			// exposure derived from it. Asserting per file rather than over the
			// concatenation is what makes a transport reaching only one of the
			// two a failure rather than a pass.
			if tc.wantContainerFrg == "" {
				if strings.Contains(deployment, "protocol:") {
					t.Errorf("a port declaring no transport emitted a protocol key:\n%s", deployment)
				}
				if strings.Contains(service, "protocol:") {
					t.Errorf("a service port whose container port declares no transport emitted a protocol key:\n%s", service)
				}
				return
			}
			if !strings.Contains(deployment, tc.wantContainerFrg) {
				t.Errorf("container port does not carry %q:\n%s", tc.wantContainerFrg, deployment)
			}
			if !strings.Contains(service, tc.wantServiceFrg) {
				t.Errorf("service port does not carry %q:\n%s", tc.wantServiceFrg, service)
			}
		})
	}
}

// TestSynthesizeWorkload_ServicePortTakesTheContainerPortTransport pins the
// single-declaration-site rule from the other direction: the service entry
// carries no transport of its own, so the one on the emitted Service can only
// have come from the container port the entry names.
func TestSynthesizeWorkload_ServicePortTakesTheContainerPortTransport(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "gateway:1.0"}
	c.Ports.Set("api", types.PortSpec{Port: 8080})
	c.Ports.Set("dns", types.PortSpec{Port: 53, Protocol: "UDP"})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("api", 8080)
	svc.Ports.Set("dns", 53)
	w.Services.Set("default", svc)

	files, err := SynthesizeWorkloadCore(w, renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("gateway"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	service := string(files[1].Body)

	// Exactly one of the two exposures carries a transport: the one whose
	// container port declared it. A blanket stamp would produce two.
	if got := strings.Count(service, "protocol:"); got != 1 {
		t.Errorf("expected exactly one service port to carry a transport, got %d:\n%s", got, service)
	}
	if !strings.Contains(service, "protocol: UDP") {
		t.Errorf("the datagram exposure lost its transport:\n%s", service)
	}
}
