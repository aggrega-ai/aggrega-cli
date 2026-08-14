// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compactform

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PillarDefaults extracts the declared defaults under a compiled pillar
// (compiled.properties.<pillar>.properties.*) as a nested OrderedMap with keys
// in sorted order. A compiled schema without that pillar yields an empty map.
//
// This is the single source of truth for schema-default extraction: both the
// IR default-materialisation pass and the schema-conformance gate at resolution read
// defaults through here, so build-time and validate-time interpret an authored
// schema identically.
func PillarDefaults(compiled Schema, pillar string) types.OrderedMap[any] {
	topProps, ok := defaultsMapAt(compiled, "properties")
	if !ok {
		return types.OrderedMap[any]{}
	}
	pillarNode, ok := defaultsMapAt(topProps, pillar)
	if !ok {
		return types.OrderedMap[any]{}
	}
	props, ok := defaultsMapAt(pillarNode, "properties")
	if !ok {
		return types.OrderedMap[any]{}
	}
	return SchemaDefaults(props)
}

// SchemaDefaults walks a compiled "properties" map and returns the declared
// defaults as an OrderedMap with keys in sorted order. A property contributes
// its `default` when present; otherwise, if it is a nested object, its non-empty
// default subtree (recursion). A `$ref`-valued property contributes nothing —
// defaults reached through composition ($ref or allOf/oneOf/anyOf) are not
// materialised here; composition-aware resolution is a deferred follow-on.
//
// The sensitivity reader walks the same compiled schema and deliberately draws a
// WIDER line, following a reference and every composition branch. That is not a
// copy of this walk that drifted, and unifying the two would be a defect, for two
// independent reasons.
//
// They feed different sides of the build. This walk materialises authored values
// into the render context, so widening it would put defaults into rendered
// artifacts that are not there today. The sensitivity reader feeds what
// human-readable surfaces withhold, where collecting more never changes a rendered
// byte.
//
// They have opposite fail-safe directions. Materialising a default must follow
// what an author wrote, so a default reached through a branch an instance does not
// satisfy would invent a value nobody declared. Reading sensitivity must catch
// everything, so a path collected that need not have been only withholds a datum
// an author left readable.
func SchemaDefaults(props map[string]any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		node, ok := props[key].(map[string]any)
		if !ok {
			continue
		}
		if _, isRef := node["$ref"]; isRef {
			continue
		}
		if d, has := node["default"]; has {
			out.Set(key, d)
			continue
		}
		if childProps, ok := defaultsMapAt(node, "properties"); ok {
			if nested := SchemaDefaults(childProps); nested.Len() > 0 {
				out.Set(key, nested)
			}
		}
	}
	return out
}

// MergeSchemaDefaults returns a new OrderedMap that fills declared defaults into
// author for keys the author omitted. The three-case nested semantic:
//
//  1. Key absent from author: filled with the default (a scalar, or a
//     synthesized nested default subtree).
//  2. Key present as an empty nested map: kept empty — an explicit empty is a
//     deliberate opt-out from nested defaults.
//  3. Key present as a non-empty nested map: recurse to fill the children the
//     author did not supply.
//
// Author-supplied scalar values always win. Output order is author keys in
// author order, then omitted-default keys in sorted order; the result is a
// fresh map at every level, so neither input is mutated.
func MergeSchemaDefaults(author, defaults types.OrderedMap[any]) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for _, k := range author.Keys() {
		v, _ := author.Get(k)
		if def, hasDef := defaults.Get(k); hasDef {
			if defOM, defNested := def.(types.OrderedMap[any]); defNested {
				if curOM, curNested := v.(types.OrderedMap[any]); curNested {
					if curOM.Len() == 0 {
						out.Set(k, v) // Case 2: explicit empty → keep empty
					} else {
						out.Set(k, MergeSchemaDefaults(curOM, defOM)) // Case 3
					}
					continue
				}
			}
		}
		out.Set(k, v) // author scalar / no matching nested default → author wins
	}
	for _, k := range defaults.Keys() {
		if _, present := author.Get(k); !present {
			def, _ := defaults.Get(k)
			out.Set(k, def) // Case 1: fill omitted key
		}
	}
	return out
}

// defaultsMapAt returns node[key] as a map[string]any when present and of that shape.
func defaultsMapAt(node map[string]any, key string) (map[string]any, bool) {
	raw, ok := node[key]
	if !ok {
		return nil, false
	}
	m, ok := raw.(map[string]any)
	return m, ok
}
