// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Publishing-linter Wasm plugin.
//!
//! Convergence point for the publishing-discipline rule set. At plugin
//! v0.1.0 the only rule registered is `schema-evolution` (PATCH /
//! MINOR / MAJOR classification of a schema-evolution change). Subsequent rule work
//! adds modules under [`rules`] and registers them in [`lint_rules`]'s
//! dispatch table.
//!
//! The plugin self-describes via [`describe`]; the host validates that
//! descriptor against its plugin contract before invoking
//! [`lint_rules`].
//!
//! The linter socket carries canonical CBOR in both directions, so
//! [`lint_rules`] takes and returns bytes, reading its input with
//! `LinterInput::decode_cbor` and writing its output with
//! `LinterOutput::encode_cbor`.

use aggrega_pdk_rust::{
    describe, Diagnostic, LinterInput, LinterOutput, PluginDescriptor, PluginDescriptorMeta,
    PluginExport, API_VERSION,
};
use extism_pdk::{plugin_fn, FnResult};

pub mod policies;
pub mod raw;
pub mod rubric;
pub mod rules;
pub mod sensitivity;

/// `descriptor` is the `describe()` self-report the host reads once at load:
/// a v1alpha1 linter plugin whose minimum export, `lint_rules`, is present.
/// The host validates it against its contract (ABI version, plugin type,
/// minimum export) before invoking any export.
fn descriptor() -> PluginDescriptor {
    PluginDescriptor {
        abi_version: API_VERSION.to_string(),
        plugin_type: "linter".to_string(),
        exports: vec![PluginExport {
            name: "lint_rules".to_string(),
            ..Default::default()
        }],
        metadata: Some(Box::new(PluginDescriptorMeta {
            author: "aggrega".to_string(),
            license: "Apache-2.0".to_string(),
            ..Default::default()
        })),
        ..Default::default()
    }
}

/// `describe` is the export every plugin implements to self-describe. The
/// `describe!` macro encodes the descriptor and folds the optional
/// version/commit build-provenance stamp from the `AGGREGA_VERSION` /
/// `AGGREGA_COMMIT` compile-time environment variables into its metadata; an
/// unstamped build omits both, so its descriptor is byte-identical to a
/// stamped build's minus the provenance. The host validates the returned
/// descriptor at load before dispatching the `lint_rules` export.
#[plugin_fn]
pub fn describe() -> FnResult<String> {
    Ok(describe!(descriptor()))
}

/// `lint_rules` is the plugin's rule-evaluation entry point. Dispatches
/// on `LinterInput.rule_id` to the appropriate rule's classifier and
/// returns its findings inside a `LinterOutput`. Unknown rule_ids
/// produce a single `unknown_rule` finding with severity `"halt"` and
/// no classification — the graceful-degradation pattern that lets
/// later rules register without re-versioning the plugin's exported
/// surface.
#[plugin_fn]
pub fn lint_rules(input: Vec<u8>) -> FnResult<Vec<u8>> {
    let input = LinterInput::decode_cbor(&input)?;
    let findings = match input.rule_id.as_str() {
        "schema-evolution" => rubric::classify(&input),
        "annotation-prefix" => rules::annotation_prefix::classify(&input),
        "name-shape" => rules::name_shape::classify(&input),
        "dynamic-refs" => rules::dynamic_refs::classify(&input),
        "id-overrides" => rules::id_overrides::classify(&input),
        "category-matches-directory" => rules::category_vs_directory::classify(&input),
        "x-aggrega-discouragement" => rules::x_aggrega_discouragement::classify(&input),
        "plugin-default-limits" => rules::plugin_default_limits::classify(&input),
        "secret-slot-declares-sensitive" => {
            rules::secret_slot_declares_sensitive::classify(&input)
        }
        "provides-declared-by-component" => {
            rules::provides_declared_by_component::classify(&input)
        }
        "connection-credential-matches-target-key" => {
            rules::connection_credential_matches_target_key::classify(&input)
        }
        other => vec![Diagnostic {
            rule_id: "unknown_rule".to_string(),
            severity: "halt".to_string(),
            message: format!("rule_id {other:?} not implemented at this plugin version"),
            ..Default::default()
        }],
    };
    let out = LinterOutput {
        findings,
        ..Default::default()
    };
    Ok(out.encode_cbor()?)
}
