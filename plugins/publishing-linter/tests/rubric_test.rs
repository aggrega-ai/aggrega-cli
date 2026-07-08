// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Per-kind × per-classification rubric coverage for the
//! publishing-linter's schema-evolution rule. Fixtures are inline
//! serde_json::json! literals exercising the structured-value
//! pathway directly (no YAML parsing at this layer — the host parses
//! YAML to JSON before invoking the plugin; these tests bypass the
//! Extism boundary and call rubric::classify directly).

mod common;

use aggrega_pdk_rust::LinterInput;
use publishing_linter::rubric;
use serde_json::json;

fn fixture(kind: &str, previous: serde_json::Value, proposed: serde_json::Value) -> LinterInput {
    common::fixture(kind, proposed, Some(previous), None, None)
}

fn classify_once(input: &LinterInput) -> String {
    let findings = rubric::classify(input);
    assert_eq!(findings.len(), 1, "expected exactly one finding");
    let c = findings[0].classification.clone();
    assert!(!c.is_empty(), "classification populated");
    c
}

// ----- Component -----

#[test]
fn component_patch_documentation_only_change() {
    let prev = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "core/api-server", "version": "1.0.0", "description": "first cut"},
        "spec": {"schema": {"values": {"type": "object"}}},
    });
    let prop = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "core/api-server", "version": "1.0.0", "description": "rewritten for clarity"},
        "spec": {"schema": {"values": {"type": "object"}}},
    });
    assert_eq!(classify_once(&fixture("Component", prev, prop)), "PATCH");
}

#[test]
fn component_minor_new_optional_field() {
    let prev = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "core/api-server", "version": "1.0.0"},
        "spec": {"schema": {"values": {"type": "object", "properties": {"port": {"type": "integer"}}}}},
    });
    let prop = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "core/api-server", "version": "1.0.0"},
        "spec": {"schema": {"values": {"type": "object", "properties": {"port": {"type": "integer"}, "host": {"type": "string"}}}}},
    });
    assert_eq!(classify_once(&fixture("Component", prev, prop)), "MINOR");
}

#[test]
fn component_major_field_removed() {
    let prev = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "core/api-server", "version": "1.0.0"},
        "spec": {"schema": {"values": {"type": "object", "properties": {"port": {"type": "integer"}, "host": {"type": "string"}}}}},
    });
    let prop = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "core/api-server", "version": "1.0.0"},
        "spec": {"schema": {"values": {"type": "object", "properties": {"port": {"type": "integer"}}}}},
    });
    assert_eq!(classify_once(&fixture("Component", prev, prop)), "MAJOR");
}

// ----- Feature -----

#[test]
fn feature_patch_description_change() {
    let prev = json!({"kind": "Feature", "metadata": {"description": "old"}, "spec": {"schema": {"values": {"type": "object"}}}});
    let prop = json!({"kind": "Feature", "metadata": {"description": "new"}, "spec": {"schema": {"values": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("Feature", prev, prop)), "PATCH");
}

#[test]
fn feature_minor_appliestoinfra_added() {
    let prev = json!({"kind": "Feature", "spec": {"schema": {}}});
    let prop = json!({"kind": "Feature", "spec": {"schema": {}, "appliesToInfra": ["compute"]}});
    assert_eq!(classify_once(&fixture("Feature", prev, prop)), "MINOR");
}

#[test]
fn feature_major_extends_removed() {
    let prev = json!({"kind": "Feature", "spec": {"schema": {}, "extends": "base/feature"}});
    let prop = json!({"kind": "Feature", "spec": {"schema": {}}});
    assert_eq!(classify_once(&fixture("Feature", prev, prop)), "MAJOR");
}

// ----- SchemaLibrary -----

#[test]
fn schemalibrary_patch_same_entries() {
    let prev = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    let prop = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("SchemaLibrary", prev, prop)), "PATCH");
}

#[test]
fn schemalibrary_minor_entry_added() {
    let prev = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    let prop = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}, "Liveness": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("SchemaLibrary", prev, prop)), "MINOR");
}

#[test]
fn schemalibrary_major_entry_removed() {
    let prev = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}, "Liveness": {"type": "object"}}}});
    let prop = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("SchemaLibrary", prev, prop)), "MAJOR");
}

// ----- MappingLibrary -----

#[test]
fn mappinglibrary_minor_mapping_added() {
    let prev = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image }}"}}});
    let prop = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image }}", "Replicas": "replicas: 1"}}});
    assert_eq!(classify_once(&fixture("MappingLibrary", prev, prop)), "MINOR");
}

#[test]
fn mappinglibrary_major_mapping_removed() {
    let prev = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "a", "Replicas": "b"}}});
    let prop = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "a"}}});
    assert_eq!(classify_once(&fixture("MappingLibrary", prev, prop)), "MAJOR");
}

#[test]
fn mappinglibrary_major_body_changed() {
    let prev = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image }}"}}});
    let prop = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image | quote }}"}}});
    assert_eq!(classify_once(&fixture("MappingLibrary", prev, prop)), "MAJOR");
}

// ----- Recipe -----

#[test]
fn recipe_patch_no_load_bearing_diff() {
    let prev = json!({"kind": "Recipe", "spec": {"target": "helm", "implements": "core/server", "workload": "kind: Workload"}});
    let prop = json!({"kind": "Recipe", "spec": {"target": "helm", "implements": "core/server", "workload": "kind: Workload"}});
    assert_eq!(classify_once(&fixture("Recipe", prev, prop)), "PATCH");
}

#[test]
fn recipe_minor_artifact_added() {
    let prev = json!({"kind": "Recipe", "spec": {"target": "helm", "implements": "core/server", "workload": "kind: Workload", "artifacts": {}}});
    let prop = json!({"kind": "Recipe", "spec": {"target": "helm", "implements": "core/server", "workload": "kind: Workload", "artifacts": {"header": "x"}}});
    assert_eq!(classify_once(&fixture("Recipe", prev, prop)), "MINOR");
}

#[test]
fn recipe_major_target_changed() {
    let prev = json!({"kind": "Recipe", "spec": {"target": "helm", "implements": "core/server", "workload": "kind: Workload"}});
    let prop = json!({"kind": "Recipe", "spec": {"target": "compose", "implements": "core/server", "workload": "kind: Workload"}});
    assert_eq!(classify_once(&fixture("Recipe", prev, prop)), "MAJOR");
}

// ----- FeatureRecipe -----
//
// FeatureRecipe and Recipe share classify_recipe_like dispatch; the
// parity tests pin that the shared code path produces equivalent
// classifications for both kinds. A future change that introduces
// FeatureRecipe-specific divergence would surface here.

#[test]
fn featurerecipe_patch_no_load_bearing_diff() {
    let prev = json!({"kind": "FeatureRecipe", "spec": {"target": "helm", "implements": "core/ingress"}});
    let prop = json!({"kind": "FeatureRecipe", "spec": {"target": "helm", "implements": "core/ingress"}});
    assert_eq!(classify_once(&fixture("FeatureRecipe", prev, prop)), "PATCH");
}

#[test]
fn featurerecipe_minor_artifact_added() {
    let prev = json!({"kind": "FeatureRecipe", "spec": {"target": "helm", "implements": "core/ingress"}});
    let prop = json!({"kind": "FeatureRecipe", "spec": {"target": "helm", "implements": "core/ingress", "artifacts": {"a": "b"}}});
    assert_eq!(classify_once(&fixture("FeatureRecipe", prev, prop)), "MINOR");
}

#[test]
fn featurerecipe_major_target_changed() {
    let prev = json!({"kind": "FeatureRecipe", "spec": {"target": "helm", "implements": "core/ingress"}});
    let prop = json!({"kind": "FeatureRecipe", "spec": {"target": "compose", "implements": "core/ingress"}});
    assert_eq!(classify_once(&fixture("FeatureRecipe", prev, prop)), "MAJOR");
}

// ----- Aggregate -----

#[test]
fn aggregate_patch_no_diff() {
    let prev = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    let prop = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    assert_eq!(classify_once(&fixture("Aggregate", prev, prop)), "PATCH");
}

#[test]
fn aggregate_minor_component_added() {
    let prev = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    let prop = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}, "db": {"type": "core/datastore"}}}});
    assert_eq!(classify_once(&fixture("Aggregate", prev, prop)), "MINOR");
}

#[test]
fn aggregate_major_component_removed() {
    let prev = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}, "db": {"type": "core/datastore"}}}});
    let prop = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    assert_eq!(classify_once(&fixture("Aggregate", prev, prop)), "MAJOR");
}

// ----- First publication -----

#[test]
fn first_publication_classifies_patch() {
    // No previous version supplied → PATCH per the first-publication
    // convention (v1.0.0 is itself the first version; no bump
    // pressure).
    let input = common::fixture(
        "Component",
        json!({"kind": "Component"}),
        None,
        None,
        None,
    );
    assert_eq!(classify_once(&input), "PATCH");
}
