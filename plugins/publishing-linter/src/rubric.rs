// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Schema-evolution PATCH / MINOR / MAJOR classification.
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
//!   entry removed, Recipe `capability` changed (different rendering
//!   engine), pipeline reordering.
//!
//! Per-kind dispatch:
//!
//! - Component / Feature: JSON-Schema-aware diff via
//!   shared walker over the documented field allowlists per kind.
//! - SchemaLibrary: per-entry diff under spec.schemas + spec.templates.
//! - MappingLibrary: entry-key-level diff (opaque template strings).
//! - Recipe / FeatureRecipe: load-bearing-field diff.
//! - Aggregate: catalog-aggregate diff.
//! - Plugin: describe()-surface diff (exports, served capabilities,
//!   provider self-report, ABI version, command surface).

use std::collections::BTreeMap;

use aggrega_pdk_rust::{Diagnostic, LinterInput};
use serde_json::Value;

use crate::raw;

/// `classify` is the entry point invoked by the plugin dispatcher when
/// `LinterInput.rule_id == "schema-evolution"`. Returns a vector
/// carrying exactly one finding: the classification verdict for the
/// supplied entity pair. The verdict string is one of "PATCH" / "MINOR"
/// / "MAJOR".
pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    let (verdict, message) = classify_one(input);
    vec![Diagnostic {
        rule_id: "schema-evolution".to_string(),
        severity: "error".to_string(),
        classification: verdict,
        message,
        ..Default::default()
    }]
}

fn classify_one(input: &LinterInput) -> (String, String) {
    let proposed = raw::parse(&input.proposed);
    let previous = match raw::parse_opt(&input.previous) {
        Some(v) => v,
        None => {
            // First publication; no previous version exists. Treat as
            // PATCH so a tag bump to 1.0.0 isn't artificially forced;
            // the catalog-level convention is that v1.0.0 IS the
            // first version.
            return (
                "PATCH".to_string(),
                "no previous version supplied; treating as first publication".to_string(),
            );
        }
    };
    let previous = &previous;
    let proposed = &proposed;

    match input.entity_kind.as_str() {
        "Component" => classify_with_allowlists(previous, proposed, COMPONENT_DOCS_PATHS, COMPONENT_FIELD_PATHS, "Component"),
        "Feature" => classify_with_allowlists(previous, proposed, FEATURE_DOCS_PATHS, FEATURE_FIELD_PATHS, "Feature"),
        "SchemaLibrary" => classify_schema_library(previous, proposed),
        "MappingLibrary" => classify_mapping_library(previous, proposed),
        "Recipe" => classify_recipe_like(previous, proposed, "Recipe"),
        "FeatureRecipe" => classify_recipe_like(previous, proposed, "FeatureRecipe"),
        "Aggregate" => classify_aggregate(previous, proposed),
        "Plugin" => classify_plugin(previous, proposed),
        other => (
            "MAJOR".to_string(),
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

// ----- Field paths the rubric considers for additive/subtractive
// detection (the "diff scope" per consolidation §2). -----

const COMPONENT_FIELD_PATHS: &[&str] = &[
    "spec.schema",
    "spec.usesSchema",
    "spec.featuresSchema",
    "spec.extends",
];
const FEATURE_FIELD_PATHS: &[&str] = &[
    "spec.schema",
    "spec.usesSchema",
    "spec.featuresSchema",
    "spec.appliesTo",
    "spec.appliesToInfra",
    "spec.appliesToValues",
    "spec.interoperability",
    "spec.extends",
];

fn classify_with_allowlists(
    previous: &Value,
    proposed: &Value,
    docs_paths: &[&str],
    field_paths: &[&str],
    kind: &str,
) -> (String, String) {
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
            "PATCH".to_string(),
            format!("{kind}: no diff detected across tracked fields"),
        );
    }
    if field_changes.is_empty() {
        return (
            "PATCH".to_string(),
            format!("{kind}: only documentation paths changed: {}", docs_changes.join(", ")),
        );
    }

    for path in &field_changes {
        let prev = get_path(previous, path);
        let prop = get_path(proposed, path);
        if is_subtractive(prev, prop) {
            return (
                "MAJOR".to_string(),
                format!("{kind}: {path} carries subtractive or narrowing change"),
            );
        }
    }

    (
        "MINOR".to_string(),
        format!("{kind}: additive changes at: {}", field_changes.join(", ")),
    )
}

fn classify_schema_library(previous: &Value, proposed: &Value) -> (String, String) {
    let prev_schemas = get_path(previous, "spec.schemas");
    let prop_schemas = get_path(proposed, "spec.schemas");
    let prev_templates = get_path(previous, "spec.templates");
    let prop_templates = get_path(proposed, "spec.templates");

    let (verdict_s, msg_s) = diff_named_entries(prev_schemas, prop_schemas, "spec.schemas");
    if verdict_s == "MAJOR" {
        return (verdict_s, format!("SchemaLibrary: {msg_s}"));
    }
    let (verdict_t, msg_t) = diff_named_entries(prev_templates, prop_templates, "spec.templates");
    if verdict_t == "MAJOR" {
        return (verdict_t, format!("SchemaLibrary: {msg_t}"));
    }

    if verdict_s == "MINOR" || verdict_t == "MINOR" {
        return (
            "MINOR".to_string(),
            format!("SchemaLibrary: additive — {msg_s}; {msg_t}"),
        );
    }

    (
        "PATCH".to_string(),
        "SchemaLibrary: no entry-set changes detected".to_string(),
    )
}

fn classify_mapping_library(previous: &Value, proposed: &Value) -> (String, String) {
    let prev_mappings = get_path(previous, "spec.mappings");
    let prop_mappings = get_path(proposed, "spec.mappings");

    let (verdict, msg) = diff_named_entries(prev_mappings, prop_mappings, "spec.mappings");
    if verdict == "MAJOR" {
        return (verdict, format!("MappingLibrary: {msg}"));
    }
    if verdict == "MINOR" {
        return (
            "MINOR".to_string(),
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
                        "MAJOR".to_string(),
                        format!("MappingLibrary: entry {key:?} body changed (structural pathway)"),
                    );
                }
            }
        }
    }
    ("PATCH".to_string(), "MappingLibrary: no diff detected".to_string())
}

fn classify_recipe_like(previous: &Value, proposed: &Value, kind: &str) -> (String, String) {
    let load_bearing = &[
        "spec.capability",
        "spec.implements",
        "spec.outputs",
        "spec.provides",
        "spec.valuesMapping",
        "spec.pipeline",
        "spec.source",
        "spec.workload",
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
            "PATCH".to_string(),
            format!("{kind}: no diff across load-bearing fields"),
        );
    }
    // capability or pipeline changes are always MAJOR.
    if changed.iter().any(|p| p == "spec.capability") {
        return (
            "MAJOR".to_string(),
            format!("{kind}: spec.capability changed (different rendering engine)"),
        );
    }
    if changed.iter().any(|p| p == "spec.pipeline") {
        return (
            "MAJOR".to_string(),
            format!("{kind}: spec.pipeline changed (pipeline reordering or step removal/addition)"),
        );
    }
    // Any subtractive change in the remaining fields is MAJOR.
    for path in &changed {
        if is_subtractive(get_path(previous, path), get_path(proposed, path)) {
            return (
                "MAJOR".to_string(),
                format!("{kind}: {path} carries subtractive change"),
            );
        }
    }
    (
        "MINOR".to_string(),
        format!("{kind}: additive changes at: {}", changed.join(", ")),
    )
}

fn classify_aggregate(previous: &Value, proposed: &Value) -> (String, String) {
    let scope = &["spec.parameters", "spec.components", "spec.subsystems", "spec.exports"];
    let mut changed: Vec<String> = vec![];
    for path in scope {
        if get_path(previous, path) != get_path(proposed, path) {
            changed.push((*path).to_string());
        }
    }
    if changed.is_empty() {
        return ("PATCH".to_string(), "Aggregate: no diff".to_string());
    }
    for path in &changed {
        if is_subtractive(get_path(previous, path), get_path(proposed, path)) {
            return (
                "MAJOR".to_string(),
                format!("Aggregate: {path} carries subtractive change"),
            );
        }
    }
    (
        "MINOR".to_string(),
        format!("Aggregate: additive changes at: {}", changed.join(", ")),
    )
}

/// `classify_plugin` scores a re-published Plugin entity from the change
/// to its describe()-surface, read from the Plugin entity YAML (which the
/// host cross-checks against the guest's live describe() report). The
/// surface spans the declared exports, the served capabilities, the
/// provider self-report, the ABI
/// version, and the declared command surface. Each surface yields an
/// independent verdict; the highest
/// magnitude wins (MAJOR > MINOR > PATCH). A change confined to
/// documentation — `metadata.description`, `spec.description`, an
/// export's free-form `selector`, or a command's or flag's
/// `description` — carries no magnitude and leaves the
/// result at PATCH.
fn classify_plugin(previous: &Value, proposed: &Value) -> (String, String) {
    let checks = [
        classify_plugin_exports(previous, proposed),
        classify_plugin_capabilities(previous, proposed),
        classify_self_report(previous, proposed, "spec.provider"),
        classify_plugin_abi_version(previous, proposed),
        classify_plugin_commands(previous, proposed),
    ];

    let mut minor: Option<String> = None;
    for (verdict, msg) in checks {
        match verdict.as_str() {
            "MAJOR" => return ("MAJOR".to_string(), format!("Plugin: {msg}")),
            "MINOR" => {
                if minor.is_none() {
                    minor = Some(msg);
                }
            }
            _ => {}
        }
    }
    if let Some(msg) = minor {
        return ("MINOR".to_string(), format!("Plugin: {msg}"));
    }
    (
        "PATCH".to_string(),
        "Plugin: only documentation or no describe()-surface changes detected".to_string(),
    )
}

/// `classify_plugin_exports` diffs `spec.exports` — an array of export
/// objects keyed by their `name` field (not a keyed map, so entries are
/// matched by name rather than by object key). A removed export, or a
/// changed `operates`/`scope` on an export present in both versions, is a
/// breaking change (MAJOR). A newly-named export with no removals is
/// additive (MINOR). The per-export `selector` is free-form
/// documentation and does not count as a surface change.
fn classify_plugin_exports(previous: &Value, proposed: &Value) -> (String, String) {
    let prev = array_by_name(get_path(previous, "spec.exports"));
    let prop = array_by_name(get_path(proposed, "spec.exports"));

    for name in prev.keys() {
        if !prop.contains_key(name) {
            return (
                "MAJOR".to_string(),
                format!("spec.exports removed export {name:?}"),
            );
        }
    }
    for (name, prev_export) in &prev {
        if let Some(prop_export) = prop.get(name) {
            for field in ["operates", "scope"] {
                if prev_export.get(field) != prop_export.get(field) {
                    return (
                        "MAJOR".to_string(),
                        format!("spec.exports export {name:?} changed {field} (breaking)"),
                    );
                }
            }
        }
    }
    for name in prop.keys() {
        if !prev.contains_key(name) {
            return (
                "MINOR".to_string(),
                format!("spec.exports added export {name:?}"),
            );
        }
    }
    ("PATCH".to_string(), "spec.exports unchanged".to_string())
}

/// `array_by_name` indexes a JSON array by each entry's `name` field —
/// the shared indexer for the name-keyed describe()-surface arrays
/// (exports, commands, per-command flags). Entries without a string
/// `name` are skipped. The `BTreeMap` keeps the diagnostic deterministic
/// when several entries change at once.
fn array_by_name(value: Option<&Value>) -> BTreeMap<&str, &Value> {
    let mut map = BTreeMap::new();
    if let Some(Value::Array(entries)) = value {
        for entry in entries {
            if let Some(name) = entry.get("name").and_then(Value::as_str) {
                map.insert(name, entry);
            }
        }
    }
    map
}

/// `classify_plugin_commands` diffs `spec.commands` — an array of command
/// objects keyed by their `name` field, each carrying an array of `flags`
/// keyed by flag `name`. The command surface is invocation contract:
/// removing a command, removing a flag, or narrowing a flag's `type`,
/// `default`, or an existing non-empty `shorthand` is breaking (MAJOR).
/// Adding a command, adding a flag, or giving an existing flag a
/// previously-absent `shorthand` widens the surface (MINOR). A command's
/// or flag's `description`, and reordering alone, are free-form
/// documentation and carry no magnitude (PATCH).
fn classify_plugin_commands(previous: &Value, proposed: &Value) -> (String, String) {
    let prev = array_by_name(get_path(previous, "spec.commands"));
    let prop = array_by_name(get_path(proposed, "spec.commands"));

    // MAJOR pass: removed command, removed flag, or a narrowed flag field.
    for name in prev.keys() {
        if !prop.contains_key(name) {
            return (
                "MAJOR".to_string(),
                format!("spec.commands removed command {name:?}"),
            );
        }
    }
    for (name, prev_cmd) in &prev {
        let Some(prop_cmd) = prop.get(name) else {
            continue;
        };
        let prev_flags = array_by_name(prev_cmd.get("flags"));
        let prop_flags = array_by_name(prop_cmd.get("flags"));
        for flag_name in prev_flags.keys() {
            if !prop_flags.contains_key(flag_name) {
                return (
                    "MAJOR".to_string(),
                    format!("spec.commands command {name:?} removed flag {flag_name:?}"),
                );
            }
        }
        for (flag_name, prev_flag) in &prev_flags {
            let Some(prop_flag) = prop_flags.get(flag_name) else {
                continue;
            };
            for field in ["type", "default"] {
                if prev_flag.get(field) != prop_flag.get(field) {
                    return (
                        "MAJOR".to_string(),
                        format!(
                            "spec.commands command {name:?} flag {flag_name:?} changed {field} (breaking)"
                        ),
                    );
                }
            }
            // A non-empty shorthand that is removed or changed breaks
            // existing invocations; a newly-non-empty shorthand is a pure
            // add handled in the MINOR pass below.
            let prev_short = flag_shorthand(prev_flag);
            let prop_short = flag_shorthand(prop_flag);
            if !prev_short.is_empty() && prev_short != prop_short {
                return (
                    "MAJOR".to_string(),
                    format!(
                        "spec.commands command {name:?} flag {flag_name:?} changed shorthand (breaking)"
                    ),
                );
            }
        }
    }

    // MINOR pass: new command, new flag, or a newly-non-empty shorthand.
    for name in prop.keys() {
        if !prev.contains_key(name) {
            return (
                "MINOR".to_string(),
                format!("spec.commands added command {name:?}"),
            );
        }
    }
    for (name, prev_cmd) in &prev {
        let Some(prop_cmd) = prop.get(name) else {
            continue;
        };
        let prev_flags = array_by_name(prev_cmd.get("flags"));
        let prop_flags = array_by_name(prop_cmd.get("flags"));
        for flag_name in prop_flags.keys() {
            if !prev_flags.contains_key(flag_name) {
                return (
                    "MINOR".to_string(),
                    format!("spec.commands command {name:?} added flag {flag_name:?}"),
                );
            }
        }
        for (flag_name, prev_flag) in &prev_flags {
            let Some(prop_flag) = prop_flags.get(flag_name) else {
                continue;
            };
            if flag_shorthand(prev_flag).is_empty() && !flag_shorthand(prop_flag).is_empty() {
                return (
                    "MINOR".to_string(),
                    format!("spec.commands command {name:?} flag {flag_name:?} added shorthand"),
                );
            }
        }
    }

    ("PATCH".to_string(), "spec.commands unchanged".to_string())
}

/// `flag_shorthand` reads a flag's `shorthand` as a string, treating a
/// missing or non-string value as empty.
fn flag_shorthand(flag: &Value) -> &str {
    flag.get("shorthand").and_then(Value::as_str).unwrap_or("")
}

/// `classify_plugin_capabilities` diffs `spec.capabilities` — an array of strings
/// (the plugin's served-capability list, mirrored by describe()'s
/// capabilities). Set semantics: a removed entry narrows the surface
/// (MAJOR); a new entry with no removals widens it (MINOR); reordering
/// alone carries no magnitude (PATCH).
fn classify_plugin_capabilities(previous: &Value, proposed: &Value) -> (String, String) {
    let prev = string_entries(get_path(previous, "spec.capabilities"));
    let prop = string_entries(get_path(proposed, "spec.capabilities"));

    let removed: Vec<&str> = prev.iter().copied().filter(|t| !prop.contains(t)).collect();
    if !removed.is_empty() {
        return (
            "MAJOR".to_string(),
            format!("spec.capabilities removed capability(ies): {}", removed.join(", ")),
        );
    }
    let added: Vec<&str> = prop.iter().copied().filter(|t| !prev.contains(t)).collect();
    if !added.is_empty() {
        return (
            "MINOR".to_string(),
            format!("spec.capabilities added capability(ies): {}", added.join(", ")),
        );
    }
    ("PATCH".to_string(), "spec.capabilities unchanged".to_string())
}

/// `string_entries` collects the string elements of a JSON array,
/// skipping non-string entries. A missing or non-array value yields an
/// empty list.
fn string_entries(value: Option<&Value>) -> Vec<&str> {
    match value {
        Some(Value::Array(entries)) => entries.iter().filter_map(Value::as_str).collect(),
        _ => vec![],
    }
}

/// `classify_plugin_abi_version` diffs `spec.abiVersion`. The ABI version
/// is a hard compatibility boundary: any change is breaking (MAJOR).
fn classify_plugin_abi_version(previous: &Value, proposed: &Value) -> (String, String) {
    let prev = get_path(previous, "spec.abiVersion");
    let prop = get_path(proposed, "spec.abiVersion");
    if prev != prop {
        return (
            "MAJOR".to_string(),
            "spec.abiVersion changed (ABI compatibility boundary)".to_string(),
        );
    }
    ("PATCH".to_string(), "spec.abiVersion unchanged".to_string())
}

/// `classify_self_report` diffs a per-socket self-report object
/// (`spec.capabilities`, `spec.provider`). Every leaf a
/// version carries is a claim: dropping a claim, or altering an existing
/// claim's value — including flipping a boolean capability from `true`
/// to `false` — narrows the surface (MAJOR). A brand-new claim a
/// previous version did not carry is purely additive (MINOR).
///
/// The narrowing walk (`narrow_report`) is deliberately more complete
/// than the generic `structural_shrink` helper: `structural_shrink`
/// treats a same-typed scalar change (a boolean `true` -> `false`, a
/// delimiter string edit) as unchanged, which would misread a dropped
/// capability claim as non-narrowing. Do not fold the two together — the
/// capability rubric requires the leaf-alteration sensitivity that
/// `structural_shrink` intentionally omits.
fn classify_self_report(previous: &Value, proposed: &Value, path: &str) -> (String, String) {
    let prev = get_path(previous, path);
    let prop = get_path(proposed, path);
    if prev == prop {
        return ("PATCH".to_string(), format!("{path} unchanged"));
    }
    let (narrowed, added) = narrow_report(prev, prop);
    if narrowed {
        return (
            "MAJOR".to_string(),
            format!("{path} narrows or alters an existing claim"),
        );
    }
    if added {
        return (
            "MINOR".to_string(),
            format!("{path} adds a new claim"),
        );
    }
    // A non-equal pair always registers as narrowed or added above, so
    // this branch is unreachable; classify conservatively regardless.
    (
        "MAJOR".to_string(),
        format!("{path} carries a non-additive change"),
    )
}

/// `narrow_report` walks a self-report subtree and reports whether the
/// change set narrows an existing claim and/or introduces a new one.
/// Returns `(narrowed, added)`. A key present in `previous` and absent in
/// `proposed`, or an existing leaf whose value changed, counts as
/// narrowing; a key present only in `proposed` counts as an addition.
fn narrow_report(previous: Option<&Value>, proposed: Option<&Value>) -> (bool, bool) {
    match (previous, proposed) {
        (None, None) => (false, false),
        (None, Some(_)) => (false, true),
        (Some(_), None) => (true, false),
        (Some(Value::Object(prev_map)), Some(Value::Object(prop_map))) => {
            let mut narrowed = false;
            let mut added = false;
            for (key, prev_val) in prev_map {
                let (n, a) = narrow_report(Some(prev_val), prop_map.get(key));
                narrowed |= n;
                added |= a;
            }
            for key in prop_map.keys() {
                if !prev_map.contains_key(key) {
                    added = true;
                }
            }
            (narrowed, added)
        }
        (Some(prev_val), Some(prop_val)) => {
            if prev_val == prop_val {
                (false, false)
            } else {
                (true, false)
            }
        }
    }
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
) -> (String, String) {
    let prev_map = previous.and_then(|v| v.as_object());
    let prop_map = proposed.and_then(|v| v.as_object());

    let prev_keys: Vec<&str> = prev_map.map(|m| m.keys().map(String::as_str).collect()).unwrap_or_default();
    let prop_keys: Vec<&str> = prop_map.map(|m| m.keys().map(String::as_str).collect()).unwrap_or_default();

    let removed: Vec<&str> = prev_keys.iter().copied().filter(|k| !prop_keys.contains(k)).collect();
    let added: Vec<&str> = prop_keys.iter().copied().filter(|k| !prev_keys.contains(k)).collect();

    if !removed.is_empty() {
        return (
            "MAJOR".to_string(),
            format!("{field_label}: removed entries: {}", removed.join(", ")),
        );
    }
    if !added.is_empty() {
        return (
            "MINOR".to_string(),
            format!("{field_label}: added entries: {}", added.join(", ")),
        );
    }
    ("PATCH".to_string(), format!("{field_label}: same key set"))
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
