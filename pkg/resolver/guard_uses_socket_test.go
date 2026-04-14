package resolver

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestGuardUsesSocket(t *testing.T) {
	t.Parallel()

	webServer := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/web-server",
			Version: "1.0.0",
			Labels:  map[string]string{types.ComponentTypeLabel: "server"},
		},
		Spec: types.ComponentSpec{
			UsesSchema: map[string]types.UsesSchemaEntry{
				"databases": {Selector: "component.aggrega.ai/type=stateful-server"},
				"tools":     {Selector: "tool.aggrega.ai/impact=read-only"},
			},
		},
	}
	noUsesComp := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/postgres-db",
			Version: "1.0.0",
			Labels:  map[string]string{types.ComponentTypeLabel: "stateful-server"},
		},
		Spec: types.ComponentSpec{},
	}
	featureWithUses := types.Feature{
		Metadata: types.FeatureMetadata{
			Name:    "core/monitoring",
			Version: "1.0.0",
		},
		Spec: types.FeatureSpec{
			UsesSchema: map[string]types.UsesSchemaEntry{
				"targets": {Selector: "component.aggrega.ai/type=server"},
			},
		},
	}
	featureNoUses := types.Feature{
		Metadata: types.FeatureMetadata{
			Name:    "core/logging",
			Version: "1.0.0",
		},
		Spec: types.FeatureSpec{},
	}

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/web-server":
				return webServer, true
			case "core/postgres-db":
				return noUsesComp, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			switch typeURI {
			case "core/monitoring":
				return featureWithUses, true
			case "core/logging":
				return featureNoUses, true
			default:
				return types.Feature{}, false
			}
		},
	}

	tests := []struct {
		name    string
		agg     *types.Aggregate
		wantErr bool
		errSent error
	}{
		{
			name: "valid socket passes",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: map[string]types.ComponentInstance{
						"api": {
							Type: "core/web-server",
							Uses: map[string]interface{}{
								"databases": []interface{}{"my-db"},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "unknown socket fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: map[string]types.ComponentInstance{
						"api": {
							Type: "core/web-server",
							Uses: map[string]interface{}{
								"caches": []interface{}{"my-cache"}, // not in usesSchema
							},
						},
					},
				},
			},
			wantErr: true,
			errSent: ErrUnknownUsesSocket,
		},
		{
			name: "no usesSchema with uses fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: map[string]types.ComponentInstance{
						"db": {
							Type: "core/postgres-db",
							Uses: map[string]interface{}{
								"anything": []interface{}{"target"},
							},
						},
					},
				},
			},
			wantErr: true,
			errSent: ErrUnknownUsesSocket,
		},
		{
			name: "no uses passes silently",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: map[string]types.ComponentInstance{
						"db": {Type: "core/postgres-db"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "feature valid socket passes",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: map[string]types.ComponentInstance{
						"api": {
							Type: "core/web-server",
							Features: map[string]types.FeatureInstance{
								"mon": {
									Type: "core/monitoring",
									Uses: map[string]interface{}{
										"targets": []interface{}{"api"},
									},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "feature unknown socket fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: map[string]types.ComponentInstance{
						"api": {
							Type: "core/web-server",
							Features: map[string]types.FeatureInstance{
								"mon": {
									Type: "core/monitoring",
									Uses: map[string]interface{}{
										"unknown-socket": []interface{}{"x"},
									},
								},
							},
						},
					},
				},
			},
			wantErr: true,
			errSent: ErrUnknownUsesSocket,
		},
		{
			name: "feature no usesSchema with uses fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: map[string]types.ComponentInstance{
						"api": {
							Type: "core/web-server",
							Features: map[string]types.FeatureInstance{
								"log": {
									Type: "core/logging",
									Uses: map[string]interface{}{
										"something": []interface{}{"x"},
									},
								},
							},
						},
					},
				},
			},
			wantErr: true,
			errSent: ErrUnknownUsesSocket,
		},
		{
			name:    "nil aggregate passes",
			agg:     nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := guardUsesSocket(tt.agg, catalog)
			if tt.wantErr {
				if len(errs) == 0 {
					t.Fatal("expected errors, got none")
				}
				if tt.errSent != nil && !errors.Is(errs[0], tt.errSent) {
					t.Errorf("expected %v, got %v", tt.errSent, errs[0].Err)
				}
			} else {
				if len(errs) > 0 {
					t.Errorf("expected no errors, got %d: %v", len(errs), errs[0])
				}
			}
		})
	}
}

func TestGuardUsesSocket_DeterministicOrder(t *testing.T) {
	t.Parallel()

	noUsesComp := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/db", Version: "1.0.0"},
		Spec:     types.ComponentSpec{},
	}
	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return noUsesComp, true
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			return types.Feature{}, false
		},
	}

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"zebra": {Type: "core/db", Uses: map[string]interface{}{"a": nil}},
				"alpha": {Type: "core/db", Uses: map[string]interface{}{"b": nil}},
				"mid":   {Type: "core/db", Uses: map[string]interface{}{"c": nil}},
			},
		},
	}

	// Run multiple times to verify determinism
	var firstFields []string
	for i := 0; i < 10; i++ {
		errs := guardUsesSocket(agg, catalog)
		if len(errs) != 3 {
			t.Fatalf("expected 3 errors, got %d", len(errs))
		}
		fields := []string{errs[0].Field, errs[1].Field, errs[2].Field}
		if i == 0 {
			firstFields = fields
		} else {
			for j, f := range fields {
				if f != firstFields[j] {
					t.Errorf("run %d: error order changed at index %d: %q vs %q", i, j, f, firstFields[j])
				}
			}
		}
	}
	// Verify sorted by component name (alpha < mid < zebra)
	if firstFields[0] > firstFields[1] || firstFields[1] > firstFields[2] {
		t.Errorf("errors not sorted: %v", firstFields)
	}
}
