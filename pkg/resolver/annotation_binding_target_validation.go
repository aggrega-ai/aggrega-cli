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

