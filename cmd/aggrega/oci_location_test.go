// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuildPushReference_LocationFaithfulness pins the rule that the OCI
// repository location encodes the full, lowercased identity: provider-scoped for
// recipes, lowercased for PascalCase libraries, unchanged for already-lowercase
// kinds. Crucially, two provider-distinct recipes of the same name resolve to
// DISTINCT locations (the #2 fix) and a PascalCase library to a valid lowercase
// repo (the #1 fix).
func TestBuildPushReference_LocationFaithfulness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, kind, provider, ename, want string
	}{
		{"recipe terraform", "Recipe", "terraform", "mariadb-rds", "reg/recipe/terraform/mariadb-rds"},
		{"recipe cloudformation", "Recipe", "cloudformation", "mariadb-rds", "reg/recipe/cloudformation/mariadb-rds"},
		{"featurerecipe provider-scoped", "FeatureRecipe", "compose", "bridge", "reg/featurerecipe/compose/bridge"},
		{"schemalibrary lowercased", "SchemaLibrary", "", "AIDatastores", "reg/schemalibrary/aidatastores"},
		{"mappinglibrary lowercased", "MappingLibrary", "", "BaseMappings", "reg/mappinglibrary/basemappings"},
		{"component unchanged", "Component", "", "core/datastore/mariadb", "reg/component/core/datastore/mariadb"},
	}
	for _, tc := range cases {
		if got := buildPushReference("reg", tc.kind, tc.provider, tc.ename); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// The two provider-distinct mariadb-rds recipes must NOT share a location.
	if a, b := buildPushReference("reg", "Recipe", "terraform", "mariadb-rds"),
		buildPushReference("reg", "Recipe", "cloudformation", "mariadb-rds"); a == b {
		t.Errorf("provider-distinct recipes collided at one location %q", a)
	}
}

func schemaLib(name string) types.SchemaLibrary {
	return types.SchemaLibrary{Kind: "SchemaLibrary", Metadata: types.SchemaLibraryMetadata{Name: name}}
}

// TestVerifyNoOCILocationCollision proves the loud no-silent-collision guard:
// distinct identities at distinct locations pass; two case-variant identities
// that lower to one OCI location halt with ErrOCILocationCollision.
func TestVerifyNoOCILocationCollision(t *testing.T) {
	t.Parallel()

	clean := &catalog.Catalog{SchemaLibraries: map[string]types.SchemaLibrary{
		"AIDatastores":   schemaLib("AIDatastores"),
		"BaseComponents": schemaLib("BaseComponents"),
	}}
	if err := verifyNoOCILocationCollision(clean); err != nil {
		t.Fatalf("clean catalog: unexpected collision: %v", err)
	}

	bad := &catalog.Catalog{SchemaLibraries: map[string]types.SchemaLibrary{
		"AIDatastores": schemaLib("AIDatastores"),
		"AiDatastores": schemaLib("AiDatastores"),
	}}
	if err := verifyNoOCILocationCollision(bad); !errors.Is(err, oci.ErrOCILocationCollision) {
		t.Errorf("case-variant identities should collide; got %v", err)
	}
}
