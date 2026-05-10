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
				usesSchema := types.OrderedMap[types.UsesSchemaEntry]{}
				usesSchema.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server", Required: true})
				usesSchema.Set("caches", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=resource"})
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/server/api"},
					Spec: types.ComponentSpec{
						UsesSchema: usesSchema,
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
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api"})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
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
	uses :=
		// "caches" is optional — its absence is fine, only "databases" (required) triggers.
		types.OrderedMap[any]{}
	uses.Set("databases", map[string]any{"main": map[string]any{"target": "db"}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %+v", errs)
	}
}

func TestGuardRequiredUsesSockets_CompactListFormPopulates(t *testing.T) {
	t.Parallel()
	uses :=
		// Compact list form: `databases: [db]` should count as populated.
		types.OrderedMap[any]{}
	uses.Set("databases", []any{"db"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 0 {
		t.Errorf("compact list form should satisfy required socket, got %+v", errs)
	}
}

func TestGuardRequiredUsesSockets_EmptyMapIsNotPopulated(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[any]{}
	uses.Set("databases", map[string]any{})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
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
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("nested-api", types.ComponentInstance{Type: "core/server/api"})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("frontend", types.Subsystem{Components: components})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
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
