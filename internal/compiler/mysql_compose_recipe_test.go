// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Per-Recipe integration test for the in-repo mysql compose Recipe.
//
// This test reads catalog entities from the aggrega-hub repository checked out
// beside this one (the paths leave this repository for aggrega-hub), so check
// out aggrega-hub beside this repository first.
//
// composeWasmFactory and composeServiceKeyCount are shared with the mariadb
// Recipe test in this same package rather than duplicated here, so both Recipes
// render through one definition of the embedded compose guest and are read by
// one definition of the service key count.

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

// TestMySQLComposeRecipe_WorkloadSynthesis exercises the compose mysql Recipe
// end-to-end at the renderer level: builds a one-node IR graph, runs RenderAll
// with the compose engine, and asserts the rendered docker-compose.yaml carries
// the mysql service body the Recipe's Workload describes — image, env vars,
// persistent volume mount, and the container healthcheck.
//
// IR-level direct construction because this test focuses on Recipe authoring
// correctness, not on the full Aggregate→Release→IR pipeline. Aggregate-level
// requires (the mysql Component declares root_password.required) belong in the
// end-to-end proof matrix, not here.
func TestMySQLComposeRecipe_WorkloadSynthesis(t *testing.T) {
	t.Parallel()

	cat, err := catalog.LoadAll("",
		"../../../aggrega-hub/aggrega-catalog/component/core/datastore",
		"../../../aggrega-hub/aggrega-catalog/recipe/compose/mysql",
		"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
	)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{
		Component:    "core/datastore/mysql:1.0.0",
		BoundaryPath: "default",
	})

	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("db", json.RawMessage(`{"database":"appdb","user":"appuser"}`))

	infraMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping.Set("db", json.RawMessage(`{"port":3306,"engine_version":"8.4","compute":{"cpu":"250m","memory":"512Mi"},"storage":{"size":"10Gi","type":"gp3"},"ha":{"enabled":false,"read_replicas":0},"backup":{"enabled":true,"retention_days":7}}`))

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "mysql-test"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: valuesMapping,
				InfraMapping:  infraMapping,
				Provenance:    types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "mysql-test"}},
			Release:   &types.Release{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				"db": {BoundaryPath: "default"},
			},
		},
	}

	composeFactory := composeWasmFactory(t)

	mt := &types.MappingTier{}
	mt.Type.Set("core/datastore/mysql:1.0.0", "mysql")
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
	if !strings.Contains(merged, "mysql:8.4") {
		t.Errorf("expected image 'mysql:8.4' (from infra.engine_version) in rendered compose:\n%s", merged)
	}

	// Environment: MYSQL_DATABASE from values.database, MYSQL_USER from the
	// optional values.user.
	for _, want := range []string{
		"MYSQL_DATABASE: appdb",
		"MYSQL_USER: appuser",
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("expected env entry %q in rendered compose:\n%s", want, merged)
		}
	}

	// Character set and collation are deliberately absent on this target: the
	// mysql container image takes them as server command-line arguments rather
	// than environment variables, so the Recipe leaves them at the image
	// defaults. Asserting their absence keeps that a stated property.
	for _, unwanted := range []string{
		"MYSQL_CHARACTER_SET_SERVER",
		"MYSQL_COLLATION_SERVER",
	} {
		if strings.Contains(merged, unwanted) {
			t.Errorf("expected no %q env entry (charset and collation stay at the image defaults on this target) in rendered compose:\n%s", unwanted, merged)
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

	// Healthcheck: the Recipe's container declares one probe, a readiness exec
	// probe running mysqladmin ping over TCP against the server's own port, and
	// it reaches the rendered service as its healthcheck. The exec probe becomes
	// a CMD-form test; interval carries periodSeconds, timeout carries
	// timeoutSeconds, and start_period carries initialDelaySeconds. The probe
	// declares no failureThreshold, so no retries line is rendered. Asserted as
	// one exact block rather than as scattered substrings, because the shape and
	// the indentation are both part of what a compose runtime consumes.
	const wantHealthcheck = "    healthcheck:\n" +
		"      test:\n" +
		"        - CMD\n" +
		"        - mysqladmin\n" +
		"        - ping\n" +
		"        - --host=127.0.0.1\n" +
		"        - --port=3306\n" +
		"      interval: 10s\n" +
		"      timeout: 5s\n" +
		"      start_period: 10s\n"
	if !strings.Contains(merged, wantHealthcheck) {
		t.Errorf("expected healthcheck block\n%s\nin rendered compose:\n%s", wantHealthcheck, merged)
	}

	// Compose carries one healthcheck per service and the Recipe declares one
	// probe, so the rendered service carries exactly one healthcheck key. It is
	// counted on the decoded service because the block match above would still
	// hold beside a second healthcheck key.
	if n := composeServiceKeyCount(t, merged, "db", "healthcheck"); n != 1 {
		t.Errorf("expected exactly one healthcheck on service %q (compose carries one per service and the Recipe declares one probe), got %d in rendered compose:\n%s", "db", n, merged)
	}
}
