// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package completion

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// --- Per-context unit tests ---

func TestCompletionKindReturnsAllEntityKinds(t *testing.T) {
	t.Parallel()
	items := Kind()
	if len(items) != 10 {
		t.Fatalf("Kind() returned %d items; want 10 entity kinds", len(items))
	}
	// First item should be the authoring-progression's natural starting point: Aggregate.
	if items[0].Label != "Aggregate" {
		t.Errorf("first item = %q; want Aggregate (Operator-layer authoring entry point)", items[0].Label)
	}
	// All 10 kinds should be present.
	got := map[string]bool{}
	for _, item := range items {
		got[item.Label] = true
	}
	for _, want := range []string{"Aggregate", "Manifest", "Release", "Component", "Feature", "Recipe", "FeatureRecipe", "Provider", "SchemaLibrary", "MappingLibrary"} {
		if !got[want] {
			t.Errorf("kind %q missing from Kind() output", want)
		}
	}
}

func TestCompletionApiVersionReturnsV1Alpha1(t *testing.T) {
	t.Parallel()
	items := APIVersion()
	if len(items) != 1 {
		t.Fatalf("APIVersion() returned %d items; want 1", len(items))
	}
	if items[0].Label != "aggrega.ai/v1alpha1" {
		t.Errorf("apiVersion label = %q; want aggrega.ai/v1alpha1", items[0].Label)
	}
}

func TestCompletionExtendsEnumeratesSameKindURIs(t *testing.T) {
	t.Parallel()
	uris := []string{"core/server", "core/web-server", "app/api"}
	items := Extends(uris, "")
	if len(items) != 3 {
		t.Fatalf("Extends() returned %d items; want 3", len(items))
	}
	if items[0].Label != "app/api" {
		t.Errorf("first item = %q; want alphabetically-first URI", items[0].Label)
	}
}

func TestCompletionExtendsAppliesPrefixNarrowing(t *testing.T) {
	t.Parallel()
	uris := []string{"core/server", "core/web-server", "app/api"}
	items := Extends(uris, "core/")
	if len(items) != 2 {
		t.Fatalf("Extends() with prefix 'core/' returned %d items; want 2", len(items))
	}
	for _, item := range items {
		if !strings.HasPrefix(item.Label, "core/") {
			t.Errorf("item %q escaped the prefix filter", item.Label)
		}
	}
}

func TestCompletionComponentsTypeEnumeratesComponentURIs(t *testing.T) {
	t.Parallel()
	uris := []string{"core/postgres", "core/redis"}
	items := ComponentsType(uris, "")
	if len(items) != 2 || items[0].Label != "core/postgres" {
		t.Fatalf("ComponentsType output = %+v; want sorted [core/postgres, core/redis]", items)
	}
}

func TestCompletionFeaturesTypeEnumeratesFeatureURIs(t *testing.T) {
	t.Parallel()
	uris := []string{"feature/autoscaler", "feature/replication"}
	items := FeaturesType(uris, "")
	if len(items) != 2 {
		t.Fatalf("FeaturesType returned %d items; want 2", len(items))
	}
}

func TestCompletionImplementsEnumeratesGivenKindURIs(t *testing.T) {
	t.Parallel()
	uris := []string{"core/api", "core/server"}
	items := Implements(uris, "")
	if len(items) != 2 {
		t.Fatalf("Implements returned %d items; want 2", len(items))
	}
}

func TestCompletionProviderEnumeratesProviderURIs(t *testing.T) {
	t.Parallel()
	uris := []string{"helm-provider", "compose-provider"}
	items := Provider(uris, "")
	if len(items) != 2 {
		t.Fatalf("Provider returned %d items; want 2", len(items))
	}
}

func TestCompletionLabelCategoryReturnsEmptyAtThisWI(t *testing.T) {
	t.Parallel()
	items := LabelValue("component.aggrega.ai/category")
	if len(items) != 0 {
		t.Errorf("LabelValue(category) = %+v; want empty (table deferred-empty at this WI)", items)
	}
}

func TestCompletionLabelUnknownLabelReturnsNil(t *testing.T) {
	t.Parallel()
	items := LabelValue("custom.example.com/whatever")
	if items != nil {
		t.Errorf("LabelValue(unknown label) = %+v; want nil", items)
	}
}

func TestCompletionSchemaKeywordsReturnsVanillaSet(t *testing.T) {
	t.Parallel()
	items := SchemaKeyword()
	vanillaCount := 0
	for _, item := range items {
		if !strings.HasPrefix(item.Label, "x-aggrega-") {
			vanillaCount++
		}
	}
	if vanillaCount != len(JSONSchemaKeywords) {
		t.Errorf("vanilla keyword count = %d; want %d", vanillaCount, len(JSONSchemaKeywords))
	}
	// Spot-check a few canonical keywords.
	got := map[string]bool{}
	for _, item := range items {
		got[item.Label] = true
	}
	for _, want := range []string{"type", "properties", "required", "$ref", "allOf"} {
		if !got[want] {
			t.Errorf("vanilla keyword %q missing", want)
		}
	}
}

func TestCompletionSchemaKeywordsIncludesAggregaExtensions(t *testing.T) {
	t.Parallel()
	items := SchemaKeyword()
	got := map[string]bool{}
	for _, item := range items {
		got[item.Label] = true
	}
	for _, want := range []string{"x-aggrega-selector", "x-aggrega-inject", "x-aggrega-validate"} {
		if !got[want] {
			t.Errorf("aggrega extension %q missing from schema-keyword candidates", want)
		}
	}
}

func TestCompletionSchemaKeywordsExtensionsSortAfterVanilla(t *testing.T) {
	t.Parallel()
	items := SchemaKeyword()
	// All x-aggrega-* sortTexts should start with "z " so they sort
	// after vanilla keywords in the client's lexicographic order.
	for _, item := range items {
		if strings.HasPrefix(item.Label, "x-aggrega-") {
			if !strings.HasPrefix(item.SortText, "z ") {
				t.Errorf("extension %q sortText = %q; want prefix 'z '", item.Label, item.SortText)
			}
		}
	}
}

func TestCompletionManifestRecipeReferenceEnumeratesRecipeURIs(t *testing.T) {
	t.Parallel()
	uris := []string{"recipes/helm-server", "recipes/compose-worker"}
	items := ManifestRecipeReference(uris, "")
	if len(items) != 2 {
		t.Fatalf("ManifestRecipeReference returned %d items; want 2", len(items))
	}
}

func TestCompletionReleaseAggregatePathEnumeratesAggregatePaths(t *testing.T) {
	t.Parallel()
	paths := []string{"app", "alt-app"}
	items := ReleaseAggregatePath(paths, "")
	if len(items) != 2 {
		t.Fatalf("ReleaseAggregatePath returned %d items; want 2", len(items))
	}
	if items[0].Label != "alt-app" {
		t.Errorf("first item = %q; want alphabetically-first path", items[0].Label)
	}
}

func TestCompletionReleaseManifestPathEnumeratesManifestPaths(t *testing.T) {
	t.Parallel()
	paths := []string{"prod-manifest", "dev-manifest"}
	items := ReleaseManifestPath(paths, "")
	if len(items) != 2 {
		t.Fatalf("ReleaseManifestPath returned %d items; want 2", len(items))
	}
}

func TestCompletionUsesTargetEnumeratesLocalSiblings(t *testing.T) {
	t.Parallel()
	names := []string{"db", "cache", "api"}
	items := UsesTarget(names, "")
	if len(items) != 3 {
		t.Fatalf("UsesTarget returned %d items; want 3", len(items))
	}
	if items[0].Label != "api" {
		t.Errorf("first item = %q; want alphabetically-first key", items[0].Label)
	}
}

// --- Intra-document walker test ---

func TestEnumerateLocalComponentKeysWalksSubsystems(t *testing.T) {
	t.Parallel()
	src := "spec:\n  components:\n    api:\n      type: core/api\n    db:\n      type: core/db\n  subsystems:\n    data:\n      components:\n        cache:\n          type: core/redis\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	keys := EnumerateLocalComponentKeys(&doc)
	if len(keys) != 3 {
		t.Fatalf("EnumerateLocalComponentKeys returned %d keys; want 3 (api, db, cache)", len(keys))
	}
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	for _, want := range []string{"api", "db", "cache"} {
		if !got[want] {
			t.Errorf("key %q missing from enumeration", want)
		}
	}
}

// --- Classifier tests ---

func TestClassifyForCompletionRecognizesKind(t *testing.T) {
	t.Parallel()
	got := ClassifyForCompletion([]string{"kind"}, yamlpos.ContextSideValue)
	if got != ContextKindKind {
		t.Errorf("ClassifyForCompletion([kind]) = %d; want ContextKindKind", got)
	}
}

func TestClassifyForCompletionRecognizesAPIVersion(t *testing.T) {
	t.Parallel()
	got := ClassifyForCompletion([]string{"apiVersion"}, yamlpos.ContextSideValue)
	if got != ContextKindAPIVersion {
		t.Errorf("ClassifyForCompletion([apiVersion]) = %d; want ContextKindAPIVersion", got)
	}
}

func TestClassifyForCompletionRecognizesLabelValue(t *testing.T) {
	t.Parallel()
	got := ClassifyForCompletion([]string{"metadata", "labels", "component.aggrega.ai/category"}, yamlpos.ContextSideValue)
	if got != ContextKindLabelValue {
		t.Errorf("ClassifyForCompletion([metadata, labels, category]) = %d; want ContextKindLabelValue", got)
	}
}

func TestClassifyForCompletionRejectsLabelKey(t *testing.T) {
	t.Parallel()
	got := ClassifyForCompletion([]string{"metadata", "labels", "component.aggrega.ai/category"}, yamlpos.ContextSideKey)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(Side=Key) = %d; want Unknown (key-side cursor on label name is deferred)", got)
	}
}

func TestClassifyForCompletionRecognizesProvider(t *testing.T) {
	t.Parallel()
	got := ClassifyForCompletion([]string{"spec", "provider"}, yamlpos.ContextSideValue)
	if got != ContextKindProvider {
		t.Errorf("ClassifyForCompletion([spec, provider]) = %d; want ContextKindProvider", got)
	}
}

func TestClassifyForCompletionRecognizesSchemaKeyword(t *testing.T) {
	t.Parallel()
	got := ClassifyForCompletion([]string{"spec", "schema", "properties"}, yamlpos.ContextSideKey)
	if got != ContextKindSchemaKeyword {
		t.Errorf("ClassifyForCompletion([spec, schema, properties]) = %d; want ContextKindSchemaKeyword", got)
	}
}

func TestClassifyForCompletionRecognizesSchemaTypeValue(t *testing.T) {
	t.Parallel()
	// Cursor on the VALUE side of a `type:` key inside spec.schema.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "port", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindSchemaTypeValue {
		t.Errorf("ClassifyForCompletion(value-side of type) = %d; want ContextKindSchemaTypeValue", got)
	}
}

func TestClassifyForCompletionRecognizesTopLevelSchemaTypeValue(t *testing.T) {
	t.Parallel()
	// Cursor on the VALUE side of spec.schema.type: (no nesting).
	got := ClassifyForCompletion([]string{"spec", "schema", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindSchemaTypeValue {
		t.Errorf("ClassifyForCompletion([spec, schema, type] value) = %d; want ContextKindSchemaTypeValue", got)
	}
}

func TestClassifyForCompletionSchemaValueOfNonTypeKeyReturnsUnknown(t *testing.T) {
	t.Parallel()
	// Value-side cursor on description: <here> — schema-value
	// completion is deferred for non-type keys; returns Unknown so
	// the dispatcher emits an empty candidate list.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "port", "description"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(value-side of non-type schema key) = %d; want Unknown (deferred for non-type values)", got)
	}
}

func TestClassifyForCompletionSchemaTypeUserNamedPropertyReturnsUnknown(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, properties, type] — user named the
	// property "type". Cursor on value-side. The parent-keyword
	// check (typeKeyIsUserNamedProperty) sees "properties" as the
	// segment before "type" and returns Unknown — primitives are
	// semantically wrong because the operator's value here is a
	// sub-schema mapping, not a primitive type name.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(user-named property 'type') = %d; want Unknown (primitives would be semantically wrong)", got)
	}
}

func TestClassifyForCompletionSchemaTypeUserNamedUnderDefsReturnsUnknown(t *testing.T) {
	t.Parallel()
	// $defs is a JSON Schema container keyword like properties.
	got := ClassifyForCompletion([]string{"spec", "schema", "$defs", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion($defs.type as user-named) = %d; want Unknown", got)
	}
}

func TestClassifyForCompletionSchemaTypeUserNamedUnderDefinitionsReturnsUnknown(t *testing.T) {
	t.Parallel()
	// definitions is the JSON Schema Draft-07 container keyword
	// (still accepted in 2020-12 for backward compat).
	got := ClassifyForCompletion([]string{"spec", "schema", "definitions", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(definitions.type as user-named) = %d; want Unknown", got)
	}
}

func TestClassifyForCompletionSchemaTypeUserNamedUnderPatternPropertiesReturnsUnknown(t *testing.T) {
	t.Parallel()
	// patternProperties is a JSON Schema container keyword whose
	// children are user-named regex-key properties.
	got := ClassifyForCompletion([]string{"spec", "schema", "patternProperties", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(patternProperties.type as user-named) = %d; want Unknown", got)
	}
}

func TestClassifyForCompletionSchemaTypeNestedUnderUserPropertyRecognized(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, properties, port, type] — `port` is a
	// user-named property; `type:` here IS the JSON Schema type
	// keyword. Parent of "type" is "port" (not a container keyword).
	// Primitives correct.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "port", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindSchemaTypeValue {
		t.Errorf("ClassifyForCompletion(properties.port.type) = %d; want ContextKindSchemaTypeValue", got)
	}
}

func TestClassifyForCompletionSchemaTypeAsArrayElementRecognized(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, properties, port, type, 0] — array-form
	// type. Cursor on the value of the first array element.
	// Primitives correct (the JSON Schema array-form takes the same
	// primitive type names).
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "port", "type", "0"}, yamlpos.ContextSideValue)
	if got != ContextKindSchemaTypeValue {
		t.Errorf("ClassifyForCompletion(array-form type element 0) = %d; want ContextKindSchemaTypeValue", got)
	}
}

func TestClassifyForCompletionSchemaTypeAsArrayUserNamedPropertyReturnsUnknown(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, properties, type, 0] — user named the
	// property "type"; the value happens to be a sequence. The
	// parent-keyword check sees "properties" before "type" — return
	// Unknown.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "type", "0"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(user-named 'type' as array) = %d; want Unknown", got)
	}
}

func TestClassifyForCompletionSchemaTypeArrayAtTopLevelRecognized(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, type, 0] — top-level schema with type as
	// an array. Parent of "type" is "schema" (not a container
	// keyword). Primitives correct.
	got := ClassifyForCompletion([]string{"spec", "schema", "type", "0"}, yamlpos.ContextSideValue)
	if got != ContextKindSchemaTypeValue {
		t.Errorf("ClassifyForCompletion(top-level schema.type as array) = %d; want ContextKindSchemaTypeValue", got)
	}
}

func TestClassifyForCompletionUserPropertyNamedPropertiesRecognized(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, properties, properties, type] — the inner
	// `properties` is a USER-NAMED property whose name collides with
	// the JSON Schema container keyword. The recursive container
	// check (isActingAsContainer) sees:
	//   - idx 1 ("properties" under schema) → IS container
	//   - idx 2 ("properties" under container at idx 1) → NOT container (user-named)
	//   - parent of type at idx 2 → not acting as container → primitives correct.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "properties", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindSchemaTypeValue {
		t.Errorf("ClassifyForCompletion(user-named property called 'properties') = %d; want ContextKindSchemaTypeValue (the type: here IS the JSON Schema keyword of the user-named property)", got)
	}
}

func TestClassifyForCompletionTripleNestedPropertiesUserNamedReturnsUnknown(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, properties, properties, properties, type]
	// The chain alternates: outer properties (container) → inner
	// properties (user-named) → innermost properties (container of
	// the user-named property's sub-schema) → type (user-named under
	// the innermost container). Primitives wrong.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "properties", "properties", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(triple-nested properties.type as user-named) = %d; want Unknown", got)
	}
}

func TestClassifyForCompletionUserPropertyNamedDefsRecognized(t *testing.T) {
	t.Parallel()
	// Path: [spec, schema, properties, $defs, type] — user named a
	// property "$defs" (collides with the reserved keyword). The
	// outer properties acts as container; $defs at idx 2 does NOT
	// (its parent IS a container). type at idx 3 is the JSON Schema
	// keyword of the user-named "$defs" property.
	got := ClassifyForCompletion([]string{"spec", "schema", "properties", "$defs", "type"}, yamlpos.ContextSideValue)
	if got != ContextKindSchemaTypeValue {
		t.Errorf("ClassifyForCompletion(user-named property called '$defs') = %d; want ContextKindSchemaTypeValue", got)
	}
}

func TestCompletionSchemaTypeValueReturnsPrimitives(t *testing.T) {
	t.Parallel()
	items := SchemaTypeValue()
	if len(items) != 7 {
		t.Fatalf("SchemaTypeValue() returned %d items; want 7 primitive types", len(items))
	}
	// object should come first per the authored order.
	if items[0].Label != "object" {
		t.Errorf("first primitive = %q; want object (most common)", items[0].Label)
	}
	// All 7 JSON Schema primitives should be present.
	got := map[string]bool{}
	for _, item := range items {
		got[item.Label] = true
	}
	for _, want := range []string{"object", "string", "integer", "number", "boolean", "array", "null"} {
		if !got[want] {
			t.Errorf("primitive %q missing from SchemaTypeValue() output", want)
		}
	}
}

func TestSchemaTypePrimitivesTableContainsExpectedSeven(t *testing.T) {
	t.Parallel()
	if len(SchemaTypePrimitives) != 7 {
		t.Errorf("SchemaTypePrimitives has %d entries; want 7", len(SchemaTypePrimitives))
	}
}

func TestClassifyForCompletionReturnsUnknownForUnrecognized(t *testing.T) {
	t.Parallel()
	got := ClassifyForCompletion([]string{"metadata", "version"}, yamlpos.ContextSideValue)
	if got != ContextKindUnknown {
		t.Errorf("ClassifyForCompletion(unrecognized) = %d; want Unknown", got)
	}
}

// --- Closed-set table verification ---

func TestEntityKindsTableContainsExpectedTen(t *testing.T) {
	t.Parallel()
	if len(EntityKinds) != 10 {
		t.Errorf("EntityKinds has %d entries; want 10", len(EntityKinds))
	}
}

func TestJSONSchemaKeywordsTableCoversCoreVocabulary(t *testing.T) {
	t.Parallel()
	got := map[string]bool{}
	for _, k := range JSONSchemaKeywords {
		got[k] = true
	}
	// Spot-check core vocabulary.
	for _, want := range []string{"$ref", "type", "properties", "required", "items", "enum", "allOf", "anyOf", "oneOf"} {
		if !got[want] {
			t.Errorf("vanilla keyword %q missing from JSONSchemaKeywords", want)
		}
	}
}

func TestAggregaSchemaExtensionsTableContainsKnownExtensions(t *testing.T) {
	t.Parallel()
	got := map[string]bool{}
	for _, ext := range AggregaSchemaExtensions {
		got[ext] = true
	}
	for _, want := range []string{"x-aggrega-selector", "x-aggrega-inject", "x-aggrega-narrowed-from", "x-aggrega-validate", "x-aggrega-source-chain"} {
		if !got[want] {
			t.Errorf("aggrega extension %q missing from AggregaSchemaExtensions", want)
		}
	}
}
