// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of a compose service naming the components whose networks
// it joins, through the real pipeline and the embedded compose provider.
//
// A FeatureRecipe generating once for its boundary writes an agent service and
// lists, under x-aggrega-networks-of, both endpoints of every uses edge inside
// the boundary, read off the boundary view rather than written out — so the ids
// the merge resolves are the ids a FeatureRecipe can read. The two components sit
// in two child boundaries, each naming its own network through the
// aggrega.ai/namespace annotation. One Release assembles both into the agent's
// file; the other assembles the database into a file of its own.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// composeNetworksOfInput compiles one Release of the fixture, over the
// feature-matrix components and compose Recipes it shares.
func composeNetworksOfInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/compose-networks-of/" + release,
		CatalogPaths: []string{
			"testdata/feature-matrix/catalog/base",
			"testdata/feature-matrix/catalog/recipes/compose-sourced-with-bridge",
			"testdata/compose-networks-of/catalog",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
		},
	}
}

// composeServiceNetworks reads the networks list of one service of an assembled
// compose file, in order, and the names the file declares under networks:.
func composeServiceNetworks(t *testing.T, assembled []byte, service string) (joined, declared []string) {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
		Networks map[string]any `yaml:"networks"`
	}
	if err := yaml.Unmarshal(assembled, &doc); err != nil {
		t.Fatalf("assembled file does not parse: %v\n%s", err, assembled)
	}
	svc, ok := doc.Services[service]
	if !ok {
		t.Fatalf("assembled file has no service %q:\n%s", service, assembled)
	}
	for name := range doc.Networks {
		declared = append(declared, name)
	}
	return svc.Networks, declared
}

// TestComposeNetworksOf_AgentJoinsTheNetworksOfTheComponentsItNames builds the
// Release assembling both components into the agent's file: the agent joins the
// database's and the server's networks in name order and then its own, which the
// file declares beside them, and the member does not reach the file.
func TestComposeNetworksOf_AgentJoinsTheNetworksOfTheComponentsItNames(t *testing.T) {
	t.Parallel()

	result, err := pipelinetest.BuildE(t, composeNetworksOfInput("release.yaml"))
	if err != nil {
		t.Fatalf("the Release did not build: %s", cerrors.CauseChain(err))
	}
	var assembled []byte
	for path, content := range result.Artifacts {
		if strings.HasSuffix(path, "dist/cluster/compose/docker-compose.yaml") {
			assembled = content
		}
	}
	if assembled == nil {
		t.Fatalf("no assembled compose file at dist/cluster/compose/docker-compose.yaml; files: %v", sortedKeys(result.Artifacts))
	}
	if strings.Contains(string(assembled), "x-aggrega-networks-of") {
		t.Errorf("the assembled file still carries x-aggrega-networks-of:\n%s", assembled)
	}

	joined, declared := composeServiceNetworks(t, assembled, "networks-agent")
	if len(joined) != 3 || joined[0] != "data" || joined[1] != "services" {
		t.Fatalf("networks-agent networks = %q, want [data services <its own>]\n%s", joined, assembled)
	}
	own := joined[2]
	if own == "data" || own == "services" {
		t.Errorf("networks-agent's last network is %q, a named component's, not its own\n%s", own, assembled)
	}
	for _, want := range []string{"data", "services", own} {
		found := false
		for _, d := range declared {
			found = found || d == want
		}
		if !found {
			t.Errorf("the file does not declare network %q the agent joins; declared: %q", want, declared)
		}
	}
	for svc, want := range map[string]string{"api": "services", "db": "data"} {
		if got, _ := composeServiceNetworks(t, assembled, svc); len(got) != 1 || got[0] != want {
			t.Errorf("%s networks = %q, want [%q]", svc, got, want)
		}
	}
}

// TestComposeNetworksOf_ComponentInAnotherFileHalts builds the Release
// assembling the database into a file of its own: the agent naming it halts,
// under the condition for a reference to something the file does not hold,
// naming the service, the id and the file.
func TestComposeNetworksOf_ComponentInAnotherFileHalts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, composeNetworksOfInput("release-cross-file.yaml"))
	if err == nil {
		t.Fatal("the Release naming a component assembled into another file built; want a halt")
	}
	if !errors.Is(err, wasm.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("got %s; want the refusal of an id naming no component of the file", cerrors.CauseChain(err))
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		`service "networks-agent" names "db" in x-aggrega-networks-of`,
		`no component by that id is assembled into the docker-compose.yaml at "dist/cluster/compose"`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
	if strings.Contains(chain, `names "api"`) {
		t.Errorf("the halt names the server, which is assembled into the agent's file:\n%s", chain)
	}
}
