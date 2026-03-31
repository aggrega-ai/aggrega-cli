package linter

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestValidateRequiredFeatures_table(t *testing.T) {
	t.Parallel()

	baseAgg := func() *types.Aggregate {
		return &types.Aggregate{
			Spec: types.AggregateSpec{
				Components: map[string]types.AggregateComponent{
					"gw": {
						Type: "ai-gateway",
						Features: []types.AggregateFeatureAttachment{
							{Type: "semantic-routing"},
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name    string
		agg     func() *types.Aggregate
		cat     *types.Catalog
		wantErr bool
	}{
		{
			name: "nil_catalog_skips",
			agg:  baseAgg,
			cat:  nil,
		},
		{
			name: "empty_catalog_skips",
			agg:  baseAgg,
			cat: &types.Catalog{ComponentDefinitions: map[string]types.ComponentDefinition{}},
		},
		{
			name: "satisfied_by_type",
			agg:  baseAgg,
			cat: &types.Catalog{
				ComponentDefinitions: map[string]types.ComponentDefinition{
					"ai-gateway": {
						Metadata: types.ComponentMetadata{Name: "ai-gateway", Labels: map[string]string{"component.aggrega.ai/type": "ai-gateway"}},
						Spec: types.ComponentSpec{
							RequiredFeatures: []string{"semantic-routing"},
						},
					},
				},
			},
		},
		{
			name: "satisfied_by_name_only",
			agg: func() *types.Aggregate {
				a := baseAgg()
				a.Spec.Components["gw"] = types.AggregateComponent{
					Type:     "ai-gateway",
					Features: []types.AggregateFeatureAttachment{{Name: "semantic-routing"}},
				}
				return a
			},
			cat: &types.Catalog{
				ComponentDefinitions: map[string]types.ComponentDefinition{
					"ai-gateway": {
						Spec: types.ComponentSpec{RequiredFeatures: []string{"semantic-routing"}},
					},
				},
			},
		},
		{
			name: "missing_required_fatal",
			agg: func() *types.Aggregate {
				a := baseAgg()
				a.Spec.Components["gw"] = types.AggregateComponent{Type: "ai-gateway", Features: nil}
				return a
			},
			cat: &types.Catalog{
				ComponentDefinitions: map[string]types.ComponentDefinition{
					"ai-gateway": {
						Spec: types.ComponentSpec{RequiredFeatures: []string{"data-masking"}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "underscore_hyphen_normalized",
			agg: func() *types.Aggregate {
				a := baseAgg()
				a.Spec.Components["gw"] = types.AggregateComponent{
					Type:     "ai-gateway",
					Features: []types.AggregateFeatureAttachment{{Type: "semantic_routing"}},
				}
				return a
			},
			cat: &types.Catalog{
				ComponentDefinitions: map[string]types.ComponentDefinition{
					"ai-gateway": {
						Spec: types.ComponentSpec{RequiredFeatures: []string{"semantic-routing"}},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateRequiredFeatures(tc.agg(), tc.cat)
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tc.wantErr && err != nil {
				const prefix = "Capability Violation:"
				if len(err.Error()) < len(prefix) || err.Error()[:len(prefix)] != prefix {
					t.Fatalf("expected capability violation prefix, got %v", err)
				}
			}
		})
	}
}
