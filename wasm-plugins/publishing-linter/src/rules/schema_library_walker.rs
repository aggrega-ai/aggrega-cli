// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Entry-level + sub-schema-aware walker for SchemaLibrary content.
//! Rules under `src/rules/` that operate on the per-entry shape of
//! a SchemaLibrary consume this walker rather than re-implementing
//! map traversal.
//!
//! [`visit_schemas_recursive`] descends into JSON Schema 2020-12
//! sub-schema-bearing keywords. It calls the visitor for each visited
//! sub-schema node as `(entry_name, json_pointer, value)`. The pointer
//! is RFC 6901-formatted; sub-schema positions only — annotation values
//! (`examples`, `default`, `const`, etc.) are NOT descended into.

use aggrega_pdk_rust::LinterInput;
use serde_json::Value;

use crate::raw;

// Object-valued sub-schema-bearing keywords (each value IS a sub-schema)
const OBJECT_SUBSCHEMA_KEYS: &[&str] = &[
    "additionalProperties",
    "items",
    "contains",
    "not",
    "if",
    "then",
    "else",
    "propertyNames",
    "unevaluatedProperties",
    "unevaluatedItems",
];

// Map-of-sub-schemas keywords (each VALUE within the map is a sub-schema)
const MAP_SUBSCHEMA_KEYS: &[&str] = &[
    "properties",
    "patternProperties",
    "$defs",
    "definitions",
    "dependentSchemas",
];

// Array-of-sub-schemas keywords (each ITEM in the array is a sub-schema)
const ARRAY_SUBSCHEMA_KEYS: &[&str] = &[
    "allOf",
    "anyOf",
    "oneOf",
    "prefixItems",
];

/// `visit_schemas_recursive` walks each entry under `spec.schemas`
/// and descends into every JSON Schema 2020-12 sub-schema-bearing
/// keyword. Calls `visitor(entry_name, json_pointer, value)` for
/// each visited (sub-)schema node — the same `entry_name` repeats
/// as the walker descends within one entry; the pointer locates the
/// node within the entry per RFC 6901.
///
/// Pointer shape:
///   - `""` at the entry top-level
///   - `/properties/foo` one level down via `properties.foo`
///   - `/anyOf/2` for the third (0-indexed) `anyOf` branch
///   - `/items` for the `items` sub-schema
///   - `/$defs/Bar` for `$defs.Bar`
///
/// Sub-schema keywords descended into:
///   - Object-valued: additionalProperties, items, contains, not,
///     if, then, else, propertyNames, unevaluatedProperties,
///     unevaluatedItems
///   - Map-of-sub-schemas: properties.*, patternProperties.*,
///     $defs.*, definitions.*, dependentSchemas.*
///   - Array-of-sub-schemas: allOf[], anyOf[], oneOf[], prefixItems[]
///
/// Annotation positions (`examples`, `default`, `const`, `enum`,
/// `title`, `description`, vendor extensions, arbitrary unrecognised
/// keys) are NOT descended into. Rules consuming the walker fire
/// only at sub-schema positions — matching where the compile-time
/// `$ref` expansion machinery actually inspects.
///
/// The walker does NOT follow `$ref` (cross-document resolution is
/// out of scope). The walker DESCENDS INTO sub-schemas with their
/// own `$id:` — the boundary is reported to the visitor, not hidden.
pub fn visit_schemas_recursive<F>(input: &LinterInput, mut visitor: F)
where
    F: FnMut(&str, &str, &Value),
{
    if input.entity_kind != "SchemaLibrary" {
        return;
    }
    // The proposed structure arrives as verbatim wire JSON; parse it
    // into an owned tree so the per-entry sub-schemas can be walked. The
    // tree is local to this call, so the visitor receives borrows into
    // it rather than into the input.
    let proposed = raw::parse(&input.proposed);
    let entries = match proposed
        .get("spec")
        .and_then(|s| s.get("schemas"))
        .and_then(|s| s.as_object())
    {
        Some(m) => m,
        None => return,
    };
    for (entry_name, entry_value) in entries {
        walk_subschemas(entry_name, "", entry_value, &mut visitor);
    }
}

fn walk_subschemas<F>(entry_name: &str, pointer: &str, value: &Value, visitor: &mut F)
where
    F: FnMut(&str, &str, &Value),
{
    visitor(entry_name, pointer, value);
    let map = match value {
        Value::Object(m) => m,
        _ => return,
    };
    for key in OBJECT_SUBSCHEMA_KEYS {
        if let Some(sub) = map.get(*key) {
            let child = format!("{pointer}/{}", escape_pointer_segment(key));
            walk_subschemas(entry_name, &child, sub, visitor);
        }
    }
    for key in MAP_SUBSCHEMA_KEYS {
        if let Some(Value::Object(inner)) = map.get(*key) {
            for (name, sub) in inner {
                let child = format!(
                    "{pointer}/{}/{}",
                    escape_pointer_segment(key),
                    escape_pointer_segment(name),
                );
                walk_subschemas(entry_name, &child, sub, visitor);
            }
        }
    }
    for key in ARRAY_SUBSCHEMA_KEYS {
        if let Some(Value::Array(arr)) = map.get(*key) {
            for (idx, item) in arr.iter().enumerate() {
                let child = format!("{pointer}/{}/{idx}", escape_pointer_segment(key));
                walk_subschemas(entry_name, &child, item, visitor);
            }
        }
    }
}

// RFC 6901 token escaping: `~` -> `~0`, `/` -> `~1`. JSON Schema
// keywords and entry names usually don't contain either character,
// but `patternProperties` regex-key sub-paths and authored
// `properties` keys could.
fn escape_pointer_segment(s: &str) -> String {
    s.replace('~', "~0").replace('/', "~1")
}
