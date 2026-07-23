// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Reject direct authoring of `x-aggrega-selector` or
//! `x-aggrega-inject` keywords in Component / Feature schema
//! content.
//!
//! The platform's recommended authoring paths are the declarative
//! `usesSchema` (dependency sockets) and `secretsSchema` (credential
//! bindings) tables on `spec`. The schema-composition layer
//! synthesises the inline `x-aggrega-*` extension form from those
//! declarative tables; authors writing the extension form directly
//! bypass the declarative surface and lose the validation /
//! tool-assist benefits the layer provides.
//!
//! Build-time peer: `aggrega-cli/pkg/validator/discourage_direct_x_aggrega.go`
//! flags the same construct at warning severity during
//! `aggrega build`. This Wasm rule mirrors the semantics at error
//! severity for `aggrega lint --pre-publish`. One logical rule, two
//! implementations; equivalence pinned by the Go-side test at
//! `aggrega-cli/pkg/validator/x_aggrega_equivalence_test.go`. Both
//! implementations emit one finding per occurrence with the
//! offending key's RFC 6901 JSON Pointer location within the
//! entity's `spec.schema` tree.
//!
//! Default: ON. Per-workspace opt-out:
//! `allow_direct_x_aggrega_extensions: true` under
//! `lint.publishing_discipline` in `.aggrega-config.yaml`.

use aggrega_pdk_rust::{Diagnostic, LinterInput};
use serde_json::Value;

use crate::raw;

pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    let policies = &input.policies;
    if policies.allow_direct_x_aggrega_extensions {
        return vec![];
    }

    let proposed = raw::parse(&input.proposed);
    let schema = match proposed.get("spec").and_then(|s| s.get("schema")) {
        Some(s) => s,
        None => return vec![],
    };
    let entity_name = proposed
        .get("metadata")
        .and_then(|m| m.get("name"))
        .and_then(|n| n.as_str())
        .unwrap_or("");

    let mut findings = Vec::new();
    collect_x_aggrega_occurrences(schema, "", entity_name, &mut findings);
    findings
}

fn collect_x_aggrega_occurrences(
    value: &Value,
    pointer: &str,
    entity_name: &str,
    findings: &mut Vec<Diagnostic>,
) {
    match value {
        Value::Object(map) => {
            for (key, child) in map {
                let child_pointer = format!("{pointer}/{}", escape_pointer_segment(key));
                if key.starts_with("x-aggrega-") {
                    let recommended = recommended_authoring_path(key);
                    // Literal `"<name>"` quoting — matches Go's
                    // %q for ASCII entity names deterministically.
                    // Pointer wrapped in <...> so the parse regex
                    // in the equivalence test can capture YAML keys
                    // containing spaces.
                    findings.push(Diagnostic {
                        rule_id: "x-aggrega-discouragement".to_string(),
                        severity: "error".to_string(),
                        message: format!(
                            "entity \"{entity_name}\" authors {key} at <{child_pointer}> in schema content; \
                             recommended path is {recommended}"
                        ),
                        ..Default::default()
                    });
                }
                collect_x_aggrega_occurrences(child, &child_pointer, entity_name, findings);
            }
        }
        Value::Array(items) => {
            for (idx, item) in items.iter().enumerate() {
                let child_pointer = format!("{pointer}/{idx}");
                collect_x_aggrega_occurrences(item, &child_pointer, entity_name, findings);
            }
        }
        _ => {}
    }
}

fn recommended_authoring_path(key: &str) -> &'static str {
    match key {
        "x-aggrega-selector" => "the usesSchema field's declarative socket table",
        "x-aggrega-inject" => "the usesSchema or secretsSchema declarative tables",
        _ => "the appropriate declarative table on spec",
    }
}

// RFC 6901 token escaping: `~` -> `~0`, `/` -> `~1`.
fn escape_pointer_segment(s: &str) -> String {
    s.replace('~', "~0").replace('/', "~1")
}
