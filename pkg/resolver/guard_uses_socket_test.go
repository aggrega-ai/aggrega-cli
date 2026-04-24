// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestGuardUsesSocket(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
	usesSchema.Set("tools", types.UsesSchemaEntry{Selector: "tool.aggrega.ai/impact=read-only"})

	webServer := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/web-server",
			Version: "1.0.0",
			Labels:  labels,
		},
		Spec: types.ComponentSpec{
			UsesSchema: usesSchema,
		},
	}
	labels2 := types.OrderedMap[string]{}
	labels2.Set(types.ComponentTypeLabel, "stateful-server")
	noUsesComp := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/postgres-db",
			Version: "1.0.0",
			Labels:  labels2,
		},
		Spec: types.ComponentSpec{},
	}
	usesSchema2 := types.OrderedMap[types.UsesSchemaEntry]{}
	usesSchema2.Set("targets", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=server"})
	featureWithUses := types.Feature{
		Metadata: types.FeatureMetadata{
			Name:    "core/monitoring",
			Version: "1.0.0",
		},
		Spec: types.FeatureSpec{
			UsesSchema: usesSchema2,
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
	uses4 := types.OrderedMap[interface{}]{}
	uses4.Set("targets", []interface{}{"api"})
	uses5 := types.OrderedMap[interface{}]{}
	uses5.Set("unknown-socket", []interface{}{"x"})
	uses6 := types.OrderedMap[interface{}]{}
	uses6.Set("something", []interface{}{"x"})
	uses := types.OrderedMap[interface{}]{}
	uses.Set("databases", []interface{}{"my-db"})
	uses2 := types.OrderedMap[interface{}]{}
	uses2.Set("caches", []interface{}{"my-cache"})
	uses3 := types.OrderedMap[interface{}]{}
	uses3.Set("anything", []interface{}{"target"})
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("mon", types.FeatureInstance{Type: "core/monitoring", Uses: uses4})
	features2 := types.OrderedMap[types.FeatureInstance]{}
	features2.Set("mon", types.FeatureInstance{Type: "core/monitoring", Uses: uses5})
	features3 := types.OrderedMap[types.FeatureInstance]{}
	features3.Set("log", types.FeatureInstance{Type: "core/logging", Uses: uses6})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Uses: uses})
	components2 := types.OrderedMap[types.ComponentInstance]{}
	components2.Set("api", types.ComponentInstance{Type: "core/web-server", Uses: uses2})
	components3 := types.OrderedMap[types.ComponentInstance]{}
	components3.Set("db", types.ComponentInstance{Type: "core/postgres-db", Uses: uses3})
	components4 := types.OrderedMap[types.ComponentInstance]{}
	components4.Set("db", types.ComponentInstance{Type: "core/postgres-db"})
	components5 := types.OrderedMap[types.ComponentInstance]{}
	components5.Set("api", types.ComponentInstance{Type: "core/web-server", Features: features})
	components6 := types.OrderedMap[types.ComponentInstance]{}
	components6.Set("api", types.ComponentInstance{Type: "core/web-server", Features: features2})
	components7 := types.OrderedMap[types.ComponentInstance]{}
	components7.Set("api", types.ComponentInstance{Type: "core/web-server", Features: features3})
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
					Components: components,
				},
			},
			wantErr: false,
		},
		{
			name: "unknown socket fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: components2,
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
					Components: components3,
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
					Components: components4,
				},
			},
			wantErr: false,
		},
		{
			name: "feature valid socket passes",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: components5,
				},
			},
			wantErr: false,
		},
		{
			name: "feature unknown socket fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: components6,
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
					Components: components7,
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
	uses := types.OrderedMap[interface{}]{}
	uses.Set("a", nil)
	uses2 := types.OrderedMap[interface{}]{}
	uses2.Set("b", nil)
	uses3 := types.OrderedMap[interface{}]{}
	uses3.Set("c", nil)
	components := types.OrderedMap[types.ComponentInstance]{

		// Run multiple times to verify determinism
	}
	components.Set("zebra", types.ComponentInstance{Type: "core/db", Uses: uses})
	components.Set("alpha", types.ComponentInstance{Type: "core/db", Uses: uses2})
	components.Set("mid", types.ComponentInstance{Type: "core/db", Uses: uses3})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

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
