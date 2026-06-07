// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end catalog-driven proof of the
// core/compose-bridge Feature + FeatureRecipe.
//
// This test depends on both aggrega-cli AND aggrega-hub submodules being
// initialized in the umbrella checkout. Contributors running tests via
// `cd aggrega-cli && go test ./...` without `git submodule update --init`
// from the umbrella will see this test fail with a missing-catalog-entry
// error. Run from the umbrella or initialise submodules first.

package compiler_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"gopkg.in/yaml.v3"
)

// TestComposeBridge_CatalogDriven_E2E exercises the canonical catalog entries
// for core/compose-bridge end-to-end through the full Step 1-5 pipeline. Closes
// the inject-path verification gap that the renderer-level proof case at
// boundary_fr_proof_test.go does not cover.
//
// It compiles through the build-faithful harness (pipelinetest.Build), so it
// runs the real pipeline with the CLI build's Options — including Step 4c
// resolution-completeness. The bridge feature `core/compose-bridge` therefore
// resolves to its real shipped FeatureRecipe (loaded from aggrega-hub below),
// which renders the gateway service and rewrites the consumer's DB_HOST — the
// rendered output this test asserts on, not a hand-built graph.
//
// Fixture shape: parent boundary "cluster" attaches the bridge; child
// "services-net" holds api, child "data-net" holds db + cache (both
// core/db, port 5432). api uses both — two cross-network edges, a forced port
// collision. All contributors merge at the parent's evaluated path.
//
// Asserts a FUNCTIONAL multi-edge bridge:
//  1. One docker-compose.yaml at dist/cluster/compose/.
//  2. Services api, db, cache, aggrega-gw merged.
//  3. The gateway's generated nginx stream config has one server per bridged
//     edge, proxy_pass-ing the edge-derived upstream, with the collision
//     fallback: listen 5432 -> cache:5432, listen 10000 -> db:5432.
//  4. Each consumer rewrite == its edge's gateway listen port: CACHE_HOST ->
//     aggrega-gw:5432, DB_HOST -> aggrega-gw:10000.
//  5. The relay path: aggrega-gw joins the consumer's AND the targets' actual
//     networks (name-matched), so the bridge actually relays.
func TestComposeBridge_CatalogDriven_E2E(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath: "testdata/compose-bridge/release.yaml",
		CatalogPaths: []string{
			"testdata/compose-bridge/catalog/base",
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components.yaml",
		},
	})

	// Assertion 1: exactly one docker-compose.yaml at the merged path.
	const wantPath = "dist/cluster/compose/docker-compose.yaml"
	body, ok := result.Artifacts[wantPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected merged compose at %q; got paths %v", wantPath, paths)
	}

	merged := string(body)

	// Assertion 2: every component + the gateway merged into one file.
	for _, svcName := range []string{"api:", "db:", "cache:", "aggrega-gw:"} {
		if !strings.Contains(merged, svcName) {
			t.Errorf("merged docker-compose.yaml missing service %q\n--- body ---\n%s", svcName, merged)
		}
	}

	// Assertion 3: the gateway's compiler-generated nginx stream config (base64
	// command:) carries one server per bridged edge, proxy_pass-ing the
	// edge-derived upstream — and the port collision falls back to portBase.
	// db and cache both provide port 5432; cache claims 5432 (earlier by edge
	// key), db collides and falls back to portBase 10000. No hand-authored
	// address anywhere — the proxy_pass targets are the edge-derived hosts.
	conf := decodeBridgeStreamConfig(t, merged)
	for _, want := range []string{
		"listen 5432;", "proxy_pass cache:5432;",
		"listen 10000;", "proxy_pass db:5432;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("gateway stream config missing %q; decoded config:\n%s", want, conf)
		}
	}

	// Assertion 4: each consumer's rewrite equals its edge's gateway listen port
	// — the two halves agree per edge, collision included.
	for _, want := range []string{"CACHE_HOST: aggrega-gw:5432", "DB_HOST: aggrega-gw:10000"} {
		if !strings.Contains(merged, want) {
			t.Errorf("expected consumer rewrite %q (== gateway listen):\n%s", want, merged)
		}
	}

	// Assertion 5: the relay path is complete — aggrega-gw joins the consumer's
	// network AND the targets' network, matched by NAME against the components'
	// actual assigned networks (not merely "has two networks"). This proves the
	// projection's network derivation agrees with the assembler's placement;
	// without the match the gateway has networks that don't relay.
	nets := parseServiceNetworks(t, merged)
	gw := nets["aggrega-gw"]
	if len(gw) == 0 {
		t.Fatalf("aggrega-gw has no networks; relay impossible:\n%s", merged)
	}
	for _, comp := range []string{"api", "db", "cache"} {
		for _, n := range nets[comp] {
			if !sliceContains(gw, n) {
				t.Errorf("aggrega-gw networks %v must include %s's actual network %q (relay path incomplete)", gw, comp, n)
			}
		}
	}
}

// parseServiceNetworks unmarshals the merged compose document and returns each
// service's networks list.
func parseServiceNetworks(t *testing.T, compose string) map[string][]string {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		t.Fatalf("parse compose for service networks: %v", err)
	}
	out := map[string][]string{}
	for name, svc := range doc.Services {
		out[name] = svc.Networks
	}
	return out
}

func sliceContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// decodeBridgeStreamConfig pulls the base64-encoded nginx config out of the
// aggrega-gw service's generated command and decodes it.
func decodeBridgeStreamConfig(t *testing.T, compose string) string {
	t.Helper()
	const echoMarker = "echo "
	i := strings.Index(compose, echoMarker)
	if i < 0 {
		t.Fatalf("no bridge gateway command (echo <b64>) in compose:\n%s", compose)
		return ""
	}
	rest := compose[i+len(echoMarker):]
	j := strings.Index(rest, " | base64 -d")
	if j < 0 {
		t.Fatalf("malformed bridge gateway command (no base64 decode) in compose:\n%s", compose)
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[:j]))
	if err != nil {
		t.Fatalf("bridge gateway command base64 decode: %v", err)
		return ""
	}
	return string(decoded)
}
