// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Direct walker tests for `visit_schemas_recursive`. Asserts:
//!   - All JSON Schema 2020-12 sub-schema-bearing keywords are
//!     descended into.
//!   - Annotation positions are NOT descended into.
//!   - The visitor's JSON Pointer matches RFC 6901 shape.

mod common;

use publishing_linter::rules::schema_library_walker::visit_schemas_recursive;
use serde_json::{json, Value};

fn schemalibrary_input(schemas: Value) -> aggrega_pdk_rust::LinterInput {
    let proposed = json!({
        "kind": "SchemaLibrary",
        "metadata": {"name": "TestLib", "version": "1.0.0"},
        "spec": {"schemas": schemas},
    });
    common::fixture("SchemaLibrary", proposed, None, None, None)
}

fn collect_pointers(input: &aggrega_pdk_rust::LinterInput) -> Vec<(String, String)> {
    let mut out = Vec::new();
    visit_schemas_recursive(input, |entry_name, pointer, _value| {
        out.push((entry_name.to_string(), pointer.to_string()));
    });
    out
}

#[test]
fn walker_visits_nested_anyof_oneof_allof() {
    let input = schemalibrary_input(json!({
        "Probe": {
            "anyOf": [{"type": "object"}, {"type": "string"}],
            "oneOf": [{"type": "integer"}],
            "allOf": [{"type": "number"}]
        }
    }));
    let visits = collect_pointers(&input);
    let pointers: Vec<&str> = visits.iter().map(|(_, p)| p.as_str()).collect();
    assert!(pointers.contains(&""), "missing top-level visit");
    assert!(pointers.contains(&"/anyOf/0"), "missing anyOf[0]: {:?}", pointers);
    assert!(pointers.contains(&"/anyOf/1"), "missing anyOf[1]");
    assert!(pointers.contains(&"/oneOf/0"), "missing oneOf[0]");
    assert!(pointers.contains(&"/allOf/0"), "missing allOf[0]");
}

#[test]
fn walker_visits_nested_properties_patternProperties_defs() {
    let input = schemalibrary_input(json!({
        "Probe": {
            "type": "object",
            "properties": {
                "host": {"type": "string"},
                "port": {"type": "integer"}
            },
            "patternProperties": {
                "^x-": {"type": "string"}
            },
            "$defs": {
                "Helper": {"type": "object"}
            }
        }
    }));
    let visits = collect_pointers(&input);
    let pointers: Vec<&str> = visits.iter().map(|(_, p)| p.as_str()).collect();
    assert!(pointers.contains(&"/properties/host"), "missing properties.host: {:?}", pointers);
    assert!(pointers.contains(&"/properties/port"), "missing properties.port");
    assert!(pointers.contains(&"/patternProperties/^x-"), "missing patternProperties");
    assert!(pointers.contains(&"/$defs/Helper"), "missing $defs.Helper");
}

#[test]
fn walker_visits_nested_items_contains_not() {
    let input = schemalibrary_input(json!({
        "Probe": {
            "type": "object",
            "items": {"type": "string"},
            "contains": {"type": "integer"},
            "not": {"type": "null"}
        }
    }));
    let visits = collect_pointers(&input);
    let pointers: Vec<&str> = visits.iter().map(|(_, p)| p.as_str()).collect();
    assert!(pointers.contains(&"/items"), "missing /items");
    assert!(pointers.contains(&"/contains"), "missing /contains");
    assert!(pointers.contains(&"/not"), "missing /not");
}

#[test]
fn walker_does_not_descend_into_non_subschema_object_values() {
    // examples is an array of arbitrary values (NOT sub-schemas).
    // default and const are arbitrary values. The walker MUST NOT
    // descend into them.
    let input = schemalibrary_input(json!({
        "Probe": {
            "type": "object",
            "examples": [{"would-be-wrong": true}, "string"],
            "default": {"would-be-wrong": true},
            "const": {"would-be-wrong": true},
            "title": "ignored",
            "description": "ignored"
        }
    }));
    let visits = collect_pointers(&input);
    let pointers: Vec<&str> = visits.iter().map(|(_, p)| p.as_str()).collect();
    // The only visit should be the top-level entry itself.
    assert_eq!(pointers, vec![""], "walker should only visit top-level; got {:?}", pointers);
}

#[test]
fn walker_visit_count_matches_expected_depth() {
    let input = schemalibrary_input(json!({
        "Probe": {
            "type": "object",
            "properties": {
                "child": {
                    "type": "object",
                    "properties": {
                        "grandchild": {"type": "string"}
                    }
                }
            }
        }
    }));
    let visits = collect_pointers(&input);
    // Expected visits: top-level, /properties/child, /properties/child/properties/grandchild
    assert_eq!(visits.len(), 3, "expected 3 visits; got {:?}", visits);
}

#[test]
fn walker_json_pointer_format_matches_rfc_6901() {
    let input = schemalibrary_input(json!({
        "Probe": {
            "type": "object",
            "properties": {
                "host": {
                    "type": "string",
                    "anyOf": [{"type": "string"}, {"type": "null"}]
                }
            },
            "items": {"type": "string"}
        }
    }));
    let visits = collect_pointers(&input);
    let pointers: Vec<&str> = visits.iter().map(|(_, p)| p.as_str()).collect();
    // Object-valued descent (items): /items
    assert!(pointers.contains(&"/items"));
    // Map-of-subschemas descent (properties): /properties/host
    assert!(pointers.contains(&"/properties/host"));
    // Array-of-subschemas descent (anyOf): /properties/host/anyOf/0
    assert!(pointers.contains(&"/properties/host/anyOf/0"));
    assert!(pointers.contains(&"/properties/host/anyOf/1"));
}
