// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the x-aggrega-discouragement rule.

mod common;

use aggrega_pdk_rust::Policies;
use publishing_linter::rules::x_aggrega_discouragement;
use serde_json::json;

fn fixture(schema: serde_json::Value, policies: Option<Policies>) -> aggrega_pdk_rust::LinterInput {
    let proposed = json!({
        "kind": "Component",
        "metadata": {"name": "core/compute/api-server"},
        "spec": {"schema": schema},
    });
    common::fixture("Component", proposed, None, policies, None)
}

#[test]
fn discouragement_clean_schema_no_findings() {
    let input = fixture(
        json!({
            "type": "object",
            "properties": {
                "port": {"type": "integer"}
            }
        }),
        None,
    );
    let findings = x_aggrega_discouragement::classify(&input);
    assert_eq!(findings.len(), 0);
}

#[test]
fn discouragement_top_level_selector_flagged() {
    let input = fixture(
        json!({
            "type": "object",
            "x-aggrega-selector": "component.aggrega.ai/type=stateful-server"
        }),
        None,
    );
    let findings = x_aggrega_discouragement::classify(&input);
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].rule_id, "x-aggrega-discouragement");
    assert!(findings[0].message.contains("x-aggrega-selector"));
    assert!(findings[0].message.contains("at </x-aggrega-selector>"));
    assert!(findings[0].message.contains("usesSchema"));
}

#[test]
fn discouragement_both_keys_findings() {
    let input = fixture(
        json!({
            "type": "object",
            "x-aggrega-selector": "x",
            "x-aggrega-inject": {"user": "y"}
        }),
        None,
    );
    let findings = x_aggrega_discouragement::classify(&input);
    assert_eq!(findings.len(), 2);
    // BTreeMap-backed serde_json::Map iterates alphabetically:
    // x-aggrega-inject < x-aggrega-selector.
    assert!(findings[0].message.contains("x-aggrega-inject"));
    assert!(findings[0].message.contains("at </x-aggrega-inject>"));
    assert!(findings[1].message.contains("x-aggrega-selector"));
    assert!(findings[1].message.contains("at </x-aggrega-selector>"));
}

#[test]
fn discouragement_nested_key_flagged() {
    let input = fixture(
        json!({
            "type": "object",
            "properties": {
                "foo": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "properties": {
                            "bar": {
                                "x-aggrega-selector": "deep"
                            }
                        }
                    }
                }
            }
        }),
        None,
    );
    let findings = x_aggrega_discouragement::classify(&input);
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains("at </properties/foo/items/properties/bar/x-aggrega-selector>"));
}

#[test]
fn discouragement_duplicate_keys_emit_per_occurrence() {
    // Same key at two different sub-paths must produce TWO findings.
    // Dedup-by-key was intentionally dropped: each occurrence is
    // surfaced independently so operators can locate every
    // instance via the bracketed JSON Pointer.
    let input = fixture(
        json!({
            "type": "object",
            "properties": {
                "left": {"x-aggrega-selector": "a"},
                "right": {"x-aggrega-selector": "b"}
            }
        }),
        None,
    );
    let findings = x_aggrega_discouragement::classify(&input);
    assert_eq!(findings.len(), 2, "expected 2 findings (one per occurrence); got {:?}", findings);
}

#[test]
fn discouragement_policy_flag_suppresses() {
    let policies = Some(Policies {
        allow_direct_x_aggrega_extensions: true,
        ..Default::default()
    });
    let input = fixture(
        json!({
            "type": "object",
            "x-aggrega-selector": "x",
            "x-aggrega-inject": "y"
        }),
        policies,
    );
    let findings = x_aggrega_discouragement::classify(&input);
    assert_eq!(findings.len(), 0, "policy flag must suppress all findings; got {:?}", findings);
}
