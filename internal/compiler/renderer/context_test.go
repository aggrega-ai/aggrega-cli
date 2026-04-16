// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestAssembleNodeContext_FullContext(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test-app"},
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"api": {
					Component:          "core/server/api-server",
					Labels:             map[string]string{types.ComponentTypeLabel: "server"},
					ResolvedVersion:    "1.0.0",
					BoundaryPath:       "local/default",
					CatalogAnnotations: map[string]string{"tier": "standard"},
					ReleaseAnnotations: map[string]string{"env": "local"},
				},
				"db": {
					Component:       "core/database/postgres",
					Labels:          map[string]string{types.ComponentTypeLabel: "stateful-server"},
					ResolvedVersion: "2.0.0",
				},
			},
			Edges: []types.IREdge{
				{Source: "api", Target: "db", Socket: "databases", Alias: "main-db", Inject: map[string]map[string]string{"env": {"DB_URL": "host"}}},
			},
			ValuesMapping: map[string]json.RawMessage{
				"api": json.RawMessage(`{"port":8080}`),
			},
			InfraMapping: map[string]json.RawMessage{
				"api": json.RawMessage(`{"replicas":3}`),
			},
			FeatureValuesMapping: map[string]map[string]types.IRFeatureEntry{
				"api": {"monitoring": {Type: "core/monitoring", Values: map[string]interface{}{"enabled": true}}},
			},
			Secrets: map[string]map[string]types.IRSecretEntry{
				"api": {"db-creds": {Type: "core/db-creds"}},
			},
			Provenance: types.IRProvenance{
				CompilerVersion:  "0.1.0",
				AggregateVersion: "1.0.0",
				ReleaseVersion:   "1.0.0",
			},
		},
	}

	allProvides := NodeProvides{
		"api": {"listen_port": "8080"},
		"db":  {"host": "db.svc.cluster.local", "service_port": "5432"},
	}

	inject, injectRaw := BuildInjectPipe(nil)
	ctx := AssembleNodeContext("api", graph, allProvides, inject, injectRaw)

	// Context block
	if ctx.Context.InstanceID != "api" {
		t.Errorf("instance_id: %q", ctx.Context.InstanceID)
	}
	if ctx.Context.Labels[types.ComponentTypeLabel] != "server" {
		t.Errorf("labels: %v", ctx.Context.Labels)
	}
	if ctx.Context.Annotations == nil || ctx.Context.Annotations.Catalog["tier"] != "standard" {
		t.Errorf("annotations: %v", ctx.Context.Annotations)
	}

	// Enriched uses
	if len(ctx.Context.Uses) != 1 {
		t.Fatalf("expected 1 uses entry, got %d", len(ctx.Context.Uses))
	}
	dbEdge, ok := ctx.Context.Uses["main-db"]
	if !ok {
		t.Fatal("expected 'main-db' in uses")
	}
	if dbEdge.Target != "db" || dbEdge.Socket != "databases" {
		t.Errorf("edge: %+v", dbEdge)
	}
	if dbEdge.Provides["host"] != "db.svc.cluster.local" {
		t.Errorf("provides: %v", dbEdge.Provides)
	}
	if dbEdge.ResolvedVersion != "2.0.0" {
		t.Errorf("resolved_version: %q", dbEdge.ResolvedVersion)
	}

	// Values + Infra
	if ctx.Values["port"] != float64(8080) { // JSON unmarshal makes numbers float64
		t.Errorf("values.port: %v (%T)", ctx.Values["port"], ctx.Values["port"])
	}
	if ctx.Infra["replicas"] != float64(3) {
		t.Errorf("infra.replicas: %v", ctx.Infra["replicas"])
	}

	// Outputs (from provides)
	if ctx.Locals["listen_port"] != "8080" {
		t.Errorf("locals: %v", ctx.Locals)
	}

	// Features
	if ctx.Features["monitoring"].Type != "core/monitoring" {
		t.Errorf("features: %v", ctx.Features)
	}

	// Secrets
	if ctx.Secrets["db-creds"].Type != "core/db-creds" {
		t.Errorf("secrets: %v", ctx.Secrets)
	}

	// InjectRaw (nil since we passed nil)
	if ctx.InjectRaw != nil {
		t.Error("expected nil injectRaw")
	}

	// Inject guaranteed keys
	if ctx.Context.Inject.Env == nil || ctx.Context.Inject.File == nil || ctx.Context.Inject.Arg == nil {
		t.Error("inject pipe missing guaranteed keys")
	}
}

func TestAssembleNodeContext_EmptyNode(t *testing.T) {
	t.Parallel()

	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "test"},
		Spec: types.IRSpec{
			Nodes: map[string]types.IRNode{
				"simple": {Component: "core/worker"},
			},
		},
	}

	inject, injectRaw := BuildInjectPipe(nil)
	ctx := AssembleNodeContext("simple", graph, NodeProvides{"simple": {}}, inject, injectRaw)

	if ctx.Context.InstanceID != "simple" {
		t.Errorf("instance_id: %q", ctx.Context.InstanceID)
	}
	if ctx.Values == nil {
		t.Error("values should be non-nil empty map")
	}
	if ctx.Infra == nil {
		t.Error("infra should be non-nil empty map")
	}
	if len(ctx.Context.Uses) != 0 {
		t.Errorf("expected 0 uses, got %d", len(ctx.Context.Uses))
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
