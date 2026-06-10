// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof of connection-scoped per-edge
// config: developer `values` (Aggregate) and operator `infra` (Release) on a
// `uses` edge, a Release-wired edge into a declared-but-unbound socket, the
// consumer-scoped safety property (a target renders identically regardless of
// its consumers' per-edge config), and the four Loud Guardrails.

package compiler_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

const usesEdgeMergedPath = "dist/cluster/compose/docker-compose.yaml"

func usesEdgeBuild(t *testing.T, releasePath string) string {
	t.Helper()
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  releasePath,
		CatalogPaths: []string{"testdata/uses-edge-config/catalog/base"},
	})
	body, ok := result.Artifacts[usesEdgeMergedPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected merged compose at %q; got %v", usesEdgeMergedPath, paths)
	}
	return string(body)
}

// TestUsesEdgeConfig_PositiveRendersConsumerConfig proves the per-edge config
// reaches the CONSUMER's real rendered output: developer `values` (protocol)
// and operator `infra` (pool_size, tls_mode) on the databases edge, plus the
// operator `infra` on the Release-wired caches edge.
func TestUsesEdgeConfig_PositiveRendersConsumerConfig(t *testing.T) {
	t.Parallel()
	merged := usesEdgeBuild(t, "testdata/uses-edge-config/release.yaml")

	for _, want := range []string{
		"DB_PROTOCOL", "grpc", // developer values, Aggregate
		"DB_POOL_SIZE", "50", // operator infra, Release (configure existing edge)
		"DB_TLS_MODE", "mutual", // operator infra, Release
		"CACHE_POOL", "25", // operator infra on the Release-WIRED caches edge
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("consumer output missing %q\n--- merged ---\n%s", want, merged)
		}
	}
}

// TestUsesEdgeConfig_TargetRenderInvariant is the real-output safety proof: the
// target (db) renders identically whether or not the consumer (api) attaches
// per-edge config and wires the extra cache edge. Only the consumer's output
// differs.
func TestUsesEdgeConfig_TargetRenderInvariant(t *testing.T) {
	t.Parallel()
	withCfg := usesEdgeBuild(t, "testdata/uses-edge-config/release.yaml")
	without := usesEdgeBuild(t, "testdata/uses-edge-config/release-no-edge-config.yaml")

	dbWith := composeService(t, withCfg, "db")
	dbWithout := composeService(t, without, "db")
	if !reflect.DeepEqual(dbWith, dbWithout) {
		t.Errorf("target db service perturbed by consumer per-edge config:\n with:    %#v\n without: %#v", dbWith, dbWithout)
	}

	apiWith := composeService(t, withCfg, "api")
	apiWithout := composeService(t, without, "api")
	if reflect.DeepEqual(apiWith, apiWithout) {
		t.Errorf("consumer api service did not change with per-edge config; projection not reaching output")
	}
}

// composeService unmarshals the merged docker-compose document and returns the
// named service subtree.
func composeService(t *testing.T, merged, name string) any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(merged), &doc); err != nil {
		t.Fatalf("unmarshal merged compose: %v", err)
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		t.Fatalf("merged compose has no services map; doc keys: %v", keysOf(doc))
	}
	svc, ok := services[name]
	if !ok {
		t.Fatalf("service %q not found; services: %v", name, keysOf(services))
	}
	return svc
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestUsesEdgeConfig_Guardrails exercises each Loud Guardrail end-to-end.
func TestUsesEdgeConfig_Guardrails(t *testing.T) {
	t.Parallel()

	t.Run("conformance", func(t *testing.T) {
		t.Parallel()
		// release-bad-infra.yaml is identical to the green positive release
		// except the per-edge infra pool_size is 999 (the socket infraSchema
		// caps it at 100) — an out-of-range per-edge infra conformance
		// violation. The Step-1 guard halt is errors.Is-recoverable at the
		// pipeline boundary; the count-substring check stays as belt-and-braces
		// that exactly one guard fired (the only authored delta from green).
		_, err := pipelinetest.BuildE(t, pipelinetest.Input{
			ReleasePath:  "testdata/uses-edge-config/release-bad-infra.yaml",
			CatalogPaths: []string{"testdata/uses-edge-config/catalog/base"},
		})
		if !errors.Is(err, resolver.ErrSchemaConformance) {
			t.Fatalf("want ErrSchemaConformance for an out-of-range per-edge infra; got %v", err)
		}
		if !strings.Contains(err.Error(), "1 guard violation") {
			t.Fatalf("want exactly one Step-1 guard violation; got %v", err)
		}
	})

	t.Run("retarget", func(t *testing.T) {
		t.Parallel()
		_, err := pipelinetest.BuildE(t, pipelinetest.Input{
			ReleasePath:  "testdata/uses-edge-config/release-retarget.yaml",
			CatalogPaths: []string{"testdata/uses-edge-config/catalog/base"},
		})
		if !errors.Is(err, topology.ErrReleaseEdgeRetarget) {
			t.Fatalf("want ErrReleaseEdgeRetarget for a Release retarget of a dev-declared edge; got %v", err)
		}
	})

	t.Run("dangling", func(t *testing.T) {
		t.Parallel()
		_, err := pipelinetest.BuildE(t, pipelinetest.Input{
			ReleasePath:  "testdata/uses-edge-config/release-dangling.yaml",
			CatalogPaths: []string{"testdata/uses-edge-config/catalog/base"},
		})
		if !errors.Is(err, topology.ErrDanglingUsesTarget) {
			t.Fatalf("want ErrDanglingUsesTarget for a Release edge to a non-existent target; got %v", err)
		}
	})

	t.Run("selector_origin_agnostic", func(t *testing.T) {
		t.Parallel()
		// A Release-CREATED edge is checked by the Step-4 selector gate exactly
		// like an Aggregate edge. The gate surfaces violations as analyzer
		// records joined into the build error (the reserved selector sentinel is
		// not wrapped), so assert the surfaced message rather than errors.Is.
		_, err := pipelinetest.BuildE(t, pipelinetest.Input{
			ReleasePath:  "testdata/uses-edge-config/release-selector.yaml",
			CatalogPaths: []string{"testdata/uses-edge-config/catalog/base"},
		})
		if err == nil {
			t.Fatal("want a selector-violation halt for a Release-created edge to a wrong-typed target; got nil")
		}
		if !strings.Contains(err.Error(), "selector") {
			t.Fatalf("want a selector-violation diagnostic; got %v", err)
		}
	})
}
