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

// TestClassifyEntityCategory_OperatorEntities verifies the operator's own
// authoring surfaces route to OpsError. The Release declares the deployment
// and the Manifest records which Recipe each type is deployed through; the
// operator is who edits either file.
func TestClassifyEntityCategory_OperatorEntities(t *testing.T) {
	cases := []string{
		"Release prod",
		"Manifest prod-manifest",
	}
	for _, entity := range cases {
		got := ClassifyEntityCategory(entity)
		if got != OpsError {
			t.Errorf("ClassifyEntityCategory(%q) = %v, want %v", entity, got, OpsError)
		}
	}
}

// TestClassifyEntityCategory_CatalogEntities verifies the catalog entity
// kinds route to CatalogError. Table-driven across the kinds; the rows are
// the statement of how many there are, so the count is not also written here.
func TestClassifyEntityCategory_CatalogEntities(t *testing.T) {
	cases := []string{
		"Recipe my-recipe",
		"Component core/api-server",
		"Feature core/dns-resolver",
		"FeatureRecipe core/dns-resolver-recipe",
	}
	for _, entity := range cases {
		got := ClassifyEntityCategory(entity)
		if got != CatalogError {
			t.Errorf("ClassifyEntityCategory(%q) = %v, want %v", entity, got, CatalogError)
		}
	}
}

// TestClassifyEntityKind_MapsEveryCoveredKind pins the kind-keyed mapping:
// the kind is matched exactly, so no reference text is parsed, and every
// covered kind reports its domain together with a true.
func TestClassifyEntityKind_MapsEveryCoveredKind(t *testing.T) {
	cases := map[string]Category{
		"Aggregate":     DeveloperError,
		"Release":       OpsError,
		"Manifest":      OpsError,
		"Recipe":        CatalogError,
		"Component":     CatalogError,
		"Feature":       CatalogError,
		"FeatureRecipe": CatalogError,
	}
	for kind, want := range cases {
		got, ok := ClassifyEntityKind(kind)
		if !ok {
			t.Errorf("ClassifyEntityKind(%q) reported the kind as uncovered", kind)
			continue
		}
		if got != want {
			t.Errorf("ClassifyEntityKind(%q) = %v, want %v", kind, got, want)
		}
	}
}

// TestClassifyEntityKind_UncoveredKindIsReportedNotDefaulted verifies that a
// kind outside the mapping — the empty kind included, and a rendered
// reference passed where a bare kind belongs — reports false and yields no
// category. A caller cannot acquire a category here by ignoring the second
// result, which is what makes the omission impossible to make silently.
func TestClassifyEntityKind_UncoveredKindIsReportedNotDefaulted(t *testing.T) {
	cases := []string{
		"",
		"Plugin",
		"SchemaLibrary",
		"component",
		"Component core/api-server", // a rendered reference, not a kind
	}
	for _, kind := range cases {
		got, ok := ClassifyEntityKind(kind)
		if ok {
			t.Errorf("ClassifyEntityKind(%q) reported the kind as covered, want uncovered", kind)
		}
		if got != "" {
			t.Errorf("ClassifyEntityKind(%q) returned category %q, want the empty category", kind, got)
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
