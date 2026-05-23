// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reject `$dynamicAnchor` / `$dynamicRef` constructs in
//! SchemaLibrary entries.
//!
//! The platform's compile-time `$ref` expansion machinery (see
//! `aggrega-cli/pkg/compactform/`) and the SchemaLibrary canonical-`$id`
//! derivation (see `aggrega-cli/pkg/types/schemalibrary_templates.go:
//! CanonicalSchemaID`) cannot resolve dynamic-scope-chain constructs.
//! The Go-side catalog loader already rejects them at workspace-load
//! time via `ErrForbiddenKeyword` (see `scanForbiddenKeywords` in the
//! same file); the publishing-linter rule mirrors that machinery
//! rejection at publish time so the rejection happens before the
//! artifact reaches a downstream consumer.
//!
//! Default: ON — the rule reflects a platform-machinery constraint,
//! not a style preference. Per-workspace opt-out:
//! `allow_dynamic_refs: true` under `lint.publishing_discipline` in
//! `.aggrega-config.yaml` (intended for experimental contexts where
//! the operator accepts that affected schemas will not compile
//! correctly).

use aggrega_pdk_rust::{LinterFinding, LinterInput, LinterSeverity};
use serde_json::Value;

use crate::policies;
use crate::rules::schema_library_walker::visit_schemas;

/// `classify` walks every entry under `spec.schemas` and recursively
/// scans for `$dynamicAnchor` or `$dynamicRef` keys at any depth.
/// Emits one finding per offending occurrence; `rule_id:
/// "dynamic-refs"`, `severity: Error`, `classification: None`.
///
/// Returns an empty vector when the per-workspace flag
/// `allow_dynamic_refs` is true OR when no offending construct is
/// found OR when the entity is not a SchemaLibrary.
pub fn classify(input: &LinterInput) -> Vec<LinterFinding> {
    let policies = policies::effective(input.policies.as_ref());
    if policies.allow_dynamic_refs {
        return vec![];
    }

    let mut findings = Vec::new();
    for (entry_name, entry_value) in visit_schemas(input) {
        scan_for_dynamic_keywords(entry_name, entry_value, &mut findings);
    }
    findings
}

fn scan_for_dynamic_keywords(entry_name: &str, value: &Value, findings: &mut Vec<LinterFinding>) {
    match value {
        Value::Object(map) => {
            for (key, child) in map {
                if key == "$dynamicAnchor" || key == "$dynamicRef" {
                    findings.push(LinterFinding {
                        rule_id: "dynamic-refs".to_string(),
                        severity: LinterSeverity::Error,
                        classification: None,
                        message: format!(
                            "SchemaLibrary entry {entry_name:?} uses forbidden keyword {key:?}; \
                             the platform's compile-time $ref expansion cannot resolve \
                             dynamic-scope-chain constructs"
                        ),
                    });
                }
                scan_for_dynamic_keywords(entry_name, child, findings);
            }
        }
        Value::Array(items) => {
            for item in items {
                scan_for_dynamic_keywords(entry_name, item, findings);
            }
        }
        _ => {}
    }
}
