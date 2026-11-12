// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//! Unit tests for the per-entity-kind name-shape rule. Fixtures
//! cover each catalog kind's documented pattern + at least one
//! negative case + the per-workspace opt-out.

mod common;

use aggrega_pdk_rust::{LinterInput, Policies};
use publishing_linter::rules::name_shape;
use serde_json::json;

fn fixture(kind: &str, name: &str, policies: Option<Policies>) -> LinterInput {
    let proposed = json!({
        "kind": kind,
        "metadata": {"name": name},
    });
    common::fixture(kind, proposed, None, policies, None)
}

fn passes(kind: &str, name: &str) {
    let findings = name_shape::classify(&fixture(kind, name, None));
    assert_eq!(
        findings.len(),
        0,
        "expected {kind} {name:?} to match the documented pattern; got finding {:?}",
        findings.first().map(|f| f.message.as_str())
    );
}

fn rejects(kind: &str, name: &str) {
    let findings = name_shape::classify(&fixture(kind, name, None));
    assert_eq!(findings.len(), 1, "expected {kind} {name:?} to fail name-shape check");
    assert!(findings[0].message.contains(name));
}

#[test]
fn name_shape_component_three_segment_kebab_passes() {
    passes("Component", "core/compute/api-server");
}

#[test]
fn name_shape_component_rejects_non_matching() {
    rejects("Component", "BadName");
    rejects("Component", "core/server"); // only two segments
}

#[test]
fn name_shape_feature_two_segment_kebab_passes() {
    passes("Feature", "core/ingress");
}

#[test]
fn name_shape_feature_rejects_non_matching() {
    rejects("Feature", "BadName");
}

#[test]
fn name_shape_recipe_single_segment_kebab_passes() {
    passes("Recipe", "server");
}

#[test]
fn name_shape_recipe_rejects_non_matching() {
    rejects("Recipe", "BadName");
}

// FeatureRecipe parity tests. Recipe and FeatureRecipe patterns are
// byte-identical today; the parity tests pin both kinds exercise the
// dispatch arm so a future change introducing FeatureRecipe-specific
// divergence would surface immediately.
#[test]
fn name_shape_featurerecipe_single_segment_kebab_passes() {
    passes("FeatureRecipe", "tls-mesh");
}

#[test]
fn name_shape_featurerecipe_rejects_non_matching() {
    rejects("FeatureRecipe", "TLS_Mesh");
}

#[test]
fn name_shape_schemalibrary_pascalcase_passes() {
    passes("SchemaLibrary", "Probes");
    passes("SchemaLibrary", "BaseMappings");
}

#[test]
fn name_shape_schemalibrary_rejects_non_pascalcase() {
    rejects("SchemaLibrary", "lower-case");
    rejects("SchemaLibrary", "not_snake_case_either");
}

#[test]
fn name_shape_mappinglibrary_pascalcase_passes() {
    passes("MappingLibrary", "BaseMappings");
}

#[test]
fn name_shape_mappinglibrary_rejects_non_pascalcase() {
    rejects("MappingLibrary", "lower-case");
}

// Every library kind carries the same PascalCase shape, so each one is
// covered in both directions rather than by one representative. The
// rejecting case is the load-bearing half: a kind with no dispatch
// entry resolves to "no shape" and reports nothing, which satisfies a
// passing assertion while checking the name against nothing at all.
#[test]
fn name_shape_workloadlibrary_pascalcase_passes() {
    passes("WorkloadLibrary", "BaseWorkloads");
}

#[test]
fn name_shape_workloadlibrary_rejects_non_pascalcase() {
    rejects("WorkloadLibrary", "base-workloads");
    rejects("WorkloadLibrary", "not_snake_case_either");
}

#[test]
fn name_shape_artifactlibrary_pascalcase_passes() {
    passes("ArtifactLibrary", "BaseArtifacts");
}

#[test]
fn name_shape_artifactlibrary_rejects_non_pascalcase() {
    rejects("ArtifactLibrary", "base-artifacts");
    rejects("ArtifactLibrary", "not_snake_case_either");
}

#[test]
fn name_shape_policy_flag_suppresses() {
    let policies = Some(Policies {
        allow_nonstandard_names: true,
        ..Default::default()
    });
    let findings = name_shape::classify(&fixture("Component", "BadName", policies));
    assert_eq!(findings.len(), 0, "allow_nonstandard_names: true suppresses the rule");
}

#[test]
fn name_shape_unknown_kind_no_finding() {
    // Any unrecognised kind has no documented pattern; the rule
    // emits no finding.
    let findings = name_shape::classify(&fixture("UnknownKind", "anything", None));
    assert_eq!(findings.len(), 0);
}

#[test]
fn name_shape_missing_name_no_finding() {
    let input = common::fixture(
        "Component",
        json!({"kind": "Component", "metadata": {}}),
        None,
        None,
        None,
    );
    let findings = name_shape::classify(&input);
    assert_eq!(findings.len(), 0);
}
