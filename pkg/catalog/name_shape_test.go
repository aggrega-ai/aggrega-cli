// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the regex source-of-truth in name_shape.go. Two surfaces:
//
//  1. Per-pattern table-driven tests with at least one positive and
//     one negative fixture per pattern. Each test compiles the pattern
//     it exercises from the exported string constant, which is the only
//     form the file declares. Positive fixtures for the per-entity-kind
//     patterns come from the hub catalog (the empirical source-of-truth
//     for what catalog names exist in practice). Negative fixtures are
//     author-constructed.
//
//  2. A guard on the declarations' literal form. The publishing
//     linter's symmetry test extracts these patterns from this
//     package's source text, so a reflow that keeps the Go code
//     compiling can still break the cross-language pin. The guard
//     applies the same extraction here, in the Go suite, so the break
//     surfaces without waiting for the Rust test surface.

package catalog

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestComponentNameShape_MatchesThreeKebabSegments(t *testing.T) {
	re := regexp.MustCompile(ComponentNameShape)
	cases := []struct {
		input string
		want  bool
	}{
		// Hub catalog fixtures
		{"core/datastore/mariadb", true},
		{"core/gateway/ai-gateway", true},
		{"core/ai-model/azure-openai", true},
		{"core/messaging/base", true},
		{"core/ai-pipeline/base", true},
		// Wrong segment count
		{"core/database", false},
		{"mariadb", false},
		{"core/datastore/mariadb/extra", false},
		// Wrong character class
		{"Core/database/mariadb", false},
		{"core/datastore/Mariadb", false},
		{"core/datastore/_mariadb", false},
		{"core/datastore/mariadb_v2", false},
		// Empty segment
		{"core//mariadb", false},
		{"/database/mariadb", false},
		{"core/datastore/", false},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("ComponentNameShape.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestFeatureNameShape_MatchesTwoKebabSegments(t *testing.T) {
	re := regexp.MustCompile(FeatureNameShape)
	cases := []struct {
		input string
		want  bool
	}{
		// Hub catalog fixtures
		{"core/cryptographic-signature", true},
		{"core/human-approval-gate", true},
		{"core/tls-pinning", true},
		{"core/concurrency-limit", true},
		// Wrong segment count
		{"cryptographic-signature", false},
		{"core/data-masking/extra", false},
		// Wrong character class
		{"Core/data-masking", false},
		{"core/Data-masking", false},
		{"core/data_masking", false},
		// Empty segment
		{"core/", false},
		{"/data-masking", false},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("FeatureNameShape.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestRecipeNameShape_MatchesSingleKebabSegment(t *testing.T) {
	re := regexp.MustCompile(RecipeNameShape)
	cases := []struct {
		input string
		want  bool
	}{
		// Hub catalog fixtures
		{"mariadb", true},
		{"mariadb-bitnami", true},
		{"mariadb-rds", true},
		// Multi-segment forms
		{"core/helm/mariadb-bitnami", false},
		{"helm/mariadb-bitnami", false},
		// Wrong character class
		{"Mariadb", false},
		{"mariadb_v2", false},
		{"-mariadb", false},
		{"1mariadb", false},
		// Empty
		{"", false},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("RecipeNameShape.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestFeatureRecipeNameShape_MatchesSingleKebabSegment(t *testing.T) {
	re := regexp.MustCompile(FeatureRecipeNameShape)
	cases := []struct {
		input string
		want  bool
	}{
		// Hub catalog fixtures
		{"k8s-secret-from-parameter", true},
		{"dotenv-secret", true},
		{"compose-bridge-fr", true},
		// Multi-segment forms
		{"helm/k8s-secret-from-parameter", false},
		{"core/helm/k8s-secret-from-parameter", false},
		// Wrong character class
		{"K8s-secret", false},
		{"k8s_secret", false},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("FeatureRecipeNameShape.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestAggregateNameShape_MatchesTwoKebabSegments(t *testing.T) {
	// No hub fixtures available; pattern matches the entity-reference
	// shape pending verification against the first published Aggregate.
	re := regexp.MustCompile(AggregateNameShape)
	cases := []struct {
		input string
		want  bool
	}{
		// Author-constructed positive (matches the documented shape)
		{"core/db-module", true},
		{"acme/web-service", true},
		// Wrong segment count
		{"db-module", false},
		{"core/db-module/extra", false},
		// Wrong character class
		{"Core/db-module", false},
		{"core/Db-module", false},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("AggregateNameShape.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestSchemaLibraryNameShape_MatchesPascalCase(t *testing.T) {
	re := regexp.MustCompile(SchemaLibraryNameShape)
	cases := []struct {
		input string
		want  bool
	}{
		// Hub catalog fixtures
		{"BaseComponents", true},
		{"AIDatastores", true},
		// Author-constructed positive
		{"WebPrimitives", true},
		// Wrong case
		{"baseComponents", false},
		{"base-components", false},
		{"base_components", false},
		// Multi-segment
		{"aggrega/datastores", false},
		// Empty
		{"", false},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("SchemaLibraryNameShape.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestMappingLibraryNameShape_MatchesPascalCase(t *testing.T) {
	// No hub fixtures available; pattern matches the JSON Schema
	// envelope at mappinglibrary-v1alpha1.schema.json.
	re := regexp.MustCompile(MappingLibraryNameShape)
	cases := []struct {
		input string
		want  bool
	}{
		{"BitnamiHelpers", true},
		{"K8sHelpers", true},
		// Wrong case
		{"bitnamiHelpers", false},
		{"bitnami-helpers", false},
		// Multi-segment
		{"aggrega/bitnami-helpers", false},
	}
	for _, tc := range cases {
		if got := re.MatchString(tc.input); got != tc.want {
			t.Errorf("MappingLibraryNameShape.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestNameShapeDeclarationsAreTextuallyExtractable guards the literal
// declaration form the cross-language pin depends on.
//
// The publishing linter does not import this package — it reads
// name_shape.go as source text, searching for the literal prefix
// "const <Name> = " followed by a backtick-quoted string. A change that
// keeps the Go code compiling can still defeat that search: grouping
// the declarations into a const block, reflowing a pattern across
// lines, or switching to double quotes each leave a compiling package
// whose patterns can no longer be found. This test applies the same
// extraction from the Go side, so such a change fails here instead of
// silently reducing the cross-runtime check to a no-op.
//
// It deliberately re-implements the search rather than sharing code
// with the extractor: the point is to detect drift in the file's
// TEXT, so a shared helper that changed alongside the file would blind
// exactly the check being made.
func TestNameShapeDeclarationsAreTextuallyExtractable(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("name_shape.go")
	if err != nil {
		t.Fatalf("read name_shape.go: %v", err)
	}
	text := string(source)

	// The seven patterns the publishing linter's symmetry test extracts,
	// paired with the compile-time constant each declaration must yield.
	cases := []struct {
		name string
		want string
	}{
		{"ComponentNameShape", ComponentNameShape},
		{"FeatureNameShape", FeatureNameShape},
		{"RecipeNameShape", RecipeNameShape},
		{"FeatureRecipeNameShape", FeatureRecipeNameShape},
		{"AggregateNameShape", AggregateNameShape},
		{"SchemaLibraryNameShape", SchemaLibraryNameShape},
		{"MappingLibraryNameShape", MappingLibraryNameShape},
	}

	for _, tc := range cases {
		needle := "const " + tc.name + " = `"
		start := strings.Index(text, needle)
		if start < 0 {
			t.Errorf("%s: declaration not extractable — %q not found in name_shape.go; "+
				"the cross-language name-shape pin reads this file textually and requires "+
				"each pattern on one line as `const <Name> = ` plus a backtick-quoted string",
				tc.name, needle)
			continue
		}
		rest := text[start+len(needle):]
		end := strings.Index(rest, "`")
		if end < 0 {
			t.Errorf("%s: no closing backtick after %q", tc.name, needle)
			continue
		}
		if got := rest[:end]; got != tc.want {
			t.Errorf("%s: extracted %q, constant is %q", tc.name, got, tc.want)
		}
	}
}
