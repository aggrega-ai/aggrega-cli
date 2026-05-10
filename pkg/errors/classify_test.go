// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import "testing"

// TestClassifyEntityCategory_AggregateIsDeveloper verifies the Aggregate
// prefix routes to DeveloperError. The Aggregate is the developer's
// authoring surface; field violations on it are developer-domain.
func TestClassifyEntityCategory_AggregateIsDeveloper(t *testing.T) {
	got := ClassifyEntityCategory("Aggregate my-app")
	if got != DeveloperError {
		t.Errorf("ClassifyEntityCategory(\"Aggregate my-app\") = %v, want %v", got, DeveloperError)
	}
}

// TestClassifyEntityCategory_ReleaseIsOps verifies the Release prefix
// routes to OpsError. The Release is the operator's authoring surface.
func TestClassifyEntityCategory_ReleaseIsOps(t *testing.T) {
	got := ClassifyEntityCategory("Release prod")
	if got != OpsError {
		t.Errorf("ClassifyEntityCategory(\"Release prod\") = %v, want %v", got, OpsError)
	}
}

// TestClassifyEntityCategory_CatalogEntities verifies all six catalog
// entity kinds route to CatalogError. Table-driven across the kinds.
func TestClassifyEntityCategory_CatalogEntities(t *testing.T) {
	cases := []string{
		"Recipe my-recipe",
		"Component core/api-server",
		"Feature core/dns-resolver",
		"FeatureRecipe core/dns-resolver-recipe",
		"Provider helm",
		"Manifest prod-manifest",
	}
	for _, entity := range cases {
		got := ClassifyEntityCategory(entity)
		if got != CatalogError {
			t.Errorf("ClassifyEntityCategory(%q) = %v, want %v", entity, got, CatalogError)
		}
	}
}

// TestClassifyEntityCategory_UnknownIsDeveloper verifies unknown prefixes
// fall back to DeveloperError (conservative — flag at the developer's
// authoring surface rather than misroute to ops).
func TestClassifyEntityCategory_UnknownIsDeveloper(t *testing.T) {
	cases := []string{
		"WeirdKind something",
		"sub-aggregate path", // free-form Entity (no Kind prefix capitalised)
		"",                   // empty Entity
	}
	for _, entity := range cases {
		got := ClassifyEntityCategory(entity)
		if got != DeveloperError {
			t.Errorf("ClassifyEntityCategory(%q) = %v, want %v (default DeveloperError)", entity, got, DeveloperError)
		}
	}
}
