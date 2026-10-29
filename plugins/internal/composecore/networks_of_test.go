// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// networksOfBoundaryPath is the path the test file is assembled at, which is
// also the identity a node with no namespace annotation is placed on.
const networksOfBoundaryPath = "bridge"

// networksOfGroup is the dispatch group the tests assemble: two namespaced
// components, each on its own network, and a contributor with no namespace that
// writes the service under test. The agent's node id sorts ahead of both
// components, so the merge reaches the service naming them before it reaches
// either of them.
func networksOfGroup(agentService string) (map[string][]renderer.EmittedFile, map[string]map[string]string) {
	return map[string][]renderer.EmittedFile{
			"a-agent": {modelFragment("a-agent", "services:\n"+agentService)},
			"adminer": {modelFragment("adminer", "services:\n  adminer:\n    image: adminer:4\n")},
			"db":      {modelFragment("db", "services:\n  db:\n    image: postgres:16\n")},
		}, map[string]map[string]string{
			"a-agent": {},
			"adminer": {"aggrega.ai/namespace": "frontend"},
			"db":      {"aggrega.ai/namespace": "backend"},
		}
}

// assembleNetworksOf runs the merge over a group, failing the test on a panic
// rather than letting a recovered one pass as a halt.
func assembleNetworksOf(t *testing.T, fragments map[string][]renderer.EmittedFile, annotations map[string]map[string]string) (out string, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("assemble panicked: %v", r)
		}
	}()
	files, _, err := AssembleBoundaryCore(fragments, annotations, nil, nil, networksOfBoundaryPath, 2)
	if err != nil {
		return "", err
	}
	return string(files[AssembledOutputFile]), nil
}

// serviceKeysAndNetworks reads one service of an assembled file back: its keys
// in the order written, and its networks list in order.
func serviceKeysAndNetworks(t *testing.T, assembled, service string) ([]string, []string) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(assembled), &doc); err != nil {
		t.Fatalf("assembled file does not parse: %v\n%s", err, assembled)
	}
	root := doc.Content[0]
	services := mappingValue(root, "services")
	if services == nil {
		t.Fatalf("assembled file has no services:\n%s", assembled)
	}
	svc := mappingValue(services, service)
	if svc == nil {
		t.Fatalf("assembled file has no service %q:\n%s", service, assembled)
	}
	var keys, networks []string
	for i := 0; i+1 < len(svc.Content); i += 2 {
		keys = append(keys, svc.Content[i].Value)
	}
	if n := mappingValue(svc, "networks"); n != nil {
		for _, item := range n.Content {
			networks = append(networks, item.Value)
		}
	}
	return keys, networks
}

// TestComposeAssemble_NetworksOfJoinsTheNamedComponentsNetworks pins the
// resolution: a service naming components joins each one's network, the names
// deduplicated and sorted, ahead of its own network, which stays last; any
// network the fragment wrote for the service stays first; the member is gone
// from the assembled file while every other extension member is still carried.
func TestComposeAssemble_NetworksOfJoinsTheNamedComponentsNetworks(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		service      string
		wantNetworks []string
	}{
		{
			name:         "two components on two networks, the networks sorted rather than the ids",
			service:      "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [adminer, db]\n",
			wantNetworks: []string{"backend", "frontend", "bridge"},
		},
		{
			name:         "an id repeated and the agent's own contributor named",
			service:      "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [db, a-agent, db]\n",
			wantNetworks: []string{"backend", "bridge"},
		},
		{
			name:         "a network the fragment wrote stays first",
			service:      "  probe-agent:\n    image: busybox:1\n    networks: [backend]\n    x-aggrega-networks-of: [adminer, db]\n",
			wantNetworks: []string{"backend", "frontend", "bridge"},
		},
		{
			name:         "an empty list names nothing",
			service:      "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: []\n",
			wantNetworks: []string{"bridge"},
		},
		{
			name:         "the member written through an anchor",
			service:      "  probe-agent:\n    image: busybox:1\n    x-ids: &ids [adminer]\n    x-aggrega-networks-of: *ids\n",
			wantNetworks: []string{"frontend", "bridge"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fragments, annotations := networksOfGroup(tc.service)
			assembled, err := assembleNetworksOf(t, fragments, annotations)
			if err != nil {
				t.Fatalf("assemble refused a service naming components of its own file: %v", err)
			}
			keys, networks := serviceKeysAndNetworks(t, assembled, "probe-agent")
			if !reflect.DeepEqual(networks, tc.wantNetworks) {
				t.Errorf("probe-agent networks = %q, want %q\n%s", networks, tc.wantNetworks, assembled)
			}
			if strings.Contains(assembled, networksOfKey) {
				t.Errorf("assembled file still carries %s:\n%s", networksOfKey, assembled)
			}
			for _, k := range keys {
				if k == networksOfKey {
					t.Errorf("probe-agent still carries %s", networksOfKey)
				}
			}
			// The components named keep only their own network.
			for svc, want := range map[string]string{"adminer": "frontend", "db": "backend"} {
				if _, got := serviceKeysAndNetworks(t, assembled, svc); !reflect.DeepEqual(got, []string{want}) {
					t.Errorf("%s networks = %q, want [%q]", svc, got, want)
				}
			}
		})
	}
}

// TestComposeAssemble_NetworksOfKeepsOtherExtensionMembers pins that taking the
// member off a service leaves every other extension member of that service in
// place and in order.
func TestComposeAssemble_NetworksOfKeepsOtherExtensionMembers(t *testing.T) {
	t.Parallel()

	fragments, annotations := networksOfGroup(
		"  probe-agent:\n    image: busybox:1\n    x-before: 1\n    x-aggrega-networks-of: [db]\n    x-after: 2\n")
	assembled, err := assembleNetworksOf(t, fragments, annotations)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	keys, _ := serviceKeysAndNetworks(t, assembled, "probe-agent")
	want := []string{"image", "networks", "x-before", "x-after"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("probe-agent keys = %q, want %q\n%s", keys, want, assembled)
	}
}

// TestComposeAssemble_NetworksOfHalts covers each refusal, one fault per row,
// each asserted against the condition it raises and the names it carries.
func TestComposeAssemble_NetworksOfHalts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// service is the agent's service; extra adds a contributor with no
		// fragment, which holds a node id but contributes nothing to the file.
		service   string
		extra     bool
		condition error
		wantParts []string
	}{
		{
			name:      "an id naming no component of the file",
			service:   "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [adminer, elsewhere]\n",
			condition: wasm.ErrPluginRequiredInputUnresolved(),
			wantParts: []string{`service "probe-agent" names "elsewhere" in x-aggrega-networks-of`, `docker-compose.yaml at "bridge"`},
		},
		{
			name:      "an id of a contributor that contributes no fragment",
			service:   "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [ghost]\n",
			extra:     true,
			condition: wasm.ErrPluginRequiredInputUnresolved(),
			wantParts: []string{`service "probe-agent" names "ghost" in x-aggrega-networks-of`, `docker-compose.yaml at "bridge"`},
		},
		{
			name:      "a scalar",
			service:   "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: db\n",
			condition: wasm.ErrPluginCannotExpressInput(),
			wantParts: []string{`service "probe-agent"`, "x-aggrega-networks-of as something other than a list of strings"},
		},
		{
			name:      "a mapping",
			service:   "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: {db: true}\n",
			condition: wasm.ErrPluginCannotExpressInput(),
			wantParts: []string{`service "probe-agent"`, "x-aggrega-networks-of as something other than a list of strings"},
		},
		{
			name:      "a null",
			service:   "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of:\n",
			condition: wasm.ErrPluginCannotExpressInput(),
			wantParts: []string{`service "probe-agent"`, "x-aggrega-networks-of as something other than a list of strings"},
		},
		{
			name:      "a list holding a number",
			service:   "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [db, 7]\n",
			condition: wasm.ErrPluginCannotExpressInput(),
			wantParts: []string{`service "probe-agent"`, "x-aggrega-networks-of as something other than a list of strings"},
		},
		{
			name:      "a list holding a list",
			service:   "  probe-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [[db]]\n",
			condition: wasm.ErrPluginCannotExpressInput(),
			wantParts: []string{`service "probe-agent"`, "x-aggrega-networks-of as something other than a list of strings"},
		},
		{
			name:      "beside network_mode",
			service:   "  probe-agent:\n    image: busybox:1\n    network_mode: host\n    x-aggrega-networks-of: [db]\n",
			condition: wasm.ErrPluginCannotExpressInput(),
			wantParts: []string{`service "probe-agent"`, `x-aggrega-networks-of beside network_mode "host"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fragments, annotations := networksOfGroup(tc.service)
			if tc.extra {
				fragments["ghost"] = nil
				annotations["ghost"] = map[string]string{"aggrega.ai/namespace": "ghost"}
			}
			assembled, err := assembleNetworksOf(t, fragments, annotations)
			if err == nil {
				t.Fatalf("assemble accepted the member; want a halt\n%s", assembled)
			}
			if !errors.Is(err, tc.condition) {
				t.Fatalf("error = %v, want it to carry %v", err, tc.condition)
			}
			for _, want := range tc.wantParts {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not carry %q", err.Error(), want)
				}
			}
		})
	}
}

// TestComposeAssemble_NetworksOfNamesEveryFindingInOrder pins that one halt names
// every unresolved id of every service, services in name order and each
// service's ids in the order written, so the message is a function of the input
// and not of the order the merge happened to walk it in.
func TestComposeAssemble_NetworksOfNamesEveryFindingInOrder(t *testing.T) {
	t.Parallel()

	fragments, annotations := networksOfGroup(
		"  zz-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [nope-b]\n" +
			"  aa-agent:\n    image: busybox:1\n    x-aggrega-networks-of: [nope-z, db, nope-a, nope-z]\n")
	_, err := assembleNetworksOf(t, fragments, annotations)
	if err == nil {
		t.Fatal("assemble accepted ids naming nothing; want a halt")
	}
	const want = `compose AssembleBoundary: ` +
		`service "aa-agent" names "nope-z" in x-aggrega-networks-of; ` +
		`service "aa-agent" names "nope-a" in x-aggrega-networks-of; ` +
		`service "zz-agent" names "nope-b" in x-aggrega-networks-of; ` +
		`no component by that id is assembled into the docker-compose.yaml at "bridge", so it has no network in this file: ` +
		`name only components assembled into the same file, or remove the id`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("halt message:\n want it to contain: %s\n                got: %s", want, err.Error())
	}
}

// TestComposeAssemble_NoNetworksOfLeavesTheFileUnchanged pins that a group
// writing no member assembles exactly as it does with the resolution absent:
// every service on its own network alone.
func TestComposeAssemble_NoNetworksOfLeavesTheFileUnchanged(t *testing.T) {
	t.Parallel()

	fragments, annotations := networksOfGroup("  probe-agent:\n    image: busybox:1\n")
	assembled, err := assembleNetworksOf(t, fragments, annotations)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(assembled), &doc); err != nil {
		t.Fatalf("assembled file does not parse: %v", err)
	}
	var declared []string
	if n := mappingValue(doc.Content[0], "networks"); n != nil {
		for i := 0; i+1 < len(n.Content); i += 2 {
			declared = append(declared, n.Content[i].Value)
		}
	}
	if want := []string{"backend", "bridge", "frontend"}; !reflect.DeepEqual(declared, want) {
		t.Errorf("declared networks = %q, want %q\n%s", declared, want, assembled)
	}
	for svc, want := range map[string]string{"adminer": "frontend", "db": "backend", "probe-agent": "bridge"} {
		if _, got := serviceKeysAndNetworks(t, assembled, svc); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s networks = %q, want [%q]", svc, got, want)
		}
	}
}
