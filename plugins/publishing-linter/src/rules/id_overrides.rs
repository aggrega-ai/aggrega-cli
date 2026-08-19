// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reject explicit `$id` declarations at any sub-schema position in
//! SchemaLibrary entries.
//!
//! The platform owns each SchemaLibrary entry's canonical identity. That
//! identity is host-less — the bare library name (`metadata.name`, see
//! `aggrega-cli/pkg/types/schemalibrary.go`) plus a `#/schemas/<name>`
//! fragment — and never embeds a registry host; physical location is
//! bound at resolution time, not in the identity. Author-supplied `$id`
//! declarations conflict with that platform-owned identity; cross-document
//! `$ref` resolution and lockfile pinning assume it.
//!
//! Scope: any sub-schema position at any depth — top-level entries
//! and nested sub-schemas (`anyOf` branches, `properties.*` items,
//! `$defs.*`, etc.). Annotation positions (`examples`, `const`,
//! `default`) are NOT scanned; the canonical-`$id` argument applies
//! to schema positions only.
//!
//! Default: ON — the rule reflects a platform-machinery constraint,
//! not a style preference. Per-workspace opt-out:
//! `allow_id_overrides: true` under `lint.publishing_discipline` in
//! `.aggrega-config.yaml` suppresses every finding from this rule
//! uniformly across the depth.

use aggrega_pdk_rust::{Diagnostic, LinterInput};
use serde_json::Value;

use crate::rules::schema_library_walker::visit_schemas_recursive;

/// `classify` walks every entry under `spec.schemas` and descends
/// into sub-schema positions via the shared walker. Each visited
/// node is checked for `$id`; one finding per occurrence. `rule_id:
/// "id-overrides"`, `severity: "halt"`, no classification.
///
/// Returns an empty vector when the per-workspace flag
/// `allow_id_overrides` is true OR when no entry carries an
/// explicit `$id` at any sub-schema position OR when the entity is
/// not a SchemaLibrary.
pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    let policies = &input.policies;
    if policies.allow_id_overrides {
        return vec![];
    }

    let mut findings = Vec::new();
    visit_schemas_recursive(input, |entry_name, pointer, value| {
        if let Value::Object(map) = value {
            if map.contains_key("$id") {
                let location = if pointer.is_empty() {
                    String::new()
                } else {
                    format!(" at {pointer}")
                };
                findings.push(Diagnostic {
                    rule_id: "id-overrides".to_string(),
                    severity: "halt".to_string(),
                    message: format!(
                        "SchemaLibrary entry {entry_name:?} declares an explicit $id{location}; \
                         the platform owns each entry's host-less canonical identity \
                         (<Library>#/schemas/<name>); author overrides \
                         conflict with cross-document $ref resolution + lockfile pinning"
                    ),
                    ..Default::default()
                });
            }
        }
    });
    findings
}
