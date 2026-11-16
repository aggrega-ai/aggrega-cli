// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end catalog-driven proof of the
// core/compose-bridge Feature + FeatureRecipe.
//
// This test reads catalog entities from the aggrega-hub repository checked out
// beside this one. Without that checkout it skips, naming it.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"go.yaml.in/yaml/v3"
)

// TestComposeBridge_CatalogDriven_E2E exercises the canonical catalog entries
// for core/compose-bridge end-to-end through the full Step 1-5 pipeline. Closes
// the inject-path verification gap that the renderer-level proof case at
// boundary_fr_proof_test.go does not cover.
//
// It compiles through the build-faithful harness (pipelinetest.Build), so it
// runs the real pipeline with the CLI build's Options — including
// resolution-completeness. The bridge feature `core/compose-bridge` therefore
// resolves to its real shipped FeatureRecipe (loaded from aggrega-hub below),
// whose own templates compute the bridge table, render the gateway's
// configuration and rewrite the consumer's DB_HOST through its injectMapping —
// the rendered output this test asserts on, not a hand-built graph.
//
// Fixture shape: parent boundary "cluster" attaches the bridge; child
// "services-net" holds api, child "data-net" holds db + cache (both
// core/db, port 5432). api uses both — two cross-network edges, a forced port
// collision. All contributors merge at the parent's evaluated path.
//
// Asserts a FUNCTIONAL multi-edge bridge:
//  1. One docker-compose.yaml at dist/cluster/compose/.
//  2. Services api, db, cache, aggrega-gw merged.
//  3. The gateway's generated nginx stream config (a mounted compose config,
//     not a base64 command) has a resolver plus one server per bridged edge
//     with deferred per-edge resolution (set $uN <host>; proxy_pass $uN), with
//     the collision fallback: listen 5432 -> cache:5432, listen 10000 -> db:5432.
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
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
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

	// Assertion 3: the gateway's nginx stream config, which the FeatureRecipe's
	// values write, is delivered as a mounted compose config (not a base64
	// command), carries the
	// Docker DNS resolver, and has one server per bridged edge with deferred
	// per-edge resolution (set $uN <host>:<port>; proxy_pass $uN). The port
	// collision falls back to portBase: db and cache both provide port 5432;
	// cache claims 5432 (earlier by edge key), db collides and falls back to
	// portBase 10000. No hand-authored address — the upstreams are edge-derived.
	// The $ in nginx stream variables is escaped to $$ so Docker Compose's
	// interpolation leaves a literal $ in the mounted file (asserting the
	// un-escaped $uN form is what passes while the deploy fails).
	conf := bridgeStreamConfigContent(t, merged)
	for _, want := range []string{
		"resolver 127.0.0.11 valid=10s;",
		"listen 5432;", "set $$u0 cache:5432;", "proxy_pass $$u0;",
		"listen 10000;", "set $$u1 db:5432;", "proxy_pass $$u1;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("gateway stream config missing %q; config content:\n%s", want, conf)
		}
	}
	// No base64 command delivery remains anywhere in the merged document.
	if strings.Contains(merged, "base64 -d") || strings.Contains(merged, "command:") {
		t.Errorf("expected no base64 command: delivery; found one:\n%s", merged)
	}
	// The gateway mounts the config at nginx's default config path.
	if !strings.Contains(merged, "target: /etc/nginx/nginx.conf") {
		t.Errorf("expected aggrega-gw to mount the config at /etc/nginx/nginx.conf:\n%s", merged)
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
	// FeatureRecipe's choice of bridged edges agrees with the assembler's
	// placement; without the match the gateway has networks that don't relay.
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

// TestComposeBridge_Deterministic is the compose provider's always-on
// double-run guard: the compose-bridge fixture compiles to byte-identical
// output across runs (the deterministic-output contract GitOps diffs rely on).
func TestComposeBridge_Deterministic(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, pipelinetest.Input{
		ReleasePath: "testdata/compose-bridge/release.yaml",
		CatalogPaths: []string{
			"testdata/compose-bridge/catalog/base",
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
		},
	})
}

// bridgeStreamConfigContent pulls the inline content of the aggrega-gw-nginx
// top-level compose config — the gateway's nginx stream config the
// FeatureRecipe writes, mounted as a file rather than delivered as a base64
// command.
func bridgeStreamConfigContent(t *testing.T, compose string) string {
	t.Helper()
	var doc struct {
		Configs map[string]struct {
			Content string `yaml:"content"`
		} `yaml:"configs"`
	}
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		t.Fatalf("parse compose for configs: %v", err)
		return ""
	}
	cfg, ok := doc.Configs["aggrega-gw-nginx"]
	if !ok {
		t.Fatalf("no aggrega-gw-nginx config in compose:\n%s", compose)
		return ""
	}
	return cfg.Content
}
