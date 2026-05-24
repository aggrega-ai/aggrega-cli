// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Publishing-linter Wasm plugin.
//!
//! Convergence point for the publishing-discipline rule set. At plugin
//! v0.1.0 the only rule registered is `schema-evolution` (PATCH /
//! MINOR / MAJOR classification per VS §2.13). Subsequent rule work
//! adds modules under [`rules`] and registers them in [`lint`]'s
//! dispatch table.

use aggrega_pdk_rust::{LinterFinding, LinterInput, LinterOutput, LinterSeverity};
use extism_pdk::{plugin_fn, FnResult, Json};

pub mod policies;
pub mod rubric;
pub mod rules;

/// `lint` is the plugin's single exported entry point. Dispatches on
/// `LinterInput.rule_id` to the appropriate rule's classifier and
/// returns its findings inside a `LinterOutput`. Unknown rule_ids
/// produce a single `unknown_rule` finding with severity `Error` and
/// no classification — the graceful-degradation pattern that lets
/// subsequent rule WIs register their rules without re-versioning
/// the plugin's exported surface.
#[plugin_fn]
pub fn lint(input: Json<LinterInput>) -> FnResult<Json<LinterOutput>> {
    let input = input.0;
    let findings = match input.rule_id.as_str() {
        "schema-evolution" => rubric::classify(&input),
        "annotation-prefix" => rules::annotation_prefix::classify(&input),
        "name-shape" => rules::name_shape::classify(&input),
        "narrowing" => rules::narrowing::classify(&input),
        "dynamic-refs" => rules::dynamic_refs::classify(&input),
        "id-overrides" => rules::id_overrides::classify(&input),
        "category-matches-directory" => rules::category_vs_directory::classify(&input),
        other => vec![LinterFinding {
            rule_id: "unknown_rule".to_string(),
            severity: LinterSeverity::Error,
            classification: None,
            message: format!("rule_id {other:?} not implemented at this plugin version"),
        }],
    };
    Ok(Json(LinterOutput { findings }))
}
