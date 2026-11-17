// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
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

// TestOCILocation_PushEqualsPull_PerKind locks the one-location rule: push and
// pull address every kind through the single catalog.OCILocation. The pull side
// (entityReference / libraryReference / the aggregate reference) forms
// `<registry>/<OCILocation(kind, provider, name)>`; this asserts buildPushReference
// produces byte-identical references from the same formula, so a future
// hand-rolled location on the push side (one that stops delegating to
// OCILocation) is caught here rather than silently splitting push from pull.
func TestOCILocation_PushEqualsPull_PerKind(t *testing.T) {
	t.Parallel()
	const reg = "reg.example"
	cases := []struct{ kind, provider, name string }{
		{"Component", "", "core/datastore/mariadb"},
		{"Feature", "", "core/secret-value"},
		{"Recipe", "compose", "mariadb"},
		{"Recipe", "terraform", "mariadb-rds"},
		{"FeatureRecipe", "compose", "provided-secret"},
		{"Provider", "", "compose"},
		{"SchemaLibrary", "", "BaseComponents"},
		{"MappingLibrary", "", "NetworkPolicies"},
		{"Aggregate", "", "test/app-agg"},
	}
	for _, c := range cases {
		// pullLocation mirrors the pull-side reference rule (oci_puller.go):
		// <registry>/<OCILocation(kind, provider, name)>.
		pullLocation := reg + "/" + catalog.OCILocation(c.kind, c.provider, c.name)
		if push := buildPushReference(reg, c.kind, c.provider, c.name); push != pullLocation {
			t.Errorf("%s %q: push location %q != pull location %q (push/pull split the single OCILocation rule)",
				c.kind, c.name, push, pullLocation)
		}
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

	clean := &catalog.Catalog{SchemaLibraryVersions: map[string]types.SchemaLibrary{
		"AIDatastores":   schemaLib("AIDatastores"),
		"BaseComponents": schemaLib("BaseComponents"),
	}}
	if err := validateops.VerifyNoOCILocationCollision(clean); err != nil {
		t.Fatalf("clean catalog: unexpected collision: %v", err)
	}

	bad := &catalog.Catalog{SchemaLibraryVersions: map[string]types.SchemaLibrary{
		"AIDatastores": schemaLib("AIDatastores"),
		"AiDatastores": schemaLib("AiDatastores"),
	}}
	if err := validateops.VerifyNoOCILocationCollision(bad); !errors.Is(err, oci.ErrOCILocationCollision()) {
		t.Errorf("case-variant identities should collide; got %v", err)
	}
}
