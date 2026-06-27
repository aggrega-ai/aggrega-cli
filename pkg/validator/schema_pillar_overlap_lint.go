// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"go.yaml.in/yaml/v3"
)

// ValidateSchemaPillarOverlap walks an entity's spec.schema (a JSON
// Schema 2020-12 yaml.Node, authored in compact form, JSON Schema
// YAML, or a mix of both) and emits an advisory warning for every
// property name that appears in both the values: pillar subtree and
// the infra: pillar subtree at the same nesting depth.
//
// The compiler accepts pillar-name overlaps — pillars are separate
// namespaces by design, and an author who genuinely wants the same
// name in both (e.g., port = app-internal under values, service-port
// under infra) is supported. The lint surfaces the frequent authoring
// smell where the overlap is accidental: readers and template authors
// face "which pillar holds the foo I'm reading?" friction.
//
// The walker is recursive: at every nesting depth of the
// `properties:` tree under the values pillar and the parallel depth
// under the infra pillar, the function computes the intersection of
// property names declared at that depth and emits one warning per
// overlap. Schema nodes that escape simple property-walk ($ref,
// patternProperties, oneOf/anyOf/allOf composition) terminate the
// recursion at that branch and produce no warning.
//
// Returns the list of warning strings in deterministic order. The
// caller (catalog Pass 2) aggregates them into the catalog's
// lint-warnings surface.
func ValidateSchemaPillarOverlap(entityKind, entityName, filePath string, schema yaml.Node) []string {
	compiled, err := compactform.Compile(&schema)
	if err != nil {
		// Compile errors surface as schema-validation errors elsewhere
		// in Pass 2; the overlap lint stays silent on uncompilable
		// schemas to avoid double-reporting the same authoring
		// problem.
		return nil
	}
	values, _ := propertyOfSchema(compiled, "values")
	infra, _ := propertyOfSchema(compiled, "infra")
	if values == nil || infra == nil {
		return nil
	}
	var out []string
	walkPillarOverlap(entityKind, entityName, filePath, "", values, infra, &out)
	return out
}

// walkPillarOverlap recurses through paired values-pillar and infra-
// pillar subtrees at the same nesting depth and emits one warning per
// same-name property that resolves to a terminal leaf on at least one
// side. The discipline is: when the same name appears in both pillars
// AND both sides descend through it into further `properties:`
// subtrees, the shared name is an organizing namespace, not a leaf
// conflict — the walker recurses without emitting at the intermediate
// level and emits only when a deeper leaf overlap actually exists.
// When at least one side terminates the name at a primitive (no
// `properties:` sub-tree), the overlap is a real property-name
// collision and the walker emits the warning.
//
// Schema shapes that escape simple property-walk ($ref,
// patternProperties, oneOf/anyOf/allOf, additionalProperties with a
// schema body) terminate the recursion at that branch silently.
func walkPillarOverlap(entityKind, entityName, filePath, currentPath string, valuesNode, infraNode map[string]any, out *[]string) {
	if hasOverlapEscapeHatch(valuesNode) || hasOverlapEscapeHatch(infraNode) {
		return
	}
	valuesProps, ok := propertiesMap(valuesNode)
	if !ok {
		return
	}
	infraProps, ok := propertiesMap(infraNode)
	if !ok {
		return
	}
	overlapNames := intersectionSorted(valuesProps, infraProps)
	for _, name := range overlapNames {
		vChild, _ := valuesProps[name].(map[string]any)
		iChild, _ := infraProps[name].(map[string]any)
		vIsObject := vChild != nil && hasNestedProperties(vChild)
		iIsObject := iChild != nil && hasNestedProperties(iChild)
		nextPath := name
		if currentPath != "" {
			nextPath = currentPath + "." + name
		}
		if vIsObject && iIsObject {
			// Shared organizing namespace: descend without emitting at
			// this level. A deeper terminal overlap, if any, fires
			// through the recursive call.
			walkPillarOverlap(entityKind, entityName, filePath, nextPath, vChild, iChild, out)
			continue
		}
		// Terminal overlap on at least one side: real property-name
		// collision.
		*out = append(*out, fmt.Sprintf(
			"LINT: %s %q at %s declares property %q in both values: and infra: pillars (path: %s)",
			entityKind, entityName, filePath, name, nextPath,
		))
	}
}

// hasNestedProperties reports whether a JSON Schema node carries a
// non-empty `properties:` map. A node that does not (a scalar leaf or
// an object-typed node without further properties) is treated as a
// terminal for overlap-lint purposes — overlaps at this level are
// real same-name property conflicts rather than shared organizing
// namespaces.
func hasNestedProperties(node map[string]any) bool {
	propsRaw, has := node["properties"]
	if !has {
		return false
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return false
	}
	return len(props) > 0
}

// propertyOfSchema returns the entry of the schema's `properties` map
// at the named key, plus an ok flag. Returns nil when the schema does
// not declare a `properties` map at the root or when the named entry
// is absent.
func propertyOfSchema(schema map[string]any, name string) (map[string]any, bool) {
	if schema == nil {
		return nil, false
	}
	propsRaw, has := schema["properties"]
	if !has {
		return nil, false
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	val, found := props[name]
	if !found {
		return nil, false
	}
	m, ok := val.(map[string]any)
	if !ok {
		return nil, false
	}
	return m, true
}

// propertiesMap reads a JSON Schema node's `properties` map and
// returns it along with an ok flag. Schema nodes without a
// `properties` map (scalar leaves, $ref nodes, oneOf nodes, etc.)
// return false; the walker treats the absence as "no children to
// recurse into" and terminates the branch.
func propertiesMap(node map[string]any) (map[string]any, bool) {
	propsRaw, has := node["properties"]
	if !has {
		return nil, false
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil, false
	}
	return props, true
}

// hasOverlapEscapeHatch reports whether a JSON Schema node carries a
// composition or reference clause that escapes simple property-walk.
// The walker terminates recursion at branches with escape hatches
// because the actually-declared property set is not statically
// derivable from the `properties` map alone.
func hasOverlapEscapeHatch(node map[string]any) bool {
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

// intersectionSorted returns the sorted intersection of two
// map[string]any key sets. Sorted output keeps the warning emission
// order deterministic across runs of identical input.
func intersectionSorted(a, b map[string]any) []string {
	var out []string
	for k := range a {
		if _, has := b[k]; has {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
