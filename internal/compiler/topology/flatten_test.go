// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Unit tests for the flattenAggregate recursion and its companions.
// Each test calls flattenAggregate directly with hand-built Aggregate
// fixtures so the assertions can pin specific recursion behaviours
// without going through the full Build orchestrator.
//
// Behaviour clusters:
//
//   - TestFlattenAggregate_SimpleComponents — happy path: top-level
//     components flatten with un-prefixed IDs.
//   - TestFlattenAggregate_SubsystemComponents — subsystem-scoped
//     components carry SubsystemPath; the subsystem itself does not
//     prefix the InstanceID (subsystems contribute to subsystemPath,
//     not to ID composition).
//   - TestFlattenAggregate_NestedSubsystems — dotted SubsystemPath
//     accumulates correctly across nested subsystem levels.
//   - TestFlattenAggregate_SubAggregate — sub-aggregate import
//     produces dot-notation child IDs; ExportedComponents are rewired
//     to dotted IDs that resolve through the parent aggregate's
//     namespace.
//   - TestFlattenAggregate_CircularSubAggregate — pins the visited-set
//     cycle detection on the inclusion graph: a sub-aggregate that
//     transitively imports itself returns ErrSubAggregateLoad.
//   - TestFlattenAggregate_MaxDepth — pins the depth bound: invoking
//     flattenAggregate with depth above maxSubAggregateDepth returns
//     ErrMaxDepth.
//   - TestFlattenAggregate_FeatureAttachments — pins
//     collectAttachments: Feature and Secret attachments authored on
//     a component instance flow into the per-node attachment slices
//     in author order.

package topology

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestFlattenAggregate_SimpleComponents(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server"})
	components.Set("worker", types.ComponentInstance{Type: "core/worker"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	result, err := flattenAggregate(agg, "", "", "", nil, map[string]bool{}, 0, nil)
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
	if apiNode.ComponentType != "core/compute/api-server" {
		t.Errorf("api type: %q", apiNode.ComponentType)
	}
}

func TestFlattenAggregate_SubsystemComponents(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("main-db", types.ComponentInstance{Type: "core/datastore/postgres"})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("data-layer", types.Subsystem{Components: components})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
		},
	}

	result, err := flattenAggregate(agg, "", "", "", nil, map[string]bool{}, 0, nil)
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
	components.Set("deep-db", types.ComponentInstance{Type: "core/datastore/postgres"})
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

	result, err := flattenAggregate(agg, "", "", "", nil, map[string]bool{}, 0, nil)
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
	components.Set("child-db", types.ComponentInstance{Type: "core/datastore/postgres"})
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
	components2.Set("api", types.ComponentInstance{Type: "core/compute/api-server"})
	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("data-module", types.SubAggregate{Type: "modules/data-layer"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: components2,
			Aggregates: aggregates,
		},
	}

	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if typeName == "modules/data-layer" {
			return childAgg, "", nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
	}

	result, err := flattenAggregate(agg, "", "", "", loader, map[string]bool{}, 0, nil)
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

	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		aggregates := types.OrderedMap[types.SubAggregate]{}
		aggregates.Set("self", types.SubAggregate{Type: "circular-module"})
		return &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "circular"},
			Spec: types.AggregateSpec{
				Aggregates: aggregates,
			},
		}, "", nil, nil
	}
	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("loop", types.SubAggregate{Type: "circular-module"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Aggregates: aggregates,
		},
	}

	_, err := flattenAggregate(agg, "", "", "", loader, map[string]bool{}, 0, nil)
	if err == nil {
		t.Fatal("expected error for circular sub-aggregate")
	}
	if !errors.Is(err, ErrSubAggregateLoad) {
		t.Errorf("expected ErrSubAggregateLoad, got %v", err)
	}
}

func TestFlattenAggregate_MaxDepth(t *testing.T) {
	t.Parallel()

	_, err := flattenAggregate(&types.Aggregate{}, "", "", "", nil, map[string]bool{}, maxSubAggregateDepth+1, nil)
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
	features.Set("db-creds", types.FeatureInstance{Type: "core/db-credentials"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/compute/api-server", Features: features})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	result, err := flattenAggregate(agg, "", "", "", nil, map[string]bool{}, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FeatureAttachments["api"]) != 2 {
		t.Errorf("expected 2 attachments, got %d", len(result.FeatureAttachments["api"]))
	}
	if result.FeatureAttachments["api"][0].InstanceName != "monitoring" {
		t.Errorf("attachment name: %q", result.FeatureAttachments["api"][0].InstanceName)
	}
	if result.FeatureAttachments["api"][1].InstanceName != "db-creds" {
		t.Errorf("attachment name: %q", result.FeatureAttachments["api"][1].InstanceName)
	}
}
