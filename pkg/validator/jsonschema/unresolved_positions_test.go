// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// One shipped declaration, authored two ways.
//
// The first is the configuration schema exactly as it ships: its registry
// list names the shape of an element through the document's reusable
// definitions. The second alters one thing and nothing else — that shape is
// moved to a declared property, and the list's element reference is pointed
// at it there. A reference naming a definition can be stood up on its own;
// a reference naming a property cannot, because standing a position up
// carries the definitions in scope onto it and nothing else.
//
// The pairing is the whole point. A position that is checked by nothing
// reports nothing, and nothing reads exactly like a document that is fine —
// so correctness here cannot be stated as a finding count. It is stated as
// agreement with an authoring whose enforcement is not in question: the two
// documents say the same thing about a registry entry, so they must accept
// and refuse the same documents.

// registriesThroughDefinition is the configuration schema as shipped.
func registriesThroughDefinition(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var s jsonschema.Schema
	if err := json.Unmarshal([]byte(schemas.AggregaConfigV1beta1), &s); err != nil {
		t.Fatalf("decode the configuration schema: %v", err)
	}
	return &s
}

// registriesThroughProperty is the same schema with the registry-entry
// shape relocated to a declared property and the element reference
// retargeted at it. Every constraint is the one that ships; only where the
// shape is declared, and how the list reaches it, differ.
func registriesThroughProperty(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(schemas.AggregaConfigV1beta1), &document); err != nil {
		t.Fatalf("decode the configuration schema: %v", err)
	}

	definitions, ok := document["$defs"].(map[string]any)
	if !ok {
		t.Fatal("the configuration schema declares no reusable definitions")
	}
	shape, ok := definitions["RegistryConfig"]
	if !ok {
		t.Fatal("the configuration schema no longer declares a RegistryConfig definition")
	}
	delete(definitions, "RegistryConfig")

	properties, ok := document["properties"].(map[string]any)
	if !ok {
		t.Fatal("the configuration schema declares no properties")
	}
	properties["registryShape"] = shape

	registries, ok := properties["registries"].(map[string]any)
	if !ok {
		t.Fatal("the configuration schema no longer declares a registries property")
	}
	registries["items"] = map[string]any{"$ref": "#/properties/registryShape"}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("re-encode the altered schema: %v", err)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(encoded, &s); err != nil {
		t.Fatalf("decode the altered schema: %v", err)
	}
	return &s
}

// registryDocuments are documents the registry-entry shape decides, one per
// way of getting it wrong plus the one that is right.
var registryDocuments = []struct {
	name     string
	document any
	accepted bool
}{
	{
		name:     "an entry omitting the host it applies to",
		document: map[string]any{"registries": []any{map[string]any{}}},
	},
	{
		name:     "an entry carrying a key the shape does not declare",
		document: map[string]any{"registries": []any{map[string]any{"host": "example.test", "trusted_identites": []any{}}}},
	},
	{
		name:     "a host written as a number",
		document: map[string]any{"registries": []any{map[string]any{"host": 42}}},
	},
	{
		name:     "the list itself written as a string",
		document: map[string]any{"registries": "example.test"},
	},
	{
		name:     "a well-formed entry",
		document: map[string]any{"registries": []any{map[string]any{"host": "example.test", "name": "example"}}},
		accepted: true,
	},
}

// TestRegistryShapeDecidesTheSameDocumentsThroughEitherAuthoring is the
// contract: relocating a shape and pointing a reference at its new home
// changes where the shape is written and nothing about what it accepts.
func TestRegistryShapeDecidesTheSameDocumentsThroughEitherAuthoring(t *testing.T) {
	t.Parallel()

	throughDefinition, err := New(registriesThroughDefinition(t))
	if err != nil {
		t.Fatalf("build a validator for the shipped authoring: %v", err)
	}
	throughProperty, err := New(registriesThroughProperty(t))
	if err != nil {
		t.Fatalf("build a validator for the altered authoring: %v", err)
	}

	// Without this the test would pass on a schema that never exercised
	// the case at all: if the alteration stopped producing a position that
	// cannot be stood up, both authorings would agree for the ordinary
	// reason and the agreement would prove nothing.
	altered, ok := throughProperty.(*googleAdapter)
	if !ok {
		t.Fatal("expected the package's own adapter")
	}
	if len(altered.unresolved) == 0 {
		t.Fatal("the altered authoring no longer places the registry list behind a reference that cannot be stood up; the pairing proves nothing in that state")
	}

	for _, document := range registryDocuments {
		t.Run(document.name, func(t *testing.T) {
			shipped, err := throughDefinition.Validate(document.document, nil)
			if err != nil {
				t.Fatalf("shipped authoring: %v", err)
			}
			relocated, err := throughProperty.Validate(document.document, nil)
			if err != nil {
				t.Fatalf("altered authoring: %v", err)
			}

			shippedAccepts := len(shipped.Findings) == 0
			relocatedAccepts := len(relocated.Findings) == 0

			if shippedAccepts != document.accepted {
				t.Errorf("shipped authoring: accepted=%v, want %v (findings: %v)",
					shippedAccepts, document.accepted, shipped.Findings)
			}
			if relocatedAccepts != document.accepted {
				t.Errorf("altered authoring: accepted=%v, want %v (findings: %v)",
					relocatedAccepts, document.accepted, relocated.Findings)
			}
		})
	}
}

// TestPositionBehindARelocatedShapeReportsAtThatPosition pins where the
// failure is named. The path is what an editor places a marker from, so it
// is part of the contract rather than a detail of the message.
func TestPositionBehindARelocatedShapeReportsAtThatPosition(t *testing.T) {
	t.Parallel()

	v, err := New(registriesThroughProperty(t))
	if err != nil {
		t.Fatalf("build a validator: %v", err)
	}
	report, err := v.Validate(map[string]any{"registries": []any{map[string]any{}}}, nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected one finding, got %d: %v", len(report.Findings), report.Findings)
	}
	if report.Findings[0].FieldPath != "registries" {
		t.Errorf("finding named %q, want %q", report.Findings[0].FieldPath, "registries")
	}
}

// TestPositionBehindARelocatedShapeReportsIdentically pins byte-identity
// across repeated calls, which the whole-document check has to preserve
// because it is the one place the schema library's own iteration is
// reachable.
func TestPositionBehindARelocatedShapeReportsIdentically(t *testing.T) {
	t.Parallel()

	v, err := New(registriesThroughProperty(t))
	if err != nil {
		t.Fatalf("build a validator: %v", err)
	}
	document := map[string]any{"registries": []any{map[string]any{"host": 42}}}

	var first string
	for i := 0; i < 32; i++ {
		report, err := v.Validate(document, nil)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		encoded, err := json.Marshal(report.Findings)
		if err != nil {
			t.Fatalf("encode findings: %v", err)
		}
		if i == 0 {
			first = string(encoded)
			continue
		}
		if string(encoded) != first {
			t.Fatalf("call %d reported differently\n first: %s\n  this: %s", i, first, encoded)
		}
	}
}

// TestSchemaLocationsReadsTheWholeChain pins the reason the attribution
// reads every location rather than the last one: a reference moves the
// chain, so the last location names where the constraint is declared while
// an earlier one names where the value being checked sits.
func TestSchemaLocationsReadsTheWholeChain(t *testing.T) {
	t.Parallel()

	var s jsonschema.Schema
	if err := json.Unmarshal([]byte(`{
  "type": "object",
  "properties": {
    "element": { "type": "string", "minLength": 3 },
    "list": { "type": "array", "items": { "$ref": "#/properties/element" } }
  }
}`), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	failure := resolved.Validate(map[string]any{"list": []any{"ab"}})
	if failure == nil {
		t.Fatal("expected the document to be refused")
	}

	locations := schemaLocations(failure)
	if len(locations) < 3 {
		t.Fatalf("expected the whole chain, got %v", locations)
	}
	if last := locations[len(locations)-1]; last != "/properties/element" {
		t.Errorf("last location %q; the chain is expected to end at the referenced declaration", last)
	}
	if !isPointerUnder(locations[len(locations)-2], "/properties/list") {
		t.Errorf("the location before the reference (%q) is expected to sit under the position that carries it",
			locations[len(locations)-2])
	}
}

// TestReportPrecisionDiffersBetweenTheTwoReferenceShapes pins the
// difference the language reference teaches an author to expect: the same
// constraint reached through a reusable definition names the value at
// fault, and reached through a pointer into a declared property names the
// field carrying the pointer. Both refuse the document; only the precision
// differs, and the path is what an editor places its marker from.
func TestReportPrecisionDiffersBetweenTheTwoReferenceShapes(t *testing.T) {
	t.Parallel()

	var s jsonschema.Schema
	if err := json.Unmarshal([]byte(`{
  "type": "object",
  "properties": {
    "primary": {
      "type": "object",
      "properties": {
        "resources": {
          "type": "object",
          "properties": {
            "requests": {
              "type": "object",
              "properties": { "cpu": { "type": "string", "pattern": "^[0-9]+(m|\\.[0-9]+)?$" } }
            }
          }
        }
      }
    },
    "secondary": {
      "type": "object",
      "properties": {
        "resources": { "$ref": "#/properties/primary/properties/resources" }
      }
    }
  }
}`), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	v, err := New(&s)
	if err != nil {
		t.Fatalf("build a validator: %v", err)
	}

	badValue := map[string]any{"requests": map[string]any{"cpu": "!!"}}
	goodValue := map[string]any{"requests": map[string]any{"cpu": "500m"}}

	for _, expected := range []struct {
		name     string
		document any
		path     string
	}{
		{"reached directly", map[string]any{"primary": map[string]any{"resources": badValue}}, "primary.resources.requests.cpu"},
		{"reached through a pointer into a property", map[string]any{"secondary": map[string]any{"resources": badValue}}, "secondary.resources"},
	} {
		report, err := v.Validate(expected.document, nil)
		if err != nil {
			t.Fatalf("%s: %v", expected.name, err)
		}
		if len(report.Findings) != 1 {
			t.Fatalf("%s: expected one finding, got %d: %v", expected.name, len(report.Findings), report.Findings)
		}
		if report.Findings[0].FieldPath != expected.path {
			t.Errorf("%s: named %q, want %q", expected.name, report.Findings[0].FieldPath, expected.path)
		}
	}

	report, err := v.Validate(map[string]any{"secondary": map[string]any{"resources": goodValue}}, nil)
	if err != nil {
		t.Fatalf("a well-formed document: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("a well-formed document was refused: %v", report.Findings)
	}
}

// TestNoShippedSchemaCarriesAPositionThatCannotBeStoodUp pins the coverage
// contract at zero for every schema the product validates against, so a
// schema starting to rely on the whole-document fallback surfaces here
// rather than as a quietly coarser diagnostic.
func TestNoShippedSchemaCarriesAPositionThatCannotBeStoodUp(t *testing.T) {
	t.Parallel()

	corpus := map[string]string{
		"AggregaConfig":   schemas.AggregaConfigV1beta1,
		"Aggregate":       schemas.AggregateV1beta1,
		"ArtifactLibrary": schemas.ArtifactLibraryV1beta1,
		"CacheMetadata":   schemas.CacheMetadataV1beta1,
		"Component":       schemas.ComponentV1beta1,
		"Feature":         schemas.FeatureV1beta1,
		"FeatureRecipe":   schemas.FeatureRecipeV1beta1,
		"IR":              schemas.IRSchemaV1beta1,
		"Lockfile":        schemas.LockfileV1beta1,
		"Manifest":        schemas.ManifestV1beta1,
		"MappingLibrary":  schemas.MappingLibraryV1beta1,
		"Plugin":          schemas.PluginV1beta1,
		"Recipe":          schemas.RecipeV1beta1,
		"Release":         schemas.ReleaseV1beta1,
		"SchemaLibrary":   schemas.SchemaLibraryV1beta1,
		"Workload":        schemas.WorkloadV1beta1,
		"WorkloadLibrary": schemas.WorkloadLibraryV1beta1,
	}
	for kind, source := range corpus {
		var s jsonschema.Schema
		if err := json.Unmarshal([]byte(source), &s); err != nil {
			t.Errorf("%s: decode: %v", kind, err)
			continue
		}
		v, err := New(&s)
		if err != nil {
			t.Errorf("%s: build a validator: %v", kind, err)
			continue
		}
		a, ok := v.(*googleAdapter)
		if !ok {
			t.Fatal("expected the package's own adapter")
		}
		for _, position := range a.unresolved {
			t.Errorf("%s: position %q (at %s) cannot be stood up on its own",
				kind, position.instancePath, position.pointer)
		}
	}
}
