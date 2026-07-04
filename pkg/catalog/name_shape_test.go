// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the regex source-of-truth in name_shape.go. Two surfaces:
//
//  1. Per-pattern table-driven tests with at least one positive and
//     one negative fixture per pattern. Positive fixtures for the
//     per-entity-kind patterns come from the hub catalog (the
//     empirical source-of-truth for what catalog names exist in
//     practice). Negative fixtures are author-constructed.
//
//  2. A Go-runtime parity test asserting each exported string constant
//     recompiles to a *regexp.Regexp with identical match results to
//     the package's pre-compiled *Re var. This proves string-constant
//     stability inside the Go runtime; cross-runtime parity with the
//     publishing-linter Wasm plugin's regex engine is exercised by the
//     plugin's own test surface, not here.

package catalog

import (
	"regexp"
	"testing"
)

func TestReservedAggregaAIPrefix_MatchesBarePrefix(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"aggrega.ai/namespace", true},
		{"aggrega.ai/compiler-version", true},
		{"feature.aggrega.ai/kind", false},
		{"acme.com/aggrega.ai/foo", false},
		{"component.aggrega.ai/role", false},
		{"my-custom/key", false},
	}
	for _, tc := range cases {
		if got := ReservedAggregaAIPrefixRe.MatchString(tc.input); got != tc.want {
			t.Errorf("ReservedAggregaAIPrefixRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestReservedFeatureAggregaAIPrefix_MatchesFeatureSubPrefix(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"feature.aggrega.ai/kind", true},
		{"feature.aggrega.ai/role", true},
		{"aggrega.ai/namespace", false},
		{"component.aggrega.ai/role", false},
		{"acme.com/feature.aggrega.ai/foo", false},
	}
	for _, tc := range cases {
		if got := ReservedFeatureAggregaAIPrefixRe.MatchString(tc.input); got != tc.want {
			t.Errorf("ReservedFeatureAggregaAIPrefixRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestComponentNameShape_MatchesThreeKebabSegments(t *testing.T) {
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
		if got := ComponentNameShapeRe.MatchString(tc.input); got != tc.want {
			t.Errorf("ComponentNameShapeRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestFeatureNameShape_MatchesTwoKebabSegments(t *testing.T) {
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
		if got := FeatureNameShapeRe.MatchString(tc.input); got != tc.want {
			t.Errorf("FeatureNameShapeRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestRecipeNameShape_MatchesSingleKebabSegment(t *testing.T) {
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
		if got := RecipeNameShapeRe.MatchString(tc.input); got != tc.want {
			t.Errorf("RecipeNameShapeRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestFeatureRecipeNameShape_MatchesSingleKebabSegment(t *testing.T) {
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
		if got := FeatureRecipeNameShapeRe.MatchString(tc.input); got != tc.want {
			t.Errorf("FeatureRecipeNameShapeRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestAggregateNameShape_MatchesTwoKebabSegments(t *testing.T) {
	// No hub fixtures available; pattern matches the entity-reference
	// shape pending verification against the first published Aggregate.
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
		if got := AggregateNameShapeRe.MatchString(tc.input); got != tc.want {
			t.Errorf("AggregateNameShapeRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestSchemaLibraryNameShape_MatchesPascalCase(t *testing.T) {
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
		if got := SchemaLibraryNameShapeRe.MatchString(tc.input); got != tc.want {
			t.Errorf("SchemaLibraryNameShapeRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestMappingLibraryNameShape_MatchesPascalCase(t *testing.T) {
	// No hub fixtures available; pattern matches the JSON Schema
	// envelope at mappinglibrary-v1alpha1.schema.json.
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
		if got := MappingLibraryNameShapeRe.MatchString(tc.input); got != tc.want {
			t.Errorf("MappingLibraryNameShapeRe.MatchString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestExportedStringConstants_GoRuntimeParity asserts that each
// exported string constant, when re-compiled via regexp.MustCompile in
// the Go runtime, produces identical match results to the package's
// pre-compiled *Re var across a shared fixture matrix. This catches
// drift between the two forms (e.g., a string constant edited without
// also updating the compiled var, or vice versa).
//
// This test proves Go-runtime parity ONLY. Cross-runtime parity with
// the publishing-linter Wasm plugin's regex engine is a separate test
// surface that the plugin ships with its own implementation.
func TestExportedStringConstants_GoRuntimeParity(t *testing.T) {
	type entry struct {
		name     string
		source   string
		compiled *regexp.Regexp
	}
	entries := []entry{
		{"ReservedAggregaAIPrefix", ReservedAggregaAIPrefix, ReservedAggregaAIPrefixRe},
		{"ReservedFeatureAggregaAIPrefix", ReservedFeatureAggregaAIPrefix, ReservedFeatureAggregaAIPrefixRe},
		{"ComponentNameShape", ComponentNameShape, ComponentNameShapeRe},
		{"FeatureNameShape", FeatureNameShape, FeatureNameShapeRe},
		{"RecipeNameShape", RecipeNameShape, RecipeNameShapeRe},
		{"FeatureRecipeNameShape", FeatureRecipeNameShape, FeatureRecipeNameShapeRe},
		{"AggregateNameShape", AggregateNameShape, AggregateNameShapeRe},
		{"SchemaLibraryNameShape", SchemaLibraryNameShape, SchemaLibraryNameShapeRe},
		{"MappingLibraryNameShape", MappingLibraryNameShape, MappingLibraryNameShapeRe},
	}

	// Fixture matrix: a heterogeneous mix of inputs exercising every
	// pattern's positive and negative space. Each pattern decides
	// independently whether to match.
	fixtures := []string{
		"aggrega.ai/namespace",
		"feature.aggrega.ai/kind",
		"core/datastore/mariadb",
		"core/cryptographic-signature",
		"mariadb-bitnami",
		"dotenv-secret",
		"helm",
		"helm-chart",
		"core/db-module",
		"BaseComponents",
		"AIDatastores",
		"BitnamiHelpers",
		"baseComponents",
		"core/Database/mariadb",
		"mariadb_v2",
		"",
		"core//mariadb",
		"aggrega/datastores",
	}

	for _, e := range entries {
		t.Run(e.name, func(t *testing.T) {
			recompiled := regexp.MustCompile(e.source)
			for _, fixture := range fixtures {
				want := e.compiled.MatchString(fixture)
				got := recompiled.MatchString(fixture)
				if got != want {
					t.Errorf("%s: parity break on input %q — compiled=%v, recompiled=%v",
						e.name, fixture, want, got)
				}
			}
		})
	}
}
