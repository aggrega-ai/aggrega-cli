// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func featureCatalog() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/monitoring":
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: "core/monitoring", Version: "1.0.0"},
				}, true
			case "core/db-creds":
				labels := types.OrderedMap[string]{}
				labels.Set(types.FeatureKindLabel, types.FeatureKindSecret)
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: labels,
					},
				}, true
			default:
				return types.Feature{}, false
			}
		},
	}
}

func TestCollectNodeFeatures_SimpleComponent(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[interface{}]{}
	values.Set("enabled", true)
	apiNode := &topology.TopologyNode{InstanceID: "api", ComponentType: "core/server/api-server"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{Type: "core/monitoring", Values: values}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Features.Len() != 1 {
		t.Fatalf("expected 1 feature, got %d", result.Features.Len())
	}
	f, _ := result.Features.Get("monitoring")
	if f.Type != "core/monitoring" {
		t.Errorf("type: %q", f.Type)
	}
	enabled, _ := f.Values.Get("enabled")
	if enabled != true {
		t.Errorf("values: %v", f.Values.Keys())
	}
	if len(result.FeatureOrder) != 1 || result.FeatureOrder[0] != "monitoring" {
		t.Errorf("order: %v", result.FeatureOrder)
	}
}

func TestCollectNodeFeatures_CredentialToSecrets(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[interface{}]{}
	values.Set("vault_path", "/secret/db")
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {
				{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds", Values: values}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Features.Len() != 0 {
		t.Errorf("expected 0 features, got %d (credential should be in secrets)", result.Features.Len())
	}
	if result.Secrets.Len() != 1 {
		t.Fatalf("expected 1 secret, got %d", result.Secrets.Len())
	}
	s, _ := result.Secrets.Get("db-creds")
	if s.Type != "core/db-creds" {
		t.Errorf("type: %q", s.Type)
	}
}

func TestCollectNodeFeatures_TypeMismatchError(t *testing.T) {
	t.Parallel()

	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "feat", Feature: types.FeatureInstance{Type: "type-a"}},
				{InstanceName: "feat", Feature: types.FeatureInstance{Type: "type-b"}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	_, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
	if !errors.Is(err, ErrFeatureTypeMismatch) {
		t.Errorf("expected ErrFeatureTypeMismatch, got: %v", err)
	}
}

func TestCollectNodeFeatures_LastWinsMerge(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[interface{}]{}
	values.Set("level", "warn")
	values.Set("port", 9090)
	values2 := types.OrderedMap[interface{}]{}
	values2.Set("level", "info")
	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "mon", Feature: types.FeatureInstance{Type: "core/monitoring", Values: values}},
				{InstanceName: "mon", Feature: types.FeatureInstance{Type: "core/monitoring", Values: values2}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, _ := result.Features.Get("mon")
	level, _ := f.Values.Get("level")
	if level != "info" {
		t.Errorf("expected 'info' (last wins), got %v", level)
	}
	port, _ := f.Values.Get("port")
	if port != 9090 {
		t.Errorf("expected port 9090 preserved, got %v", port)
	}
}

func TestCollectNodeFeatures_MarkersDeduplicatedAndSorted(t *testing.T) {
	t.Parallel()

	apiNode := &topology.TopologyNode{InstanceID: "api"}
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", apiNode)
	topoResult := &topology.TopologyResult{
		Nodes: nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "z-feat", Feature: types.FeatureInstance{Type: "z-type"}},
				{InstanceName: "a-feat", Feature: types.FeatureInstance{Type: "a-type"}},
				{InstanceName: "z-feat2", Feature: types.FeatureInstance{Type: "z-type"}}, // duplicate type
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", apiNode, topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Markers) != 2 {
		t.Fatalf("expected 2 unique markers, got %d: %v", len(result.Markers), result.Markers)
	}
	if result.Markers[0] != "a-type" || result.Markers[1] != "z-type" {
		t.Errorf("markers not sorted/deduped: %v", result.Markers)
	}
}

func TestSplitPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"single", []string{"single"}},
		{"a/b/c", []string{"a", "b", "c"}},
		{"a.b.c", []string{"a", "b", "c"}},
		{"a/b.c", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		parts := splitPath(tt.input)
		if len(parts) != len(tt.want) {
			t.Errorf("splitPath(%q) = %v, want %v", tt.input, parts, tt.want)
			continue
		}
		for i, p := range parts {
			if p != tt.want[i] {
				t.Errorf("splitPath(%q)[%d] = %q, want %q", tt.input, i, p, tt.want[i])
			}
		}
	}
}
