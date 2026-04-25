// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestFlattenAggregate_SimpleComponents(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server"})
	components.Set("worker", types.ComponentInstance{Type: "core/worker"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	result, err := flattenAggregate(agg, "", "", nil, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Nodes.Len() != 2 {
		t.Fatalf("expected 2 nodes, got %d", result.Nodes.Len())
	}
	apiNode, hasAPI := result.Nodes.Get("api")
	workerNode, hasWorker := result.Nodes.Get("worker")
	if !hasAPI || apiNode == nil || !hasWorker || workerNode == nil {
		t.Error("expected api and worker nodes")
	}
	if apiNode.ComponentType != "core/server/api-server" {
		t.Errorf("api type: %q", apiNode.ComponentType)
	}
}

func TestFlattenAggregate_SubsystemComponents(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("main-db", types.ComponentInstance{Type: "core/database/postgres"})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("data-layer", types.Subsystem{Components: components})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
		},
	}

	result, err := flattenAggregate(agg, "", "", nil, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mainDB, ok := result.Nodes.Get("main-db")
	if !ok || mainDB == nil {
		t.Fatal("expected main-db node")
	}
	if mainDB.SubsystemPath != "data-layer" {
		t.Errorf("subsystem path: %q", mainDB.SubsystemPath)
	}
}

func TestFlattenAggregate_NestedSubsystems(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("deep-db", types.ComponentInstance{Type: "core/database/postgres"})
	subsystems2 := types.OrderedMap[types.Subsystem]{}
	subsystems2.Set("inner", types.Subsystem{Components: components})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("outer", types.Subsystem{Subsystems: subsystems2})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
		},
	}

	result, err := flattenAggregate(agg, "", "", nil, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deepDB, ok := result.Nodes.Get("deep-db")
	if !ok || deepDB == nil {
		t.Fatal("expected deep-db node")
	}
	if deepDB.SubsystemPath != "outer.inner" {
		t.Errorf("subsystem path: %q, want %q", deepDB.SubsystemPath, "outer.inner")
	}
}

func TestFlattenAggregate_SubAggregate(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("child-db", types.ComponentInstance{Type: "core/database/postgres"})
	exportedComponents := types.OrderedMap[string]{}
	exportedComponents.Set("db", "child-db")

	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "child-module"},
		Spec: types.AggregateSpec{
			Components:         components,
			ExportedComponents: exportedComponents,
		},
	}
	components2 := types.OrderedMap[types.ComponentInstance]{}
	components2.Set("api", types.ComponentInstance{Type: "core/server/api-server"})
	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("data-module", types.SubAggregate{Type: "modules/data-layer"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: components2,
			Aggregates: aggregates,
		},
	}

	loader := func(typeName string) (*types.Aggregate, error) {
		if typeName == "modules/data-layer" {
			return childAgg, nil
		}
		return nil, fmt.Errorf("unknown: %s", typeName)
	}

	result, err := flattenAggregate(agg, "", "", loader, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Nodes.Len() != 2 {
		t.Fatalf("expected 2 nodes, got %d", result.Nodes.Len())
	}
	childDB, ok := result.Nodes.Get("data-module.child-db")
	if !ok || childDB == nil {
		t.Fatal("expected data-module.child-db node with dot-notation ID")
	}
	if result.ExportedComponents["data-module.db"] != "data-module.child-db" {
		t.Errorf("exported: %v", result.ExportedComponents)
	}
}

func TestFlattenAggregate_CircularSubAggregate(t *testing.T) {
	t.Parallel()

	loader := func(typeName string) (*types.Aggregate, error) {
		aggregates := types.OrderedMap[types.SubAggregate]{}
		aggregates.Set("self", types.SubAggregate{Type: "circular-module"})
		return &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "circular"},
			Spec: types.AggregateSpec{
				Aggregates: aggregates,
			},
		}, nil
	}
	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("loop", types.SubAggregate{Type: "circular-module"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Aggregates: aggregates,
		},
	}

	_, err := flattenAggregate(agg, "", "", loader, map[string]bool{}, 0)
	if err == nil {
		t.Fatal("expected error for circular sub-aggregate")
	}
	if !errors.Is(err, ErrSubAggregateLoad) {
		t.Errorf("expected ErrSubAggregateLoad, got %v", err)
	}
}

func TestFlattenAggregate_MaxDepth(t *testing.T) {
	t.Parallel()

	_, err := flattenAggregate(&types.Aggregate{}, "", "", nil, map[string]bool{}, maxSubAggregateDepth+1)
	if err == nil {
		t.Fatal("expected error for max depth")
	}
	if !errors.Is(err, ErrMaxDepth) {
		t.Errorf("expected ErrMaxDepth, got %v", err)
	}
}

func TestFlattenAggregate_FeatureAttachments(t *testing.T) {
	t.Parallel()
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("monitoring", types.FeatureInstance{Type: "core/monitoring"})
	secrets := types.OrderedMap[types.SecretInstance]{}
	secrets.Set("db-creds", types.SecretInstance{Type: "core/db-credentials"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api-server", Features: features, Secrets: secrets})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	result, err := flattenAggregate(agg, "", "", nil, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FeatureAttachments["api"]) != 1 {
		t.Errorf("expected 1 feature attachment, got %d", len(result.FeatureAttachments["api"]))
	}
	if len(result.SecretAttachments["api"]) != 1 {
		t.Errorf("expected 1 secret attachment, got %d", len(result.SecretAttachments["api"]))
	}
	if result.FeatureAttachments["api"][0].InstanceName != "monitoring" {
		t.Errorf("feature name: %q", result.FeatureAttachments["api"][0].InstanceName)
	}
}
