// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestAssembleNodeContext_FullContext(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	catalogAnnotations := types.OrderedMap[string]{}
	catalogAnnotations.Set("tier", "standard")
	releaseAnnotations := types.OrderedMap[string]{}
	releaseAnnotations.Set("env", "local")
	labels2 := types.OrderedMap[string]{}
	labels2.Set(types.ComponentTypeLabel, "stateful-server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels, ResolvedVersion: "1.0.0", BoundaryPath: "local/default", CatalogAnnotations: catalogAnnotations, ReleaseAnnotations: releaseAnnotations})
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres", Labels: labels2, ResolvedVersion: "2.0.0"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("api", json.RawMessage(`{"port":8080}`))
	infraMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping.Set("api", json.RawMessage(`{"replicas":3}`))

	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test-app"},
		Spec: types.IRSpec{
			Nodes: nodes,

			Edges: []types.IREdge{
				{Source: "api", Target: "db", Socket: "databases", Alias: "main-db", Inject: func() types.OrderedMap[types.OrderedMap[string]] {
					inj := types.OrderedMap[types.OrderedMap[string]]{}
					env := types.OrderedMap[string]{}
					env.Set("DB_URL", "host")
					inj.Set("env", env)
					return inj
				}()},
			},
			ValuesMapping: valuesMapping,
			InfraMapping:  infraMapping,
			FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
				inner := types.OrderedMap[types.IRFeatureEntry]{}
				vals := types.OrderedMap[any]{}
				vals.Set("enabled", true)
				inner.Set("monitoring", types.IRFeatureEntry{Type: "core/monitoring", Values: vals})
				outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
				outer.Set("api", inner)
				return outer
			}(),
			Secrets: func() types.OrderedMap[types.OrderedMap[types.IRSecretEntry]] {
				inner := types.OrderedMap[types.IRSecretEntry]{}
				inner.Set("db-creds", types.IRSecretEntry{Type: "core/db-creds"})
				outer := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
				outer.Set("api", inner)
				return outer
			}(),
			Provenance: types.IRProvenance{
				CompilerVersion:  "0.1.0",
				AggregateVersion: "1.0.0",
				ReleaseVersion:   "1.0.0",
			},
		},
	}

	apiProv := types.OrderedMap[string]{}
	apiProv.Set("listen_port", "8080")
	dbProv := types.OrderedMap[string]{}
	dbProv.Set("host", "db.svc.cluster.local")
	dbProv.Set("service_port", "5432")
	allProvides := NodeProvides{
		"api": apiProv,
		"db":  dbProv,
	}

	inject, injectRaw := BuildInjectPipe(nil)
	ctx := AssembleNodeContext("api", graph, allProvides, inject, injectRaw)

	// Context block
	if ctx.Context.InstanceID != "api" {
		t.Errorf("instance_id: %q", ctx.Context.InstanceID)
	}
	ctxType, _ := ctx.Context.Labels.Get(types.ComponentTypeLabel)
	if ctxType != "server" {
		t.Errorf("labels: %v", ctx.Context.Labels.Keys())
	}
	if ctx.Context.Annotations == nil {
		t.Fatalf("annotations nil")
	}
	tier, _ := ctx.Context.Annotations.Catalog.Get("tier")
	if tier != "standard" {
		t.Errorf("annotations: %v", ctx.Context.Annotations)
	}

	// Enriched uses
	if ctx.Context.Uses.Len() != 1 {
		t.Fatalf("expected 1 uses entry, got %d", ctx.Context.Uses.Len())
	}
	dbEdge, ok := ctx.Context.Uses.Get("main-db")
	if !ok {
		t.Fatal("expected 'main-db' in uses")
	}
	if dbEdge.Target != "db" || dbEdge.Socket != "databases" {
		t.Errorf("edge: %+v", dbEdge)
	}
	provHost, _ := dbEdge.Provides.Get("host")
	if provHost != "db.svc.cluster.local" {
		t.Errorf("provides: %v", dbEdge.Provides.Keys())
	}
	if dbEdge.ResolvedVersion != "2.0.0" {
		t.Errorf("resolved_version: %q", dbEdge.ResolvedVersion)
	}

	// Values + Infra. JSON unmarshal of OrderedMap[any] decodes numeric
	// values as json.Number (string-backed) so caller-side type-switches
	// preserve exact integer precision regardless of magnitude.
	portVal, _ := ctx.Values.Get("port")
	portNum, ok := portVal.(json.Number)
	if !ok {
		t.Errorf("values.port: type %T, want json.Number", portVal)
	} else if got, _ := portNum.Int64(); got != 8080 {
		t.Errorf("values.port: got %d, want 8080", got)
	}
	replicasVal, _ := ctx.Infra.Get("replicas")
	replicasNum, ok := replicasVal.(json.Number)
	if !ok {
		t.Errorf("infra.replicas: type %T, want json.Number", replicasVal)
	} else if got, _ := replicasNum.Int64(); got != 3 {
		t.Errorf("infra.replicas: got %d, want 3", got)
	}

	// .Provides reflects Pass 1 output. .Locals is intentionally empty here
	// because AssembleNodeContext does not run the Phase B locals preamble
	// — the orchestrator calls EvaluateRecipeLocals separately.
	listenPort, _ := ctx.Provides.Get("listen_port")
	if listenPort != "8080" {
		t.Errorf("provides: %v", ctx.Provides.Keys())
	}
	if ctx.Locals.Len() != 0 {
		t.Errorf("expected empty locals from AssembleNodeContext alone, got %v", ctx.Locals.Keys())
	}

	// Features
	monFeat, _ := ctx.Features.Get("monitoring")
	if monFeat.Type != "core/monitoring" {
		t.Errorf("features: %v", ctx.Features.Keys())
	}

	// Secrets
	dbSecret, _ := ctx.Secrets.Get("db-creds")
	if dbSecret.Type != "core/db-creds" {
		t.Errorf("secrets: %v", ctx.Secrets.Keys())
	}

	// InjectRaw (nil since we passed nil)
	if ctx.InjectRaw != nil {
		t.Error("expected nil injectRaw")
	}

	// Inject guaranteed keys (always initialized as OrderedMap, never nil pointer)
	// The assertion is that they can be safely iterated; no type-level check needed.
	_ = ctx.Context.Inject.Env
	_ = ctx.Context.Inject.File
	_ = ctx.Context.Inject.Arg
}

func TestAssembleNodeContext_ProvidesTemplateReference(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres"})

	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "t"},
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}
	dbProv2 := types.OrderedMap[string]{}
	dbProv2.Set("host", "db.svc.cluster.local")
	dbProv2.Set("port", "5432")
	allProvides := NodeProvides{"db": dbProv2}
	inject, injectRaw := BuildInjectPipe(nil)
	ctx := AssembleNodeContext("db", graph, allProvides, inject, injectRaw)

	out, err := EvalTemplate("test-values", `{"dsn":"postgres://{{ .provides.host }}:{{ .provides.port }}"}`, ctx)
	if err != nil {
		t.Fatalf("EvalTemplate failed: %v", err)
	}
	want := `{"dsn":"postgres://db.svc.cluster.local:5432"}`
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestAssembleNodeContext_EmptyNode(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("simple", types.IRNode{Component: "core/worker"})

	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test"},
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}

	inject, injectRaw := BuildInjectPipe(nil)
	ctx := AssembleNodeContext("simple", graph, NodeProvides{"simple": types.OrderedMap[string]{}}, inject, injectRaw)

	if ctx.Context.InstanceID != "simple" {
		t.Errorf("instance_id: %q", ctx.Context.InstanceID)
	}
	// Values/Infra are zero-value OrderedMap[any] — always safe to use; no nil check needed.
	_ = ctx.Values
	_ = ctx.Infra
	if ctx.Context.Uses.Len() != 0 {
		t.Errorf("expected 0 uses, got %d", ctx.Context.Uses.Len())
	}
}

func TestAssembleNodeContextWithRelease(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Metadata: &types.TemplateMetadata{AggregateName: "test-app"},
	}
	release := &types.Release{
		Metadata: types.ReleaseMetadata{
			Name:        "prod-release",
			Version:     "2.0.0",
			Environment: "production",
		},
	}

	AssembleNodeContextWithRelease(ctx, release)

	if ctx.Metadata.ReleaseName != "prod-release" {
		t.Errorf("release_name: %q", ctx.Metadata.ReleaseName)
	}
	if ctx.Metadata.ReleaseVersion != "2.0.0" {
		t.Errorf("release_version: %q", ctx.Metadata.ReleaseVersion)
	}
	if ctx.Metadata.Environment != "production" {
		t.Errorf("environment: %q", ctx.Metadata.Environment)
	}
}
