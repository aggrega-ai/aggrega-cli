// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// publishedPortWorkload builds a one-container workload publishing one port
// through a LoadBalancer service, with the port declared under the given
// transport. Only a NodePort or LoadBalancer entry publishes on compose, so
// this is the shape in which a transport can reach compose's output at all.
func publishedPortWorkload(protocol string) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "resolver:1.0"}
	c.Ports.Set("dns", types.PortSpec{Port: 53, Protocol: protocol})
	w.Containers.Set("main", c)

	svc := types.ServiceSpec{Type: "LoadBalancer"}
	svc.Ports.Set("dns", 53)
	w.Services.Set("default", svc)
	return w
}

// TestSynthesizeWorkload_PublishedPortCarriesItsTransport reads the transport
// off the rendered compose file rather than off the parsed value: compose
// spells a published port's transport as a suffix on the port entry, so a
// dropped transport is invisible in every other assertion.
func TestSynthesizeWorkload_PublishedPortCarriesItsTransport(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		proto string
		want  string
	}{
		{"no transport declared publishes with no suffix", "", "53:53"},
		{"an explicitly declared stream transport is spelled out", "TCP", "53:53/tcp"},
		{"a datagram transport reaches the published entry", "UDP", "53:53/udp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, _, err := SynthesizeWorkloadCore(publishedPortWorkload(tc.proto), nodeCtxFor("resolver"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			body := string(files[0].Body)
			if !strings.Contains(body, tc.want) {
				t.Errorf("published port %q absent from the rendered compose file:\n%s", tc.want, body)
			}
			// The no-transport row additionally pins that nothing was appended:
			// a suffix on a port that declared none would be a transport the
			// author never wrote.
			if tc.proto == "" && strings.Contains(body, "53:53/") {
				t.Errorf("a port declaring no transport was published with a suffix:\n%s", body)
			}
		})
	}
}

// TestSynthesizeWorkload_UnspellableTransportHalts pins the loud refusal. A
// compose published port spells its transport as tcp or udp; anything else has
// no compose spelling, and dropping the suffix would bring the deployment up
// carrying a transport its clients cannot reach — the failure the declarable
// transport exists to prevent, reintroduced at translation.
//
// The assertion is against the condition's own identity rather than against any
// error, so a different fault reaching this fixture fails rather than passing.
func TestSynthesizeWorkload_UnspellableTransportHalts(t *testing.T) {
	t.Parallel()
	defer func() {
		// A negative-input test asserts it did not panic: the runner does not
		// fail automatically on a recovered panic, and a panic here would
		// otherwise read as this test's own failure mode being untested.
		if r := recover(); r != nil {
			t.Fatalf("synthesis panicked on an unspellable transport instead of halting: %v", r)
		}
	}()

	_, _, err := SynthesizeWorkloadCore(publishedPortWorkload("SCTP"), nodeCtxFor("resolver"))
	if err == nil {
		t.Fatal("a published port declaring a transport compose cannot spell rendered without error")
	}
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Errorf("halt does not carry the value-not-representable condition: %v", err)
	}
	// The condition is shared with every other unrepresentable value this core
	// reports, so the sentence is what still says this was the transport.
	if want := "compose publishes a port's transport as a tcp or udp suffix; this port declares a transport compose cannot express"; !strings.Contains(errorChainText(err), want) {
		t.Errorf("the halt no longer says the transport is what has no representation.\n  want: %s\n  got:  %v", want, err)
	}
}

// TestSynthesizeWorkload_UnpublishedPortTransportIsNotAHalt records the bound
// deliberately. A container port that no service publishes reaches no part of a
// compose file at all — that is compose's standing container-port behaviour and
// predates the transport — so a transport on such a port has nothing to be
// spelled onto and cannot misrepresent anything. It must not halt, or an
// author would be refused for a port the target never emits.
func TestSynthesizeWorkload_UnpublishedPortTransportIsNotAHalt(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "resolver:1.0"}
	c.Ports.Set("dns", types.PortSpec{Port: 53, Protocol: "SCTP"})
	w.Containers.Set("main", c)
	svc := types.ServiceSpec{Type: "ClusterIP"} // ClusterIP publishes nothing on compose.
	svc.Ports.Set("dns", 53)
	w.Services.Set("default", svc)

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("resolver"))
	if err != nil {
		t.Fatalf("an unpublished port's transport halted: %v", err)
	}
	if body := string(files[0].Body); strings.Contains(body, "53:53") {
		t.Errorf("a ClusterIP entry published a port on compose:\n%s", body)
	}
}
