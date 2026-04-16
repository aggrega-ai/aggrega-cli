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

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api":    {Type: "core/server/api-server"},
				"worker": {Type: "core/worker"},
			},
		},
	}

	result, err := flattenAggregate(agg, "", "", nil, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	if result.Nodes["api"] == nil || result.Nodes["worker"] == nil {
		t.Error("expected api and worker nodes")
	}
	if result.Nodes["api"].ComponentType != "core/server/api-server" {
		t.Errorf("api type: %q", result.Nodes["api"].ComponentType)
	}
}

func TestFlattenAggregate_SubsystemComponents(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Subsystems: map[string]types.Subsystem{
				"data-layer": {
					Components: map[string]types.ComponentInstance{
						"main-db": {Type: "core/database/postgres"},
					},
				},
			},
		},
	}

	result, err := flattenAggregate(agg, "", "", nil, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Nodes["main-db"] == nil {
		t.Fatal("expected main-db node")
	}
	if result.Nodes["main-db"].SubsystemPath != "data-layer" {
		t.Errorf("subsystem path: %q", result.Nodes["main-db"].SubsystemPath)
	}
}

func TestFlattenAggregate_NestedSubsystems(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Subsystems: map[string]types.Subsystem{
				"outer": {
					Subsystems: map[string]types.Subsystem{
						"inner": {
							Components: map[string]types.ComponentInstance{
								"deep-db": {Type: "core/database/postgres"},
							},
						},
					},
				},
			},
		},
	}

	result, err := flattenAggregate(agg, "", "", nil, map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Nodes["deep-db"] == nil {
		t.Fatal("expected deep-db node")
	}
	if result.Nodes["deep-db"].SubsystemPath != "outer.inner" {
		t.Errorf("subsystem path: %q, want %q", result.Nodes["deep-db"].SubsystemPath, "outer.inner")
	}
}

func TestFlattenAggregate_SubAggregate(t *testing.T) {
	t.Parallel()

	childAgg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "child-module"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"child-db": {Type: "core/database/postgres"},
			},
			ExportedComponents: map[string]string{
				"db": "child-db",
			},
		},
	}

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api-server"},
			},
			Aggregates: map[string]types.SubAggregate{
				"data-module": {Type: "modules/data-layer"},
			},
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
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	if result.Nodes["data-module.child-db"] == nil {
		t.Fatal("expected data-module.child-db node with dot-notation ID")
	}
	if result.ExportedComponents["data-module.db"] != "data-module.child-db" {
		t.Errorf("exported: %v", result.ExportedComponents)
	}
}

func TestFlattenAggregate_CircularSubAggregate(t *testing.T) {
	t.Parallel()

	loader := func(typeName string) (*types.Aggregate, error) {
		return &types.Aggregate{
			Metadata: types.AggregateMetadata{Name: "circular"},
			Spec: types.AggregateSpec{
				Aggregates: map[string]types.SubAggregate{
					"self": {Type: "circular-module"},
				},
			},
		}, nil
	}

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "root"},
		Spec: types.AggregateSpec{
			Aggregates: map[string]types.SubAggregate{
				"loop": {Type: "circular-module"},
			},
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

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api-server",
					Features: map[string]types.FeatureInstance{
						"monitoring": {Type: "core/monitoring"},
					},
					Secrets: map[string]types.SecretInstance{
						"db-creds": {Type: "core/db-credentials"},
					},
				},
			},
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
