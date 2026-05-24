// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the narrowing rule. Exercises:
//!
//!   - Default-OFF disposition (the rule must not fire when
//!     enforce_monotonic_narrowing is false).
//!   - No-parent graceful-pass (when LinterInput.parent is None,
//!     the rule emits nothing).
//!   - Narrowing-direction (child narrows parent): no finding.
//!   - Broadening-direction (child broadens parent): error finding.
//!   - narrowing_exemptions[] suppresses specific paths while
//!     other violations continue to flag.

mod common;

use aggrega_pdk_rust::{LinterInput, NarrowingExemption, Policies};
use publishing_linter::rules::narrowing;
use serde_json::json;

fn enforced() -> Policies {
    Policies {
        enforce_monotonic_narrowing: true,
        ..Default::default()
    }
}

fn fixture(
    kind: &str,
    child_schema: serde_json::Value,
    parent_schema: Option<serde_json::Value>,
    policies: Option<Policies>,
    name: &str,
) -> LinterInput {
    let proposed = json!({
        "kind": kind,
        "metadata": {"name": name},
        "spec": {"schema": child_schema, "extends": "base/component"}
    });
    match parent_schema {
        Some(s) => {
            let parent = json!({
                "kind": kind,
                "metadata": {"name": "base/component"},
                "spec": {"schema": s}
            });
            common::fixture_with_parent(kind, proposed, parent, policies)
        }
        None => common::fixture(kind, proposed, None, policies, None),
    }
}

#[test]
fn narrowing_default_off_suppresses_violation() {
    // Default Policies has enforce_monotonic_narrowing == false.
    // Even an obvious broadening must NOT fire.
    let input = fixture(
        "Component",
        json!({"values": {"a": 1, "b": 2}}),
        Some(json!({"values": {"a": 1}})), // child added "b"
        None, // default policies
        "x",
    );
    let findings = narrowing::classify(&input);
    assert_eq!(findings.len(), 0, "default OFF: rule must not fire without opt-in");
}

#[test]
fn narrowing_no_parent_graceful_pass() {
    let input = fixture(
        "Component",
        json!({"values": {"a": 1}}),
        None, // no parent
        Some(enforced()),
        "x",
    );
    let findings = narrowing::classify(&input);
    assert_eq!(findings.len(), 0, "no parent supplied: rule passes silently");
}

#[test]
fn narrowing_child_narrows_parent_passes() {
    // Child has FEWER fields than parent — narrowing direction.
    let input = fixture(
        "Component",
        json!({"values": {"a": 1}}),
        Some(json!({"values": {"a": 1, "b": 2}})),
        Some(enforced()),
        "x",
    );
    let findings = narrowing::classify(&input);
    assert_eq!(findings.len(), 0, "narrowing direction is permitted");
}

#[test]
fn narrowing_child_broadens_parent_flagged() {
    // Child adds a field the parent doesn't have — broadening.
    let input = fixture(
        "Component",
        json!({"values": {"a": 1, "b": 2}}),
        Some(json!({"values": {"a": 1}})),
        Some(enforced()),
        "x",
    );
    let findings = narrowing::classify(&input);
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].rule_id, "narrowing");
    assert!(findings[0].message.contains("narrowing violation"));
}

#[test]
fn narrowing_annotation_keyword_change_does_not_fire() {
    // Child revises `description` and adds an `examples` entry —
    // both are JSON Schema annotation keywords, carry no constraint
    // shape, and MUST NOT trigger broadening.
    let parent = json!({
        "type": "object",
        "properties": {
            "db_url": {
                "type": "string",
                "description": "Postgres connection URL"
            }
        }
    });
    let child = json!({
        "type": "object",
        "properties": {
            "db_url": {
                "type": "string",
                "description": "Postgres connection URL (revised wording)",
                "examples": ["postgres://localhost:5432/app"]
            }
        }
    });
    let input = fixture("Component", child, Some(parent), Some(enforced()), "x");
    let findings = narrowing::classify(&input);
    assert_eq!(
        findings.len(),
        0,
        "annotation-keyword changes are documentation, not broadening"
    );
}

#[test]
fn narrowing_realistic_schema_constraint_widening_flagged() {
    // Realistic JSON Schema: child loosens `maxLength` constraint —
    // a genuine widening that MUST fire despite the surrounding
    // annotation keywords being present.
    let parent = json!({
        "type": "object",
        "properties": {
            "name": {
                "type": "string",
                "description": "Component name",
                "maxLength": 32
            }
        },
        "required": ["name"]
    });
    let child = json!({
        "type": "object",
        "properties": {
            "name": {
                "type": "string",
                "description": "Component name (clarified)",
                "maxLength": 64
            }
        },
        "required": ["name"]
    });
    let input = fixture("Component", child, Some(parent), Some(enforced()), "x");
    let findings = narrowing::classify(&input);
    assert_eq!(
        findings.len(),
        1,
        "maxLength widening is a real constraint change; annotation siblings must not mask it"
    );
    assert!(findings[0].message.contains("maxLength"));
}

#[test]
fn narrowing_property_named_as_annotation_keyword_still_walks() {
    // A property literally named `examples` (e.g., a Component whose
    // schema exposes a list of usage examples). The annotation-keyword
    // skip is position-aware: `examples` sitting under
    // `properties.<name>` is a user-defined field, NOT the JSON Schema
    // annotation keyword in its native position. Broadening in its
    // subtree MUST fire.
    let parent = json!({
        "type": "object",
        "properties": {
            "examples": {
                "type": "array",
                "items": {"type": "string"},
                "maxItems": 10
            }
        }
    });
    let child = json!({
        "type": "object",
        "properties": {
            "examples": {
                "type": "array",
                "items": {"type": "string"},
                "maxItems": 100
            }
        }
    });
    let input = fixture("Component", child, Some(parent), Some(enforced()), "x");
    let findings = narrowing::classify(&input);
    assert_eq!(
        findings.len(),
        1,
        "property named `examples` is user-defined, not the annotation keyword; broadening in its subtree must fire"
    );
    assert!(findings[0].message.contains("maxItems"));
}

#[test]
fn narrowing_exemption_suppresses_specific_path() {
    // Two violations; one path exempted; the other still fires.
    let policies = Policies {
        enforce_monotonic_narrowing: true,
        narrowing_exemptions: vec![NarrowingExemption {
            entity: "x".to_string(),
            paths: vec!["spec.schema.values.b (added in child)".to_string()],
            reason: "documented exemption".to_string(),
        }],
        ..Default::default()
    };
    let input = fixture(
        "Component",
        json!({"values": {"a": 1, "b": 2, "c": 3}}),
        Some(json!({"values": {"a": 1}})),
        Some(policies),
        "x",
    );
    let findings = narrowing::classify(&input);
    // "b" exempted; "c" still fires.
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains("c (added in child)"));
}
