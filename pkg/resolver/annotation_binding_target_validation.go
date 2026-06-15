// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
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

		// Supply the catalog's SchemaLibraries so a Feature schema that uses a
		// cross-document $ref (e.g. the standard BaseComponents#/schemas/
		// BaseFeatureParameter) compiles here exactly as it does on every other
		// feature-schema compile path. Without them the $ref cannot resolve and
		// validation halts spuriously. No-op for $ref-less schemas.
		var opts []compactform.CompileOption
		if len(catalogLookup.SchemaLibraries) > 0 {
			opts = append(opts, compactform.WithSchemaLibraries(catalogLookup.SchemaLibraries))
		}
		compiled, err := compactform.Compile(&feature.Spec.Schema, opts...)
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

// validateSubkeyValueAgainstFeatureSchema validates a bound annotation subkey
// value against the target Feature's spec.schema sub-schema at the routed
// pillar+path — full JSON Schema 2020-12 validation (type, enum, pattern,
// numeric range, nested object/array shape), not a type-only check. The
// sub-schema is located by walking properties.<pillar>.properties.<segment>...
// to the terminal node; the value is then validated against that sub-schema
// through the shared pillar-conformance machinery, carrying the Feature
// schema's definitional context so a terminal $ref / allOf / oneOf resolves.
//
// Compile caching. The compiled form of feature.Spec.Schema arrives via the
// [featureSchemaCache] supplied by the caller — one compile per versioned
// Feature URI per [ExpandAnnotations] invocation. A nil/!ok cache result
// soft-skips (a compile failure is surfaced at catalog load, not here).
//
// Skip vs halt. The default-to-skip discipline is retained ONLY for the
// navigation step: an escape hatch ($ref / patternProperties / composition /
// schema-bodied additionalProperties) encountered before the terminal segment
// leaves the path statically unresolvable, so the validator soft-skips that
// binding-path pair rather than risk a false halt. Once the terminal sub-schema
// is located it is validated in full — no type-only relaxation, no soft-skip of
// a terminal composed shape. A terminal segment absent from an explicit
// properties map returns an OPS_ERROR naming the undeclared subkey (this case
// typically pre-empts via [ValidateAnnotationBindingTargets] at catalog load).
// A value conflicting with the located sub-schema returns an OPS_ERROR naming
// the subkey, the value, and the failing constraint.
func validateSubkeyValueAgainstFeatureSchema(cache *featureSchemaCache, feature *types.Feature, pillar, subkey string, value any) error {
	if feature == nil {
		return nil
	}
	compiled, ok := cache.schemaFor(feature)
	if !ok {
		// Compile errors surface upstream at catalog load; defensive
		// soft-skip here matches the path-target validator's
		// default-to-skip discipline.
		return nil
	}
	subSchema, found, skippable := schemaSubSchemaAtPath(compiled, pillar, subkey)
	if skippable {
		return nil
	}
	if !found {
		return fmt.Errorf(
			"OPS_ERROR: annotation subkey %q is not declared at Feature %q spec.schema.%s.%s",
			subkey, feature.Metadata.Name, pillar, subkey,
		)
	}
	terminal := subkey
	if i := strings.LastIndex(subkey, "."); i >= 0 {
		terminal = subkey[i+1:]
	}
	if msg, bad := subkeyConformanceFailure(compiled, subSchema, terminal, value); bad {
		return fmt.Errorf(
			"OPS_ERROR: annotation subkey %q value %v does not conform to Feature %q spec.schema.%s.%s: %s",
			subkey, value, feature.Metadata.Name, pillar, subkey, msg,
		)
	}
	return nil
}

// schemaSubSchemaAtPath walks the compiled JSON Schema 2020-12 form along the
// pillar + dotted-path and returns the terminal property's sub-schema node.
// The contract mirrors the path walker: (sub, true, false) when the path
// resolves cleanly through explicit `properties` maps; (nil, false, false) when
// a `properties` node lists no such segment (a real "undeclared" miss);
// (nil, false, true) when an escape hatch ($ref, patternProperties,
// oneOf/anyOf/allOf, schema-bodied additionalProperties) on the way leaves the
// path statically unresolvable. The terminal node is returned verbatim — a
// terminal $ref/allOf/oneOf is resolved by the validator with the root
// definitional context, not soft-skipped here.
func schemaSubSchemaAtPath(schema compactform.Schema, pillar, path string) (sub map[string]any, found bool, skippable bool) {
	if schema == nil {
		return nil, false, true
	}
	pillarNode, foundPillar := propertyOf(schema, pillar)
	if !foundPillar {
		return nil, false, true
	}
	current, ok := pillarNode.(map[string]any)
	if !ok {
		return nil, false, true
	}
	if path == "" {
		return nil, false, true
	}
	segments := strings.Split(path, ".")
	for i, seg := range segments {
		if hasEscapeHatch(current) {
			return nil, false, true
		}
		next, ok := propertyOf(current, seg)
		if !ok {
			return nil, false, false
		}
		nextMap, isMap := next.(map[string]any)
		if !isMap {
			return nil, false, true
		}
		if i == len(segments)-1 {
			return nextMap, true, false
		}
		current = nextMap
	}
	return nil, false, true
}

// subkeyConformanceFailure validates a single bound value against subSchema —
// the terminal sub-schema located at the binding path — through the shared
// pillar-conformance machinery: a one-key wrapper {seg: subSchema} validated
// over {seg: value}, carrying the compiled schema's $defs/definitions so a
// $ref/allOf/oneOf in subSchema resolves. Unlike the prior type-only check this
// enforces the full JSON Schema 2020-12 constraint set (enum, pattern, numeric
// range, nested object/array shape). Returns the leaf-most finding's message
// and true on a violation; ("", false) when the value conforms or the wrapper
// does not compile (a malformed schema is surfaced at catalog load, not here).
func subkeyConformanceFailure(compiled compactform.Schema, subSchema map[string]any, seg string, value any) (string, bool) {
	wrapper := map[string]any{
		"type":       "object",
		"properties": map[string]any{seg: subSchema},
	}
	if defs, ok := compiled["$defs"]; ok {
		wrapper["$defs"] = defs
	}
	if defs, ok := compiled["definitions"]; ok {
		wrapper["definitions"] = defs
	}
	v, err := newPillarValidator(wrapper)
	if err != nil {
		return "", false
	}
	raw, err := json.Marshal(map[string]any{seg: value})
	if err != nil {
		return "", false
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return "", false
	}
	rpt, err := v.Validate(instance, &jsonvalidator.Context{Surround: instance})
	if err != nil || rpt == nil || len(rpt.Findings) == 0 {
		return "", false
	}
	f := leafmostFindings(rpt.Findings)[0]
	if f.Keyword != "" {
		return fmt.Sprintf("%s (%s)", f.Message, f.Keyword), true
	}
	return f.Message, true
}
