// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
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

// TestGuardInjectKeys_StubSkipsCueAuthoringEntries verifies the
// transitional stub-skip behavior for inject-key validation: when the
// target catalog entity authors Spec.CueSchema, the provides-key
// extraction is skipped and the guard returns no errors. Production
// callers see no build halt; the deprecation log fires once per
// CUE-authoring target.
func TestGuardInjectKeys_StubSkipsCueAuthoringEntries(t *testing.T) {
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
			comp := types.Component{}
			comp.Metadata.Name = "core/api-server"
			comp.Spec.CueSchema = `provides: { host: string }`
			return comp, true
		},
	}

	if errs := guardInjectKeys(agg, catalog); errs != nil {
		t.Errorf("expected nil errors (transitional stub-skips Spec.CueSchema); got %v", errs)
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
			comp := types.Component{}
			comp.Spec.CueSchema = `provides: { host: string }`
			return comp, true
		},
	}

	if errs := guardInjectKeys(agg, catalog); errs != nil {
		t.Errorf("expected nil errors; got %v", errs)
	}
}
