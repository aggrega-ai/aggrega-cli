// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the id-overrides rule.

mod common;

use aggrega_pdk_rust::{LinterInput, Policies};
use publishing_linter::rules::id_overrides;
use serde_json::json;

fn fixture(schemas: serde_json::Value, policies: Option<Policies>) -> LinterInput {
    let proposed = json!({
        "kind": "SchemaLibrary",
        "metadata": {"name": "Probes", "version": "1.0.0"},
        "spec": {"schemas": schemas},
    });
    common::fixture("SchemaLibrary", proposed, None, policies, None)
}

#[test]
fn id_overrides_rejects_explicit_id() {
    let input = fixture(
        json!({
            "CustomShape": {
                "$id": "https://example.com/schemas/custom",
                "type": "object"
            }
        }),
        None,
    );
    let findings = id_overrides::classify(&input);
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].rule_id, "id-overrides");
    assert!(findings[0].message.contains("CustomShape"));
    assert!(findings[0].message.contains("$id"));
}

#[test]
fn id_overrides_passes_clean_schema() {
    let input = fixture(
        json!({
            "Probe": {"type": "object", "properties": {"path": {"type": "string"}}}
        }),
        None,
    );
    let findings = id_overrides::classify(&input);
    assert_eq!(findings.len(), 0);
}

#[test]
fn id_overrides_policy_flag_suppresses() {
    let policies = Some(Policies {
        allow_id_overrides: true,
        ..Default::default()
    });
    let input = fixture(
        json!({"X": {"$id": "https://example.com/x"}}),
        policies,
    );
    let findings = id_overrides::classify(&input);
    assert_eq!(findings.len(), 0);
}

#[test]
fn id_overrides_flags_one_finding_per_offending_entry() {
    let input = fixture(
        json!({
            "EntryA": {"$id": "https://example.com/a"},
            "EntryB": {"$id": "https://example.com/b"},
            "EntryC": {"type": "object"}
        }),
        None,
    );
    let findings = id_overrides::classify(&input);
    assert_eq!(findings.len(), 2);
}

#[test]
fn id_overrides_rejects_nested_id_in_properties() {
    // $id at a sub-schema position under properties.someField MUST
    // be flagged (any-depth scope).
    let input = fixture(
        json!({
            "Probe": {
                "type": "object",
                "properties": {
                    "someField": {
                        "$id": "https://example.com/inner",
                        "type": "string"
                    }
                }
            }
        }),
        None,
    );
    let findings = id_overrides::classify(&input);
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].rule_id, "id-overrides");
    assert!(findings[0].message.contains("at /properties/someField"));
}

#[test]
fn id_overrides_rejects_nested_id_in_anyof() {
    let input = fixture(
        json!({
            "Probe": {
                "anyOf": [
                    {"type": "object"},
                    {
                        "$id": "https://example.com/branch",
                        "type": "string"
                    }
                ]
            }
        }),
        None,
    );
    let findings = id_overrides::classify(&input);
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains("at /anyOf/1"));
}

#[test]
fn id_overrides_message_includes_json_pointer() {
    // Nested $id finding message MUST carry the RFC 6901 pointer.
    let input = fixture(
        json!({
            "Probe": {
                "$defs": {
                    "Helper": {
                        "$id": "https://example.com/helper",
                        "type": "object"
                    }
                }
            }
        }),
        None,
    );
    let findings = id_overrides::classify(&input);
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains(" at /$defs/Helper"),
        "message should include RFC 6901 pointer; got {:?}", findings[0].message);
}
