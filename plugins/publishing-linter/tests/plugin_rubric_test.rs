// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Plugin-kind schema-evolution rubric coverage. The classifier scores a
//! re-published Plugin entity from the change to its describe()-surface —
//! exports, capabilities, provider self-report, ABI
//! version, and the declared command surface. Fixtures are inline
//! serde_json::json! literals exercising the
//! structured-value pathway directly (the host parses YAML to JSON before
//! invoking the plugin; these tests bypass the Extism boundary and call
//! rubric::classify directly).

mod common;

use aggrega_pdk_rust::LinterInput;
use publishing_linter::rubric;
use serde_json::json;

fn fixture(previous: serde_json::Value, proposed: serde_json::Value) -> LinterInput {
    common::fixture("Plugin", proposed, Some(previous), None, None)
}

fn classify_once(input: &LinterInput) -> String {
    let findings = rubric::classify(input);
    assert_eq!(findings.len(), 1, "expected exactly one finding");
    let c = findings[0].classification.clone();
    assert!(!c.is_empty(), "classification populated");
    c
}

// ----- exports -----

#[test]
fn plugin_major_export_removed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"exports": [
            {"name": "render_node", "operates": "emit", "scope": "own"},
            {"name": "patch_node", "operates": "patch", "scope": "own"},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"exports": [
            {"name": "render_node", "operates": "emit", "scope": "own"},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_export_added() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"exports": [
            {"name": "render_node", "operates": "emit", "scope": "own"},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"exports": [
            {"name": "render_node", "operates": "emit", "scope": "own"},
            {"name": "patch_node", "operates": "patch", "scope": "own"},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_major_export_operates_changed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"exports": [{"name": "render_node", "operates": "emit", "scope": "own"}]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"exports": [{"name": "render_node", "operates": "patch", "scope": "own"}]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

// ----- documentation-only -----

#[test]
fn plugin_patch_metadata_description_change() {
    let prev = json!({
        "kind": "Plugin",
        "metadata": {"name": "core/render-helm", "description": "first cut"},
        "spec": {"exports": [{"name": "render_node", "operates": "emit", "scope": "own"}]},
    });
    let prop = json!({
        "kind": "Plugin",
        "metadata": {"name": "core/render-helm", "description": "rewritten for clarity"},
        "spec": {"exports": [{"name": "render_node", "operates": "emit", "scope": "own"}]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "PATCH");
}

#[test]
fn plugin_patch_selector_only_change() {
    // An export's `selector` is free-form documentation; editing it is not
    // a surface change.
    let prev = json!({
        "kind": "Plugin",
        "spec": {"exports": [
            {"name": "render_node", "operates": "emit", "scope": "own", "selector": "renders a node"},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"exports": [
            {"name": "render_node", "operates": "emit", "scope": "own", "selector": "renders a single node to a document"},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "PATCH");
}

// ----- served capabilities -----

#[test]
fn plugin_major_capabilities_narrowed() {
    let prev = json!({"kind": "Plugin", "spec": {"capabilities": ["helm", "compose"]}});
    let prop = json!({"kind": "Plugin", "spec": {"capabilities": ["helm"]}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_capabilities_widened() {
    let prev = json!({"kind": "Plugin", "spec": {"capabilities": ["helm"]}});
    let prop = json!({"kind": "Plugin", "spec": {"capabilities": ["helm", "compose"]}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

// ----- provider contract -----

#[test]
fn plugin_major_provider_contract_narrowed_bool_flip() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"provider": {"contract": {"inputKinds": {"sourced": true, "synthesized": true}}}},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"provider": {"contract": {"inputKinds": {"sourced": true, "synthesized": false}}}},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_provider_contract_new_claim() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"provider": {"contract": {"inputKinds": {"sourced": true}}}},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"provider": {"contract": {"inputKinds": {"sourced": true}, "boundaryAssembly": true}}},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

// ----- ABI version -----

#[test]
fn plugin_major_abi_version_changed() {
    // The live schema enum carries a single ABI value today; the pair
    // still exercises the comparison so the rule is proven independent of
    // the current enum cardinality.
    let prev = json!({"kind": "Plugin", "spec": {"abiVersion": "v1alpha1"}});
    let prop = json!({"kind": "Plugin", "spec": {"abiVersion": "v1alpha2"}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

// ----- command surface -----

#[test]
fn plugin_major_command_removed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build"},
            {"name": "lint"},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build"},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_major_command_flag_removed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [
                {"name": "output", "type": "string"},
                {"name": "verbose", "type": "bool"},
            ]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [
                {"name": "output", "type": "string"},
            ]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_major_command_flag_type_changed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string"}]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "int"}]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_major_command_flag_default_changed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string", "default": "out"}]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string", "default": "dist"}]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_major_command_flag_shorthand_removed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string", "shorthand": "o"}]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string"}]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_major_command_flag_shorthand_changed() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string", "shorthand": "o"}]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string", "shorthand": "f"}]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_command_flag_shorthand_added() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string"}]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string", "shorthand": "o"}]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_minor_command_added() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [{"name": "build"}]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [{"name": "build"}, {"name": "lint"}]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_minor_command_flag_added() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [{"name": "output", "type": "string"}]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [
                {"name": "output", "type": "string"},
                {"name": "verbose", "type": "bool"},
            ]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_patch_command_description_only_change() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [{"name": "build", "description": "builds the thing"}]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [{"name": "build", "description": "assembles the thing"}]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "PATCH");
}

#[test]
fn plugin_patch_command_flag_description_only_change() {
    let prev = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [
                {"name": "output", "type": "string", "description": "output path"},
            ]},
        ]},
    });
    let prop = json!({
        "kind": "Plugin",
        "spec": {"commands": [
            {"name": "build", "flags": [
                {"name": "output", "type": "string", "description": "where output is written"},
            ]},
        ]},
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "PATCH");
}

#[test]
fn plugin_major_command_change_folds_over_patch_surfaces() {
    // A breaking command change must win the fold-in even when every other
    // describe()-surface (exports, capabilities, provider, ABI) is at PATCH.
    let prev = json!({
        "kind": "Plugin",
        "metadata": {"description": "first cut"},
        "spec": {
            "exports": [{"name": "render_node", "operates": "emit", "scope": "own"}],
            "capabilities": ["helm"],
            "abiVersion": "v1alpha1",
            "commands": [{"name": "build"}, {"name": "lint"}],
        },
    });
    let prop = json!({
        "kind": "Plugin",
        "metadata": {"description": "rewritten"},
        "spec": {
            "exports": [{"name": "render_node", "operates": "emit", "scope": "own"}],
            "capabilities": ["helm"],
            "abiVersion": "v1alpha1",
            "commands": [{"name": "build"}],
        },
    });
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

// ----- byte-identical -----

#[test]
fn plugin_patch_no_diff() {
    let entity = json!({
        "kind": "Plugin",
        "spec": {"exports": [{"name": "render_node", "operates": "emit", "scope": "own"}]},
    });
    assert_eq!(classify_once(&fixture(entity.clone(), entity)), "PATCH");
}
