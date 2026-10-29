// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// loopbackService is one authored service entry for the loopback fixtures: a
// type and the container ports it names, in author order.
type loopbackService struct {
	name  string
	typ   string
	ports []string
}

// loopbackWorkload builds a one-container workload whose container declares
// four ports — two with no transport, one datagram and one message-oriented —
// and whose services are exactly the given entries, in the given order.
func loopbackWorkload(services ...loopbackService) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "prometheus:3.0"}
	c.Ports.Set("http", types.PortSpec{Port: 8080})
	c.Ports.Set("admin", types.PortSpec{Port: 9090})
	c.Ports.Set("syslog", types.PortSpec{Port: 5514, Protocol: "UDP"})
	c.Ports.Set("assoc", types.PortSpec{Port: 3868, Protocol: "SCTP"})
	w.Containers.Set("main", c)

	numbers := map[string]int{"http": 8080, "admin": 9090, "syslog": 5514, "assoc": 3868}
	for _, s := range services {
		svc := types.ServiceSpec{Type: s.typ}
		for _, p := range s.ports {
			svc.Ports.Set(p, numbers[p])
		}
		w.Services.Set(s.name, svc)
	}
	return w
}

// publishedPorts reads the main service's ports list off the rendered fragment,
// so a row asserts the entries compose will bind rather than the parsed value.
func publishedPorts(t *testing.T, body []byte, mainService string) []string {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse the rendered fragment: %v\n%s", err, body)
	}
	svc, ok := doc.Services[mainService]
	if !ok {
		t.Fatalf("the rendered fragment has no service %q:\n%s", mainService, body)
	}
	return svc.Ports
}

// TestSynthesizeWorkload_LoopbackPublishesOnTheLoopbackAddress reads every
// row's ports off the rendered compose fragment. A Loopback entry publishes its
// ports on the IPv4 loopback address only, with the same transport suffix and
// the same container-port order a published port takes; where an entry with a
// wider reach names the same port, the port is published on every interface
// whichever entry was authored first.
//
// The first row is the control: a workload with no Loopback entry renders the
// ports it rendered before the value existed, so the rows after it read a
// difference the value made rather than one the fixture did.
func TestSynthesizeWorkload_LoopbackPublishesOnTheLoopbackAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		services []loopbackService
		want     []string
	}{
		{
			name: "with no Loopback entry the published ports are unchanged",
			services: []loopbackService{
				{"public", "NodePort", []string{"http", "syslog"}},
				{"internal", "ClusterIP", []string{"admin"}},
			},
			want: []string{"5514:5514/udp", "8080:8080"},
		},
		{
			name:     "a Loopback entry publishes on the loopback address only",
			services: []loopbackService{{"local", "Loopback", []string{"admin"}}},
			want:     []string{"127.0.0.1:9090:9090"},
		},
		{
			name:     "a Loopback entry carries its port's declared transport",
			services: []loopbackService{{"local", "Loopback", []string{"syslog"}}},
			want:     []string{"127.0.0.1:5514:5514/udp"},
		},
		{
			name: "entries of different reach on different ports keep their own reach, in port order",
			services: []loopbackService{
				{"local", "Loopback", []string{"admin"}},
				{"public", "NodePort", []string{"http"}},
			},
			want: []string{"8080:8080", "127.0.0.1:9090:9090"},
		},
		{
			name: "a NodePort entry authored after a Loopback entry on one port widens it",
			services: []loopbackService{
				{"local", "Loopback", []string{"admin"}},
				{"public", "NodePort", []string{"admin"}},
			},
			want: []string{"9090:9090"},
		},
		{
			name: "a Loopback entry authored after a NodePort entry on one port does not narrow it",
			services: []loopbackService{
				{"public", "NodePort", []string{"admin"}},
				{"local", "Loopback", []string{"admin"}},
			},
			want: []string{"9090:9090"},
		},
		{
			name: "a LoadBalancer entry widens a port a Loopback entry names",
			services: []loopbackService{
				{"local", "Loopback", []string{"syslog"}},
				{"edge", "LoadBalancer", []string{"syslog"}},
			},
			want: []string{"5514:5514/udp"},
		},
		{
			name: "two Loopback entries naming one port publish it once",
			services: []loopbackService{
				{"local", "Loopback", []string{"admin"}},
				{"debug", "Loopback", []string{"admin"}},
			},
			want: []string{"127.0.0.1:9090:9090"},
		},
		{
			name: "ClusterIP and Headless entries beside a Loopback entry still publish nothing",
			services: []loopbackService{
				{"internal", "ClusterIP", []string{"http"}},
				{"peers", "Headless", []string{"syslog"}},
				{"local", "Loopback", []string{"admin"}},
			},
			want: []string{"127.0.0.1:9090:9090"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, _, err := SynthesizeWorkloadCore(loopbackWorkload(tc.services...), nodeCtxFor("metrics"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			got := publishedPorts(t, files[0].Body, "metrics")
			if !slices.Equal(got, tc.want) {
				t.Errorf("published ports = %q, want %q\n%s", got, tc.want, files[0].Body)
			}
			// The IPv6 loopback address is deliberately not bound: a second
			// entry naming it would fail to start on a host whose loopback
			// carries no IPv6 address.
			if strings.Contains(string(files[0].Body), "::1") {
				t.Errorf("a port was bound on the IPv6 loopback address:\n%s", files[0].Body)
			}
		})
	}
}

// TestSynthesizeWorkload_LoopbackRendersByteIdenticallyAcrossRuns holds the
// deterministic-output contract for the new value: the widest-reach merge reads
// a map, and its output must not depend on the map's iteration order.
func TestSynthesizeWorkload_LoopbackRendersByteIdenticallyAcrossRuns(t *testing.T) {
	t.Parallel()
	w := loopbackWorkload(
		loopbackService{"local", "Loopback", []string{"admin", "syslog", "http"}},
		loopbackService{"public", "NodePort", []string{"syslog"}},
	)
	first, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("metrics"))
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("metrics"))
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if string(again[0].Body) != string(first[0].Body) {
			t.Fatalf("run %d rendered different bytes:\n--- first ---\n%s\n--- again ---\n%s", i, first[0].Body, again[0].Body)
		}
	}
	want := []string{"5514:5514/udp", "127.0.0.1:8080:8080", "127.0.0.1:9090:9090"}
	if got := publishedPorts(t, first[0].Body, "metrics"); !slices.Equal(got, want) {
		t.Errorf("published ports = %q, want %q", got, want)
	}
}

// TestSynthesizeWorkload_LoopbackUnspellableTransportHalts pins that a Loopback
// entry is held to the same transport rule as every published port: a port
// whose transport compose cannot spell halts under the value-not-representable
// condition rather than publishing on loopback under a different transport.
func TestSynthesizeWorkload_LoopbackUnspellableTransportHalts(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("synthesis panicked on a Loopback entry naming an unspellable transport instead of halting: %v", r)
		}
	}()

	_, _, err := SynthesizeWorkloadCore(
		loopbackWorkload(loopbackService{"local", "Loopback", []string{"assoc"}}), nodeCtxFor("metrics"))
	if err == nil {
		t.Fatal("a Loopback entry naming a transport compose cannot spell rendered without error")
	}
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Errorf("halt does not carry the value-not-representable condition: %v", err)
	}
	if want := "compose publishes a port's transport as a tcp or udp suffix; this port declares a transport compose cannot express"; !strings.Contains(errorChainText(err), want) {
		t.Errorf("the halt no longer says the transport is what has no representation.\n  want: %s\n  got:  %v", want, err)
	}
}
