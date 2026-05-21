// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
	// "caches" is optional — its absence is fine, only "databases" (required) triggers.
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("main", types.VerboseUsesEntry{Target: "db"})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
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
	// Compact list form: `databases: [db]` should count as populated.
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Compact: []string{"db"}})
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
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("databases", types.UsesSocketValue{Verbose: types.OrderedMap[types.VerboseUsesEntry]{}})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Type: "core/server/api", Uses: uses})
	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Components: components,
		},
	}
	errs := guardRequiredUsesSockets(agg, requiredSocketCatalog())
	if len(errs) != 1 {
		t.Fatalf("empty verbose map should count as not-populated, got %+v", errs)
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

// TestSocketIsPopulated_VerboseFormRecognizesPopulated pins the
// typed-wrapper Verbose-field arm of socketIsPopulated. The verbose
// form's emptiness is determined by the inner OrderedMap's length;
// empty Verbose with no Compact entries returns false; populated
// Verbose returns true.
func TestSocketIsPopulated_VerboseFormRecognizesPopulated(t *testing.T) {
	t.Parallel()

	t.Run("populated_verbose", func(t *testing.T) {
		t.Parallel()
		verbose := types.OrderedMap[types.VerboseUsesEntry]{}
		verbose.Set("alias", types.VerboseUsesEntry{Target: "main-db"})
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
		if !socketIsPopulated(uses, "databases") {
			t.Error("populated Verbose should count as populated, got false")
		}
	})

	t.Run("empty_verbose_no_compact", func(t *testing.T) {
		t.Parallel()
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{})
		if socketIsPopulated(uses, "databases") {
			t.Error("empty UsesSocketValue should count as unpopulated, got true")
		}
	})

	t.Run("compact_with_empty_string_only_unpopulated", func(t *testing.T) {
		t.Parallel()
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{Compact: []string{""}})
		if socketIsPopulated(uses, "databases") {
			t.Error("compact with only empty strings should count as unpopulated, got true")
		}
	})
}
