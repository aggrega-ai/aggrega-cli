// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"context"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestARecipeAndAFeatureRecipeOfOneNameEachKeepTheirOwnLibraryFloor drives library
// discovery over a Recipe and a FeatureRecipe that share one reference and import
// one library at different version floors — the Recipe at 1.5.0, the FeatureRecipe
// at 1.2.0. Each is its own consumer, so the selection sees both floors and settles
// on the higher, 1.5.0, which satisfies both. Keyed by the shared reference alone,
// the FeatureRecipe's floor would overwrite the Recipe's and the library would be
// pinned at 1.2.0, below what the Recipe requires.
//
// The lockfile records a consumer by its reference alone, so the two are one
// consumer row there, carrying the fragment each of them imports.
func TestARecipeAndAFeatureRecipeOfOneNameEachKeepTheirOwnLibraryFloor(t *testing.T) {
	t.Parallel()
	const consumer = "helm/shared:1.0.0"
	var c closure.Closure
	err := c.DiscoverTemplateLibraries(context.Background(),
		closure.LibraryAxisNamed("MappingLibraries"), lookupWithLibBeta(),
		[]closure.TemplateConsumer{
			{URI: consumer, Kind: closure.KindRecipe, Reference: "LibBeta:1.5.0#/mappings/Web"},
			{URI: consumer, Kind: closure.KindFeatureRecipe, Reference: "LibBeta:1.2.0#/mappings/Api"},
		},
		noBodies)
	if err != nil {
		t.Fatalf("discovery refused two compatible floors: %v", err)
	}
	if len(c.MappingLibraries) != 1 {
		t.Fatalf("expected exactly one library; got %+v", c.MappingLibraries)
	}
	lib := c.MappingLibraries[0]
	if lib.ResolvedVersion != "1.5.0" {
		t.Errorf("LibBeta selected at %q; want 1.5.0, the higher of the Recipe's and the FeatureRecipe's floors", lib.ResolvedVersion)
	}
	if len(lib.Consumers) != 1 || lib.Consumers[0].ConsumerURI != consumer || len(lib.Consumers[0].Fragments) != 2 {
		t.Errorf("consumers = %+v; want one row for %q carrying both imported fragments", lib.Consumers, consumer)
	}
}

// TestAComponentAndAFeatureOfOneNameEachKeepTheirOwnSchemaLibraryFloor is the same
// property on the schema axis, whose consumers the entity walk reads from Component
// and Feature schemas: a Component asking LibBeta at 1.5.0 and a Feature of the same
// name asking it at 1.2.0 are two consumers, so the library is selected at 1.5.0.
func TestAComponentAndAFeatureOfOneNameEachKeepTheirOwnSchemaLibraryFloor(t *testing.T) {
	t.Parallel()
	const consumer = "core/shared:1.0.0"
	lookup := &fakeLookup{schemaLibs: map[string]closure.LibraryResolution{"LibBeta": resolvedLib("1.0.0", "sha256:beta")}}
	var c closure.Closure
	err := c.DiscoverSchemaLibraries(context.Background(), lookup,
		[]closure.SchemaConsumer{
			{URI: consumer, Kind: closure.KindComponent, Schema: mustParse(t, `$ref: "LibBeta:1.5.0#/schemas/Size"`)},
			{URI: consumer, Kind: closure.KindFeature, Schema: mustParse(t, `$ref: "LibBeta:1.2.0#/schemas/Port"`)},
		},
		func(closure.ResolvedLibrary) ([]*yaml.Node, bool, error) { return nil, false, nil })
	if err != nil {
		t.Fatalf("discovery refused two compatible floors: %v", err)
	}
	if len(c.SchemaLibraries) != 1 || c.SchemaLibraries[0].ResolvedVersion != "1.5.0" {
		t.Errorf("schema libraries = %+v; want LibBeta selected at 1.5.0, the higher of the two floors", c.SchemaLibraries)
	}
}
