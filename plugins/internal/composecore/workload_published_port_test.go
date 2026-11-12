// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestSynthesizeWorkload_PublishesServicePortOntoNamedContainerPort pins how a
// service entry's number and the container port it names reach compose's
// published pair. The entry's number is the host side and the named container
// port's number is the container side, as a Kubernetes Service maps its port
// onto a targetPort. So an entry that publishes a container port under a
// different number reaches the port the image serves, and an entry carrying the
// container port's own number renders that number on both sides.
//
// The published list is read off the rendered compose file, so a mapping that
// puts the entry's number on both sides fails every row whose two numbers
// differ.
func TestSynthesizeWorkload_PublishesServicePortOntoNamedContainerPort(t *testing.T) {
	t.Parallel()
	type published struct {
		svcType string
		name    string
		number  int
	}
	cases := []struct {
		name      string
		container types.PortSpec
		entries   []published
		want      []string
	}{
		{
			name:      "a service port differing from the container port publishes onto the container port",
			container: types.PortSpec{Port: 8080},
			entries:   []published{{"NodePort", "http", 8081}},
			want:      []string{"8081:8080"},
		},
		{
			name:      "a service port equal to the container port publishes it on both sides, unchanged",
			container: types.PortSpec{Port: 8080},
			entries:   []published{{"NodePort", "http", 8080}},
			want:      []string{"8080:8080"},
		},
		{
			name:      "a LoadBalancer entry maps the same way",
			container: types.PortSpec{Port: 8080},
			entries:   []published{{"LoadBalancer", "http", 9000}},
			want:      []string{"9000:8080"},
		},
		{
			name:      "a Loopback entry maps onto the container port on the loopback address",
			container: types.PortSpec{Port: 8080},
			entries:   []published{{"Loopback", "http", 8081}},
			want:      []string{"127.0.0.1:8081:8080"},
		},
		{
			name:      "the container port's transport rides on the mapped pair",
			container: types.PortSpec{Port: 53, Protocol: "UDP"},
			entries:   []published{{"NodePort", "dns", 5353}},
			want:      []string{"5353:53/udp"},
		},
		{
			name:      "one container port published under two numbers renders both pairs, published port order",
			container: types.PortSpec{Port: 8080},
			entries:   []published{{"NodePort", "http", 9090}, {"LoadBalancer", "http", 8081}},
			want:      []string{"8081:8080", "9090:8080"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
			c := types.ContainerSpec{Image: "web:1.0"}
			portName := tc.entries[0].name
			c.Ports.Set(portName, tc.container)
			w.Containers.Set("main", c)
			for i, e := range tc.entries {
				svc := types.ServiceSpec{Type: e.svcType}
				svc.Ports.Set(e.name, e.number)
				w.Services.Set("entry"+string(rune('a'+i)), svc)
			}

			files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("web"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			var frag ComposeFile
			if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
				t.Fatalf("parse fragment: %v", err)
			}
			got := frag.Services["web"].Ports
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("published ports: got %q, want %q\n%s", got, tc.want, files[0].Body)
			}
		})
	}
}
