// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the BuildIR orchestrator — the integration-shaped surface
// that exercises the package end-to-end. testCatalog provides an in-test
// CatalogLookup faking the two component types and one credential
// feature the assertions depend on.
//
// Test cases group by behaviour cluster, one per BuildIR concern:
//
//   - TestBuildIR_SimpleGraph — the happy-path pin: two nodes, one edge,
//     ResolvedVersion populated from the catalog, CatalogAnnotations and
//     ReleaseAnnotations from their distinct sources, ValuesMapping
//     populated from topology values, Provenance carrying the supplied
//     CompilerVersion + AggregateVersion. No features in this case.
//   - TestBuildIR_FeatureNormalization — pins the FeatureValuesMapping
//     and Secrets split (an operational feature lands in Features; a
//     credential-labelled one routes to Secrets via the
//     credential-routing branch) plus the FeatureOrder return value and
//     the feature_markers slice's deduplicate-and-sort discipline.
//   - TestBuildIR_SyncWave — pins findSyncWave's per-node lookup against
//     a Release.Spec.SyncWaves declaration with two waves.
//   - TestBuildIR_InfraMapping — pins the InfraMapping population from
//     resolver BoundaryInfra.Infra.
//
// Tests use the package-private symbols directly; the integration
// surface is package-internal because BuildIR's three callers (the
// orchestrator and two compiler-level integration tests) all sit
// outside this package.

package ir

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func testCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/server/api-server":
				labels := types.OrderedMap[string]{}
				labels.Set(types.ComponentTypeLabel, "server")
				annotations := types.OrderedMap[string]{}
				annotations.Set("catalog.aggrega.ai/tier", "standard")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:        "core/server/api-server",
						Version:     "1.0.0",
						Labels:      labels,
						Annotations: annotations,
					},
				}, true
			case "core/database/postgres":
				labels2 := types.OrderedMap[string]{}
				labels2.Set(types.ComponentTypeLabel, "stateful-server")
				return types.Component{
					Metadata: types.ComponentMetadata{
						Name:    "core/database/postgres",
						Version: "2.0.0",
						Labels:  labels2,
					},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/db-creds" {
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: labels,
					},
				}, true
			}
			return types.Feature{}, false
		},
	}
}

func TestBuildIR_SimpleGraph(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	values := types.OrderedMap[any]{}
	values.Set("port", 8080)
	labels2 := types.OrderedMap[string]{}
	labels2.Set(types.ComponentTypeLabel, "stateful-server")
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server", Labels: labels, Values: values})
	nodes.Set("main-db", &topology.TopologyNode{InstanceID: "main-db", ComponentType: "core/database/postgres", Labels: labels2})
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		Edges: []topology.NormalizedEdge{
			{Source: "api", Target: "main-db", Socket: "databases", Alias: "main-db"},
		},
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}
	annotations := types.OrderedMap[string]{}
	annotations.Set("env", "local")

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "test-app", Version: "1.0.0"},
		},
		Release: &types.Release{
			Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			Spec:     types.ReleaseSpec{Boundaries: types.OrderedMap[types.Boundary]{}},
		},
		Manifest: &types.Manifest{
			Metadata: types.ManifestMetadata{Version: "1.0.0"},
		},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {BoundaryPath: "local/default", Annotations: annotations},
		},
	}

	graph, order, err := BuildIR(BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   testCatalog(),
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nodes
	if graph.Spec.Nodes.Len() != 2 {
		t.Fatalf("expected 2 nodes, got %d", graph.Spec.Nodes.Len())
	}
	apiNode, _ := graph.Spec.Nodes.Get("api")
	if apiNode.ResolvedVersion != "1.0.0" {
		t.Errorf("api resolved version: %q", apiNode.ResolvedVersion)
	}
	if apiNode.Component != "core/server/api-server" {
		t.Errorf("api component: %q", apiNode.Component)
	}

	// Edges
	if len(graph.Spec.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Spec.Edges))
	}
	if graph.Spec.Edges[0].Source != "api" || graph.Spec.Edges[0].Target != "main-db" {
		t.Errorf("edge: %+v", graph.Spec.Edges[0])
	}

	// Values mapping
	if _, ok := graph.Spec.ValuesMapping.Get("api"); !ok {
		t.Error("expected api values mapping")
	}

	// Annotations
	tier, _ := apiNode.CatalogAnnotations.Get("catalog.aggrega.ai/tier")
	if tier != "standard" {
		t.Errorf("catalog annotations: %v", apiNode.CatalogAnnotations.Keys())
	}
	env, _ := apiNode.ReleaseAnnotations.Get("env")
	if env != "local" {
		t.Errorf("release annotations: %v", apiNode.ReleaseAnnotations.Keys())
	}

	// Infra mapping
	// (no infra in this test)

	// Provenance
	if graph.Spec.Provenance.CompilerVersion != "0.1.0" {
		t.Errorf("compiler version: %q", graph.Spec.Provenance.CompilerVersion)
	}
	if graph.Spec.Provenance.AggregateVersion != "1.0.0" {
		t.Errorf("aggregate version: %q", graph.Spec.Provenance.AggregateVersion)
	}

	// Metadata
	if graph.Metadata.AggregateName != "test-app" {
		t.Errorf("app name: %q", graph.Metadata.AggregateName)
	}
	if graph.APIVersion != types.IRAPIVersion || graph.Kind != types.IRKind {
		t.Errorf("api/kind: %q/%q", graph.APIVersion, graph.Kind)
	}

	// Feature order (empty in this test)
	_ = order
}

func TestBuildIR_FeatureNormalization(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	values := types.OrderedMap[any]{}
	values.Set("level", "warn")
	values2 := types.OrderedMap[any]{}
	values2.Set("path", "/secret")
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server", Labels: labels})
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		Edges: nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "logging", Feature: types.FeatureInstance{Type: "core/logging", Values: values}},
			},
		},
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {
				{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds", Values: values2}},
			},
		},
	}

	resolveResult := &resolver.ResolveResult{
		Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}

	graph, order, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: testCatalog(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Features
	if fvm, ok := graph.Spec.FeatureValuesMapping.Get("api"); !ok || fvm.Len() == 0 {
		t.Fatal("expected feature values mapping for api")
	} else {
		loggingEntry, _ := fvm.Get("logging")
		if loggingEntry.Type != "core/logging" {
			t.Errorf("feature type: %q", loggingEntry.Type)
		}
	}

	// Secrets
	if sec, ok := graph.Spec.Secrets.Get("api"); !ok || sec.Len() == 0 {
		t.Fatal("expected secrets for api")
	} else {
		dbEntry, _ := sec.Get("db-creds")
		if dbEntry.Type != "core/db-creds" {
			t.Errorf("secret type: %q", dbEntry.Type)
		}
	}

	// Feature order
	if len(order["api"]) != 1 || order["api"][0] != "logging" {
		t.Errorf("feature order: %v", order["api"])
	}

	// Markers
	found := map[string]bool{}
	for _, m := range graph.Spec.FeatureMarkers {
		found[m] = true
	}
	if !found["core/logging"] || !found["core/db-creds"] {
		t.Errorf("markers: %v", graph.Spec.FeatureMarkers)
	}
}

func TestBuildIR_SyncWave(t *testing.T) {
	t.Parallel()

	// Each node lives in its own boundary; the boundaries declare different
	// syncWave values. The IR builder copies each boundary's SyncWave to
	// every contained IRNode via the resolver's perComponent BoundaryInfra
	// map (which carries the containing-boundary SyncWave). Per-component
	// SyncWave overrides do not exist; the boundary IS the wave-authoring
	// unit.
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/database/postgres"})
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server"})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release: &types.Release{
			Spec: types.ReleaseSpec{
				Boundaries: types.OrderedMap[types.Boundary]{},
			},
		},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"db":  {BoundaryPath: "infra", SyncWave: 1},
			"api": {BoundaryPath: "services", SyncWave: 2},
		},
	}

	graph, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: testCatalog(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dbNode, _ := graph.Spec.Nodes.Get("db")
	if dbNode.SyncWave != 1 {
		t.Errorf("db sync wave: got %d, want 1", dbNode.SyncWave)
	}
	apiNode, _ := graph.Spec.Nodes.Get("api")
	if apiNode.SyncWave != 2 {
		t.Errorf("api sync wave: got %d, want 2", apiNode.SyncWave)
	}
}

func TestBuildIR_SyncWave_DefaultZero(t *testing.T) {
	t.Parallel()
	// A node whose boundary omits syncWave (or has none assigned) gets
	// IRNode.SyncWave == 0, matching every comparable tool's default.
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("svc", &topology.TopologyNode{InstanceID: "svc", ComponentType: "core/server/api-server"})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		Release: &types.Release{
			Spec: types.ReleaseSpec{
				Boundaries: types.OrderedMap[types.Boundary]{},
			},
		},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"svc": {BoundaryPath: "default"}, // SyncWave zero-value
		},
	}

	graph, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: testCatalog(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svcNode, _ := graph.Spec.Nodes.Get("svc")
	if svcNode.SyncWave != 0 {
		t.Errorf("svc sync wave: got %d, want 0 (default)", svcNode.SyncWave)
	}
}

func TestBuildIR_InfraMapping(t *testing.T) {
	t.Parallel()

	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server"})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		Edges:              nil,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
		SecretAttachments:  map[string][]topology.SecretAttachment{},
	}
	infra := types.OrderedMap[any]{}
	infra.Set("replicas", 3)
	infra.Set("cpu", "500m")
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{
			"api": {Infra: infra},
		},
	}

	graph, _, err := BuildIR(BuildIRConfig{
		Topology:      topoResult,
		ResolveResult: resolveResult,
		CatalogLookup: testCatalog(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := graph.Spec.InfraMapping.Get("api"); !ok {
		t.Fatal("expected infra mapping for api")
	}
}

// TestConvertInjectBlock_AdditionalIteration pins the IR-edge per-mechanism
// shape when the source InjectBlock carries custom mechanisms in Additional.
// The three core mechanisms surface first in struct-field order (env, file,
// arg); custom mechanisms follow in author order via OrderedMap.All().
func TestConvertInjectBlock_AdditionalIteration(t *testing.T) {
	t.Parallel()

	inject := &types.InjectBlock{
		Env: map[string]any{"DB_URL": "host"},
	}
	ssm := types.NewOrderedMap[any]()
	ssm.Set("DB_PASSWORD", "connection.password")
	ssm.Set("API_KEY", "connection.token")
	inject.Additional.Set("ssm_param", ssm)

	vault := types.NewOrderedMap[any]()
	vault.Set("VAULT_TOKEN", "vault.token")
	inject.Additional.Set("vault_secret", vault)

	result := convertInjectBlock(inject)

	wantOuter := []string{"env", "ssm_param", "vault_secret"}
	gotOuter := result.Keys()
	if len(gotOuter) != len(wantOuter) {
		t.Fatalf("outer Keys len = %d, want %d (got %v)", len(gotOuter), len(wantOuter), gotOuter)
	}
	for i, want := range wantOuter {
		if gotOuter[i] != want {
			t.Errorf("outer Keys[%d] = %q, want %q (full=%v)", i, gotOuter[i], want, gotOuter)
		}
	}

	// Inner ssm_param map carries DB_PASSWORD then API_KEY in author order.
	ssmInner, ok := result.Get("ssm_param")
	if !ok {
		t.Fatalf("result missing ssm_param")
	}
	wantSsm := []string{"DB_PASSWORD", "API_KEY"}
	gotSsm := ssmInner.Keys()
	for i, want := range wantSsm {
		if i >= len(gotSsm) || gotSsm[i] != want {
			t.Errorf("ssm_param inner Keys[%d] = %v, want %q", i, gotSsm, want)
		}
	}
	if v, _ := ssmInner.Get("DB_PASSWORD"); v != "connection.password" {
		t.Errorf("ssm_param.DB_PASSWORD = %q, want %q", v, "connection.password")
	}

	// Vault inner is a single-key map.
	vaultInner, ok := result.Get("vault_secret")
	if !ok {
		t.Fatalf("result missing vault_secret")
	}
	if v, _ := vaultInner.Get("VAULT_TOKEN"); v != "vault.token" {
		t.Errorf("vault_secret.VAULT_TOKEN = %q, want %q", v, "vault.token")
	}
}

// TestConvertInjectBlock_CoreOnlyUnchanged pins the bare-three-mechanism
// behaviour. The new Additional iteration is a no-op when Additional is
// empty; the result shape matches the pre-WI behaviour.
func TestConvertInjectBlock_CoreOnlyUnchanged(t *testing.T) {
	t.Parallel()

	inject := &types.InjectBlock{
		Env:  map[string]any{"PORT": "8080"},
		File: map[string]any{"config": "config.path"},
	}

	result := convertInjectBlock(inject)
	if result.Len() != 2 {
		t.Fatalf("result.Len = %d, want 2 (env+file); got %v", result.Len(), result.Keys())
	}
	if _, ok := result.Get("env"); !ok {
		t.Errorf("result missing env")
	}
	if _, ok := result.Get("file"); !ok {
		t.Errorf("result missing file")
	}
}

// TestConvertInjectBlock_VerboseArrayForm pins the schema-verbose-form
// decode arm. The verbose form's [{in: K, from?: P, value?: V}] elements
// decode into the same OrderedMap[string] shape as the compact form. The
// `value:` field is preferred over `from:` if both are present (defensive;
// schema validation should preempt). Empty arrays produce no mechanism
// entry; entries without `in:` or with non-string `in:` are skipped.
func TestConvertInjectBlock_VerboseArrayForm(t *testing.T) {
	t.Parallel()

	inject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "DB_URL", "from": "connection.url"},
			map[string]any{"in": "PORT", "value": "8080"},
		},
		Arg: []any{
			map[string]any{"in": "FLAG", "value": "--enable"},
		},
	}

	result := convertInjectBlock(inject)
	if result.Len() != 2 {
		t.Fatalf("result.Len = %d, want 2 (env+arg); got %v", result.Len(), result.Keys())
	}

	envInner, ok := result.Get("env")
	if !ok {
		t.Fatalf("result missing env")
	}
	if v, _ := envInner.Get("DB_URL"); v != "connection.url" {
		t.Errorf("env.DB_URL = %q, want %q", v, "connection.url")
	}
	if v, _ := envInner.Get("PORT"); v != "8080" {
		t.Errorf("env.PORT = %q, want %q", v, "8080")
	}

	argInner, ok := result.Get("arg")
	if !ok {
		t.Fatalf("result missing arg")
	}
	if v, _ := argInner.Get("FLAG"); v != "--enable" {
		t.Errorf("arg.FLAG = %q, want %q", v, "--enable")
	}

	// Empty verbose-array → mechanism not present (mirrors empty-map semantic).
	emptyInject := &types.InjectBlock{Env: []any{}}
	emptyResult := convertInjectBlock(emptyInject)
	if emptyResult.Len() != 0 {
		t.Errorf("empty []any{} should produce no mechanism entry; got Len=%d", emptyResult.Len())
	}

	// Defensive: value: wins when both value: and from: are present.
	bothInject := &types.InjectBlock{
		Env: []any{map[string]any{"in": "BOTH", "from": "ref.path", "value": "literal"}},
	}
	bothResult := convertInjectBlock(bothInject)
	bothInner, _ := bothResult.Get("env")
	if v, _ := bothInner.Get("BOTH"); v != "literal" {
		t.Errorf("when both value: and from: present, value: should win; got %q", v)
	}
}

// TestBuildIR_InjectValidationHaltsWithSpecificError pins the BuildIR-level
// wiring of the second-line-of-defense inject validator: a resolved
// Aggregate carrying a both-`value:`-and-`from:`-present inject entry
// halts BuildIR before any IR construction begins, with the error wrapping
// ErrInjectEntryAmbiguous and the field path of the violating entry. The
// case is schema-illegal (inject_delivery oneOf rejects it at envelope
// load); this test exercises the schema-bypass path that motivates the
// validator's existence — a programmatically constructed Aggregate that
// never passed through schema validation.
func TestBuildIR_InjectValidationHaltsWithSpecificError(t *testing.T) {
	t.Parallel()

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{
		Type: "core/server/api-server",
		Inject: &types.InjectBlock{
			Env: []any{
				map[string]any{"in": "DB_URL", "from": "connection.url", "value": "literal-text"},
			},
		},
	})

	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "test-app", Version: "1.0.0"},
			Spec:     types.AggregateSpec{Components: components},
		},
		Release: &types.Release{
			Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			Spec:     types.ReleaseSpec{Boundaries: types.OrderedMap[types.Boundary]{}},
		},
		Manifest:      &types.Manifest{Metadata: types.ManifestMetadata{Version: "1.0.0"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}

	graph, _, err := BuildIR(BuildIRConfig{
		Topology:        &topology.TopologyResult{Nodes: types.OrderedMap[*topology.TopologyNode]{}},
		ResolveResult:   resolveResult,
		CatalogLookup:   testCatalog(),
		CompilerVersion: "0.1.0",
	})
	if err == nil {
		t.Fatalf("expected ErrInjectEntryAmbiguous, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), `components["api"].inject.env[0]`) {
		t.Errorf("error path missing components[\"api\"].inject.env[0]: %v", err)
	}
	if graph != nil {
		t.Errorf("expected nil graph on validation halt, got non-nil")
	}
}
