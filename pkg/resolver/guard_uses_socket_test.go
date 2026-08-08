// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// compactUses constructs an OrderedMap[UsesSocketValue] with one socket
// in compact form. Convenience helper for tests; production code reaches
// the typed shape via YAML or JSON unmarshal.
func compactUses(socket string, targets ...string) types.OrderedMap[types.UsesSocketValue] {
	out := types.OrderedMap[types.UsesSocketValue]{}
	out.Set(socket, types.UsesSocketValue{Compact: targets})
	return out
}

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
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("mon", types.FeatureInstance{Type: "core/monitoring", Uses: compactUses("targets", "api")})
	features2 := types.OrderedMap[types.FeatureInstance]{}
	features2.Set("mon", types.FeatureInstance{Type: "core/monitoring", Uses: compactUses("unknown-socket", "x")})
	features3 := types.OrderedMap[types.FeatureInstance]{}
	features3.Set("log", types.FeatureInstance{Type: "core/logging", Uses: compactUses("something", "x")})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Uses: compactUses("databases", "my-db")})
	components2 := types.OrderedMap[types.ComponentInstance]{}
	components2.Set("api", types.ComponentInstance{Type: "core/web-server", Uses: compactUses("caches", "my-cache")})
	components3 := types.OrderedMap[types.ComponentInstance]{}
	components3.Set("db", types.ComponentInstance{Type: "core/postgres-db", Uses: compactUses("anything", "target")})
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
			// An author may name a socket the component's catalog type does not
			// declare. The edge is built; the IR records the socket as not
			// catalog-declared so a policy can refuse it.
			name: "socket absent from a declared usesSchema passes",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: components2,
				},
			},
			wantErr: false,
		},
		{
			// Catalog silence means the connection was not anticipated, not that it
			// is forbidden — so a component type declaring no usesSchema at all can
			// still host an author-named socket.
			name: "component with no usesSchema at all passes",
			agg: &types.Aggregate{
				Metadata: types.AggregateMetadata{Name: "test-app"},
				Spec: types.AggregateSpec{
					Components: components3,
				},
			},
			wantErr: false,
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
			errSent: ErrUnknownUsesSocket(),
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
			errSent: ErrUnknownUsesSocket(),
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
			errs := guardUsesSocket(normalized(t, tt.agg), nil, catalog)
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
	// Attachment sockets, not component-own sockets: a component's own uses block
	// may name a socket the catalog did not declare, so it produces no diagnostic
	// to order. An attachment's socket must exist on the attached Feature, which is
	// what still yields one diagnostic per offending socket here.
	noUsesFeat := types.Feature{
		Metadata: types.FeatureMetadata{Name: "core/logging", Version: "1.0.0"},
		Spec:     types.FeatureSpec{},
	}
	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return noUsesComp, true
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			return noUsesFeat, true
		},
	}
	attach := func(socket string) types.OrderedMap[types.FeatureInstance] {
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set(socket, types.UsesSocketValue{Compact: []string{"x"}})
		features := types.OrderedMap[types.FeatureInstance]{}
		features.Set("log", types.FeatureInstance{Type: "core/logging", Uses: uses})
		return features
	}
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("zebra", types.ComponentInstance{Type: "core/db", Features: attach("a")})
	components.Set("alpha", types.ComponentInstance{Type: "core/db", Features: attach("b")})
	components.Set("mid", types.ComponentInstance{Type: "core/db", Features: attach("c")})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	var firstFields []string
	for i := 0; i < 10; i++ {
		errs := guardUsesSocket(normalized(t, agg), nil, catalog)
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
