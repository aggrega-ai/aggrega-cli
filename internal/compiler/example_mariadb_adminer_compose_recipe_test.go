// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Per-Recipe integration test for the compose Recipe carried by the
// mariadb-adminer worked example in the language reference.
//
// The example ships its own catalog rather than pointing at the standard
// library, so its Recipe is a distinct authored artifact that needs its own
// covering render. Entity-level linting reaches the example already, but at
// that layer a Recipe's workload is an opaque template string, so nothing
// there observes what the synthesized Workload actually contains.
//
// This test reads its catalog from the aggrega-docs repository checked out
// beside this one (the catalog path leaves this repository for aggrega-docs),
// so check out aggrega-docs beside this repository first.
//
// composeWasmFactory is shared with the mariadb and mysql Recipe tests in this
// same package rather than duplicated here, so every compose Recipe proof
// renders through one definition of the embedded compose guest.

package compiler_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestExampleMariaDBAdminerComposeRecipe_WorkloadSynthesis exercises the
// compose Recipe from the mariadb-adminer worked example end-to-end at the
// renderer level: builds a one-node IR graph from the example's own catalog,
// runs RenderAll with the compose engine, and asserts the rendered
// docker-compose.yaml carries the mariadb service body the Recipe's Workload
// describes — image, env vars, persistent volume mount, and the container
// healthcheck.
//
// IR-level direct construction because this test focuses on Recipe authoring
// correctness, not on the full Aggregate→Release→IR pipeline. Aggregate-level
// requires (the example's mariadb Component declares root_password.required)
// belong in the end-to-end proof matrix, not here.
func TestExampleMariaDBAdminerComposeRecipe_WorkloadSynthesis(t *testing.T) {
	t.Parallel()

	const exampleCatalog = "../../../aggrega-docs/language-reference/examples/mariadb-adminer/catalog-compose"

	cat, err := catalog.LoadAll("",
		exampleCatalog+"/component/mariadb.yaml",
		exampleCatalog+"/recipe/compose/mariadb/1.0.0/recipe.yaml",
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
	valuesMapping.Set("db", json.RawMessage(`{"database":"appdb","user":"appuser","charset":"utf8mb4","collation":"utf8mb4_general_ci"}`))

	infraMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping.Set("db", json.RawMessage(`{"port":3306,"engine_version":"11.4","compute":{"cpu":"250m","memory":"512Mi"},"storage":{"size":"10Gi","type":"gp3"},"ha":{"enabled":false,"read_replicas":0},"backup":{"enabled":true,"retention_days":7}}`))

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "mariadb-adminer-example-test"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: valuesMapping,
				InfraMapping:  infraMapping,
				Provenance:    types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "mariadb-adminer-example-test"}},
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

	// Image: defaults from infra.engine_version unless overrides.image supplies
	// a different reference.
	if !strings.Contains(merged, "mariadb:11.4") {
		t.Errorf("expected image 'mariadb:11.4' (from infra.engine_version) in rendered compose:\n%s", merged)
	}

	// Environment: MARIADB_DATABASE from values.database, MARIADB_USER from the
	// optional values.user, charset and collation from the values defaults.
	for _, want := range []string{
		"MARIADB_DATABASE: appdb",
		"MARIADB_USER: appuser",
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
	if !strings.Contains(merged, "db--data") {
		t.Errorf("expected named volume 'db--data' (workload volume 'data' on node 'db') in rendered compose:\n%s", merged)
	}

	// Healthcheck: the Recipe's container health block reaches the rendered
	// service. The exec probe becomes a CMD-form test running the image's own
	// healthcheck.sh; interval carries periodSeconds, timeout carries
	// timeoutSeconds, retries carries failureThreshold and start_period carries
	// initialDelaySeconds. Asserted as one exact block rather than as scattered
	// substrings, because the shape and the indentation are both part of what a
	// compose runtime consumes.
	const wantHealthcheck = "    healthcheck:\n" +
		"      test:\n" +
		"        - CMD\n" +
		"        - healthcheck.sh\n" +
		"        - --connect\n" +
		"        - --innodb_initialized\n" +
		"      interval: 10s\n" +
		"      timeout: 5s\n" +
		"      retries: 3\n" +
		"      start_period: 10s\n"
	if !strings.Contains(merged, wantHealthcheck) {
		t.Errorf("expected healthcheck block\n%s\nin rendered compose:\n%s", wantHealthcheck, merged)
	}

	// Compose carries a single healthcheck per service, and the Recipe declares
	// one probe for it, so the rendered service carries exactly one.
	if n := strings.Count(merged, "healthcheck:\n"); n != 1 {
		t.Errorf("expected exactly one healthcheck in rendered compose (compose carries one per service); got %d:\n%s", n, merged)
	}
}
