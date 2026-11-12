// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package errors

import "testing"

// TestClassifyEntityCategory_AggregateIsDeveloper verifies the Aggregate
// prefix routes to DeveloperError. The Aggregate is the developer's
// authoring surface; field violations on it are developer-domain.
func TestClassifyEntityCategory_AggregateIsDeveloper(t *testing.T) {
	got, covered := ClassifyEntityCategory("Aggregate my-app")
	if !covered {
		t.Fatal("ClassifyEntityCategory(\"Aggregate my-app\") reported the reference as uncovered")
	}
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
		got, covered := ClassifyEntityCategory(entity)
		if !covered {
			t.Errorf("ClassifyEntityCategory(%q) reported the reference as uncovered", entity)
			continue
		}
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
		got, covered := ClassifyEntityCategory(entity)
		if !covered {
			t.Errorf("ClassifyEntityCategory(%q) reported the reference as uncovered", entity)
			continue
		}
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

// TestClassifyEntityCategory_UncoveredReferenceIsReportedNotDefaulted verifies
// that a reference the mapping does not cover is reported as uncovered and
// yields no category — the same answer ClassifyEntityKind gives for an
// unmapped kind, so the pair has one mapping rather than a mapping and a
// default.
//
// 🔴 The rows are the two facts a caller has to be able to tell apart, and
// neither of them is a category. A reference naming a kind outside the mapping
// and a reference naming nothing at all both arrive here as "the mapping has
// no answer", and what each MEANS is the caller's to decide: a finding that
// named nothing is filed under no surface, while one that named something
// outside the mapping is filed where its raise sites sit. Deciding either of
// those here would hand every future caller a verdict it was never asked for,
// which is exactly how a compiler fault came to be reported as the author's.
func TestClassifyEntityCategory_UncoveredReferenceIsReportedNotDefaulted(t *testing.T) {
	cases := []string{
		"WeirdKind something",
		"sub-aggregate path", // free-form Entity (no Kind prefix capitalised)
		"component",          // a covered kind, wrongly cased
		"",                   // named nothing at all
	}
	for _, entity := range cases {
		got, covered := ClassifyEntityCategory(entity)
		if covered {
			t.Errorf("ClassifyEntityCategory(%q) reported the reference as covered, want uncovered.\n"+
				"INVARIANT: the classifier reports what the mapping says and decides nothing beyond it.\n"+
				"CONSEQUENCE: a caller acquires a category by omission, and a raise site that named "+
				"no document has one chosen for it.", entity)
		}
		if got != "" {
			t.Errorf("ClassifyEntityCategory(%q) returned category %q, want the empty category.\n"+
				"INVARIANT: an uncovered reference yields no category, so nothing reads a guess as "+
				"a verdict.\n"+
				"CONSEQUENCE: a caller that ignores the second result still gets a surface named, "+
				"which is the silent guess this signature exists to prevent.", entity, got)
		}
	}
}
