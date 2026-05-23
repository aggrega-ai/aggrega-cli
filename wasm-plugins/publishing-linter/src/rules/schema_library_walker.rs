// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Minimal entry-level walker for SchemaLibrary content. Rules under
//! `src/rules/` that operate on the per-entry shape of a
//! SchemaLibrary consume this walker rather than re-implementing
//! map traversal.
//!
//! API stability: `visit_schemas`'s signature is **additive-only**.
//! Future extensions of this module add new functions (e.g.,
//! `visit_templates`, `visit_recursive_refs`); they MUST NOT modify
//! or remove the existing function. Multiple rule modules depend on
//! the exact signature; reviewers gate against breaking changes.

use aggrega_pdk_rust::LinterInput;
use serde_json::Value;

/// `visit_schemas` iterates the entries under `proposed.spec.schemas`,
/// yielding `(entry_name, entry_value)` pairs. Returns an empty
/// iterator when:
///
///   - The entity kind is not `SchemaLibrary`.
///   - `proposed.spec.schemas` is absent.
///   - `proposed.spec.schemas` is not a JSON object.
///
/// Callers iterate over the result; per-entry policing logic lives
/// in the calling rule module (this walker is intentionally minimal
/// — it provides access, not policy).
pub fn visit_schemas(input: &LinterInput) -> impl Iterator<Item = (&str, &Value)> {
    let entries: Option<&serde_json::Map<String, Value>> = if input.entity_kind == "SchemaLibrary" {
        input
            .proposed
            .get("spec")
            .and_then(|s| s.get("schemas"))
            .and_then(|s| s.as_object())
    } else {
        None
    };
    entries
        .into_iter()
        .flat_map(|m| m.iter().map(|(k, v)| (k.as_str(), v)))
}
