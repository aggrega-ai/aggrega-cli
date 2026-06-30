// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the plugin-default-limits rule.
//!
//! Coverage:
//!  - A Plugin with no spec.runtime block fires one warning.
//!  - A Plugin whose spec.runtime carries non-cap fields only (or
//!    both caps set to zero) fires one warning.
//!  - A Plugin with a non-zero maxMemoryPages is silent.
//!  - A Plugin with a non-zero timeoutMs is silent.
//!  - A non-Plugin kind is silent regardless of runtime.

mod common;

use publishing_linter::rules::plugin_default_limits;
use serde_json::json;

fn plugin(spec: serde_json::Value) -> serde_json::Value {
    json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Plugin",
        "metadata": {"name": "core/example", "version": "1.0.0"},
        "spec": spec,
    })
}

fn base_spec() -> serde_json::Value {
    json!({
        "plugin": "oci://hub.aggrega.ai/plugins/example@sha256:abc",
        "pluginTypes": ["linter"],
        "abiVersion": "v1alpha1",
    })
}

#[test]
fn warns_when_runtime_block_absent() {
    let input = common::fixture("Plugin", plugin(base_spec()), None, None, None);
    let findings = plugin_default_limits::classify(&input);
    assert_eq!(findings.len(), 1, "expected one warning, got {findings:?}");
    assert_eq!(findings[0].rule_id, "plugin-default-limits");
    assert_eq!(findings[0].severity, "warning");
    assert!(
        findings[0].classification.is_empty(),
        "advisory finding carries no classification"
    );
}

#[test]
fn warns_when_runtime_present_but_caps_absent() {
    let mut spec = base_spec();
    spec["runtime"] = json!({
        "hostFunctions": ["log_message"],
        "configKV": {"mode": "strict"},
    });
    let input = common::fixture("Plugin", plugin(spec), None, None, None);
    let findings = plugin_default_limits::classify(&input);
    assert_eq!(findings.len(), 1, "expected one warning, got {findings:?}");
    assert_eq!(findings[0].rule_id, "plugin-default-limits");
    assert_eq!(findings[0].severity, "warning");
}

#[test]
fn warns_when_both_caps_zero() {
    let mut spec = base_spec();
    spec["runtime"] = json!({"maxMemoryPages": 0, "timeoutMs": 0});
    let input = common::fixture("Plugin", plugin(spec), None, None, None);
    let findings = plugin_default_limits::classify(&input);
    assert_eq!(findings.len(), 1, "expected one warning, got {findings:?}");
    assert_eq!(findings[0].rule_id, "plugin-default-limits");
}

#[test]
fn silent_when_max_memory_pages_set() {
    let mut spec = base_spec();
    spec["runtime"] = json!({"maxMemoryPages": 256});
    let input = common::fixture("Plugin", plugin(spec), None, None, None);
    let findings = plugin_default_limits::classify(&input);
    assert!(findings.is_empty(), "expected no findings, got {findings:?}");
}

#[test]
fn silent_when_timeout_ms_set() {
    let mut spec = base_spec();
    spec["runtime"] = json!({"timeoutMs": 3000});
    let input = common::fixture("Plugin", plugin(spec), None, None, None);
    let findings = plugin_default_limits::classify(&input);
    assert!(findings.is_empty(), "expected no findings, got {findings:?}");
}

#[test]
fn silent_for_non_plugin_kind() {
    // A Component with no runtime must not trip the rule.
    let component = json!({
        "apiVersion": "aggrega.ai/v1alpha1",
        "kind": "Component",
        "metadata": {"name": "example", "version": "1.0.0"},
        "spec": {"schema": {"type": "object"}},
    });
    let input = common::fixture("Component", component, None, None, None);
    let findings = plugin_default_limits::classify(&input);
    assert!(findings.is_empty(), "expected no findings, got {findings:?}");
}
