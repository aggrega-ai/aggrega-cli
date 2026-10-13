// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// fromEntry pairs a delivery key with the provides key it references.
type fromEntry struct {
	deliveryKey string
	providesKey string
}

// decodeFromEntries extracts (delivery-key, provides-key) pairs from one
// inject mechanism's authored value. The selfMode flag distinguishes the two
// reference surfaces:
//
//   - selfMode true (component-level self-inject): a compact string value is a
//     literal, not a provides-key reference, and is skipped. Only verbose
//     `from:` entries yield a reference.
//   - selfMode false (uses-level cross-inject): every compact string value is a
//     provides-key reference, and verbose `from:` entries yield references too.
//
// Verbose `value:` entries (templates) are never provides-key references in
// either mode. The mechanism value may arrive as a compact map
// (map[string]any / map[string]string), a verbose array, or the ordered-map
// round-trip shape; each is handled. Iteration order is not significant —
// the caller's errors are sorted at the resolver boundary.
func decodeFromEntries(raw any, selfMode bool) []fromEntry {
	var out []fromEntry
	if raw == nil {
		return out
	}
	switch v := raw.(type) {
	case map[string]any:
		for k, val := range v {
			switch vv := val.(type) {
			case string:
				// Compact string: a literal in self-inject, a provides-key
				// reference in uses-inject.
				if !selfMode {
					out = append(out, fromEntry{deliveryKey: k, providesKey: vv})
				}
			case map[string]any:
				// Verbose form: {from: K} is a provides-key reference in both modes.
				if fromRaw, ok := vv["from"]; ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			case *types.OrderedMap[any]:
				if vv == nil {
					continue
				}
				if fromRaw, ok := vv.Get("from"); ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			}
		}
	case map[string]string:
		for k, s := range v {
			if !selfMode {
				out = append(out, fromEntry{deliveryKey: k, providesKey: s})
			}
		}
	case *types.OrderedMap[any]:
		if v == nil {
			return out
		}
		for k, val := range v.All() {
			switch vv := val.(type) {
			case string:
				if !selfMode {
					out = append(out, fromEntry{deliveryKey: k, providesKey: vv})
				}
			case *types.OrderedMap[any]:
				if vv == nil {
					continue
				}
				if fromRaw, ok := vv.Get("from"); ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			case map[string]any:
				if fromRaw, ok := vv["from"]; ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			}
		}
	case types.OrderedMap[any]:
		return decodeFromEntries(&v, selfMode)
	case []any:
		for _, entry := range v {
			in, _, from, _, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
			if !ok || !hasFrom || from == "" {
				continue
			}
			out = append(out, fromEntry{deliveryKey: in, providesKey: from})
		}
	}
	return out
}

// ComponentProvidesKeys returns the set of declared `provides` keys for a
// catalog Component — the output contract a realizing Recipe must publish. The
// keys are the property names of the `provides` pillar in the Component's
// spec.schema, compiled to JSON Schema 2020-12 via pkg/compactform (the same
// compile path the provider-base and pillar validators use). A Component with
// no spec.schema, no `provides` pillar, or a `provides` pillar that compiles to
// no properties yields an empty set. Mirrors FeatureProvidesKeys; both delegate
// to pillarKeysFromSchema.
//
// The schema is read as-authored on the resolved Component; cross-document
// $ref expansion uses the supplied SchemaLibraries map when present.
func ComponentProvidesKeys(comp types.Component, libraries map[string]types.SchemaLibrary) map[string]bool {
	return pillarKeysFromSchema(comp.Spec.Schema, "provides", libraries)
}

// ComponentValuesKeys returns the set of top-level keys a catalog Component's
// `values` pillar declares — the names an Aggregate may set on an instance of
// it. It reads the pillar exactly as ComponentProvidesKeys reads `provides`:
// compiled through pkg/compactform, with cross-document $ref expansion against
// the supplied SchemaLibraries, and empty for a Component declaring no values
// pillar or one that compiles to no properties. Only the names are read; no
// value and no default is.
func ComponentValuesKeys(comp types.Component, libraries map[string]types.SchemaLibrary) map[string]bool {
	return pillarKeysFromSchema(comp.Spec.Schema, "values", libraries)
}

// FeatureProvidesKeys returns the provides-pillar keys a Feature declares in its
// spec.schema — the output contract a realizing FeatureRecipe must publish.
// Mirrors ComponentProvidesKeys; both delegate to pillarKeysFromSchema.
func FeatureProvidesKeys(feat types.Feature, libraries map[string]types.SchemaLibrary) map[string]bool {
	return pillarKeysFromSchema(feat.Spec.Schema, "provides", libraries)
}

// pillarKeysFromSchema extracts the property names one pillar of an entity's
// spec.schema node declares (compact-form-aware). Empty when the schema declares
// no such pillar or the pillar fails to compile (the pillar validator reports
// that fault).
func pillarKeysFromSchema(schema yaml.Node, pillar string, libraries map[string]types.SchemaLibrary) map[string]bool {
	keys := map[string]bool{}
	node := &schema
	if node.Kind == 0 {
		return keys
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return keys
	}

	var pillarNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == pillar {
			pillarNode = node.Content[i+1]
			break
		}
	}
	if pillarNode == nil {
		return keys
	}

	opts := []compactform.CompileOption{}
	if libraries != nil {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(pillarNode, opts...)
	if err != nil {
		// A pillar that fails to compile is reported by the catalog pillar
		// validator at load; this reader treats it as declaring no keys rather
		// than emitting a second diagnostic for the same fault.
		return keys
	}
	props, ok := compiled["properties"].(compactform.Schema)
	if !ok {
		return keys
	}
	for k := range props {
		keys[k] = true
	}
	return keys
}
