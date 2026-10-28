// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the reserved-prefix discipline rule. Fixtures are
//! inline serde_json::json! literals exercising the rule's classify
//! function directly. Test names follow user-visible voice — they
//! describe the behaviour the rule enforces.

mod common;

use aggrega_pdk_rust::{LinterInput, Policies};
use publishing_linter::rules::annotation_prefix;
use serde_json::json;

fn fixture(kind: &str, annotations: serde_json::Value, policies: Option<Policies>) -> LinterInput {
    let proposed = json!({
        "kind": kind,
        "metadata": {
            "name": "x",
            "annotations": annotations,
        },
    });
    common::fixture(kind, proposed, None, policies, None)
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
            "aggrega.ai/kubernetes-version": "1.31",
        }),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0, "well-known annotation keys are exempt from the bare-prefix rule");
}

#[test]
fn annotation_prefix_cloud_target_keys_exempted() {
    // The three cloud-target keys are live: a provider declares them and an
    // engine reads them on the cross-boundary link path, so a catalog entity
    // that annotates one is authoring correctly and must not be flagged.
    let input = fixture(
        "Recipe",
        json!({
            "aggrega.ai/aws-account": "123456789012",
            "aggrega.ai/aws-region": "us-east-1",
            "aggrega.ai/aws-assume-role-arn": "arn:aws:iam::123456789012:role/peer",
        }),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0, "cloud-target annotation keys are reserved and exempt");
}

#[test]
fn annotation_prefix_reserved_but_unread_keys_exempted() {
    // The register is a reservation, not a support claim: a reserved name stays
    // exempt even while no engine reads it, so the name cannot be taken by an
    // enterprise catalog and collide when the platform makes it live.
    let input = fixture(
        "Recipe",
        json!({
            "aggrega.ai/region": "eu-west-1",
            "aggrega.ai/account-id": "123456789012",
            "aggrega.ai/iam-role": "workload-role",
            "aggrega.ai/stack-name": "my-stack",
            "aggrega.ai/tf-backend": "remote-state",
            "aggrega.ai/cluster-domain": "cluster.local",
            "aggrega.ai/ingress-class": "nginx",
            "aggrega.ai/service-mesh": "istio",
            "aggrega.ai/pod-security-standard": "restricted",
            "aggrega.ai/chart-version": "1.2.3",
            "aggrega.ai/project-name": "acme",
        }),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(
        findings.len(),
        0,
        "a reserved name stays exempt even with no engine reading it"
    );
}

#[test]
fn annotation_prefix_provenance_label_keys_exempted() {
    // Providers stamp these keys on rendered output, so the platform owns the
    // names: a catalog entity that annotates one must not be flagged, and an
    // enterprise catalog cannot take one for a meaning of its own.
    let input = fixture(
        "Recipe",
        json!({
            "aggrega.ai/component-id": "api",
            "aggrega.ai/recipe-uri": "acme/api:1.0.0",
            "aggrega.ai/boundary-path": "prod",
            "aggrega.ai/aggregate-name": "shop",
            "aggrega.ai/compiler-version": "1.0.0",
            "aggrega.ai/provider": "helm",
            "aggrega.ai/provider-version": "1.0.0",
            "aggrega.ai/environment": "production",
            "aggrega.ai/sync-wave": "1",
        }),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0, "provenance label keys are reserved and exempt");
}

#[test]
fn annotation_prefix_unreserved_bare_key_still_flagged_alongside_reserved_ones() {
    // The exemption stays a list, not a blanket pass on the bare prefix: an
    // unreserved key is still namespace pollution even when it sits beside
    // reserved keys in the same annotations block.
    let input = fixture(
        "Recipe",
        json!({
            "aggrega.ai/aws-region": "us-east-1",
            "aggrega.ai/not-a-real-key": "whatever",
        }),
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 1, "only the unreserved key is flagged");
    assert!(findings[0].message.contains("aggrega.ai/not-a-real-key"));
    assert!(
        !findings[0].message.contains("aws-region"),
        "the reserved key must not appear in any finding"
    );
}

#[test]
fn annotation_prefix_no_annotations_no_findings() {
    let input = common::fixture(
        "Component",
        json!({"kind": "Component", "metadata": {"name": "x"}}),
        None,
        None,
        None,
    );
    let findings = annotation_prefix::classify(&input);
    assert_eq!(findings.len(), 0);
}
