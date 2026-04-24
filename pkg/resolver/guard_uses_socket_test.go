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

	webServer := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/web-server",
			Version: "1.0.0",
			Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "server"); return _m }(),
		},
		Spec: types.ComponentSpec{
			UsesSchema: func() types.OrderedMap[types.UsesSchemaEntry] { _m := types.OrderedMap[types.UsesSchemaEntry]{}; _m.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"}); _m.Set("tools", types.UsesSchemaEntry{Selector: "tool.aggrega.ai/impact=read-only"}); return _m }(),
		},
	}
	noUsesComp := types.Component{
		Metadata: types.ComponentMetadata{
			Name:    "core/postgres-db",
			Version: "1.0.0",
			Labels:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set(types.ComponentTypeLabel, "stateful-server"); return _m }(),
		},
		Spec: types.ComponentSpec{},
	}
	featureWithUses := types.Feature{
		Metadata: types.FeatureMetadata{
			Name:    "core/monitoring",
			Version: "1.0.0",
		},
		Spec: types.FeatureSpec{
			UsesSchema: func() types.OrderedMap[types.UsesSchemaEntry] { _m := types.OrderedMap[types.UsesSchemaEntry]{}; _m.Set("targets", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=server"}); return _m }(),
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
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
							Type: "core/web-server",
							Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("databases", []interface{}{"my-db"}); return _m }(),
						}); return _m }(),
				},
			},
			wantErr: false,
		},
		{
			name: "unknown socket fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
							Type: "core/web-server",
							Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("caches", []interface{}{"my-cache"}); return _m }(),
						}); return _m }(),
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
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("db", types.ComponentInstance{
							Type: "core/postgres-db",
							Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("anything", []interface{}{"target"}); return _m }(),
						}); return _m }(),
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
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("db", types.ComponentInstance{Type: "core/postgres-db"}); return _m }(),
				},
			},
			wantErr: false,
		},
		{
			name: "feature valid socket passes",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
							Type: "core/web-server",
							Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("mon", types.FeatureInstance{
									Type: "core/monitoring",
									Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("targets", []interface{}{"api"}); return _m }(),
								}); return _m }(),
						}); return _m }(),
				},
			},
			wantErr: false,
		},
		{
			name: "feature unknown socket fails",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
							Type: "core/web-server",
							Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("mon", types.FeatureInstance{
									Type: "core/monitoring",
									Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("unknown-socket", []interface{}{"x"}); return _m }(),
								}); return _m }(),
						}); return _m }(),
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
					Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("api", types.ComponentInstance{
							Type: "core/web-server",
							Features: func() types.OrderedMap[types.FeatureInstance] { _m := types.OrderedMap[types.FeatureInstance]{}; _m.Set("log", types.FeatureInstance{
									Type: "core/logging",
									Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("something", []interface{}{"x"}); return _m }(),
								}); return _m }(),
						}); return _m }(),
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
			Components: func() types.OrderedMap[types.ComponentInstance] { _m := types.OrderedMap[types.ComponentInstance]{}; _m.Set("zebra", types.ComponentInstance{Type: "core/db", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("a", nil); return _m }()}); _m.Set("alpha", types.ComponentInstance{Type: "core/db", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("b", nil); return _m }()}); _m.Set("mid", types.ComponentInstance{Type: "core/db", Uses: func() types.OrderedMap[interface{}] { _m := types.OrderedMap[interface{}]{}; _m.Set("c", nil); return _m }()}); return _m }(),
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
