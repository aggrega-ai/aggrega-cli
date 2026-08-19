// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Per-entity-kind `metadata.name` shape enforcement.
//!
//! Each catalog entity kind has a documented name pattern. The Go-side
//! source-of-truth lives at `aggrega-cli/pkg/catalog/name_shape.go`,
//! which declares the regex strings and nothing else — no Go code
//! matches against them. This rule is the only place the patterns are
//! applied.
//!
//! The Rust constants below are byte-identical to the Go-declared
//! strings; a symmetry test in the plugin's `tests/` directory reads
//! the Go file as source text and asserts byte-identity. Drift between
//! the two surfaces surfaces at unit-test time.
//!
//! Per-workspace opt-out: `policies.allow_nonstandard_names == true`
//! suppresses the rule entirely. The host fills that policy field from
//! the workspace's `lint.publishing_discipline.allow_nonstandard_names`
//! key, or from the equivalent flag on the lint invocation.

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
                severity: "halt".to_string(),
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
        severity: "halt".to_string(),
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
pub const WORKLOAD_LIBRARY_NAME_SHAPE: &str = r"^[A-Z][A-Za-z0-9]*$";
pub const ARTIFACTS_LIBRARY_NAME_SHAPE: &str = r"^[A-Z][A-Za-z0-9]*$";

/// `NAME_SHAPES` pairs each entity kind with its documented name
/// pattern, and is the only place that pairing is written down. A kind
/// absent from it has no pattern, so the rule reports nothing for a
/// document of that kind — which is why the table is enumerable rather
/// than a match expression: the symmetry test walks it against the
/// Go-side declarations in both directions, so a kind added to one
/// side alone fails rather than silently resolving to "no shape".
pub const NAME_SHAPES: &[(&str, &str)] = &[
    ("Component", COMPONENT_NAME_SHAPE),
    ("Feature", FEATURE_NAME_SHAPE),
    ("Recipe", RECIPE_NAME_SHAPE),
    ("FeatureRecipe", FEATURE_RECIPE_NAME_SHAPE),
    ("Aggregate", AGGREGATE_NAME_SHAPE),
    ("SchemaLibrary", SCHEMA_LIBRARY_NAME_SHAPE),
    ("MappingLibrary", MAPPING_LIBRARY_NAME_SHAPE),
    ("WorkloadLibrary", WORKLOAD_LIBRARY_NAME_SHAPE),
    ("ArtifactsLibrary", ARTIFACTS_LIBRARY_NAME_SHAPE),
];

fn per_kind_pattern(kind: &str) -> Option<&'static str> {
    NAME_SHAPES
        .iter()
        .find(|(k, _)| *k == kind)
        .map(|(_, pattern)| *pattern)
}
