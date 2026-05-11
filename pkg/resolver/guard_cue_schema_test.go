// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// TestGuardCueSchema_NilInputsReturnNil verifies the guard's boundary
// on nil inputs.
func TestGuardCueSchema_NilInputsReturnNil(t *testing.T) {
	t.Parallel()
	if errs := guardCueSchema(nil, CatalogLookup{}, nil); errs != nil {
		t.Errorf("expected nil errors on nil agg; got %v", errs)
	}
	agg := &types.Aggregate{}
	if errs := guardCueSchema(agg, CatalogLookup{Component: nil}, nil); errs != nil {
		t.Errorf("expected nil errors on nil catalog.Component; got %v", errs)
	}
}

// TestGuardCueSchema_StubSkipsCueAuthoringEntries verifies the
// transitional stub-skip behavior: catalog entities authoring
// Spec.CueSchema produce no validation errors. The deprecation log
// fires once per CUE-authoring entity; production callers see no
// build halt.
func TestGuardCueSchema_StubSkipsCueAuthoringEntries(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Metadata.Name = "test-agg"
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/api-server:1.0.0"})

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			comp := types.Component{}
			comp.Metadata.Name = "core/api-server"
			comp.Spec.CueSchema = `values: { port: int }`
			return comp, true
		},
	}

	if errs := guardCueSchema(agg, catalog, nil); errs != nil {
		t.Errorf("expected nil errors (transitional stub-skips Spec.CueSchema); got %v", errs)
	}
}

// TestGuardCueSchema_NoCueSchemaIsClean verifies that a catalog entity
// without Spec.CueSchema produces no errors and no deprecation log.
func TestGuardCueSchema_NoCueSchemaIsClean(t *testing.T) {
	t.Parallel()

	agg := &types.Aggregate{}
	agg.Spec.Components = types.OrderedMap[types.ComponentInstance]{}
	agg.Spec.Components.Set("api", types.ComponentInstance{Type: "core/api-server:1.0.0"})

	catalog := CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			comp := types.Component{}
			comp.Metadata.Name = "core/api-server"
			return comp, true
		},
	}

	if errs := guardCueSchema(agg, catalog, nil); errs != nil {
		t.Errorf("expected nil errors; got %v", errs)
	}
}
