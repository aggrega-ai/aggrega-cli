// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Per-kind × per-classification rubric coverage for the
//! publishing-linter's schema-evolution rule. Fixtures are inline
//! serde_json::json! literals exercising the structured-value
//! pathway directly (no YAML parsing at this layer — the host parses
//! YAML to JSON before invoking the plugin; these tests bypass the
//! Extism boundary and call rubric::classify directly).

use aggrega_pdk_rust::{LinterClassification, LinterInput};
use publishing_linter::rubric;
use serde_json::json;

fn fixture(kind: &str, previous: serde_json::Value, proposed: serde_json::Value) -> LinterInput {
    LinterInput {
        rule_id: "schema-evolution".to_string(),
        entity_kind: kind.to_string(),
        proposed,
        previous: Some(previous),
        policies: None,
    }
}

fn classify_once(input: &LinterInput) -> LinterClassification {
    let findings = rubric::classify(input);
    assert_eq!(findings.len(), 1, "expected exactly one finding");
    findings[0].classification.expect("classification populated")
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
    assert_eq!(classify_once(&fixture("Component", prev, prop)), LinterClassification::Patch);
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
    assert_eq!(classify_once(&fixture("Component", prev, prop)), LinterClassification::Minor);
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
    assert_eq!(classify_once(&fixture("Component", prev, prop)), LinterClassification::Major);
}

// ----- Feature -----

#[test]
fn feature_patch_description_change() {
    let prev = json!({"kind": "Feature", "metadata": {"description": "old"}, "spec": {"schema": {"values": {"type": "object"}}}});
    let prop = json!({"kind": "Feature", "metadata": {"description": "new"}, "spec": {"schema": {"values": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("Feature", prev, prop)), LinterClassification::Patch);
}

#[test]
fn feature_minor_appliestoinfra_added() {
    let prev = json!({"kind": "Feature", "spec": {"schema": {}}});
    let prop = json!({"kind": "Feature", "spec": {"schema": {}, "appliesToInfra": ["compute"]}});
    assert_eq!(classify_once(&fixture("Feature", prev, prop)), LinterClassification::Minor);
}

#[test]
fn feature_major_extends_removed() {
    let prev = json!({"kind": "Feature", "spec": {"schema": {}, "extends": "base/feature"}});
    let prop = json!({"kind": "Feature", "spec": {"schema": {}}});
    assert_eq!(classify_once(&fixture("Feature", prev, prop)), LinterClassification::Major);
}

// ----- Provider -----

#[test]
fn provider_patch_description_change() {
    let prev = json!({"kind": "Provider", "metadata": {"description": "v1"}, "spec": {"baseSchema": "type: object"}});
    let prop = json!({"kind": "Provider", "metadata": {"description": "v1 (clarified)"}, "spec": {"baseSchema": "type: object"}});
    assert_eq!(classify_once(&fixture("Provider", prev, prop)), LinterClassification::Patch);
}

#[test]
fn provider_minor_workloadschema_added() {
    let prev = json!({"kind": "Provider", "spec": {"baseSchema": "type: object"}});
    let prop = json!({"kind": "Provider", "spec": {"baseSchema": "type: object", "workloadSchema": "type: object"}});
    assert_eq!(classify_once(&fixture("Provider", prev, prop)), LinterClassification::Minor);
}

#[test]
fn provider_major_baseschema_narrowed() {
    let prev = json!({"kind": "Provider", "spec": {"baseSchema": {"a": 1, "b": 2}}});
    let prop = json!({"kind": "Provider", "spec": {"baseSchema": {"a": 1}}});
    assert_eq!(classify_once(&fixture("Provider", prev, prop)), LinterClassification::Major);
}

// ----- SchemaLibrary -----

#[test]
fn schemalibrary_patch_same_entries() {
    let prev = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    let prop = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("SchemaLibrary", prev, prop)), LinterClassification::Patch);
}

#[test]
fn schemalibrary_minor_entry_added() {
    let prev = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    let prop = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}, "Liveness": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("SchemaLibrary", prev, prop)), LinterClassification::Minor);
}

#[test]
fn schemalibrary_major_entry_removed() {
    let prev = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}, "Liveness": {"type": "object"}}}});
    let prop = json!({"kind": "SchemaLibrary", "spec": {"schemas": {"Probe": {"type": "object"}}}});
    assert_eq!(classify_once(&fixture("SchemaLibrary", prev, prop)), LinterClassification::Major);
}

// ----- MappingLibrary -----

#[test]
fn mappinglibrary_minor_mapping_added() {
    let prev = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image }}"}}});
    let prop = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image }}", "Replicas": "replicas: 1"}}});
    assert_eq!(classify_once(&fixture("MappingLibrary", prev, prop)), LinterClassification::Minor);
}

#[test]
fn mappinglibrary_major_mapping_removed() {
    let prev = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "a", "Replicas": "b"}}});
    let prop = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "a"}}});
    assert_eq!(classify_once(&fixture("MappingLibrary", prev, prop)), LinterClassification::Major);
}

#[test]
fn mappinglibrary_major_body_changed() {
    let prev = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image }}"}}});
    let prop = json!({"kind": "MappingLibrary", "spec": {"mappings": {"SimpleValues": "image: {{ .Values.image | quote }}"}}});
    assert_eq!(classify_once(&fixture("MappingLibrary", prev, prop)), LinterClassification::Major);
}

// ----- Recipe -----

#[test]
fn recipe_patch_no_load_bearing_diff() {
    let prev = json!({"kind": "Recipe", "spec": {"provider": "helm", "implements": "core/server", "workload": "kind: Workload"}});
    let prop = json!({"kind": "Recipe", "spec": {"provider": "helm", "implements": "core/server", "workload": "kind: Workload"}});
    assert_eq!(classify_once(&fixture("Recipe", prev, prop)), LinterClassification::Patch);
}

#[test]
fn recipe_minor_artifact_added() {
    let prev = json!({"kind": "Recipe", "spec": {"provider": "helm", "implements": "core/server", "workload": "kind: Workload", "artifacts": {}}});
    let prop = json!({"kind": "Recipe", "spec": {"provider": "helm", "implements": "core/server", "workload": "kind: Workload", "artifacts": {"header": "x"}}});
    assert_eq!(classify_once(&fixture("Recipe", prev, prop)), LinterClassification::Minor);
}

#[test]
fn recipe_major_provider_changed() {
    let prev = json!({"kind": "Recipe", "spec": {"provider": "helm", "implements": "core/server", "workload": "kind: Workload"}});
    let prop = json!({"kind": "Recipe", "spec": {"provider": "compose", "implements": "core/server", "workload": "kind: Workload"}});
    assert_eq!(classify_once(&fixture("Recipe", prev, prop)), LinterClassification::Major);
}

// ----- FeatureRecipe -----
//
// FeatureRecipe and Recipe share classify_recipe_like dispatch; the
// parity tests pin that the shared code path produces equivalent
// classifications for both kinds. A future change that introduces
// FeatureRecipe-specific divergence would surface here.

#[test]
fn featurerecipe_patch_no_load_bearing_diff() {
    let prev = json!({"kind": "FeatureRecipe", "spec": {"provider": "helm", "implements": "core/ingress"}});
    let prop = json!({"kind": "FeatureRecipe", "spec": {"provider": "helm", "implements": "core/ingress"}});
    assert_eq!(classify_once(&fixture("FeatureRecipe", prev, prop)), LinterClassification::Patch);
}

#[test]
fn featurerecipe_minor_artifact_added() {
    let prev = json!({"kind": "FeatureRecipe", "spec": {"provider": "helm", "implements": "core/ingress"}});
    let prop = json!({"kind": "FeatureRecipe", "spec": {"provider": "helm", "implements": "core/ingress", "artifacts": {"a": "b"}}});
    assert_eq!(classify_once(&fixture("FeatureRecipe", prev, prop)), LinterClassification::Minor);
}

#[test]
fn featurerecipe_major_provider_changed() {
    let prev = json!({"kind": "FeatureRecipe", "spec": {"provider": "helm", "implements": "core/ingress"}});
    let prop = json!({"kind": "FeatureRecipe", "spec": {"provider": "compose", "implements": "core/ingress"}});
    assert_eq!(classify_once(&fixture("FeatureRecipe", prev, prop)), LinterClassification::Major);
}

// ----- Aggregate -----

#[test]
fn aggregate_patch_no_diff() {
    let prev = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    let prop = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    assert_eq!(classify_once(&fixture("Aggregate", prev, prop)), LinterClassification::Patch);
}

#[test]
fn aggregate_minor_component_added() {
    let prev = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    let prop = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}, "db": {"type": "core/datastore"}}}});
    assert_eq!(classify_once(&fixture("Aggregate", prev, prop)), LinterClassification::Minor);
}

#[test]
fn aggregate_major_component_removed() {
    let prev = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}, "db": {"type": "core/datastore"}}}});
    let prop = json!({"kind": "Aggregate", "spec": {"components": {"api": {"type": "core/server"}}}});
    assert_eq!(classify_once(&fixture("Aggregate", prev, prop)), LinterClassification::Major);
}

// ----- CueModule -----

#[test]
fn cuemodule_major_default() {
    // Per Decision 5: legacy transitional pathway classifies ANY change as MAJOR.
    let prev = json!({"kind": "CueModule", "spec": {"contents": "package x"}});
    let prop = json!({"kind": "CueModule", "spec": {"contents": "package x\n// added comment\n"}});
    assert_eq!(classify_once(&fixture("CueModule", prev, prop)), LinterClassification::Major);
}

// ----- First publication -----

#[test]
fn first_publication_classifies_patch() {
    // No previous version supplied → PATCH per the first-publication
    // convention (v1.0.0 is itself the first version; no bump
    // pressure).
    let input = LinterInput {
        rule_id: "schema-evolution".to_string(),
        entity_kind: "Component".to_string(),
        proposed: json!({"kind": "Component"}),
        previous: None,
        policies: None,
    };
    assert_eq!(classify_once(&input), LinterClassification::Patch);
}
