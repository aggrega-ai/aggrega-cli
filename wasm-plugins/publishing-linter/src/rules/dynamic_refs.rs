// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reject `$dynamicAnchor` / `$dynamicRef` constructs at sub-schema
//! positions in SchemaLibrary entries.
//!
//! The platform's compile-time `$ref` expansion machinery
//! (`aggrega-cli/pkg/compactform/`) and the SchemaLibrary canonical-
//! `$id` derivation (`aggrega-cli/pkg/types/schemalibrary_templates.go:
//! CanonicalSchemaID`) cannot resolve dynamic-scope-chain constructs.
//! The Go-side catalog loader rejects them at workspace-load time
//! via `scanForbiddenKeywords` in the same file; the publishing-
//! linter rule mirrors that machinery rejection at publish time so
//! the rejection happens before the artifact reaches a downstream
//! consumer.
//!
//! Scope: sub-schema positions per JSON Schema 2020-12, at any
//! depth — top-level entry body, `anyOf`/`allOf`/`oneOf` branches,
//! `properties.*` values, `items`, `additionalProperties`, etc.
//! Annotation positions (`examples`, `default`, `const`, vendor
//! extensions) are NOT scanned: the compile-time `$ref` expansion
//! machinery doesn't inspect annotation values, so a `$dynamicRef`
//! parked inside `examples` would not break anything.
//!
//! Default: ON — the rule reflects a platform-machinery constraint,
//! not a style preference. Per-workspace opt-out:
//! `allow_dynamic_refs: true` under `lint.publishing_discipline` in
//! `.aggrega-config.yaml`.

use aggrega_pdk_rust::{LinterFinding, LinterInput, LinterSeverity};
use serde_json::Value;

use crate::policies;
use crate::rules::schema_library_walker::visit_schemas_recursive;

/// `classify` walks every entry under `spec.schemas` and descends
/// into JSON Schema 2020-12 sub-schema positions via the shared
/// walker. Each visited node is checked for `$dynamicAnchor` or
/// `$dynamicRef`; one finding per occurrence. `rule_id:
/// "dynamic-refs"`, `severity: Error`, `classification: None`.
///
/// Returns an empty vector when the per-workspace flag
/// `allow_dynamic_refs` is true OR when no offending construct is
/// found at a sub-schema position OR when the entity is not a
/// SchemaLibrary.
pub fn classify(input: &LinterInput) -> Vec<LinterFinding> {
    let policies = policies::effective(input.policies.as_ref());
    if policies.allow_dynamic_refs {
        return vec![];
    }

    let mut findings = Vec::new();
    visit_schemas_recursive(input, |entry_name, pointer, value| {
        if let Value::Object(map) = value {
            for key in &["$dynamicAnchor", "$dynamicRef"] {
                if map.contains_key(*key) {
                    let location = if pointer.is_empty() {
                        String::new()
                    } else {
                        format!(" at {pointer}")
                    };
                    findings.push(LinterFinding {
                        rule_id: "dynamic-refs".to_string(),
                        severity: LinterSeverity::Error,
                        classification: None,
                        message: format!(
                            "SchemaLibrary entry {entry_name:?} uses forbidden keyword {key:?}{location}; \
                             the platform's compile-time $ref expansion cannot resolve \
                             dynamic-scope-chain constructs"
                        ),
                    });
                }
            }
        }
    });
    findings
}
