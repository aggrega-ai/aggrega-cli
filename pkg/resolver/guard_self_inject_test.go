// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func selfInjectCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/web-server": // White-Box: allows env, file
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/web-server"},
					Spec:     types.ComponentSpec{Inject: []string{"env", "file"}},
				}, true
			case "core/database/postgres": // Black-Box: no spec.inject
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/database/postgres"},
					Spec:     types.ComponentSpec{},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardSelfInject_WhiteBoxAccepted(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Inject: &types.InjectBlock{Env: map[string]interface{}{"PORT": "8080"}}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(agg, selfInjectCatalog())
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs[0])
	}
}

func TestGuardSelfInject_BlackBoxRejected(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/database/postgres", Inject: &types.InjectBlock{Env: map[string]interface{}{"X": "1"}}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(agg, selfInjectCatalog())
	if len(errs) == 0 {
		t.Fatal("expected error for Black-Box self-inject")
	}
	if !errors.Is(errs[0], ErrSelfInjectForbidden) {
		t.Errorf("expected ErrSelfInjectForbidden, got: %v", errs[0])
	}
}

func TestGuardSelfInject_ForbiddenMechanism(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Inject: &types.InjectBlock{Arg: map[string]interface{}{"--flag": "value"}}})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(agg, selfInjectCatalog())
	if len(errs) == 0 {
		t.Fatal("expected error for forbidden mechanism")
	}
	if !errors.Is(errs[0], ErrSelfInjectForbidden) {
		t.Errorf("expected ErrSelfInjectForbidden, got: %v", errs[0])
	}
}

func TestGuardSelfInject_KeyCollision(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[interface{}]{}
	uses.Set("databases", map[string]interface{}{"db": map[string]interface{}{"target": "my-db", "inject": map[string]interface{}{"env": map[string]interface{}{"DB_URL": "host"}}}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/web-server", Inject: &types.InjectBlock{Env: map[string]interface{}{"DB_URL": "localhost"}}, Uses: uses})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(agg, selfInjectCatalog())
	found := false
	for _, e := range errs {
		if errors.Is(e, ErrInjectKeyCollision) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ErrInjectKeyCollision, got %d errors: %v", len(errs), errs)
	}
}

func TestGuardSelfInject_NoInjectNilPasses(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/database/postgres"})

	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := guardSelfInject(agg, selfInjectCatalog())
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errs))
	}
}
