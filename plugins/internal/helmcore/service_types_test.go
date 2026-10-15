// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// serviceTypeEntry is one authored service entry: its name, its type and the
// one container port it names.
type serviceTypeEntry struct {
	name string
	typ  string
	port string
}

// serviceTypeWorkload builds a workload of the given form whose container
// declares two ports and whose services are exactly the given entries, in the
// given order.
func serviceTypeWorkload(form string, entries ...serviceTypeEntry) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form}
	c := types.ContainerSpec{Image: "prometheus:3.0"}
	c.Ports.Set("http", types.PortSpec{Port: 8080})
	c.Ports.Set("admin", types.PortSpec{Port: 9090})
	w.Containers.Set("main", c)
	numbers := map[string]int{"http": 8080, "admin": 9090}
	for _, e := range entries {
		svc := types.ServiceSpec{Type: e.typ}
		svc.Ports.Set(e.port, numbers[e.port])
		w.Services.Set(e.name, svc)
	}
	return w
}

// TestSynthesizeWorkload_ServiceTypesHelmRenders is the positive control for the
// refusal below: every service type a Kubernetes Service expresses renders, one
// Service per entry, carrying the type it was declared with — Headless as a
// ClusterIP Service with no cluster address.
func TestSynthesizeWorkload_ServiceTypesHelmRenders(t *testing.T) {
	t.Parallel()
	files, err := synthNoPanic(t, serviceTypeWorkload("",
		serviceTypeEntry{"internal", "ClusterIP", "http"},
		serviceTypeEntry{"public", "NodePort", "http"},
		serviceTypeEntry{"edge", "LoadBalancer", "admin"},
		serviceTypeEntry{"peers", "Headless", "admin"},
	))
	if err != nil {
		t.Fatalf("the four service types a Service expresses were refused: %v", err)
	}
	want := map[string][]string{
		"store-internal-service.yaml": {"type: ClusterIP"},
		"store-public-service.yaml":   {"type: NodePort"},
		"store-edge-service.yaml":     {"type: LoadBalancer"},
		"store-peers-service.yaml":    {"type: ClusterIP", "clusterIP: None"},
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Name] = string(f.Body)
	}
	for name, fragments := range want {
		body, ok := got[name]
		if !ok {
			t.Errorf("no %s emitted; files: %v", name, emittedFileNames(files))
			continue
		}
		for _, frag := range fragments {
			if !strings.Contains(body, frag) {
				t.Errorf("%s does not carry %q:\n%s", name, frag, body)
			}
		}
	}
}

// TestSynthesizeWorkload_LoopbackServiceHalts pins the refusal. A Kubernetes
// Service has no type that binds to a host's loopback address, so a Loopback
// entry halts by name under the value-not-representable condition and emits
// nothing at all — never a ClusterIP Service, which would quietly drop the reach
// the author asked for, and never a NodePort one, which would publish the ports
// on every node address the cluster allows.
//
// Each row carries one Loopback fault; the assertion is against the condition's
// identity and the sentence naming the node, the entry and the value, so a
// different fault reaching a fixture fails rather than passes.
func TestSynthesizeWorkload_LoopbackServiceHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		w       *types.Workload
		wantFor string
	}{
		{
			name:    "a Loopback entry alone",
			w:       serviceTypeWorkload("", serviceTypeEntry{"local", "Loopback", "admin"}),
			wantFor: "local",
		},
		{
			name: "a Loopback entry after entries a Service expresses is named, not the ones before it",
			w: serviceTypeWorkload("",
				serviceTypeEntry{"internal", "ClusterIP", "http"},
				serviceTypeEntry{"public", "NodePort", "http"},
				serviceTypeEntry{"local", "Loopback", "admin"}),
			wantFor: "local",
		},
		{
			name: "of two Loopback entries the first authored is named",
			w: serviceTypeWorkload("",
				serviceTypeEntry{"debug", "Loopback", "http"},
				serviceTypeEntry{"local", "Loopback", "admin"}),
			wantFor: "debug",
		},
		{
			name: "a stateful workload is refused on the Loopback entry, ahead of its governing-Service check",
			w: serviceTypeWorkload("stateful",
				serviceTypeEntry{"peers", "Headless", "http"},
				serviceTypeEntry{"local", "Loopback", "admin"}),
			wantFor: "local",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, err := synthNoPanic(t, tc.w)
			if err == nil {
				t.Fatalf("a Loopback service entry rendered on helm; files: %v", emittedFileNames(files))
			}
			if !errors.Is(err, wasm.ErrPluginValueNotRepresentable()) {
				t.Fatalf("halt does not carry the value-not-representable condition: %v", err)
			}
			if len(files) != 0 {
				t.Errorf("a refused workload still emitted files: %v", emittedFileNames(files))
			}
			want := `helm: node "store": services.` + tc.wantFor + `: type "Loopback" publishes the service's ports on ` +
				`the host's loopback address only, and a Kubernetes Service has no type that does`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the halt does not name the node, the entry and the value.\n  want: %s\n  got:  %v", want, err)
			}
		})
	}
}

// emittedFileNames lists the names of emitted files for a failure message.
func emittedFileNames(files []renderer.EmittedFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}
