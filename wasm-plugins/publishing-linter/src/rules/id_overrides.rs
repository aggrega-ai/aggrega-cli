// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reject explicit `$id` declarations on SchemaLibrary entries.
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
//! This rule polices entry-top-level `$id`. Sub-schema `$id`
//! overrides under nested `$ref` constructs are not scanned at this
//! WI (out of scope; the entry-top-level case is the canonical
//! violation surface).
//!
//! Default: ON — the rule reflects a platform-machinery constraint,
//! not a style preference. Per-workspace opt-out:
//! `allow_id_overrides: true` under `lint.publishing_discipline` in
//! `.aggrega-config.yaml` (intended for experimental contexts where
//! the operator accepts the consequence).

use aggrega_pdk_rust::{LinterFinding, LinterInput, LinterSeverity};

use crate::policies;
use crate::rules::schema_library_walker::visit_schemas;

/// `classify` walks every entry under `spec.schemas` and checks for
/// an explicit `$id` key at the entry's top level. Emits one finding
/// per offending entry; `rule_id: "id-overrides"`, `severity: Error`,
/// `classification: None`.
///
/// Returns an empty vector when the per-workspace flag
/// `allow_id_overrides` is true OR when no entry carries an explicit
/// `$id` OR when the entity is not a SchemaLibrary.
pub fn classify(input: &LinterInput) -> Vec<LinterFinding> {
    let policies = policies::effective(input.policies.as_ref());
    if policies.allow_id_overrides {
        return vec![];
    }

    let mut findings = Vec::new();
    for (entry_name, entry_value) in visit_schemas(input) {
        if let Some(obj) = entry_value.as_object() {
            if obj.contains_key("$id") {
                findings.push(LinterFinding {
                    rule_id: "id-overrides".to_string(),
                    severity: LinterSeverity::Error,
                    classification: None,
                    message: format!(
                        "SchemaLibrary entry {entry_name:?} declares an explicit $id; \
                         the platform derives the canonical $id per entry \
                         (oci://<host>/<library>@<version>#/schemas/<name>); author overrides \
                         conflict with cross-document $ref resolution + lockfile pinning"
                    ),
                });
            }
        }
    }
    findings
}
