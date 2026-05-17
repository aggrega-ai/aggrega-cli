// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/compactform"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ValidateAnnotationBindingTargets verifies that every path declared in
// the manifest's annotation bindings corresponds to a property the
// target Feature's spec.schema actually declares at the named pillar.
//
// For each binding in the manifest's
// `mappings.features.annotations` map, the validator:
//
//  1. Resolves the target Feature via catalogLookup. A binding pointing
//     at a Feature type the catalog does not carry returns a CATALOG_ERROR
//     naming the binding key and the unknown Feature type.
//  2. Compiles the Feature's spec.schema once via compactform.Compile to
//     obtain the resolved JSON Schema 2020-12 form. Authors may declare
//     spec.schema in compact form, JSON Schema YAML, or a mix; the
//     compile step normalises both to a uniform property tree.
//  3. For each path in binding.Maps.Infra, walks the compiled schema's
//     `properties.infra.properties.<segment>...` chain; raises an
//     OPS_ERROR when any segment is absent.
//  4. For each path in binding.Maps.Values, symmetrically walks
//     `properties.values.properties.<segment>...`.
//
// Schema shapes that escape simple property-walk — $ref nodes,
// patternProperties, oneOf/anyOf/allOf composition, additionalProperties
// with a schema body — return a "skippable" signal from the walker.
// The validator treats skippable as a soft no-op for that binding-path
// pair and trusts downstream schema validation to catch any mismatch.
// The discipline is default-to-skip: only halt when the walker is
// certain the path is missing.
//
// Returns nil when every binding's declared paths resolve (or skip
// softly) against the target Feature's schema.
//
// The validator is the static cross-reference check between the manifest
// authoring surface and the catalog's Feature schemas. It runs from the
// orchestrator after Resolve but before ExpandAnnotations so that a
// malformed binding surfaces with a clear "binding routes to nonexistent
// path" error before the synthesized Feature reaches downstream schema
// validation and produces a less informative "unknown property" error.
func ValidateAnnotationBindingTargets(manifest *types.Manifest, catalogLookup CatalogLookup) error {
	if manifest == nil {
		return nil
	}
	if manifest.Spec.Mappings.Features == nil || manifest.Spec.Mappings.Features.Annotations == nil {
		return nil
	}
	if catalogLookup.Feature == nil {
		return nil
	}

	for annotationKey, binding := range manifest.Spec.Mappings.Features.Annotations.All() {
		feature, found := catalogLookup.Feature(binding.Feature)
		if !found {
			return fmt.Errorf(
				"CATALOG_ERROR: annotation binding %q targets Feature %q, but no such Feature is loaded in the catalog",
				annotationKey, binding.Feature,
			)
		}

		compiled, err := compactform.Compile(&feature.Spec.Schema)
		if err != nil {
			// A Feature schema that fails to compile is a catalog-load
			// error caught upstream; if it reaches here, return a wrapping
			// CATALOG_ERROR so the operator can locate the offending
			// Feature.
			return fmt.Errorf(
				"CATALOG_ERROR: annotation binding %q targets Feature %q whose spec.schema does not compile: %w",
				annotationKey, binding.Feature, err,
			)
		}

		for _, path := range binding.Maps.Infra {
			ok, skippable := schemaHasPropertyAtPath(compiled, "infra", path)
			if skippable {
				continue
			}
			if !ok {
				return fmt.Errorf(
					"OPS_ERROR: annotation binding %q routes through pillar %q at path %q on Feature %q, but the Feature's spec.schema does not declare a property at that path",
					annotationKey, "infra", path, binding.Feature,
				)
			}
		}
		for _, path := range binding.Maps.Values {
			ok, skippable := schemaHasPropertyAtPath(compiled, "values", path)
			if skippable {
				continue
			}
			if !ok {
				return fmt.Errorf(
					"OPS_ERROR: annotation binding %q routes through pillar %q at path %q on Feature %q, but the Feature's spec.schema does not declare a property at that path",
					annotationKey, "values", path, binding.Feature,
				)
			}
		}
	}
	return nil
}

// schemaHasPropertyAtPath walks a compiled JSON Schema 2020-12 schema
// along the dotted-path segments under the named pillar, checking that
// each segment exists in an explicit properties map at the current
// level. Returns (ok, skippable):
//
//   - ok=true, skippable=false: the path resolves cleanly through
//     explicit `properties` maps at every segment.
//   - ok=false, skippable=false: the walker reached a `properties`
//     node and a named segment was not listed. The validator halts
//     for this case.
//   - skippable=true: the walker encountered a schema shape it cannot
//     statically resolve ($ref, patternProperties, additionalProperties
//     with a schema body, oneOf/anyOf/allOf). The validator treats this
//     as a soft no-op and trusts downstream validation; the path may
//     or may not be valid under the escaping shape, and false-positive
//     halts are worse than false-negative passes.
//
// The discipline is default-to-skip: any uncertainty at any segment
// flips skippable true. Only the explicit "encountered properties map,
// segment not listed, no escape hatches at this level" path produces a
// halt.
func schemaHasPropertyAtPath(schema compactform.Schema, pillar, path string) (ok, skippable bool) {
	if schema == nil {
		return false, true
	}
	pillarNode, foundPillar := propertyOf(schema, pillar)
	if !foundPillar {
		return false, true
	}
	current, currentSkippable := pillarNode.(map[string]any)
	if !currentSkippable {
		return false, true
	}
	if path == "" {
		return true, false
	}
	for _, seg := range strings.Split(path, ".") {
		if hasEscapeHatch(current) {
			return false, true
		}
		next, found := propertyOf(current, seg)
		if !found {
			return false, false
		}
		nextMap, isMap := next.(map[string]any)
		if !isMap {
			return false, true
		}
		current = nextMap
	}
	return true, false
}

// propertyOf returns the `properties.<name>` entry from a JSON Schema
// 2020-12 node, plus a found flag. Returns false when the node does not
// carry a `properties` map at all or when the named property is absent
// from the map.
func propertyOf(node map[string]any, name string) (any, bool) {
	propsRaw, has := node["properties"]
	if !has {
		return nil, false
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	val, found := props[name]
	return val, found
}

// hasEscapeHatch reports whether a JSON Schema node carries any
// composition or reference clause that escapes simple property-walk.
// The walker flips skippable when it encounters such a clause before
// the path has fully resolved.
func hasEscapeHatch(node map[string]any) bool {
	if _, has := node["$ref"]; has {
		return true
	}
	if _, has := node["patternProperties"]; has {
		return true
	}
	if _, has := node["oneOf"]; has {
		return true
	}
	if _, has := node["anyOf"]; has {
		return true
	}
	if _, has := node["allOf"]; has {
		return true
	}
	if ap, has := node["additionalProperties"]; has {
		// additionalProperties may be a boolean (true/false) or a
		// schema body. A schema body is the escape — any property
		// is potentially declared. A boolean false is a no-escape
		// (only listed properties are valid); boolean true is an
		// escape (any property is allowed).
		switch v := ap.(type) {
		case bool:
			if v {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// validateSubkeyValueAgainstFeatureSchema compiles the target Feature's
// spec.schema and verifies that a subkey value's runtime Go type
// matches the schema's declared type at the routed pillar+path.
// Annotation values arrive from YAML decode as primitives (string,
// int, float64, bool) or composite types (map, slice); the
// validator checks each against the schema's `type` field.
//
// Performance note: compiles feature.Spec.Schema on every call.
// Resolver-context-level caching (one compile per Feature per build)
// is a future optimization deferred until profiling shows expansion-
// time growth; the godoc records the deferral so the optimization
// opportunity stays visible. The test-report.md notes any test
// scenario that surfaces noticeable expansion-time growth so the
// deferral can be revisited promptly.
//
// Returns nil when the value's type matches the schema declaration,
// when the schema-walker reaches an escape-hatch branch ($ref,
// patternProperties, oneOf/anyOf/allOf, schema-bodied
// additionalProperties — soft-skip per the default-to-skip
// discipline established for the path-target validator), or when the
// schema's declared property carries no `type` field. Returns an
// OPS_ERROR-prefixed message naming the subkey, the actual Go type,
// and the schema-declared type when the value's Go type
// definitively conflicts with the declared schema type.
//
// A missing path under the pillar (segment not listed in
// properties; no escape hatches) returns an OPS_ERROR naming the
// subkey as not declared on the Feature schema. This case typically
// pre-empts via [ValidateAnnotationBindingTargets] at catalog load;
// the expansion-time check is the defensive backstop for bindings
// whose paths route to schema properties that the load-time check
// soft-skipped.
//
// Relaxed-default discipline. The two "return nil" branches above
// (escape-hatch skippable; schema property declares no `type`)
// extend the path-target validator's default-to-skip philosophy:
// when the validator is uncertain about the schema constraint,
// prefer progress over halting. The lever for strict per-subkey
// type validation is authoring discipline — declare `type` on
// every property in the Feature's `spec.schema` to opt into strict
// matching. Properties without a `type` accept any runtime value
// shape including nested maps; the FeatureRecipe rendering layer
// handles whatever shape the annotation contribution carries.
// Authoring docs surface this trade-off explicitly so operators
// understand that strict type validation is opt-in at the schema
// authoring site, not the validator's default.
func validateSubkeyValueAgainstFeatureSchema(feature *types.Feature, pillar, subkey string, value any) error {
	if feature == nil {
		return nil
	}
	compiled, err := compactform.Compile(&feature.Spec.Schema)
	if err != nil {
		// Compile errors surface upstream at catalog load; defensive
		// pass-through here.
		return nil
	}
	declaredType, found, skippable := schemaPropertyType(compiled, pillar, subkey)
	if skippable {
		return nil
	}
	if !found {
		return fmt.Errorf(
			"OPS_ERROR: annotation subkey %q is not declared at Feature %q spec.schema.%s.%s",
			subkey, feature.Metadata.Name, pillar, subkey,
		)
	}
	if declaredType == "" {
		// Property exists but carries no `type` field — accept any
		// runtime type. Authors who want strict typing declare it on
		// the schema.
		return nil
	}
	if !valueMatchesSchemaType(value, declaredType) {
		return fmt.Errorf(
			"OPS_ERROR: annotation subkey %q value %v (Go type %T) does not match Feature %q spec.schema.%s.%s declared type %q",
			subkey, value, value, feature.Metadata.Name, pillar, subkey, declaredType,
		)
	}
	return nil
}

// schemaPropertyType walks the compiled JSON Schema 2020-12 form
// along the pillar + dotted-path to retrieve the schema's declared
// `type` value at the terminal property. Returns
// (typeString, found, skippable) where:
//
//   - found=true, skippable=false → the property exists and the
//     returned typeString is its declared type (empty if no `type`
//     declared).
//   - found=false, skippable=false → the walker reached a properties
//     node and the terminal segment is absent.
//   - skippable=true → the walker hit an escape hatch ($ref,
//     patternProperties, oneOf/anyOf/allOf, schema-bodied
//     additionalProperties); the caller treats this as a soft no-op.
func schemaPropertyType(schema compactform.Schema, pillar, path string) (declaredType string, found bool, skippable bool) {
	if schema == nil {
		return "", false, true
	}
	pillarNode, foundPillar := propertyOf(schema, pillar)
	if !foundPillar {
		return "", false, true
	}
	current, ok := pillarNode.(map[string]any)
	if !ok {
		return "", false, true
	}
	if path == "" {
		return "", false, true
	}
	segments := strings.Split(path, ".")
	for i, seg := range segments {
		if hasEscapeHatch(current) {
			return "", false, true
		}
		next, ok := propertyOf(current, seg)
		if !ok {
			return "", false, false
		}
		nextMap, isMap := next.(map[string]any)
		if !isMap {
			return "", false, true
		}
		if i == len(segments)-1 {
			typ, _ := nextMap["type"].(string)
			return typ, true, false
		}
		current = nextMap
	}
	return "", false, true
}

// valueMatchesSchemaType reports whether a runtime Go value's type
// matches the JSON Schema 2020-12 declared type. The mapping
// follows YAML-decode conventions: integers decode to int, fractional
// numbers to float64, booleans to bool, strings to string, mappings
// to map[string]any or types.OrderedMap[any], sequences to []any.
//
// The match is strict by design: no type coercion (e.g., "20"
// against integer is a type mismatch, not a parseable conversion).
// Authors declare types correctly on the Feature schema or use
// scalar annotations that pass through as strings.
func valueMatchesSchemaType(value any, declaredType string) bool {
	switch declaredType {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		}
		return false
	case "number":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
			float32, float64:
			return true
		}
		return false
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		if _, ok := value.(map[string]any); ok {
			return true
		}
		if _, ok := value.(types.OrderedMap[any]); ok {
			return true
		}
		if _, ok := value.(*types.OrderedMap[any]); ok {
			return true
		}
		return false
	case "array":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	}
	// Unknown declared-type strings (or empty) accept any runtime
	// type; authors who want strict typing declare a known schema
	// type at the property.
	return true
}
