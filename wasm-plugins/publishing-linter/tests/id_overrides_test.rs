// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the id-overrides rule.

use aggrega_pdk_rust::{LinterInput, Policies};
use publishing_linter::rules::id_overrides;
use serde_json::json;

fn fixture(schemas: serde_json::Value, policies: Option<Policies>) -> LinterInput {
    LinterInput {
        rule_id: "id-overrides".to_string(),
        entity_kind: "SchemaLibrary".to_string(),
        proposed: json!({
            "kind": "SchemaLibrary",
            "metadata": {"name": "Probes", "version": "1.0.0"},
            "spec": {"schemas": schemas},
        }),
        previous: None,
        parent: None,
        policies,
    }
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
