// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// componentPillarDefaults compiles a Component's schema and returns the declared
// defaults for the values and infra pillars as nested OrderedMaps. A Component
// with no schema, or one whose schema fails to compile, yields empty maps — a
// malformed schema surfaces through the validator, not here. The compiled form
// is an unordered map, so extraction emits keys in sorted order for
// deterministic output.
func componentPillarDefaults(comp types.Component, libraries map[string]types.SchemaLibrary) (values, infra types.OrderedMap[any]) {
	if comp.Spec.Schema.Kind == 0 {
		return types.OrderedMap[any]{}, types.OrderedMap[any]{}
	}
	// Resolve cross-library schema `$ref`s (whole-pillar and base+flavor) against
	// the catalog's SchemaLibraries so library-declared defaults materialize. The
	// libraries are passed through the same compactform option the validator uses,
	// so build-time and validate-time interpret the schema identically. Omitted
	// when absent (inline-only behavior, unchanged). WithConsumerFile is not needed:
	// cross-library refs resolve by library name, not by relative path.
	var opts []compactform.CompileOption
	if len(libraries) > 0 {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(&comp.Spec.Schema, opts...)
	if err != nil {
		return types.OrderedMap[any]{}, types.OrderedMap[any]{}
	}
	return pillarDefaults(compiled, "values"), pillarDefaults(compiled, "infra")
}

// pillarDefaults extracts the declared defaults under a compiled pillar
// (compiled.properties.<pillar>.properties.*).
func pillarDefaults(compiled compactform.Schema, pillar string) types.OrderedMap[any] {
	topProps, ok := mapAt(compiled, "properties")
	if !ok {
		return types.OrderedMap[any]{}
	}
	pillarNode, ok := mapAt(topProps, pillar)
	if !ok {
		return types.OrderedMap[any]{}
	}
	props, ok := mapAt(pillarNode, "properties")
	if !ok {
		return types.OrderedMap[any]{}
	}
	return schemaDefaults(props)
}

// schemaDefaults walks a compiled "properties" map and returns the declared
// defaults as an OrderedMap with keys in sorted order. A property contributes
// its `default` when present; otherwise, if it is a nested object, its non-empty
// default subtree (recursion). A `$ref`-valued property contributes nothing —
// defaults reached through composition ($ref or allOf/oneOf/anyOf) are not yet
// materialized; composition-aware resolution is a deferred follow-on.
func schemaDefaults(props map[string]any) types.OrderedMap[any] {
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
		if childProps, ok := mapAt(node, "properties"); ok {
			if nested := schemaDefaults(childProps); nested.Len() > 0 {
				out.Set(key, nested)
			}
		}
	}
	return out
}

// mergeSchemaDefaults returns a new OrderedMap that fills declared defaults into
// author for keys the author omitted. The three-case nested semantic mirrors the
// parameter default-backfill:
//
//  1. Key absent from author: filled with the default (a scalar, or a
//     synthesized nested default subtree).
//  2. Key present as an empty nested map: kept empty — an explicit empty is a
//     deliberate opt-out from nested defaults.
//  3. Key present as a non-empty nested map: recurse to fill the children the
//     author did not supply.
//
// Author-supplied scalar values always win. Output order is author keys in
// author order, then omitted-default keys in sorted order; the result is a fresh
// map at every level, so neither the author's nor the schema's structures are
// mutated.
func mergeSchemaDefaults(author, defaults types.OrderedMap[any]) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for _, k := range author.Keys() {
		v, _ := author.Get(k)
		if def, hasDef := defaults.Get(k); hasDef {
			if defOM, defNested := def.(types.OrderedMap[any]); defNested {
				if curOM, curNested := v.(types.OrderedMap[any]); curNested {
					if curOM.Len() == 0 {
						out.Set(k, v) // Case 2: explicit empty → keep empty
					} else {
						out.Set(k, mergeSchemaDefaults(curOM, defOM)) // Case 3
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

// mapAt returns node[key] as a map[string]any when present and of that shape.
func mapAt(node map[string]any, key string) (map[string]any, bool) {
	raw, ok := node[key]
	if !ok {
		return nil, false
	}
	m, ok := raw.(map[string]any)
	return m, ok
}
