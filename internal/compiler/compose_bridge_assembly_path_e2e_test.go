// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// This test reads catalog entities from the aggrega-hub repository checked out
// beside this one, as the compose-bridge end-to-end test does.

package compiler_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// composeFileShape reads what a merged compose file declares and names: each
// service's networks and environment, the top-level networks and configs.
type composeFileShape struct {
	Services map[string]struct {
		Networks    []string          `yaml:"networks"`
		Environment map[string]string `yaml:"environment"`
	} `yaml:"services"`
	Networks map[string]any `yaml:"networks"`
	Configs  map[string]any `yaml:"configs"`
}

// buildComposeBridgeVariant copies the compose-bridge fixture into a temporary
// tree, lets edit rewrite its Release, and builds it through the real pipeline
// with the shipped bridge entities, returning the one merged compose file.
func buildComposeBridgeVariant(t *testing.T, edit func(release string) string) (string, composeFileShape) {
	t.Helper()
	root := t.TempDir()
	copyDirTree(t, "testdata/compose-bridge", root)
	releasePath := filepath.Join(root, "release.yaml")
	src, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatalf("read the fixture Release: %v", err)
	}
	if err := os.WriteFile(releasePath, []byte(edit(string(src))), 0o644); err != nil {
		t.Fatalf("write the Release: %v", err)
	}
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath: releasePath,
		CatalogPaths: []string{
			filepath.Join(root, "catalog", "base"),
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
		},
	})
	const path = "dist/cluster/compose/docker-compose.yaml"
	body, ok := result.Artifacts[path]
	if !ok {
		t.Fatalf("no merged compose file at %s; files: %v", path, sortedKeys(result.Artifacts))
	}
	var shape composeFileShape
	if err := yaml.Unmarshal(body, &shape); err != nil {
		t.Fatalf("parse the merged compose file: %v\n%s", err, body)
	}
	return string(body), shape
}

// undeclaredNetworks names every network a service of the file joins that the
// file's top-level networks block does not declare, sorted.
func undeclaredNetworks(shape composeFileShape) []string {
	var out []string
	for name, svc := range shape.Services {
		for _, n := range svc.Networks {
			if _, declared := shape.Networks[n]; !declared {
				out = append(out, name+" -> "+n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestComposeBridge_ReadsTheNetworkTheAssemblerPlacesEachEndpointOn builds the
// compose-bridge fixture twice: as written, its two child boundaries annotated
// onto two namespaces, and with the two namespace annotations removed, so both
// children assemble into the parent's one file and share its one network. As
// written, the gateway is configured, joins the endpoints' networks and the
// consumers are rewritten to it. Without the annotations no edge crosses a
// network seam, so no gateway configuration is generated, the gateway joins no
// endpoint network and no consumer is rewritten. Both times every network a
// service joins is one the file declares.
func TestComposeBridge_ReadsTheNetworkTheAssemblerPlacesEachEndpointOn(t *testing.T) {
	t.Parallel()
	stripNamespaces := func(release string) string {
		for _, ns := range []string{"services", "data"} {
			block := "          annotations:\n            aggrega.ai/namespace: \"" + ns + "\"\n"
			if strings.Count(release, block) != 1 {
				t.Fatalf("the fixture Release carries %q %d time(s), want 1", block, strings.Count(release, block))
			}
			release = strings.Replace(release, block, "", 1)
		}
		return release
	}
	for _, tc := range []struct {
		name    string
		edit    func(string) string
		gateway bool
	}{
		{"annotated onto two namespaces", func(r string) string { return r }, true},
		{"unannotated, one file", stripNamespaces, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, shape := buildComposeBridgeVariant(t, tc.edit)
			_, hasConfig := shape.Configs["aggrega-gw-nginx"]
			if hasConfig != tc.gateway {
				t.Errorf("gateway config %v; want %v\n%s", hasConfig, tc.gateway, body)
			}
			// The gateway's networks: as written, the two endpoint networks and the
			// file's own; unbridged, the file's own alone, which the merge attaches.
			gw, hasGateway := shape.Services["aggrega-gw"]
			switch {
			case tc.gateway && !hasGateway:
				t.Errorf("no gateway service\n%s", body)
			case tc.gateway && len(gw.Networks) < 3:
				t.Errorf("a configured gateway joins %v; want the two endpoint networks and the file's own\n%s", gw.Networks, body)
			case !tc.gateway && hasGateway && len(gw.Networks) != 1:
				t.Errorf("an unconfigured gateway joins %v; want only the file's own network\n%s", gw.Networks, body)
			}
			rewritten := 0
			for _, svc := range shape.Services {
				for _, v := range svc.Environment {
					// A rewrite takes one of two forms: "aggrega-gw:<port>" for an
					// address variable alone, "aggrega-gw" where the connection also
					// delivers a port variable.
					if v == "aggrega-gw" || strings.HasPrefix(v, "aggrega-gw:") {
						rewritten++
					}
				}
			}
			if (rewritten > 0) != tc.gateway {
				t.Errorf("%d environment value(s) point at the gateway; want rewrites %v\n%s", rewritten, tc.gateway, body)
			}
			if undeclared := undeclaredNetworks(shape); len(undeclared) != 0 {
				t.Errorf("services join networks the file does not declare: %v\n%s", undeclared, body)
			}
		})
	}
}
