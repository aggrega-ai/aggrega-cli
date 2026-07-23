// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Per-entity-kind `metadata.name` shape enforcement.
//!
//! Each catalog entity kind has a documented name pattern. The Go-side
//! source-of-truth lives at `aggrega-cli/pkg/catalog/name_shape.go`
//! and exports both the regex string constants AND the compiled
//! forms. The compiled forms are consumed by the build-time
//! `linter.wasm` warning path; the string constants are mirrored
//! here in the publishing-linter for the publish-time error path.
//!
//! The Rust constants below are byte-identical to the Go-exported
//! strings; a symmetry test in the plugin's `tests/` directory
//! reads the Go file and asserts byte-identity. Drift between the
//! two surfaces surfaces at unit-test time.
//!
//! Per-workspace opt-out: `policies.allow_nonstandard_names == true`
//! suppresses the rule entirely (matches the workspace-config flag
//! that the build-time `linter.wasm` warning path also honours).

use aggrega_pdk_rust::{Diagnostic, LinterInput};
use regex::Regex;

use crate::raw;

/// `classify` returns at most one finding. Returns an empty vector
/// when the name matches the per-kind pattern, when the rule is
/// suppressed by `allow_nonstandard_names`, or when the entity kind
/// has no documented pattern.
pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    let policies = &input.policies;
    if policies.allow_nonstandard_names {
        return vec![];
    }

    let proposed = raw::parse(&input.proposed);
    let name = match proposed
        .get("metadata")
        .and_then(|m| m.get("name"))
        .and_then(|n| n.as_str())
    {
        Some(s) => s,
        None => return vec![],
    };

    let pattern = match per_kind_pattern(input.entity_kind.as_str()) {
        Some(p) => p,
        None => return vec![],
    };

    // Compile-on-demand. The patterns are static; an embedded
    // regex compiler would amortise compilation across calls, but
    // a publishing-linter invocation typically classifies one entity
    // per call, so the savings are negligible. Compilation failure
    // would only happen on a programmer bug in the patterns above;
    // surface it as an error finding rather than panic.
    let re = match Regex::new(pattern) {
        Ok(r) => r,
        Err(e) => {
            return vec![Diagnostic {
                rule_id: "name-shape".to_string(),
                severity: "error".to_string(),
                message: format!("name-shape rule: regex compile failure: {e}"),
                ..Default::default()
            }];
        }
    };

    if re.is_match(name) {
        return vec![];
    }

    vec![Diagnostic {
        rule_id: "name-shape".to_string(),
        severity: "error".to_string(),
        message: format!(
            "metadata.name {name:?} does not match the documented pattern for {} entities ({pattern})",
            input.entity_kind
        ),
        ..Default::default()
    }]
}

/// Per-kind regex strings, byte-identical to the Go-exported strings
/// in `aggrega-cli/pkg/catalog/name_shape.go`. The symmetry is pinned
/// at unit-test time by `tests/name_shape_symmetry_test.rs`.
pub const COMPONENT_NAME_SHAPE: &str = r"^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$";
pub const FEATURE_NAME_SHAPE: &str = r"^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$";
pub const RECIPE_NAME_SHAPE: &str = r"^[a-z][a-z0-9-]*$";
pub const FEATURE_RECIPE_NAME_SHAPE: &str = r"^[a-z][a-z0-9-]*$";
pub const AGGREGATE_NAME_SHAPE: &str = r"^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$";
pub const SCHEMA_LIBRARY_NAME_SHAPE: &str = r"^[A-Z][A-Za-z0-9]*$";
pub const MAPPING_LIBRARY_NAME_SHAPE: &str = r"^[A-Z][A-Za-z0-9]*$";

fn per_kind_pattern(kind: &str) -> Option<&'static str> {
    match kind {
        "Component" => Some(COMPONENT_NAME_SHAPE),
        "Feature" => Some(FEATURE_NAME_SHAPE),
        "Recipe" => Some(RECIPE_NAME_SHAPE),
        "FeatureRecipe" => Some(FEATURE_RECIPE_NAME_SHAPE),
        "Aggregate" => Some(AGGREGATE_NAME_SHAPE),
        "SchemaLibrary" => Some(SCHEMA_LIBRARY_NAME_SHAPE),
        "MappingLibrary" => Some(MAPPING_LIBRARY_NAME_SHAPE),
        _ => None,
    }
}
