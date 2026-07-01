// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Publishing-linter Wasm plugin.
//!
//! Convergence point for the publishing-discipline rule set. At plugin
//! v0.1.0 the only rule registered is `schema-evolution` (PATCH /
//! MINOR / MAJOR classification per VS §2.13). Subsequent rule work
//! adds modules under [`rules`] and registers them in [`lint_rules`]'s
//! dispatch table.
//!
//! The plugin self-describes via [`describe`]; the host validates that
//! descriptor against its plugin contract before invoking
//! [`lint_rules`].

use aggrega_pdk_rust::{LinterFinding, LinterInput, LinterOutput};
use extism_pdk::{plugin_fn, FnResult, Json};

pub mod policies;
pub mod raw;
pub mod rubric;
pub mod rules;

/// `PLUGIN_DESCRIPTOR` is the static `describe()` self-report the host
/// reads once at load: a v1alpha1 linter plugin whose minimum export,
/// `lint_rules`, is present. The host validates this descriptor against
/// its contract (ABI version, plugin type, minimum export) before
/// invoking any export.
const PLUGIN_DESCRIPTOR: &str = r#"{"abi_version":"v1alpha1","plugin_type":"linter","exports":[{"name":"lint_rules"}],"metadata":{"author":"aggrega","license":"Apache-2.0"}}"#;

/// `describe` is the export every plugin implements to self-describe.
/// It returns the static descriptor the host validates at load before
/// dispatching the `lint_rules` export.
#[plugin_fn]
pub fn describe() -> FnResult<String> {
    Ok(PLUGIN_DESCRIPTOR.to_string())
}

/// `lint_rules` is the plugin's rule-evaluation entry point. Dispatches
/// on `LinterInput.rule_id` to the appropriate rule's classifier and
/// returns its findings inside a `LinterOutput`. Unknown rule_ids
/// produce a single `unknown_rule` finding with severity `"error"` and
/// no classification — the graceful-degradation pattern that lets
/// later rules register without re-versioning the plugin's exported
/// surface.
#[plugin_fn]
pub fn lint_rules(input: Json<LinterInput>) -> FnResult<Json<LinterOutput>> {
    let input = input.0;
    let findings = match input.rule_id.as_str() {
        "schema-evolution" => rubric::classify(&input),
        "annotation-prefix" => rules::annotation_prefix::classify(&input),
        "name-shape" => rules::name_shape::classify(&input),
        "narrowing" => rules::narrowing::classify(&input),
        "dynamic-refs" => rules::dynamic_refs::classify(&input),
        "id-overrides" => rules::id_overrides::classify(&input),
        "category-matches-directory" => rules::category_vs_directory::classify(&input),
        "x-aggrega-discouragement" => rules::x_aggrega_discouragement::classify(&input),
        "plugin-default-limits" => rules::plugin_default_limits::classify(&input),
        other => vec![LinterFinding {
            rule_id: "unknown_rule".to_string(),
            severity: "error".to_string(),
            message: format!("rule_id {other:?} not implemented at this plugin version"),
            ..Default::default()
        }],
    };
    Ok(Json(LinterOutput { findings }))
}
