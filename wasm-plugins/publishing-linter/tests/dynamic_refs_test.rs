// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the dynamic-refs rule. Fixtures are inline
//! serde_json::json! literals exercising classify() directly.

mod common;

use aggrega_pdk_rust::{LinterInput, Policies};
use publishing_linter::rules::dynamic_refs;
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
fn dynamic_refs_rejects_dynamic_anchor() {
    let input = fixture(
        json!({
            "PolymorphicShape": {
                "$dynamicAnchor": "node",
                "type": "object",
            }
        }),
        None,
    );
    let findings = dynamic_refs::classify(&input);
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].rule_id, "dynamic-refs");
    assert!(findings[0].message.contains("$dynamicAnchor"));
    assert!(findings[0].message.contains("PolymorphicShape"));
}

#[test]
fn dynamic_refs_rejects_dynamic_ref() {
    let input = fixture(
        json!({
            "RecursiveTree": {
                "type": "object",
                "properties": {
                    "child": { "$dynamicRef": "#node" }
                }
            }
        }),
        None,
    );
    let findings = dynamic_refs::classify(&input);
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains("$dynamicRef"));
    assert!(findings[0].message.contains("RecursiveTree"));
}

#[test]
fn dynamic_refs_passes_clean_schema() {
    let input = fixture(
        json!({
            "Probe": {
                "type": "object",
                "properties": {"path": {"type": "string"}, "port": {"type": "integer"}}
            }
        }),
        None,
    );
    let findings = dynamic_refs::classify(&input);
    assert_eq!(findings.len(), 0);
}

#[test]
fn dynamic_refs_policy_flag_suppresses() {
    let policies = Some(Policies {
        allow_dynamic_refs: true,
        ..Default::default()
    });
    let input = fixture(
        json!({"X": {"$dynamicAnchor": "x"}}),
        policies,
    );
    let findings = dynamic_refs::classify(&input);
    assert_eq!(findings.len(), 0);
}

#[test]
fn dynamic_refs_passes_when_not_schemalibrary() {
    // Component with a misleading metadata.name; walker is
    // entity-kind gated so this should yield nothing.
    let input = common::fixture(
        "Component",
        json!({
            "kind": "Component",
            "spec": {"schemas": {"X": {"$dynamicAnchor": "x"}}}
        }),
        None,
        None,
        None,
    );
    let findings = dynamic_refs::classify(&input);
    assert_eq!(findings.len(), 0);
}
