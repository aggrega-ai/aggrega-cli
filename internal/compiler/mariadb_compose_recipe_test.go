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
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestMariaDBRecipes_LoadCleanly is the smoke test for all four in-repo
// mariadb Recipes. Asserts each loads under the catalog resolver without
// triggering any of the seven Recipe load-time validation rules.
func TestMariaDBRecipes_LoadCleanly(t *testing.T) {
	t.Parallel()

	for _, p := range []string{
		"../../../aggrega-hub/aggrega-catalog/recipe/helm/mariadb-bitnami/1.0.0/recipe.yaml",
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
// healthcheck-style probes).
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

	composeFactory := func(string) (renderer.Engine, error) {
		return &renderCompose.ComposeEngine{}, nil
	}

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

	merged := string(body)

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

	// Healthcheck assertions are deferred: the compose Workload synthesizer's
	// probe→healthcheck translation is not yet exercised by this test surface
	// (the existing compose tests do not declare probes; whether
	// mysqladmin / tcp probes round-trip into a compose healthcheck block is
	// a separate verification scope tracked in the end-to-end proof matrix).
}
