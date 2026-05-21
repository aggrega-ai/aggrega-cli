// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestGuardInjectKeys_NilInputsReturnNil verifies boundary behavior.
func TestGuardInjectKeys_NilInputsReturnNil(t *testing.T) {
	t.Parallel()
	if errs := guardInjectKeys(nil, CatalogLookup{}); errs != nil {
		t.Errorf("expected nil errors on nil agg; got %v", errs)
	}
	agg := &types.Aggregate{}
	if errs := guardInjectKeys(agg, CatalogLookup{Component: nil}); errs != nil {
		t.Errorf("expected nil errors on nil catalog.Component; got %v", errs)
	}
}

// TestGuardInjectKeys_NoErrorsWithInjectAndTargetSchema verifies the
// guard's current no-op contract: with developer inject blocks plus a
// target catalog entity carrying authored Schema content, the guard
// returns no errors. The provides-key extraction surface activates
// once the load pipeline compiles Spec.Schema into JSON Schema; until
// then the guard is the documented call site.
func TestGuardInjectKeys_NoErrorsWithInjectAndTargetSchema(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "test-agg"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}

	inst := types.ComponentInstance{Type: "core/api-server:1.0.0"}
	inst.Features = types.OrderedMap[types.FeatureInstance]{}
	feat := types.FeatureInstance{}
	feat.Inject = &types.InjectBlock{Env: map[string]any{"DB_URL": "host"}}
	inst.Features.Set("db", feat)
	agg.Spec.Components.Set("api", inst)

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return types.Component{}, true
		},
	}

	if errs := guardInjectKeys(agg, catalog); errs != nil {
		t.Errorf("expected nil errors; got %v", errs)
	}
}

// TestGuardInjectKeys_NoInjectIsClean verifies that components without
// inject blocks produce no errors regardless of target schema.
func TestGuardInjectKeys_NoInjectIsClean(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/api-server:1.0.0"})

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			return types.Component{}, true
		},
	}

	if errs := guardInjectKeys(agg, catalog); errs != nil {
		t.Errorf("expected nil errors; got %v", errs)
	}
}
