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

/// `diagnose_once` returns the finding's message rather than its verdict, so
/// a test can assert what the author is told and not merely which bump is
/// demanded. The two are separate guarantees: a verdict can be right for the
/// wrong reason, and a message naming a key the entity does not carry sends
/// the author looking in the wrong place.
fn diagnose_once(input: &LinterInput) -> String {
    let findings = rubric::classify(input);
    assert_eq!(findings.len(), 1, "expected exactly one finding");
    findings[0].message.clone()
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

// ----- served capabilities, per plugin type -----
//
// Each block below spells its path out literally rather than deriving it
// from the rubric's own block set. That duplication is deliberate: it is an
// independent cross-check of the paths against the entity schema, and
// folding it into the constant the classifier reads would blind these tests
// to exactly the failure they exist to catch — a capability list the
// classifier no longer looks at, which scores every change as PATCH while
// reading like a clean verdict.

#[test]
fn plugin_major_provider_capability_removed() {
    let prev =
        json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm", "compose"]}}});
    let prop = json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_provider_capability_added() {
    // The added case is the one a path the classifier cannot resolve gets
    // wrong in BOTH directions: the capability arm falls silent, and the
    // provider self-report arm reads any edited array as a narrowing, so
    // widening the surface reports a breaking change unless the two arms
    // are reconciled.
    let prev = json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm"]}}});
    let prop =
        json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm", "compose"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_patch_provider_capability_reordered() {
    let prev =
        json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm", "compose"]}}});
    let prop =
        json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["compose", "helm"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "PATCH");
}

#[test]
fn plugin_major_pipeline_step_capability_removed() {
    let prev =
        json!({"kind": "Plugin", "spec": {"pipelineStep": {"capabilities": ["helm", "compose"]}}});
    let prop = json!({"kind": "Plugin", "spec": {"pipelineStep": {"capabilities": ["helm"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_pipeline_step_capability_added() {
    let prev = json!({"kind": "Plugin", "spec": {"pipelineStep": {"capabilities": ["helm"]}}});
    let prop =
        json!({"kind": "Plugin", "spec": {"pipelineStep": {"capabilities": ["helm", "compose"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_major_parser_capability_removed() {
    let prev = json!({"kind": "Plugin", "spec": {"parser": {"capabilities": ["parse:yaml", "parse:json"]}}});
    let prop = json!({"kind": "Plugin", "spec": {"parser": {"capabilities": ["parse:yaml"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_parser_capability_added() {
    let prev = json!({"kind": "Plugin", "spec": {"parser": {"capabilities": ["parse:yaml"]}}});
    let prop = json!({"kind": "Plugin", "spec": {"parser": {"capabilities": ["parse:yaml", "parse:json"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_major_importer_capability_removed() {
    let prev = json!({"kind": "Plugin", "spec": {"importer": {"capabilities": ["import:langflow", "import:n8n"]}}});
    let prop =
        json!({"kind": "Plugin", "spec": {"importer": {"capabilities": ["import:langflow"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_minor_importer_capability_added() {
    let prev =
        json!({"kind": "Plugin", "spec": {"importer": {"capabilities": ["import:langflow"]}}});
    let prop = json!({"kind": "Plugin", "spec": {"importer": {"capabilities": ["import:langflow", "import:n8n"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_major_removal_in_one_block_outranks_addition_in_another() {
    // A removal anywhere outranks an addition anywhere, so the block order
    // in which the two appear must not decide the verdict.
    let prev = json!({"kind": "Plugin", "spec": {
        "provider": {"capabilities": ["helm"]},
        "parser": {"capabilities": ["parse:yaml", "parse:json"]},
    }});
    let prop = json!({"kind": "Plugin", "spec": {
        "provider": {"capabilities": ["helm", "compose"]},
        "parser": {"capabilities": ["parse:yaml"]},
    }});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_major_capability_moved_between_blocks() {
    // A capability relocated from one block to another is a removal from the
    // block that no longer claims it: the two blocks carry different senses,
    // so the sense that lost the claim narrowed.
    let prev = json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm"]}}});
    let prop = json!({"kind": "Plugin", "spec": {"pipelineStep": {"capabilities": ["helm"]}}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
}

#[test]
fn plugin_patch_capability_only_block_dropped_when_list_empty() {
    // A block carrying only an empty capability list makes no claim, so
    // omitting it is not a narrowing — neither of the capability list (it
    // was empty) nor of the provider self-report (it held no claims).
    let prev = json!({"kind": "Plugin", "spec": {"provider": {"capabilities": []}}});
    let prop = json!({"kind": "Plugin", "spec": {}});
    assert_eq!(classify_once(&fixture(prev, prop)), "PATCH");
}

#[test]
fn plugin_minor_provider_capability_added_alongside_unchanged_contract() {
    // The provider block holds both a capability list and the render
    // contract. Widening the list while the contract stands still must not
    // borrow the contract arm's narrowing verdict.
    let prev = json!({"kind": "Plugin", "spec": {"provider": {
        "capabilities": ["helm"],
        "contract": {"inputKinds": {"sourced": true}},
    }}});
    let prop = json!({"kind": "Plugin", "spec": {"provider": {
        "capabilities": ["helm", "compose"],
        "contract": {"inputKinds": {"sourced": true}},
    }}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MINOR");
}

#[test]
fn plugin_capability_diagnostic_names_the_block_the_change_landed_in() {
    // The verdict alone cannot distinguish a capability change scored by the
    // capability rubric from one scored as a provider self-report narrowing:
    // both read MAJOR on a removal. The message is what separates them, and
    // it must name the block whose list changed so the author knows which
    // sense narrowed and where to look.
    let prev = json!({"kind": "Plugin", "spec": {"parser": {"capabilities": ["parse:yaml", "parse:json"]}}});
    let prop = json!({"kind": "Plugin", "spec": {"parser": {"capabilities": ["parse:yaml"]}}});
    let message = diagnose_once(&fixture(prev, prop));
    assert!(
        message.contains("spec.parser.capabilities") && message.contains("parse:json"),
        "diagnostic should name the block path and the dropped capability; got: {message}"
    );

    let prev = json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm"]}}});
    let prop =
        json!({"kind": "Plugin", "spec": {"provider": {"capabilities": ["helm", "compose"]}}});
    let message = diagnose_once(&fixture(prev, prop));
    assert!(
        message.contains("spec.provider.capabilities") && message.contains("compose"),
        "diagnostic should name the block path and the added capability; got: {message}"
    );
}

#[test]
fn plugin_major_provider_contract_narrowed_while_capability_added() {
    // The contract arm still fires on its own claims: dropping a contract
    // claim outranks a widened capability list.
    let prev = json!({"kind": "Plugin", "spec": {"provider": {
        "capabilities": ["helm"],
        "contract": {"inputKinds": {"sourced": true, "synthesized": true}},
    }}});
    let prop = json!({"kind": "Plugin", "spec": {"provider": {
        "capabilities": ["helm", "compose"],
        "contract": {"inputKinds": {"sourced": true, "synthesized": false}},
    }}});
    assert_eq!(classify_once(&fixture(prev, prop)), "MAJOR");
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
    let prev = json!({"kind": "Plugin", "spec": {"abiVersion": "v1beta1"}});
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
            "provider": {"capabilities": ["helm"]},
            "abiVersion": "v1beta1",
            "commands": [{"name": "build"}, {"name": "lint"}],
        },
    });
    let prop = json!({
        "kind": "Plugin",
        "metadata": {"description": "rewritten"},
        "spec": {
            "exports": [{"name": "render_node", "operates": "emit", "scope": "own"}],
            "provider": {"capabilities": ["helm"]},
            "abiVersion": "v1beta1",
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
