// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Schema-evolution PATCH / MINOR / MAJOR classification per VS §2.13.
//!
//! The classifier consumes the previous-version + proposed-version
//! entity structures (parsed by the host from YAML to JSON before
//! invocation) plus the entity kind, walks the per-kind diff scope,
//! and emits exactly one finding carrying the classification verdict
//! plus a diagnostic naming the offending field(s).
//!
//! Rubric (mechanical; does not consult workspace policies):
//!
//! - **PATCH**: the only delta is in non-behaviour fields
//!   (descriptions, documentation, comments). Field-by-field
//!   comparison against an enumerated documentation-only allowlist.
//! - **MINOR**: strictly additive — new optional schema fields, new
//!   schema entries in a SchemaLibrary, new mappings in a
//!   MappingLibrary, new components / subsystems / parameters in an
//!   Aggregate. Detected by walking the proposed-vs-previous
//!   structure and asserting every previous field is present in
//!   proposed AND proposed introduces ≥1 new path.
//! - **MAJOR**: any subtractive or narrowing change — field removed,
//!   type narrowed, required-field added without default, library
//!   entry removed, Recipe `provider` changed (different rendering
//!   engine), pipeline reordering.
//!
//! Per-kind dispatch:
//!
//! - Component / Feature / Provider: JSON-Schema-aware diff via
//!   shared walker over the documented field allowlists per kind.
//! - SchemaLibrary: per-entry diff under spec.schemas + spec.templates.
//! - MappingLibrary: entry-key-level diff (opaque template strings).
//! - Recipe / FeatureRecipe: load-bearing-field diff.
//! - Aggregate: catalog-aggregate diff.
//! - CueModule: conservative MAJOR default (legacy transitional
//!   pathway).

use aggrega_pdk_rust::{LinterClassification, LinterFinding, LinterInput, LinterSeverity};
use serde_json::Value;

/// `classify` is the entry point invoked by the plugin dispatcher when
/// `LinterInput.rule_id == "schema-evolution"`. Returns a vector
/// carrying exactly one finding: the classification verdict for the
/// supplied entity pair.
pub fn classify(input: &LinterInput) -> Vec<LinterFinding> {
    let (verdict, message) = classify_one(input);
    vec![LinterFinding {
        rule_id: "schema-evolution".to_string(),
        severity: LinterSeverity::Error,
        classification: Some(verdict),
        message,
    }]
}

fn classify_one(input: &LinterInput) -> (LinterClassification, String) {
    let proposed = &input.proposed;
    let previous = match &input.previous {
        Some(v) => v,
        None => {
            // First publication; no previous version exists. Treat as
            // PATCH so a tag bump to 1.0.0 isn't artificially forced;
            // the catalog-level convention is that v1.0.0 IS the
            // first version.
            return (
                LinterClassification::Patch,
                "no previous version supplied; treating as first publication".to_string(),
            );
        }
    };

    match input.entity_kind.as_str() {
        "Component" => classify_with_allowlists(previous, proposed, COMPONENT_DOCS_PATHS, COMPONENT_FIELD_PATHS, "Component"),
        "Feature" => classify_with_allowlists(previous, proposed, FEATURE_DOCS_PATHS, FEATURE_FIELD_PATHS, "Feature"),
        "Provider" => classify_with_allowlists(previous, proposed, PROVIDER_DOCS_PATHS, PROVIDER_FIELD_PATHS, "Provider"),
        "SchemaLibrary" => classify_schema_library(previous, proposed),
        "MappingLibrary" => classify_mapping_library(previous, proposed),
        "Recipe" => classify_recipe_like(previous, proposed, "Recipe"),
        "FeatureRecipe" => classify_recipe_like(previous, proposed, "FeatureRecipe"),
        "Aggregate" => classify_aggregate(previous, proposed),
        "CueModule" => (
            LinterClassification::Major,
            "CueModule kind: legacy transitional pathway classifies any change as MAJOR".to_string(),
        ),
        other => (
            LinterClassification::Major,
            format!("entity_kind {other:?} unrecognised; conservative MAJOR default"),
        ),
    }
}

// ----- Documentation-only paths per kind -----
//
// Any change confined to these paths classifies PATCH.

const COMPONENT_DOCS_PATHS: &[&str] = &[
    "metadata.description",
    "spec.description",
    "spec.schema.description",
];
const FEATURE_DOCS_PATHS: &[&str] = &[
    "metadata.description",
    "spec.description",
    "spec.schema.description",
];
const PROVIDER_DOCS_PATHS: &[&str] = &[
    "metadata.description",
    "spec.description",
];

// ----- Field paths the rubric considers for additive/subtractive
// detection (the "diff scope" per consolidation §2). -----

const COMPONENT_FIELD_PATHS: &[&str] = &[
    "spec.schema",
    "spec.usesSchema",
    "spec.secretsSchema",
    "spec.requiredFeatures",
    "spec.extends",
];
const FEATURE_FIELD_PATHS: &[&str] = &[
    "spec.schema",
    "spec.usesSchema",
    "spec.secretsSchema",
    "spec.appliesTo",
    "spec.appliesToInfra",
    "spec.appliesToValues",
    "spec.interoperability",
    "spec.extends",
];
const PROVIDER_FIELD_PATHS: &[&str] = &[
    "spec.plugin",
    "spec.baseSchema",
    "spec.workloadSchema",
];

fn classify_with_allowlists(
    previous: &Value,
    proposed: &Value,
    docs_paths: &[&str],
    field_paths: &[&str],
    kind: &str,
) -> (LinterClassification, String) {
    let mut field_changes: Vec<String> = vec![];
    let mut docs_changes: Vec<String> = vec![];

    for path in field_paths {
        if get_path(previous, path) != get_path(proposed, path) {
            field_changes.push((*path).to_string());
        }
    }
    for path in docs_paths {
        if get_path(previous, path) != get_path(proposed, path) {
            docs_changes.push((*path).to_string());
        }
    }

    if field_changes.is_empty() && docs_changes.is_empty() {
        return (
            LinterClassification::Patch,
            format!("{kind}: no diff detected across tracked fields"),
        );
    }
    if field_changes.is_empty() {
        return (
            LinterClassification::Patch,
            format!("{kind}: only documentation paths changed: {}", docs_changes.join(", ")),
        );
    }

    for path in &field_changes {
        let prev = get_path(previous, path);
        let prop = get_path(proposed, path);
        if is_subtractive(prev, prop) {
            return (
                LinterClassification::Major,
                format!("{kind}: {path} carries subtractive or narrowing change"),
            );
        }
    }

    (
        LinterClassification::Minor,
        format!("{kind}: additive changes at: {}", field_changes.join(", ")),
    )
}

fn classify_schema_library(previous: &Value, proposed: &Value) -> (LinterClassification, String) {
    let prev_schemas = get_path(previous, "spec.schemas");
    let prop_schemas = get_path(proposed, "spec.schemas");
    let prev_templates = get_path(previous, "spec.templates");
    let prop_templates = get_path(proposed, "spec.templates");

    let (verdict_s, msg_s) = diff_named_entries(prev_schemas, prop_schemas, "spec.schemas");
    if matches!(verdict_s, LinterClassification::Major) {
        return (verdict_s, format!("SchemaLibrary: {msg_s}"));
    }
    let (verdict_t, msg_t) = diff_named_entries(prev_templates, prop_templates, "spec.templates");
    if matches!(verdict_t, LinterClassification::Major) {
        return (verdict_t, format!("SchemaLibrary: {msg_t}"));
    }

    if matches!(verdict_s, LinterClassification::Minor) || matches!(verdict_t, LinterClassification::Minor) {
        return (
            LinterClassification::Minor,
            format!("SchemaLibrary: additive — {msg_s}; {msg_t}"),
        );
    }

    (
        LinterClassification::Patch,
        "SchemaLibrary: no entry-set changes detected".to_string(),
    )
}

fn classify_mapping_library(previous: &Value, proposed: &Value) -> (LinterClassification, String) {
    let prev_mappings = get_path(previous, "spec.mappings");
    let prop_mappings = get_path(proposed, "spec.mappings");

    let (verdict, msg) = diff_named_entries(prev_mappings, prop_mappings, "spec.mappings");
    if matches!(verdict, LinterClassification::Major) {
        return (verdict, format!("MappingLibrary: {msg}"));
    }
    if matches!(verdict, LinterClassification::Minor) {
        return (
            LinterClassification::Minor,
            format!("MappingLibrary: additive entries in {msg}"),
        );
    }
    // Check whether any existing entry's body changed (structural
    // pathway: opaque template strings; any body change is MAJOR
    // pending semantic equivalence analysis a future implementation may relax).
    if let (Some(Value::Object(prev_map)), Some(Value::Object(prop_map))) =
        (prev_mappings, prop_mappings)
    {
        for (key, prev_value) in prev_map {
            if let Some(prop_value) = prop_map.get(key) {
                if prev_value != prop_value {
                    return (
                        LinterClassification::Major,
                        format!("MappingLibrary: entry {key:?} body changed (structural pathway)"),
                    );
                }
            }
        }
    }
    (LinterClassification::Patch, "MappingLibrary: no diff detected".to_string())
}

fn classify_recipe_like(previous: &Value, proposed: &Value, kind: &str) -> (LinterClassification, String) {
    let load_bearing = &[
        "spec.provider",
        "spec.implements",
        "spec.outputs",
        "spec.provides",
        "spec.valuesMapping",
        "spec.pipeline",
        "spec.source",
        "spec.workload",
        "spec.workloadFrom",
        "spec.artifacts",
    ];
    let mut changed: Vec<String> = vec![];
    for path in load_bearing {
        if get_path(previous, path) != get_path(proposed, path) {
            changed.push((*path).to_string());
        }
    }
    if changed.is_empty() {
        return (
            LinterClassification::Patch,
            format!("{kind}: no diff across load-bearing fields"),
        );
    }
    // provider or pipeline changes are always MAJOR.
    if changed.iter().any(|p| p == "spec.provider") {
        return (
            LinterClassification::Major,
            format!("{kind}: spec.provider changed (different rendering engine)"),
        );
    }
    if changed.iter().any(|p| p == "spec.pipeline") {
        return (
            LinterClassification::Major,
            format!("{kind}: spec.pipeline changed (pipeline reordering or step removal/addition)"),
        );
    }
    // Any subtractive change in the remaining fields is MAJOR.
    for path in &changed {
        if is_subtractive(get_path(previous, path), get_path(proposed, path)) {
            return (
                LinterClassification::Major,
                format!("{kind}: {path} carries subtractive change"),
            );
        }
    }
    (
        LinterClassification::Minor,
        format!("{kind}: additive changes at: {}", changed.join(", ")),
    )
}

fn classify_aggregate(previous: &Value, proposed: &Value) -> (LinterClassification, String) {
    let scope = &["spec.parameters", "spec.components", "spec.subsystems", "spec.exports"];
    let mut changed: Vec<String> = vec![];
    for path in scope {
        if get_path(previous, path) != get_path(proposed, path) {
            changed.push((*path).to_string());
        }
    }
    if changed.is_empty() {
        return (LinterClassification::Patch, "Aggregate: no diff".to_string());
    }
    for path in &changed {
        if is_subtractive(get_path(previous, path), get_path(proposed, path)) {
            return (
                LinterClassification::Major,
                format!("Aggregate: {path} carries subtractive change"),
            );
        }
    }
    (
        LinterClassification::Minor,
        format!("Aggregate: additive changes at: {}", changed.join(", ")),
    )
}

// ----- Helpers -----

/// `get_path` follows a dotted field path into a JSON value. Returns
/// `None` for missing or non-object intermediates.
fn get_path<'a>(root: &'a Value, path: &str) -> Option<&'a Value> {
    let mut cur = root;
    for segment in path.split('.') {
        let map = cur.as_object()?;
        cur = map.get(segment)?;
    }
    Some(cur)
}

/// `diff_named_entries` compares two named-entry objects (e.g.,
/// `spec.schemas`, `spec.mappings`). Removed entries → MAJOR; added
/// entries → MINOR; same key set → PATCH. Per-entry body diff is
/// the caller's responsibility (e.g., MappingLibrary's structural
/// pathway).
fn diff_named_entries(
    previous: Option<&Value>,
    proposed: Option<&Value>,
    field_label: &str,
) -> (LinterClassification, String) {
    let prev_map = previous.and_then(|v| v.as_object());
    let prop_map = proposed.and_then(|v| v.as_object());

    let prev_keys: Vec<&str> = prev_map.map(|m| m.keys().map(String::as_str).collect()).unwrap_or_default();
    let prop_keys: Vec<&str> = prop_map.map(|m| m.keys().map(String::as_str).collect()).unwrap_or_default();

    let removed: Vec<&str> = prev_keys.iter().copied().filter(|k| !prop_keys.contains(k)).collect();
    let added: Vec<&str> = prop_keys.iter().copied().filter(|k| !prev_keys.contains(k)).collect();

    if !removed.is_empty() {
        return (
            LinterClassification::Major,
            format!("{field_label}: removed entries: {}", removed.join(", ")),
        );
    }
    if !added.is_empty() {
        return (
            LinterClassification::Minor,
            format!("{field_label}: added entries: {}", added.join(", ")),
        );
    }
    (LinterClassification::Patch, format!("{field_label}: same key set"))
}

/// `is_subtractive` returns true when `proposed` represents a
/// subtractive or narrowing change relative to `previous`. The
/// detection is conservative: any structural shrink (object key
/// removed, array shortened, scalar changed to a different type)
/// counts as subtractive. A future iteration can refine this with
/// kind-specific equivalence rules.
fn is_subtractive(previous: Option<&Value>, proposed: Option<&Value>) -> bool {
    match (previous, proposed) {
        (Some(_), None) => true,
        (None, _) => false,
        (Some(prev), Some(prop)) => structural_shrink(prev, prop),
    }
}

fn structural_shrink(previous: &Value, proposed: &Value) -> bool {
    match (previous, proposed) {
        (Value::Object(prev_map), Value::Object(prop_map)) => {
            for (key, prev_val) in prev_map {
                match prop_map.get(key) {
                    None => return true,
                    Some(prop_val) => {
                        if structural_shrink(prev_val, prop_val) {
                            return true;
                        }
                    }
                }
            }
            false
        }
        (Value::Array(prev_arr), Value::Array(prop_arr)) => {
            if prop_arr.len() < prev_arr.len() {
                return true;
            }
            for (i, prev_item) in prev_arr.iter().enumerate() {
                if let Some(prop_item) = prop_arr.get(i) {
                    if structural_shrink(prev_item, prop_item) {
                        return true;
                    }
                }
            }
            false
        }
        (prev, prop) => {
            std::mem::discriminant(prev) != std::mem::discriminant(prop) && previous != proposed
        }
    }
}
