// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the category-matches-directory rule.
//!
//! Coverage:
//!  - All 15 closed-set values pass when label and path agree.
//!  - Mismatch between path and label fires a finding.
//!  - Enterprise-namespace entities (no /core/ segment) pass silently.
//!  - source_path: None passes silently.
//!  - Missing component.aggrega.ai/category label under core/ fires.
//!  - Non-Component entity kinds pass silently.
//!  - Unknown path segment under core/ (not in the closed set) fires.
//!  - allow_category_mismatches: true suppresses all findings,
//!    including unknown-segment violations (uniform suppression).

mod common;

use aggrega_pdk_rust::Policies;
use publishing_linter::rules::category_vs_directory;
use serde_json::json;

const ALL_CATEGORIES: &[&str] = &[
    "ai-agent",
    "ai-model",
    "ai-tool",
    "ai-pipeline",
    "datastore",
    "gateway",
    "mesh",
    "network",
    "messaging",
    "storage",
    "compute",
    "observability",
    "security",
    "bigdata",
    "devtool",
];

fn component_with_label(category: &str) -> serde_json::Value {
    json!({
        "kind": "Component",
        "metadata": {
            "name": format!("core/{category}/example"),
            "labels": {"component.aggrega.ai/category": category}
        }
    })
}

fn path_for(category: &str) -> String {
    format!("aggrega-hub/aggrega-catalog/component/core/{category}/example/example.yaml")
}

#[test]
fn category_all_closed_set_values_pass_when_matched() {
    // Acceptance criterion 2: each of the 15 closed-set values passes
    // when the path's first directory under core/ and the
    // component.aggrega.ai/category label agree.
    for category in ALL_CATEGORIES {
        let input = common::fixture(
            "Component",
            component_with_label(category),
            None,
            None,
            path_for(category),
        );
        let findings = category_vs_directory::classify(&input);
        assert_eq!(
            findings.len(),
            0,
            "category {category:?} must pass when path and label agree; got {:?}",
            findings.first().map(|f| f.message.as_str())
        );
    }
}

#[test]
fn category_mismatch_between_path_and_label_flagged() {
    // Path says datastore; label says compute.
    let proposed = json!({
        "kind": "Component",
        "metadata": {
            "name": "core/datastore/foo",
            "labels": {"component.aggrega.ai/category": "compute"}
        }
    });
    let input = common::fixture(
        "Component",
        proposed,
        None,
        None,
        "aggrega-hub/aggrega-catalog/component/core/datastore/foo/foo.yaml".to_string(),
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(findings.len(), 1);
    assert_eq!(findings[0].rule_id, "category-matches-directory");
    assert!(findings[0].message.contains("compute"));
    assert!(findings[0].message.contains("datastore"));
}

#[test]
fn category_enterprise_namespace_is_out_of_scope() {
    // Path has no /core/ segment; the rule's scope check passes
    // silently regardless of label.
    let proposed = json!({
        "kind": "Component",
        "metadata": {
            "name": "acme-corp/datastore/foo",
            "labels": {"component.aggrega.ai/category": "compute"}
        }
    });
    let input = common::fixture(
        "Component",
        proposed,
        None,
        None,
        "aggrega-hub/aggrega-catalog/component/acme-corp/datastore/foo/foo.yaml".to_string(),
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(findings.len(), 0, "entities outside core/ are out of rule scope");
}

#[test]
fn category_no_source_path_passes_silently() {
    let input = common::fixture(
        "Component",
        component_with_label("datastore"),
        None,
        None,
        None,
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(findings.len(), 0, "no source_path: rule passes silently");
}

#[test]
fn category_missing_label_under_core_flagged() {
    // Path under core/datastore/; no labels at all.
    let proposed = json!({
        "kind": "Component",
        "metadata": {"name": "core/datastore/foo"}
    });
    let input = common::fixture(
        "Component",
        proposed,
        None,
        None,
        "aggrega-hub/aggrega-catalog/component/core/datastore/foo/foo.yaml".to_string(),
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains("missing"));
    assert!(findings[0].message.contains("datastore"));
}

#[test]
fn category_non_component_entity_kind_passes_silently() {
    // Only Components carry component.aggrega.ai/category.
    let proposed = json!({
        "kind": "Feature",
        "metadata": {"name": "core/ingress/external"}
    });
    let input = common::fixture(
        "Feature",
        proposed,
        None,
        None,
        "aggrega-hub/aggrega-catalog/feature/core/ingress/external/external.yaml".to_string(),
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(findings.len(), 0);
}

#[test]
fn category_unknown_path_segment_flagged() {
    // Path has /core/<unknown-segment>/...; finding cites the unknown
    // segment regardless of label.
    let proposed = json!({
        "kind": "Component",
        "metadata": {
            "name": "core/quantum/foo",
            "labels": {"component.aggrega.ai/category": "quantum"}
        }
    });
    let input = common::fixture(
        "Component",
        proposed,
        None,
        None,
        "aggrega-hub/aggrega-catalog/component/core/quantum/foo/foo.yaml".to_string(),
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(findings.len(), 1);
    assert!(findings[0].message.contains("quantum"));
    assert!(findings[0].message.contains("closed set"));
}

#[test]
fn category_workspace_flag_suppresses_violation() {
    // Path + label mismatch with allow_category_mismatches=true → 0 findings.
    let policies = Some(Policies {
        allow_category_mismatches: true,
        ..Default::default()
    });
    let proposed = json!({
        "kind": "Component",
        "metadata": {
            "name": "core/datastore/foo",
            "labels": {"component.aggrega.ai/category": "compute"}
        }
    });
    let input = common::fixture(
        "Component",
        proposed,
        None,
        policies,
        "aggrega-hub/aggrega-catalog/component/core/datastore/foo/foo.yaml".to_string(),
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(findings.len(), 0, "workspace flag must suppress mismatch findings");
}

#[test]
fn category_workspace_flag_suppresses_unknown_category_too() {
    // Even unknown-segment violations are suppressed by the flag —
    // pins the uniform-suppression invariant.
    let policies = Some(Policies {
        allow_category_mismatches: true,
        ..Default::default()
    });
    let proposed = json!({
        "kind": "Component",
        "metadata": {"name": "core/quantum/foo"}
    });
    let input = common::fixture(
        "Component",
        proposed,
        None,
        policies,
        "aggrega-hub/aggrega-catalog/component/core/quantum/foo/foo.yaml".to_string(),
    );
    let findings = category_vs_directory::classify(&input);
    assert_eq!(
        findings.len(),
        0,
        "workspace flag covers all rule findings uniformly, including unknown-segment violations"
    );
}
