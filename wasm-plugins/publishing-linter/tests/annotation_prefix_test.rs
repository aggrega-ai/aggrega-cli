// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the reserved-prefix discipline rule. Fixtures are
//! inline serde_json::json! literals exercising the rule's classify
//! function directly. Test names follow user-visible voice — they
//! describe the behaviour the rule enforces.

use aggrega_pdk_rust::{LinterInput, Policies};
use publishing_linter::rules::annotation_prefix;
use serde_json::json;

fn fixture(kind: &str, annotations: serde_json::Value, policies: Option<Policies>) -> LinterInput {
    LinterInput {
        rule_id: "annotation-prefix".to_string(),
        entity_kind: kind.to_string(),
        proposed: json!({
            "kind": kind,
            "metadata": {
                "name": "x",
                "annotations": annotations,
            },
        }),
        previous: None,
        parent: None,
        policies,
    }
}

#[test]
fn annotation_prefix_rejects_bare_aggrega_ai_prefix() {
    let input = fixture(
        "Component",
        json!({"aggrega.ai/custom": "x"}),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 1, "expected one finding for bare prefix");
    assert_eq!(findings[0].rule_id, "annotation-prefix");
    assert!(findings[0].message.contains("aggrega.ai/custom"));
}

#[test]
fn annotation_prefix_rejects_multi_segment_bare_aggrega_ai() {
    // Multi-segment forms under the bare prefix are also namespace
    // pollution — the well-known list defines the canonical
    // single-segment shapes; everything else under aggrega.ai/ gets
    // flagged regardless of segment count.
    let input = fixture(
        "Component",
        json!({"aggrega.ai/team/payments/build-info": "y"}),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 1, "multi-segment bare prefix is also namespace pollution");
    assert!(findings[0].message.contains("aggrega.ai/team/payments/build-info"));
}

#[test]
fn annotation_prefix_ignores_non_aggrega_domain() {
    let input = fixture(
        "Component",
        json!({
            "acme.com/build-info": "2026",
            "kubernetes.io/managed-by": "aggrega",
        }),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0, "non-aggrega prefixes are out of rule scope");
}

#[test]
fn annotation_prefix_rejects_feature_aggrega_ai_on_non_feature() {
    let input = fixture(
        "Component",
        json!({"feature.aggrega.ai/policy": "strict"}),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains("feature.aggrega.ai/policy"));
    assert!(findings[0].message.contains("Component"));
}

#[test]
fn annotation_prefix_permits_feature_aggrega_ai_on_feature_kind() {
    let input = fixture(
        "Feature",
        json!({"feature.aggrega.ai/policy": "strict"}),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0, "Feature kind is the canonical context for feature.aggrega.ai/");
}

#[test]
fn annotation_prefix_policy_flag_suppresses_both_reserved_namespaces() {
    let policies = Some(Policies {
        allow_aggrega_ai_prefix: true,
        ..Default::default()
    });
    let input = fixture(
        "Component",
        json!({
            "aggrega.ai/custom": "x",
            "feature.aggrega.ai/policy": "strict",
        }),
        policies,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0, "policy flag suppresses both reserved-namespace checks");
}

#[test]
fn annotation_prefix_well_known_keys_exempted() {
    let input = fixture(
        "Component",
        json!({
            "aggrega.ai/namespace": "production",
            "aggrega.ai/service-account": "api-server",
        }),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0, "well-known annotation keys are exempt from the bare-prefix rule");
}

#[test]
fn annotation_prefix_no_annotations_no_findings() {
    let input = LinterInput {
        rule_id: "annotation-prefix".to_string(),
        entity_kind: "Component".to_string(),
        proposed: json!({"kind": "Component", "metadata": {"name": "x"}}),
        previous: None,
        parent: None,
        policies: None,
    };
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0);
}
