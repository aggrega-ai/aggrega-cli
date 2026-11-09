// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! Unit tests for the provides-declared-by-component rule.
//!
//! Every firing case is paired with the case that makes it meaningful. A
//! rule that reported every published key, or none, would look exactly
//! like correct behaviour in a positive-only suite — so a recipe whose
//! keys are all declared is asserted silent beside the one whose key is
//! not, and each shape that must NOT be read as an empty contract is
//! asserted silent beside a shape that genuinely is one.
//!
//! Coverage:
//!  - One finding per offending key, in a reproducible order.
//!  - A conforming pair, a recipe publishing nothing, and a recipe
//!    publishing a subset of what the component declares: all silent.
//!  - A component declaring no provides pillar, and one carrying no
//!    schema at all: both are empty contracts, so every published key
//!    fires.
//!  - No component resolved: silent, because the comparison is
//!    unavailable rather than empty. This is the case a reader is most
//!    likely to collapse into the one above it.
//!  - Every pillar shape the guest cannot enumerate — a reference, each
//!    composition keyword, and an escaped body authored as YAML text —
//!    is silent, and the escaped body authored as a mapping is read on.
//!  - Entity kinds other than Recipe.
//!  - The suppression flag.
//!  - The finding's severity is `warn`, so the rule never halts.

mod common;

use aggrega_pdk_rust::Policies;
use publishing_linter::rules::provides_declared_by_component as rule;
use serde_json::{json, Value};

/// A Recipe publishing the given provides bag.
fn recipe(provides: Value) -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1beta1",
        "kind": "Recipe",
        "metadata": {"name": "example", "version": "1.0.0"},
        "spec": {
            "capability": "helm",
            "implements": "core/ai-model/example:1.0.0",
            "provides": provides,
        },
    })
}

/// A Recipe with no provides block at all.
fn recipe_without_provides() -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1beta1",
        "kind": "Recipe",
        "metadata": {"name": "example", "version": "1.0.0"},
        "spec": {"capability": "helm", "implements": "core/ai-model/example:1.0.0"},
    })
}

/// A Component whose schema carries the given pillar block.
fn component(schema: Value) -> Value {
    json!({
        "apiVersion": "aggrega.ai/v1beta1",
        "kind": "Component",
        "metadata": {"name": "core/ai-model/example", "version": "1.0.0"},
        "spec": {"schema": schema},
    })
}

/// A Component carrying a provides pillar with the given body.
fn component_providing(provides: Value) -> Value {
    component(json!({"values": {"model": {"type": "string"}}, "provides": provides}))
}

/// The two declared keys the family contract in the standard catalog uses,
/// as a pillar body.
fn endpoint_and_model() -> Value {
    json!({
        "endpoint": {"type": "string", "required": true},
        "model": {"type": "string", "required": true},
    })
}

fn classify(kind: &str, proposed: Value, implemented: Option<Value>) -> Vec<String> {
    let input = common::fixture_implementing(kind, proposed, implemented, None);
    rule::classify(&input)
        .into_iter()
        .map(|f| {
            assert_eq!(f.rule_id, "provides-declared-by-component");
            assert_eq!(
                f.severity, "warn",
                "the rule is advisory; a non-warn severity would halt a pre-publish run"
            );
            f.message
        })
        .collect()
}

/// The published keys a run reported, recovered from the messages so the
/// assertion is about which key each finding names rather than only about
/// how many there were.
fn reported_keys(messages: &[String]) -> Vec<String> {
    messages
        .iter()
        .map(|m| {
            let opened = m
                .find("publishes the provides key \"")
                .expect("the message names the key it reports")
                + "publishes the provides key \"".len();
            let rest = &m[opened..];
            rest[..rest.find('"').expect("the key is quoted")].to_string()
        })
        .collect()
}

#[test]
fn a_published_key_the_component_does_not_declare_fires() {
    let messages = classify(
        "Recipe",
        recipe(json!({
            "endpoint": "{{ .infra.endpoint }}",
            "model": "{{ .values.model }}",
            "provider": "{{ .values.provider_name }}",
        })),
        Some(component_providing(endpoint_and_model())),
    );
    assert_eq!(reported_keys(&messages), vec!["provider".to_string()]);
    assert!(
        messages[0].contains("core/ai-model/example"),
        "the finding names the component whose contract it read: {}",
        messages[0]
    );
    assert!(
        messages[0].contains("\"endpoint\", \"model\""),
        "the finding lists the declared alternatives an author picks from: {}",
        messages[0]
    );
}

#[test]
fn every_offending_key_gets_its_own_finding_in_a_reproducible_order() {
    let messages = classify(
        "Recipe",
        recipe(json!({
            "zone": "eu",
            "endpoint": "{{ .infra.endpoint }}",
            "alpha": "a",
        })),
        Some(component_providing(endpoint_and_model())),
    );
    assert_eq!(
        reported_keys(&messages),
        vec!["alpha".to_string(), "zone".to_string()],
        "one finding per offending key, ordered by key so a run is reproducible"
    );
}

#[test]
fn a_conforming_pair_is_silent() {
    assert!(classify(
        "Recipe",
        recipe(json!({
            "endpoint": "{{ .infra.endpoint }}",
            "model": "{{ .values.model }}",
        })),
        Some(component_providing(endpoint_and_model())),
    )
    .is_empty());
}

#[test]
fn publishing_fewer_keys_than_the_component_declares_is_silent() {
    // The shortfall direction halts the build already; reporting it here
    // would be a second report of one fault.
    assert!(classify(
        "Recipe",
        recipe(json!({"endpoint": "{{ .infra.endpoint }}"})),
        Some(component_providing(endpoint_and_model())),
    )
    .is_empty());
}

#[test]
fn a_recipe_publishing_nothing_is_silent() {
    assert!(classify(
        "Recipe",
        recipe_without_provides(),
        Some(component_providing(endpoint_and_model())),
    )
    .is_empty());
    assert!(classify(
        "Recipe",
        recipe(json!({})),
        Some(component_providing(endpoint_and_model())),
    )
    .is_empty());
}

#[test]
fn a_component_declaring_no_provides_pillar_is_an_empty_contract() {
    let messages = classify(
        "Recipe",
        recipe(json!({"endpoint": "{{ .infra.endpoint }}"})),
        Some(component(json!({"values": {"model": {"type": "string"}}}))),
    );
    assert_eq!(reported_keys(&messages), vec!["endpoint".to_string()]);
    assert!(
        messages[0].contains("declares no provides pillar at all"),
        "an empty contract is said in words rather than as an empty list: {}",
        messages[0]
    );
}

#[test]
fn a_component_carrying_no_schema_is_an_empty_contract() {
    let bare = json!({
        "apiVersion": "aggrega.ai/v1beta1",
        "kind": "Component",
        "metadata": {"name": "core/ai-model/example", "version": "1.0.0"},
        "spec": {},
    });
    assert_eq!(
        reported_keys(&classify(
            "Recipe",
            recipe(json!({"endpoint": "{{ .infra.endpoint }}"})),
            Some(bare),
        )),
        vec!["endpoint".to_string()]
    );
}

#[test]
fn no_component_resolved_is_silent_rather_than_an_empty_contract() {
    // The pairing that matters: the same recipe fires against a component
    // that declares nothing and stays silent when none was resolved. A
    // reader that collapsed the two would report the whole bag whenever
    // the host could not resolve the reference.
    assert!(classify(
        "Recipe",
        recipe(json!({"endpoint": "{{ .infra.endpoint }}"})),
        None,
    )
    .is_empty());
}

/// The pillar shapes the guest cannot enumerate. Each must be silent: a
/// shape whose keys this reader cannot see is unknown, not empty.
#[test]
fn an_unenumerable_pillar_is_silent() {
    let unreadable = [
        // A reference: its target is merged in before the compiler reads
        // the entity, so nothing here can follow it.
        json!({"$ref": "BaseComponents#/schemas/ModelContract"}),
        // A reference carrying sibling properties is still a reference.
        json!({"$ref": "BaseComponents#/schemas/ModelContract",
               "properties": {"endpoint": {"type": "string"}}}),
        // Each composition keyword, because the branches carry the keys.
        json!({"allOf": [{"properties": {"endpoint": {"type": "string"}}}]}),
        json!({"anyOf": [{"properties": {"endpoint": {"type": "string"}}}]}),
        json!({"oneOf": [{"properties": {"endpoint": {"type": "string"}}}]}),
        // An escaped body authored as YAML text needs a parser this crate
        // does not carry.
        json!({"$jsonschema": "type: object\nproperties:\n  endpoint:\n    type: string\n"}),
    ];
    for pillar in unreadable {
        let messages = classify(
            "Recipe",
            recipe(json!({"provider": "{{ .values.provider_name }}"})),
            Some(component_providing(pillar.clone())),
        );
        assert!(
            messages.is_empty(),
            "an unenumerable provides pillar must report nothing; pillar {pillar} produced {messages:?}"
        );
    }
}

#[test]
fn an_escaped_body_authored_as_a_mapping_is_read_on() {
    // The counterpart to the text-authored body above: a mapping body is
    // authored JSON Schema this reader can open, so the contract is known.
    let pillar = json!({"$jsonschema": {
        "type": "object",
        "properties": {"endpoint": {"type": "string"}},
    }});
    assert!(classify(
        "Recipe",
        recipe(json!({"endpoint": "{{ .infra.endpoint }}"})),
        Some(component_providing(pillar.clone())),
    )
    .is_empty());
    assert_eq!(
        reported_keys(&classify(
            "Recipe",
            recipe(json!({"provider": "p"})),
            Some(component_providing(pillar)),
        )),
        vec!["provider".to_string()]
    );
}

#[test]
fn a_pillar_whose_property_name_is_a_compact_keyword_reads_as_a_leaf() {
    // Named here because it surprises: a pillar body whose only key is one
    // of the compact-form keywords classifies as a constraint leaf rather
    // than an object scope, so it exposes no property and the contract
    // reads empty. The compiler classifies it the same way, and the shared
    // parity fixture is what holds the two together — this test records
    // the consequence for the rule, not the classification itself.
    let messages = classify(
        "Recipe",
        recipe(json!({"endpoint": "{{ .infra.endpoint }}"})),
        Some(component_providing(json!({"format": {"type": "string"}}))),
    );
    assert_eq!(reported_keys(&messages), vec!["endpoint".to_string()]);
}

#[test]
fn a_kind_other_than_recipe_is_silent() {
    // A Component may carry a spec.provides-shaped block of its own; the
    // rule is about a Recipe against its Component and must not read one.
    for kind in ["Component", "Feature", "FeatureRecipe", "SchemaLibrary"] {
        let entity = json!({
            "apiVersion": "aggrega.ai/v1beta1",
            "kind": kind,
            "metadata": {"name": "core/ai-model/example", "version": "1.0.0"},
            "spec": {"provides": {"provider": "p"}},
        });
        assert!(
            classify(
                kind,
                entity,
                Some(component_providing(endpoint_and_model()))
            )
            .is_empty(),
            "the rule scopes to Recipe; kind {kind} produced a finding"
        );
    }
}

#[test]
fn the_suppression_flag_silences_the_rule() {
    let offending = || {
        common::fixture_implementing(
            "Recipe",
            recipe(json!({"provider": "{{ .values.provider_name }}"})),
            Some(component_providing(endpoint_and_model())),
            None,
        )
    };

    // The pairing: one input, two policy settings. Without the second half
    // a suppressed run and a fixture that never offended look the same.
    assert_eq!(rule::classify(&offending()).len(), 1);

    let mut suppressed = offending();
    suppressed.policies = Policies {
        allow_undeclared_recipe_provides: true,
        ..Default::default()
    };
    assert!(rule::classify(&suppressed).is_empty());
}

#[test]
fn the_rule_id_the_module_publishes_is_the_one_findings_carry() {
    let messages = classify(
        "Recipe",
        recipe(json!({"provider": "p"})),
        Some(component_providing(endpoint_and_model())),
    );
    assert_eq!(messages.len(), 1);
    assert_eq!(rule::RULE_ID, "provides-declared-by-component");
}
