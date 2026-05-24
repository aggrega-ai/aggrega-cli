// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reject explicit `$id` declarations at any sub-schema position in
//! SchemaLibrary entries.
//!
//! The platform owns the canonical `$id` for every SchemaLibrary
//! entry. The derivation lives at
//! `aggrega-cli/pkg/types/schemalibrary_templates.go:
//! CanonicalSchemaID` and produces
//! `oci://<host>/<library>@<version>#/schemas/<name>`. Author-supplied
//! `$id` declarations conflict with that derivation; cross-document
//! `$ref` resolution, lockfile pinning, and registry-based discovery
//! all assume platform-derived canonical IDs.
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

use aggrega_pdk_rust::{LinterFinding, LinterInput, LinterSeverity};
use serde_json::Value;

use crate::policies;
use crate::rules::schema_library_walker::visit_schemas_recursive;

/// `classify` walks every entry under `spec.schemas` and descends
/// into sub-schema positions via the shared walker. Each visited
/// node is checked for `$id`; one finding per occurrence. `rule_id:
/// "id-overrides"`, `severity: Error`, `classification: None`.
///
/// Returns an empty vector when the per-workspace flag
/// `allow_id_overrides` is true OR when no entry carries an
/// explicit `$id` at any sub-schema position OR when the entity is
/// not a SchemaLibrary.
pub fn classify(input: &LinterInput) -> Vec<LinterFinding> {
    let policies = policies::effective(input.policies.as_ref());
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
                findings.push(LinterFinding {
                    rule_id: "id-overrides".to_string(),
                    severity: LinterSeverity::Error,
                    classification: None,
                    message: format!(
                        "SchemaLibrary entry {entry_name:?} declares an explicit $id{location}; \
                         the platform derives the canonical $id per entry \
                         (oci://<host>/<library>@<version>#/schemas/<name>); author overrides \
                         conflict with cross-document $ref resolution + lockfile pinning"
                    ),
                });
            }
        }
    });
    findings
}
