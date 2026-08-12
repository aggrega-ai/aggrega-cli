// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Per-Recipe integration tests for the in-repo mariadb Recipe set.
//
// These tests depend on both aggrega-cli AND aggrega-hub submodules being
// initialized in the umbrella checkout (paths cross the submodule boundary
// into aggrega-hub). Run from the umbrella or initialise submodules first.

package compiler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// composeWasmFactory renders through the embedded compose provider guest via the
// production wasmengine assembler adapter — the Wasm-only render backend that
// replaces the removed in-tree compose engine for this end-to-end recipe proof.
func composeWasmFactory(t *testing.T) renderer.EngineFactory {
	t.Helper()
	loader := wasm.NewCachingLoader(nil)
	var uri string
	for _, u := range embedded.URIs() {
		if strings.HasSuffix(u, "/compose") {
			uri = u
			break
		}
	}
	if uri == "" {
		t.Fatalf("no embedded compose guest; embedded URIs: %v", embedded.URIs())
	}
	lookup := func(u string) (pluginpath.ResolvedPlugin, error) {
		b, ok := embedded.Lookup(u)
		if !ok {
			return pluginpath.ResolvedPlugin{}, fmt.Errorf("no embedded guest for %q", u)
		}
		return pluginpath.ResolvedPlugin{Bytes: b, URI: u, Tier: pluginpath.TierEmbed, Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096}}, nil
	}
	return func(string) (renderer.Provider, error) {
		return wasmengine.NewAssembler(lookup, uri, loader), nil
	}
}

// TestMariaDBRecipes_LoadCleanly is the smoke test for all three in-repo
// mariadb Recipes. Asserts each loads under the catalog resolver without
// triggering any of the seven Recipe load-time validation rules.
func TestMariaDBRecipes_LoadCleanly(t *testing.T) {
	t.Parallel()

	for _, p := range []string{
		"../../../aggrega-hub/aggrega-catalog/recipe/compose/mariadb/1.0.0/recipe.yaml",
		"../../../aggrega-hub/aggrega-catalog/recipe/terraform/mariadb-rds/1.0.0/recipe.yaml",
		"../../../aggrega-hub/aggrega-catalog/recipe/cloudformation/mariadb-rds/1.0.0/recipe.yaml",
	} {
		if _, err := catalog.LoadAll("", p); err != nil {
			t.Errorf("Recipe %q failed to load: %v", p, err)
		}
	}
}

// TestMariaDBComposeRecipe_WorkloadSynthesis exercises the compose mariadb
// Recipe end-to-end at the renderer level: builds a one-node IR graph,
// runs RenderAll with the compose engine, and asserts the rendered
// docker-compose.yaml carries the mariadb service body the Recipe's
// Workload describes (image, env vars, persistent volume mount,
// healthcheck).
//
// IR-level direct construction (matching boundary_fr_proof_test.go pattern)
// because this test focuses on Recipe authoring correctness, not on full
// Aggregate→Release→IR pipeline. Aggregate-level requires (the mariadb
// Component declares root_password.required) belong in the end-to-end
// proof matrix, not here.
func TestMariaDBComposeRecipe_WorkloadSynthesis(t *testing.T) {
	t.Parallel()

	cat, err := catalog.LoadAll("",
		"../../../aggrega-hub/aggrega-catalog/component/core/datastore",
		"../../../aggrega-hub/aggrega-catalog/recipe/compose/mariadb",
		"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
	)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{
		Component:    "core/datastore/mariadb:1.0.0",
		BoundaryPath: "default",
	})

	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("db", json.RawMessage(`{"database":"appdb","charset":"utf8mb4","collation":"utf8mb4_general_ci","port":3306}`))

	infraMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping.Set("db", json.RawMessage(`{"engine_version":"11.4","compute":{"cpu":"250m","memory":"512Mi"},"storage":{"size":"10Gi","type":"gp3"},"ha":{"enabled":false,"read_replicas":0},"backup":{"enabled":true,"retention_days":7}}`))

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "mariadb-test"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: valuesMapping,
				InfraMapping:  infraMapping,
				Provenance:    types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "mariadb-test"}},
			Release:   &types.Release{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				"db": {BoundaryPath: "default"},
			},
		},
	}

	composeFactory := composeWasmFactory(t)

	mt := &types.MappingTier{}
	mt.Type.Set("core/datastore/mariadb:1.0.0", "mariadb")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: mt}}}

	result, err := renderer.RenderAll(
		context.Background(),
		input,
		cat.RecipeLookupWithManifest(manifest),
		composeFactory,
	)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	const wantPath = "boundaries/default/compose/docker-compose.yaml"
	body, ok := result.Files[wantPath]
	if !ok {
		paths := make([]string, 0, len(result.Files))
		for p := range result.Files {
			paths = append(paths, p)
		}
		t.Fatalf("expected compose at %q; got paths %v", wantPath, paths)
	}

	merged := string(body.Content)

	// Image: defaults from infra.engine_version unless overrides.image
	// supplies a different reference.
	if !strings.Contains(merged, "mariadb:11.4") {
		t.Errorf("expected image 'mariadb:11.4' (from infra.engine_version) in rendered compose:\n%s", merged)
	}

	// Environment: MARIADB_DATABASE from values.database; charset and
	// collation from the values defaults.
	for _, want := range []string{
		"MARIADB_DATABASE: appdb",
		"MARIADB_CHARACTER_SET_SERVER: utf8mb4",
		"MARIADB_COLLATION_SERVER: utf8mb4_general_ci",
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("expected env entry %q in rendered compose:\n%s", want, merged)
		}
	}

	// Persistent volume mount + top-level named volume from the workload's
	// volumes.data.persistent block.
	if !strings.Contains(merged, "/var/lib/mysql") {
		t.Errorf("expected persistent mount path '/var/lib/mysql' in rendered compose:\n%s", merged)
	}
	if !strings.Contains(merged, "db-data") {
		t.Errorf("expected named volume 'db-data' (workload volume 'data' on node 'db') in rendered compose:\n%s", merged)
	}

	// Healthcheck: the Recipe's container health block reaches the rendered
	// service. The exec probe becomes a CMD-form test; interval carries
	// periodSeconds and timeout carries timeoutSeconds. Asserted as one exact
	// block rather than as scattered substrings, because the shape and the
	// indentation are both part of what a compose runtime consumes.
	const wantHealthcheck = "    healthcheck:\n" +
		"      test:\n" +
		"        - CMD\n" +
		"        - mysqladmin\n" +
		"        - ping\n" +
		"      interval: 10s\n" +
		"      timeout: 5s\n"
	if !strings.Contains(merged, wantHealthcheck) {
		t.Errorf("expected healthcheck block\n%s\nin rendered compose:\n%s", wantHealthcheck, merged)
	}

	// Compose carries a single healthcheck per service, so the container's
	// readiness probe collapses away in favour of the liveness one. Asserting
	// its absence keeps the collapse a stated property of the rendered output
	// rather than a silent one.
	if strings.Contains(merged, "nc -z localhost 3306") {
		t.Errorf("expected the tcp readiness probe to collapse away (compose carries one healthcheck per service), but its translation is present in rendered compose:\n%s", merged)
	}
}
