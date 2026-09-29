// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Warn when a published Plugin entity declares no explicit runtime
//! resource cap.
//!
//! A Plugin's `spec.runtime` block carries `maxMemoryPages` and
//! `timeoutMs` — the two resource caps the host applies when loading
//! and running the plugin. When neither cap is set (the `spec.runtime`
//! block is absent, or present but both caps are absent or zero), the
//! plugin runs under the platform default caps rather than caps the
//! author chose. That is a safe default, not an error — so this rule
//! emits a non-halting `warning` to make the author aware that the
//! defaults are in effect and that they can pin explicit caps.
//!
//! The rule scopes itself to `Plugin` entities and stays silent for
//! every other kind, and silent for a Plugin that already declares a
//! non-zero cap on either axis.

use aggrega_pdk_rust::{Diagnostic, LinterInput};

use crate::raw;

pub fn classify(input: &LinterInput) -> Vec<Diagnostic> {
    if input.entity_kind != "Plugin" {
        return vec![];
    }
    let proposed = raw::parse(&input.proposed);
    let runtime = proposed.get("spec").and_then(|s| s.get("runtime"));
    // An explicit cap is a positive integer on either axis; zero or
    // absent means the platform default applies (per the runtime
    // schema), which is exactly what this advisory flags.
    let has_explicit_cap = runtime.is_some_and(|rt| {
        let positive = |key: &str| {
            rt.get(key)
                .and_then(serde_json::Value::as_u64)
                .is_some_and(|n| n > 0)
        };
        positive("maxMemoryPages") || positive("timeoutMs")
    });
    if has_explicit_cap {
        return vec![];
    }
    vec![Diagnostic {
        rule_id: "plugin-default-limits".to_string(),
        severity: "warn".to_string(),
        message: "Plugin declares no explicit runtime cap (spec.runtime.maxMemoryPages \
                  and spec.runtime.timeoutMs are absent or zero); the platform default \
                  memory and per-call execution-time caps apply. Set spec.runtime.maxMemoryPages \
                  and spec.runtime.timeoutMs to pin explicit caps."
            .to_string(),
        ..Default::default()
    }]
}
