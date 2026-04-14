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
				return types.Feature{
					Metadata: types.FeatureMetadata{
						Name:   "core/db-creds",
						Labels: map[string]string{types.FeatureKindLabel: types.FeatureKindSecret},
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

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api", ComponentType: "core/api-server"},
		},
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "monitoring", Feature: types.FeatureInstance{Type: "core/monitoring", Values: map[string]interface{}{"enabled": true}}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", topoResult.Nodes["api"], topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(result.Features))
	}
	f := result.Features["monitoring"]
	if f.Type != "core/monitoring" {
		t.Errorf("type: %q", f.Type)
	}
	if f.Values["enabled"] != true {
		t.Errorf("values: %v", f.Values)
	}
	if len(result.FeatureOrder) != 1 || result.FeatureOrder[0] != "monitoring" {
		t.Errorf("order: %v", result.FeatureOrder)
	}
}

func TestCollectNodeFeatures_CredentialToSecrets(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api"},
		},
		SecretAttachments: map[string][]topology.SecretAttachment{
			"api": {
				{InstanceName: "db-creds", Secret: types.SecretInstance{Type: "core/db-creds", Values: map[string]interface{}{"vault_path": "/secret/db"}}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", topoResult.Nodes["api"], topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Features) != 0 {
		t.Errorf("expected 0 features, got %d (credential should be in secrets)", len(result.Features))
	}
	if len(result.Secrets) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(result.Secrets))
	}
	s := result.Secrets["db-creds"]
	if s.Type != "core/db-creds" {
		t.Errorf("type: %q", s.Type)
	}
}

func TestCollectNodeFeatures_TypeMismatchError(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api"},
		},
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

	_, err := collectNodeFeatures("api", topoResult.Nodes["api"], topoResult, resolveResult, featureCatalog())
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
	if !errors.Is(err, ErrFeatureTypeMismatch) {
		t.Errorf("expected ErrFeatureTypeMismatch, got: %v", err)
	}
}

func TestCollectNodeFeatures_LastWinsMerge(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api"},
		},
		FeatureAttachments: map[string][]topology.FeatureAttachment{
			"api": {
				{InstanceName: "mon", Feature: types.FeatureInstance{Type: "core/monitoring", Values: map[string]interface{}{"level": "warn", "port": 9090}}},
				{InstanceName: "mon", Feature: types.FeatureInstance{Type: "core/monitoring", Values: map[string]interface{}{"level": "info"}}},
			},
		},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
	}

	result, err := collectNodeFeatures("api", topoResult.Nodes["api"], topoResult, resolveResult, featureCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f := result.Features["mon"]
	if f.Values["level"] != "info" {
		t.Errorf("expected 'info' (last wins), got %v", f.Values["level"])
	}
	if f.Values["port"] != 9090 {
		t.Errorf("expected port 9090 preserved, got %v", f.Values["port"])
	}
}

func TestCollectNodeFeatures_MarkersDeduplicatedAndSorted(t *testing.T) {
	t.Parallel()

	topoResult := &topology.TopologyResult{
		Nodes: map[string]*topology.TopologyNode{
			"api": {InstanceID: "api"},
		},
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

	result, err := collectNodeFeatures("api", topoResult.Nodes["api"], topoResult, resolveResult, featureCatalog())
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
