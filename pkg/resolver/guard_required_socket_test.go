// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func requiredSocketCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server/api" {
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/server/api"},
					Spec: types.ComponentSpec{
						UsesSchema: map[string]types.UsesSchemaEntry{
							"databases": {
								Selector: "component.aggrega.ai/type=stateful-server",
								Required: true,
							},
							"caches": {
								Selector: "component.aggrega.ai/type=resource",
							},
						},
					},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

func TestGuardRequiredUsesSockets_MissingRequiredSocket(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {Type: "core/server/api"}, // no uses block at all
			},
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(errs), errs)
	}
	if !errors.Is(errs[0], ErrMissingRequiredSocket) {
		t.Errorf("expected ErrMissingRequiredSocket, got %v", errs[0].Err)
	}
	if !strings.Contains(errs[0].Field, ".components.api.uses.databases") {
		t.Errorf("field: %q", errs[0].Field)
	}
	if !strings.Contains(errs[0].Message, "databases") {
		t.Errorf("message: %q", errs[0].Message)
	}
}

func TestGuardRequiredUsesSockets_OptionalMissingOK(t *testing.T) {
	t.Parallel()
	// "caches" is optional — its absence is fine, only "databases" (required) triggers.
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api",
					Uses: map[string]interface{}{
						"databases": map[string]interface{}{
							"main": map[string]interface{}{"target": "db"},
						},
					},
				},
			},
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %+v", errs)
	}
}

func TestGuardRequiredUsesSockets_CompactListFormPopulates(t *testing.T) {
	t.Parallel()
	// Compact list form: `databases: [db]` should count as populated.
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api",
					Uses: map[string]interface{}{
						"databases": []interface{}{"db"},
					},
				},
			},
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 0 {
		t.Errorf("compact list form should satisfy required socket, got %+v", errs)
	}
}

func TestGuardRequiredUsesSockets_EmptyMapIsNotPopulated(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"api": {
					Type: "core/server/api",
					Uses: map[string]interface{}{
						"databases": map[string]interface{}{},
					},
				},
			},
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("empty map should count as not-populated, got %+v", errs)
	}
	if !errors.Is(errs[0], ErrMissingRequiredSocket) {
		t.Errorf("expected ErrMissingRequiredSocket")
	}
}

func TestGuardRequiredUsesSockets_SubsystemRecursion(t *testing.T) {
	t.Parallel()
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Subsystems: map[string]types.Subsystem{
				"frontend": {
					Components: map[string]types.ComponentInstance{
						"nested-api": {Type: "core/server/api"}, // missing required socket
					},
				},
			},
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error from subsystem-nested component, got %+v", errs)
	}
	if !strings.Contains(errs[0].Field, "nested-api") {
		t.Errorf("expected nested-api in field path, got %q", errs[0].Field)
	}
}
